package signal

import (
	"errors"
	"sync"
	"time"
)

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
}

func DefaultSignalBusConfig() SignalBusConfig {
	return SignalBusConfig{
		DefaultTimeout:    5 * time.Second,
		RetryCount:        1,
		ChannelBufferSize: 100,
	}
}

type pendingAck struct {
	expected  map[string]bool
	received  []SignalAck
	done      chan struct{}
	mu        sync.Mutex
	completed bool
}

type SignalBus struct {
	config      SignalBusConfig
	subscribers map[string]*SignalSubscriber
	pending     map[string]*pendingAck
	mu          sync.RWMutex
	closed      bool
}

func NewSignalBus(config SignalBusConfig) *SignalBus {
	return &SignalBus{
		config:      config,
		subscribers: make(map[string]*SignalSubscriber),
		pending:     make(map[string]*pendingAck),
	}
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
		expected: make(map[string]bool),
		received: make([]SignalAck, 0),
		done:     make(chan struct{}),
	}

	for _, sub := range subscribers {
		pa.expected[sub.ID] = false
	}

	b.pending[signalID] = pa
}

func (b *SignalBus) sendToSubscribers(subscribers []*SignalSubscriber, msg SignalMessage) {
	for _, sub := range subscribers {
		select {
		case sub.Channel <- msg:
		default:
		}
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
	b.mu.Lock()
	defer b.mu.Unlock()

	b.closed = true

	for _, pa := range b.pending {
		pa.mu.Lock()
		if !pa.completed {
			pa.completed = true
			close(pa.done)
		}
		pa.mu.Unlock()
	}
}
