package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

// ErrConnClosed is returned by a call whose connection closed before a response
// arrived.
var ErrConnClosed = errors.New("mcp: connection closed")

// conn is a JSON-RPC 2.0 client connection over a messageStream. It correlates
// responses to outstanding requests by id and is safe for concurrent calls.
type conn struct {
	stream messageStream

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcResult

	closeOnce sync.Once
	closed    chan struct{}
	closeErr  error
}

type rpcResult struct {
	result json.RawMessage
	err    *rpcError
}

func newConn(stream messageStream) *conn {
	c := &conn{
		stream:  stream,
		pending: make(map[int64]chan rpcResult),
		closed:  make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// readLoop dispatches incoming frames until the stream closes, then fails every
// outstanding call with the terminal error.
func (c *conn) readLoop() {
	for frame := range c.stream.Incoming() {
		c.dispatch(frame)
	}
	c.shutdown(c.stream.Err())
}

func (c *conn) dispatch(frame []byte) {
	var msg rpcMessage
	if err := json.Unmarshal(frame, &msg); err != nil {
		return // ignore malformed frames rather than tearing down the connection
	}
	id, ok := messageID(msg.ID)
	if !ok {
		// A notification (no id) or a server→client request (id + method). We do
		// not implement server-initiated requests for the tools-only client, so
		// both are ignored.
		return
	}
	c.mu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ch != nil {
		ch <- rpcResult{result: msg.Result, err: msg.Error}
	}
}

// call sends a request and waits for its response, honoring ctx cancellation and
// connection shutdown.
func (c *conn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	select {
	case <-c.closed:
		return nil, c.terminalErr()
	default:
	}

	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResult, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	frame, err := encodeRequest(id, method, params)
	if err != nil {
		c.forget(id)
		return nil, err
	}
	if err := c.stream.Write(ctx, frame); err != nil {
		c.forget(id)
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.forget(id)
		return nil, ctx.Err()
	case <-c.closed:
		return nil, c.terminalErr()
	case res, ok := <-ch:
		if !ok {
			// shutdown closed the pending channel before a response arrived.
			return nil, c.terminalErr()
		}
		if res.err != nil {
			return nil, res.err
		}
		return res.result, nil
	}
}

// notify sends a fire-and-forget notification (no response expected).
func (c *conn) notify(ctx context.Context, method string, params any) error {
	select {
	case <-c.closed:
		return c.terminalErr()
	default:
	}
	frame, err := encodeNotification(method, params)
	if err != nil {
		return err
	}
	return c.stream.Write(ctx, frame)
}

func (c *conn) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// shutdown closes the connection, recording the terminal cause and failing all
// outstanding calls. It is safe to call repeatedly.
func (c *conn) shutdown(cause error) {
	c.closeOnce.Do(func() {
		c.closeErr = cause
		close(c.closed)
		c.mu.Lock()
		pending := c.pending
		c.pending = make(map[int64]chan rpcResult)
		c.mu.Unlock()
		for _, ch := range pending {
			close(ch)
		}
	})
}

func (c *conn) terminalErr() error {
	if c.closeErr != nil {
		return c.closeErr
	}
	return ErrConnClosed
}

// close tears down the connection and its transport.
func (c *conn) close() error {
	err := c.stream.Close()
	c.shutdown(err)
	return err
}
