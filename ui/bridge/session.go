package bridge

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	sessionBridgeName = "bridge.session"
	sessionBufferSize = 256
	// Zero uses the scope's max lifetime; session bridge is long-lived for the UI session.
	sessionDrainTimeout = 0
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
	stopOnce    sync.Once
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
	b.stopOnce.Do(func() {
		if b.unsubscribe != nil {
			b.unsubscribe()
		}
		close(b.done)
	})
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
//
// UI-13: every session event publishes as SessionEventMsg for the
// generic subscriber set (status bar, session picker). When the
// event is EventSwitched, the drain additionally fan-outs a typed
// SessionSwitchedMsg so the AppModel can react to the buffer-
// invalidating transition without filtering every event. Both
// messages land on the same program in a deterministic order
// (event-first, switch-second) — subscribers that handle both see
// the generic update first, preserving backwards compatibility with
// code that only watches SessionEventMsg.
func (b *SessionBridge) drainFunc(program TeaProgram) concurrency.WorkFunc {
	return func(ctx context.Context) error {
		var previousActiveID string
		for {
			if stop, err := shouldStop(b.done, ctx); stop {
				return err
			}
			select {
			case event := <-b.buffer:
				program.Send(msg.SessionEventMsg{Event: event})
				if switchMsg, ok := sessionSwitchedMsgFromEvent(event, previousActiveID); ok {
					program.Send(switchMsg)
					previousActiveID = switchMsg.ActiveID
				}
			case <-b.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// sessionSwitchedMsgFromEvent returns a typed SessionSwitchedMsg
// when event carries a session switch, or (_, false) otherwise.
// Extracted so tests can exercise the filter without constructing a
// live bridge + drain goroutine.
func sessionSwitchedMsgFromEvent(event *session.Event, previousActiveID string) (msg.SessionSwitchedMsg, bool) {
	if event == nil || event.Type != session.EventSwitched {
		return msg.SessionSwitchedMsg{}, false
	}
	if event.SessionID == "" {
		return msg.SessionSwitchedMsg{}, false
	}
	return msg.SessionSwitchedMsg{
		PreviousID: previousActiveID,
		ActiveID:   event.SessionID,
		Event:      event,
	}, true
}
