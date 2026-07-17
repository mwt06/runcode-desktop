package engine

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/wt68/runcode/engine/llm"
	"github.com/wt68/runcode/engine/sessions"
)

// buildTestProviderName is a fake provider registered once for Build tests, so
// Build's provider construction succeeds without network or credentials.
const buildTestProviderName = "build-test-fake"

var registerBuildTestProvider sync.Once

func useBuildTestProvider() {
	registerBuildTestProvider.Do(func() {
		llm.Register(buildTestProviderName, func(llm.Config) (llm.Provider, error) {
			return buildTestProvider{}, nil
		})
	})
}

type buildTestProvider struct{}

func (buildTestProvider) Name() string                   { return buildTestProviderName }
func (buildTestProvider) Capabilities() llm.Capabilities { return llm.Capabilities{} }
func (buildTestProvider) Stream(context.Context, llm.Request) (llm.Stream, error) {
	return nil, errors.New("build-test provider does not stream")
}

// fakeBackend records usage so a test can assert an injected backend is
// consulted by Build but never closed by the session. It retains every Store
// it opened so tests can also assert per-session stores were closed (e.g. on
// a failed Build's cleanup path).
type fakeBackend struct {
	mu           sync.Mutex
	openedStores []string
	stores       []*fakeStore
	closed       bool
}

type fakeStore struct {
	backend *fakeBackend
	closed  bool
}

func (b *fakeBackend) OpenStore(_ context.Context, id string) (sessions.Store, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.openedStores = append(b.openedStores, id)
	st := &fakeStore{backend: b}
	b.stores = append(b.stores, st)
	return st, nil
}

func (b *fakeBackend) LoadHistory(context.Context, string) ([]llm.Message, error) { return nil, nil }
func (b *fakeBackend) List(context.Context) ([]sessions.Info, error)              { return nil, nil }
func (b *fakeBackend) Describe(context.Context, string) (sessions.Info, error) {
	return sessions.Info{}, errors.New("not found")
}
func (b *fakeBackend) Latest(context.Context) (string, error) { return "", nil }
func (b *fakeBackend) SaveMeta(context.Context, string, sessions.SessionMeta) error {
	return nil
}
func (b *fakeBackend) LoadMeta(context.Context, string) (sessions.SessionMeta, error) {
	return sessions.SessionMeta{}, nil
}
func (b *fakeBackend) Close(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return nil
}

func (b *fakeBackend) wasClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

// allStoresClosed reports whether every Store the backend handed out has been
// closed (false when none were opened at all, so a test cannot vacuously pass).
func (b *fakeBackend) allStoresClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.stores) == 0 {
		return false
	}
	for _, st := range b.stores {
		if !st.closed {
			return false
		}
	}
	return true
}

func (s *fakeStore) Append(context.Context, []llm.Message) error { return nil }
func (s *fakeStore) Close(context.Context) error {
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()
	s.closed = true
	return nil
}

func buildTestConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Provider:       buildTestProviderName,
		Model:          "test-model",
		CWD:            t.TempDir(),
		PermissionMode: safeMode,
		SessionID:      "build-test-session",
		PersistSession: true,
	}
}

// An injected backend is used for the session's stores but its lifecycle stays
// with the host: Session.Close must close the per-session Store yet leave the
// backend open for the host's other sessions.
func TestBuildInjectedBackendIsUsedButNotClosed(t *testing.T) {
	useBuildTestProvider()

	backend := &fakeBackend{}
	session, err := Build(buildTestConfig(t), Options{Backend: backend})
	if err != nil {
		t.Fatalf("Build with injected backend: %v", err)
	}

	backend.mu.Lock()
	opened := append([]string(nil), backend.openedStores...)
	backend.mu.Unlock()
	if len(opened) != 1 || opened[0] != "build-test-session" {
		t.Fatalf("injected backend OpenStore calls = %v, want [build-test-session]", opened)
	}

	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if backend.wasClosed() {
		t.Fatal("Session.Close closed the injected backend; the host owns its lifecycle")
	}
}

// Without an injected backend, behavior is the historical one: Build opens the
// configured backend itself and Close shuts everything down without error.
func TestBuildWithoutInjectedBackendOwnsLifecycle(t *testing.T) {
	useBuildTestProvider()

	session, err := Build(buildTestConfig(t), Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := session.SessionID(); got != "build-test-session" {
		t.Fatalf("session id = %q, want build-test-session", got)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
