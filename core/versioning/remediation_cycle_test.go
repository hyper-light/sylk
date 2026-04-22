package versioning

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRemediationCycle_RejectRemediateSupersedeCommit walks the
// full §3.8 rejection recovery flow directly on the session VFS:
//
//  1. Pipeline A merges, the coordinator-equivalent test marks it
//     Rejected.
//  2. A fix pipeline A' dispatches with BaseCopyVersion pinned to
//     A's MergedVersion. When A' merges, the descriptor's
//     SupersedesVersion records that pinning.
//  3. Marking A' Accepted + MarkSuperseded(A, A') transitions A's
//     slot to Superseded.
//  4. The resolver flushes A' in A's slot, advances, and the water
//     line passes both.
//
// This is the "end-to-end reject → remediate → supersede → accept"
// cycle the audit coordinator + architect remediation path
// produces in production — exercised here at the VFS layer so the
// version/queue/resolver plumbing is tested independently of LLM
// replicas.
func TestRemediationCycle_RejectRemediateSupersedeCommit(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sess, err := NewSessionVFS(SessionVFSConfig{
		SessionID:       "sess-remediation",
		WorkingDir:      workDir,
		AllowDiskExport: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	ctx := context.Background()

	// Pipeline A: a first merge that will be rejected.
	pipeA, err := sess.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task_a",
		SessionID:  "sess-remediation",
		WorkingDir: workDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.NewPipelineFileAccess(pipeA).WriteFile(ctx, filepath.Join(workDir, "shared.py"), []byte("buggy")); err != nil {
		t.Fatal(err)
	}
	resA, err := sess.MergePipelineIntoGreen(ctx, "task_a", PipelineInspectorCertificate{})
	if err != nil {
		t.Fatalf("MergePipelineIntoGreen A: %v", err)
	}

	// Audit rejects A.
	if err := sess.CommitQueue().MarkRejected(resA.MergedVersion, "r-a", "buggy impl", []string{"cycle"}); err != nil {
		t.Fatal(err)
	}
	aEntry := sess.CommitQueue().Lookup(resA.MergedVersion)
	if aEntry == nil || aEntry.State != CommitStateRejected {
		t.Fatalf("A after reject: state = %v, want Rejected", aEntry)
	}

	// Pipeline A' (remediation): dispatched pinned to A's Copy.
	pipeFix, err := sess.BeginPipeline(BeginPipelineConfig{
		PipelineID:      "task_a_fix",
		SessionID:       "sess-remediation",
		WorkingDir:      workDir,
		BaseCopyVersion: resA.MergedVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.NewPipelineFileAccess(pipeFix).WriteFile(ctx, filepath.Join(workDir, "shared.py"), []byte("fixed")); err != nil {
		t.Fatal(err)
	}
	resFix, err := sess.MergePipelineIntoGreen(ctx, "task_a_fix", PipelineInspectorCertificate{
		DeclaredScope: "fix shared.py",
		Summary:       "fixed interface",
	})
	if err != nil {
		t.Fatalf("MergePipelineIntoGreen fix: %v", err)
	}

	// The merge descriptor should carry SupersedesVersion = A's
	// MergedVersion because A is Rejected and the fix was pinned to
	// A's Copy.
	fixDesc, ok := sess.FindMergeDescriptor(resFix.MergedVersion)
	if !ok {
		t.Fatal("fix descriptor missing")
	}
	if fixDesc.SupersedesVersion != resA.MergedVersion {
		t.Errorf("fix.SupersedesVersion = %v, want %v", fixDesc.SupersedesVersion, resA.MergedVersion)
	}

	// Accept the fix + mark A superseded (this is the coordinator's
	// job in production; here we drive it directly).
	if err := sess.CommitQueue().MarkAccepted(resFix.MergedVersion, "r-fix"); err != nil {
		t.Fatal(err)
	}
	if err := sess.CommitQueue().MarkSuperseded(resA.MergedVersion, resFix.MergedVersion); err != nil {
		t.Fatal(err)
	}

	// The resolver should flush A' in A's slot and pop both.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		aState := stateOf(sess.CommitQueue().Lookup(resA.MergedVersion))
		fixState := stateOf(sess.CommitQueue().Lookup(resFix.MergedVersion))
		if isAdvanced(aState) && isAdvanced(fixState) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	aState := stateOf(sess.CommitQueue().Lookup(resA.MergedVersion))
	fixState := stateOf(sess.CommitQueue().Lookup(resFix.MergedVersion))
	if !isAdvanced(aState) {
		t.Errorf("A never advanced: state = %q", aState)
	}
	if !isAdvanced(fixState) {
		t.Errorf("fix never advanced: state = %q", fixState)
	}
}

func stateOf(entry *CommitEntry) string {
	if entry == nil {
		return "missing"
	}
	return string(entry.State)
}

// isAdvanced reports whether the entry has reached a post-resolver
// terminal state (Committed, Superseded, Abandoned) OR has been
// removed from the queue (missing).
func isAdvanced(state string) bool {
	switch state {
	case string(CommitStateCommitted),
		string(CommitStateSuperseded),
		string(CommitStateAbandoned),
		"missing":
		return true
	}
	return false
}
