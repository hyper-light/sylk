package session

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignalDispatcher_SendAndReceive(t *testing.T) {
	baseDir := t.TempDir()

	sender, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   baseDir,
		SessionID: "sender",
	})
	require.NoError(t, err)
	defer sender.Close()

	receiver, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   baseDir,
		SessionID: "receiver",
	})
	require.NoError(t, err)
	defer receiver.Close()

	received := make(chan CrossSessionSignal, 1)
	receiver.RegisterHandler(SignalPreempt, func(sig CrossSessionSignal) {
		received <- sig
	})

	ctx := t.Context()

	require.NoError(t, receiver.Watch(ctx))

	time.Sleep(50 * time.Millisecond)

	signal := NewPreemptSignal("sender", "receiver", "test-payload")
	err = sender.SendSignal(signal)
	require.NoError(t, err)

	select {
	case sig := <-received:
		assert.Equal(t, SignalPreempt, sig.Type)
		assert.Equal(t, "sender", sig.FromSession)
		assert.Equal(t, "test-payload", sig.Payload)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for signal")
	}
}

func TestSignalDispatcher_SignalConsumed(t *testing.T) {
	baseDir := t.TempDir()

	dispatcher, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   baseDir,
		SessionID: "test-session",
	})
	require.NoError(t, err)
	defer dispatcher.Close()

	received := make(chan struct{}, 1)
	dispatcher.RegisterHandler(SignalPressure, func(sig CrossSessionSignal) {
		received <- struct{}{}
	})

	ctx := t.Context()

	require.NoError(t, dispatcher.Watch(ctx))

	time.Sleep(200 * time.Millisecond)

	signalFile := filepath.Join(baseDir, "test-session", "pressure-123.signal")
	data := `{"type":"pressure","from_session":"other","timestamp":"2024-01-01T00:00:00Z"}`
	require.NoError(t, os.WriteFile(signalFile, []byte(data), 0644))

	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for signal")
	}

	time.Sleep(100 * time.Millisecond)

	_, err = os.Stat(signalFile)
	assert.True(t, os.IsNotExist(err))
}

func TestSignalDispatcher_MultipleHandlers(t *testing.T) {
	baseDir := t.TempDir()

	dispatcher, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   baseDir,
		SessionID: "test-session",
	})
	require.NoError(t, err)
	defer dispatcher.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	handler1Called := false
	handler2Called := false

	dispatcher.RegisterHandler(SignalShutdown, func(sig CrossSessionSignal) {
		handler1Called = true
		wg.Done()
	})

	dispatcher.RegisterHandler(SignalShutdown, func(sig CrossSessionSignal) {
		handler2Called = true
		wg.Done()
	})

	ctx := t.Context()

	require.NoError(t, dispatcher.Watch(ctx))

	time.Sleep(50 * time.Millisecond)

	signalFile := filepath.Join(baseDir, "test-session", "shutdown-123.signal")
	data := `{"type":"shutdown","from_session":"other","timestamp":"2024-01-01T00:00:00Z"}`
	require.NoError(t, os.WriteFile(signalFile, []byte(data), 0644))

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		assert.True(t, handler1Called)
		assert.True(t, handler2Called)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for handlers")
	}
}

func TestSignalDispatcher_CleanupOnClose(t *testing.T) {
	baseDir := t.TempDir()

	dispatcher, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   baseDir,
		SessionID: "cleanup-test",
	})
	require.NoError(t, err)

	sessionDir := dispatcher.SignalDir()
	_, err = os.Stat(sessionDir)
	require.NoError(t, err)

	require.NoError(t, dispatcher.Close())

	_, err = os.Stat(sessionDir)
	assert.True(t, os.IsNotExist(err))
}

func TestSignalDispatcher_ClosedOperations(t *testing.T) {
	baseDir := t.TempDir()

	dispatcher, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   baseDir,
		SessionID: "test-session",
	})
	require.NoError(t, err)

	require.NoError(t, dispatcher.Close())

	err = dispatcher.SendSignal(NewShutdownSignal("test"))
	assert.ErrorIs(t, err, ErrDispatcherClosed)

	err = dispatcher.Watch(context.Background())
	assert.ErrorIs(t, err, ErrDispatcherClosed)

	err = dispatcher.Close()
	assert.ErrorIs(t, err, ErrDispatcherClosed)
}

func TestSignalDispatcher_SessionID(t *testing.T) {
	baseDir := t.TempDir()

	dispatcher, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   baseDir,
		SessionID: "my-session",
	})
	require.NoError(t, err)
	defer dispatcher.Close()

	assert.Equal(t, "my-session", dispatcher.SessionID())
}

func TestSignalDispatcher_SignalDir(t *testing.T) {
	baseDir := t.TempDir()

	dispatcher, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   baseDir,
		SessionID: "my-session",
	})
	require.NoError(t, err)
	defer dispatcher.Close()

	expected := filepath.Join(baseDir, "my-session")
	assert.Equal(t, expected, dispatcher.SignalDir())
}

func TestSignal_Constructors(t *testing.T) {
	preempt := NewPreemptSignal("from", "to", "payload")
	assert.Equal(t, SignalPreempt, preempt.Type)
	assert.Equal(t, "from", preempt.FromSession)
	assert.Equal(t, "to", preempt.ToSession)
	assert.Equal(t, "payload", preempt.Payload)
	assert.True(t, preempt.IsTargeted())
	assert.False(t, preempt.IsBroadcast())

	pressure := NewPressureSignal("from", "payload")
	assert.Equal(t, SignalPressure, pressure.Type)
	assert.True(t, pressure.IsBroadcast())

	rebalance := NewRebalanceSignal("from", "payload")
	assert.Equal(t, SignalRebalance, rebalance.Type)

	shutdown := NewShutdownSignal("from")
	assert.Equal(t, SignalShutdown, shutdown.Type)
	assert.Empty(t, shutdown.Payload)
}

func TestSignalDispatcher_SenderDoesNotReceiveOwnBroadcast(t *testing.T) {
	baseDir := t.TempDir()

	dispatcher, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   baseDir,
		SessionID: "self-sender",
	})
	require.NoError(t, err)
	defer dispatcher.Close()

	received := make(chan struct{}, 1)
	dispatcher.RegisterHandler(SignalRebalance, func(sig CrossSessionSignal) {
		received <- struct{}{}
	})

	ctx := t.Context()

	require.NoError(t, dispatcher.Watch(ctx))

	time.Sleep(50 * time.Millisecond)

	signal := NewRebalanceSignal("self-sender", "test")
	require.NoError(t, dispatcher.SendSignal(signal))

	select {
	case <-received:
		t.Fatal("should not receive own broadcast")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestSignalDispatcher_MergeStopChannels_NormalShutdown(t *testing.T) {
	baseDir := t.TempDir()

	dispatcher, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   baseDir,
		SessionID: "test-session",
	})
	require.NoError(t, err)

	ctx := t.Context()
	require.NoError(t, dispatcher.Watch(ctx))

	time.Sleep(50 * time.Millisecond)

	err = dispatcher.Close()
	require.NoError(t, err)
}

func TestSignalDispatcher_MergeStopChannels_ContextCancellation(t *testing.T) {
	baseDir := t.TempDir()

	dispatcher, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   baseDir,
		SessionID: "test-session",
	})
	require.NoError(t, err)
	defer dispatcher.Close()

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, dispatcher.Watch(ctx))

	time.Sleep(50 * time.Millisecond)

	cancel()

	time.Sleep(100 * time.Millisecond)
}

func TestSignalDispatcher_MergeStopChannels_WatcherClosesCleansUp(t *testing.T) {
	baseDir := t.TempDir()

	dispatcher, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
		BaseDir:   baseDir,
		SessionID: "test-session",
	})
	require.NoError(t, err)

	ctx := t.Context()
	require.NoError(t, dispatcher.Watch(ctx))

	time.Sleep(50 * time.Millisecond)

	dispatcher.watcher.Close()

	select {
	case <-dispatcher.doneChan:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for doneChan to close after watcher close")
	}
}

func TestSignalDispatcher_MergeStopChannels_NoLeakOnRapidStartStop(t *testing.T) {
	for i := 0; i < 10; i++ {
		baseDir := t.TempDir()

		dispatcher, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
			BaseDir:   baseDir,
			SessionID: "rapid-test",
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		require.NoError(t, dispatcher.Watch(ctx))

		time.Sleep(10 * time.Millisecond)

		cancel()
		dispatcher.Close()
	}
}
