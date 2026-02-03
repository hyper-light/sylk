package bridge

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	sessionBridgeName   = "bridge.session"
	sessionBufferSize   = 256
	sessionDrainTimeout = 30 * time.Second
)

// SessionBridge subscribes to session.Manager lifecycle events and forwards
// them as msg.SessionEventMsg to the Bubble Tea program.
type SessionBridge struct {
	manager     *session.Manager
	scope       *concurrency.GoroutineScope
	buffer      chan *session.Event
	dropped     atomic.Int64
	done        chan struct{}
	unsubscribe func()
}

// NewSessionBridge creates a bridge that converts session.Manager events
// into Bubble Tea messages.
func NewSessionBridge(manager *session.Manager, scope *concurrency.GoroutineScope) *SessionBridge {
	return &SessionBridge{
		manager: manager,
		scope:   scope,
		buffer:  make(chan *session.Event, sessionBufferSize),
		done:    make(chan struct{}),
	}
}

// -- Bridge implementation --

// Start subscribes to the session manager and launches the drain goroutine.
func (b *SessionBridge) Start(program TeaProgram) error {
	b.unsubscribe = b.manager.Subscribe(b.enqueue)
	return b.scope.Go(sessionBridgeName, sessionDrainTimeout, b.drainFunc(program))
}

// Stop unsubscribes from the manager and signals the drain goroutine to exit.
func (b *SessionBridge) Stop() {
	if b.unsubscribe != nil {
		b.unsubscribe()
	}
	close(b.done)
}

// Name returns the bridge identifier.
func (b *SessionBridge) Name() string { return sessionBridgeName }

// DroppedCount returns the total number of events dropped due to backpressure.
func (b *SessionBridge) DroppedCount() int64 { return b.dropped.Load() }

// enqueue is the session.EventHandler passed to Manager.Subscribe.
// It pushes events into the bounded buffer, counting drops on backpressure.
func (b *SessionBridge) enqueue(event *session.Event) {
	select {
	case b.buffer <- event:
	default:
		b.dropped.Add(1)
	}
}

// drainFunc returns the WorkFunc that drains the buffer and sends tea messages.
func (b *SessionBridge) drainFunc(program TeaProgram) concurrency.WorkFunc {
	return func(ctx context.Context) error {
		for {
			select {
			case event := <-b.buffer:
				program.Send(msg.SessionEventMsg{Event: event})
			case <-b.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
