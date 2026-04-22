package versioning

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

func newGateScope(t *testing.T) *concurrency.GoroutineScope {
	t.Helper()
	scope := concurrency.NewGoroutineScope(context.Background(), "dispatch-gate-test", nil)
	t.Cleanup(func() {
		_ = scope.Shutdown(100*time.Millisecond, 2*time.Second)
	})
	return scope
}

func newGateTestSession(t *testing.T, name string) *SessionVFS {
	t.Helper()
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sess, err := NewSessionVFS(SessionVFSConfig{
		SessionID:   SessionID(name),
		WorkingDir:  workDir,
		StorageRoot: filepath.Join(dir, "session"),
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	return sess
}

// TestDispatchGate_PassThroughWhenUncapped verifies that MaxInFlight=0
// makes the gate a pass-through.
func TestDispatchGate_PassThroughWhenUncapped(t *testing.T) {
	sess := newGateTestSession(t, "sess-uncapped")
	defer sess.Close()

	g := NewDispatchGate(DispatchGateConfig{Session: sess, MaxInFlight: 0, Scope: newGateScope(t)})
	if err := g.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer g.Stop()

	// Enqueue many descriptors; Acquire should never block.
	for i := 1; i <= 20; i++ {
		desc := MergeDescriptor{
			PipelineID:    "p",
			BaseVersion:   SemanticVersion{Minor: uint32(i - 1)},
			MergedVersion: SemanticVersion{Minor: uint32(i)},
		}
		if _, err := sess.CommitQueue().Enqueue(desc); err != nil {
			t.Fatal(err)
		}
	}

	done := make(chan error, 1)
	go func() {
		done <- g.Acquire(context.Background())
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Acquire blocked with uncapped gate")
	}
}

// TestDispatchGate_BlocksAtCapacity verifies the gate blocks when
// depth >= MaxInFlight and unblocks when a descriptor reaches
// terminal state.
func TestDispatchGate_BlocksAtCapacity(t *testing.T) {
	sess := newGateTestSession(t, "sess-cap")
	defer sess.Close()

	cap := 2
	g := NewDispatchGate(DispatchGateConfig{Session: sess, MaxInFlight: cap, Scope: newGateScope(t)})
	if err := g.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer g.Stop()

	// Enqueue exactly `cap` descriptors — we are at the ceiling.
	descs := make([]MergeDescriptor, cap)
	for i := 0; i < cap; i++ {
		d := MergeDescriptor{
			PipelineID:    "p",
			BaseVersion:   SemanticVersion{Minor: uint32(i)},
			MergedVersion: SemanticVersion{Minor: uint32(i + 1)},
		}
		descs[i] = d
		if _, err := sess.CommitQueue().Enqueue(d); err != nil {
			t.Fatal(err)
		}
	}

	// Acquire should block.
	var acquired atomic.Bool
	done := make(chan error, 1)
	go func() {
		err := g.Acquire(context.Background())
		acquired.Store(true)
		done <- err
	}()

	time.Sleep(150 * time.Millisecond)
	if acquired.Load() {
		t.Fatal("Acquire returned while at capacity")
	}

	// Drain one: MarkAccepted + MarkCommitted sends it terminal.
	if err := sess.CommitQueue().MarkAccepted(descs[0].MergedVersion, "r"); err != nil {
		t.Fatal(err)
	}
	if err := sess.CommitQueue().MarkCommitted(descs[0].MergedVersion); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Acquire did not unblock after drain")
	}
}

// TestDispatchGate_HonorsContextCancel verifies Acquire returns ctx.Err()
// when the caller's context is cancelled while waiting.
func TestDispatchGate_HonorsContextCancel(t *testing.T) {
	sess := newGateTestSession(t, "sess-cancel")
	defer sess.Close()

	g := NewDispatchGate(DispatchGateConfig{Session: sess, MaxInFlight: 1, Scope: newGateScope(t)})
	if err := g.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer g.Stop()

	// Fill to capacity.
	_, _ = sess.CommitQueue().Enqueue(MergeDescriptor{
		PipelineID:    "p",
		BaseVersion:   SemanticVersion{},
		MergedVersion: SemanticVersion{Minor: 1},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- g.Acquire(ctx)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil || err.Error() != context.Canceled.Error() {
			t.Errorf("Acquire err = %v, want context.Canceled", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Acquire did not return after cancel")
	}
}

// TestDispatchGate_StopWakesWaiters verifies Stop unblocks pending
// Acquires with ErrDispatchGateStopped.
func TestDispatchGate_StopWakesWaiters(t *testing.T) {
	sess := newGateTestSession(t, "sess-stop")
	defer sess.Close()

	g := NewDispatchGate(DispatchGateConfig{Session: sess, MaxInFlight: 1, Scope: newGateScope(t)})
	if err := g.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, _ = sess.CommitQueue().Enqueue(MergeDescriptor{
		PipelineID:    "p",
		BaseVersion:   SemanticVersion{},
		MergedVersion: SemanticVersion{Minor: 1},
	})

	done := make(chan error, 1)
	go func() {
		done <- g.Acquire(context.Background())
	}()
	time.Sleep(50 * time.Millisecond)
	g.Stop()

	select {
	case err := <-done:
		if err != ErrDispatchGateStopped {
			t.Errorf("Acquire err = %v, want ErrDispatchGateStopped", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Acquire did not unblock after Stop")
	}
}
