package mail

import (
	"os/exec"
	"strings"
	"testing"
)

// TestMailDependencyBoundary 用编译器解析的传递闭包守住「独立业务」边界：
// 不得依赖 automation / desktop / localusage / Wails。
func TestMailDependencyBoundary(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	forbiddenPrefixes := []string{
		"github.com/wt68/runcode/internal/automation",
		"github.com/wt68/runcode/internal/desktop",
		"github.com/wt68/runcode/internal/localusage",
		"github.com/wailsapp/",
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, bad := range forbiddenPrefixes {
			if dep == bad || strings.HasPrefix(dep, bad) {
				t.Errorf("mail package depends on forbidden host feature: %s", dep)
			}
		}
	}
}
