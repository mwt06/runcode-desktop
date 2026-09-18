package desktop

// 麒麟 KYSEC 执行控制与运行时包。与平台无关的那一半（纯逻辑，任何平台都能测）；
// 真正调 kysec_set 的在 kysec_linux.go。
//
// # 问题（2026-09-18 在麒麟 V10 SP1 真机上逐条实测）
//
//   - dpkg 装的文件被标为 verified；我们自己解压出的 ELF 是 unknown。
//   - 启动一个 unknown 的程序：桌面弹一个安全框，30 秒没人点就拒（退出码 126）。
//   - 点了"允许"**不会被记住**，标签仍是 unknown，下一次照样弹。
//   - 一个 verified 的进程去加载 unknown 的 .so，同样被拒（failed to map segment）。
//
// 合起来的后果：模型每跑一次 python3，用户桌面就弹一次框，没人盯着就失败。而且只给
// 解释器加白不够——lxml、Pillow 那些 .so 在一个 verified 的 Python 里也会被拒。
//
// # 修法
//
// 装完运行时，用 sudo 一次性把包里**所有** ELF（程序与 .so）标成 verified：密码框
// 只弹一次，实测 38 个文件 1 秒，之后一个框都不弹。之后 pip 装的带 C 扩展的新库同样
// 是 unknown——所以记下加白时那批文件的指纹，指纹变了就重新亮出「授权」按钮。

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// kysecMarker 记着"这个包上一次加白时，里面的 ELF 长什么样"（指纹）。
//
// 用指纹而不是文件数：pip 升级一个库时 .so 的个数可能不变，但换成了新文件——新文件
// 同样是 unknown。指纹包含路径、大小与修改时间，换了文件它一定会变。
const kysecMarker = ".kysec-trusted"

// elfMagic 是 ELF 文件头。按内容认，不按扩展名猜：libfoo.so.1.2、没有扩展名的可执行
// 文件、甚至被改过名的共享库，扩展名都靠不住。
var elfMagic = []byte{0x7f, 'E', 'L', 'F'}

// elfFile 是包里的一个 ELF 文件。
type elfFile struct {
	path  string
	size  int64
	mtime int64
}

// listELF 找出 dir 下所有 ELF 文件（按路径排序，指纹才稳定）。
//
// 只看普通文件：符号链接指向的目标本身也在包里，会被单独找到；给链接加白是在给
// 它指向的东西加白，重复且没有必要。
func listELF(dir string) ([]elfFile, error) {
	var out []elfFile
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		ok, err := isELF(p)
		if err != nil || !ok {
			return nil //nolint:nilerr // 读不了的文件跳过：它连执行都谈不上
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // 同上
		}
		out = append(out, elfFile{path: p, size: info.Size(), mtime: info.ModTime().UnixNano()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, err
}

func isELF(p string) (bool, error) {
	f, err := os.Open(p) //nolint:gosec // 读自己解压出来的运行时包
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	var head [4]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return false, nil //nolint:nilerr // 不足 4 字节的文件不是 ELF
	}
	return bytes.Equal(head[:], elfMagic), nil
}

// elfFingerprint 是一批 ELF 的指纹。路径取相对 root 的形式，包被整个挪走也不失效。
func elfFingerprint(root string, files []elfFile) string {
	h := sha256.New()
	for _, f := range files {
		rel, err := filepath.Rel(root, f.path)
		if err != nil {
			rel = f.path
		}
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00%d\n", filepath.ToSlash(rel), f.size, f.mtime)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// readTrustMarker 读上一次加白时记下的指纹（""=从没加过）。
func readTrustMarker(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, kysecMarker)) //nolint:gosec // 自己写的标记文件
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writeTrustMarker(dir, fingerprint string) error {
	return os.WriteFile(filepath.Join(dir, kysecMarker), []byte(fingerprint+"\n"), 0o600)
}

// parseExecControl 从 getstatus 的输出里判断执行控制开没开。
//
// 实测输出里那一行是 "exec control : warning"。warning 并不是"只提示不拦"——它会弹框、
// 没人点就拒——所以除了明确的关闭，一律当"开着"。
func parseExecControl(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(strings.ToLower(key)) != "exec control" {
			continue
		}
		switch strings.TrimSpace(strings.ToLower(val)) {
		case "", "off", "close", "closed", "disable", "disabled":
			return false
		}
		return true
	}
	return false
}

// needsTrust 报告 dir 里的运行时包要不要（重新）加白。
func needsTrust(dir string) (bool, []elfFile, error) {
	files, err := listELF(dir)
	if err != nil {
		return false, nil, err
	}
	if len(files) == 0 {
		return false, nil, nil
	}
	return readTrustMarker(dir) != elfFingerprint(dir, files), files, nil
}
