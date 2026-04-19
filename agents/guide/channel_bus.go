package guide

import (
	"errors"
	"fmt"
	"runtime/debug"
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
	// ErrMessageExpired is returned by Publish when msg.IsExpired() is true
	// at the time of publish. Callers can errors.Is against this sentinel
	// to distinguish TTL/deadline rejection from other publish failures.
	ErrMessageExpired = errors.New("message expired before publish")
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

	// wg tracks subscription run loops + async handler goroutines.
	wg sync.WaitGroup

	// closerWG tracks the bounded waiter goroutine spawned inside awaitDrain
	// so tests (and future shutdown coordinators) can observe whether the
	// waiter is still live after Close returns on timeout. See
	// WaitForPendingClosers.
	closerWG sync.WaitGroup

	// overflowListener is invoked synchronously when a subscription queue
	// drops messages due to overflow. Callers wire this to route overflow
	// events to telemetry, alerting, or their own bus topic. The listener
	// must be fast — it runs in the publish path. See SetOverflowListener.
	overflowListener atomic.Value // of OverflowListener (may be nil)
}

// BusCloseTimeoutError wraps ErrBusCloseTimeout with a list of subscriptions
// that did not drain within the close timeout. Callers can `errors.Is` the
// wrapped error or access Stuck for diagnostics.
type BusCloseTimeoutError struct {
	Stuck []StuckSubscription
}

// StuckSubscription identifies a subscription that failed to exit its run
// loop before the bus close timeout. Used for diagnosis only.
type StuckSubscription struct {
	Topic  string
	Async  bool
	Queued int
}

func (e *BusCloseTimeoutError) Error() string {
	if len(e.Stuck) == 0 {
		return ErrBusCloseTimeout.Error()
	}
	return fmt.Sprintf("%s: %d stuck subscription(s), first topic=%q queued=%d",
		ErrBusCloseTimeout.Error(), len(e.Stuck), e.Stuck[0].Topic, e.Stuck[0].Queued)
}

func (e *BusCloseTimeoutError) Unwrap() error { return ErrBusCloseTimeout }

// OverflowEvent describes a subscription-queue overflow drop.
type OverflowEvent struct {
	Topic          string   // topic of the overflowing subscription
	Dropped        int      // number of messages dropped from this batch
	TotalDropped   uint64   // cumulative drops on this subscription
	Correlations   []string // first correlation IDs of dropped messages
	QueueRemaining int      // queue length after drop
}

// OverflowListener is a callback fired synchronously on subscription queue
// overflow. Keep it fast — it runs in the publish critical path.
type OverflowListener func(OverflowEvent)

// SetOverflowListener installs (or replaces) the listener. Pass nil to clear.
func (b *ChannelBus) SetOverflowListener(fn OverflowListener) {
	if fn == nil {
		b.overflowListener.Store(OverflowListener(nil))
		return
	}
	b.overflowListener.Store(fn)
}

// loadOverflowListener returns the current listener or nil.
func (b *ChannelBus) loadOverflowListener() OverflowListener {
	v := b.overflowListener.Load()
	if v == nil {
		return nil
	}
	fn, _ := v.(OverflowListener)
	return fn
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

// maxLoggedOverflowCorrelations bounds how many correlation IDs we capture
// per overflow event to avoid log/telemetry volume blowups under sustained
// drop conditions.
const maxLoggedOverflowCorrelations = 8

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
	// Activity Fabric chokepoint: every cross-agent message emits one
	// bus_message_emitted activity (Medium resolution) carrying topic,
	// correlation chain, source/target. Causal context is read from
	// context.Context where available; for now the publisher chokepoint
	// uses Background — the message itself carries CorrelationID for
	// causal stitching.
	span := publishSpan(topic, msg)
	defer func() { span.End() }()

	if b.closed.Load() {
		span.EndWithError(ErrBusClosed)
		return ErrBusClosed
	}
	// SCH-02: routing middleware rejects messages that have already missed
	// their deadline/TTL. The caller receives a typed error and can surface
	// it on its reply channel; downstream subscribers aren't even charged
	// for the delivery attempt.
	if msg != nil && msg.IsExpired() {
		err := newExpiredMessageError(topic, msg)
		span.EndWithError(err)
		return err
	}

	// Exact-match subscribers.
	totalSubs := 0
	topicSubs, ok := b.subscriptions.Get(topic)
	if ok && topicSubs != nil {
		subs := b.snapshotSubscribers(topicSubs)
		totalSubs = len(subs)

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
	span.SetAttribute("subscriber_count", totalSubs)

	// Wildcard-match subscribers.
	b.publishToWildcardMatches(topic, msg)

	return nil
}

// ExpiredMessageError wraps ErrMessageExpired with the rejected message's
// topic and correlation id so callers / telemetry can attribute drops.
type ExpiredMessageError struct {
	Topic         string
	CorrelationID string
	MessageID     string
	ExpiredAt     *time.Time
}

func (e *ExpiredMessageError) Error() string {
	return fmt.Sprintf("%s (topic=%q correlation_id=%q message_id=%q)",
		ErrMessageExpired.Error(), e.Topic, e.CorrelationID, e.MessageID)
}

func (e *ExpiredMessageError) Unwrap() error { return ErrMessageExpired }

func newExpiredMessageError(topic string, msg *Message) *ExpiredMessageError {
	return &ExpiredMessageError{
		Topic:         topic,
		CorrelationID: msg.CorrelationID,
		MessageID:     msg.ID,
		ExpiredAt:     msg.ExpiresAt(),
	}
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
	// SCH-01: insert in priority order. Higher priority goes ahead of any
	// lower-priority messages already queued, but stably preserves FIFO
	// order within the same priority class.
	sub.queue = insertByPriority(sub.queue, msg)
	dropped := 0
	var droppedCorrelations []string
	if len(sub.queue) > maxQueueSize {
		dropped = len(sub.queue) - maxQueueSize
		droppedCorrelations = collectCorrelations(sub.queue[:dropped], maxLoggedOverflowCorrelations)
		// Drop oldest messages to keep the queue bounded.
		// copy avoids holding references to dropped messages.
		remaining := make([]*Message, maxQueueSize)
		copy(remaining, sub.queue[dropped:])
		sub.queue = remaining
	}
	queueLen := len(sub.queue)
	sub.mu.Unlock()

	if dropped > 0 {
		total := sub.droppedCount.Add(uint64(dropped))
		DebugFileLog().Warn("publishToSubscriber: QUEUE_OVERFLOW_DROP",
			"topic", sub.topic,
			"dropped", dropped,
			"queue_len", queueLen,
			"dropped_correlations", droppedCorrelations,
			"total_dropped", total)
		if listener := b.loadOverflowListener(); listener != nil {
			listener(OverflowEvent{
				Topic:          sub.topic,
				Dropped:        dropped,
				TotalDropped:   total,
				Correlations:   droppedCorrelations,
				QueueRemaining: queueLen,
			})
		}
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

	pending := b.closeAllSubscriptions()
	return b.awaitDrain(pending)
}

// closeAllSubscriptions signals every subscription to drain and exit, and
// returns the list of subscriptions that were live at close time. Callers
// use the list to distinguish drained vs. stuck subscriptions on timeout.
func (b *ChannelBus) closeAllSubscriptions() []*channelSubscription {
	var pending []*channelSubscription

	// Exact subscriptions.
	allTopics := b.subscriptions.Snapshot()
	for _, topicSubs := range allTopics {
		if topicSubs == nil {
			continue
		}
		topicSubs.mu.Lock()
		for _, sub := range topicSubs.subs {
			sub.close()
			pending = append(pending, sub)
		}
		topicSubs.subs = nil
		topicSubs.mu.Unlock()
	}
	b.subscriptions.Clear()

	// Wildcard subscriptions.
	b.wildcardMu.Lock()
	for _, sub := range b.wildcardIndex {
		sub.close()
		pending = append(pending, sub)
	}
	b.wildcardIndex = nil
	b.wildcardMu.Unlock()

	return pending
}

// awaitDrain waits for all subscription goroutines to finish, bounded by
// closeTimeout. Returns a *BusCloseTimeoutError wrapping ErrBusCloseTimeout
// (with identified stuck subscriptions) if the timeout elapses. The waiter
// goroutine is tracked via closerWG so callers can observe it post-timeout
// via WaitForPendingClosers.
func (b *ChannelBus) awaitDrain(pending []*channelSubscription) error {
	done := make(chan struct{})
	b.closerWG.Add(1)
	go func() {
		defer b.closerWG.Done()
		b.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		b.closerWG.Wait()
		return nil
	case <-time.After(b.closeTimeout):
		return &BusCloseTimeoutError{Stuck: collectStuckSubscriptions(pending)}
	}
}

// collectStuckSubscriptions returns diagnostic entries for any subscriptions
// in pending whose run loop has not yet signaled exit.
func collectStuckSubscriptions(pending []*channelSubscription) []StuckSubscription {
	var stuck []StuckSubscription
	for _, s := range pending {
		if s.exited.Load() {
			continue
		}
		stuck = append(stuck, StuckSubscription{
			Topic:  s.topic,
			Async:  s.async,
			Queued: s.queuedLen(),
		})
	}
	return stuck
}

// queuedLen returns the number of messages currently queued on this
// subscription. Safe to call concurrently with publishers (it takes the
// subscription's queue mutex).
func (s *channelSubscription) queuedLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// WaitForPendingClosers blocks until every awaitDrain waiter goroutine has
// exited. On a normal Close() this returns immediately; on a close timeout
// it blocks until the stuck subscriptions finally drain (bounded leak). Use
// in tests and controlled shutdown sequences to verify no residual waiters.
func (b *ChannelBus) WaitForPendingClosers() {
	b.closerWG.Wait()
}

type channelSubscription struct {
	bus        *ChannelBus
	topic      string
	handler    MessageHandler
	async      bool
	active     atomic.Bool
	closed     atomic.Bool
	mu         sync.Mutex    // guards queue
	queue      []*Message    // append-only from publishers, batch-drained by consumer
	notify     chan struct{} // cap-1 signal: "queue has messages"
	done       chan struct{} // closed on unsubscribe/bus-close to wake consumer
	topicShard *topicSubscriptions

	// panicCount tallies handler panics for this subscription so operators can
	// detect chronic misbehavior without tailing logs. Panics are recovered —
	// the worker keeps serving subsequent messages.
	panicCount atomic.Uint64

	// droppedCount tallies messages dropped from this subscription's queue
	// due to overflow (maxQueueSize exceeded).
	droppedCount atomic.Uint64

	// exited is set to true by the run goroutine immediately before it
	// returns. awaitDrain uses it to distinguish drained subscriptions from
	// stuck ones when the close timeout elapses.
	exited atomic.Bool

	// wildcardCleanup removes this sub from the TopicRouter and wildcard index.
	// Non-nil only for wildcard subscriptions.
	wildcardCleanup func()
}

// PanicCount returns the number of handler panics recovered on this
// subscription since its creation.
func (s *channelSubscription) PanicCount() uint64 {
	return s.panicCount.Load()
}

// DroppedCount returns the cumulative number of messages dropped from this
// subscription's queue due to overflow.
func (s *channelSubscription) DroppedCount() uint64 {
	return s.droppedCount.Load()
}

// insertByPriority inserts msg into queue ordered by Priority (descending),
// with FIFO order preserved within the same priority. Linear scan from the
// tail terminates as soon as a same-or-higher priority entry is found —
// O(k) where k is the number of trailing lower-priority items, which is
// O(1) in the common case where priorities arrive in monotonic order.
func insertByPriority(queue []*Message, msg *Message) []*Message {
	if msg == nil {
		return append(queue, nil)
	}
	insertAt := len(queue)
	for i := len(queue) - 1; i >= 0; i-- {
		if queue[i] == nil {
			continue
		}
		if queue[i].Priority >= msg.Priority {
			break
		}
		insertAt = i
	}
	if insertAt == len(queue) {
		return append(queue, msg)
	}
	queue = append(queue, nil)
	copy(queue[insertAt+1:], queue[insertAt:])
	queue[insertAt] = msg
	return queue
}

// collectCorrelations pulls up to max correlation IDs from msgs. Safe for
// nil messages (treated as empty correlation).
func collectCorrelations(msgs []*Message, max int) []string {
	if max <= 0 || len(msgs) == 0 {
		return nil
	}
	limit := len(msgs)
	if limit > max {
		limit = max
	}
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		if msgs[i] == nil {
			out = append(out, "")
			continue
		}
		out = append(out, msgs[i].CorrelationID)
	}
	return out
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
	defer func() {
		s.exited.Store(true)
		wg.Done()
	}()

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
	defer func() {
		if r := recover(); r != nil {
			s.recordPanic(msg, r)
		}
	}()
	_ = s.handler(msg)
}

// recordPanic increments the subscription's panic counter and logs a
// structured entry with the message's correlation id so chronic handler
// crashes can be diagnosed from telemetry alone.
func (s *channelSubscription) recordPanic(msg *Message, r any) {
	s.panicCount.Add(1)
	DebugFileLog().Error("channel bus subscription panic",
		"topic", s.topic,
		"async", s.async,
		"correlation_id", panicMessageCorrelation(msg),
		"panic_count", s.panicCount.Load(),
		"panic", r,
		"stack", string(debug.Stack()))
}

func panicMessageCorrelation(msg *Message) string {
	if msg == nil {
		return ""
	}
	return msg.CorrelationID
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
