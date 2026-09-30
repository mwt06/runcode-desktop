package appupdate

import (
	"os/exec"
	"strings"
	"testing"
)

// Like the server's dependency audit, inspect the compiler-resolved closure,
// not source keywords. A helper cannot smuggle the desktop host back in.
func TestUpdateDependencyBoundary(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	allowed := map[string]bool{
		"github.com/wt68/runcode/internal/appupdate": true,
		"github.com/wt68/runcode/internal/download":  true,
		"github.com/wt68/runcode/internal/protocol":  true,
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.HasPrefix(dep, "github.com/wt68/runcode/") && !allowed[dep] {
			t.Errorf("update service depends on host feature: %s", dep)
		}
		if strings.HasPrefix(dep, "github.com/wailsapp/") {
			t.Errorf("update service depends on Wails: %s", dep)
		}
	}
}
