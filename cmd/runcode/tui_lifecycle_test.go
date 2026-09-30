package main

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/turn"
)

type lifecycleTuiSession struct {
	tuiEngineSession
	entered, finish chan struct{}
	closes          atomic.Int32
	closeErr        error
}

func (s *lifecycleTuiSession) RunTurnWithImages(ctx context.Context, _ string, _ []llm.ImageSource) (turn.Result, error) {
	close(s.entered)
	<-ctx.Done()
	<-s.finish
	return turn.Result{}, ctx.Err()
}
func (s *lifecycleTuiSession) Close(context.Context) error {
	s.closes.Add(1)
	return s.closeErr
}

func TestTuiCloseJoinsTurnAndRetriesCleanup(t *testing.T) {
	failed := errors.New("retry close")
	fs := &lifecycleTuiSession{entered: make(chan struct{}), finish: make(chan struct{}), closeErr: failed}
	s := &tuiSessionService{session: fs}
	result := make(chan error, 1)
	go func() { _, err := s.RunTurn(context.Background(), "hello"); result <- err }()
	<-fs.entered
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := s.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close = %v", err)
	}
	if fs.closes.Load() != 0 {
		t.Fatal("closed active engine")
	}
	if _, err := s.RunTurn(context.Background(), "late"); err == nil {
		t.Fatal("closing service accepted turn")
	}
	close(fs.finish)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("turn = %v", err)
	}
	if err := s.Close(context.Background()); !errors.Is(err, failed) {
		t.Fatalf("Close = %v", err)
	}
	fs.closeErr = nil
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fs.closes.Load() != 2 {
		t.Fatal("successful cleanup repeated")
	}
}

type cleanupChatRunner struct {
	fakeChatRunner
	closeErr          error
	cleanupContextErr error
}

func (r *cleanupChatRunner) Close(ctx context.Context) error {
	r.cleanupContextErr = ctx.Err()
	return r.closeErr
}

func TestChatReturnsCloseErrorWithFreshContext(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "test-token")
	failed := errors.New("cleanup failed")
	runner := &cleanupChatRunner{closeErr: failed}
	cmd := newChatCmd(runner)
	cmd.SetArgs([]string{"--model", "test-model", "hello"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cmd.ExecuteContext(ctx); !errors.Is(err, failed) {
		t.Fatalf("command = %v", err)
	}
	if runner.cleanupContextErr != nil {
		t.Fatal("cleanup reused cancelled command context")
	}
}
