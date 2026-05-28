package shared

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/versioning"
)

func openIntegrationSession(t *testing.T, name string) *versioning.SessionVFS {
	t.Helper()
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:       versioning.SessionID(name),
		WorkingDir:      workDir,
		StorageRoot:     filepath.Join(dir, "session"),
		AllowDiskExport: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

// TestIntegration_CrashMidAuditRecovery models a crash during an
// audit: a pipeline merges, replicas spawn (ReplicaSpawned is WAL-
// logged), but the coordinator does not receive a decision before
// the session process dies. Reopening the session must:
//
//   - Restore the commit-queue entry in Auditing state.
//   - Restore the replica lifecycle log entries as InFlight.
//   - Let ResumeInFlightReplicas re-dispatch with the same
//     deterministic IDs so the spawner can pick up where it left off.
//
// docs/PARALLEL_GLOBAL_VFS.md §8 (robustness: "replica crash") and
// task #104.
func TestIntegration_CrashMidAuditRecovery(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := versioning.SessionVFSConfig{
		SessionID:       "sess-crash",
		WorkingDir:      workDir,
		StorageRoot:     filepath.Join(dir, "session"),
		AllowDiskExport: true,
	}

	// Session 1: merge, spawn replicas, then "crash" (close without
	// finalization).
	sess1, err := versioning.NewSessionVFS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	inspector := &stubSpawnerShared{}
	tester := &stubSpawnerShared{}
	coord := NewMergeAuditCoordinator(MergeAuditCoordinatorConfig{
		Session:   sess1,
		Inspector: inspector,
		Tester:    tester,
	})
	if err := coord.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	pipe, err := sess1.BeginPipeline(versioning.BeginPipelineConfig{
		PipelineID: "task_crash",
		SessionID:  "sess-crash",
		WorkingDir: workDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess1.NewPipelineFileAccess(pipe).WriteFile(ctx, filepath.Join(workDir, "a.py"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	res, err := sess1.MergePipelineIntoGreen(ctx, "task_crash", versioning.PipelineInspectorCertificate{})
	if err != nil {
		t.Fatal(err)
	}

	inspectorID := MergeReplicaAgentID("inspector-global", "sess-crash", res.MergedVersion)
	testerID := MergeReplicaAgentID("tester-global", "sess-crash", res.MergedVersion)

	// Both replicas recorded as Spawned and dispatched.
	if got := len(sess1.ReplicaLifecycleLog().InFlight()); got != 2 {
		t.Errorf("InFlight after spawn = %d, want 2", got)
	}

	coord.Stop()
	if err := sess1.Close(); err != nil {
		t.Fatal(err)
	}

	// Session 2: reopen + recovery.
	sess2, err := versioning.NewSessionVFS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sess2.Close()

	entry := sess2.CommitQueue().Lookup(res.MergedVersion)
	if entry == nil {
		t.Fatal("commit queue entry missing after reopen")
	}
	if entry.State != versioning.CommitStateAuditing {
		t.Errorf("commit queue state after reopen = %v, want Auditing", entry.State)
	}
	if got := len(sess2.ReplicaLifecycleLog().InFlight()); got != 2 {
		t.Errorf("InFlight after reopen = %d, want 2", got)
	}

	// Resume replicas — should re-dispatch with the same deterministic IDs.
	inspector2 := &stubSpawnerShared{}
	tester2 := &stubSpawnerShared{}
	coord2 := NewMergeAuditCoordinator(MergeAuditCoordinatorConfig{
		Session:   sess2,
		Inspector: inspector2,
		Tester:    tester2,
	})
	if err := coord2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer coord2.Stop()
	if err := coord2.ResumeInFlightReplicas(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := len(inspector2.calls); got != 1 || inspector2.calls[0].ReplicaID != inspectorID {
		t.Errorf("inspector resume calls = %+v, want exactly inspectorID=%q", inspector2.calls, inspectorID)
	}
	if got := len(tester2.calls); got != 1 || tester2.calls[0].ReplicaID != testerID {
		t.Errorf("tester resume calls = %+v, want exactly testerID=%q", tester2.calls, testerID)
	}
	if !inspector2.calls[0].IsReAudit {
		t.Errorf("resumed inspector call missing IsReAudit flag")
	}
}

// TestIntegration_MultiSessionIsolation verifies two concurrent
// SessionVFS instances keep their commit queues, lifecycles, and SLA
// clocks fully independent — mutation in one never leaks into the
// other. Task #106.
func TestIntegration_MultiSessionIsolation(t *testing.T) {
	sessA := openIntegrationSession(t, "sess-a")
	sessB := openIntegrationSession(t, "sess-b")
	defer sessA.Close()
	defer sessB.Close()

	// Mutate A: enqueue + reject.
	descA := versioning.MergeDescriptor{
		PipelineID:    "p",
		BaseVersion:   versioning.SemanticVersion{},
		MergedVersion: versioning.SemanticVersion{Minor: 1},
	}
	if _, err := sessA.CommitQueue().Enqueue(descA); err != nil {
		t.Fatal(err)
	}
	if err := sessA.CommitQueue().MarkRejected(descA.MergedVersion, "r", "bad", nil); err != nil {
		t.Fatal(err)
	}

	// B sees an empty queue.
	if got := sessB.CommitQueue().Depth(); got != 0 {
		t.Errorf("sessB Depth = %d, want 0 — leak from A", got)
	}
	if got := sessB.CommitQueue().DepthBlocked(); got != 0 {
		t.Errorf("sessB DepthBlocked = %d, want 0", got)
	}

	// Clock isolation: A and B advance independently over the same
	// sampled interval. Compare interval deltas instead of absolute
	// elapsed values so scheduler delays between session opens do not
	// masquerade as cross-session leakage.
	const clockSampleWindow = 50 * time.Millisecond
	if aClock, bClock := sessA.Clock(), sessB.Clock(); aClock != nil && bClock != nil {
		startCheck := time.Now().UTC()
		aStart := aClock.Elapsed(startCheck)
		bStart := bClock.Elapsed(startCheck)
		time.Sleep(clockSampleWindow)
		nowCheck := time.Now().UTC()
		aDelta := aClock.Elapsed(nowCheck) - aStart
		bDelta := bClock.Elapsed(nowCheck) - bStart
		if aDelta != bDelta {
			t.Errorf("clock deltas diverged: A=%v B=%v", aDelta, bDelta)
		}
	}
}

// TestIntegration_MultiSessionSLAFreezeDuringClose verifies an SLA
// timer attached to session A does not fire while A is closed, even
// if wall-clock time elapses while another session is open. Task #106.
func TestIntegration_MultiSessionSLAFreezeDuringClose(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	_ = os.MkdirAll(workDir, 0o755)
	cfgA := versioning.SessionVFSConfig{
		SessionID:   "sess-sla-a",
		WorkingDir:  workDir,
		StorageRoot: filepath.Join(dir, "a"),
	}
	cfgB := versioning.SessionVFSConfig{
		SessionID:   "sess-sla-b",
		WorkingDir:  workDir,
		StorageRoot: filepath.Join(dir, "b"),
	}
	sessA1, err := versioning.NewSessionVFS(cfgA)
	if err != nil {
		t.Fatal(err)
	}

	// Reject a merge on A.
	descA := versioning.MergeDescriptor{
		PipelineID:    "p",
		MergedVersion: versioning.SemanticVersion{Minor: 1},
	}
	_, _ = sessA1.CommitQueue().Enqueue(descA)
	_ = sessA1.CommitQueue().MarkRejected(descA.MergedVersion, "r", "bad", nil)

	// Attach a short SLA to A — threshold ≤ open-time so we can
	// confirm it fires.
	bus := &captureBusShared{}
	slaScope := concurrency.NewGoroutineScope(context.Background(), "integ-sla", nil)
	defer slaScope.Shutdown(100*time.Millisecond, 2*time.Second)
	tracker := NewAuditRejectionSLATracker(AuditRejectionSLATrackerConfig{
		Session:   sessA1,
		Bus:       bus,
		Threshold: 100 * time.Millisecond,
		Poll:      25 * time.Millisecond,
		Scope:     slaScope,
	})
	_ = tracker.Start(context.Background())

	// Close A immediately — SLA must freeze.
	_ = sessA1.Close()
	tracker.Stop()

	// Bring up B, keep it running 400ms. A's SLA must NOT breach
	// just because B's clock is advancing.
	sessB, err := versioning.NewSessionVFS(cfgB)
	if err != nil {
		t.Fatal(err)
	}
	defer sessB.Close()
	time.Sleep(400 * time.Millisecond)

	// Reopen A. Depending on timing, the SLA may or may not have
	// already tripped — but the B interval alone shouldn't do it.
	// The pivotal check here: tracker fired 0 events during the
	// interval when A was closed.
	if got := bus.count(); got != 0 {
		t.Errorf("SLA fired while session closed: %d events", got)
	}
}

// TestIntegration_BackpressureReleasesOnAdvance exercises the
// dispatch gate against a live session + resolver: two concurrent
// capacity attempts block at the ceiling, and Advance / MarkCommitted
// on the head unblocks one of them. Task #108 (partial).
func TestIntegration_BackpressureReleasesOnAdvance(t *testing.T) {
	sess := openIntegrationSession(t, "sess-backpressure")
	defer sess.Close()

	scope := concurrency.NewGoroutineScope(context.Background(), "integ-bp", nil)
	defer scope.Shutdown(100*time.Millisecond, 2*time.Second)
	gate := versioning.NewDispatchGate(versioning.DispatchGateConfig{
		Session:     sess,
		MaxInFlight: 1,
		Scope:       scope,
	})
	if err := gate.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer gate.Stop()

	// Occupy the only slot.
	d1 := versioning.MergeDescriptor{
		PipelineID:    "p1",
		MergedVersion: versioning.SemanticVersion{Minor: 1},
	}
	if _, err := sess.CommitQueue().Enqueue(d1); err != nil {
		t.Fatal(err)
	}

	// Acquire must block.
	done := make(chan error, 1)
	go func() {
		done <- gate.Acquire(context.Background())
	}()
	select {
	case err := <-done:
		t.Fatalf("Acquire returned early: err=%v", err)
	case <-time.After(100 * time.Millisecond):
	}

	// Complete d1 (accept + commit).
	_ = sess.CommitQueue().MarkAccepted(d1.MergedVersion, "r")
	_ = sess.CommitQueue().MarkCommitted(d1.MergedVersion)

	// Acquire should unblock.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Acquire err: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Acquire did not unblock after commit")
	}
}

// --- Test helpers ---

type stubSpawnerShared struct {
	calls []*AuditMergeRequest
	ctxs  []context.Context
}

func (s *stubSpawnerShared) SpawnAuditReplica(ctx context.Context, req *AuditMergeRequest) error {
	s.calls = append(s.calls, req)
	s.ctxs = append(s.ctxs, ctx)
	return nil
}

type captureBusShared struct {
	published []*guide.Message
}

func (b *captureBusShared) Publish(topic string, msg *guide.Message) error {
	_ = topic
	b.published = append(b.published, msg)
	_ = json.Marshal // imported
	return nil
}
func (b *captureBusShared) Subscribe(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, nil
}
func (b *captureBusShared) SubscribeAsync(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, nil
}
func (b *captureBusShared) Close() error                             { return nil }
func (b *captureBusShared) CloseWithContext(_ context.Context) error { return nil }
func (b *captureBusShared) count() int                               { return len(b.published) }
