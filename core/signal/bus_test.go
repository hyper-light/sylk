package signal_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/signal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignalBus_Subscribe(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	sub := signal.SignalSubscriber{
		ID:      "sub-1",
		AgentID: "agent-1",
		Signals: []signal.Signal{signal.PauseAll, signal.ResumeAll},
		Channel: make(chan signal.SignalMessage, 10),
	}

	err := bus.Subscribe(sub)
	require.NoError(t, err)

	// Duplicate subscription should fail
	err = bus.Subscribe(sub)
	assert.Equal(t, signal.ErrSubscriberExists, err)
}

func TestSignalBus_Unsubscribe(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	sub := signal.SignalSubscriber{
		ID:      "sub-1",
		AgentID: "agent-1",
		Signals: []signal.Signal{signal.PauseAll},
		Channel: make(chan signal.SignalMessage, 10),
	}

	err := bus.Subscribe(sub)
	require.NoError(t, err)

	err = bus.Unsubscribe("sub-1")
	require.NoError(t, err)

	// Unsubscribe non-existent should fail
	err = bus.Unsubscribe("sub-1")
	assert.Equal(t, signal.ErrSubscriberNotFound, err)
}

func TestSignalBus_Broadcast(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	ch1 := make(chan signal.SignalMessage, 10)
	ch2 := make(chan signal.SignalMessage, 10)

	sub1 := signal.SignalSubscriber{
		ID:      "sub-1",
		AgentID: "agent-1",
		Signals: []signal.Signal{signal.PauseAll},
		Channel: ch1,
	}

	sub2 := signal.SignalSubscriber{
		ID:      "sub-2",
		AgentID: "agent-2",
		Signals: []signal.Signal{signal.PauseAll, signal.ResumeAll},
		Channel: ch2,
	}

	require.NoError(t, bus.Subscribe(sub1))
	require.NoError(t, bus.Subscribe(sub2))

	msg := signal.SignalMessage{
		ID:          "msg-1",
		Signal:      signal.PauseAll,
		TargetID:    "",
		Reason:      "test pause",
		RequiresAck: false,
		Timeout:     5 * time.Second,
		SentAt:      time.Now(),
	}

	err := bus.Broadcast(msg)
	require.NoError(t, err)

	// Both subscribers should receive
	select {
	case received := <-ch1:
		assert.Equal(t, msg.ID, received.ID)
		assert.Equal(t, signal.PauseAll, received.Signal)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("sub-1 did not receive message")
	}

	select {
	case received := <-ch2:
		assert.Equal(t, msg.ID, received.ID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("sub-2 did not receive message")
	}
}

func TestSignalBus_BroadcastOnlySubscribedSignals(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	ch1 := make(chan signal.SignalMessage, 10)
	ch2 := make(chan signal.SignalMessage, 10)

	sub1 := signal.SignalSubscriber{
		ID:      "sub-1",
		AgentID: "agent-1",
		Signals: []signal.Signal{signal.PauseAll},
		Channel: ch1,
	}

	sub2 := signal.SignalSubscriber{
		ID:      "sub-2",
		AgentID: "agent-2",
		Signals: []signal.Signal{signal.ResumeAll},
		Channel: ch2,
	}

	require.NoError(t, bus.Subscribe(sub1))
	require.NoError(t, bus.Subscribe(sub2))

	msg := signal.SignalMessage{
		ID:          "msg-1",
		Signal:      signal.ResumeAll,
		RequiresAck: false,
	}

	err := bus.Broadcast(msg)
	require.NoError(t, err)

	// Only sub2 should receive (subscribed to ResumeAll)
	select {
	case <-ch1:
		t.Fatal("sub-1 should not receive ResumeAll")
	case <-time.After(50 * time.Millisecond):
		// Expected - sub-1 not subscribed to ResumeAll
	}

	select {
	case received := <-ch2:
		assert.Equal(t, signal.ResumeAll, received.Signal)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("sub-2 should receive ResumeAll")
	}
}

func TestSignalBus_Acknowledge(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	ch := make(chan signal.SignalMessage, 10)
	sub := signal.SignalSubscriber{
		ID:      "sub-1",
		AgentID: "agent-1",
		Signals: []signal.Signal{signal.PauseAll},
		Channel: ch,
	}

	require.NoError(t, bus.Subscribe(sub))

	msg := signal.SignalMessage{
		ID:          "msg-1",
		Signal:      signal.PauseAll,
		RequiresAck: true,
		Timeout:     5 * time.Second,
	}

	err := bus.Broadcast(msg)
	require.NoError(t, err)

	// Consume message
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("did not receive message")
	}

	// Send acknowledgment
	ack := signal.SignalAck{
		SignalID:     "msg-1",
		SubscriberID: "sub-1",
		AgentID:      "agent-1",
		ReceivedAt:   time.Now(),
		State:        "paused",
	}

	err = bus.Acknowledge(ack)
	require.NoError(t, err)

	// Wait for acks should complete immediately
	acks, err := bus.WaitForAcks("msg-1", time.Second)
	require.NoError(t, err)
	assert.Len(t, acks, 1)
	assert.Equal(t, "sub-1", acks[0].SubscriberID)
}

func TestSignalBus_WaitForAcksTimeout(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	ch := make(chan signal.SignalMessage, 10)
	sub := signal.SignalSubscriber{
		ID:      "sub-1",
		AgentID: "agent-1",
		Signals: []signal.Signal{signal.PauseAll},
		Channel: ch,
	}

	require.NoError(t, bus.Subscribe(sub))

	msg := signal.SignalMessage{
		ID:          "msg-1",
		Signal:      signal.PauseAll,
		RequiresAck: true,
		Timeout:     100 * time.Millisecond,
	}

	err := bus.Broadcast(msg)
	require.NoError(t, err)

	// Don't acknowledge - should timeout
	start := time.Now()
	acks, err := bus.WaitForAcks("msg-1", 100*time.Millisecond)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Empty(t, acks)
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond)
}

func TestSignalBus_UnsubscribeStopsDelivery(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	ch := make(chan signal.SignalMessage, 10)
	sub := signal.SignalSubscriber{
		ID:      "sub-1",
		AgentID: "agent-1",
		Signals: []signal.Signal{signal.PauseAll},
		Channel: ch,
	}

	require.NoError(t, bus.Subscribe(sub))
	require.NoError(t, bus.Unsubscribe("sub-1"))

	msg := signal.SignalMessage{
		ID:     "msg-1",
		Signal: signal.PauseAll,
	}

	err := bus.Broadcast(msg)
	require.NoError(t, err)

	// Should not receive after unsubscribe
	select {
	case <-ch:
		t.Fatal("should not receive after unsubscribe")
	case <-time.After(50 * time.Millisecond):
		// Expected
	}
}

func TestSignalBus_ConcurrentBroadcastSubscribe(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	var wg sync.WaitGroup
	var received int64

	// Start multiple subscribers concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			ch := make(chan signal.SignalMessage, 100)
			sub := signal.SignalSubscriber{
				ID:      formatSubID(idx),
				AgentID: formatAgentID(idx),
				Signals: []signal.Signal{signal.PauseAll},
				Channel: ch,
			}

			if err := bus.Subscribe(sub); err != nil {
				return
			}

			// Receive messages
			go func() {
				for range ch {
					atomic.AddInt64(&received, 1)
				}
			}()
		}(i)
	}

	// Allow subscribers to register
	time.Sleep(10 * time.Millisecond)

	// Broadcast concurrently
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			msg := signal.SignalMessage{
				ID:     formatMsgID(idx),
				Signal: signal.PauseAll,
			}
			_ = bus.Broadcast(msg)
		}(i)
	}

	wg.Wait()
	time.Sleep(50 * time.Millisecond)

	// Should have received messages (exact count depends on timing)
	assert.Greater(t, atomic.LoadInt64(&received), int64(0))
}

func TestSignalBus_ClosedBus(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())

	sub := signal.SignalSubscriber{
		ID:      "sub-1",
		AgentID: "agent-1",
		Signals: []signal.Signal{signal.PauseAll},
		Channel: make(chan signal.SignalMessage, 10),
	}

	bus.Close()

	err := bus.Subscribe(sub)
	assert.Equal(t, signal.ErrBusClosed, err)

	err = bus.Broadcast(signal.SignalMessage{})
	assert.Equal(t, signal.ErrBusClosed, err)
}

func TestSignalBus_MultipleAcks(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	ch1 := make(chan signal.SignalMessage, 10)
	ch2 := make(chan signal.SignalMessage, 10)
	ch3 := make(chan signal.SignalMessage, 10)

	subs := []signal.SignalSubscriber{
		{ID: "sub-1", AgentID: "agent-1", Signals: []signal.Signal{signal.PauseAll}, Channel: ch1},
		{ID: "sub-2", AgentID: "agent-2", Signals: []signal.Signal{signal.PauseAll}, Channel: ch2},
		{ID: "sub-3", AgentID: "agent-3", Signals: []signal.Signal{signal.PauseAll}, Channel: ch3},
	}

	for _, sub := range subs {
		require.NoError(t, bus.Subscribe(sub))
	}

	msg := signal.SignalMessage{
		ID:          "msg-1",
		Signal:      signal.PauseAll,
		RequiresAck: true,
	}

	err := bus.Broadcast(msg)
	require.NoError(t, err)

	// Consume messages
	<-ch1
	<-ch2
	<-ch3

	// Send acks from all subscribers
	for _, sub := range subs {
		ack := signal.SignalAck{
			SignalID:     "msg-1",
			SubscriberID: sub.ID,
			AgentID:      sub.AgentID,
			ReceivedAt:   time.Now(),
			State:        "paused",
		}
		require.NoError(t, bus.Acknowledge(ack))
	}

	acks, err := bus.WaitForAcks("msg-1", time.Second)
	require.NoError(t, err)
	assert.Len(t, acks, 3)
}

func TestSignalHandler_StartStop(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	handler := signal.NewSignalHandler(bus, "agent-1", []signal.Signal{signal.PauseAll, signal.ResumeAll})

	err := handler.Start()
	require.NoError(t, err)

	// Duplicate start should be idempotent
	err = handler.Start()
	require.NoError(t, err)

	handler.Stop()

	// Stop again should be safe
	handler.Stop()
}

func TestSignalHandler_ReceiveSignals(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	handler := signal.NewSignalHandler(bus, "agent-1", []signal.Signal{signal.PauseAll})

	var received signal.SignalMessage
	var receivedMu sync.Mutex
	done := make(chan struct{})

	handler.OnSignal(func(msg signal.SignalMessage) {
		receivedMu.Lock()
		received = msg
		receivedMu.Unlock()
		close(done)
	})

	err := handler.Start()
	require.NoError(t, err)
	defer handler.Stop()

	msg := signal.SignalMessage{
		ID:     "msg-1",
		Signal: signal.PauseAll,
		Reason: "test",
	}

	err = bus.Broadcast(msg)
	require.NoError(t, err)

	select {
	case <-done:
		receivedMu.Lock()
		assert.Equal(t, "msg-1", received.ID)
		assert.Equal(t, signal.PauseAll, received.Signal)
		receivedMu.Unlock()
	case <-time.After(time.Second):
		t.Fatal("handler did not receive signal")
	}
}

func TestSignalHandler_Ack(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	handler := signal.NewSignalHandler(bus, "agent-1", []signal.Signal{signal.PauseAll})

	done := make(chan struct{})

	handler.OnSignal(func(msg signal.SignalMessage) {
		err := handler.Ack(msg, "paused", nil)
		assert.NoError(t, err)
		close(done)
	})

	err := handler.Start()
	require.NoError(t, err)
	defer handler.Stop()

	msg := signal.SignalMessage{
		ID:          "msg-1",
		Signal:      signal.PauseAll,
		RequiresAck: true,
	}

	err = bus.Broadcast(msg)
	require.NoError(t, err)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not process signal")
	}

	acks, err := bus.WaitForAcks("msg-1", time.Second)
	require.NoError(t, err)
	assert.Len(t, acks, 1)
	assert.Equal(t, "paused", acks[0].State)
}

func TestNewSignalMessage(t *testing.T) {
	msg := signal.NewSignalMessage(signal.PauseAll, "target-1", true)

	assert.NotEmpty(t, msg.ID)
	assert.Equal(t, signal.PauseAll, msg.Signal)
	assert.Equal(t, "target-1", msg.TargetID)
	assert.True(t, msg.RequiresAck)
	assert.Equal(t, 5*time.Second, msg.Timeout)
	assert.False(t, msg.SentAt.IsZero())
}

func TestValidSignals(t *testing.T) {
	signals := signal.ValidSignals()

	assert.Contains(t, signals, signal.PauseAll)
	assert.Contains(t, signals, signal.ResumeAll)
	assert.Contains(t, signals, signal.PausePipeline)
	assert.Contains(t, signals, signal.ResumePipeline)
	assert.Contains(t, signals, signal.CancelTask)
	assert.Contains(t, signals, signal.AbortSession)
	assert.Contains(t, signals, signal.QuotaWarning)
	assert.Contains(t, signals, signal.StateChanged)
	assert.Contains(t, signals, signal.EvictCaches)
	assert.Contains(t, signals, signal.CompactContexts)
	assert.Contains(t, signals, signal.MemoryPressureChanged)
	assert.Len(t, signals, 11)
}

// Helper functions
func formatSubID(idx int) string {
	return "sub-" + itoa(idx)
}

func formatAgentID(idx int) string {
	return "agent-" + itoa(idx)
}

func formatMsgID(idx int) string {
	return "msg-" + itoa(idx)
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	result := ""
	for i > 0 {
		result = string(digits[i%10]) + result
		i /= 10
	}
	return result
}
