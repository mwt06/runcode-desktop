package desktop

import (
	"testing"

	"github.com/wt68/runcode/internal/protocol"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	coreprotocol "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
)

func TestPreviewReadyOnPublicStartAndResume(t *testing.T) {
	isolateConfigDir(t)
	sink := &recordingSink{}
	app := newWithBuild(sink, func(cfg engine.Config, _ engine.Options) (host.Session, error) {
		s := newFakeHostSession()
		s.id, s.cwd = cfg.SessionID, cfg.CWD
		return s, nil
	})
	defer app.Shutdown()
	first, err := app.StartSession(StartSessionRequest{CWD: t.TempDir(), Provider: "openai", Model: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	assertReady := func(id, old string) string {
		t.Helper()
		var generation string
		sink.mu.Lock()
		defer sink.mu.Unlock()
		for _, event := range sink.events {
			if event.name != protocol.EventPreviewLifecycle {
				continue
			}
			env, ok := event.data.(coreprotocol.Envelope)
			if !ok {
				continue
			}
			payload, ok := env.Payload.(protocol.PreviewLifecycle)
			if ok && payload.SessionID == id && payload.Phase == "ready" {
				generation = payload.Generation
			}
		}
		if generation == "" || generation == old || generation != app.previewGenerationOf(id) {
			t.Fatalf("ready generation=%q old=%q current=%q", generation, old, app.previewGenerationOf(id))
		}
		return generation
	}
	old := assertReady(first.SessionID, "")
	if err := app.CloseSession(first.SessionID); err != nil {
		t.Fatal(err)
	}
	resumed, err := app.ResumeSession(first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Info.SessionID != first.SessionID {
		t.Fatal("resume changed session identity")
	}
	assertReady(first.SessionID, old)
	if rebuilt, err := app.ReloadMCPServers(); err != nil || !rebuilt {
		t.Fatalf("reload=%v error=%v", rebuilt, err)
	}
	current := app.previewGenerationOf(first.SessionID)
	sink.mu.Lock()
	defer sink.mu.Unlock()
	ready := 0
	for _, event := range sink.events {
		if event.name != protocol.EventPreviewLifecycle {
			continue
		}
		env := event.data.(coreprotocol.Envelope)
		payload := env.Payload.(protocol.PreviewLifecycle)
		if payload.Phase == "ready" && payload.Generation == current {
			ready++
		}
	}
	if ready != 1 {
		t.Fatalf("rebuild published %d ready events for one generation", ready)
	}
}
