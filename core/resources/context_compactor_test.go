package resources

import (
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/signal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupCompactorTest(t *testing.T) (*ContextCompactor, *UsageRegistry, chan signal.SignalMessage) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	t.Cleanup(func() { bus.Close() })

	received := make(chan signal.SignalMessage, 10)
	sub := signal.SignalSubscriber{
		ID:      "test-sub",
		Signals: []signal.Signal{signal.CompactContexts},
		Channel: received,
	}
	require.NoError(t, bus.Subscribe(sub))

	registry := NewUsageRegistry()
	compactor := NewContextCompactor(bus, registry)

	return compactor, registry, received
}

func TestContextCompactor_CompactAll(t *testing.T) {
	compactor, _, received := setupCompactorTest(t)

	compactor.CompactAll()

	select {
	case msg := <-received:
		assert.Equal(t, signal.CompactContexts, msg.Signal)
		assert.Equal(t, "", msg.TargetID)

		payload, ok := msg.Payload.(signal.CompactContextsPayload)
		require.True(t, ok)
		assert.True(t, payload.All)
		assert.Equal(t, "Memory pressure", payload.Reason)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No signal received")
	}
}

func TestContextCompactor_CompactLargest(t *testing.T) {
	compactor, registry, received := setupCompactorTest(t)

	registry.Report("agent-1", UsageCategoryLLMBuffers, 1000)
	registry.Report("agent-2", UsageCategoryLLMBuffers, 5000)
	registry.Report("agent-3", UsageCategoryLLMBuffers, 3000)
	registry.Report("cache-1", UsageCategoryCaches, 10000)

	compactor.CompactLargest(2)

	signals := collectSignals(received, 2, 100*time.Millisecond)
	assert.Len(t, signals, 2)

	targetIDs := extractTargetIDs(signals)
	assert.Contains(t, targetIDs, "agent-2")
	assert.Contains(t, targetIDs, "agent-3")
	assert.NotContains(t, targetIDs, "agent-1")
	assert.NotContains(t, targetIDs, "cache-1")
}

func collectSignals(ch chan signal.SignalMessage, maxCount int, timeout time.Duration) []signal.SignalMessage {
	var signals []signal.SignalMessage
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for len(signals) < maxCount {
		select {
		case msg := <-ch:
			signals = append(signals, msg)
		case <-timer.C:
			return signals
		}
	}
	return signals
}

func extractTargetIDs(signals []signal.SignalMessage) []string {
	ids := make([]string, len(signals))
	for i, s := range signals {
		ids[i] = s.TargetID
	}
	return ids
}

func TestContextCompactor_CompactLargest_NoComponents(t *testing.T) {
	compactor, _, received := setupCompactorTest(t)

	compactor.CompactLargest(5)

	select {
	case <-received:
		t.Fatal("Should not receive signal when no components exist")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestContextCompactor_CompactLargest_FewerThanRequested(t *testing.T) {
	compactor, registry, received := setupCompactorTest(t)

	registry.Report("agent-1", UsageCategoryLLMBuffers, 1000)

	compactor.CompactLargest(5)

	signals := collectSignals(received, 5, 100*time.Millisecond)
	assert.Len(t, signals, 1)
	assert.Equal(t, "agent-1", signals[0].TargetID)
}

func TestContextCompactor_NonBlocking(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	slowChan := make(chan signal.SignalMessage, 1)
	sub := signal.SignalSubscriber{
		ID:      "slow-sub",
		Signals: []signal.Signal{signal.CompactContexts},
		Channel: slowChan,
	}
	require.NoError(t, bus.Subscribe(sub))

	registry := NewUsageRegistry()
	compactor := NewContextCompactor(bus, registry)

	done := make(chan bool)
	go func() {
		for i := 0; i < 10; i++ {
			compactor.CompactAll()
		}
		done <- true
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("CompactAll should be non-blocking")
	}
}

func TestContextCompactor_SignalPayload(t *testing.T) {
	compactor, registry, received := setupCompactorTest(t)

	registry.Report("test-agent", UsageCategoryLLMBuffers, 1000)

	compactor.CompactLargest(1)

	select {
	case msg := <-received:
		payload, ok := msg.Payload.(signal.CompactContextsPayload)
		require.True(t, ok)

		assert.Equal(t, "test-agent", payload.TargetID)
		assert.False(t, payload.All)
		assert.Contains(t, payload.Reason, "Memory pressure")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No signal received")
	}
}

func TestContextCompactor_ConcurrentCompact(t *testing.T) {
	compactor, registry, received := setupCompactorTest(t)

	for i := 0; i < 5; i++ {
		registry.Report("agent-"+string(rune('a'+i)), UsageCategoryLLMBuffers, int64((i+1)*1000))
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			compactor.CompactAll()
			compactor.CompactLargest(2)
		}()
	}

	wg.Wait()

	count := 0
	timeout := time.After(100 * time.Millisecond)
	for {
		select {
		case <-received:
			count++
		case <-timeout:
			assert.Greater(t, count, 0, "Should receive at least some signals")
			return
		}
	}
}
