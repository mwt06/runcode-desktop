package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"testing"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
)

func TestConfigureOmitsRequestSnapshots(t *testing.T) {
	var opts engine.Options
	configureSession(host.SessionContext{}, &engine.Config{}, &opts)
	if !opts.OmitRequestSnapshots {
		t.Fatal("server retains unused request snapshots")
	}
}

type retryServerSession struct {
	fakeSession
	closeErr error
}

func (s *retryServerSession) Close(context.Context) error { return s.closeErr }

func TestCloseFailurePreservesEventSubscription(t *testing.T) {
	failed := errors.New("cleanup incomplete")
	fs := &retryServerSession{closeErr: failed}
	h := newHub(nil)
	mgr := host.NewManager(host.Options{Sink: h, Build: func(cfg engine.Config, _ engine.Options) (host.Session, error) { fs.id = cfg.SessionID; return fs, nil }})
	defer func() { fs.closeErr = nil; _ = mgr.CloseAll(context.Background()) }()
	id, _, err := mgr.Create(context.Background(), engine.Config{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(config{}, mgr, h, log.New(io.Discard, "", 0))
	events, unsubscribe := h.subscribe(id)
	defer unsubscribe()
	request := []byte(fmt.Sprintf(`{"sessionId":%q}`, id))
	if _, err = srv.rpcCloseSession(context.Background(), request); !errors.Is(err, failed) {
		t.Fatalf("close=%v", err)
	}
	select {
	case <-events:
		t.Fatal("subscription closed before cleanup completed")
	default:
	}
	fs.closeErr = nil
	if _, err = srv.rpcCloseSession(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-events; ok {
		t.Fatal("successful close kept subscription open")
	}
}
