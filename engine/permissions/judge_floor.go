package permissions

import "strings"

// This file holds judge ("smart") mode's deterministic safety floor: the set of
// actions a model harm verdict must NOT be allowed to auto-approve on its own.
//
// A model gate cannot be made injection-proof by prompting alone, so it is used
// only to reduce approval noise for the safe-but-ordinary middle. The irreversible
// / blind-spot tail is caught here by fixed rules that never consult the model:
//   - mutations to execution-surface or credential files (CI, git hooks, the
//     agent's own .runcode config, shell rc, .env), and
//   - MCP external calls (the judge sees only the tool name, never its arguments).
// These always fall through to a human prompt regardless of the verdict.

// executionSurfaceDirs are path segments that, when present anywhere in a target
// path, mark it as CI / VCS / agent-config surface — a write there can persist
// code execution or alter the security boundary well beyond the current task.
var executionSurfaceDirs = map[string]bool{
	".git":      true,
	".github":   true,
	".gitlab":   true,
	".circleci": true,
	".husky":    true,
	".runcode":  true,
	".hg":       true,
	".svn":      true,
}

// executionSurfaceBases are exact basenames that carry execution or credential
// surface: shell startup files run on future shells, and credential files hold
// secrets. Kept as a small, explicit, easily-extended set to avoid over-broadening
// into routine source edits (which would only train approval fatigue).
var executionSurfaceBases = map[string]bool{
	".bashrc":       true,
	".bash_profile": true,
	".profile":      true,
	".zshrc":        true,
	".zprofile":     true,
	".gitconfig":    true,
	".npmrc":        true,
	".netrc":        true,
}

// isExecutionSurfacePath reports whether a resolved file/dir path targets an
// execution-surface or credential file. It normalizes separators so it holds on
// both Windows and POSIX, and matches on path segments and the basename.
func isExecutionSurfacePath(path string) bool {
	p := strings.ToLower(strings.TrimSpace(path))
	if p == "" {
		return false
	}
	p = strings.ReplaceAll(p, "\\", "/")
	segments := strings.Split(p, "/")
	for _, segment := range segments {
		if executionSurfaceDirs[segment] {
			return true
		}
	}
	base := segments[len(segments)-1]
	if executionSurfaceBases[base] {
		return true
	}
	// Every .env variant (.env, .env.local, .env.production, ...) is credential surface.
	return base == ".env" || strings.HasPrefix(base, ".env.")
}

// isSensitiveMutation reports whether an action writes, edits, or deletes an
// execution-surface / credential file. Judge mode must not auto-allow these.
func isSensitiveMutation(action Action) bool {
	switch action.Operation {
	case OperationWrite, OperationEdit, OperationDelete:
	default:
		return false
	}
	for _, resource := range action.Resources {
		if resource.Type != ResourceFile && resource.Type != ResourceDirectory {
			continue
		}
		if isExecutionSurfacePath(resource.Path) {
			return true
		}
	}
	return false
}

// mustPromptDespiteSafeVerdict reports whether an action must still be escalated
// to a human prompt even when the harm judge deems it safe. It is the floor under
// judge mode's auto-allow: the model's judgment reduces noise, but never waives a
// prompt for these irreversible / blind-spot categories.
func mustPromptDespiteSafeVerdict(action Action) bool {
	// The judge only ever sees an MCP call's server/tool name, not its arguments,
	// so a "safe" verdict on an external call is judged blind — always confirm.
	if action.Operation == OperationExternal {
		return true
	}
	// Rewriting CI / hooks / agent config / credentials is execution/credential
	// surface: a safe verdict must not silently authorize it.
	if isSensitiveMutation(action) {
		return true
	}
	// Defensive floor for irreversible command capabilities. The policy already
	// hard-denies these before the gate is consulted, so this only matters if that
	// ever relaxes; keeping it makes "safe verdict => allow" structurally safe.
	capabilities := metadataStrings(action.Metadata, MetadataCommandCapabilities)
	return containsString(capabilities, string(CommandCapabilityRequiresPrivilege)) ||
		containsString(capabilities, string(CommandCapabilityWritesOutside)) ||
		containsString(capabilities, string(CommandCapabilityDestructiveVCS))
}
