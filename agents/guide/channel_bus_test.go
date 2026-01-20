package guide_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
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
