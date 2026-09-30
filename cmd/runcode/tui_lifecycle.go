package main

import (
	"context"
	"errors"
	"time"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/turn"
)

const sessionCloseTimeout = 15 * time.Second

// tuiEngineSession is the engine surface owned by the terminal service.
type tuiEngineSession interface {
	RunTurnWithImages(context.Context, string, []llm.ImageSource) (turn.Result, error)
	ResetHistory()
	Compact(context.Context) (int, int, llm.Usage, error)
	SetPermissionMode(string) error
	SetModel(string) error
	Status() engine.Status
	Close(context.Context) error
}

type tuiCloseAttempt struct {
	done chan struct{}
	err  error
}

// beginOperation covers both turns and explicit compaction. UI exit must join
// either before closing engine-owned stores, even on an abnormal program exit.
func (s *tuiSessionService) beginOperation(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return nil, nil, errors.New("session is closing")
	}
	if s.active != nil {
		return nil, nil, errors.New("session is busy")
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.active, s.cancel = done, cancel
	return ctx, func() {
		cancel()
		s.mu.Lock()
		s.active, s.cancel = nil, nil
		close(done)
		s.mu.Unlock()
	}, nil
}

func (s *tuiSessionService) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closing = true
	if s.cancel != nil {
		s.cancel()
	}
	if attempt := s.cleanup; attempt != nil {
		s.mu.Unlock()
		if err := waitTuiDone(ctx, attempt.done); err != nil {
			return err
		}
		return attempt.err
	}
	attempt := &tuiCloseAttempt{done: make(chan struct{})}
	s.cleanup = attempt
	active := s.active
	s.mu.Unlock()
	err := waitTuiDone(ctx, active)
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = s.session.Close(ctx)
	}
	s.mu.Lock()
	s.closed = err == nil
	attempt.err = err
	s.cleanup = nil
	close(attempt.done)
	s.mu.Unlock()
	return err
}

func waitTuiDone(ctx context.Context, done <-chan struct{}) error {
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
