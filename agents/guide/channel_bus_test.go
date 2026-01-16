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

// TestChannelBus_NewChannelBus tests creating a new channel bus
func TestChannelBus_NewChannelBus(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	require.NotNil(t, bus)

	stats := bus.Stats()
	assert.Equal(t, 0, stats.Topics)
	assert.Equal(t, 0, stats.Subscriptions)
	assert.Equal(t, 256, stats.BufferSize)
	assert.False(t, stats.Closed)
}

// TestChannelBus_Subscribe tests subscribing to a topic
func TestChannelBus_Subscribe(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	// Subscribe to a topic
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

// TestChannelBus_PublishSubscribe tests publishing and receiving messages
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

	// Publish a message
	msg := &guide.Message{
		ID:      "msg-1",
		Type:    guide.MessageTypeRequest,
		Payload: map[string]any{"test": "data"},
	}

	err = bus.Publish("test-topic", msg)
	require.NoError(t, err)

	// Wait for message
	select {
	case recvMsg := <-received:
		assert.Equal(t, "msg-1", recvMsg.ID)
		assert.Equal(t, guide.MessageTypeRequest, recvMsg.Type)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

// TestChannelBus_MultipleSubscribers tests multiple subscribers on same topic
func TestChannelBus_MultipleSubscribers(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	var count int32

	// Create multiple subscribers
	for i := 0; i < 3; i++ {
		_, err := bus.Subscribe("multi-topic", func(msg *guide.Message) error {
			atomic.AddInt32(&count, 1)
			return nil
		})
		require.NoError(t, err)
	}

	stats := bus.Stats()
	assert.Equal(t, 3, stats.Subscriptions)

	// Publish a message
	err := bus.Publish("multi-topic", &guide.Message{ID: "msg-1"})
	require.NoError(t, err)

	// Wait for all subscribers to process
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(3), atomic.LoadInt32(&count))
}

// TestChannelBus_Unsubscribe tests unsubscribing from a topic
func TestChannelBus_Unsubscribe(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	sub, err := bus.Subscribe("test-topic", func(msg *guide.Message) error {
		return nil
	})
	require.NoError(t, err)

	// Unsubscribe
	err = sub.Unsubscribe()
	require.NoError(t, err)

	assert.False(t, sub.IsActive())

	// Second unsubscribe should fail
	err = sub.Unsubscribe()
	assert.Error(t, err)
}

// TestChannelBus_SubscribeAsync tests async subscription
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

// TestChannelBus_Close tests closing the bus
func TestChannelBus_Close(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())

	_, err := bus.Subscribe("topic", func(msg *guide.Message) error {
		return nil
	})
	require.NoError(t, err)

	// Close the bus
	err = bus.Close()
	require.NoError(t, err)

	// Verify bus is closed
	stats := bus.Stats()
	assert.True(t, stats.Closed)

	// Publish should fail
	err = bus.Publish("topic", &guide.Message{ID: "msg-1"})
	assert.Error(t, err)

	// Subscribe should fail
	_, err = bus.Subscribe("topic2", func(msg *guide.Message) error { return nil })
	assert.Error(t, err)

	// Double close should fail
	err = bus.Close()
	assert.Error(t, err)
}

// TestChannelBus_NilHandler tests that nil handler is rejected
func TestChannelBus_NilHandler(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	_, err := bus.Subscribe("topic", nil)
	assert.Error(t, err)

	_, err = bus.SubscribeAsync("topic", nil)
	assert.Error(t, err)
}

// TestChannelBus_TopicSubscriberCount tests subscriber counting
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

// TestChannelBus_ConcurrentPublish tests concurrent publishing
func TestChannelBus_ConcurrentPublish(t *testing.T) {
	bus := guide.NewChannelBus(guide.ChannelBusConfig{BufferSize: 1000})
	defer bus.Close()

	var count int64

	_, err := bus.Subscribe("concurrent", func(msg *guide.Message) error {
		atomic.AddInt64(&count, 1)
		return nil
	})
	require.NoError(t, err)

	// Publish from multiple goroutines
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

	// All messages should be received
	assert.Equal(t, int64(100), atomic.LoadInt64(&count))
}

// TestChannelBus_HandlerPanicRecovery tests that handler panics don't crash the bus
func TestChannelBus_HandlerPanicRecovery(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	var afterPanic int32

	// First subscriber panics
	_, err := bus.Subscribe("panic-topic", func(msg *guide.Message) error {
		panic("test panic")
	})
	require.NoError(t, err)

	// Second subscriber should still work
	_, err = bus.Subscribe("panic-topic", func(msg *guide.Message) error {
		atomic.StoreInt32(&afterPanic, 1)
		return nil
	})
	require.NoError(t, err)

	// Publish - first handler panics but second should still receive
	bus.Publish("panic-topic", &guide.Message{ID: "msg-1"})

	time.Sleep(100 * time.Millisecond)

	// Second handler should have run
	assert.Equal(t, int32(1), atomic.LoadInt32(&afterPanic))
}
