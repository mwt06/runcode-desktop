package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	appwire "github.com/wt68/runcode/internal/protocol"
)

// 生成结果落盘（设计第 8 节）：只收 PNG/JPEG/WebP，按真实文件头定格式与扩展名，
// 核对字节数与像素上限；输出到工作区 生成图片/<generationId>/image.<ext>，独立目录
// 排他创建、不覆盖现有文件，失败清理。URL 下载用独立客户端：不带任何凭据头，只认
// http/https，拒绝本机/私网目标与任何跳转。

var (
	imageGenerationMaxImageBytes int64 = 20 << 20
	imageDownloadTimeout               = 60 * time.Second
	imageSaveTimeout                   = 90 * time.Second
	// imageDownloadAddrAllowed decides, per connected address, whether a
	// download may proceed. Tests swap it to use a loopback test server.
	imageDownloadAddrAllowed = publicImageAddr
)

const imageGenerationDir = "生成图片"

var errImageGenerationSave = errors.New("已收到生成结果，但下载或保存图片失败")

func publicImageAddr(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	// 运营商级 NAT 共享段也不是公开图床。
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 {
		return false
	}
	return true
}

func imageDownloadClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 15 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil || !imageDownloadAddrAllowed(net.ParseIP(host)) {
				return errors.New("图片地址指向本机或内网，已拒绝")
			}
			return nil
		},
	}
	transport := &http.Transport{
		// 只直连：代理会让上面的目标检查看不到真实地址。
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableKeepAlives:     true,
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       imageDownloadTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("图片地址发生跳转，已拒绝") },
	}
}

func downloadGeneratedImage(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, errors.New("图片地址无效")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !imageDownloadAddrAllowed(ip) {
		return nil, errors.New("图片地址指向本机或内网，已拒绝")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("图片地址无效")
	}
	resp, err := imageDownloadClient().Do(req)
	if err != nil {
		return nil, errors.New("下载图片失败")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载图片失败（HTTP %d）", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, imageGenerationMaxImageBytes+1))
	if err != nil {
		return nil, errors.New("下载图片中断")
	}
	if int64(len(data)) > imageGenerationMaxImageBytes {
		return nil, errors.New("图片超过大小上限")
	}
	return data, nil
}

func decodeGeneratedBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if int64(len(s)) > imageGenerationMaxImageBytes/3*4+8 {
		return nil, errors.New("图片超过大小上限")
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, errors.New("图片数据无效")
	}
	if int64(len(data)) > imageGenerationMaxImageBytes {
		return nil, errors.New("图片超过大小上限")
	}
	return data, nil
}

// detectGeneratedImage 按真实文件头识别格式，不信文件名或声明的 MIME。
func detectGeneratedImage(data []byte) (ext, mime string, width, height int, err error) {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		cfg, cerr := png.DecodeConfig(bytes.NewReader(data))
		if cerr != nil {
			return "", "", 0, 0, errors.New("PNG 图片无效")
		}
		ext, mime, width, height = "png", "image/png", cfg.Width, cfg.Height
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		cfg, cerr := jpeg.DecodeConfig(bytes.NewReader(data))
		if cerr != nil {
			return "", "", 0, 0, errors.New("JPEG 图片无效")
		}
		ext, mime, width, height = "jpg", "image/jpeg", cfg.Width, cfg.Height
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		w, h, werr := webpSize(data)
		if werr != nil {
			return "", "", 0, 0, werr
		}
		ext, mime, width, height = "webp", "image/webp", w, h
	default:
		return "", "", 0, 0, errors.New("不是受支持的图片格式（仅 PNG/JPEG/WebP）")
	}
	if width <= 0 || height <= 0 || int64(width)*int64(height) > imageGenerationMaxPixels {
		return "", "", 0, 0, errors.New("图片像素超过上限")
	}
	return ext, mime, width, height, nil
}

// webpSize 从首个 VP8／VP8L／VP8X 块读取画布尺寸。
func webpSize(data []byte) (int, int, error) {
	bad := errors.New("WebP 图片无效")
	if len(data) < 20 {
		return 0, 0, bad
	}
	chunk, payload := string(data[12:16]), data[20:]
	switch chunk {
	case "VP8X":
		if len(payload) < 10 {
			return 0, 0, bad
		}
		w := 1 + (int(payload[4]) | int(payload[5])<<8 | int(payload[6])<<16)
		h := 1 + (int(payload[7]) | int(payload[8])<<8 | int(payload[9])<<16)
		return w, h, nil
	case "VP8L":
		if len(payload) < 5 || payload[0] != 0x2f {
			return 0, 0, bad
		}
		bits := binary.LittleEndian.Uint32(payload[1:5])
		return int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1, nil
	case "VP8 ":
		if len(payload) < 10 || payload[3] != 0x9d || payload[4] != 0x01 || payload[5] != 0x2a {
			return 0, 0, bad
		}
		return int(binary.LittleEndian.Uint16(payload[6:8]) & 0x3fff), int(binary.LittleEndian.Uint16(payload[8:10]) & 0x3fff), nil
	}
	return 0, 0, bad
}

// saveGeneratedImage 校验一张生成结果并写入工作区新目录；从不覆盖已有文件。
func saveGeneratedImage(ctx context.Context, workspace, generationID string, item imageGenerationWireImage) (appwire.ImageGenerationArtifact, error) {
	if !validImageGenerationID(generationID) || workspace == "" {
		return appwire.ImageGenerationArtifact{}, errImageGenerationSave
	}
	// POST 已经成功时结果可能已计费；停止当前回合不能取消 b64 解码、URL 下载和落盘。
	// 保存独立限时，只保留“结果已到达”这一事实。
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), imageSaveTimeout)
	defer cancel()
	ctx = saveCtx
	var data []byte
	var err error
	switch {
	case item.B64JSON != "":
		data, err = decodeGeneratedBase64(item.B64JSON)
	case item.URL != "":
		data, err = downloadGeneratedImage(ctx, item.URL)
	default:
		err = errors.New("生成结果里没有图片")
	}
	if err != nil {
		return appwire.ImageGenerationArtifact{}, fmt.Errorf("%w：%w", errImageGenerationSave, err)
	}
	ext, mime, width, height, err := detectGeneratedImage(data)
	if err != nil {
		return appwire.ImageGenerationArtifact{}, fmt.Errorf("%w：%w", errImageGenerationSave, err)
	}
	if err := ctx.Err(); err != nil {
		return appwire.ImageGenerationArtifact{}, fmt.Errorf("%w：已停止", errImageGenerationSave)
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return appwire.ImageGenerationArtifact{}, errImageGenerationSave
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(imageGenerationDir, 0o755); err != nil {
		return appwire.ImageGenerationArtifact{}, errImageGenerationSave
	}
	dir := filepath.Join(imageGenerationDir, generationID)
	// A fresh directory per generation: an existing one means this id was used.
	if err := root.Mkdir(dir, 0o755); err != nil {
		return appwire.ImageGenerationArtifact{}, errImageGenerationSave
	}
	fileName := generatedImageFileName(generationID, ext)
	rel := filepath.Join(dir, fileName)
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		_ = root.Remove(dir)
		return appwire.ImageGenerationArtifact{}, errImageGenerationSave
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if errors.Join(writeErr, syncErr, closeErr) != nil {
		_ = root.Remove(rel)
		_ = root.Remove(dir)
		return appwire.ImageGenerationArtifact{}, errImageGenerationSave
	}
	return appwire.ImageGenerationArtifact{
		Path:     path.Join(imageGenerationDir, generationID, fileName),
		MIMEType: mime,
		Bytes:    int64(len(data)),
		Width:    width,
		Height:   height,
	}, nil
}

// generatedImageFileName 用上级 generation 目录的 UUID 段命名，避免多图都叫 image.jpg。
// 目录 gen_<uuid> → image_<uuid>.<ext>；其它合法 id 则整段拼进文件名。
func generatedImageFileName(generationID, ext string) string {
	id := strings.TrimPrefix(generationID, "gen_")
	if id == "" {
		id = generationID
	}
	return "image_" + id + "." + ext
}
