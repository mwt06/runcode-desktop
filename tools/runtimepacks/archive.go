package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// 归档的读与写。
//
// **全程流式转换，不落盘再压回去**——这是本文件唯一重要的设计决定，理由是一个
// 只在目标机器上才会显形的故障：在 Windows 上把 Linux 的 tar.gz 解到磁盘再重新
// 打包，所有可执行位会被 NTFS 抹平，产出的包装到麒麟上是一句 "Permission denied"，
// 而在打包机上完全看不出异常。直接把源归档的条目改名转写进目标归档，权限位与符号
// 链接都原样带过去，打包机是什么系统就无关紧要了。
//
// 输出统一是 tar.gz，Windows 包也是：一种格式意味着客户端只有一个解压器、一套
// zip-slip 与符号链接的防护要写。zip 在这件事上是不够的——它既不可靠地保留
// Unix 权限位，也表达不了 node 那几个 bin/ 下的符号链接（npm 就是其中之一）。

// entry 是归档里的一条，与具体格式无关。
type entry struct {
	name string      // 目标归档里的路径，一律 / 分隔、不以 / 开头
	mode fs.FileMode // 含权限位；目录带 fs.ModeDir，符号链接带 fs.ModeSymlink
	link string      // 符号链接的目标（仅 ModeSymlink）
	size int64
	open func() (io.ReadCloser, error) // 目录与符号链接为 nil
}

// readSource 按扩展名读出源归档的全部条目，并剥掉 strip 指定的顶层目录。
func readSource(p, strip string) ([]entry, error) {
	switch {
	case strings.HasSuffix(p, ".zip"):
		return readZip(p, strip)
	case strings.HasSuffix(p, ".tar.gz"), strings.HasSuffix(p, ".tgz"):
		return readTarGz(p, strip)
	}
	return nil, fmt.Errorf("不认识的归档格式: %s", p)
}

// stripPrefix 去掉顶层目录；返回 ok=false 表示这条不在该目录下，应当跳过。
//
// 跳过而不是报错：上游归档里除了那个顶层目录，有时还躺着一个平级的 LICENSE 或
// pax_global_header，它们进不进包都无所谓，但为它们中断整次打包毫无意义。
func stripPrefix(name, strip string) (string, bool) {
	name = strings.TrimPrefix(filepath.ToSlash(name), "./")
	if strip == "" {
		return name, name != ""
	}
	if !strings.HasPrefix(name, strip) {
		return "", false
	}
	rest := strings.TrimPrefix(name, strip)
	return rest, rest != ""
}

func readZip(p, strip string) ([]entry, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, err
	}
	// 读取器要活到条目被写出去为止，所以不在这里关——调用方拿到的 open 还指着它。
	// 本工具是一次性的命令行程序，进程退出即回收。
	out := make([]entry, 0, len(zr.File))
	for _, f := range zr.File {
		name, ok := stripPrefix(f.Name, strip)
		if !ok {
			continue
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			out = append(out, entry{name: strings.TrimSuffix(name, "/") + "/", mode: mode})
		case mode&fs.ModeSymlink != 0:
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			link, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				return nil, err
			}
			out = append(out, entry{name: name, mode: mode, link: string(link)})
		case mode.IsRegular():
			// zip 来自 Windows 上游时权限位通常是 0666/0777 这类无意义的值，
			// 按扩展名给一个像样的：Windows 端不看这位，但包里躺着 0666 的 exe
			// 会让人误以为它坏了。
			out = append(out, entry{name: name, mode: zipFileMode(name, mode), size: zipSize(f.UncompressedSize64), open: f.Open})
		}
	}
	return out, nil
}

// zipFileMode 给 zip 里的普通文件定权限位。
func zipFileMode(name string, mode fs.FileMode) fs.FileMode {
	if perm := mode.Perm(); perm != 0 && perm != 0o666 && perm != 0o777 {
		return perm
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".exe", ".dll", ".bat", ".cmd", ".ps1", ".sh":
		return 0o755
	}
	return 0o644
}

func readTarGz(p, strip string) ([]entry, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	tr := tar.NewReader(gz)
	var out []entry
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		name, ok := stripPrefix(h.Name, strip)
		if !ok {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			out = append(out, entry{name: strings.TrimSuffix(name, "/") + "/", mode: fs.ModeDir | tarMode(h.Mode)})
		case tar.TypeSymlink:
			out = append(out, entry{name: name, mode: fs.ModeSymlink | 0o777, link: h.Linkname})
		case tar.TypeReg:
			// tar 是顺序读的：条目的内容必须在这一轮就取走，不能像 zip 那样留一个
			// 稍后再打开的句柄。包最大也就几百 MB，整个读进内存最省事也最不容易错。
			buf, err := io.ReadAll(tr)
			if err != nil {
				_ = f.Close()
				return nil, err
			}
			out = append(out, entry{name: name, mode: tarMode(h.Mode), size: int64(len(buf)), open: bytesOpener(buf)})
		default:
			// 硬链接、设备文件、FIFO 一律不要：运行时包里不该有这些东西，而把它们
			// 原样转写出去只会让客户端的解压器需要多懂几种类型。
			continue
		}
	}
	return out, nil
}

func bytesOpener(b []byte) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(string(b))), nil }
}

// dirEntries 把磁盘上的一棵目录树读成条目（预装轮子那一层用它），名字前缀成 prefix。
func dirEntries(root, prefix string) ([]entry, error) {
	var out []entry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := prefix + filepath.ToSlash(rel)
		if d.IsDir() {
			out = append(out, entry{name: name + "/", mode: fs.ModeDir | 0o755})
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		// 轮子是在打包机上装的，磁盘权限位不可信（Windows 上全是 0666）。按位置
		// 定：脚本目录下的可执行，其余只读。
		mode := fs.FileMode(0o644)
		if strings.Contains(name, "/bin/") || strings.Contains(name, "/Scripts/") {
			mode = 0o755
		}
		// p 是回调参数，每次调用各自一份，闭包直接捕获即可。
		out = append(out, entry{name: name, mode: mode, size: info.Size(), open: func() (io.ReadCloser, error) { return os.Open(p) }})
		return nil
	})
	return out, err
}

// writeTarGz 把条目写成 tar.gz。返回产物的 sha256 与字节数由调用方另算（写完再读
// 一遍文件即可——包不大，而把哈希算在这里会让这个函数同时负责两件事）。
func writeTarGz(dest string, entries []entry) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		if seen[e.name] {
			// 同名条目只留第一条：预装轮子那一层可能与上游自带的 site-packages
			// 目录条目重名，重复写进 tar 会让某些解压器报错。
			continue
		}
		seen[e.name] = true
		h := &tar.Header{Name: e.name, Mode: int64(e.mode.Perm())}
		switch {
		case e.mode.IsDir():
			h.Typeflag = tar.TypeDir
		case e.mode&fs.ModeSymlink != 0:
			h.Typeflag = tar.TypeSymlink
			h.Linkname = e.link
		default:
			h.Typeflag = tar.TypeReg
			h.Size = e.size
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		rc, err := e.open()
		if err != nil {
			return err
		}
		n, err := io.Copy(tw, rc)
		_ = rc.Close()
		if err != nil {
			return err
		}
		if n != e.size {
			return fmt.Errorf("%s: 声明 %d 字节，实际写入 %d", e.name, e.size, n)
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Close()
}

// tarMode 把 tar 头里的权限位转成 fs.FileMode。先掩掉高位再转，所以不会有溢出：
// tar 的 Mode 是 int64，而真正有意义的只有低 12 位（权限 + setuid/setgid/sticky）。
func tarMode(mode int64) fs.FileMode {
	return fs.FileMode(uint32(mode & 0o7777)) //nolint:gosec // 已掩到 12 位，转换无溢出
}

// zipSize 把 zip 条目的无符号长度转成 int64，超界的当成 0（打包工具随后会因为
// "声明与实际写入不符"报错，而不是带着一个负数的长度继续跑）。
func zipSize(n uint64) int64 {
	if n > 1<<62 {
		return 0
	}
	return int64(n) //nolint:gosec // 上面已经排除了溢出
}
