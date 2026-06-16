package bash

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

const (
	// maxBackgroundShells bounds how many background shells can run at once, so a
	// runaway agent cannot spawn unbounded processes.
	maxBackgroundShells = 16
	// maxBackgroundOutputBytes caps how much output one background shell retains.
	maxBackgroundOutputBytes = 1 << 20 // 1 MiB
	// killWait bounds how long Kill/Close waits for a shell to actually exit.
	killWait = 5 * time.Second
)

// Manager owns the background shells started with run_in_background. It is shared
// by the Bash, BashOutput, and KillShell tools so output read and termination
// can find shells the Bash tool started. It satisfies io-style Close so a session
// can terminate all background shells on shutdown.
type Manager struct {
	mu     sync.Mutex
	shells map[string]*backgroundShell
	seq    int
	closed bool
}

// NewManager returns an empty manager.
func NewManager() *Manager {
	return &Manager{shells: make(map[string]*backgroundShell)}
}

type backgroundShell struct {
	id      string
	command string
	cancel  context.CancelFunc
	out     *shellBuffer
	done    chan struct{}

	mu       sync.Mutex
	running  bool
	exitCode int
	endedAt  time.Time
}

// ShellStatus is a snapshot of a background shell plus any output not yet read.
type ShellStatus struct {
	ID        string
	Command   string
	Running   bool
	ExitCode  int
	NewOutput string
	Truncated bool
}

// Start launches command as a background shell rooted at workspace and returns
// its id. The shell outlives the calling tool invocation (it is tied to a
// background context), so it keeps running until it exits or is killed.
func (m *Manager) Start(command, workspace string) (string, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return "", errors.New("shell manager is closed")
	}
	if len(m.shells) >= maxBackgroundShells {
		m.mu.Unlock()
		return "", fmt.Errorf("too many background shells (max %d); kill one first", maxBackgroundShells)
	}
	m.seq++
	id := fmt.Sprintf("bash_%d", m.seq)
	m.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Dir = workspace
	buf := &shellBuffer{limit: maxBackgroundOutputBytes}
	cmd.Stdout = buf
	cmd.Stderr = buf

	sh := &backgroundShell{
		id:      id,
		command: command,
		cancel:  cancel,
		out:     buf,
		done:    make(chan struct{}),
		running: true,
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return "", fmt.Errorf("start background bash: %w", err)
	}

	m.mu.Lock()
	m.shells[id] = sh
	m.mu.Unlock()

	go func() {
		err := cmd.Wait()
		sh.mu.Lock()
		sh.running = false
		sh.endedAt = time.Now()
		sh.exitCode = 0
		if err != nil {
			sh.exitCode = -1
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				sh.exitCode = exitErr.ExitCode()
			}
		}
		sh.mu.Unlock()
		close(sh.done)
	}()
	return id, nil
}

// Output returns a shell's status plus any output produced since the last call.
func (m *Manager) Output(id string) (ShellStatus, error) {
	sh, err := m.lookup(id)
	if err != nil {
		return ShellStatus{}, err
	}
	out, truncated := sh.out.readNew()
	sh.mu.Lock()
	running, code := sh.running, sh.exitCode
	sh.mu.Unlock()
	return ShellStatus{
		ID:        id,
		Command:   sh.command,
		Running:   running,
		ExitCode:  code,
		NewOutput: out,
		Truncated: truncated,
	}, nil
}

// Kill terminates a background shell and returns its final status (including any
// remaining output). Killing an already-exited shell is a no-op.
func (m *Manager) Kill(id string) (ShellStatus, error) {
	sh, err := m.lookup(id)
	if err != nil {
		return ShellStatus{}, err
	}
	sh.cancel()
	select {
	case <-sh.done:
	case <-time.After(killWait):
	}
	return m.Output(id)
}

func (m *Manager) lookup(id string) (*backgroundShell, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh, ok := m.shells[id]
	if !ok {
		return nil, fmt.Errorf("unknown background shell %q", id)
	}
	return sh, nil
}

// Close terminates every background shell and waits (bounded by ctx) for them to
// exit, so a session shutdown does not leave orphaned processes.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	shells := make([]*backgroundShell, 0, len(m.shells))
	for _, sh := range m.shells {
		shells = append(shells, sh)
	}
	m.mu.Unlock()

	for _, sh := range shells {
		sh.cancel()
	}
	for _, sh := range shells {
		select {
		case <-sh.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// shellBuffer is a thread-safe, byte-capped buffer that tracks how much has been
// read, so BashOutput can return only output produced since the last read.
type shellBuffer struct {
	mu        sync.Mutex
	buf       []byte
	readOff   int
	limit     int
	truncated bool
}

func (b *shellBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.limit - len(b.buf)
	if room <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		b.buf = append(b.buf, p[:room]...)
		b.truncated = true
		return len(p), nil
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

// readNew returns output appended since the last readNew, advancing the read
// offset, plus whether output has ever been truncated by the cap.
func (b *shellBuffer) readNew() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := string(b.buf[b.readOff:])
	b.readOff = len(b.buf)
	return out, b.truncated
}
