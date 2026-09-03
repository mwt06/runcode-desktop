package desktop

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

// maxImageBytes bounds a single attached image so a huge file cannot blow up the
// request payload (and the provider's limits). 8 MiB comfortably covers screenshots
// and photos while rejecting accidental multi-hundred-MB files.
const maxImageBytes = 8 << 20

// PickImageAttachment opens a native image picker and returns the chosen path ("" if
// cancelled). The frontend adds it to the composer's attachment list; the bytes are
// read at send time by SendMessageWithImages.
func (a *App) PickImageAttachment() (string, error) {
	a.mu.Lock()
	dialog := a.dialog
	a.mu.Unlock()
	if dialog == nil {
		return "", wireError(errors.New("当前环境不支持文件选择"))
	}
	path, err := dialog.PickImage("选择图片附件")
	return path, wireError(err)
}

// SendMessageWithImages runs one user turn whose message carries the images at the
// given paths. It mirrors SendMessage (async, single in-flight turn) but reads the
// images up front and submits via the manager's image-turn entry point. With no
// paths it falls back to a plain text turn.
func (a *App) SendMessageWithImages(sessionID, text string, paths []string) error {
	if len(paths) == 0 {
		return a.SendMessage(sessionID, text)
	}
	if _, err := a.sessionIDOf(sessionID); err != nil {
		return wireError(err)
	}
	// The reads are small (each file is capped at maxImageBytes) so loading
	// synchronously here keeps the command's error surface unchanged: image
	// problems become a turn:error event (which the frontend renders as a
	// failed turn), not a command rejection — the pre-host contract.
	images, err := loadImages(paths)
	if err != nil {
		a.emitSessionEvent(EventTurnError, TurnError{Error: err.Error()})
		return nil
	}
	return wireError(a.sendUserTurn(sessionID, text, images, true))
}

// InjectMessageWithImages is InjectMessage for a message carrying image
// attachments: it injects into the in-flight turn as mid-turn steering, falling
// back to a fresh turn if none is running (returning startedTurn=true). With no
// paths it degrades to a plain text injection. Image read failures surface as a
// turn:error event, mirroring SendMessageWithImages.
func (a *App) InjectMessageWithImages(sessionID, text string, paths []string) (bool, error) {
	if len(paths) == 0 {
		return a.InjectMessage(sessionID, text)
	}
	images, err := loadImages(paths)
	if err != nil {
		a.emitSessionEvent(EventTurnError, TurnError{Error: err.Error()})
		return false, nil
	}
	return a.injectOrSend(sessionID, text, images, true)
}

// loadImages reads each path into a neutral llm.ImageSource, inferring the media
// type from the extension and rejecting oversized files.
func loadImages(paths []string) ([]llm.ImageSource, error) {
	images := make([]llm.ImageSource, 0, len(paths))
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("读取图片失败: %s", filepath.Base(p))
		}
		if info.Size() > maxImageBytes {
			return nil, fmt.Errorf("图片过大(>8MB): %s", filepath.Base(p))
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("读取图片失败: %s", filepath.Base(p))
		}
		images = append(images, llm.ImageSource{MediaType: imageMediaType(p), Data: data})
	}
	return images, nil
}

// imageMediaType maps a file extension to an image media type, defaulting to PNG.
func imageMediaType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "image/png"
	}
}

// ---- 粘贴进输入框的附件 -------------------------------------------------------
//
// 粘贴到 WebView 里的东西只有字节,没有路径:剪贴板里的截图本来就不是文件,而从资源
// 管理器复制来的文件,浏览器的安全模型也不把真实路径交给页面。所以由前端读出字节
// 交到这里落盘,之后就和"选一张图"没有区别——图片进多模态请求,其它类型交给模型
// 自己去 Read。
//
// 落在应用数据目录下有两重考虑。其一,appdirs 已经给本应用的数据目录常驻访问权,
// 模型读这些文件不会再弹一次"项目外授权";其二,它们不散落进用户的工作区,不会被
// 误当成项目文件提交上去。

// maxPastedBytes 是单个粘贴附件的上限。图片另有 maxImageBytes 那道更严的闸门,
// 由前端在加附件前按 MIME 判掉;这一道是给所有类型兜底的,防的是"把一段 700MB 的
// 视频粘进来"——那不只是慢,base64 之后还要在 WebView 的 IPC 里整个搬一遍,界面会
// 直接卡死。
const maxPastedBytes = 32 << 20

// pastedRetention 是粘贴附件的保留期。过期的目录在下一次粘贴时顺手清掉——附件的
// 用处只在当次对话,但不能发完就删:回合是异步的,而且用户可能连发两条都引用它。
const pastedRetention = 7 * 24 * time.Hour

// SavePastedFile 把前端从剪贴板读出的字节落盘,返回附件的绝对路径。
//
// name 只用来给文件起名,一律当**不可信输入**处理:目录成分、控制字符与 Windows 的
// 保留字符全部剔掉,清不出东西来就退回 attachment。每次粘贴各自落进一个带时间戳的
// 子目录,于是文件名可以保持原样(界面上附件芯片显示的就是它),同名文件也不会互相
// 覆盖。
func (a *App) SavePastedFile(name, dataB64 string) (string, error) {
	// base64 比原字节长 4/3。先按长度粗判一次,免得为一个必然要拒的巨大载荷先做一遍
	// 解码——那会凭空多占一份内存。
	if len(dataB64) > maxPastedBytes/3*4+16 {
		return "", wireError(fmt.Errorf("文件过大(上限 %d MB)", maxPastedBytes>>20))
	}
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return "", wireError(errors.New("附件数据无法解码"))
	}
	if len(data) == 0 {
		return "", wireError(errors.New("附件是空文件"))
	}
	if len(data) > maxPastedBytes {
		return "", wireError(fmt.Errorf("文件过大(上限 %d MB)", maxPastedBytes>>20))
	}
	root, err := pastedRoot()
	if err != nil {
		return "", wireError(err)
	}
	prunePasted(root)
	dir, err := newPastedDir(root)
	if err != nil {
		return "", wireError(fmt.Errorf("创建附件目录失败: %w", err))
	}
	path := filepath.Join(dir, safeAttachmentName(name))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", wireError(fmt.Errorf("保存附件失败: %w", err))
	}
	return path, nil
}

// pastedRoot 是粘贴附件的落盘根目录(见上面这一节的说明)。
func pastedRoot() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("定位配置目录失败: %w", err)
	}
	return filepath.Join(dir, "runcode", "pasted"), nil
}

// newPastedDir 为本次粘贴建一个唯一的子目录。
//
// 时间戳只精确到秒,连着粘两张图会撞名,所以后面跟一段随机后缀。用 crypto/rand 不是
// 为了保密,而是省掉自己维护计数器/重试:撞名的代价是覆盖掉用户上一次粘的东西。
func newPastedDir(root string) (string, error) {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	dir := filepath.Join(root, time.Now().Format("20060102-150405")+"-"+hex.EncodeToString(suffix[:]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// safeAttachmentName 把外面传进来的名字压成一个安全的**纯文件名**。
//
// 路径分隔符两种都要自己列:filepath 只认当前平台的那一种,而这个名字是从另一端
// 传过来的,Windows 上照样可能收到 "a/b.png"。剔干净之后不可能再含目录成分,所以
// 拼进落盘目录不会跑到别处去。
func safeAttachmentName(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"/\|?*`, r) {
			return -1
		}
		return r
	}, name)
	// 首尾的空格和点在 Windows 上会被静默吃掉,于是 "..\" 这种残留可能变成 ".."。
	cleaned = strings.Trim(strings.TrimSpace(cleaned), ".")
	if cleaned == "" {
		return "attachment"
	}
	return truncateName(cleaned, 80)
}

// truncateName 把文件名截短到 maxBytes 以内,保留扩展名。
//
// 按 rune 截而不是按字节:中文名一刀切下去会留下半个字符,那既不好看,在某些文件
// 系统上还直接是非法名字。
func truncateName(name string, maxBytes int) string {
	if len(name) <= maxBytes {
		return name
	}
	ext := filepath.Ext(name)
	// 超过这个长度的"扩展名"不是扩展名,是名字里正好有个点。
	if len(ext) > 16 {
		ext = ""
	}
	budget := maxBytes - len(ext)
	var b strings.Builder
	for _, r := range strings.TrimSuffix(name, ext) {
		if b.Len()+utf8.RuneLen(r) > budget {
			break
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "attachment" + ext
	}
	return b.String() + ext
}

// prunePasted 删掉过了保留期的粘贴目录。清不掉不算错——那只是占点地方,不该让这次
// 粘贴本身失败。
func prunePasted(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-pastedRetention)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			debugLog("pasted: 清理过期附件 %s: %v", e.Name(), err)
		}
	}
}
