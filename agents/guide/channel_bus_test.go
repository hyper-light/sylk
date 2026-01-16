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
