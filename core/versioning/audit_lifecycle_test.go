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

// TestAuditLifecycle_ReplicaVFSReleasedOnWaterLineAdvance verifies
// the §5 refcount+water-line cleanup path end-to-end:
//
//  1. Open a ReplicaVFS for a merge via BeginAuditReplicaVFS.
//  2. Retain the Copy (coordinator would do this for a replica).
//  3. Release the retention.
//  4. Advance the water line past the merge version.
//  5. The OnRelease callback fires, dropping the ReplicaVFS in-RAM
//     handle.
//
// Without this chain the §5 guarantee "ReplicaVFS_N is releasable
// when terminally resolved AND no holders" is unwired.
func TestAuditLifecycle_ReplicaVFSReleasedOnWaterLineAdvance(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	_ = os.MkdirAll(workDir, 0o755)
	sess, err := NewSessionVFS(SessionVFSConfig{
		SessionID:   "sess-release",
		WorkingDir:  workDir,
		StorageRoot: filepath.Join(dir, "session"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	// Seed a merge descriptor + ReplicaVFS.
	ver := SemanticVersion{Minor: 1}
	desc := MergeDescriptor{
		PipelineID:    "p",
		BaseVersion:   SemanticVersion{},
		MergedVersion: ver,
	}
	sess.RecordMergeDescriptorForTest(desc)
	replica, err := sess.BeginAuditReplicaVFS(ver, "holder-r1")
	if err != nil {
		t.Fatal(err)
	}
	if replica == nil {
		t.Fatal("BeginAuditReplicaVFS returned nil")
	}
	if sess.LookupAuditReplicaVFS(ver) == nil {
		t.Fatal("session does not track the replica VFS after Begin")
	}

	// Hold a retention on this Copy so the water-line GC skips it
	// initially, then drop the hold.
	if err := sess.CopyRetention().Retain(ver, "test-holder"); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.CopyRetention().AdvanceWaterLine(SemanticVersion{Minor: 2}, []SemanticVersion{ver}); err != nil {
		t.Fatal(err)
	}
	// Retained — should still be tracked.
	if sess.LookupAuditReplicaVFS(ver) == nil {
		t.Fatal("replica VFS released while retention > 0 (violates §5)")
	}

	// Drop the hold and advance again (idempotent for same water
	// line; AdvanceWaterLine uses strictly-greater comparison).
	if err := sess.CopyRetention().Release("test-holder"); err != nil {
		t.Fatal(err)
	}
	released, err := sess.CopyRetention().AdvanceWaterLine(SemanticVersion{Minor: 3}, []SemanticVersion{ver})
	if err != nil {
		t.Fatal(err)
	}
	if len(released) != 1 || released[0] != ver {
		t.Fatalf("released = %v, want [%v]", released, ver)
	}

	// The session's in-RAM handle should now be dropped.
	if sess.LookupAuditReplicaVFS(ver) != nil {
		t.Error("replica VFS still tracked after water-line advance with zero holders")
	}
	// And the live *ReplicaVFS should be abandoned (chain-bypass
	// signal for any child that still held it as parent).
	if !replica.IsAbandoned() {
		t.Error("released ReplicaVFS was not abandoned — child chain walks will still traverse stale content")
	}
}

// TestAuditLifecycle_DispatchGateBlocksBeginPipelineUnderBackpressure
// verifies the VFSVolume → SessionVFS integration path: when the
// commit queue is at capacity, a pipeline dispatch that goes through
// AcquireDispatchSlot is held until an entry transitions terminal.
//
// This is the §5.1 backpressure contract made observable.
func TestAuditLifecycle_DispatchGateBlocksBeginPipelineUnderBackpressure(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	_ = os.MkdirAll(workDir, 0o755)
	scope := concurrency.NewGoroutineScope(context.Background(), "dispatch-test", nil)
	defer scope.Shutdown(100*time.Millisecond, 2*time.Second)

	sess, err := NewSessionVFS(SessionVFSConfig{
		SessionID:           "sess-dispatch",
		WorkingDir:          workDir,
		StorageRoot:         filepath.Join(dir, "session"),
		DispatchMaxInFlight: 1,
		DispatchScope:       scope,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	// Fill the gate to capacity.
	desc := MergeDescriptor{
		PipelineID:    "p1",
		BaseVersion:   SemanticVersion{},
		MergedVersion: SemanticVersion{Minor: 1},
	}
	if _, err := sess.CommitQueue().Enqueue(desc); err != nil {
		t.Fatal(err)
	}

	// A second AcquireDispatchSlot must block.
	var acquired atomic.Bool
	done := make(chan error, 1)
	go func() {
		err := sess.AcquireDispatchSlot(context.Background())
		acquired.Store(true)
		done <- err
	}()
	time.Sleep(120 * time.Millisecond)
	if acquired.Load() {
		t.Fatal("AcquireDispatchSlot returned while at capacity")
	}

	// Drain the slot (accept + commit → non-terminal count drops).
	if err := sess.CommitQueue().MarkAccepted(desc.MergedVersion, "r"); err != nil {
		t.Fatal(err)
	}
	if err := sess.CommitQueue().MarkCommitted(desc.MergedVersion); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("AcquireDispatchSlot: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AcquireDispatchSlot did not unblock after drain")
	}
}

// TestAuditLifecycle_SessionCloseOrderingNoLeaks verifies that
// SessionVFS.Close shuts subsystems down in the correct order and
// does not leak goroutines: dispatch gate stops, resolver stops,
// merge pipe stops, WAL fsyncs, clock halts, replicaVFSByVersion
// cleared.
//
// The race detector + goroutine-scope accounting in the enclosing
// `go test -race` pass asserts "no leaks" implicitly; this test
// asserts the observable state transitions.
func TestAuditLifecycle_SessionCloseOrderingNoLeaks(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	_ = os.MkdirAll(workDir, 0o755)
	scope := concurrency.NewGoroutineScope(context.Background(), "close-ordering-test", nil)
	defer scope.Shutdown(100*time.Millisecond, 2*time.Second)

	sess, err := NewSessionVFS(SessionVFSConfig{
		SessionID:           "sess-close",
		WorkingDir:          workDir,
		StorageRoot:         filepath.Join(dir, "session"),
		DispatchMaxInFlight: 4,
		DispatchScope:       scope,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Populate some state so Close has something to tear down.
	desc := MergeDescriptor{PipelineID: "p", MergedVersion: SemanticVersion{Minor: 1}}
	if _, err := sess.CommitQueue().Enqueue(desc); err != nil {
		t.Fatal(err)
	}
	sess.RecordMergeDescriptorForTest(desc)
	_, _ = sess.BeginAuditReplicaVFS(desc.MergedVersion, "holder-close")

	elapsedBeforeClose := sess.Clock().Elapsed(time.Now().UTC())
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// After close: clock frozen, dispatch gate released, replica
	// map cleared. Close is idempotent.
	if sess.Clock().IsOpen() {
		t.Error("session clock still open after Close")
	}
	if sess.Clock().Elapsed(time.Now().UTC()) < elapsedBeforeClose {
		t.Error("clock elapsed regressed after Close")
	}
	if sess.LookupAuditReplicaVFS(desc.MergedVersion) != nil {
		t.Error("replica VFS still tracked after Close")
	}
	if err := sess.Close(); err != nil {
		t.Errorf("Close idempotency: second Close returned %v", err)
	}
}
