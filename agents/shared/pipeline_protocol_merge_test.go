package shared

import (
	"strings"
	"testing"
)

// TestMergePipelineSnapshots_BothNilReturnsNil exercises the degenerate
// base case: no checkpoint on disk, no task.Context snapshot. The merge
// has nothing to return.
func TestMergePipelineSnapshots_BothNilReturnsNil(t *testing.T) {
	t.Parallel()
	if got := mergePipelineSnapshots(nil, nil, nil); got != nil {
		t.Fatalf("mergePipelineSnapshots(nil, nil, nil) = %#v, want nil", got)
	}
}

// TestMergePipelineSnapshots_NilBaseReturnsCheckpoint: no dispatcher
// context means the checkpoint is the only source. Return it verbatim.
func TestMergePipelineSnapshots_NilBaseReturnsCheckpoint(t *testing.T) {
	t.Parallel()
	checkpoint := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{ID: "c-1", RequestingAgent: "inspector-pipeline"},
		CurrentRequest:   "checkpoint request",
	}
	got := mergePipelineSnapshots(checkpoint, nil, nil)
	if got == nil || got.PendingChallenge == nil {
		t.Fatalf("expected checkpoint PendingChallenge preserved; got=%#v", got)
	}
	if got.PendingChallenge.ID != "c-1" {
		t.Fatalf("PendingChallenge.ID = %q, want c-1", got.PendingChallenge.ID)
	}
	if got.CurrentRequest != "checkpoint request" {
		t.Fatalf("CurrentRequest = %q, want unchanged", got.CurrentRequest)
	}
}

// TestMergePipelineSnapshots_NilCheckpointReturnsBase: first-ever open
// for this scope. No checkpoint yet. The base snapshot is the only
// source.
func TestMergePipelineSnapshots_NilCheckpointReturnsBase(t *testing.T) {
	t.Parallel()
	base := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{ID: "c-2", RequestingAgent: "inspector-pipeline"},
	}
	got := mergePipelineSnapshots(nil, base, nil)
	if got == nil || got.PendingChallenge == nil {
		t.Fatalf("expected base PendingChallenge preserved; got=%#v", got)
	}
	if got.PendingChallenge.ID != "c-2" {
		t.Fatalf("PendingChallenge.ID = %q, want c-2", got.PendingChallenge.ID)
	}
}

// TestMergePipelineSnapshots_BaseRescuesPendingChallenge is the exact
// live bug reproducer: the checkpoint on disk has no PendingChallenge
// (because the write-path silently dropped the event in the old
// dedupe-on-correlation bug), but the dispatcher's task.Context
// baseSnapshot carries the fresh challenge. The merge must rescue
// it; the WAL rescue is the belt-and-suspenders defense that would
// have kept the tester from wedging even with Fix A broken.
//
// If this assertion ever regresses, the state-loss failure reproduces
// exactly as observed in session `.sylk/sessions/default/` with the
// missing challenge `task_1-challenge-169a56d1`.
func TestMergePipelineSnapshots_BaseRescuesPendingChallenge(t *testing.T) {
	t.Parallel()
	checkpoint := &PipelineProtocolSnapshot{
		Roster: []PipelineProtocolAgent{
			{AgentType: PipelineAgentInspector},
			{AgentType: PipelineAgentTester},
		},
		// Deliberately no PendingChallenge — the write-path lost it.
	}
	base := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{
			ID:              "task_1-challenge-169a56d1",
			RequestingAgent: "inspector-pipeline",
			TargetAgents:    []string{"tester-pipeline"},
			Request:         "Remove line 1 (`package main`) from hello-cli/tests/test_cli.py.",
		},
	}
	got := mergePipelineSnapshots(checkpoint, base, nil)
	if got == nil || got.PendingChallenge == nil {
		t.Fatalf("merge must rescue PendingChallenge from base; got=%#v", got)
	}
	if got.PendingChallenge.ID != "task_1-challenge-169a56d1" {
		t.Fatalf("merge rescued wrong challenge %q, want 169a56d1", got.PendingChallenge.ID)
	}
	if got.PendingChallenge.RequestingAgent != "inspector-pipeline" {
		t.Fatalf("challenge requester = %q, want inspector-pipeline", got.PendingChallenge.RequestingAgent)
	}
}

// TestMergePipelineSnapshots_ProcessedResolutionTrumpStaleBasePending
// locks the WAL-authority invariant: even when base has a
// PendingChallenge, if the checkpoint's processed list shows that
// challenge already resolved, the merged snapshot drops the pending
// entry. A stale in-flight snapshot must not resurrect a resolved
// challenge.
func TestMergePipelineSnapshots_ProcessedResolutionTrumpStaleBasePending(t *testing.T) {
	t.Parallel()
	checkpoint := &PipelineProtocolSnapshot{}
	base := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{
			ID:              "c-resolved",
			RequestingAgent: "inspector-pipeline",
		},
	}
	processed := []PipelineValidationProcessing{
		{ChallengeID: "c-resolved", Decision: PipelineValidationDecisionAccept},
	}
	got := mergePipelineSnapshots(checkpoint, base, processed)
	if got == nil {
		t.Fatal("expected merged snapshot, got nil")
	}
	if got.PendingChallenge != nil {
		t.Fatalf("processed resolution must drop stale base PendingChallenge; got %#v", got.PendingChallenge)
	}
}

// TestMergePipelineSnapshots_CheckpointPreservesHistoricalFields verifies
// the authority split: historical fields (Roster, RecentEvents) stay
// from the checkpoint, not overwritten by base. The checkpoint is the
// authoritative record of what happened; base is only authoritative
// for in-flight state.
func TestMergePipelineSnapshots_CheckpointPreservesHistoricalFields(t *testing.T) {
	t.Parallel()
	checkpoint := &PipelineProtocolSnapshot{
		Roster: []PipelineProtocolAgent{
			{AgentType: PipelineAgentInspector},
			{AgentType: PipelineAgentEngineer},
			{AgentType: PipelineAgentTester},
		},
	}
	base := &PipelineProtocolSnapshot{
		// Empty roster on base — dispatcher's view is in-flight only.
		CurrentRequest: "fresh request",
	}
	got := mergePipelineSnapshots(checkpoint, base, nil)
	if got == nil {
		t.Fatal("expected merged snapshot")
	}
	if len(got.Roster) != 3 {
		t.Fatalf("roster count = %d, want 3 (preserved from checkpoint)", len(got.Roster))
	}
	if got.CurrentRequest != "fresh request" {
		t.Fatalf("CurrentRequest = %q, want fresh value from base", got.CurrentRequest)
	}
}

// TestMergePipelineSnapshots_BaseWinsOnInFlightFields verifies the
// freshness-authoritative class: AuditLock, ActiveAgents, RequestedBy,
// Mode all come from base when base has them populated.
func TestMergePipelineSnapshots_BaseWinsOnInFlightFields(t *testing.T) {
	t.Parallel()
	checkpoint := &PipelineProtocolSnapshot{
		ActiveAgents:   []string{"old-agent"},
		RequestedBy:    "old-requester",
		Mode:           "old-mode",
		CurrentRequest: "old-request",
		AuditLock:      &PipelineAuditLock{Phase: "old-phase"},
	}
	base := &PipelineProtocolSnapshot{
		ActiveAgents:   []string{"new-agent-1", "new-agent-2"},
		RequestedBy:    "new-requester",
		Mode:           "new-mode",
		CurrentRequest: "new-request",
		AuditLock:      &PipelineAuditLock{Phase: "new-phase"},
	}
	got := mergePipelineSnapshots(checkpoint, base, nil)
	if got == nil {
		t.Fatal("expected merged snapshot")
	}
	if len(got.ActiveAgents) != 2 || got.ActiveAgents[0] != "new-agent-1" {
		t.Fatalf("ActiveAgents = %v, want new-agent-*", got.ActiveAgents)
	}
	if got.RequestedBy != "new-requester" {
		t.Fatalf("RequestedBy = %q, want new-requester", got.RequestedBy)
	}
	if got.Mode != "new-mode" {
		t.Fatalf("Mode = %q, want new-mode", got.Mode)
	}
	if got.CurrentRequest != "new-request" {
		t.Fatalf("CurrentRequest = %q, want new-request", got.CurrentRequest)
	}
	if got.AuditLock == nil || got.AuditLock.Phase != "new-phase" {
		t.Fatalf("AuditLock = %+v, want phase=new-phase", got.AuditLock)
	}
}

// TestMergePipelineSnapshots_BaseEmptyInFlightKeepsCheckpointValues:
// when base's in-flight fields are empty, the checkpoint's values stay.
// This prevents a dispatcher that didn't populate the snapshot from
// clobbering real state.
func TestMergePipelineSnapshots_BaseEmptyInFlightKeepsCheckpointValues(t *testing.T) {
	t.Parallel()
	checkpoint := &PipelineProtocolSnapshot{
		ActiveAgents: []string{"kept-agent"},
		RequestedBy:  "kept-requester",
		Mode:         "kept-mode",
	}
	base := &PipelineProtocolSnapshot{
		// Everything empty except one field just to prove the merge
		// happened.
		PendingChallenge: &PipelineProtocolChallenge{ID: "c-x", RequestingAgent: "inspector-pipeline"},
	}
	got := mergePipelineSnapshots(checkpoint, base, nil)
	if got == nil {
		t.Fatal("expected merged snapshot")
	}
	if len(got.ActiveAgents) != 1 || got.ActiveAgents[0] != "kept-agent" {
		t.Fatalf("ActiveAgents = %v, want [kept-agent]", got.ActiveAgents)
	}
	if got.RequestedBy != "kept-requester" {
		t.Fatalf("RequestedBy = %q, want kept-requester", got.RequestedBy)
	}
	if got.Mode != "kept-mode" {
		t.Fatalf("Mode = %q, want kept-mode", got.Mode)
	}
	if got.PendingChallenge == nil || got.PendingChallenge.ID != "c-x" {
		t.Fatal("expected base's PendingChallenge preserved")
	}
}

// TestMergePipelineSnapshots_CheckpointChallengeSurvivesWhenBaseAgrees
// exercises the agreement path: both sources report the same challenge.
// No divergence, no drop, base overwrite is a no-op.
func TestMergePipelineSnapshots_CheckpointChallengeSurvivesWhenBaseAgrees(t *testing.T) {
	t.Parallel()
	challenge := &PipelineProtocolChallenge{ID: "c-agree", RequestingAgent: "inspector-pipeline"}
	checkpoint := &PipelineProtocolSnapshot{PendingChallenge: challenge}
	base := &PipelineProtocolSnapshot{PendingChallenge: challenge}
	got := mergePipelineSnapshots(checkpoint, base, nil)
	if got == nil || got.PendingChallenge == nil || got.PendingChallenge.ID != "c-agree" {
		t.Fatalf("expected agreed challenge preserved; got=%#v", got)
	}
}

// TestMergePipelineSnapshots_DivergingChallengeTakesBase: both sources
// report a PendingChallenge but with different IDs. Base is the newer
// source (dispatcher wrote it post-checkpoint), so base wins.
func TestMergePipelineSnapshots_DivergingChallengeTakesBase(t *testing.T) {
	t.Parallel()
	checkpoint := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{ID: "c-old", RequestingAgent: "inspector-pipeline"},
	}
	base := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{ID: "c-new", RequestingAgent: "inspector-pipeline"},
	}
	got := mergePipelineSnapshots(checkpoint, base, nil)
	if got == nil || got.PendingChallenge == nil {
		t.Fatal("expected merged PendingChallenge")
	}
	if got.PendingChallenge.ID != "c-new" {
		t.Fatalf("divergent PendingChallenge: base should win; got %q, want c-new", got.PendingChallenge.ID)
	}
}

// TestMergePipelineSnapshots_PendingValidationRescuesFromBase mirrors the
// PendingChallenge rescue: when the checkpoint lacks the validation
// record but the base snapshot has one (fresh on the wire), the merge
// carries it forward.
func TestMergePipelineSnapshots_PendingValidationRescuesFromBase(t *testing.T) {
	t.Parallel()
	checkpoint := &PipelineProtocolSnapshot{}
	base := &PipelineProtocolSnapshot{
		PendingValidation: &PipelineValidationRecord{
			ChallengeID:     "c-42",
			RespondingAgent: "tester-pipeline",
			Status:          "passed",
		},
	}
	got := mergePipelineSnapshots(checkpoint, base, nil)
	if got == nil || got.PendingValidation == nil {
		t.Fatalf("PendingValidation must be rescued from base; got=%#v", got)
	}
	if got.PendingValidation.ChallengeID != "c-42" {
		t.Fatalf("PendingValidation.ChallengeID = %q, want c-42", got.PendingValidation.ChallengeID)
	}
}

// TestMergePipelineSnapshots_ProcessedResolutionAppliesToNilCheckpoint
// covers the nil-checkpoint-with-processed edge case: even when the
// checkpoint is nil (first open), if the caller passed processed
// resolutions (from an earlier replay), stale base pending entries
// are dropped.
func TestMergePipelineSnapshots_ProcessedResolutionAppliesToNilCheckpoint(t *testing.T) {
	t.Parallel()
	base := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{ID: "c-resolved", RequestingAgent: "inspector-pipeline"},
	}
	processed := []PipelineValidationProcessing{
		{ChallengeID: "c-resolved", Decision: PipelineValidationDecisionAccept},
	}
	got := mergePipelineSnapshots(nil, base, processed)
	if got == nil {
		t.Fatal("expected merged snapshot")
	}
	if got.PendingChallenge != nil {
		t.Fatalf("processed resolution must drop base pending even with nil checkpoint; got %#v", got.PendingChallenge)
	}
}

// TestPipelineChallengeEqual_MatchesOnIDAndRequester verifies the
// equality helper: same ID + same normalized requester → equal.
// Different ID or different requester → not equal.
func TestPipelineChallengeEqual_MatchesOnIDAndRequester(t *testing.T) {
	t.Parallel()
	a := &PipelineProtocolChallenge{ID: " c-1 ", RequestingAgent: "inspector-pipeline"}
	b := &PipelineProtocolChallenge{ID: "c-1", RequestingAgent: "inspector-pipeline"}
	if !pipelineChallengeEqual(a, b) {
		t.Fatalf("identical challenges (after trim) must compare equal")
	}
	c := &PipelineProtocolChallenge{ID: "c-1", RequestingAgent: "other-agent"}
	if pipelineChallengeEqual(a, c) {
		t.Fatal("different requester must compare unequal")
	}
	d := &PipelineProtocolChallenge{ID: "c-2", RequestingAgent: "inspector-pipeline"}
	if pipelineChallengeEqual(a, d) {
		t.Fatal("different ID must compare unequal")
	}
}

// TestPipelineValidationEqual_MatchesOnIdentityFields verifies the
// validation equality helper.
func TestPipelineValidationEqual_MatchesOnIdentityFields(t *testing.T) {
	t.Parallel()
	a := &PipelineValidationRecord{ChallengeID: "c-1", RespondingAgent: "tester-pipeline", Status: "passed"}
	b := &PipelineValidationRecord{ChallengeID: "c-1", RespondingAgent: "tester-pipeline", Status: "passed"}
	if !pipelineValidationEqual(a, b) {
		t.Fatal("identical validations must compare equal")
	}
	if pipelineValidationEqual(a, &PipelineValidationRecord{ChallengeID: "c-2", RespondingAgent: "tester-pipeline", Status: "passed"}) {
		t.Fatal("different ChallengeID must compare unequal")
	}
	if pipelineValidationEqual(a, &PipelineValidationRecord{ChallengeID: "c-1", RespondingAgent: "other", Status: "passed"}) {
		t.Fatal("different responding agent must compare unequal")
	}
	if pipelineValidationEqual(a, &PipelineValidationRecord{ChallengeID: "c-1", RespondingAgent: "tester-pipeline", Status: "failed"}) {
		t.Fatal("different status must compare unequal")
	}
}

// TestApplyProcessedResolutionsToMerged_DropsPendingForResolvedIDs
// exercises the post-merge cleanup: any remaining PendingChallenge or
// PendingValidation whose ChallengeID appears in processed is dropped.
func TestApplyProcessedResolutionsToMerged_DropsPendingForResolvedIDs(t *testing.T) {
	t.Parallel()
	merged := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{ID: "c-a", RequestingAgent: "inspector-pipeline"},
		PendingValidation: &PipelineValidationRecord{
			ChallengeID:     "c-b",
			RespondingAgent: "tester-pipeline",
			Status:          "passed",
		},
	}
	processed := []PipelineValidationProcessing{
		{ChallengeID: "c-a"},
	}
	applyProcessedResolutionsToMerged(merged, processed)
	if merged.PendingChallenge != nil {
		t.Fatal("resolved challenge must be dropped from PendingChallenge")
	}
	if merged.PendingValidation == nil {
		t.Fatal("unresolved validation must remain")
	}
}

// TestApplyProcessedResolutionsToMerged_PreservesWhenIDMismatch: if
// processed contains resolutions for other challenges, pending entries
// for different IDs stay.
func TestApplyProcessedResolutionsToMerged_PreservesWhenIDMismatch(t *testing.T) {
	t.Parallel()
	merged := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{ID: "c-current", RequestingAgent: "inspector-pipeline"},
	}
	processed := []PipelineValidationProcessing{
		{ChallengeID: "c-old"},
	}
	applyProcessedResolutionsToMerged(merged, processed)
	if merged.PendingChallenge == nil || merged.PendingChallenge.ID != "c-current" {
		t.Fatal("pending challenge with unresolved ID must be preserved")
	}
}

// TestApplyProcessedResolutionsToMerged_NilSnapshotIsNoOp: safety for
// the pre-merge-nil case that the public merge function passes
// through.
func TestApplyProcessedResolutionsToMerged_NilSnapshotIsNoOp(t *testing.T) {
	t.Parallel()
	// Must not panic.
	applyProcessedResolutionsToMerged(nil, []PipelineValidationProcessing{{ChallengeID: "x"}})
}

// TestMergePipelineSnapshots_PendingChallengeIDReturnsBlankOnNil
// exercises the nil-safety guard on the logging helper.
func TestMergePipelineSnapshots_PendingChallengeIDReturnsBlankOnNil(t *testing.T) {
	t.Parallel()
	if got := pendingChallengeID(nil); got != "" {
		t.Fatalf("pendingChallengeID(nil) = %q, want blank", got)
	}
	empty := &PipelineProtocolSnapshot{}
	if got := pendingChallengeID(empty); got != "" {
		t.Fatalf("pendingChallengeID(empty) = %q, want blank", got)
	}
	withChallenge := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{ID: "  c-trimmed  "},
	}
	if got := pendingChallengeID(withChallenge); got != "c-trimmed" {
		t.Fatalf("pendingChallengeID trim/extract = %q, want c-trimmed", got)
	}
}

// TestMergePipelineSnapshots_Deterministic: running the merge twice on
// equivalent inputs produces structurally-identical output. Important
// for deterministic replay and snapshot comparison.
func TestMergePipelineSnapshots_Deterministic(t *testing.T) {
	t.Parallel()
	checkpoint := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{ID: "c-old"},
		Roster:           []PipelineProtocolAgent{{AgentType: PipelineAgentInspector}},
	}
	base := &PipelineProtocolSnapshot{
		PendingChallenge: &PipelineProtocolChallenge{ID: "c-new", RequestingAgent: "inspector-pipeline"},
		CurrentRequest:   "fresh",
	}
	a := mergePipelineSnapshots(checkpoint, base, nil)
	b := mergePipelineSnapshots(checkpoint, base, nil)
	if a == nil || b == nil {
		t.Fatal("expected both merges to return non-nil")
	}
	if a.PendingChallenge.ID != b.PendingChallenge.ID {
		t.Fatalf("merge non-deterministic: a=%q b=%q", a.PendingChallenge.ID, b.PendingChallenge.ID)
	}
	if a.CurrentRequest != b.CurrentRequest {
		t.Fatalf("merge non-deterministic on CurrentRequest")
	}
	if !strings.EqualFold(a.PendingChallenge.RequestingAgent, b.PendingChallenge.RequestingAgent) {
		t.Fatalf("merge non-deterministic on requester")
	}
}
