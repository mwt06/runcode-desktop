package desktop

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wt68/runcode/internal/browsertool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/toolpath"
)

// Artifact actions are the product's local output path. The model may create
// an SVG, image, HTML page, office document, or a file with an application
// specific extension without a second approval prompt. The permission engine
// still owns the hard gates: an un-read overwrite remains denied, and this
// wrapper never changes deletes, commands, network, or MCP decisions.
const (
	reasonArtifactWrite     permissions.Reason = "artifact_write"
	reasonArtifactPreview   permissions.Reason = "artifact_preview"
	metadataArtifactPreview                    = "desktop_artifact_preview"
)

type artifactPolicy struct{ inner permissions.Policy }

func newArtifactPolicy(inner permissions.Policy) artifactPolicy {
	return artifactPolicy{inner: inner}
}

func (p artifactPolicy) Decide(ctx context.Context, action permissions.Action) permissions.Decision {
	decision := p.inner.Decide(ctx, action)
	if decision.FinalEffect != permissions.EffectAsk {
		return decision
	}
	if isArtifactMutation(action) {
		return permissions.Allow(reasonArtifactWrite, "desktop.artifact.write")
	}
	if isBoundArtifactPreview(action) {
		return permissions.Allow(reasonArtifactPreview, "desktop.artifact.preview")
	}
	return decision
}

// isArtifactMutation deliberately uses the resolved operation and resource
// boundary rather than an extension allowlist. Product output can have an
// unknown extension, while a familiar extension inside .git or .env is still
// an execution/credential surface. The resolver supplies canonical paths and
// scopes; the wrapper accepts only a complete set of workspace files.
func isArtifactMutation(action permissions.Action) bool {
	switch action.Operation {
	case permissions.OperationWrite, permissions.OperationEdit:
	default:
		return false
	}
	if len(action.Resources) == 0 {
		return false
	}
	for _, resource := range action.Resources {
		if resource.Type != permissions.ResourceFile || resource.Scope != permissions.ResourceScopeWorkspace {
			return false
		}
		if isArtifactProtectedPath(resource.Path) {
			return false
		}
	}
	return true
}

// The engine's judge floor keeps this same execution/credential surface out of
// silent approvals. Keep the desktop wrapper aligned so interactive product
// writes cannot bypass that boundary either.
func isArtifactProtectedPath(path string) bool {
	if isArtifactProtectedPathText(path) {
		return true
	}
	// Resolve existing symlinks before deciding: a workspace-local alias can
	// point into .git/.env or another execution surface while its lexical name
	// looks harmless. Canonical also resolves existing ancestors for a new leaf.
	if canonical, err := toolpath.Canonical(path); err == nil && canonical != path {
		return isArtifactProtectedPathText(canonical)
	}
	if resolved := resolveArtifactMutationPath(path); resolved != path {
		return isArtifactProtectedPathText(resolved)
	}
	return false
}

// resolveArtifactMutationPath follows existing and dangling links while
// retaining missing path components. ResolveMutationTarget uses the same
// physical rule for workspace scope; repeating it here keeps the execution
// surface check aligned even though the engine does not expose its resolved
// target on permissions.Action.
func resolveArtifactMutationPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	cur := filepath.Clean(abs)
	var tail []string
	for i := 0; i < 32; i++ {
		info, statErr := os.Lstat(cur)
		if statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				dest, readErr := os.Readlink(cur)
				if readErr != nil {
					return path
				}
				if !filepath.IsAbs(dest) {
					dest = filepath.Join(filepath.Dir(cur), dest)
				}
				cur = filepath.Clean(dest)
				continue
			}
			for j := len(tail) - 1; j >= 0; j-- {
				cur = filepath.Join(cur, tail[j])
			}
			return cur
		}
		if !os.IsNotExist(statErr) {
			return path
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return path
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
	return path
}

func isArtifactProtectedPathText(path string) bool {
	p := strings.ToLower(strings.TrimSpace(path))
	if p == "" {
		return true
	}
	// A colon beyond the Windows volume selects an NTFS data stream. In
	// particular, .env::$DATA writes .env itself, so none of these aliases may
	// receive the ordinary artifact override. Preserve the inner policy instead.
	if runtime.GOOS == "windows" && strings.Contains(p[len(filepath.VolumeName(p)):], ":") {
		return true
	}
	segments := strings.FieldsFunc(strings.ReplaceAll(p, "\\", "/"), func(r rune) bool { return r == '/' })
	for _, segment := range segments {
		switch segment {
		case ".git", ".github", ".gitlab", ".circleci", ".husky", ".runcode", ".hg", ".svn":
			return true
		}
	}
	base := ""
	if len(segments) > 0 {
		base = segments[len(segments)-1]
	}
	switch base {
	case ".bashrc", ".bash_profile", ".profile", ".zshrc", ".zprofile", ".gitconfig", ".npmrc", ".netrc":
		return true
	}
	return base == ".env" || strings.HasPrefix(base, ".env.")
}

func isBoundArtifactPreview(action permissions.Action) bool {
	if action.ToolName != browsertool.Name || action.Operation != permissions.OperationExternal {
		return false
	}
	return action.Metadata != nil && action.Metadata[metadataArtifactPreview] == true
}
