package resources

import (
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/signal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignalBusPublisher_ImplementsInterface(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	var publisher SignalPublisher = NewSignalBusPublisher(bus)
	assert.NotNil(t, publisher)
}

func TestSignalBusPublisher_PublishMemorySignal_ComponentLRU(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	received := make(chan signal.SignalMessage, 1)
	sub := signal.SignalSubscriber{
		ID:      "test-sub",
		Signals: []signal.Signal{signal.EvictCaches},
		Channel: received,
	}
	require.NoError(t, bus.Subscribe(sub))

	publisher := NewSignalBusPublisher(bus)
	payload := MemorySignalPayload{
		Component:     "test-component",
		CurrentBytes:  100,
		BudgetBytes:   80,
		GlobalBytes:   1000,
		GlobalCeiling: 2000,
		Level:         MemoryPressureHigh,
		Timestamp:     time.Now(),
	}

	err := publisher.PublishMemorySignal(SignalComponentLRU, payload)
	require.NoError(t, err)

	select {
	case msg := <-received:
		assert.Equal(t, signal.EvictCaches, msg.Signal)
		assert.Equal(t, "test-component", msg.TargetID)

		evictPayload, ok := msg.Payload.(signal.EvictCachesPayload)
		require.True(t, ok, "Payload should be EvictCachesPayload")
		assert.Equal(t, 0.25, evictPayload.Percent)
		assert.Equal(t, string(SignalComponentLRU), evictPayload.Reason)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for signal")
	}
}

func TestSignalBusPublisher_PublishMemorySignal_ComponentAggressive(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	received := make(chan signal.SignalMessage, 1)
	sub := signal.SignalSubscriber{
		ID:      "test-sub",
		Signals: []signal.Signal{signal.EvictCaches},
		Channel: received,
	}
	require.NoError(t, bus.Subscribe(sub))

	publisher := NewSignalBusPublisher(bus)
	payload := MemorySignalPayload{
		Component:     "test-component",
		CurrentBytes:  100,
		BudgetBytes:   80,
		GlobalBytes:   1000,
		GlobalCeiling: 2000,
		Level:         MemoryPressureCritical,
		Timestamp:     time.Now(),
	}

	err := publisher.PublishMemorySignal(SignalComponentAggressive, payload)
	require.NoError(t, err)

	select {
	case msg := <-received:
		evictPayload, ok := msg.Payload.(signal.EvictCachesPayload)
		require.True(t, ok)
		assert.Equal(t, 0.50, evictPayload.Percent)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for signal")
	}
}

func TestSignalBusPublisher_PublishMemorySignal_GlobalEmergency(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	received := make(chan signal.SignalMessage, 1)
	sub := signal.SignalSubscriber{
		ID:      "test-sub",
		Signals: []signal.Signal{signal.MemoryPressureChanged},
		Channel: received,
	}
	require.NoError(t, bus.Subscribe(sub))

	publisher := NewSignalBusPublisher(bus)
	now := time.Now()
	payload := MemorySignalPayload{
		Component:     "global",
		CurrentBytes:  1800,
		BudgetBytes:   2000,
		GlobalBytes:   1800,
		GlobalCeiling: 2000,
		Level:         MemoryPressureCritical,
		Timestamp:     now,
	}

	err := publisher.PublishMemorySignal(SignalGlobalEmergency, payload)
	require.NoError(t, err)

	select {
	case msg := <-received:
		assert.Equal(t, signal.MemoryPressureChanged, msg.Signal)

		pressurePayload, ok := msg.Payload.(signal.MemoryPressurePayload)
		require.True(t, ok, "Payload should be MemoryPressurePayload")
		assert.Equal(t, "critical", pressurePayload.To)
		assert.InDelta(t, 0.9, pressurePayload.Usage, 0.01)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for signal")
	}
}

func TestSignalBusPublisher_PublishMemorySignal_GlobalPause(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	received := make(chan signal.SignalMessage, 1)
	sub := signal.SignalSubscriber{
		ID:      "test-sub",
		Signals: []signal.Signal{signal.MemoryPressureChanged},
		Channel: received,
	}
	require.NoError(t, bus.Subscribe(sub))

	publisher := NewSignalBusPublisher(bus)
	err := publisher.PublishMemorySignal(SignalGlobalPause, MemorySignalPayload{
		Level:         MemoryPressureHigh,
		GlobalBytes:   700,
		GlobalCeiling: 1000,
		Timestamp:     time.Now(),
	})
	require.NoError(t, err)

	select {
	case msg := <-received:
		assert.Equal(t, signal.MemoryPressureChanged, msg.Signal)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for signal")
	}
}

func TestSignalBusPublisher_PublishMemorySignal_GlobalResume(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	received := make(chan signal.SignalMessage, 1)
	sub := signal.SignalSubscriber{
		ID:      "test-sub",
		Signals: []signal.Signal{signal.MemoryPressureChanged},
		Channel: received,
	}
	require.NoError(t, bus.Subscribe(sub))

	publisher := NewSignalBusPublisher(bus)
	err := publisher.PublishMemorySignal(SignalGlobalResume, MemorySignalPayload{
		Level:         MemoryPressureNone,
		GlobalBytes:   300,
		GlobalCeiling: 1000,
		Timestamp:     time.Now(),
	})
	require.NoError(t, err)

	select {
	case msg := <-received:
		pressurePayload, ok := msg.Payload.(signal.MemoryPressurePayload)
		require.True(t, ok)
		assert.Equal(t, "none", pressurePayload.To)
		assert.InDelta(t, 0.3, pressurePayload.Usage, 0.01)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for signal")
	}
}

func TestSignalBusPublisher_ConcurrentPublish(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	received := make(chan signal.SignalMessage, 100)
	sub := signal.SignalSubscriber{
		ID:      "test-sub",
		Signals: []signal.Signal{signal.EvictCaches, signal.MemoryPressureChanged},
		Channel: received,
	}
	require.NoError(t, bus.Subscribe(sub))

	publisher := NewSignalBusPublisher(bus)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			payload := MemorySignalPayload{
				Component:     ComponentName("comp-" + string(rune('a'+idx))),
				CurrentBytes:  int64(idx * 100),
				BudgetBytes:   1000,
				GlobalBytes:   int64(idx * 100),
				GlobalCeiling: 2000,
				Timestamp:     time.Now(),
			}

			if idx%2 == 0 {
				_ = publisher.PublishMemorySignal(SignalComponentLRU, payload)
			} else {
				_ = publisher.PublishMemorySignal(SignalGlobalPause, payload)
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(50 * time.Millisecond)

	close(received)
	count := 0
	for range received {
		count++
	}
	assert.Equal(t, 10, count, "Should receive all 10 signals")
}

func TestSignalBusPublisher_NonBlockingOnFullChannel(t *testing.T) {
	config := signal.DefaultSignalBusConfig()
	bus := signal.NewSignalBus(config)
	defer bus.Close()

	received := make(chan signal.SignalMessage, 1)
	sub := signal.SignalSubscriber{
		ID:      "test-sub",
		Signals: []signal.Signal{signal.EvictCaches},
		Channel: received,
	}
	require.NoError(t, bus.Subscribe(sub))

	publisher := NewSignalBusPublisher(bus)
	payload := MemorySignalPayload{
		Component:     "test",
		CurrentBytes:  100,
		BudgetBytes:   200,
		GlobalBytes:   100,
		GlobalCeiling: 1000,
		Timestamp:     time.Now(),
	}

	err := publisher.PublishMemorySignal(SignalComponentLRU, payload)
	require.NoError(t, err)

	done := make(chan bool, 1)
	go func() {
		for i := 0; i < 5; i++ {
			_ = publisher.PublishMemorySignal(SignalComponentLRU, payload)
		}
		done <- true
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Publish should be non-blocking even when channel is full")
	}
}

func TestSignalBusPublisher_UsageRatioEdgeCases(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	received := make(chan signal.SignalMessage, 1)
	sub := signal.SignalSubscriber{
		ID:      "test-sub",
		Signals: []signal.Signal{signal.MemoryPressureChanged},
		Channel: received,
	}
	require.NoError(t, bus.Subscribe(sub))

	publisher := NewSignalBusPublisher(bus)

	err := publisher.PublishMemorySignal(SignalGlobalEmergency, MemorySignalPayload{
		GlobalBytes:   100,
		GlobalCeiling: 0,
		Timestamp:     time.Now(),
	})
	require.NoError(t, err)

	select {
	case msg := <-received:
		pressurePayload := msg.Payload.(signal.MemoryPressurePayload)
		assert.Equal(t, 0.0, pressurePayload.Usage, "Usage should be 0 when ceiling is 0")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout")
	}
}
