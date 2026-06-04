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
		path := firstResourcePath(action.Resources)
		if path == "" {
			return ""
		}
		return "mutate\x00" + action.ToolName + "\x00" + path
	case OperationExecute:
		category := metadataString(action.Metadata, MetadataCommandCategory)
		if category == "" || category == string(CommandCategoryUnknown) {
			return ""
		}
		capabilities := metadataStrings(action.Metadata, MetadataCommandCapabilities)
		sorted := append([]string(nil), capabilities...)
		sort.Strings(sorted)
		return "command\x00" + action.ToolName + "\x00" + category + "\x00" + strings.Join(sorted, ",")
	case OperationNetwork:
		host := metadataString(action.Metadata, MetadataNetworkHost)
		if host == "" {
			return ""
		}
		return "network\x00" + action.ToolName + "\x00" + host
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
