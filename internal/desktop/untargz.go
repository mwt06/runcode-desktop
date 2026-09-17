package desktop

// 解压运行时包（tar.gz）。
//
// 为什么运行时包是 tar.gz 而技能包是 zip：运行时包里有**符号链接**（Linux 的
// bin/python3 → python3.12、npm → npm-cli.js）和**可执行位**，zip 这两样都表达不
// 可靠。反过来技能包只有普通文本文件，zip 足够。两种格式各留在自己该在的地方，
// 比统一成一种再去补缺口划算。
//
// 防护与 unzipSkill 那份同源，逐条对应一种能把文件写到解压目录之外的手法：
// 条目名里的 .. 与绝对路径、指向根外的符号链接、以及解压后的体积上限。清单里的
// sha256 已经在解压前比对过了（内容就是平台发布的那份），这些防护是第二道闸——
// 但第一道闸只保证"没被中途换掉"，不保证"平台发的东西一定规矩"。

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// untarMaxBytes 给解压后的总体积封顶。
//
// 现实里最大的包（Windows 的 Python 解开约 100 MB、MinGit 约 130 MB）离它很远；
// 这个数是防"一个畸形的包把用户磁盘写满"，不是容量规划。
const untarMaxBytes int64 = 2 << 30

// untargz 把 src 解到 dir。dir 必须是本次安装专用的空目录——函数不清理它，
// 失败时由调用方整个删掉。
//
// onBytes 按解压出的累计字节数回调（可为 nil）。解压几万个小文件比下载还慢，
// 没有进度的话用户会以为卡死了。
func untargz(src, dir string, onBytes func(n int64)) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("运行时包不是有效的 tar.gz: %w", err)
	}
	defer func() { _ = gz.Close() }()

	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}

	var total int64
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("解压运行时包: %w", err)
		}
		target, ok := safeJoin(root, h.Name)
		if !ok {
			// 条目名越界。跳过而不是中断：一个畸形条目不该让整包作废，而它被跳过
			// 之后包要么照样能用、要么在探测那一步露馅（探测跑的是真的 --version）。
			debugLog("runtime pack: skipping unsafe entry %q", h.Name)
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := writeSymlink(root, target, h.Linkname); err != nil {
				return err
			}
		case tar.TypeReg:
			n, err := writeTarFile(tr, target, h)
			if err != nil {
				return err
			}
			total += n
			if total > untarMaxBytes {
				return fmt.Errorf("运行时包解压后超过 %d MiB，拒绝继续", untarMaxBytes>>20)
			}
			if onBytes != nil {
				onBytes(total)
			}
		default:
			// 硬链接、设备文件、FIFO：运行时包里不该有，打包工具也不产出它们。
			continue
		}
	}
}

// safeJoin 把归档里的条目名接到 root 下，并挡住 zip-slip。
//
// 三种越界一起挡：绝对路径、含 .. 的相对路径、以及规范化之后仍然落在 root 之外的
// （Windows 上 "C:x" 这类奇形怪状的相对路径）。
func safeJoin(root, name string) (string, bool) {
	// 先按**归档的语义**判绝对路径，再按主机的判一次。tar 里的名字是 POSIX 路径，
	// "/etc/passwd" 无论在哪个系统上都该当成绝对路径拒掉——而 Windows 的
	// filepath.IsAbs 对它返回 false（没有盘符），只靠后者的话它会被接到解压根下面，
	// 悄悄变成一个"看着没越界"的怪路径。
	raw := strings.TrimPrefix(name, "./")
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, `\`) || volumeLike(raw) {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(raw))
	if clean == "." || clean == string(filepath.Separator) {
		return "", false
	}
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", false
	}
	target := filepath.Join(root, clean)
	// Join 已经 Clean 过，再核一次前缀才能挡住 volume 名之类的花样。
	if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return "", false
	}
	return target, true
}

// writeTarFile 把一个普通文件写出去，返回写了多少字节。
func writeTarFile(tr io.Reader, target string, h *tar.Header) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	// 权限位原样落盘：Linux 与 macOS 上少了可执行位，python3 就是一句
	// "Permission denied"，而在打包机上完全看不出异常。
	mode := os.FileMode(h.Mode).Perm() //nolint:gosec // 归档里的权限位就是要还原的东西
	if mode == 0 {
		mode = 0o644
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return 0, err
	}
	//nolint:gosec // G110: 体积上限由调用方按累计字节数把关（untarMaxBytes）
	n, err := io.Copy(f, tr)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	// Windows 上 OpenFile 的 perm 只影响只读位，Unix 上还会被 umask 削一刀；
	// 补一次 Chmod 才能保证解出来的就是包里那份权限。
	if err := os.Chmod(target, mode); err != nil {
		return n, err
	}
	return n, nil
}

// writeSymlink 建一个符号链接，并保证它指向解压根之内。
//
// Windows 上建符号链接要开发者模式或管理员权限——而本功能的前提就是"全程不提权"。
// 所以那边失败了就退化成复制：Windows 的包本来不含符号链接（打包时就没有），这条
// 分支是给"哪天上游改了布局"留的后路，不是常规路径。
func writeSymlink(root, target, link string) error {
	if !symlinkInRoot(root, target, link) {
		debugLog("runtime pack: skipping symlink %q -> %q (outside pack)", target, link)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	_ = os.Remove(target)
	if err := os.Symlink(link, target); err == nil {
		return nil
	}
	// 退化成复制。源还没解出来（tar 是顺序的，链接可能先于目标出现）就只能放弃
	// 这一条——包要么照样能用，要么在探测那一步露馅。
	src := filepath.Join(filepath.Dir(target), filepath.FromSlash(link))
	data, err := os.ReadFile(src) //nolint:gosec // src 已由 symlinkInRoot 限定在解压根内
	if err != nil {
		debugLog("runtime pack: symlink %q -> %q unsupported and target missing", target, link)
		return nil
	}
	return os.WriteFile(target, data, 0o755) //nolint:gosec // 复制的是包里的可执行文件
}

// symlinkInRoot 判断 link（相对 target 所在目录解析）是否仍落在 root 内。
// 绝对路径的链接一律拒绝——运行时包里没有任何理由指向包外。
func symlinkInRoot(root, target, link string) bool {
	if link == "" || filepath.IsAbs(filepath.FromSlash(link)) {
		return false
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(target), filepath.FromSlash(link)))
	return resolved == root || strings.HasPrefix(resolved, root+string(filepath.Separator))
}

// volumeLike 判断条目名是不是带盘符（"C:x"、"C:/x"）。这类名字在 Windows 上会
// 绕过"接到 root 下面"的意图，在类 Unix 上则是个合法的普通文件名——所以统一按
// 可疑处理，运行时包里没有任何理由出现带冒号的路径。
func volumeLike(name string) bool {
	return len(name) >= 2 && name[1] == ':'
}
