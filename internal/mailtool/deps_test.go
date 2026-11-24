package mailtool

import (
	"os/exec"
	"strings"
	"testing"
)

// TestMailToolDependencyBoundary 确保工具包不依赖 automation 业务层。
func TestMailToolDependencyBoundary(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == "github.com/wt68/runcode/internal/automation" ||
			strings.HasPrefix(dep, "github.com/wt68/runcode/internal/automation/") {
			t.Errorf("mailtool depends on automation: %s", dep)
		}
	}
}
