// Package transport defines the wire-level transports MCP can run over.
//
// Every transport satisfies the same Transport interface so the client and
// server packages can swap stdio, HTTP, SSE, or in-process without branching.
// Each Transport is single-use: a Close() shuts down both the underlying
// connection and any goroutines spawned to service it.
package transport

import (
	"context"
	"errors"

	"github.com/adalundhe/sylk/core/mcp"
)

// ErrTransportClosed signals an attempt to use a closed transport.
var ErrTransportClosed = errors.New("mcp transport: closed")

// Transport is the bidirectional MCP wire surface.
//
// Send must be safe for concurrent calls. Receive returns one message per call
// and may block until a frame arrives or the transport closes; on close it
// returns the close error (or io.EOF for a clean end). Implementations MUST
// fan their goroutines through a caller-supplied concurrency.GoroutineScope so
// the broader Sylk leak-detection / shutdown machinery owns lifetime.
type Transport interface {
	// Start performs any I/O needed to bring the wire up (process spawn,
	// HTTP handshake, etc.). Called exactly once before Send/Receive.
	Start(ctx context.Context) error
	// Send writes a frame and returns when the frame is on the wire (or
	// queued for delivery). Returns ErrTransportClosed after Close.
	Send(ctx context.Context, msg *mcp.Message) error
	// Receive blocks until the next frame is available. Returns
	// io.EOF on clean close, ErrTransportClosed if Close already happened.
	Receive(ctx context.Context) (*mcp.Message, error)
	// Close releases all resources. Multiple calls return the first error.
	Close() error
	// Endpoint returns a human-readable identifier for diagnostics.
	Endpoint() string
}

// Direction enumerates which end of an in-process pair a transport speaks.
// Used only by the in-process transport.
type Direction string

const (
	DirectionClient Direction = "client"
	DirectionServer Direction = "server"
)
