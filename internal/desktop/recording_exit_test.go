package desktop

import (
	"sync"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/protocol"
	"github.com/wt68/runcode/internal/recorder"
)

func TestStartRecordingRejectsAfterCompletedExit(t *testing.T) {
	app, _, capturer, _ := newRecorderApp(t)
	defer app.closeRecorder()
	if !app.PrepareExit(time.Second) {
		t.Fatal("idle exit did not complete")
	}
	if _, err := app.StartRecording(protocol.StartRecordingRequest{}); err == nil {
		t.Fatal("recording admitted after completed exit")
	}
	if !app.PrepareExit(time.Hour) {
		t.Fatal("rejected recording changed completed exit result")
	}
	capturer.mu.Lock()
	defer capturer.mu.Unlock()
	if len(capturer.streams) != 0 {
		t.Fatalf("rejected recording opened %d capture streams", len(capturer.streams))
	}
}

func TestStartRecordingQueuedBehindLockRejectsAfterExitBegins(t *testing.T) {
	app, _, capturer, _ := newRecorderApp(t)
	defer app.closeRecorder()
	app.rec.mu.Lock()
	started := make(chan error, 1)
	go func() {
		_, err := app.StartRecording(protocol.StartRecordingRequest{})
		started <- err
	}()
	waitLifecycleLock(t, "(*App).StartRecording")
	finished := make(chan bool, 1)
	go func() { finished <- app.PrepareExit(2 * time.Second) }()
	waitLifecycleLock(t, "(*App).closeRecorder")
	app.rec.mu.Unlock()
	if err := <-started; err == nil {
		t.Error("queued recording admitted after exit began")
	}
	if !<-finished {
		t.Fatal("rejected queued recording prevented exit")
	}
	capturer.mu.Lock()
	defer capturer.mu.Unlock()
	if len(capturer.streams) != 0 {
		t.Fatalf("queued recording opened %d capture streams", len(capturer.streams))
	}
}

type exitHeldCapturer struct {
	*fakeCapturer
	entered, release chan struct{}
	once             sync.Once
}

func (c *exitHeldCapturer) Open(cfg recorder.OpenConfig) (recorder.Stream, error) {
	c.once.Do(func() {
		close(c.entered)
		<-c.release
	})
	return c.fakeCapturer.Open(cfg)
}

func TestPrepareExitDrainsRecordingAlreadyStarting(t *testing.T) {
	app, _, capturer, _ := newRecorderApp(t)
	defer app.closeRecorder()
	held := &exitHeldCapturer{fakeCapturer: capturer, entered: make(chan struct{}), release: make(chan struct{})}
	app.SetCapturer(held)
	started := make(chan error, 1)
	go func() {
		_, err := app.StartRecording(protocol.StartRecordingRequest{})
		started <- err
	}()
	<-held.entered
	finished := make(chan bool, 1)
	go func() { finished <- app.PrepareExit(2 * time.Second) }()
	waitLifecycleLock(t, "(*App).closeRecorder")
	close(held.release)
	if err := <-started; err != nil {
		t.Fatalf("recording accepted before exit failed: %v", err)
	}
	if !<-finished {
		t.Fatal("in-flight recording did not drain")
	}
	app.rec.mu.Lock()
	active := app.rec.sess != nil
	app.rec.mu.Unlock()
	if active {
		t.Fatal("exit retained an active recording")
	}
	capturer.mu.Lock()
	defer capturer.mu.Unlock()
	if len(capturer.streams) != 2 {
		t.Fatalf("in-flight recording opened %d streams, want two", len(capturer.streams))
	}
	for source, stream := range capturer.streams {
		if !stream.closed.Load() {
			t.Errorf("exit retained capture stream %s", source)
		}
	}
}
