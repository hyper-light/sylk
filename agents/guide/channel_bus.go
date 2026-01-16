package guide

import (
	"errors"
	"sync"
	"sync/atomic"
)

// =============================================================================
// Channel Bus Implementation
// =============================================================================
//
// ChannelBus is an in-process event bus using Go channels.
// Suitable for single-process deployments with many agents.
//
// Features:
// - Buffered channels prevent blocking publishers
// - Multiple subscribers per topic
// - Async handler execution option
// - Graceful shutdown

var (
	ErrBusClosed         = errors.New("bus is closed")
	ErrSubscriptionClosed = errors.New("subscription is closed")
	ErrInvalidHandler    = errors.New("handler cannot be nil")
)

// ChannelBus implements EventBus using Go channels
type ChannelBus struct {
	mu sync.RWMutex

	// Subscriptions by topic
	subscriptions map[string][]*channelSubscription

	// Configuration
	bufferSize int

	// State
	closed atomic.Bool

	// Wait group for async handlers
	wg sync.WaitGroup
}

// ChannelBusConfig configures the channel bus
type ChannelBusConfig struct {
	// BufferSize is the buffer size for subscription channels.
	// Larger buffers reduce blocking but use more memory.
	// Default: 256
	BufferSize int
}

// DefaultChannelBusConfig returns sensible defaults
func DefaultChannelBusConfig() ChannelBusConfig {
	return ChannelBusConfig{
		BufferSize: 256,
	}
}

// NewChannelBus creates a new channel-based event bus
func NewChannelBus(cfg ChannelBusConfig) *ChannelBus {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 256
	}

	return &ChannelBus{
		subscriptions: make(map[string][]*channelSubscription),
		bufferSize:    cfg.BufferSize,
	}
}

// =============================================================================
// EventBus Implementation
// =============================================================================

// Publish sends a message to all subscribers of a topic
func (b *ChannelBus) Publish(topic string, msg *Message) error {
	if b.closed.Load() {
		return ErrBusClosed
	}

	b.mu.RLock()
	subs := b.subscriptions[topic]
	b.mu.RUnlock()

	// Send to all subscribers (non-blocking with buffered channels)
	for _, sub := range subs {
		if sub.active.Load() {
			select {
			case sub.ch <- msg:
				// Delivered
			default:
				// Buffer full - message dropped
				// Could add metrics/logging here
			}
		}
	}

	return nil
}

// Subscribe registers a synchronous handler for a topic
func (b *ChannelBus) Subscribe(topic string, handler MessageHandler) (Subscription, error) {
	return b.subscribe(topic, handler, false)
}

// SubscribeAsync registers an async handler that runs in a goroutine
func (b *ChannelBus) SubscribeAsync(topic string, handler MessageHandler) (Subscription, error) {
	return b.subscribe(topic, handler, true)
}

func (b *ChannelBus) subscribe(topic string, handler MessageHandler, async bool) (Subscription, error) {
	if b.closed.Load() {
		return nil, ErrBusClosed
	}
	if handler == nil {
		return nil, ErrInvalidHandler
	}

	sub := &channelSubscription{
		bus:     b,
		topic:   topic,
		ch:      make(chan *Message, b.bufferSize),
		handler: handler,
		async:   async,
	}
	sub.active.Store(true)

	// Register subscription
	b.mu.Lock()
	b.subscriptions[topic] = append(b.subscriptions[topic], sub)
	b.mu.Unlock()

	// Start message processing goroutine
	b.wg.Add(1)
	go sub.run(&b.wg)

	return sub, nil
}

// Close shuts down the bus and all subscriptions
func (b *ChannelBus) Close() error {
	if b.closed.Swap(true) {
		return ErrBusClosed // Already closed
	}

	b.mu.Lock()
	// Close all subscription channels
	for _, subs := range b.subscriptions {
		for _, sub := range subs {
			sub.close()
		}
	}
	b.subscriptions = make(map[string][]*channelSubscription)
	b.mu.Unlock()

	// Wait for all handlers to complete
	b.wg.Wait()

	return nil
}

// =============================================================================
// Channel Subscription
// =============================================================================

type channelSubscription struct {
	bus     *ChannelBus
	topic   string
	ch      chan *Message
	handler MessageHandler
	async   bool
	active  atomic.Bool
	closed  atomic.Bool
}

func (s *channelSubscription) Topic() string {
	return s.topic
}

func (s *channelSubscription) IsActive() bool {
	return s.active.Load() && !s.closed.Load()
}

func (s *channelSubscription) Unsubscribe() error {
	if !s.active.Swap(false) {
		return ErrSubscriptionClosed
	}

	// Remove from bus
	s.bus.mu.Lock()
	subs := s.bus.subscriptions[s.topic]
	for i, sub := range subs {
		if sub == s {
			s.bus.subscriptions[s.topic] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	s.bus.mu.Unlock()

	// Close channel (will cause run() to exit)
	s.close()

	return nil
}

func (s *channelSubscription) close() {
	if s.closed.Swap(true) {
		return // Already closed
	}
	close(s.ch)
}

func (s *channelSubscription) run(wg *sync.WaitGroup) {
	defer wg.Done()

	for msg := range s.ch {
		if !s.active.Load() {
			continue
		}

		if s.async {
			// Async: run handler in separate goroutine
			go s.handleMessage(msg)
		} else {
			// Sync: run handler in this goroutine
			s.handleMessage(msg)
		}
	}
}

func (s *channelSubscription) handleMessage(msg *Message) {
	// Recover from handler panics to prevent bus crash
	defer func() {
		if r := recover(); r != nil {
			// Could log panic here
			_ = r
		}
	}()

	_ = s.handler(msg)
}

// =============================================================================
// Bus Statistics
// =============================================================================

// ChannelBusStats contains statistics about the bus
type ChannelBusStats struct {
	Topics          int            `json:"topics"`
	Subscriptions   int            `json:"subscriptions"`
	ByTopic         map[string]int `json:"by_topic"`
	BufferSize      int            `json:"buffer_size"`
	Closed          bool           `json:"closed"`
}

// Stats returns statistics about the bus
func (b *ChannelBus) Stats() ChannelBusStats {
	b.mu.RLock()
	defer b.mu.RUnlock()

	stats := ChannelBusStats{
		Topics:     len(b.subscriptions),
		ByTopic:    make(map[string]int),
		BufferSize: b.bufferSize,
		Closed:     b.closed.Load(),
	}

	for topic, subs := range b.subscriptions {
		activeSubs := 0
		for _, sub := range subs {
			if sub.active.Load() {
				activeSubs++
			}
		}
		stats.ByTopic[topic] = activeSubs
		stats.Subscriptions += activeSubs
	}

	return stats
}

// TopicSubscriberCount returns the number of active subscribers for a topic
func (b *ChannelBus) TopicSubscriberCount(topic string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	count := 0
	for _, sub := range b.subscriptions[topic] {
		if sub.active.Load() {
			count++
		}
	}
	return count
}
