package desktop

// 带进度与校验的大文件下载。
//
// 两个功能共用它：技能市场装技能包（skillmarket.go）、版本更新下安装包
// （update.go）。抽出来不只是为了少写一遍——这两处都得同时满足三件事：真实字节
// 进度、边下边算 sha256、下完能立刻比对。任何一处自己写一遍，迟早有一处漏掉校验。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// progressInterval 是两次进度回报的最小间隔。低于这个数只是在刷事件总线——
// 人眼看不出 60ms 和 120ms 的区别，而每一次都是一趟跨进程的事件投递。
const progressInterval = 120 * time.Millisecond

// progressWriter 数写过去多少字节，顺便按时间节流地往外报。
//
// 节流按**时间**不按字节数：按字节数（比如每 128KiB 报一次）在慢网络上可能几秒
// 都不报一次，快网络上又一秒几百次；按时间两头都稳定。
type progressWriter struct {
	total    int64
	received int64
	last     time.Time
	report   func(received, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.received += int64(len(b))
	if now := time.Now(); now.Sub(p.last) >= progressInterval {
		p.last = now
		p.report(p.received, p.total)
	}
	return len(b), nil
}

// downloadHTTP 是下载大文件专用的客户端。
//
// **不能复用 passportHTTP()**：那个带 60 秒的 Client.Timeout，而 Client.Timeout 管的
// 是「整趟请求，包括把响应体读完」。拿它下一个几十 MB 的包，只要总时长超过 60 秒就
// 会在半路被掐断——网络越慢越必然失败，而报出来的样子像是服务端断的连接，查起来
// 会一路查到服务端去。（技能包此前正是走的那个客户端，这条链路上的大包一直有这个
// 隐患，抽出本文件时一并修掉。）
//
// 这里只给「连上/握手/等第一个响应头」设上限——它们卡住是真的连不通，该早点失败；
// 整趟的时长上限交给调用方的 ctx，那是按"下多大的东西"决定的，不该写死在这里。
func downloadHTTP() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

// fetchToFile 把 rawURL 下到 w，同时算 sha256，返回十六进制摘要与字节数。
//
// label 是报错里的东西名（"技能包"、"安装包"）——同一句"下载失败"，说清楚下的是
// 什么，用户才知道该去点哪个按钮重试。maxBytes 是字节数上限，超了就算失败：这些包都
// 是从网络来的，没有上限意味着对方能把磁盘写满。onProgress 按 progressInterval
// 节流地调用；total 为 0 表示服务端没给 Content-Length（此时进度条只能画不确定态）。
//
// 整趟的时长与取消都由 ctx 决定：更新下载要能被用户按「取消」中止，而取消一次
// http.Client.Do 的唯一办法就是取消它的 ctx。
func fetchToFile(ctx context.Context, rawURL, label string, w *os.File, maxBytes int64, onProgress func(received, total int64)) (string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := downloadHTTP().Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("下载%s失败: %w", label, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("下载%s返回 %d", label, resp.StatusCode)
	}
	h := sha256.New()
	// ContentLength 为 -1 表示分块传输、长度未知；对外统一用 0 表示"不知道"，
	// 免得前端拿 -1 去算百分比。
	total := resp.ContentLength
	if total < 0 {
		total = 0
	}
	pw := &progressWriter{total: total, report: onProgress}
	n, err := io.Copy(io.MultiWriter(w, h, pw), io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", 0, fmt.Errorf("下载%s失败: %w", label, err)
	}
	// 收尾再报一次：节流会吞掉最后那一小段，不补的话进度条永远停在 97% 这种地方。
	onProgress(n, total)
	if n > maxBytes {
		return "", 0, fmt.Errorf("%s超过 %d MiB，拒绝继续", label, maxBytes>>20)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
