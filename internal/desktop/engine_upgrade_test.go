package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/wt68/runcode/internal/browsertool"
	"github.com/wt68/runcode/internal/officetool"
	"github.com/wt68/runcode/internal/plantool"
	"github.com/wt68/runcode/internal/previewtool"
	"github.com/wt68/runcode/internal/runtimetool"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

var upgradeProviderOnce sync.Once

type upgradeProvider struct{}

func (upgradeProvider) Name() string                   { return "desktop-upgrade-test" }
func (upgradeProvider) Capabilities() llm.Capabilities { return llm.Capabilities{} }
func (upgradeProvider) Stream(_ context.Context, req llm.Request) (llm.Stream, error) {
	ch := make(chan llm.StreamEvent, 6)
	if len(req.Messages) == 1 {
		ch <- llm.StreamEvent{Type: llm.StreamEventTypeContentBlockStart, Block: &llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "preview-1", Name: previewtool.Name}}
		ch <- llm.StreamEvent{Type: llm.StreamEventTypeContentBlockDelta, Delta: &llm.ContentDelta{InputJSON: []byte(`{"path":"rep`)}}
		ch <- llm.StreamEvent{Type: llm.StreamEventTypeContentBlockDelta, Delta: &llm.ContentDelta{InputJSON: []byte(`ort.txt"}`)}}
		ch <- llm.StreamEvent{Type: llm.StreamEventTypeContentBlockStop}
		ch <- llm.StreamEvent{Type: llm.StreamEventTypeMessageStop, StopReason: llm.StopReasonToolUse, Usage: &llm.Usage{OutputTokens: 1}}
	} else {
		ch <- llm.StreamEvent{Type: llm.StreamEventTypeContentBlockStart, Block: &llm.ContentBlock{Type: llm.ContentBlockTypeText, Text: "done"}}
		ch <- llm.StreamEvent{Type: llm.StreamEventTypeContentBlockStop}
		ch <- llm.StreamEvent{Type: llm.StreamEventTypeMessageStop, StopReason: llm.StopReasonEndTurn, Usage: &llm.Usage{OutputTokens: 2}}
	}
	close(ch)
	return upgradeStream{ch}, nil
}

type upgradeStream struct{ ch <-chan llm.StreamEvent }

func (s upgradeStream) Events() <-chan llm.StreamEvent { return s.ch }
func (upgradeStream) Err() error                       { return nil }
func (upgradeStream) Close() error                     { return nil }

func TestEngineUpgradePreservesAuditHistoryAndToolProgress(t *testing.T) {
	isolateConfigDir(t)
	asTestBuild(t)
	upgradeProviderOnce.Do(func() {
		llm.Register("desktop-upgrade-test", func(llm.Config) (llm.Provider, error) { return upgradeProvider{}, nil })
	})
	app := New(&recordingSink{})
	if _, err := app.audit.enable(); err != nil {
		t.Fatal(err)
	}
	defer app.audit.disable()
	cfg := engine.Config{CWD: t.TempDir(), SessionID: "upgrade-audit", Provider: "desktop-upgrade-test", Model: "fake", PermissionMode: "safe", PersistSession: true, MaxIterations: 3}
	if err := os.WriteFile(filepath.Join(cfg.CWD, "report.txt"), []byte("report"), 0600); err != nil {
		t.Fatal(err)
	}
	events := make(chan tool.Event, 64)
	opts := engine.Options{ToolEvents: events}
	app.configureSession(host.SessionContext{ID: cfg.SessionID, Emit: func(string, any) {}}, &cfg, &opts)
	if !opts.OmitRequestSnapshots || opts.LLMRequestObserver == nil {
		t.Fatal("snapshot/audit wiring missing")
	}
	sess, err := engine.Build(cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close(context.Background())
	result, err := sess.RunTurn(context.Background(), "preview the report")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.FirstRequest, llm.Request{}) || result.Requests != nil || result.ClassificationRequest != nil {
		t.Fatal("retained full request snapshots")
	}
	if llm.TextContent(result.FinalAssistant) != "done" || result.FinalUsage.OutputTokens != 2 || len(result.Usages) != 2 || len(sess.History()) != 4 {
		t.Fatalf("execution changed: %+v", result)
	}
	records, err := app.audit.store.readSession(cfg.SessionID)
	if err != nil || len(records) != 2 {
		t.Fatalf("audit records=%d err=%v", len(records), err)
	}
	var decoded [2]auditRecord
	for i, raw := range records {
		if err := json.Unmarshal(raw, &decoded[i]); err != nil {
			t.Fatal(err)
		}
	}
	if len(decoded[0].System) == 0 || len(decoded[0].Messages) != 1 || len(decoded[1].Messages) != 3 || len(decoded[0].Tools) == 0 {
		t.Fatal("audit lost request content")
	}
	var progress, started bool
	for len(events) > 0 {
		ev := <-events
		if ev.ToolName != previewtool.Name {
			continue
		}
		if ev.Type == tool.EventTypeStarted {
			started = true
		}
		if ev.Type == tool.EventTypeProgress && !started {
			var input map[string]string
			if json.Unmarshal(ev.Input, &input) == nil && input["path"] == "rep" {
				progress = true
			}
		}
	}
	if !progress || !started {
		t.Fatal("custom input preview did not precede execution")
	}
	if err = sess.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	history, err := sessions.LoadHistory(cfg.CWD, cfg.SessionID)
	if err != nil || len(history) != 4 {
		t.Fatalf("persisted history=%d err=%v", len(history), err)
	}
}

func TestHostInputPresentationsMatchStringSchema(t *testing.T) {
	for _, entry := range []struct {
		tool  tool.Tool
		field string
	}{
		{browsertool.New(nil), "url"}, {previewtool.New(), "path"}, {officetool.New(), "path"}, {runtimetool.New(nil), "runtime"}, {plantool.New(nil), "stage"},
	} {
		t.Run(entry.tool.Name(), func(t *testing.T) {
			p, ok := entry.tool.(tool.InputPresentationProvider)
			if !ok {
				t.Fatal("missing input presentation")
			}
			declaration := p.InputPresentation()
			if declaration.PrimaryField != entry.field || declaration.BodyField != "" {
				t.Fatalf("presentation=%+v", declaration)
			}
			if entry.tool.InputSchema().Properties[entry.field].Type != tool.SchemaTypeString {
				t.Fatal("preview field is not a string")
			}
		})
	}
}
