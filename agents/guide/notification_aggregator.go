package guide

import (
	"context"
	"log/slog"
	"sync"
	"time"

	corecontext "github.com/adalundhe/sylk/core/context"
)

// NotificationAggregator debounces rapid task execution signals from the 
// orchestrator to prevent UI thrashing.
type NotificationAggregator struct {
	mu           sync.RWMutex
	tasks        map[string]*corecontext.TaskState
	lastUpdates  map[string]time.Time
	bus          EventBus
	tickInterval time.Duration
	stopCh       chan struct{}
}

func NewNotificationAggregator(bus EventBus, tickInterval time.Duration) *NotificationAggregator {
	if tickInterval == 0 {
		tickInterval = 500 * time.Millisecond // Default debounce window
	}
	return &NotificationAggregator{
		tasks:        make(map[string]*corecontext.TaskState),
		lastUpdates:  make(map[string]time.Time),
		bus:          bus,
		tickInterval: tickInterval,
		stopCh:       make(chan struct{}),
	}
}

// Start begins listening to variant requests and task events.
func (n *NotificationAggregator) Start(ctx context.Context) {
	// Subscribe to orchestrator status updates (e.g. task_complete, dag_status)
	_, _ = n.bus.SubscribeAsync("orchestrator.status", func(msg *Message) error {
		// Update internal state
		if msg.Type == MessageTypeTaskComplete || msg.Type == MessageTypeDAGStatus {
			n.updateTask(msg)
		}
		return nil
	})

	go n.loop(ctx)
}

func (n *NotificationAggregator) Stop() {
	close(n.stopCh)
}

func (n *NotificationAggregator) updateTask(msg *Message) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Parse state from the payload.
	ts, ok := msg.Payload.(corecontext.TaskState)
	if !ok {
		if ptr, okPtr := msg.Payload.(*corecontext.TaskState); okPtr {
			ts = *ptr
		} else {
			return
		}
	}

	n.tasks[msg.CorrelationID] = &ts
	n.lastUpdates[msg.CorrelationID] = time.Now()
}

func (n *NotificationAggregator) loop(ctx context.Context) {
	ticker := time.NewTicker(n.tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-n.stopCh:
			return
		case <-ticker.C:
			n.flushUpdates()
		}
	}
}

func (n *NotificationAggregator) flushUpdates() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if len(n.tasks) == 0 {
		return
	}

	// Prepare aggregated state for UI consumption
	updates := make(map[string]*corecontext.TaskState)
	for id, state := range n.tasks {
		// Only send recent updates to avoid spamming the bus
		if time.Since(n.lastUpdates[id]) <= n.tickInterval*2 {
			updates[id] = state
		}
	}

	if len(updates) > 0 {
		// Publish the batched updates to a UI-facing topic
		msg := &Message{
			ID:            generateMessageID(),
			Type:          "task_status_batch",
			SourceAgentID: "guide",
			TargetAgentID: "ui",
			Timestamp:     time.Now(),
			Payload:       updates,
		}
		if err := n.bus.Publish("ui.notifications", msg); err != nil {
			slog.Warn("notification_aggregator_publish_failed",
				"update_count", len(updates),
				"error", err.Error(),
			)
		}
	}

	// Clean up old task states after flushing
	for id, tstamp := range n.lastUpdates {
		if time.Since(tstamp) > 5*time.Minute {
			delete(n.tasks, id)
			delete(n.lastUpdates, id)
		}
	}
}
