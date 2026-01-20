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

// TestSignalDispatcher_W12_4_GoroutineCleanup tests that mergeStopChannels
// properly tracks and cleans up its internal goroutine (W12.4 fix).
func TestSignalDispatcher_W12_4_GoroutineCleanup(t *testing.T) {
	t.Run("cleanup waits for goroutine completion", func(t *testing.T) {
		baseDir := t.TempDir()

		dispatcher, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
			BaseDir:   baseDir,
			SessionID: "w12-4-test",
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		require.NoError(t, dispatcher.Watch(ctx))

		// Give time for watch loop to start
		time.Sleep(50 * time.Millisecond)

		// Cancel context which triggers cleanup
		cancel()

		// Close should complete without hanging (cleanup waits for goroutine)
		done := make(chan struct{})
		go func() {
			dispatcher.Close()
			close(done)
		}()

		select {
		case <-done:
			// Success - Close completed
		case <-time.After(2 * time.Second):
			t.Fatal("Close() timed out - goroutine cleanup may be stuck")
		}
	})

	t.Run("multiple rapid watch and close cycles", func(t *testing.T) {
		// Test that rapid cycles don't leak goroutines
		for i := 0; i < 20; i++ {
			baseDir := t.TempDir()

			dispatcher, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
				BaseDir:   baseDir,
				SessionID: "w12-4-rapid",
			})
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			err = dispatcher.Watch(ctx)
			require.NoError(t, err)

			// Immediate cancel and close
			cancel()

			// Use timeout to detect any hang
			done := make(chan error, 1)
			go func() {
				done <- dispatcher.Close()
			}()

			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(1 * time.Second):
				t.Fatalf("iteration %d: Close() timed out", i)
			}
		}
	})

	t.Run("stopChan signal triggers cleanup", func(t *testing.T) {
		baseDir := t.TempDir()

		dispatcher, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
			BaseDir:   baseDir,
			SessionID: "w12-4-stop",
		})
		require.NoError(t, err)

		ctx := context.Background()
		require.NoError(t, dispatcher.Watch(ctx))

		time.Sleep(50 * time.Millisecond)

		// Close triggers stopChan
		done := make(chan struct{})
		go func() {
			dispatcher.Close()
			close(done)
		}()

		select {
		case <-done:
			// Success
		case <-time.After(2 * time.Second):
			t.Fatal("Close() timed out on stopChan signal")
		}
	})
}

// TestSignalDispatcher_W12_6_BroadcastErrorHandling tests that sendBroadcast
// collects and returns errors from individual send attempts (W12.6 fix).
func TestSignalDispatcher_W12_6_BroadcastErrorHandling(t *testing.T) {
	t.Run("successful broadcast returns no error", func(t *testing.T) {
		baseDir := t.TempDir()

		// Create sender
		sender, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
			BaseDir:   baseDir,
			SessionID: "sender",
		})
		require.NoError(t, err)
		defer sender.Close()

		// Create receivers
		receiver1, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
			BaseDir:   baseDir,
			SessionID: "receiver1",
		})
		require.NoError(t, err)
		defer receiver1.Close()

		receiver2, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
			BaseDir:   baseDir,
			SessionID: "receiver2",
		})
		require.NoError(t, err)
		defer receiver2.Close()

		// Send broadcast - should succeed
		signal := NewPressureSignal("sender", "test-payload")
		err = sender.SendSignal(signal)
		require.NoError(t, err)
	})

	t.Run("broadcast to non-existent session dir succeeds", func(t *testing.T) {
		baseDir := t.TempDir()

		sender, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
			BaseDir:   baseDir,
			SessionID: "sender",
		})
		require.NoError(t, err)
		defer sender.Close()

		// Create a receiver directory (simulating another session)
		receiverDir := filepath.Join(baseDir, "receiver")
		require.NoError(t, os.MkdirAll(receiverDir, 0755))

		// Send broadcast - should succeed because writeSignalFile creates dirs
		signal := NewPressureSignal("sender", "test-payload")
		err = sender.SendSignal(signal)
		require.NoError(t, err)
	})

	t.Run("broadcast collects errors from failed writes", func(t *testing.T) {
		baseDir := t.TempDir()

		sender, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
			BaseDir:   baseDir,
			SessionID: "sender",
		})
		require.NoError(t, err)
		defer sender.Close()

		// Create a receiver directory that we'll make read-only
		receiverDir := filepath.Join(baseDir, "receiver")
		require.NoError(t, os.MkdirAll(receiverDir, 0755))

		// Make the directory read-only so writes fail
		require.NoError(t, os.Chmod(receiverDir, 0444))
		defer os.Chmod(receiverDir, 0755) // Cleanup

		// Send broadcast - should return an error
		signal := NewPressureSignal("sender", "test-payload")
		err = sender.SendSignal(signal)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "receiver")
	})

	t.Run("broadcast aggregates multiple errors", func(t *testing.T) {
		baseDir := t.TempDir()

		sender, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
			BaseDir:   baseDir,
			SessionID: "sender",
		})
		require.NoError(t, err)
		defer sender.Close()

		// Create multiple receiver directories that will fail
		receiver1Dir := filepath.Join(baseDir, "receiver1")
		receiver2Dir := filepath.Join(baseDir, "receiver2")
		require.NoError(t, os.MkdirAll(receiver1Dir, 0755))
		require.NoError(t, os.MkdirAll(receiver2Dir, 0755))

		// Make both directories read-only
		require.NoError(t, os.Chmod(receiver1Dir, 0444))
		require.NoError(t, os.Chmod(receiver2Dir, 0444))
		defer func() {
			os.Chmod(receiver1Dir, 0755)
			os.Chmod(receiver2Dir, 0755)
		}()

		// Send broadcast - should return aggregated errors
		signal := NewPressureSignal("sender", "test-payload")
		err = sender.SendSignal(signal)
		assert.Error(t, err)

		// Check that error message contains both receiver names
		errStr := err.Error()
		assert.Contains(t, errStr, "receiver1")
		assert.Contains(t, errStr, "receiver2")
	})

	t.Run("partial failure returns errors for failures only", func(t *testing.T) {
		baseDir := t.TempDir()

		sender, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
			BaseDir:   baseDir,
			SessionID: "sender",
		})
		require.NoError(t, err)
		defer sender.Close()

		// Create two receivers - one that will work, one that will fail
		goodReceiver, err := NewCrossSessionSignalDispatcher(CrossSessionSignalDispatcherConfig{
			BaseDir:   baseDir,
			SessionID: "good-receiver",
		})
		require.NoError(t, err)
		defer goodReceiver.Close()

		badReceiverDir := filepath.Join(baseDir, "bad-receiver")
		require.NoError(t, os.MkdirAll(badReceiverDir, 0755))
		require.NoError(t, os.Chmod(badReceiverDir, 0444))
		defer os.Chmod(badReceiverDir, 0755)

		// Send broadcast
		signal := NewPressureSignal("sender", "test-payload")
		err = sender.SendSignal(signal)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "bad-receiver")
		assert.NotContains(t, err.Error(), "good-receiver")

		// Verify the good receiver got the signal
		files, readErr := os.ReadDir(filepath.Join(baseDir, "good-receiver"))
		require.NoError(t, readErr)
		signalFound := false
		for _, f := range files {
			if filepath.Ext(f.Name()) == ".signal" {
				signalFound = true
				break
			}
		}
		assert.True(t, signalFound, "good-receiver should have received the signal")
	})

	t.Run("targeted signal not affected by broadcast changes", func(t *testing.T) {
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

		// Send targeted signal - should succeed
		signal := NewPreemptSignal("sender", "receiver", "test-payload")
		err = sender.SendSignal(signal)
		require.NoError(t, err)
	})
}
