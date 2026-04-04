package bridge

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/msg"
)

const (
	activityBridgeName = "bridge.activity"
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
type ActivityBridge struct {
	id        string
	bus       guide.EventBus
	sub       guide.Subscription
	debouncer *guide.DebouncedMessageHandler
	dropped   atomic.Int64
	stopOnce  sync.Once
}

// NewActivityBridge creates a bridge that converts ChannelBus activity
// messages into Bubble Tea messages.
func NewActivityBridge(id string, bus guide.EventBus) *ActivityBridge {
	return &ActivityBridge{
		id:  id,
		bus: bus,
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
	return nil
}

// Stop unsubscribes from the ChannelBus and releases the debouncer.
func (b *ActivityBridge) Stop() {
	b.stopOnce.Do(func() {
		if b.sub != nil {
			_ = b.sub.Unsubscribe()
		}
		if b.debouncer != nil {
			b.debouncer.Stop()
		}
	})
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
