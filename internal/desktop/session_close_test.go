package desktop

import (
	"context"
	"errors"
	"testing"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
)

type failingCloseHostSession struct {
	*fakeHostSession
	closeErr error
}

func (s *failingCloseHostSession) Close(context.Context) error { return s.closeErr }

func TestCloseFailureKeepsDesktopSessionAndPreventsRebuild(t *testing.T) {
	failed := errors.New("store not flushed")
	fs := &failingCloseHostSession{fakeHostSession: newFakeHostSession(), closeErr: failed}
	builds := 0
	app := newWithBuild(&recordingSink{}, func(cfg engine.Config, opts engine.Options) (host.Session, error) {
		builds++
		if !opts.OmitRequestSnapshots {
			t.Error("desktop retained request snapshots")
		}
		fs.id, fs.cwd = cfg.SessionID, cfg.CWD
		return fs, nil
	})
	cfg := engine.Config{CWD: t.TempDir(), SessionID: "close-retry", Model: "fake"}
	id, _, err := app.mgr.Create(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	e := focusOn(app, id, cfg.CWD)
	defer func() { fs.closeErr = nil; _ = app.CloseAllSessions() }()
	if err = app.CloseSession(id); err == nil {
		t.Fatal("close failure hidden")
	}
	app.mu.Lock()
	kept := app.sessions[id] == e && !e.closed && app.focused == id
	app.mu.Unlock()
	if !kept {
		t.Fatal("close failure discarded session")
	}
	app.startMu.Lock()
	_, err = app.openSessionWithConnectionHeld(cfg, false, "")
	app.startMu.Unlock()
	if err == nil || builds != 1 {
		t.Fatal("rebuilt after failed close")
	}
	if err = app.CloseAllSessions(); err == nil {
		t.Fatal("CloseAll hid cleanup failure")
	}
	fs.closeErr = nil
	if err = app.CloseSession(id); err != nil {
		t.Fatal(err)
	}
	if len(app.OpenSessions()) != 0 {
		t.Fatal("successful retry kept session")
	}
}
