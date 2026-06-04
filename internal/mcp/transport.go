package mcp

import "context"

// messageStream is the transport seam under the JSON-RPC layer. A transport
// (stdio subprocess or Streamable HTTP) implements it; the conn frames JSON-RPC
// on top, so the protocol logic never knows which transport it is on.
//
// Contract:
//   - Write sends one already-encoded JSON-RPC frame. The conn serializes its
//     own writes, so implementations may assume a single concurrent caller.
//   - Incoming delivers received frames and is closed exactly once when the
//     transport reaches EOF or is closed. After it closes, Err reports the
//     terminal cause (nil for a clean EOF).
//   - Close is idempotent and unblocks any in-flight Write and the read loop.
type messageStream interface {
	Write(ctx context.Context, frame []byte) error
	Incoming() <-chan []byte
	Close() error
	Err() error
}
