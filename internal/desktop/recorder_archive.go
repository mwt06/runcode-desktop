package desktop

// 录音在工作区留一份。
//
// 录音本体落在应用自管的数据目录（Windows 上是 LocalAppData——Roaming 会把一小时
// 双轨 230MB 的音频跟着域账号同步走，见 defaultRecorderRoot）。那个目录用户找不到，
// 也不该让他去找。可是一场会的产出是成套的：纪要 .md 写在工作区，音频和转写却在另一
// 个看不见的地方，想整场归档、发给别人、或者半年后回听，就得去翻应用目录。
//
// 所以停止录音时把整场（WAV 轨 + 转写 + meta.json）复制一份到工作区，与纪要并排：
//
//	会议纪要-20260901-项目周会.md      ← 纪要（前端 minutes.ts 写）
//	录音-20260901-项目周会/            ← 这里复制的这一份
//
// 是**复制**不是移动：应用数据目录那份是补转写唯一的依据（NeedsBackfill 时要拿本地
// WAV 重跑），也是录音列表、「在文件夹中显示」、删除管理认的那一份。工作区这一份从此
// 归用户——在录音列表里删掉一场**不会**动它。

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wt68/runcode/internal/recorder"
)

const (
	// archivePrefix 是工作区里那个目录的前缀。与纪要文件名（前端 minutes.ts 的
	// minutesFileName）一样把日期放在标题前面：一个工作区里多场会才排得整齐，
	// 而且一眼看得出「录音-20260901-周会/」和「会议纪要-20260901-周会.md」是一对。
	archivePrefix = "录音-"
	// maxArchiveTitleRunes 与 minutes.ts 的 slice(0, 40) 对齐。按 rune 截而不是按
	// 字节：中文标题按字节截会把一个字劈成两半，落到磁盘上是乱码。
	maxArchiveTitleRunes = 40
	// fallbackArchiveTitle 是标题为空（或洗完只剩空白）时的名字，与 minutes.ts 的
	// 那个默认标题一致。
	fallbackArchiveTitle = "录音纪要"
)

// illegalNameChars 是 Windows 文件名里不允许的字符。标题是用户随手填的，得洗一遍
// 才能当目录名用。
const illegalNameChars = `\/:*?"<>|`

// recordingArchiveName 是这场录音在工作区里的目录名。
func recordingArchiveName(m recorder.SessionMeta) string {
	started := m.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	// 取本地时区：前端那边的 minutesFileName 用的是浏览器本地时间，两个名字上的
	// 日期必须是同一天，否则并排放的一对看着像两场会。
	return archivePrefix + started.Local().Format("20060102") + "-" + sanitizeArchiveTitle(m.Title)
}

// sanitizeArchiveTitle 把用户填的标题洗成一个能当目录名用的串。
func sanitizeArchiveTitle(title string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(illegalNameChars, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(title))

	if runes := []rune(cleaned); len(runes) > maxArchiveTitleRunes {
		cleaned = string(runes[:maxArchiveTitleRunes])
	}
	// Windows 不接受以点或空格结尾的目录名——截断正好切在那儿是很常见的。
	cleaned = strings.TrimRight(strings.TrimSpace(cleaned), ". ")
	if cleaned == "" {
		return fallbackArchiveTitle
	}
	return cleaned
}

// archiveRecording 把 srcDir 这场录音整份复制到工作区，返回复制到了哪里。
//
// 没有打开工作区时返回 ("", nil)：那不是失败，只是没地方可放。
func (a *App) archiveRecording(srcDir string, m recorder.SessionMeta) (string, error) {
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	if ws == "" || srcDir == "" {
		return "", nil
	}
	dest, err := freeArchiveDir(ws, recordingArchiveName(m), m.ID)
	if err != nil {
		return "", err
	}
	// 先复制到同级的临时目录再整体改名，理由与内置技能的 writeSkillTree 一样：
	// 复制几百 MB 中途失败（磁盘满、应用被关掉），工作区里不该留下一个半截的录音
	// 目录——那种目录看着是有的，回听到一半没了，比干脆没有难查得多。
	staging, err := os.MkdirTemp(ws, ".录音-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if err := copyTreeStreaming(srcDir, staging); err != nil {
		return "", err
	}
	if err := os.Rename(staging, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// freeArchiveDir 挑一个还没被占用的目录名。同一天、同一个标题的两场会不能互相覆盖，
// 撞上就缀录音 id（它本身唯一）。
func freeArchiveDir(ws, name, id string) (string, error) {
	first := filepath.Join(ws, name)
	if _, err := os.Stat(first); errors.Is(err, fs.ErrNotExist) {
		return first, nil
	}
	withID := filepath.Join(ws, name+"-"+id)
	if _, err := os.Stat(withID); errors.Is(err, fs.ErrNotExist) {
		return withID, nil
	}
	// 连带 id 的名字都在，说明这场录音已经存过一份了。
	return "", fmt.Errorf("工作区里已经有 %s", filepath.Base(withID))
}

// copyTreeStreaming 递归复制一棵目录树，逐块搬运而不是把整个文件读进内存。
//
// 不复用 skills.go 的 copySkillDir：那边 os.ReadFile 整份读进来，对一个 SKILL.md
// 完全没问题，对一条几百 MB 的 WAV 就是几百 MB 的常驻内存。
//
// 只复制普通文件与目录；符号链接与特殊文件跳过，免得一条指向别处的链接把复制引到
// 录音目录外面去。
func copyTreeStreaming(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFileStreaming(p, target)
	})
}

func copyFileStreaming(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // src 来自本应用自己写的录音目录
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // dst 由工作区路径与洗过的名字拼出
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
