package bridge

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	activityBridgeName = "bridge.activity"
	// Zero uses the scope's max lifetime; the activity bridge runs for the
	// entire TUI session and is torn down by scope cancellation at exit.
	activityBridgeDrainTimeout = 0
)

var (
	bridgeDebugLog     *slog.Logger
	bridgeDebugLogOnce sync.Once
)

func bridgeEventDebugLog() *slog.Logger {
	bridgeDebugLogOnce.Do(func() {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".sylk", "logs")
		os.MkdirAll(dir, 0755)
		f, err := os.OpenFile(filepath.Join(dir, "ui_events.log"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			bridgeDebugLog = slog.Default()
			return
		}
		bridgeDebugLog = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	return bridgeDebugLog
}

// ActivityBridge subscribes to ChannelBus TopicActivity and forwards
// activity events as msg.ActivityEventMsg to the Bubble Tea program.
// Events are filtered (UserVisible only) and deduplicated before delivery.
//
// UI-12: the bridge accepts a GoroutineScope so its shutdown-watch goroutine
// is tracked alongside every other UI scope consumer. Scope cancellation
// causes a clean unsubscribe + debouncer stop, so no goroutine leaks past
// the TUI's scope lifecycle. Stop() is idempotent via an atomic flag — the
// former sync.Once is no longer needed because Unsubscribe is itself atomic
// and the debouncer's Stop is idempotent by the same pattern.
type ActivityBridge struct {
	id        string
	bus       guide.EventBus
	scope     *concurrency.GoroutineScope
	sub       guide.Subscription
	debouncer *guide.DebouncedMessageHandler
	dropped   atomic.Int64
	done      chan struct{}
	stopped   atomic.Bool
}

// NewActivityBridge creates a bridge that converts ChannelBus activity
// messages into Bubble Tea messages. scope may be nil for tests that
// exercise only Start/Stop without requiring scope-tracked goroutines.
func NewActivityBridge(id string, bus guide.EventBus, scope *concurrency.GoroutineScope) *ActivityBridge {
	return &ActivityBridge{
		id:    id,
		bus:   bus,
		scope: scope,
		done:  make(chan struct{}),
	}
}

// -- Bridge implementation --

// Start subscribes to TopicActivity on the ChannelBus with filtering
// and deduplication, forwarding matching events to the Bubble Tea program.
func (b *ActivityBridge) Start(program TeaProgram) error {
	forwarder := b.forwardHandler(program)
	b.debouncer = guide.NewDebouncedMessageHandler(forwarder)
	filtered := guide.NewFilteredActivityHandler(b.debouncer.Handle, guide.UserVisibleFilter())

	sub, err := b.bus.SubscribeAsync(guide.TopicActivity, filtered)
	if err != nil {
		b.debouncer.Stop()
		b.debouncer = nil
		return err
	}
	b.sub = sub
	if b.scope != nil {
		if err := b.scope.Go(activityBridgeName, activityBridgeDrainTimeout, b.scopeWatcher()); err != nil {
			_ = sub.Unsubscribe()
			b.debouncer.Stop()
			b.sub = nil
			b.debouncer = nil
			return err
		}
	}
	return nil
}

// Stop unsubscribes from the ChannelBus and releases the debouncer.
// Idempotent via the stopped atomic flag.
func (b *ActivityBridge) Stop() {
	if !b.stopped.CompareAndSwap(false, true) {
		return
	}
	close(b.done)
	b.teardown()
}

// teardown unsubscribes and stops the debouncer. Safe to call multiple times
// — Unsubscribe and debouncer.Stop are both atomic no-ops after the first
// invocation.
func (b *ActivityBridge) teardown() {
	if b.sub != nil {
		_ = b.sub.Unsubscribe()
	}
	if b.debouncer != nil {
		b.debouncer.Stop()
	}
}

// scopeWatcher is the WorkFunc run inside the GoroutineScope. It blocks
// until either Stop() is called or the scope context cancels, then tears
// down the subscription. This guarantees the bridge unsubscribes as part
// of scope shutdown rather than leaking past it.
func (b *ActivityBridge) scopeWatcher() concurrency.WorkFunc {
	return func(ctx context.Context) error {
		select {
		case <-b.done:
			return nil
		case <-ctx.Done():
			// Scope cancellation fires before user-initiated Stop. Mark
			// stopped so a subsequent Stop() is a no-op, tear down in
			// place, and surface ctx.Err so the scope logs a clean
			// cancellation reason.
			if b.stopped.CompareAndSwap(false, true) {
				close(b.done)
				b.teardown()
			}
			return ctx.Err()
		}
	}
}

// Name returns the bridge identifier.
func (b *ActivityBridge) Name() string { return activityBridgeName }

// DroppedCount returns the total number of events dropped due to backpressure.
func (b *ActivityBridge) DroppedCount() int64 { return b.dropped.Load() }

// forwardHandler returns a MessageHandler that extracts the ActivityEvent
// from a ChannelBus message and sends it to the Bubble Tea program.
func (b *ActivityBridge) forwardHandler(program TeaProgram) guide.MessageHandler {
	return func(m *guide.Message) error {
		event, ok := m.GetActivityEvent()
		if !ok {
			return nil
		}
		event = normalizeActivityEventForUI(event)
		b.logEvent(event)
		program.Send(msg.ActivityEventMsg{Event: event})
		return nil
	}
}

func (b *ActivityBridge) logEvent(event *events.ActivityEvent) {
	bridgeEventDebugLog().Info("activity_bridge: forward",
		"agent_id", event.AgentID,
		"event_type", event.EventType,
		"content", event.Content,
		"outcome", event.Outcome,
		"event_ts", event.Timestamp.Format(time.RFC3339Nano),
		"forward_ts", time.Now().Format(time.RFC3339Nano))
}
