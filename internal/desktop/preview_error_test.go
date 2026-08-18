package desktop

import (
	"errors"
	"os"
	"strings"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
)

// A preview opened from a conversation card can name a file that has since been
// moved or deleted. That case must read as what it is, not as the Windows syscall
// os.Stat happened to use ("GetFileAttributesEx …: The system cannot find the file
// specified"), and it must name the file the way the card did.
func TestArtifactFSErrorExplainsAMissingFile(t *testing.T) {
	t.Parallel()
	_, statErr := os.Stat(t.TempDir() + "/gone.md")
	err := artifactFSError("docs/gone.md", statErr)
	if !strings.Contains(err.Error(), "已被移动") || !strings.Contains(err.Error(), "docs/gone.md") {
		t.Fatalf("err = %q, want a moved/deleted explanation naming the relative path", err)
	}
	if strings.Contains(err.Error(), "GetFileAttributesEx") || strings.Contains(err.Error(), "CreateFile") {
		t.Fatalf("err = %q, leaked the raw syscall error", err)
	}
	// The UI decides whether to open a tab from the code, not from the wording.
	var coded *protocol.Error
	if !errors.As(wireError(err), &coded) || coded.Code != protocol.ErrCodeNotFound {
		t.Fatalf("wireError(%v) = %#v, want a not_found code", err, coded)
	}
}

// An unknown cause keeps the underlying error — it is all the diagnosis there is —
// but still leads with the file the user was looking at.
func TestArtifactFSErrorKeepsUnknownCauses(t *testing.T) {
	t.Parallel()
	err := artifactFSError("a/b.png", errors.New("device not ready"))
	if !strings.HasPrefix(err.Error(), "读取文件失败：a/b.png") || !strings.Contains(err.Error(), "device not ready") {
		t.Fatalf("err = %q, want the path first and the cause kept", err)
	}
}

// 端到端：从命令入口打开一个已经不在的文件，必须拿到 not_found 码与人话。
//
// 这条曾经漏网，而上面两个测试一直是绿的——因为它们只测 artifactFSError 本身。
// 真实链路里 resolveWithinWorkspace 的 EvalSymlinks 先于 os.Stat 失败并原样上抛，
// 那个写得很仔细的包装函数在"文件不存在"这个最常见的场景下根本走不到，用户看到的
// 是 "GetFileAttributesEx D:\…\part2.md: The system cannot find the file specified"。
// 所以这里从 App 的导出方法测，覆盖"包装写对了但没接上"这一类缺陷。
func TestMissingArtifactNeverLeaksSyscallError(t *testing.T) {
	ws := t.TempDir()
	a := appWithWorkspace(t, ws)

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"ReadArtifact", func() error { _, err := a.ReadArtifact("notes/part2.md"); return err }},
		{"ReadArtifactBytes", func() error { _, err := a.ReadArtifactBytes("notes/part2.md"); return err }},
		{"ResolveArtifactPath", func() error { _, err := a.ResolveArtifactPath("notes/part2.md"); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatal("缺失的文件必须报错")
			}
			if strings.Contains(err.Error(), "GetFileAttributesEx") || strings.Contains(err.Error(), "CreateFile") {
				t.Fatalf("泄漏了原始 syscall 错误: %v", err)
			}
			if !strings.Contains(err.Error(), "已被移动") || !strings.Contains(err.Error(), "notes/part2.md") {
				t.Fatalf("err = %q, want 人话 + 卡片上那个相对路径", err)
			}
			var coded *protocol.Error
			if !errors.As(err, &coded) || coded.Code != protocol.ErrCodeNotFound {
				t.Fatalf("err = %#v, want not_found 码（前端据此不开标签页）", coded)
			}
		})
	}
}
