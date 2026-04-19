package signal

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// DroppedSignalEvent is fired to an installed OverflowListener whenever a
// broadcast cannot be enqueued on a subscriber's channel (channel full).
// Keep listener handlers fast — they run in the broadcast critical path.
type DroppedSignalEvent struct {
	SubscriberID string
	AgentID      string
	Signal       Signal
	SignalID     string
	TargetID     string
	TotalDropped uint64 // cumulative drops on this bus
}

// OverflowListener is a callback invoked when a signal is dropped due to a
// full subscriber channel. A nil listener disables the callback; logs fire
// regardless.
type OverflowListener func(DroppedSignalEvent)

var (
	ErrSubscriberExists   = errors.New("subscriber already exists")
	ErrSubscriberNotFound = errors.New("subscriber not found")
	ErrBusClosed          = errors.New("signal bus is closed")
	ErrAckTimeout         = errors.New("acknowledgment timeout")
)

type SignalBusConfig struct {
	DefaultTimeout    time.Duration
	RetryCount        int
	ChannelBufferSize int
	PendingAckTTL     time.Duration // TTL for pending ACK entries (default: 30s)
	CleanupInterval   time.Duration // Interval for cleanup goroutine (default: 10s)
}

func DefaultSignalBusConfig() SignalBusConfig {
	return SignalBusConfig{
		DefaultTimeout:    5 * time.Second,
		RetryCount:        1,
		ChannelBufferSize: 100,
		PendingAckTTL:     30 * time.Second,
		CleanupInterval:   10 * time.Second,
	}
}

type pendingAck struct {
	expected  map[string]bool
	received  []SignalAck
	done      chan struct{}
	mu        sync.Mutex
	completed bool
	createdAt time.Time // Timestamp for TTL-based cleanup
}

type SignalBus struct {
	config      SignalBusConfig
	subscribers map[string]*SignalSubscriber
	pending     map[string]*pendingAck
	mu          sync.RWMutex
	closed      bool
	done        chan struct{}
	stopOnce    sync.Once

	// droppedCount tallies broadcasts that could not be enqueued on a
	// subscriber channel due to channel-full conditions.
	droppedCount atomic.Uint64

	// overflowListener, when non-nil, is called synchronously on each drop.
	overflowListener atomic.Value // of OverflowListener
}

// SetOverflowListener installs (or replaces) the overflow listener. Pass nil
// to clear. The listener must be fast — it runs in the broadcast path.
func (b *SignalBus) SetOverflowListener(fn OverflowListener) {
	if fn == nil {
		b.overflowListener.Store(OverflowListener(nil))
		return
	}
	b.overflowListener.Store(fn)
}

func (b *SignalBus) loadOverflowListener() OverflowListener {
	v := b.overflowListener.Load()
	if v == nil {
		return nil
	}
	fn, _ := v.(OverflowListener)
	return fn
}

// DroppedCount returns the cumulative number of signal messages dropped due
// to subscriber-channel overflow since the bus was created.
func (b *SignalBus) DroppedCount() uint64 {
	return b.droppedCount.Load()
}

func NewSignalBus(config SignalBusConfig) *SignalBus {
	config = normalizeSignalBusConfig(config)
	b := &SignalBus{
		config:      config,
		subscribers: make(map[string]*SignalSubscriber),
		pending:     make(map[string]*pendingAck),
		done:        make(chan struct{}),
	}
	go b.cleanupLoop()
	return b
}

func normalizeSignalBusConfig(config SignalBusConfig) SignalBusConfig {
	if config.PendingAckTTL <= 0 {
		config.PendingAckTTL = 30 * time.Second
	}
	if config.CleanupInterval <= 0 {
		config.CleanupInterval = 10 * time.Second
	}
	return config
}

func (b *SignalBus) Subscribe(sub SignalSubscriber) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return ErrBusClosed
	}

	if _, exists := b.subscribers[sub.ID]; exists {
		return ErrSubscriberExists
	}

	b.subscribers[sub.ID] = &sub
	return nil
}

func (b *SignalBus) Unsubscribe(subscriberID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.subscribers[subscriberID]; !exists {
		return ErrSubscriberNotFound
	}

	delete(b.subscribers, subscriberID)
	return nil
}

func (b *SignalBus) Broadcast(msg SignalMessage) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return ErrBusClosed
	}

	subscribers := b.findMatchingSubscribers(msg)
	b.mu.RUnlock()

	if msg.RequiresAck {
		b.initPendingAck(msg.ID, subscribers)
	}

	b.sendToSubscribers(subscribers, msg)
	return nil
}

func (b *SignalBus) findMatchingSubscribers(msg SignalMessage) []*SignalSubscriber {
	var matches []*SignalSubscriber
	for _, sub := range b.subscribers {
		if b.subscriberMatchesSignal(sub, msg.Signal) {
			matches = append(matches, sub)
		}
	}
	return matches
}

func (b *SignalBus) subscriberMatchesSignal(sub *SignalSubscriber, sig Signal) bool {
	for _, s := range sub.Signals {
		if s == sig {
			return true
		}
	}
	return false
}

func (b *SignalBus) initPendingAck(signalID string, subscribers []*SignalSubscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()

	pa := &pendingAck{
		expected:  make(map[string]bool),
		received:  make([]SignalAck, 0),
		done:      make(chan struct{}),
		createdAt: time.Now(),
	}

	for _, sub := range subscribers {
		pa.expected[sub.ID] = false
	}

	b.pending[signalID] = pa
}

func (b *SignalBus) sendToSubscribers(subscribers []*SignalSubscriber, msg SignalMessage) {
	for _, sub := range subscribers {
		b.sendToSubscriber(sub, msg)
	}
}

func (b *SignalBus) sendToSubscriber(sub *SignalSubscriber, msg SignalMessage) {
	select {
	case sub.Channel <- msg:
	default:
		b.recordDrop(sub, msg)
	}
}

func (b *SignalBus) recordDrop(sub *SignalSubscriber, msg SignalMessage) {
	total := b.droppedCount.Add(1)
	slog.Default().Warn("signal bus drop: subscriber channel full",
		"subscriber_id", sub.ID,
		"agent_id", sub.AgentID,
		"signal", string(msg.Signal),
		"signal_id", msg.ID,
		"target_id", msg.TargetID,
		"total_dropped", total)
	if listener := b.loadOverflowListener(); listener != nil {
		listener(DroppedSignalEvent{
			SubscriberID: sub.ID,
			AgentID:      sub.AgentID,
			Signal:       msg.Signal,
			SignalID:     msg.ID,
			TargetID:     msg.TargetID,
			TotalDropped: total,
		})
	}
}

func (b *SignalBus) Acknowledge(ack SignalAck) error {
	b.mu.RLock()
	pa, exists := b.pending[ack.SignalID]
	b.mu.RUnlock()

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

	if b.allAcksReceived(pa) {
		pa.completed = true
		close(pa.done)
	}

	return nil
}

func (b *SignalBus) allAcksReceived(pa *pendingAck) bool {
	for _, acked := range pa.expected {
		if !acked {
			return false
		}
	}
	return true
}

func (b *SignalBus) WaitForAcks(signalID string, timeout time.Duration) ([]SignalAck, error) {
	b.mu.RLock()
	pa, exists := b.pending[signalID]
	b.mu.RUnlock()

	if !exists {
		return nil, nil
	}

	select {
	case <-pa.done:
		return b.collectAcks(signalID)
	case <-time.After(timeout):
		return b.collectAcks(signalID)
	}
}

func (b *SignalBus) collectAcks(signalID string) ([]SignalAck, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	pa, exists := b.pending[signalID]
	if !exists {
		return nil, nil
	}

	acks := make([]SignalAck, len(pa.received))
	copy(acks, pa.received)
	delete(b.pending, signalID)

	return acks, nil
}

func (b *SignalBus) Close() {
	b.stopOnce.Do(func() {
		close(b.done)
	})

	b.mu.Lock()
	defer b.mu.Unlock()

	b.closed = true

	for signalID, pa := range b.pending {
		pa.mu.Lock()
		if !pa.completed {
			pa.completed = true
			close(pa.done)
		}
		pa.mu.Unlock()
		delete(b.pending, signalID)
	}
}

// cleanupLoop periodically removes expired pending ACK entries.
func (b *SignalBus) cleanupLoop() {
	ticker := time.NewTicker(b.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.cleanupExpiredPendingAcks()
		case <-b.done:
			return
		}
	}
}

// cleanupExpiredPendingAcks removes pending ACK entries that have exceeded TTL.
func (b *SignalBus) cleanupExpiredPendingAcks() {
	cutoff := time.Now().Add(-b.config.PendingAckTTL)
	var expired []string

	b.mu.RLock()
	for signalID, pa := range b.pending {
		if pa.createdAt.Before(cutoff) {
			expired = append(expired, signalID)
		}
	}
	b.mu.RUnlock()

	if len(expired) == 0 {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	for _, signalID := range expired {
		pa, exists := b.pending[signalID]
		if !exists {
			continue
		}
		// Re-check under write lock to avoid race condition
		if pa.createdAt.Before(cutoff) {
			pa.mu.Lock()
			if !pa.completed {
				pa.completed = true
				close(pa.done)
			}
			pa.mu.Unlock()
			delete(b.pending, signalID)
		}
	}
}

// ChannelBufferSize returns the configured channel buffer size for subscribers.
func (b *SignalBus) ChannelBufferSize() int {
	return b.config.ChannelBufferSize
}

// PendingCount returns the number of pending ACK entries (for testing/monitoring).
func (b *SignalBus) PendingCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.pending)
}
