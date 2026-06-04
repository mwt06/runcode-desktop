package permissions

import (
	"sort"
	"strings"
	"sync"
)

// SessionAllowStore remembers approvals that the user granted for the lifetime
// of a session. Implementations must be safe for concurrent use because tools
// may be authorized from parallel goroutines.
type SessionAllowStore interface {
	// Allowed reports whether the given session key has an active grant.
	Allowed(key string) bool
	// Remember records a session grant for the given key.
	Remember(key string)
}

// MemorySessionAllowStore is an in-process SessionAllowStore. Grants live for
// the lifetime of the store (the process session) and are never persisted to
// disk.
type MemorySessionAllowStore struct {
	mu      sync.Mutex
	allowed map[string]struct{}
}

// NewMemorySessionAllowStore returns an empty in-memory session allow store.
func NewMemorySessionAllowStore() *MemorySessionAllowStore {
	return &MemorySessionAllowStore{allowed: map[string]struct{}{}}
}

func (s *MemorySessionAllowStore) Allowed(key string) bool {
	if s == nil || key == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.allowed[key]
	return ok
}

func (s *MemorySessionAllowStore) Remember(key string) {
	if s == nil || key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.allowed == nil {
		s.allowed = map[string]struct{}{}
	}
	s.allowed[key] = struct{}{}
}

// Session-key scopes and separator. Keys are scope-prefixed and NUL-delimited so
// the parts never collide with path, host, or category characters. The scopes
// are also surfaced by ParseRule so management tooling can render keys.
const (
	ScopeMutate   = "mutate"
	ScopeCommand  = "command"
	ScopeNetwork  = "network"
	sessionKeySep = "\x00"
)

// NetworkSessionKey builds the session/persistent key for a network operation
// against a host. Empty inputs yield an empty key (never remembered).
func NetworkSessionKey(tool, host string) string {
	if tool == "" || host == "" {
		return ""
	}
	return strings.Join([]string{ScopeNetwork, tool, host}, sessionKeySep)
}

// MutateSessionKey builds the key for a Write/Edit mutation of a concrete target.
func MutateSessionKey(tool, path string) string {
	if tool == "" || path == "" {
		return ""
	}
	return strings.Join([]string{ScopeMutate, tool, path}, sessionKeySep)
}

// CommandSessionKey builds the key for a Bash command category and its
// capabilities. Capabilities are sorted so the key is order-independent.
func CommandSessionKey(tool, category string, capabilities []string) string {
	if tool == "" || category == "" {
		return ""
	}
	sorted := append([]string(nil), capabilities...)
	sort.Strings(sorted)
	return strings.Join([]string{ScopeCommand, tool, category, strings.Join(sorted, ",")}, sessionKeySep)
}

// Rule is a parsed session/persistent key for display. Target holds the
// scope-specific remainder: the host, the mutation path, or "category caps".
type Rule struct {
	Key    string
	Scope  string
	Tool   string
	Target string
}

// ParseRule splits a key into its scope, tool, and a human-readable target. It
// never fails: an unrecognized key still yields a Rule with whatever parts are
// present, so management tooling can always show something.
func ParseRule(key string) Rule {
	rule := Rule{Key: key}
	parts := strings.Split(key, sessionKeySep)
	if len(parts) > 0 {
		rule.Scope = parts[0]
	}
	if len(parts) > 1 {
		rule.Tool = parts[1]
	}
	if len(parts) > 2 {
		rule.Target = strings.TrimSpace(strings.Join(parts[2:], " "))
	}
	return rule
}

// DefaultSessionKey derives a stable session-scope key from an action. An empty
// result means the action must not be remembered for the session, so each
// occurrence is approved individually. The grain is intentionally per-tool:
//
//   - Write/Edit: keyed by the concrete mutation target, so "allow for session"
//     means "keep mutating this file".
//   - Bash: keyed by the command category and capabilities, so "allow for
//     session" means "keep running this kind of command" (the exact command
//     string varies between calls, so a per-command key would never match).
//
// Unknown commands and pathless mutations return an empty key and always
// re-prompt. Hard-denied actions never reach authorization, so a session grant
// can never widen the policy.
func DefaultSessionKey(action Action) string {
	switch action.Operation {
	case OperationWrite, OperationEdit:
		return MutateSessionKey(action.ToolName, firstResourcePath(action.Resources))
	case OperationExecute:
		category := metadataString(action.Metadata, MetadataCommandCategory)
		if category == "" || category == string(CommandCategoryUnknown) {
			return ""
		}
		return CommandSessionKey(action.ToolName, category, metadataStrings(action.Metadata, MetadataCommandCapabilities))
	case OperationNetwork:
		return NetworkSessionKey(action.ToolName, metadataString(action.Metadata, MetadataNetworkHost))
	default:
		return ""
	}
}

func firstResourcePath(resources []Resource) string {
	for _, resource := range resources {
		if strings.TrimSpace(resource.Path) != "" {
			return resource.Path
		}
	}
	return ""
}
