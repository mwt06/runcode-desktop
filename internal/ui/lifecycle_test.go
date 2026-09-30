package ui

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	tea "github.com/charmbracelet/bubbletea"
)

type cancelledService struct{ fakeService }

func (*cancelledService) RunTurn(ctx context.Context, _ string) (TurnResult, error) {
	return TurnResult{}, ctx.Err()
}

func TestTurnCancellationStillDeliversCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		turn, cancel := context.WithCancel(context.Background())
		cancel()
		events := make(chan tea.Msg)
		runTurnCmd(context.Background(), turn, &cancelledService{}, "work", events)()
		msg := <-events
		failure, ok := msg.(turnErrorMsg)
		if !ok || !errors.Is(failure.Err, context.Canceled) {
			t.Fatalf("completion = %#v", msg)
		}
	})
}

func TestUILifetimeReleasesBlockedCommands(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, stop := context.WithCancel(context.Background())
		events := make(chan tea.Msg)
		runTurnCmd(ctx, context.Background(), &fakeService{}, "work", events)()
		synctest.Wait() // The result sender is blocked; there is no UI consumer.
		stop()
		synctest.Wait() // The bubble cannot finish with a leaked sender.
		if msg := waitEventCmd(ctx, events)(); msg != nil {
			t.Fatalf("event after shutdown = %#v", msg)
		}
	})
}
