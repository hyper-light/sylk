package guide

import (
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/signal"
	"github.com/google/uuid"
)

// ChannelBusSignalAdapter implements signal.SignalBusInterface by routing
// signal broadcasts through the Guide ChannelBus. Signal messages are
// published to signal.<signal_type> topics and ACKs are collected via
// the signal.ack topic.
type ChannelBusSignalAdapter struct {
	bus         EventBus
	subscribers map[string]*signal.SignalSubscriber
	pending     map[string]*adapterPendingAck
	mu          sync.RWMutex
	closed      bool
	done        chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup
	ackSub      Subscription

	config ChannelBusSignalAdapterConfig
}

// ChannelBusSignalAdapterConfig provides construction parameters.
type ChannelBusSignalAdapterConfig struct {
	PendingAckTTL   time.Duration // TTL for pending ACK entries (default: 30s)
	CleanupInterval time.Duration // Interval for cleanup goroutine (default: 10s)
	ChannelBuffer   int           // Buffer size for subscriber channels (default: 100)
}

func normalizeAdapterConfig(cfg ChannelBusSignalAdapterConfig) ChannelBusSignalAdapterConfig {
	if cfg.PendingAckTTL <= 0 {
		cfg.PendingAckTTL = 30 * time.Second
	}
	if cfg.CleanupInterval <= 0 {
		cfg.CleanupInterval = 10 * time.Second
	}
	if cfg.ChannelBuffer <= 0 {
		cfg.ChannelBuffer = 100
	}
	return cfg
}

type adapterPendingAck struct {
	expected  map[string]bool
	received  []signal.SignalAck
	done      chan struct{}
	mu        sync.Mutex
	completed bool
	createdAt time.Time
}

// NewChannelBusSignalAdapter creates a ChannelBusSignalAdapter backed by
// the given EventBus. Call Close() to release resources.
func NewChannelBusSignalAdapter(bus EventBus, cfg ChannelBusSignalAdapterConfig) (*ChannelBusSignalAdapter, error) {
	cfg = normalizeAdapterConfig(cfg)

	a := &ChannelBusSignalAdapter{
		bus:         bus,
		subscribers: make(map[string]*signal.SignalSubscriber),
		pending:     make(map[string]*adapterPendingAck),
		done:        make(chan struct{}),
		config:      cfg,
	}

	// Subscribe to ACK topic to collect acknowledgments.
	sub, err := bus.SubscribeAsync(TopicSignalAck, a.handleAckMessage)
	if err != nil {
		return nil, err
	}
	a.ackSub = sub

	a.wg.Add(1)
	go a.cleanupLoop()

	return a, nil
}

// Subscribe registers a signal subscriber.
func (a *ChannelBusSignalAdapter) Subscribe(sub signal.SignalSubscriber) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return signal.ErrBusClosed
	}
	if _, exists := a.subscribers[sub.ID]; exists {
		return signal.ErrSubscriberExists
	}
	a.subscribers[sub.ID] = &sub
	return nil
}

// Unsubscribe removes a signal subscriber.
func (a *ChannelBusSignalAdapter) Unsubscribe(subscriberID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, exists := a.subscribers[subscriberID]; !exists {
		return signal.ErrSubscriberNotFound
	}
	delete(a.subscribers, subscriberID)
	return nil
}

// Broadcast publishes a signal to matching subscribers via the ChannelBus.
func (a *ChannelBusSignalAdapter) Broadcast(msg signal.SignalMessage) error {
	a.mu.RLock()
	if a.closed {
		a.mu.RUnlock()
		return signal.ErrBusClosed
	}

	// Find matching subscribers and deliver directly to their channels.
	matches := a.findMatching(msg)
	a.mu.RUnlock()

	if msg.RequiresAck {
		a.initPending(msg.ID, matches)
	}

	// Deliver to each subscriber's channel (non-blocking).
	for _, sub := range matches {
		select {
		case sub.Channel <- msg:
		default:
		}
	}

	// Also publish to the ChannelBus signal topic for any ChannelBus
	// subscribers (e.g. telemetry observers). Errors are non-fatal for the
	// direct-delivery path (subscribers above have already received the
	// signal) but must be observable so chronic failures surface.
	topic := TopicSignal(string(msg.Signal))
	busMsg := NewSignalBusMessage(msg.ID, &SignalPayload{
		Signal:      string(msg.Signal),
		TargetID:    msg.TargetID,
		Reason:      msg.Reason,
		Payload:     msg.Payload,
		RequiresAck: msg.RequiresAck,
	})
	if err := a.bus.Publish(topic, busMsg); err != nil {
		slog.Default().Warn("signal adapter: bus publish failed",
			"topic", topic,
			"signal", string(msg.Signal),
			"signal_id", msg.ID,
			"error", err)
	}

	return nil
}

// Acknowledge records an ACK from a subscriber.
func (a *ChannelBusSignalAdapter) Acknowledge(ack signal.SignalAck) error {
	a.mu.RLock()
	pa, exists := a.pending[ack.SignalID]
	a.mu.RUnlock()

	if !exists {
		return nil
	}

	pa.mu.Lock()
	defer pa.mu.Unlock()

	if pa.completed {
		return nil
	}

	pa.received = append(pa.received, ack)
	pa.expected[ack.SubscriberID] = true

	if allReceived(pa) {
		pa.completed = true
		close(pa.done)
	}

	// Also publish ACK to the ChannelBus for observability.
	ackMsg := NewSignalAckMessage("", &SignalAckPayload{
		SignalID:     ack.SignalID,
		SubscriberID: ack.SubscriberID,
		AgentID:      ack.AgentID,
		State:        ack.State,
		Checkpoint:   ack.Checkpoint,
	})
	if err := a.bus.Publish(TopicSignalAck, ackMsg); err != nil {
		slog.Default().Warn("signal adapter: ack bus publish failed",
			"topic", TopicSignalAck,
			"signal_id", ack.SignalID,
			"subscriber_id", ack.SubscriberID,
			"error", err)
	}

	return nil
}

// WaitForAcks blocks until all expected ACKs are received or timeout.
func (a *ChannelBusSignalAdapter) WaitForAcks(signalID string, timeout time.Duration) ([]signal.SignalAck, error) {
	a.mu.RLock()
	pa, exists := a.pending[signalID]
	a.mu.RUnlock()

	if !exists {
		return nil, nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-pa.done:
	case <-timer.C:
	}

	return a.collectAcks(signalID)
}

// Close shuts down the adapter, releasing resources.
func (a *ChannelBusSignalAdapter) Close() {
	a.stopOnce.Do(func() {
		close(a.done)
	})

	a.mu.Lock()
	a.closed = true

	// Complete all pending ACKs.
	for signalID, pa := range a.pending {
		pa.mu.Lock()
		if !pa.completed {
			pa.completed = true
			close(pa.done)
		}
		pa.mu.Unlock()
		delete(a.pending, signalID)
	}
	a.mu.Unlock()

	if a.ackSub != nil {
		_ = a.ackSub.Unsubscribe()
	}

	a.wg.Wait()
}

// handleAckMessage processes ACK messages from the ChannelBus.
func (a *ChannelBusSignalAdapter) handleAckMessage(msg *Message) error {
	ackPayload, ok := msg.GetSignalAckPayload()
	if !ok {
		return nil
	}

	a.mu.RLock()
	pa, exists := a.pending[ackPayload.SignalID]
	a.mu.RUnlock()

	if !exists {
		return nil
	}

	pa.mu.Lock()
	defer pa.mu.Unlock()

	if pa.completed {
		return nil
	}

	pa.expected[ackPayload.SubscriberID] = true
	pa.received = append(pa.received, signal.SignalAck{
		SignalID:     ackPayload.SignalID,
		SubscriberID: ackPayload.SubscriberID,
		AgentID:      ackPayload.AgentID,
		ReceivedAt:   time.Now(),
		State:        ackPayload.State,
		Checkpoint:   ackPayload.Checkpoint,
	})

	if allReceived(pa) {
		pa.completed = true
		close(pa.done)
	}

	return nil
}

func (a *ChannelBusSignalAdapter) findMatching(msg signal.SignalMessage) []*signal.SignalSubscriber {
	var matches []*signal.SignalSubscriber
	for _, sub := range a.subscribers {
		for _, s := range sub.Signals {
			if s == msg.Signal {
				matches = append(matches, sub)
				break
			}
		}
	}
	return matches
}

func (a *ChannelBusSignalAdapter) initPending(signalID string, subscribers []*signal.SignalSubscriber) {
	a.mu.Lock()
	defer a.mu.Unlock()

	pa := &adapterPendingAck{
		expected:  make(map[string]bool, len(subscribers)),
		received:  make([]signal.SignalAck, 0, len(subscribers)),
		done:      make(chan struct{}),
		createdAt: time.Now(),
	}
	for _, sub := range subscribers {
		pa.expected[sub.ID] = false
	}
	a.pending[signalID] = pa
}

func (a *ChannelBusSignalAdapter) collectAcks(signalID string) ([]signal.SignalAck, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	pa, exists := a.pending[signalID]
	if !exists {
		return nil, nil
	}

	acks := make([]signal.SignalAck, len(pa.received))
	copy(acks, pa.received)
	delete(a.pending, signalID)

	return acks, nil
}

func (a *ChannelBusSignalAdapter) cleanupLoop() {
	defer a.wg.Done()

	ticker := time.NewTicker(a.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			a.cleanupExpired()
		case <-a.done:
			return
		}
	}
}

func (a *ChannelBusSignalAdapter) cleanupExpired() {
	cutoff := time.Now().Add(-a.config.PendingAckTTL)

	var expired []string
	a.mu.RLock()
	for signalID, pa := range a.pending {
		if pa.createdAt.Before(cutoff) {
			expired = append(expired, signalID)
		}
	}
	a.mu.RUnlock()

	if len(expired) == 0 {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for _, signalID := range expired {
		pa, exists := a.pending[signalID]
		if !exists {
			continue
		}
		if pa.createdAt.Before(cutoff) {
			pa.mu.Lock()
			if !pa.completed {
				pa.completed = true
				close(pa.done)
			}
			pa.mu.Unlock()
			delete(a.pending, signalID)
		}
	}
}

func allReceived(pa *adapterPendingAck) bool {
	for _, acked := range pa.expected {
		if !acked {
			return false
		}
	}
	return true
}

// ChannelBufferSize returns the configured channel buffer size for subscribers.
func (a *ChannelBusSignalAdapter) ChannelBufferSize() int {
	return a.config.ChannelBuffer
}

// PendingCount returns the number of pending ACK entries (for testing/monitoring).
func (a *ChannelBusSignalAdapter) PendingCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.pending)
}

// Compile-time check: ChannelBusSignalAdapter satisfies signal.SignalBusInterface.
var _ signal.SignalBusInterface = (*ChannelBusSignalAdapter)(nil)

// ensure uuid import is used
var _ = uuid.New
