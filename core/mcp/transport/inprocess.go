package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/adalundhe/sylk/core/mcp"
)

// inProcessTransport is one end of a paired in-memory MCP wire. Both ends
// share two channels: each side's outbound messages flow to the other side's
// inbound. Used by Sylk's MCP server when an internal agent needs to drive it
// without paying for stdio framing, and by tests to exercise both peers.
type inProcessTransport struct {
	direction Direction
	send      chan<- *mcp.Message
	recv      <-chan *mcp.Message

	closeOnce sync.Once
	closeCh   chan struct{}
	closeFn   func() error
	closeErr  error

	startedMu sync.Mutex
	started   bool
}

// inProcessQueueDepth is sized to the same multiplier as the stdio inbound
// channel so neither direction observes asymmetric buffer pressure.
func inProcessQueueDepth() int { return inboundQueueDepth() }

// NewInProcessPair returns a connected pair of transports: the first speaks
// as the client, the second as the server. They share two channels so each
// peer's Send becomes the other peer's Receive.
func NewInProcessPair() (Transport, Transport) {
	clientToServer := make(chan *mcp.Message, inProcessQueueDepth())
	serverToClient := make(chan *mcp.Message, inProcessQueueDepth())
	closeCh := make(chan struct{})
	var closeOnce sync.Once
	closeBoth := func() error {
		closeOnce.Do(func() {
			close(closeCh)
			close(clientToServer)
			close(serverToClient)
		})
		return nil
	}
	client := &inProcessTransport{
		direction: DirectionClient,
		send:      clientToServer,
		recv:      serverToClient,
		closeCh:   closeCh,
		closeFn:   closeBoth,
	}
	server := &inProcessTransport{
		direction: DirectionServer,
		send:      serverToClient,
		recv:      clientToServer,
		closeCh:   closeCh,
		closeFn:   closeBoth,
	}
	return client, server
}

func (t *inProcessTransport) Endpoint() string {
	return fmt.Sprintf("inprocess:%s", t.direction)
}

func (t *inProcessTransport) Start(ctx context.Context) error {
	t.startedMu.Lock()
	defer t.startedMu.Unlock()
	if t.started {
		return errors.New("mcp inprocess: already started")
	}
	t.started = true
	return nil
}

func (t *inProcessTransport) Send(ctx context.Context, msg *mcp.Message) error {
	select {
	case <-t.closeCh:
		return ErrTransportClosed
	case <-ctx.Done():
		return ctx.Err()
	case t.send <- msg:
		return nil
	}
}

func (t *inProcessTransport) Receive(ctx context.Context) (*mcp.Message, error) {
	select {
	case <-t.closeCh:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-t.recv:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	}
}

func (t *inProcessTransport) Close() error {
	t.closeOnce.Do(func() {
		if t.closeFn != nil {
			t.closeErr = t.closeFn()
		}
	})
	return t.closeErr
}
