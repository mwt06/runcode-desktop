package recorder

// 网关 WebSocket 客户端：一条音轨一个实例。
//
// 协议（gateway/app.py）：首帧发一条 JSON 配置，之后一路发二进制 PCM
// (16k/mono/int16)，下行是 JSON 事件。控制指令也走文本帧：
// {"cmd":"flush"} 立即出结果但会话继续，{"cmd":"stop"} 正常收尾。
//
// 三条不能动的语义，每条都是服务端既有行为决定的：
//
//  1. **断线期间的音频直接丢，不缓存补发。** 语音是实时流，补发只会让延迟越积
//     越大。丢掉的时长记在 Stats.GapMS 里，会后用本地 WAV 走补转写补回来——
//     本地那份永远是完整的，这是 WAVSink 存在的意义。
//  2. **重连要复用同一个 track id。** 服务端保留 TrackSession 120 秒，同 id 重连
//     能续上分段编号和时间轴；换 id 会让时间轴从头开始，界面上表现为一场会里
//     出现两段互不相干的转写。
//  3. **只有主动发 stop 才算正常结束。** 意外断开时服务端不 flush——人可能话说
//     到一半断网，此时 flush 会把句子截断，还会重置分段编号。

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// LiveSeg 是块模式下那条持续增长的实时行的段号（服务端 engine/track.py 的
// LIVE_SEG）。它不是一个真实段落，块结束时会被确认文本整体替换。
const LiveSeg = -1

// 修订号。前端按 (track, seg) 定位一句话，按 rev 决定是否覆盖。
const (
	RevPartial = 0 // 实时行，随时会被覆盖
	RevFinal   = 1 // 块处理后的确认文本
	RevL2      = 2 // 词典/ITN 纠错
	RevL3      = 3 // LLM 纠错
)

const (
	// 重连退避：1s→2s→4s…封顶 15s，与 web/index.html 那版一致，也在服务端
	// 120 秒的会话保留窗口之内。
	reconnectBase = time.Second
	reconnectMax  = 15 * time.Second

	// 写超时。客户端每 600 ms 就要写一帧，写不动说明链路已经死了（典型是笔记本
	// 休眠唤醒或换了网络后的半开连接）。靠它比靠 TCP 自己超时快得多，UI 上
	// 「离线录制中」才能及时亮起来。
	writeTimeout = 10 * time.Second

	// 发送队列深度。32 帧 ≈ 19 秒音频，够扛住短暂的网络抖动；再深就没意义了，
	// 因为实时流积压到那个程度本来就该丢。
	sendQueueDepth = 32
)

// UplinkState 是上行链路的状态，直接映射到界面上的提示。
type UplinkState string

// 上行链路的四个状态。UplinkOffline 是其中唯一需要用户看见的：界面上显示
// 「离线录制中」——录音没有中断，只是暂时没在上传，结束后走补转写补齐。
const (
	UplinkConnecting UplinkState = "connecting"
	UplinkConnected  UplinkState = "connected"
	UplinkOffline    UplinkState = "offline"
	UplinkStopped    UplinkState = "stopped"
)

// Event 是网关下行的一条事件。
//
// 字段是 gateway/ 与 engine/track.py 实际发出来的并集；用一个结构体而不是
// map[string]any，是为了让「哪些字段存在」这件事在 Go 侧可读、可改、能被编译器
// 检查——服务端加字段时这里不改也不会崩，只是读不到新字段。
type Event struct {
	Type  string `json:"type"`
	Track string `json:"track,omitempty"`
	Room  string `json:"room,omitempty"`

	Seg  int    `json:"seg,omitempty"`
	Rev  int    `json:"rev,omitempty"`
	Text string `json:"text,omitempty"`
	BT   int64  `json:"bt,omitempty"` // 房间时间轴上的起止毫秒
	ET   int64  `json:"et,omitempty"`

	Spk    string  `json:"spk,omitempty"`     // 盲聚类编号 spk1/spk2
	Name   string  `json:"name,omitempty"`    // 声纹库认出的姓名
	UserID string  `json:"user_id,omitempty"` // 声纹库 id
	Conf   float64 `json:"conf,omitempty"`

	Segs []int `json:"segs,omitempty"` // drop：要撤掉的段号

	Silence int `json:"silence,omitempty"` // live_status：已静音秒数
	Need    int `json:"need,omitempty"`    // live_status：还需静音多少秒才出结果

	Stage    string `json:"stage,omitempty"`     // provisional | refined
	QueuePos int    `json:"queue_pos,omitempty"` // 精修排队位次（单卡下必须有）
}

// UplinkStats 是一条音轨的上行统计。
type UplinkStats struct {
	Sent       int64 // 真正写进 socket 的帧数（入队但没发出去的不算）
	Dropped    int64 // 断线或队列满而丢弃的帧数
	GapMS      int64 // 丢弃音频的总时长，会后据此决定要不要补转写
	Reconnects int64
}

// UplinkConfig 描述一条上行链路。
type UplinkConfig struct {
	URL   string // ws://host/ws 或 wss://host/ws
	Room  string
	Track Source
	Name  string // 说话人显示名，可空
	Auth  string // passport JWT

	// Diarize 只在回环轨打开。麦克风轨天然单人，跑聚类是白费 GPU。
	Diarize bool
	// Lang 对应界面上的「自动识别∨」。
	Lang string
	// OffsetMS 是本轨相对房间起点的偏移。两轨同时开始时为 0。
	OffsetMS int64

	// OnEvent 在读 goroutine 上被调用，不要在里面做阻塞的事。
	OnEvent func(Event)
	// OnState 报告链路状态变化，驱动界面上的「离线录制中」。
	OnState func(UplinkState)

	// Dialer 可注入，测试用。零值走 websocket.DefaultDialer。
	Dialer *websocket.Dialer

	// ReconnectBase/ReconnectMax 是重连退避的首个间隔与上限，零值取默认
	// （1s / 15s）。留成可配置一是给部署调，二是测试要靠它把等待压到毫秒级。
	ReconnectBase time.Duration
	ReconnectMax  time.Duration
}

// Uplink 是一条音轨到网关的上行链路，自带断线重连。
type Uplink struct {
	cfg                     UplinkConfig
	dialer                  *websocket.Dialer
	backoffBase, backoffMax time.Duration

	send    chan []int16
	ctrl    chan string   // 控制指令：flush / stop
	stopped chan struct{} // 正常收尾完成

	cancel context.CancelFunc
	done   chan struct{}

	stats struct {
		sent       atomic.Int64
		dropped    atomic.Int64
		gapMS      atomic.Int64
		reconnects atomic.Int64
	}

	stateMu sync.Mutex
	state   UplinkState

	stopOnce sync.Once
}

// NewUplink 建链路并立即开始连接。调用方应最终调用 Stop 或 Abort。
func NewUplink(cfg UplinkConfig) (*Uplink, error) {
	if cfg.URL == "" {
		return nil, errors.New("网关地址为空")
	}
	if cfg.Room == "" || cfg.Track == "" {
		return nil, errors.New("room 与 track 不能为空")
	}
	d := cfg.Dialer
	if d == nil {
		d = websocket.DefaultDialer
	}

	base, maxWait := cfg.ReconnectBase, cfg.ReconnectMax
	if base <= 0 {
		base = reconnectBase
	}
	if maxWait <= 0 {
		maxWait = reconnectMax
	}

	ctx, cancel := context.WithCancel(context.Background())
	u := &Uplink{
		cfg:         cfg,
		dialer:      d,
		backoffBase: base,
		backoffMax:  maxWait,
		send:        make(chan []int16, sendQueueDepth),
		ctrl:        make(chan string, 4),
		stopped:     make(chan struct{}),
		cancel:      cancel,
		done:        make(chan struct{}),
		state:       UplinkConnecting,
	}
	go u.run(ctx)
	return u, nil
}

// Send 把一帧 PCM 排队上行。
//
// 永不阻塞：断线或队列满时直接丢弃并计入 GapMS。调用方在音频回调链上，
// 阻塞它会直接导致丢音，而且丢的是**本地录音**——那比丢上行严重得多。
func (u *Uplink) Send(pcm []int16) {
	// 拷贝：入参来自采集层的复用缓冲，回调返回即失效。
	buf := make([]int16, len(pcm))
	copy(buf, pcm)

	select {
	case u.send <- buf:
	default:
		u.stats.dropped.Add(1)
		u.stats.gapMS.Add(int64(len(pcm)) * 1000 / TargetSampleRate)
	}
}

// Flush 请求服务端立即出结果，会话继续。对应界面上的「立即出结果」。
func (u *Uplink) Flush() {
	select {
	case u.ctrl <- "flush":
	default: // 队列满说明已经有一个在路上，重复请求没有意义
	}
}

// Stop 正常收尾：发 stop 让服务端 flush 最后一块，等它把尾巴发完再断开。
//
// ctx 到期就强断——收尾等待不能无限期挂着用户的「结束录音」按钮。
func (u *Uplink) Stop(ctx context.Context) error {
	u.stopOnce.Do(func() {
		select {
		case u.ctrl <- "stop":
		default:
		}
	})
	select {
	case <-u.stopped:
		u.cancel()
		<-u.done
		return nil
	case <-ctx.Done():
		u.cancel()
		<-u.done
		return fmt.Errorf("等待服务端收尾超时: %w", ctx.Err())
	}
}

// Abort 立即断开，不发 stop。用于用户放弃本次录音。
//
// 注意这会让服务端把本轨当作**意外断开**处理：保留会话 120 秒等重连，不 flush。
// 那正是我们想要的——放弃的录音不该在服务端留下半句话。
func (u *Uplink) Abort() {
	u.cancel()
	<-u.done
}

// Stats 返回当前统计快照。
func (u *Uplink) Stats() UplinkStats {
	return UplinkStats{
		Sent:       u.stats.sent.Load(),
		Dropped:    u.stats.dropped.Load(),
		GapMS:      u.stats.gapMS.Load(),
		Reconnects: u.stats.reconnects.Load(),
	}
}

// State 返回当前链路状态。
func (u *Uplink) State() UplinkState {
	u.stateMu.Lock()
	defer u.stateMu.Unlock()
	return u.state
}

func (u *Uplink) setState(s UplinkState) {
	u.stateMu.Lock()
	changed := u.state != s
	u.state = s
	u.stateMu.Unlock()
	if changed && u.cfg.OnState != nil {
		u.cfg.OnState(s)
	}
}

// run 是连接生命周期的主循环：连上 → 跑到断 → 退避 → 再连。
func (u *Uplink) run(ctx context.Context) {
	defer close(u.done)
	defer u.setState(UplinkStopped)

	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}

		conn, err := u.dial(ctx)
		if err != nil {
			attempt++
			if !u.waitBackoff(ctx, attempt) {
				return
			}
			continue
		}

		attempt = 0
		// 丢掉退避期间攒在队列里的陈音频。不这样做的话，一次 15 秒的断线会在
		// 重连瞬间把整队积压灌给服务端——那正是「补发只会让延迟越积越大」说的
		// 情况，而且服务端会按收到的顺序当成刚说的话去识别，时间轴直接错位。
		u.drainStale()
		u.setState(UplinkConnected)
		clean := u.pump(ctx, conn)
		_ = conn.Close()

		if clean || ctx.Err() != nil {
			return
		}
		// 意外断开：进入离线态，界面据此提示「离线录制中」。
		u.setState(UplinkOffline)
		u.stats.reconnects.Add(1)
		attempt++
		if !u.waitBackoff(ctx, attempt) {
			return
		}
	}
}

// drainStale 清空发送队列，把丢掉的时长记进 GapMS。
//
// 队列里的东西一律按「已丢弃」计，会后据 GapMS 判断要不要拿本地 WAV 走补转写。
func (u *Uplink) drainStale() {
	for {
		select {
		case pcm := <-u.send:
			u.stats.dropped.Add(1)
			u.stats.gapMS.Add(int64(len(pcm)) * 1000 / TargetSampleRate)
		default:
			return
		}
	}
}

// waitBackoff 退避等待，返回 false 表示已被取消。
func (u *Uplink) waitBackoff(ctx context.Context, attempt int) bool {
	d := time.Duration(math.Min(
		float64(u.backoffBase)*math.Pow(2, float64(attempt-1)),
		float64(u.backoffMax),
	))
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// dial 连上网关并发出首帧配置。
func (u *Uplink) dial(ctx context.Context) (*websocket.Conn, error) {
	u.setState(UplinkConnecting)

	header := http.Header{}
	if u.cfg.Auth != "" {
		// 同时放 header 和首帧：header 让网关能在升级阶段就拒掉未授权连接，
		// 首帧那份是给应用层用的。
		header.Set("Authorization", "Bearer "+u.cfg.Auth)
	}

	conn, resp, err := u.dialer.DialContext(ctx, u.cfg.URL, header)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("连接网关: %w", err)
	}

	first := map[string]any{
		"room":      u.cfg.Room,
		"track":     string(u.cfg.Track),
		"role":      "publisher",
		"diarize":   u.cfg.Diarize,
		"lang":      u.cfg.Lang,
		"offset_ms": u.cfg.OffsetMS,
	}
	if u.cfg.Name != "" {
		first["name"] = u.cfg.Name
	}
	if u.cfg.Auth != "" {
		first["auth"] = u.cfg.Auth
	}
	payload, err := json.Marshal(first)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("编码首帧配置: %w", err)
	}
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("发送首帧配置: %w", err)
	}
	return conn, nil
}

// pump 在一条已建立的连接上收发，直到连接断开或收到停止指令。
// 返回 true 表示是正常收尾（已发过 stop 并等到服务端关闭）。
func (u *Uplink) pump(ctx context.Context, conn *websocket.Conn) (clean bool) {
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var ev Event
			if err := json.Unmarshal(data, &ev); err != nil {
				continue // 服务端加了我们不认识的东西，忽略比断开好
			}
			if u.cfg.OnEvent != nil {
				u.cfg.OnEvent(ev)
			}
		}
	}()

	sentStop := false
	for {
		select {
		case <-ctx.Done():
			return false

		case <-readDone:
			// 服务端关掉了连接。发过 stop 就是正常收尾，否则是意外断开。
			if sentStop {
				close(u.stopped)
				return true
			}
			return false

		case cmd := <-u.ctrl:
			if err := u.writeText(conn, `{"cmd":"`+cmd+`"}`); err != nil {
				return false
			}
			if cmd == "stop" {
				sentStop = true
				// 不立刻断开：服务端要 flush 最后一块并把 final 发回来。
				// 等它关连接（走上面的 readDone 分支）。
			}

		case pcm := <-u.send:
			if sentStop {
				continue // 已经收尾，不再送新音频
			}
			if err := u.writePCM(conn, pcm); err != nil {
				return false
			}
			u.stats.sent.Add(1)
		}
	}
}

func (u *Uplink) writeText(conn *websocket.Conn, s string) error {
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return conn.WriteMessage(websocket.TextMessage, []byte(s))
}

func (u *Uplink) writePCM(conn *websocket.Conn, pcm []int16) error {
	// 小端序：服务端是 np.frombuffer(data, dtype=np.int16)，numpy 用本机字节序，
	// 目标平台都是小端。
	b := make([]byte, len(pcm)*2)
	for i, s := range pcm {
		//nolint:gosec // int16→uint16 是等宽位重解释，无信息损失
		binary.LittleEndian.PutUint16(b[i*2:], uint16(s))
	}
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return conn.WriteMessage(websocket.BinaryMessage, b)
}
