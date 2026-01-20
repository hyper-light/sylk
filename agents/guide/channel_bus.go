package guide

import (
	"errors"
	"sync"
	"sync/atomic"
)

type topicSubscriptions struct {
	mu   sync.RWMutex
	subs []*channelSubscription
}

var (
	ErrBusClosed          = errors.New("bus is closed")
	ErrSubscriptionClosed = errors.New("subscription is closed")
	ErrInvalidHandler     = errors.New("handler cannot be nil")
)

type ChannelBus struct {
	subscriptions *ShardedMap[string, *topicSubscriptions]

	bufferSize int

	closed atomic.Bool

	wg sync.WaitGroup
}

type ChannelBusConfig struct {
	BufferSize int
	ShardCount int
}

func DefaultChannelBusConfig() ChannelBusConfig {
	return ChannelBusConfig{
		BufferSize: 256,
		ShardCount: DefaultShardCount,
	}
}

func NewChannelBus(cfg ChannelBusConfig) *ChannelBus {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 256
	}
	if cfg.ShardCount <= 0 {
		cfg.ShardCount = DefaultShardCount
	}

	return &ChannelBus{
		subscriptions: NewStringMap[*topicSubscriptions](cfg.ShardCount),
		bufferSize:    cfg.BufferSize,
	}
}

func (b *ChannelBus) Publish(topic string, msg *Message) error {
	if b.closed.Load() {
		return ErrBusClosed
	}

	topicSubs, ok := b.subscriptions.Get(topic)
	if !ok || topicSubs == nil {
		return nil
	}

	subs := b.snapshotSubscribers(topicSubs)
	b.publishToSubscribers(subs, msg)

	return nil
}

func (b *ChannelBus) snapshotSubscribers(topicSubs *topicSubscriptions) []*channelSubscription {
	topicSubs.mu.RLock()
	defer topicSubs.mu.RUnlock()
	return append([]*channelSubscription(nil), topicSubs.subs...)
}

func (b *ChannelBus) publishToSubscribers(subs []*channelSubscription, msg *Message) {
	for _, sub := range subs {
		b.publishToSubscriber(sub, msg)
	}
}

func (b *ChannelBus) publishToSubscriber(sub *channelSubscription, msg *Message) {
	if !sub.active.Load() {
		return
	}
	select {
	case sub.ch <- msg:
	default:
	}
}

func (b *ChannelBus) Subscribe(topic string, handler MessageHandler) (Subscription, error) {
	return b.subscribe(topic, handler, false)
}

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

	// W12.27: Use GetOrCreate with factory to avoid check-then-act race.
	// The factory is only called if the topic doesn't exist, and the entire
	// operation is atomic (factory runs under shard lock).
	topicSubs, _ := b.subscriptions.GetOrCreate(topic, func() *topicSubscriptions {
		return &topicSubscriptions{}
	})
	topicSubs.mu.Lock()
	topicSubs.subs = append(topicSubs.subs, sub)
	topicSubs.mu.Unlock()
	sub.topicShard = topicSubs

	b.wg.Add(1)
	go sub.run(&b.wg)

	return sub, nil
}

func (b *ChannelBus) Close() error {
	if b.closed.Swap(true) {
		return ErrBusClosed
	}

	allTopics := b.subscriptions.Snapshot()
	for _, topicSubs := range allTopics {
		if topicSubs == nil {
			continue
		}
		topicSubs.mu.Lock()
		for _, sub := range topicSubs.subs {
			sub.close()
		}
		topicSubs.subs = nil
		topicSubs.mu.Unlock()
	}
	b.subscriptions.Clear()

	b.wg.Wait()

	return nil
}

type channelSubscription struct {
	bus        *ChannelBus
	topic      string
	ch         chan *Message
	handler    MessageHandler
	async      bool
	active     atomic.Bool
	closed     atomic.Bool
	topicShard *topicSubscriptions
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

	topicSubs := s.topicShard
	if topicSubs == nil {
		if looked, ok := s.bus.subscriptions.Get(s.topic); ok {
			topicSubs = looked
		}
	}
	if topicSubs != nil {
		topicSubs.mu.Lock()
		updated := topicSubs.subs[:0]
		for _, sub := range topicSubs.subs {
			if sub != s {
				updated = append(updated, sub)
			}
		}
		topicSubs.subs = updated
		topicSubs.mu.Unlock()
	}

	s.close()

	return nil
}

func (s *channelSubscription) close() {
	if s.closed.Swap(true) {
		return
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
			// Track async handler goroutines for proper shutdown
			wg.Add(1)
			go s.handleMessageAsync(wg, msg)
		} else {
			s.handleMessage(msg)
		}
	}
}

// handleMessageAsync wraps handleMessage with WaitGroup tracking for async handlers.
// This ensures the bus waits for all async handlers to complete during shutdown.
func (s *channelSubscription) handleMessageAsync(wg *sync.WaitGroup, msg *Message) {
	defer wg.Done()
	s.handleMessage(msg)
}

func (s *channelSubscription) handleMessage(msg *Message) {
	defer s.recoverPanic()
	_ = s.handler(msg)
}

func (s *channelSubscription) recoverPanic() {
	if r := recover(); r != nil {
		_ = r
	}
}

type ChannelBusStats struct {
	Topics        int            `json:"topics"`
	Subscriptions int            `json:"subscriptions"`
	ByTopic       map[string]int `json:"by_topic"`
	BufferSize    int            `json:"buffer_size"`
	Closed        bool           `json:"closed"`
}

func (b *ChannelBus) Stats() ChannelBusStats {
	stats := ChannelBusStats{
		ByTopic:    make(map[string]int),
		BufferSize: b.bufferSize,
		Closed:     b.closed.Load(),
	}

	snapshot := b.subscriptions.Snapshot()
	stats.Topics = len(snapshot)

	for topic, topicSubs := range snapshot {
		if topicSubs == nil {
			continue
		}
		topicSubs.mu.RLock()
		activeSubs := 0
		for _, sub := range topicSubs.subs {
			if sub.active.Load() {
				activeSubs++
			}
		}
		topicSubs.mu.RUnlock()
		stats.ByTopic[topic] = activeSubs
		stats.Subscriptions += activeSubs
	}

	return stats
}

func (b *ChannelBus) TopicSubscriberCount(topic string) int {
	topicSubs, ok := b.subscriptions.Get(topic)
	if !ok || topicSubs == nil {
		return 0
	}

	topicSubs.mu.RLock()
	count := 0
	for _, sub := range topicSubs.subs {
		if sub.active.Load() {
			count++
		}
	}
	topicSubs.mu.RUnlock()
	return count
}
