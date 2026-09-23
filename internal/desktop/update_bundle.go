package desktop

// macOS 安装包的纯校验逻辑放在无平台标记的文件，Windows CI 也能覆盖。
// 真正解压用 ditto 保留资源叉/权限/签名；解压前先拒绝越界路径与符号链接写穿。
import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	macUpdateMaxFiles        = 100_000
	macUpdateMaxBytes uint64 = 4 << 30
)

func macBundleRoot(exe string) string {
	bin := filepath.Dir(exe)
	contents := filepath.Dir(bin)
	root := filepath.Dir(contents)
	if filepath.Base(bin) != "MacOS" || filepath.Base(contents) != "Contents" ||
		!strings.HasSuffix(filepath.Base(root), ".app") {
		return ""
	}
	return root
}

// validateMacUpdateZip 只接受一份顶层 .app，外加 ditto 产生的 __MACOSX 资源叉目录。
// 链接必须相对且留在同一 bundle 内，归档不能再往一个链接目录下面写文件。
func validateMacUpdateZip(file string) (string, error) {
	r, err := zip.OpenReader(file)
	if err != nil {
		return "", fmt.Errorf("无法读取 macOS 更新包（需要 .app 的 zip）: %w", err)
	}
	defer func() { _ = r.Close() }()
	if len(r.File) == 0 || len(r.File) > macUpdateMaxFiles {
		return "", errors.New("更新包的文件数量不合理")
	}
	var root string
	var total uint64
	names := make(map[string]bool, len(r.File))
	links := map[string]bool{}
	for _, f := range r.File {
		name := strings.TrimSuffix(f.Name, "/")
		if !safeBundleEntry(name) {
			return "", fmt.Errorf("更新包包含不安全的路径: %q", f.Name)
		}
		key := strings.ToLower(name)
		if names[key] {
			return "", fmt.Errorf("更新包包含重复路径: %q", name)
		}
		names[key] = true
		if f.UncompressedSize64 > macUpdateMaxBytes-total {
			return "", errors.New("更新包解压后过大")
		}
		total += f.UncompressedSize64
		top, _, _ := strings.Cut(name, "/")
		if top != "__MACOSX" {
			if !strings.HasSuffix(top, ".app") || (root != "" && root != top) {
				return "", errors.New("更新包必须且只能包含一个顶层 .app")
			}
			root = top
		}
		mode := f.Mode()
		switch {
		case mode.IsRegular(), mode.IsDir():
		default:
			if mode.Type() != os.ModeSymlink {
				return "", fmt.Errorf("更新包包含特殊文件: %q", name)
			}
			target, err := zipLinkTarget(f)
			if err != nil {
				return "", err
			}
			resolved := path.Clean(path.Join(path.Dir(name), target))
			if top == "__MACOSX" || path.IsAbs(target) || strings.ContainsAny(target, "\\:\x00\r\n") ||
				(resolved != top && !strings.HasPrefix(resolved, top+"/")) {
				return "", fmt.Errorf("更新包包含越界的符号链接: %q", name)
			}
			links[key] = true
		}
	}
	if root == "" || !names[strings.ToLower(root+"/Contents/Info.plist")] {
		return "", errors.New("更新包没有应用的 Info.plist")
	}
	for name := range names {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if links[parent] {
				return "", fmt.Errorf("更新包试图向符号链接下写文件: %q", name)
			}
		}
	}
	return root, nil
}

func safeBundleEntry(name string) bool {
	if name == "" || name == "." || path.IsAbs(name) || path.Clean(name) != name ||
		strings.ContainsAny(name, "\\:") {
		return false
	}
	for _, p := range strings.Split(name, "/") {
		if p == ".." || p == "." || p == "" {
			return false
		}
	}
	return !strings.ContainsFunc(name, unicode.IsControl)
}

func zipLinkTarget(f *zip.File) (string, error) {
	if f.UncompressedSize64 == 0 || f.UncompressedSize64 > 4096 {
		return "", errors.New("更新包的符号链接长度不合法")
	}
	r, err := f.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(io.LimitReader(r, 4097))
	if err != nil {
		return "", err
	}
	if len(data) == 0 || len(data) > 4096 {
		return "", errors.New("更新包的符号链接长度不合法")
	}
	return string(data), nil
}

func codeSignTeam(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if team, ok := strings.CutPrefix(strings.TrimSpace(line), "TeamIdentifier="); ok && team != "not set" {
			return strings.TrimSpace(team)
		}
	}
	return ""
}
