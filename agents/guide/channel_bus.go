package guide

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

type topicSubscriptions struct {
	mu   sync.RWMutex
	subs []*channelSubscription
}

var (
	ErrBusClosed          = errors.New("bus is closed")
	ErrBusCloseTimeout    = errors.New("bus close timed out waiting for handlers")
	ErrSubscriptionClosed = errors.New("subscription is closed")
	ErrInvalidHandler     = errors.New("handler cannot be nil")
)

type ChannelBus struct {
	subscriptions *ShardedMap[string, *topicSubscriptions]

	// wildcardRouter matches published topics against wildcard subscriptions.
	wildcardRouter *TopicRouter
	wildcardMu     sync.RWMutex
	wildcardIndex  map[int64]*channelSubscription // topicSub.id → channelSub

	bufferSize   int
	closeTimeout time.Duration

	closed atomic.Bool

	wg sync.WaitGroup
}

// defaultCloseTimeout is the maximum time Close() waits for subscription
// goroutines to drain before returning. Queue drain should complete in
// milliseconds; 5s provides generous safety margin.
const defaultCloseTimeout = 5 * time.Second

// maxAsyncHandlersPerSubscription caps async fan-out for a single subscription
// to prevent unbounded goroutine growth under sustained publish load.
const maxAsyncHandlersPerSubscription = 32

// maxQueueSize caps the per-subscriber message queue. When a subscriber's
// queue reaches this size, oldest messages are dropped to make room for new
// ones. This prevents unbounded memory growth when handlers are slow or
// stalled. The value is generous enough to absorb bursts (e.g. DAG layer
// dispatch) without dropping under normal operation.
const maxQueueSize = 4096

type ChannelBusConfig struct {
	BufferSize   int
	ShardCount   int
	CloseTimeout time.Duration
}

func DefaultChannelBusConfig() ChannelBusConfig {
	return ChannelBusConfig{
		BufferSize:   256,
		ShardCount:   DefaultShardCount,
		CloseTimeout: defaultCloseTimeout,
	}
}

func NewChannelBus(cfg ChannelBusConfig) *ChannelBus {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 256
	}
	if cfg.ShardCount <= 0 {
		cfg.ShardCount = DefaultShardCount
	}
	if cfg.CloseTimeout <= 0 {
		cfg.CloseTimeout = defaultCloseTimeout
	}

	return &ChannelBus{
		subscriptions:  NewStringMap[*topicSubscriptions](cfg.ShardCount),
		wildcardRouter: NewTopicRouter(),
		wildcardIndex:  make(map[int64]*channelSubscription),
		bufferSize:     cfg.BufferSize,
		closeTimeout:   cfg.CloseTimeout,
	}
}

func (b *ChannelBus) Publish(topic string, msg *Message) error {
	if b.closed.Load() {
		return ErrBusClosed
	}

	// Exact-match subscribers.
	topicSubs, ok := b.subscriptions.Get(topic)
	if ok && topicSubs != nil {
		subs := b.snapshotSubscribers(topicSubs)

		// Diagnostic: log subscriber state for stream-complete events.
		if b.isStreamComplete(msg) {
			activeSubs, closedSubs := b.classifySubscribers(subs)
			DebugFileLog().Info("Publish: STREAM_COMPLETE_SUBSCRIBER_SNAPSHOT",
				"topic", topic,
				"correlation_id", msg.CorrelationID,
				"total_subs", len(subs),
				"active_subs", activeSubs,
				"closed_subs", closedSubs)
		}

		b.publishToSubscribers(subs, msg)
	}

	// Wildcard-match subscribers.
	b.publishToWildcardMatches(topic, msg)

	return nil
}

// publishToWildcardMatches queries the TopicRouter for wildcard patterns
// matching topic and delivers msg to matched channelSubscriptions.
func (b *ChannelBus) publishToWildcardMatches(topic string, msg *Message) {
	matches := b.wildcardRouter.Match(topic)
	if len(matches) == 0 {
		return
	}

	b.wildcardMu.RLock()
	defer b.wildcardMu.RUnlock()

	for _, topicSub := range matches {
		channelSub, ok := b.wildcardIndex[topicSub.id]
		if ok {
			b.publishToSubscriber(channelSub, msg)
		}
	}
}

// isStreamComplete returns true if the message carries a StreamEventComplete.
func (b *ChannelBus) isStreamComplete(msg *Message) bool {
	if msg == nil || msg.Type != MessageTypeStream {
		return false
	}
	stream, ok := msg.GetStreamResponse()
	if !ok || stream == nil || stream.Event == nil {
		return false
	}
	return stream.Event.Type == StreamEventComplete
}

// classifySubscribers counts active vs closed subscriptions in a snapshot.
func (b *ChannelBus) classifySubscribers(subs []*channelSubscription) (active, closed int) {
	for _, sub := range subs {
		if sub.closed.Load() {
			closed++
		} else if sub.active.Load() {
			active++
		}
	}
	return
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
		DebugFileLog().Warn("publishToSubscriber: INACTIVE_DROP",
			"topic", sub.topic,
			"msg_type", string(msg.Type),
			"correlation_id", msg.CorrelationID)
		return
	}
	if sub.closed.Load() {
		DebugFileLog().Warn("publishToSubscriber: CLOSED_DROP",
			"topic", sub.topic,
			"msg_type", string(msg.Type),
			"correlation_id", msg.CorrelationID)
		return
	}
	sub.mu.Lock()
	sub.queue = append(sub.queue, msg)
	dropped := 0
	if len(sub.queue) > maxQueueSize {
		dropped = len(sub.queue) - maxQueueSize
		// Drop oldest messages to keep the queue bounded.
		// copy avoids holding references to dropped messages.
		remaining := make([]*Message, maxQueueSize)
		copy(remaining, sub.queue[dropped:])
		sub.queue = remaining
	}
	queueLen := len(sub.queue)
	sub.mu.Unlock()

	if dropped > 0 {
		DebugFileLog().Warn("publishToSubscriber: QUEUE_OVERFLOW_DROP",
			"topic", sub.topic,
			"dropped", dropped,
			"queue_len", queueLen)
	}

	// Diagnostic: confirm enqueue for stream-complete events.
	if b.isStreamComplete(msg) {
		DebugFileLog().Info("publishToSubscriber: STREAM_COMPLETE_ENQUEUED",
			"topic", sub.topic,
			"correlation_id", msg.CorrelationID,
			"queue_len", queueLen,
			"async", sub.async)
	}

	// Non-blocking signal: if a signal is already pending the consumer
	// will drain all accumulated messages (including this one) when it wakes.
	select {
	case sub.notify <- struct{}{}:
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
		notify:  make(chan struct{}, 1),
		done:    make(chan struct{}),
		handler: handler,
		async:   async,
	}
	sub.active.Store(true)

	if hasWildcard(topic) {
		b.registerWildcardSub(topic, sub)
	} else {
		b.registerExactSub(topic, sub)
	}

	b.wg.Add(1)
	go sub.run(&b.wg)

	return sub, nil
}

// registerExactSub stores the subscription under the exact topic key.
func (b *ChannelBus) registerExactSub(topic string, sub *channelSubscription) {
	topicSubs, _ := b.subscriptions.GetOrCreate(topic, func() *topicSubscriptions {
		return &topicSubscriptions{}
	})
	topicSubs.mu.Lock()
	topicSubs.subs = append(topicSubs.subs, sub)
	topicSubs.mu.Unlock()
	sub.topicShard = topicSubs
}

// registerWildcardSub registers the subscription in the TopicRouter for
// pattern matching and stores the channelSubscription in the wildcard index.
func (b *ChannelBus) registerWildcardSub(topic string, sub *channelSubscription) {
	topicSub := b.wildcardRouter.Subscribe(topic, nil) // handler unused; delivery via channelSub

	b.wildcardMu.Lock()
	b.wildcardIndex[topicSub.id] = sub
	b.wildcardMu.Unlock()

	sub.wildcardCleanup = func() {
		b.wildcardRouter.Unsubscribe(topicSub)
		b.wildcardMu.Lock()
		delete(b.wildcardIndex, topicSub.id)
		b.wildcardMu.Unlock()
	}
}

func (b *ChannelBus) Close() error {
	if b.closed.Swap(true) {
		return ErrBusClosed
	}

	b.closeAllSubscriptions()
	return b.awaitDrain()
}

// closeAllSubscriptions signals every subscription to drain and exit.
func (b *ChannelBus) closeAllSubscriptions() {
	// Exact subscriptions.
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

	// Wildcard subscriptions.
	b.wildcardMu.Lock()
	for _, sub := range b.wildcardIndex {
		sub.close()
	}
	b.wildcardIndex = nil
	b.wildcardMu.Unlock()
}

// awaitDrain waits for all subscription goroutines to finish, bounded by
// closeTimeout. Returns ErrBusCloseTimeout if handlers don't complete in time.
func (b *ChannelBus) awaitDrain() error {
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(b.closeTimeout):
		return ErrBusCloseTimeout
	}
}

type channelSubscription struct {
	bus        *ChannelBus
	topic      string
	handler    MessageHandler
	async      bool
	active     atomic.Bool
	closed     atomic.Bool
	mu         sync.Mutex   // guards queue
	queue      []*Message   // append-only from publishers, batch-drained by consumer
	notify     chan struct{} // cap-1 signal: "queue has messages"
	done       chan struct{} // closed on unsubscribe/bus-close to wake consumer
	topicShard *topicSubscriptions

	// wildcardCleanup removes this sub from the TopicRouter and wildcard index.
	// Non-nil only for wildcard subscriptions.
	wildcardCleanup func()
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

	if s.wildcardCleanup != nil {
		s.wildcardCleanup()
	} else {
		s.removeFromTopicShard()
	}

	s.close()

	return nil
}

// removeFromTopicShard removes this exact-match subscription from its shard.
func (s *channelSubscription) removeFromTopicShard() {
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
}

func (s *channelSubscription) close() {
	if s.closed.Swap(true) {
		return
	}
	close(s.done)
}

func (s *channelSubscription) run(wg *sync.WaitGroup) {
	defer wg.Done()

	var asyncLimiter chan struct{}
	if s.async {
		asyncLimiter = make(chan struct{}, maxAsyncHandlersPerSubscription)
	}

	for {
		select {
		case <-s.notify:
			s.processBatch(wg, asyncLimiter)
		case <-s.done:
			s.processBatch(wg, asyncLimiter)
			return
		}
	}
}

// processBatch drains all queued messages under a single lock acquisition
// and dispatches them to the handler (sync or async, same as before).
func (s *channelSubscription) processBatch(wg *sync.WaitGroup, asyncLimiter chan struct{}) {
	s.mu.Lock()
	batch := s.queue
	s.queue = nil
	s.mu.Unlock()

	for _, msg := range batch {
		if !s.active.Load() {
			continue
		}
		if s.async {
			asyncLimiter <- struct{}{}
			wg.Add(1)
			go s.handleMessageAsync(wg, asyncLimiter, msg)
		} else {
			s.handleMessage(msg)
		}
	}
}

// handleMessageAsync wraps handleMessage with WaitGroup tracking for async handlers.
// This ensures the bus waits for all async handlers to complete during shutdown.
func (s *channelSubscription) handleMessageAsync(wg *sync.WaitGroup, limiter chan struct{}, msg *Message) {
	defer wg.Done()
	defer func() {
		<-limiter
	}()
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

	// Include wildcard subscriptions.
	b.wildcardMu.RLock()
	for _, sub := range b.wildcardIndex {
		if sub.active.Load() {
			stats.ByTopic[sub.topic]++
			stats.Subscriptions++
			stats.Topics++
		}
	}
	b.wildcardMu.RUnlock()

	return stats
}

func (b *ChannelBus) TopicSubscriberCount(topic string) int {
	count := 0

	// Exact subscribers.
	topicSubs, ok := b.subscriptions.Get(topic)
	if ok && topicSubs != nil {
		topicSubs.mu.RLock()
		for _, sub := range topicSubs.subs {
			if sub.active.Load() {
				count++
			}
		}
		topicSubs.mu.RUnlock()
	}

	// Wildcard subscribers matching this topic.
	matches := b.wildcardRouter.Match(topic)
	b.wildcardMu.RLock()
	for _, topicSub := range matches {
		if channelSub, ok := b.wildcardIndex[topicSub.id]; ok {
			if channelSub.active.Load() {
				count++
			}
		}
	}
	b.wildcardMu.RUnlock()

	return count
}
