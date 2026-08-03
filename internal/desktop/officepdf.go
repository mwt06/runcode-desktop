package desktop

// Office 文档(pptx/docx/xlsx)的高保真预览:交给本机装着的 Office 套件转成 PDF,
// 再用 WebView 自带的 PDF 查看器显示。
//
// 内嵌的 JS 渲染器(pptx-preview / docx-preview / SheetJS)在文字度量、autofit 缩放
// 与中文字体回退这些基本面上就和真 Office 对不齐——实测一套只有矩形和纯色填充的
// 演示稿都差得明显,那不是调 CSS 能补的,是排版引擎的差别。把渲染交回给用户机器上
// 那个"标准答案",预览看到的就是他另开 WPS/PowerPoint 会看到的东西。
//
// 转换是尽力而为:没装 Office、调用失败、非 Windows 平台,一律返回错误,由前端退回
// JS 渲染器。**能力缺失从来不是错误路径**——降级方向是安全的,只是不够好看。

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// previewCacheDir 放转换产物。在 .runcode 下,所以文件选择器、Glob/Grep 与
	// @ 引用清单都看不见它(那些都跳过 .runcode)。
	previewCacheDir = "preview-cache"
	// previewCacheKeep 是缓存里保留的 PDF 个数。一次会话可能预览很多稿件,每份都
	// 是几百 KB;按最近使用裁剪,免得工作区里悄悄堆出几百兆。
	previewCacheKeep = 24
)

// errOfficeUnavailable 表示这台机器上没有能用的转换器(没装 Office,或平台不支持)。
// 前端据此静默退回 JS 渲染器,不当故障报。
var errOfficeUnavailable = errors.New("本机没有可用于转换的 Office 组件")

// officeConvertMu 串行化转换。Office 的 COM 自动化并不欢迎并发调用,而预览又常常
// 是"打开一个标签页"这种低频动作,排队比并发安全。
var officeConvertMu sync.Mutex

// officeKind 按扩展名判断文档类型,空串表示不是可转换的 Office 文档。
func officeKind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pptx", ".ppt":
		return "ppt"
	case ".docx", ".doc":
		return "doc"
	case ".xlsx", ".xls":
		return "xls"
	default:
		return ""
	}
}

// RenderOfficePDF 把工作区里的一份 Office 文档转成 PDF,返回**工作区相对**的 PDF
// 路径,前端拿它走既有的 PDF 预览(经预览服务器的 URL 塞进 iframe)。
//
// 同一份文件重复打开命中缓存直接返回;源文件改了(大小或修改时间变了)键就变了,
// 于是自然重转,不需要额外的失效逻辑。
func (a *App) RenderOfficePDF(relPath string) (string, error) {
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	resolved, err := resolveWithinWorkspace(ws, relPath)
	if err != nil {
		return "", wireError(err)
	}
	if officeKind(resolved) == "" {
		return "", wireError(fmt.Errorf("不是可转换的 Office 文档：%s", relPath))
	}
	info, err := os.Stat(resolved)
	if err != nil {
		// 与 ReadArtifact 同样的语义:文件没了就带 not_found 码,前端静默关掉标签页。
		return "", wireError(artifactFSError(relPath, err))
	}

	cacheRel := filepath.ToSlash(filepath.Join(".runcode", previewCacheDir, officeCacheName(resolved, info)))
	cacheAbs := filepath.Join(ws, filepath.FromSlash(cacheRel))
	if st, statErr := os.Stat(cacheAbs); statErr == nil && st.Size() > 0 {
		return cacheRel, nil
	}

	officeConvertMu.Lock()
	defer officeConvertMu.Unlock()
	// 排队期间别的调用可能已经转好了同一份。
	if st, statErr := os.Stat(cacheAbs); statErr == nil && st.Size() > 0 {
		return cacheRel, nil
	}
	if err := os.MkdirAll(filepath.Dir(cacheAbs), 0o755); err != nil {
		return "", wireError(fmt.Errorf("创建预览缓存目录失败: %w", err))
	}
	if err := convertOfficeToPDF(resolved, cacheAbs); err != nil {
		return "", wireError(err)
	}
	if st, statErr := os.Stat(cacheAbs); statErr != nil || st.Size() == 0 {
		// 转换器报成功却没产出:当作不可用,让前端退回 JS 渲染,而不是给一个空白 PDF。
		_ = os.Remove(cacheAbs)
		return "", wireError(errOfficeUnavailable)
	}
	prunePreviewCache(filepath.Dir(cacheAbs), previewCacheKeep)
	return cacheRel, nil
}

// officeCacheName 由源文件的绝对路径 + 修改时间 + 大小算出缓存文件名。三者任一变化
// 都换一个名字,所以"源文件改了要重转"这件事由键本身保证,不必再写失效逻辑;旧的那份
// 由 prunePreviewCache 按时间淘汰。
func officeCacheName(absPath string, info os.FileInfo) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", strings.ToLower(absPath), info.ModTime().UnixNano(), info.Size())))
	return hex.EncodeToString(sum[:])[:16] + ".pdf"
}

// prunePreviewCache 只保留最近修改的 keep 个 PDF。删除失败无所谓——缓存本来就是
// 可再生的,清不掉的那个下次再试。
func prunePreviewCache(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type aged struct {
		path string
		mod  time.Time
	}
	files := make([]aged, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pdf") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, aged{path: filepath.Join(dir, e.Name()), mod: info.ModTime()})
	}
	if len(files) <= keep {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for _, f := range files[keep:] {
		_ = os.Remove(f.path)
	}
}
