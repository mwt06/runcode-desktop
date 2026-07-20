package toolpath

import (
	"os"
	"path/filepath"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
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

func TestResolveMutationTargetMissingParentIsCreatable(t *testing.T) {
	t.Parallel()

	// A missing parent chain inside the workspace is allowed: the writer will create
	// it (mkdir -p). The target resolves as a within-workspace, not-yet-existing file.
	workspace := t.TempDir()
	rel := filepath.Join("missing", "deep", "new.txt")
	target, err := ResolveMutationTarget(rel, &tool.Context{WorkingDirectory: workspace})
	if err != nil {
		t.Fatalf("resolve mutation target: %v", err)
	}
	if target.Exists || !target.Within || target.Path != filepath.Join(workspace, rel) {
		t.Fatalf("unexpected target: %+v", target)
	}
}

func TestResolveMutationTargetParentIsFile(t *testing.T) {
	t.Parallel()

	// When an ancestor is a file rather than a directory, the target is invalid.
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "afile"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := ResolveMutationTarget(filepath.Join("afile", "new.txt"), &tool.Context{WorkingDirectory: workspace}); err == nil {
		t.Fatal("expected error when an ancestor is a file")
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
