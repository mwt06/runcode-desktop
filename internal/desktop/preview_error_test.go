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
