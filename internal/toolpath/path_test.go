package toolpath

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wt68/runcode/pkg/tool"
)

func TestResolveMutationTargetExistingWorkspaceFile(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.txt")
	writeFile(t, path, "alpha")

	target, err := ResolveMutationTarget("sample.txt", &tool.Context{WorkingDirectory: workspace})
	if err != nil {
		t.Fatalf("resolve mutation target: %v", err)
	}
	if !target.Exists || !target.Within || target.Path != path {
		t.Fatalf("unexpected target: %+v", target)
	}
}

func TestResolveMutationTargetExistingOutsideFile(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()
	path := filepath.Join(outside, "sample.txt")
	writeFile(t, path, "alpha")

	target, err := ResolveMutationTarget(path, &tool.Context{WorkingDirectory: workspace})
	if err != nil {
		t.Fatalf("resolve mutation target: %v", err)
	}
	if !target.Exists || target.Within {
		t.Fatalf("unexpected target: %+v", target)
	}
}

func TestResolveMutationTargetWorkspaceSymlinkToOutside(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	writeFile(t, outsideFile, "secret")
	linkPath := filepath.Join(workspace, "secret-link.txt")
	if err := os.Symlink(outsideFile, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	target, err := ResolveMutationTarget("secret-link.txt", &tool.Context{WorkingDirectory: workspace})
	if err != nil {
		t.Fatalf("resolve mutation target: %v", err)
	}
	if !target.Exists || target.Within {
		t.Fatalf("unexpected target: %+v", target)
	}
}

func TestResolveMutationTargetMissingFileInWorkspaceParent(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "new.txt")

	target, err := ResolveMutationTarget("new.txt", &tool.Context{WorkingDirectory: workspace})
	if err != nil {
		t.Fatalf("resolve mutation target: %v", err)
	}
	if target.Exists || !target.Within || target.Path != path {
		t.Fatalf("unexpected target: %+v", target)
	}
}

func TestResolveMutationTargetDanglingSymlinkIsExistingOutsideTarget(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()
	linkPath := filepath.Join(workspace, "dangling-link.txt")
	if err := os.Symlink(filepath.Join(outside, "missing.txt"), linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	target, err := ResolveMutationTarget("dangling-link.txt", &tool.Context{WorkingDirectory: workspace})
	if err != nil {
		t.Fatalf("resolve mutation target: %v", err)
	}
	if !target.Exists || target.Within {
		t.Fatalf("unexpected target: %+v", target)
	}
}

func TestResolveMutationTargetMissingParent(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	_, err := ResolveMutationTarget(filepath.Join("missing", "new.txt"), &tool.Context{WorkingDirectory: workspace})
	if err == nil {
		t.Fatal("expected missing parent error")
	}
}

func TestResolveMutationTargetDoesNotUsePrefixMatching(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	workspace := filepath.Join(base, "repo")
	outsideSibling := filepath.Join(base, "repo-other")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatalf("make workspace: %v", err)
	}
	if err := os.Mkdir(outsideSibling, 0o700); err != nil {
		t.Fatalf("make outside sibling: %v", err)
	}
	path := filepath.Join(outsideSibling, "new.txt")

	target, err := ResolveMutationTarget(path, &tool.Context{WorkingDirectory: workspace})
	if err != nil {
		t.Fatalf("resolve mutation target: %v", err)
	}
	if target.Exists || target.Within {
		t.Fatalf("unexpected target: %+v", target)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}
