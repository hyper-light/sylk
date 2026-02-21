package bridge

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	activityBridgeName = "bridge.activity"
	activityBufferSize = 256
	// Zero uses the scope's max lifetime; activity bridge is long-lived for the UI session.
	activityDrainTimeout = 0
)

// ActivityBridge implements both events.EventSubscriber and Bridge.
// It subscribes to the ActivityEventBus and forwards events as
// msg.ActivityEventMsg to the Bubble Tea program.
type ActivityBridge struct {
	id       string
	bus      *events.ActivityEventBus
	scope    *concurrency.GoroutineScope
	buffer   chan *events.ActivityEvent
	dropped  atomic.Int64
	done     chan struct{}
	stopOnce sync.Once
}

// NewActivityBridge creates a bridge that converts ActivityEventBus events
// into Bubble Tea messages. The bridge must be registered with the bus
// via bus.Subscribe(bridge) before calling Start.
func NewActivityBridge(id string, bus *events.ActivityEventBus, scope *concurrency.GoroutineScope) *ActivityBridge {
	return &ActivityBridge{
		id:     id,
		bus:    bus,
		scope:  scope,
		buffer: make(chan *events.ActivityEvent, activityBufferSize),
		done:   make(chan struct{}),
	}
}

// -- events.EventSubscriber implementation --

// ID returns the unique subscriber identifier.
func (b *ActivityBridge) ID() string { return b.id }

// EventTypes returns nil to subscribe to all event types (wildcard).
func (b *ActivityBridge) EventTypes() []events.EventType { return nil }

// OnEvent is called by the ActivityEventBus. It enqueues the event into the
// bounded buffer, incrementing the dropped counter on backpressure.
func (b *ActivityBridge) OnEvent(event *events.ActivityEvent) error {
	select {
	case b.buffer <- event:
	default:
		b.dropped.Add(1)
	}
	return nil
}

// -- Bridge implementation --

// Start registers with the bus and launches the drain goroutine via GoroutineScope.
func (b *ActivityBridge) Start(program TeaProgram) error {
	b.bus.Subscribe(b)
	return b.scope.Go(activityBridgeName, activityDrainTimeout, b.drainFunc(program))
}

// Stop unsubscribes from the bus and signals the drain goroutine to exit.
func (b *ActivityBridge) Stop() {
	b.stopOnce.Do(func() {
		b.bus.Unsubscribe(b.id)
		close(b.done)
	})
}

// Name returns the bridge identifier.
func (b *ActivityBridge) Name() string { return activityBridgeName }

// DroppedCount returns the total number of events dropped due to backpressure.
func (b *ActivityBridge) DroppedCount() int64 { return b.dropped.Load() }

// drainFunc returns the WorkFunc that drains the buffer and sends tea messages.
func (b *ActivityBridge) drainFunc(program TeaProgram) concurrency.WorkFunc {
	return func(ctx context.Context) error {
		for {
			if stop, err := shouldStop(b.done, ctx); stop {
				return err
			}
			select {
			case event := <-b.buffer:
				program.Send(msg.ActivityEventMsg{Event: event})
			case <-b.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
