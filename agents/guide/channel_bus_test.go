package guide_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelBus_NewChannelBus(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	require.NotNil(t, bus)

	stats := bus.Stats()
	assert.Equal(t, 0, stats.Topics)
	assert.Equal(t, 0, stats.Subscriptions)
	assert.Equal(t, 256, stats.BufferSize)
	assert.False(t, stats.Closed)
}

func TestChannelBus_ShardsConfigured(t *testing.T) {
	bus := guide.NewChannelBus(guide.ChannelBusConfig{BufferSize: 1, ShardCount: 1})
	defer bus.Close()

	_, err := bus.Subscribe("topic", func(msg *guide.Message) error { return nil })
	require.NoError(t, err)

	stats := bus.Stats()
	assert.Equal(t, 1, stats.Topics)
	assert.Equal(t, 1, stats.Subscriptions)
	assert.Equal(t, 1, bus.TopicSubscriberCount("topic"))
}

func TestChannelBus_Subscribe(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	sub, err := bus.Subscribe("test-topic", func(msg *guide.Message) error {
		return nil
	})
	require.NoError(t, err)
	require.NotNil(t, sub)

	assert.Equal(t, "test-topic", sub.Topic())
	assert.True(t, sub.IsActive())

	stats := bus.Stats()
	assert.Equal(t, 1, stats.Topics)
	assert.Equal(t, 1, stats.Subscriptions)
	assert.Equal(t, 1, stats.ByTopic["test-topic"])
}

func TestChannelBus_PublishSubscribe(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	received := make(chan *guide.Message, 1)

	sub, err := bus.Subscribe("test-topic", func(msg *guide.Message) error {
		received <- msg
		return nil
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	msg := &guide.Message{
		ID:      "msg-1",
		Type:    guide.MessageTypeRequest,
		Payload: map[string]any{"test": "data"},
	}

	err = bus.Publish("test-topic", msg)
	require.NoError(t, err)

	select {
	case recvMsg := <-received:
		assert.Equal(t, "msg-1", recvMsg.ID)
		assert.Equal(t, guide.MessageTypeRequest, recvMsg.Type)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestChannelBus_MultipleSubscribers(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	var count int32

	for i := 0; i < 3; i++ {
		_, err := bus.Subscribe("multi-topic", func(msg *guide.Message) error {
			atomic.AddInt32(&count, 1)
			return nil
		})
		require.NoError(t, err)
	}

	stats := bus.Stats()
	assert.Equal(t, 3, stats.Subscriptions)

	err := bus.Publish("multi-topic", &guide.Message{ID: "msg-1"})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(3), atomic.LoadInt32(&count))
}

func TestChannelBus_Unsubscribe(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	sub, err := bus.Subscribe("test-topic", func(msg *guide.Message) error {
		return nil
	})
	require.NoError(t, err)

	err = sub.Unsubscribe()
	require.NoError(t, err)

	assert.False(t, sub.IsActive())

	err = sub.Unsubscribe()
	assert.Error(t, err)
}

func TestChannelBus_SubscribeAsync(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	var wg sync.WaitGroup
	wg.Add(1)

	var receivedGoroutine bool

	_, err := bus.SubscribeAsync("async-topic", func(msg *guide.Message) error {
		defer wg.Done()
		receivedGoroutine = true
		return nil
	})
	require.NoError(t, err)

	err = bus.Publish("async-topic", &guide.Message{ID: "msg-1"})
	require.NoError(t, err)

	wg.Wait()
	assert.True(t, receivedGoroutine)
}

func TestChannelBus_Close(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())

	_, err := bus.Subscribe("topic", func(msg *guide.Message) error {
		return nil
	})
	require.NoError(t, err)

	err = bus.Close()
	require.NoError(t, err)

	stats := bus.Stats()
	assert.True(t, stats.Closed)

	err = bus.Publish("topic", &guide.Message{ID: "msg-1"})
	assert.Error(t, err)

	_, err = bus.Subscribe("topic2", func(msg *guide.Message) error { return nil })
	assert.Error(t, err)

	err = bus.Close()
	assert.Error(t, err)
}

func TestChannelBus_NilHandler(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	_, err := bus.Subscribe("topic", nil)
	assert.Error(t, err)

	_, err = bus.SubscribeAsync("topic", nil)
	assert.Error(t, err)
}

func TestChannelBus_TopicSubscriberCount(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	assert.Equal(t, 0, bus.TopicSubscriberCount("topic"))

	sub1, _ := bus.Subscribe("topic", func(msg *guide.Message) error { return nil })
	assert.Equal(t, 1, bus.TopicSubscriberCount("topic"))

	sub2, _ := bus.Subscribe("topic", func(msg *guide.Message) error { return nil })
	assert.Equal(t, 2, bus.TopicSubscriberCount("topic"))

	sub1.Unsubscribe()
	assert.Equal(t, 1, bus.TopicSubscriberCount("topic"))

	sub2.Unsubscribe()
	assert.Equal(t, 0, bus.TopicSubscriberCount("topic"))
}

func TestChannelBus_ConcurrentPublish(t *testing.T) {
	bus := guide.NewChannelBus(guide.ChannelBusConfig{BufferSize: 1000})
	defer bus.Close()

	var count int64

	_, err := bus.Subscribe("concurrent", func(msg *guide.Message) error {
		atomic.AddInt64(&count, 1)
		return nil
	})
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			bus.Publish("concurrent", &guide.Message{ID: "msg"})
		}(i)
	}

	wg.Wait()
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, int64(100), atomic.LoadInt64(&count))
}

func TestChannelBus_HandlerPanicRecovery(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	var afterPanic int32

	_, err := bus.Subscribe("panic-topic", func(msg *guide.Message) error {
		panic("test panic")
	})
	require.NoError(t, err)

	_, err = bus.Subscribe("panic-topic", func(msg *guide.Message) error {
		atomic.StoreInt32(&afterPanic, 1)
		return nil
	})
	require.NoError(t, err)

	bus.Publish("panic-topic", &guide.Message{ID: "msg-1"})

	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int32(1), atomic.LoadInt32(&afterPanic))
}

// TestChannelBus_PriorityOrdering closes SCH-01. Higher-priority messages
// published to a topic with a blocked consumer should be delivered ahead of
// lower-priority messages already queued, while FIFO order is preserved
// within the same priority class. We block the subscriber on the first
// message, publish an interleaved priority stream, then release and
// observe the dispatch order.
func TestChannelBus_PriorityOrdering(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	released := make(chan struct{})
	var order []string
	var orderMu sync.Mutex

	_, err := bus.Subscribe("priority-topic", func(msg *guide.Message) error {
		if msg.ID == "blocker" {
			<-released
		}
		orderMu.Lock()
		order = append(order, msg.ID)
		orderMu.Unlock()
		return nil
	})
	require.NoError(t, err)

	// Prime the consumer with the blocker.
	require.NoError(t, bus.Publish("priority-topic", &guide.Message{ID: "blocker"}))
	// Give the consumer a tick to pull the blocker off the queue.
	time.Sleep(10 * time.Millisecond)

	// Enqueue interleaved priorities. Expected post-release drain order:
	//   high-1, high-2 (stable within high)
	//   normal-1, normal-2 (stable within normal)
	//   low-1, low-2 (stable within low)
	require.NoError(t, bus.Publish("priority-topic", &guide.Message{ID: "low-1", Priority: messaging.PriorityLow}))
	require.NoError(t, bus.Publish("priority-topic", &guide.Message{ID: "normal-1", Priority: messaging.PriorityNormal}))
	require.NoError(t, bus.Publish("priority-topic", &guide.Message{ID: "high-1", Priority: messaging.PriorityHigh}))
	require.NoError(t, bus.Publish("priority-topic", &guide.Message{ID: "normal-2", Priority: messaging.PriorityNormal}))
	require.NoError(t, bus.Publish("priority-topic", &guide.Message{ID: "low-2", Priority: messaging.PriorityLow}))
	require.NoError(t, bus.Publish("priority-topic", &guide.Message{ID: "high-2", Priority: messaging.PriorityHigh}))

	close(released)

	require.Eventually(t, func() bool {
		orderMu.Lock()
		defer orderMu.Unlock()
		return len(order) == 7
	}, time.Second, 5*time.Millisecond)

	expected := []string{"blocker", "high-1", "high-2", "normal-1", "normal-2", "low-1", "low-2"}
	orderMu.Lock()
	defer orderMu.Unlock()
	assert.Equal(t, expected, order)
}

// TestChannelBus_ExpiredMessageRejected closes SCH-02. Publishing a message
// whose Deadline has already elapsed returns ErrMessageExpired wrapped in a
// structured ExpiredMessageError carrying the rejected topic and ids. The
// subscriber never sees the message.
func TestChannelBus_ExpiredMessageRejected(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	var received atomic.Int32
	_, err := bus.Subscribe("expiry-topic", func(msg *guide.Message) error {
		received.Add(1)
		return nil
	})
	require.NoError(t, err)

	past := time.Now().Add(-time.Second)
	err = bus.Publish("expiry-topic", &guide.Message{
		ID:            "expired-1",
		CorrelationID: "corr-expired",
		Timestamp:     past.Add(-2 * time.Second),
		Deadline:      &past,
	})

	require.Error(t, err)
	require.True(t, errors.Is(err, guide.ErrMessageExpired),
		"Publish should return an error wrapping ErrMessageExpired")

	var expiredErr *guide.ExpiredMessageError
	require.ErrorAs(t, err, &expiredErr)
	assert.Equal(t, "expiry-topic", expiredErr.Topic)
	assert.Equal(t, "expired-1", expiredErr.MessageID)
	assert.Equal(t, "corr-expired", expiredErr.CorrelationID)

	// Subscriber must not observe the message.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(0), received.Load())
}

// TestChannelBus_UnexpiredMessagePassesThrough sanity-checks the negative
// case — a message with a future deadline is delivered normally.
func TestChannelBus_UnexpiredMessagePassesThrough(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	var received atomic.Int32
	_, err := bus.Subscribe("expiry-pass-topic", func(*guide.Message) error {
		received.Add(1)
		return nil
	})
	require.NoError(t, err)

	future := time.Now().Add(5 * time.Second)
	require.NoError(t, bus.Publish("expiry-pass-topic", &guide.Message{
		ID:       "fresh-1",
		Deadline: &future,
	}))

	require.Eventually(t, func() bool { return received.Load() == 1 },
		time.Second, 5*time.Millisecond)
}

// TestChannelBus_CloseTimeout_ReportsStuckSubscriptions closes MSG-15. A
// subscription blocked inside its handler should be identified in the
// returned error when Close() exceeds its configured timeout.
func TestChannelBus_CloseTimeout_ReportsStuckSubscriptions(t *testing.T) {
	cfg := guide.DefaultChannelBusConfig()
	cfg.CloseTimeout = 100 * time.Millisecond
	bus := guide.NewChannelBus(cfg)

	block := make(chan struct{})
	release := make(chan struct{})
	_, err := bus.Subscribe("stuck-topic", func(msg *guide.Message) error {
		close(block)
		<-release
		return nil
	})
	require.NoError(t, err)

	require.NoError(t, bus.Publish("stuck-topic", &guide.Message{ID: "blocker"}))
	<-block

	err = bus.Close()
	require.Error(t, err)

	var timeoutErr *guide.BusCloseTimeoutError
	require.ErrorAs(t, err, &timeoutErr, "Close error should unwrap to *BusCloseTimeoutError")
	require.True(t, errors.Is(err, guide.ErrBusCloseTimeout), "error should wrap ErrBusCloseTimeout")
	require.NotEmpty(t, timeoutErr.Stuck, "should identify at least one stuck subscription")
	assert.Equal(t, "stuck-topic", timeoutErr.Stuck[0].Topic)

	// Free the stuck handler so the waiter goroutine can exit.
	close(release)
	bus.WaitForPendingClosers()
}

// TestChannelBus_Close_NoStuckOnCleanShutdown verifies that Close() returns
// nil and the closer goroutine exits promptly when all subscriptions are
// responsive. Regression guard against awaitDrain introducing a wait that
// persists after Close returns.
func TestChannelBus_Close_NoStuckOnCleanShutdown(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	_, err := bus.Subscribe("clean-topic", func(msg *guide.Message) error { return nil })
	require.NoError(t, err)

	require.NoError(t, bus.Publish("clean-topic", &guide.Message{ID: "msg"}))
	require.NoError(t, bus.Close())
	bus.WaitForPendingClosers()
}

// TestChannelBus_OverflowListener verifies the per-subscription drop counter,
// the `DropCounter` capability interface, and the `SetOverflowListener`
// callback all fire once the queue exceeds maxQueueSize. This closes MSG-13
// (overflow drops were previously logged as a bare count with no correlation
// IDs and no programmatic observability).
func TestChannelBus_OverflowListener(t *testing.T) {
	cfg := guide.DefaultChannelBusConfig()
	bus := guide.NewChannelBus(cfg)
	defer bus.Close()

	// Block the subscriber on the first message so subsequent publishes queue.
	// Using sync Subscribe (not Async) means the consumer loop blocks entirely
	// inside the handler — no asyncLimiter absorbs messages, so queue growth
	// is determined purely by publish rate vs. handler progress.
	blockCh := make(chan struct{})
	proceed := make(chan struct{})
	var firstOnce sync.Once
	sub, err := bus.Subscribe("overflow-topic", func(msg *guide.Message) error {
		if msg.ID == "first" {
			firstOnce.Do(func() { close(blockCh) })
			<-proceed
		}
		return nil
	})
	require.NoError(t, err)

	// Wire the overflow listener.
	var events []guide.OverflowEvent
	var evMu sync.Mutex
	bus.SetOverflowListener(func(evt guide.OverflowEvent) {
		evMu.Lock()
		events = append(events, evt)
		evMu.Unlock()
	})

	// Fire the blocker.
	require.NoError(t, bus.Publish("overflow-topic", &guide.Message{ID: "first", CorrelationID: "corr-first"}))
	<-blockCh

	// Consumer is pinned inside handler for "first". Every subsequent publish
	// queues. Push maxQueueSize + overflow to force overflow.
	const overflowCount = 5
	const maxQueue = 4096
	for i := 0; i < maxQueue+overflowCount; i++ {
		require.NoError(t, bus.Publish("overflow-topic", &guide.Message{
			ID:            fmt.Sprintf("msg-%d", i),
			CorrelationID: fmt.Sprintf("corr-%d", i),
		}))
	}

	// Unblock and drain.
	close(proceed)

	// Assertions — use Eventually so we don't race the listener.
	require.Eventually(t, func() bool {
		evMu.Lock()
		defer evMu.Unlock()
		return len(events) > 0
	}, time.Second, 5*time.Millisecond, "listener should have observed overflow")

	evMu.Lock()
	require.NotEmpty(t, events)
	evt := events[0]
	evMu.Unlock()

	assert.Equal(t, "overflow-topic", evt.Topic)
	assert.Greater(t, evt.Dropped, 0, "should drop at least 1 message")
	assert.NotEmpty(t, evt.Correlations, "should capture at least one correlation_id")
	assert.LessOrEqual(t, len(evt.Correlations), 8, "correlation capture is bounded")

	dc, ok := sub.(guide.DropCounter)
	require.True(t, ok, "subscription should satisfy DropCounter")
	assert.Greater(t, dc.DroppedCount(), uint64(0))
}

// TestChannelBus_OverflowListener_NilOK verifies clearing the listener is safe.
func TestChannelBus_OverflowListener_NilOK(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	bus.SetOverflowListener(func(evt guide.OverflowEvent) {})
	bus.SetOverflowListener(nil)
	// No assertion needed — should not panic.
	_, _ = bus.Subscribe("noop", func(m *guide.Message) error { return nil })
}

// TestChannelBus_ConcurrentUnsubscribeAndClose exercises the path flagged in
// MSG-16 (unsubscribe race with nil topicSubs). On inspection the existing
// code is correct: `topicSubs.mu` serializes, `append(nil, ...)` is safe, and
// `channelSubscription.close()` guards double-close via atomic.Swap. This
// test is a regression guard — the -race detector would flag any future
// change that reintroduces the hazard.
func TestChannelBus_ConcurrentUnsubscribeAndClose(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())

	const n = 32
	subs := make([]guide.Subscription, 0, n)
	for i := 0; i < n; i++ {
		s, err := bus.Subscribe(fmt.Sprintf("race-topic-%d", i), func(m *guide.Message) error {
			return nil
		})
		require.NoError(t, err)
		subs = append(subs, s)
	}

	var wg sync.WaitGroup
	for _, s := range subs {
		wg.Add(1)
		go func(sub guide.Subscription) {
			defer wg.Done()
			_ = sub.Unsubscribe()
		}(s)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = bus.Close()
	}()
	wg.Wait()

	// Additional Unsubscribe calls after Close should be a no-op / return
	// ErrSubscriptionClosed without panicking.
	for _, s := range subs {
		_ = s.Unsubscribe()
	}
}

// TestChannelBus_PanicCounter verifies the recovered-panic counter is exposed
// via the PanicCounter capability interface and increments per panic. The
// worker must continue serving subsequent messages on the same subscription
// after a panic — the counter lets operators detect chronic failures without
// tailing logs.
func TestChannelBus_PanicCounter(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	var served atomic.Int64
	sub, err := bus.Subscribe("panic-counter", func(msg *guide.Message) error {
		served.Add(1)
		if msg.ID == "boom" {
			panic("intentional panic for test")
		}
		return nil
	})
	require.NoError(t, err)

	pc, ok := sub.(guide.PanicCounter)
	require.True(t, ok, "channel subscription should satisfy PanicCounter")
	require.Equal(t, uint64(0), pc.PanicCount())

	// Interleave good and bad messages to verify (a) counter increments only on
	// panic, and (b) the worker keeps serving after recovery.
	require.NoError(t, bus.Publish("panic-counter", &guide.Message{ID: "ok-1"}))
	require.NoError(t, bus.Publish("panic-counter", &guide.Message{ID: "boom"}))
	require.NoError(t, bus.Publish("panic-counter", &guide.Message{ID: "boom"}))
	require.NoError(t, bus.Publish("panic-counter", &guide.Message{ID: "ok-2"}))

	require.Eventually(t, func() bool {
		return served.Load() == 4
	}, time.Second, 5*time.Millisecond, "all 4 messages should have been dispatched despite panics")

	assert.Equal(t, uint64(2), pc.PanicCount(), "counter should equal number of panicking handlers")
}

// =============================================================================
// W12.3 Tests - Async Handler Tracking
// =============================================================================

func TestChannelBus_AsyncHandler_TrackedOnShutdown(t *testing.T) {
	// Test that async handlers are properly tracked and waited for on shutdown
	bus := guide.NewChannelBus(guide.ChannelBusConfig{BufferSize: 100})

	var handlerStarted int64
	var handlerFinished int64
	handlerRunning := make(chan struct{})
	handlerCanFinish := make(chan struct{})

	_, err := bus.SubscribeAsync("async-topic", func(msg *guide.Message) error {
		atomic.AddInt64(&handlerStarted, 1)
		close(handlerRunning)
		<-handlerCanFinish // Block until signaled
		atomic.AddInt64(&handlerFinished, 1)
		return nil
	})
	require.NoError(t, err)

	// Publish a message to trigger the async handler
	err = bus.Publish("async-topic", &guide.Message{ID: "msg-1"})
	require.NoError(t, err)

	// Wait for handler to start
	select {
	case <-handlerRunning:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for handler to start")
	}

	// Verify handler started but not finished
	assert.Equal(t, int64(1), atomic.LoadInt64(&handlerStarted))
	assert.Equal(t, int64(0), atomic.LoadInt64(&handlerFinished))

	// Start Close() in a goroutine (it should block until handler finishes)
	closeDone := make(chan struct{})
	go func() {
		bus.Close()
		close(closeDone)
	}()

	// Verify Close() is blocking (handler still running)
	select {
	case <-closeDone:
		t.Fatal("Close() returned before handler finished")
	case <-time.After(50 * time.Millisecond):
		// Expected - Close() is blocking
	}

	// Signal handler to finish
	close(handlerCanFinish)

	// Now Close() should complete
	select {
	case <-closeDone:
		// Success - Close() waited for handler
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for Close() to complete")
	}

	// Verify handler finished
	assert.Equal(t, int64(1), atomic.LoadInt64(&handlerFinished))
}

func TestChannelBus_AsyncHandler_MultipleHandlersTracked(t *testing.T) {
	// Test that multiple concurrent async handlers are all tracked
	bus := guide.NewChannelBus(guide.ChannelBusConfig{BufferSize: 1000})

	var handlersFinished int64
	const numMessages = 50

	_, err := bus.SubscribeAsync("multi-async", func(msg *guide.Message) error {
		time.Sleep(10 * time.Millisecond) // Simulate work
		atomic.AddInt64(&handlersFinished, 1)
		return nil
	})
	require.NoError(t, err)

	// Publish many messages rapidly
	for i := 0; i < numMessages; i++ {
		bus.Publish("multi-async", &guide.Message{ID: "msg"})
	}

	// Close should wait for all handlers
	err = bus.Close()
	require.NoError(t, err)

	// All handlers should have finished by now
	assert.Equal(t, int64(numMessages), atomic.LoadInt64(&handlersFinished))
}

func TestChannelBus_AsyncHandler_PanicRecovery(t *testing.T) {
	// Test that panicking async handlers don't prevent shutdown
	bus := guide.NewChannelBus(guide.ChannelBusConfig{BufferSize: 100})

	var goodHandlerCount int64

	// Panicking async handler
	_, err := bus.SubscribeAsync("panic-async", func(msg *guide.Message) error {
		panic("intentional panic")
	})
	require.NoError(t, err)

	// Good async handler
	_, err = bus.SubscribeAsync("good-async", func(msg *guide.Message) error {
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt64(&goodHandlerCount, 1)
		return nil
	})
	require.NoError(t, err)

	// Publish to both
	for i := 0; i < 10; i++ {
		bus.Publish("panic-async", &guide.Message{ID: "panic-msg"})
		bus.Publish("good-async", &guide.Message{ID: "good-msg"})
	}

	// Close should complete despite panics
	done := make(chan struct{})
	go func() {
		bus.Close()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Close() timed out - possible goroutine leak")
	}

	// Good handler should have processed messages
	assert.GreaterOrEqual(t, atomic.LoadInt64(&goodHandlerCount), int64(1))
}

func TestChannelBus_SyncHandler_NotAffectedByFix(t *testing.T) {
	// Verify synchronous handlers still work correctly
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	var count int64

	_, err := bus.Subscribe("sync-topic", func(msg *guide.Message) error {
		atomic.AddInt64(&count, 1)
		return nil
	})
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		bus.Publish("sync-topic", &guide.Message{ID: "msg"})
	}

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int64(10), atomic.LoadInt64(&count))
}

// =============================================================================
// W12.27 Tests - Topic Registration Race Prevention
// =============================================================================

func TestChannelBus_W12_27_ConcurrentTopicRegistration(t *testing.T) {
	// W12.27: Test that concurrent subscriptions to the same topic don't race
	// when registering the topic for the first time.
	bus := guide.NewChannelBus(guide.ChannelBusConfig{BufferSize: 100})
	defer bus.Close()

	const goroutines = 100
	const topic = "concurrent-topic"

	var wg sync.WaitGroup
	wg.Add(goroutines)

	var subscribeErrors int64
	var successfulSubs int64

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			sub, err := bus.Subscribe(topic, func(msg *guide.Message) error {
				return nil
			})
			if err != nil {
				atomic.AddInt64(&subscribeErrors, 1)
				return
			}
			atomic.AddInt64(&successfulSubs, 1)
			// Keep subscription alive until test ends
			_ = sub
		}()
	}

	wg.Wait()

	// All subscriptions should succeed
	assert.Equal(t, int64(0), subscribeErrors)
	assert.Equal(t, int64(goroutines), successfulSubs)

	// Verify all subscribers are registered
	count := bus.TopicSubscriberCount(topic)
	assert.Equal(t, goroutines, count)
}

func TestChannelBus_W12_27_ConcurrentNewTopics(t *testing.T) {
	// W12.27: Test concurrent creation of many different topics
	bus := guide.NewChannelBus(guide.ChannelBusConfig{BufferSize: 100, ShardCount: 64})
	defer bus.Close()

	const goroutines = 100
	const topicsPerGoroutine = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(gid int) {
			defer wg.Done()
			for j := 0; j < topicsPerGoroutine; j++ {
				topic := string(rune('A'+gid%26)) + "-topic-" + string(rune('0'+j%10))
				_, err := bus.Subscribe(topic, func(msg *guide.Message) error {
					return nil
				})
				if err != nil {
					t.Errorf("subscribe failed: %v", err)
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify stats show subscriptions
	stats := bus.Stats()
	assert.GreaterOrEqual(t, stats.Subscriptions, 1)
}

// =============================================================================
// Wildcard Subscription Tests
// =============================================================================

func TestChannelBus_WildcardSubscription_SingleSegment(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	received := make(chan *guide.Message, 3)

	_, err := bus.SubscribeAsync("pipeline.update.*", func(msg *guide.Message) error {
		received <- msg
		return nil
	})
	require.NoError(t, err)

	// Publish to matching topics.
	bus.Publish("pipeline.update.engineer", &guide.Message{ID: "msg-eng"})
	bus.Publish("pipeline.update.designer", &guide.Message{ID: "msg-des"})
	bus.Publish("pipeline.update.inspector", &guide.Message{ID: "msg-ins"})

	var gotIDs []string
	for range 3 {
		select {
		case msg := <-received:
			gotIDs = append(gotIDs, msg.ID)
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for wildcard delivery")
		}
	}
	assert.ElementsMatch(t, []string{"msg-eng", "msg-des", "msg-ins"}, gotIDs)
}

func TestChannelBus_WildcardSubscription_NoFalseMatch(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	var count int64

	_, err := bus.SubscribeAsync("pipeline.update.*", func(msg *guide.Message) error {
		atomic.AddInt64(&count, 1)
		return nil
	})
	require.NoError(t, err)

	// Non-matching topics should not deliver.
	bus.Publish("pipeline.state.engineer", &guide.Message{ID: "no-match-1"})
	bus.Publish("pipeline.update", &guide.Message{ID: "no-match-2"})
	bus.Publish("tasks.dispatch", &guide.Message{ID: "no-match-3"})

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int64(0), atomic.LoadInt64(&count))
}

func TestChannelBus_WildcardAndExact_BothDelivered(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	var wildcardCount, exactCount int64

	_, err := bus.SubscribeAsync("events.*", func(msg *guide.Message) error {
		atomic.AddInt64(&wildcardCount, 1)
		return nil
	})
	require.NoError(t, err)

	_, err = bus.SubscribeAsync("events.login", func(msg *guide.Message) error {
		atomic.AddInt64(&exactCount, 1)
		return nil
	})
	require.NoError(t, err)

	bus.Publish("events.login", &guide.Message{ID: "login"})

	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int64(1), atomic.LoadInt64(&wildcardCount))
	assert.Equal(t, int64(1), atomic.LoadInt64(&exactCount))
}

func TestChannelBus_WildcardUnsubscribe(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	var count int64

	sub, err := bus.SubscribeAsync("pipeline.*", func(msg *guide.Message) error {
		atomic.AddInt64(&count, 1)
		return nil
	})
	require.NoError(t, err)

	bus.Publish("pipeline.update", &guide.Message{ID: "m1"})
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int64(1), atomic.LoadInt64(&count))

	err = sub.Unsubscribe()
	require.NoError(t, err)
	assert.False(t, sub.IsActive())

	bus.Publish("pipeline.update", &guide.Message{ID: "m2"})
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int64(1), atomic.LoadInt64(&count)) // no increment
}

func TestChannelBus_WildcardTopicSubscriberCount(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	sub, err := bus.SubscribeAsync("data.*", func(msg *guide.Message) error { return nil })
	require.NoError(t, err)

	// Wildcard sub should be counted for matching topics.
	assert.Equal(t, 1, bus.TopicSubscriberCount("data.events"))
	assert.Equal(t, 1, bus.TopicSubscriberCount("data.metrics"))

	// Non-matching topic should have zero.
	assert.Equal(t, 0, bus.TopicSubscriberCount("other.topic"))

	sub.Unsubscribe()
	assert.Equal(t, 0, bus.TopicSubscriberCount("data.events"))
}

func TestChannelBus_WildcardStats(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	_, err := bus.SubscribeAsync("pipeline.update.*", func(msg *guide.Message) error { return nil })
	require.NoError(t, err)

	_, err = bus.Subscribe("tasks.dispatch", func(msg *guide.Message) error { return nil })
	require.NoError(t, err)

	stats := bus.Stats()
	assert.Equal(t, 2, stats.Subscriptions)
	assert.Equal(t, 2, stats.Topics)
	assert.Equal(t, 1, stats.ByTopic["pipeline.update.*"])
	assert.Equal(t, 1, stats.ByTopic["tasks.dispatch"])
}

func TestChannelBus_WildcardClose(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())

	var count int64

	_, err := bus.SubscribeAsync("events.*", func(msg *guide.Message) error {
		atomic.AddInt64(&count, 1)
		return nil
	})
	require.NoError(t, err)

	bus.Publish("events.test", &guide.Message{ID: "m1"})
	time.Sleep(50 * time.Millisecond)

	err = bus.Close()
	require.NoError(t, err)

	assert.Equal(t, int64(1), atomic.LoadInt64(&count))

	// After close, publish should fail.
	err = bus.Publish("events.test", &guide.Message{ID: "m2"})
	assert.Error(t, err)
}

func TestChannelBus_W12_27_ConcurrentSubscribeAndPublish(t *testing.T) {
	// W12.27: Test that subscribing and publishing to the same topic concurrently is safe
	bus := guide.NewChannelBus(guide.ChannelBusConfig{BufferSize: 1000})
	defer bus.Close()

	const topic = "subscribe-publish-topic"
	var received int64

	var wg sync.WaitGroup

	// Start publishers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				bus.Publish(topic, &guide.Message{ID: "msg"})
			}
		}()
	}

	// Start subscribers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := bus.Subscribe(topic, func(msg *guide.Message) error {
				atomic.AddInt64(&received, 1)
				return nil
			})
			if err != nil {
				t.Errorf("subscribe failed: %v", err)
			}
		}()
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	// Just verify no crashes/races - exact count depends on timing
	t.Logf("Received %d messages", atomic.LoadInt64(&received))
}
