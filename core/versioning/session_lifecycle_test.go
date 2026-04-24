package versioning

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSessionLifecycle_CleanCloseAndReopenPreservesCommitQueue
// verifies that closing a session and opening a new one against the
// same StorageRoot rehydrates the commit-queue state from the
// ControlWAL. The reopened session sees every enqueued descriptor in
// its last-recorded state. docs/PARALLEL_GLOBAL_VFS.md §8.
func TestSessionLifecycle_CleanCloseAndReopenPreservesCommitQueue(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	storageRoot := filepath.Join(dir, "session")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := SessionVFSConfig{
		SessionID:       "sess-lifecycle",
		WorkingDir:      workDir,
		StorageRoot:     storageRoot,
		AllowDiskExport: true,
	}

	sess1, err := NewSessionVFS(cfg)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}

	// Commit one merge, leave another rejected.
	ctx := context.Background()
	pipeA, err := sess1.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task_a",
		SessionID:  "sess-lifecycle",
		WorkingDir: workDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess1.NewPipelineFileAccess(pipeA).WriteFile(ctx, filepath.Join(workDir, "a.py"), []byte("a")); err != nil {
		t.Fatal(err)
	}
	resA, err := sess1.MergePipelineIntoGreen(ctx, "task_a", PipelineInspectorCertificate{})
	if err != nil {
		t.Fatal(err)
	}

	pipeB, err := sess1.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task_b",
		SessionID:  "sess-lifecycle",
		WorkingDir: workDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess1.NewPipelineFileAccess(pipeB).WriteFile(ctx, filepath.Join(workDir, "b.py"), []byte("b")); err != nil {
		t.Fatal(err)
	}
	resB, err := sess1.MergePipelineIntoGreen(ctx, "task_b", PipelineInspectorCertificate{})
	if err != nil {
		t.Fatal(err)
	}

	// A accepts (then committed via resolver), B rejects.
	if err := sess1.CommitQueue().MarkAccepted(resA.MergedVersion, "r-a"); err != nil {
		t.Fatal(err)
	}
	if err := sess1.CommitQueue().MarkRejected(resB.MergedVersion, "r-b", "bad", []string{"concern-1"}); err != nil {
		t.Fatal(err)
	}
	// Wait a bit for resolver to flush A.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if entry := sess1.CommitQueue().Lookup(resA.MergedVersion); entry != nil && entry.State == CommitStateCommitted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Clean close.
	if err := sess1.Close(); err != nil {
		t.Fatalf("close 1: %v", err)
	}

	// Reopen — must reconstruct state from ControlWAL.
	sess2, err := NewSessionVFS(cfg)
	if err != nil {
		t.Fatalf("open 2: %v", err)
	}
	defer sess2.Close()

	// Rejected entry should still be present.
	bEntry := sess2.CommitQueue().Lookup(resB.MergedVersion)
	if bEntry == nil {
		t.Fatal("rejected entry missing after reopen")
	}
	if bEntry.State != CommitStateRejected {
		t.Errorf("rejected entry state = %v, want Rejected", bEntry.State)
	}
	if bEntry.RejectionReason != "bad" {
		t.Errorf("rejection reason = %q", bEntry.RejectionReason)
	}
	if len(bEntry.BigPictureConcerns) != 1 || bEntry.BigPictureConcerns[0] != "concern-1" {
		t.Errorf("concerns = %v", bEntry.BigPictureConcerns)
	}
}

// TestSessionLifecycle_OverlayWritesSurviveReopen verifies §3.6
// durability: a ReplicaVFS that received writes through the
// overlay-WAL layer reconstructs that content on session reopen.
// This is Stage 5's "full Open replay: commit queue, retention,
// lifecycle, chain, overlays" — the overlay leg specifically.
func TestSessionLifecycle_OverlayWritesSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	storageRoot := filepath.Join(dir, "session")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := SessionVFSConfig{
		SessionID:   "sess-overlay-replay",
		WorkingDir:  workDir,
		StorageRoot: storageRoot,
	}

	// Session 1: create merge descriptor + ReplicaVFS + write overlay.
	sess1, err := NewSessionVFS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ver := SemanticVersion{Minor: 1}
	desc := MergeDescriptor{
		PipelineID:    "p1",
		BaseVersion:   SemanticVersion{},
		MergedVersion: ver,
		Paths:         []string{"audit-authored.md"},
	}
	sess1.RecordMergeDescriptorForTest(desc)
	vfs1, err := sess1.BeginAuditReplicaVFS(ver, "replica-holder")
	if err != nil {
		t.Fatal(err)
	}
	// Inspector + tester each write different content.
	if err := vfs1.Write("inspector-replica", "audit-authored.md", []byte("inspector notes")); err != nil {
		t.Fatal(err)
	}
	if err := vfs1.Write("tester-replica", "tests/audit.spec", []byte("tester-authored test")); err != nil {
		t.Fatal(err)
	}
	if err := vfs1.Delete("inspector-replica", "stale.md"); err != nil {
		t.Fatal(err)
	}

	if err := sess1.Close(); err != nil {
		t.Fatal(err)
	}

	// Session 2: reopen. Overlay state must be reconstructed from
	// the WAL so a session that crashed mid-audit doesn't lose
	// writes / deletes.
	sess2, err := NewSessionVFS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sess2.Close()

	vfs2 := sess2.LookupAuditReplicaVFS(ver)
	if vfs2 == nil {
		t.Fatal("reopen: ReplicaVFS for ver 0.1 not reconstructed from WAL")
	}
	got, err := vfs2.Read("audit-authored.md")
	if err != nil {
		t.Fatalf("reopen read inspector write: %v", err)
	}
	if string(got) != "inspector notes" {
		t.Errorf("inspector write post-reopen = %q, want %q", got, "inspector notes")
	}
	got, err = vfs2.Read("tests/audit.spec")
	if err != nil {
		t.Fatalf("reopen read tester write: %v", err)
	}
	if string(got) != "tester-authored test" {
		t.Errorf("tester write post-reopen = %q, want %q", got, "tester-authored test")
	}
	if _, err := vfs2.Read("stale.md"); err == nil {
		t.Error("stale.md readable post-reopen — delete shadow lost")
	}
}

// TestSessionLifecycle_OverlaySealAndAbandonSurviveReopen confirms
// that Seal + Abandon terminal markers are also replayed.
func TestSessionLifecycle_OverlaySealAndAbandonSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	storageRoot := filepath.Join(dir, "session")
	_ = os.MkdirAll(workDir, 0o755)
	cfg := SessionVFSConfig{
		SessionID:   "sess-overlay-terminal",
		WorkingDir:  workDir,
		StorageRoot: storageRoot,
	}
	sess1, err := NewSessionVFS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sealedVer := SemanticVersion{Minor: 1}
	abandonedVer := SemanticVersion{Minor: 2}
	for _, d := range []MergeDescriptor{
		{PipelineID: "p1", MergedVersion: sealedVer},
		{PipelineID: "p2", MergedVersion: abandonedVer},
	} {
		sess1.RecordMergeDescriptorForTest(d)
	}
	sealed, _ := sess1.BeginAuditReplicaVFS(sealedVer, "holder-seal")
	abandoned, _ := sess1.BeginAuditReplicaVFS(abandonedVer, "holder-abandon")
	_ = sealed.Write("r", "x.go", []byte("seal content"))
	if _, err := sealed.Seal(); err != nil {
		t.Fatal(err)
	}
	if err := abandoned.Abandon(); err != nil {
		t.Fatal(err)
	}
	_ = sess1.Close()

	sess2, err := NewSessionVFS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sess2.Close()

	s := sess2.LookupAuditReplicaVFS(sealedVer)
	if s == nil || !s.IsSealed() {
		t.Errorf("sealed ver: IsSealed=%v (vfs=%v)", s != nil && s.IsSealed(), s)
	}
	a := sess2.LookupAuditReplicaVFS(abandonedVer)
	if a == nil || !a.IsAbandoned() {
		t.Errorf("abandoned ver: IsAbandoned=%v (vfs=%v)", a != nil && a.IsAbandoned(), a)
	}
}

// TestSessionLifecycle_ClockSurvivesReopen verifies the session
// clock's accumulated open-time is preserved across Close/Open
// boundaries — critical for SLA timers that must measure in
// open-only wall time (§8).
func TestSessionLifecycle_ClockSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	storageRoot := filepath.Join(dir, "session")
	_ = os.MkdirAll(workDir, 0o755)

	cfg := SessionVFSConfig{
		SessionID:   "sess-clock-survival",
		WorkingDir:  workDir,
		StorageRoot: storageRoot,
	}

	sess1, err := NewSessionVFS(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Accumulate some open time.
	time.Sleep(100 * time.Millisecond)
	firstElapsed := sess1.Clock().Elapsed(time.Now().UTC())
	if err := sess1.Close(); err != nil {
		t.Fatal(err)
	}

	// Long closed interval — should NOT count toward open clock.
	time.Sleep(300 * time.Millisecond)

	sess2, err := NewSessionVFS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sess2.Close()

	time.Sleep(50 * time.Millisecond)
	second := sess2.Clock().Elapsed(time.Now().UTC())

	// Reopened clock should include firstElapsed + ~50ms, NOT the
	// 300ms closed interval.
	if second <= firstElapsed {
		t.Errorf("reopened clock = %v, want > first (%v)", second, firstElapsed)
	}
	if second > firstElapsed+250*time.Millisecond {
		t.Errorf("reopened clock = %v, too large — closed-interval leak?", second)
	}
}
