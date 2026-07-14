package llm

import (
	"fmt"
	"sort"
	"sync"
)

// Config is the neutral configuration a provider factory receives. Fields common
// to every provider are explicit; provider-specific escape hatches that do not
// generalize go in Options (e.g. a compatibility flag for one backend).
type Config struct {
	APIKey           string
	AuthToken        string
	BaseURL          string
	DefaultMaxTokens int
	MaxContextTokens int
	MaxRetries       int
	Options          map[string]string
	// TokenSource, when set, is consulted per request for a fresh bearer token
	// (e.g. an OAuth access token that auto-refreshes). It takes precedence over
	// APIKey/AuthToken; a nil TokenSource keeps the static-credential behavior.
	TokenSource func() (string, error)
}

// Factory builds a Provider from neutral Config. Each provider package registers
// one from its init().
type Factory func(Config) (Provider, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register adds a provider factory under name, intended to be called from a
// provider package's init(). A duplicate or empty registration panics, since it
// is a programming error surfaced deterministically at startup.
func Register(name string, f Factory) {
	if name == "" || f == nil {
		panic("llm: Register requires a non-empty name and a factory")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[name]; dup {
		panic("llm: duplicate provider registration: " + name)
	}
	registry[name] = f
}

// Build constructs the named provider. An unknown name is an error listing the
// registered providers — there is no silent fallback that would turn a typo into
// the wrong backend.
func Build(name string, cfg Config) (Provider, error) {
	registryMu.RLock()
	f, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("llm: unknown provider %q (registered: %v)", name, Registered())
	}
	return f(cfg)
}

// IsRegistered reports whether a provider name has a registered factory.
func IsRegistered(name string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[name]
	return ok
}

// Registered returns the registered provider names, sorted.
func Registered() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
