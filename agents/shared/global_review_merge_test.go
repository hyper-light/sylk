package shared

import (
	"testing"
)

// TestMergeGlobalReviewSnapshots_BothNilReturnsNil is the trivial base
// case: merging two nils must not panic and must produce nil.
func TestMergeGlobalReviewSnapshots_BothNilReturnsNil(t *testing.T) {
	t.Parallel()
	got := mergeGlobalReviewSnapshots(nil, nil, nil)
	if got != nil {
		t.Fatalf("mergeGlobalReviewSnapshots(nil,nil,nil) = %#v, want nil", got)
	}
}

// TestMergeGlobalReviewSnapshots_NilBaseReturnsCheckpoint verifies that a
// nil base (no in-flight metadata) preserves the checkpoint verbatim.
func TestMergeGlobalReviewSnapshots_NilBaseReturnsCheckpoint(t *testing.T) {
	t.Parallel()
	checkpoint := &GlobalReviewSnapshot{ReviewID: "review-1"}
	got := mergeGlobalReviewSnapshots(checkpoint, nil, nil)
	if got == nil {
		t.Fatal("expected checkpoint, got nil")
	}
	if got.ReviewID != "review-1" {
		t.Fatalf("ReviewID = %q, want review-1", got.ReviewID)
	}
}

// TestMergeGlobalReviewSnapshots_NilCheckpointReturnsBase is the first-
// open case: no checkpoint on disk yet, base is the only source.
func TestMergeGlobalReviewSnapshots_NilCheckpointReturnsBase(t *testing.T) {
	t.Parallel()
	base := &GlobalReviewSnapshot{ReviewID: "review-2"}
	got := mergeGlobalReviewSnapshots(nil, base, nil)
	if got == nil {
		t.Fatal("expected base, got nil")
	}
	if got.ReviewID != "review-2" {
		t.Fatalf("ReviewID = %q, want review-2", got.ReviewID)
	}
}

// TestMergeGlobalReviewSnapshots_BaseRescuesPendingChallenge is the
// direct analog of the pipeline live-bug regression: the checkpoint
// lacks the challenge that the dispatcher just committed; base must
// win.
func TestMergeGlobalReviewSnapshots_BaseRescuesPendingChallenge(t *testing.T) {
	t.Parallel()
	checkpoint := &GlobalReviewSnapshot{ReviewID: "review-1"}
	base := &GlobalReviewSnapshot{
		ReviewID: "review-1",
		PendingChallenge: &GlobalReviewChallenge{
			ID:              "g-challenge-1",
			RequestingAgent: GlobalReviewAgentInspector,
			TargetAgent:     GlobalReviewAgentTester,
			Request:         "validate the commit audit",
		},
	}
	got := mergeGlobalReviewSnapshots(checkpoint, base, nil)
	if got == nil || got.PendingChallenge == nil {
		t.Fatalf("PendingChallenge must be rescued from base; got=%#v", got)
	}
	if got.PendingChallenge.ID != "g-challenge-1" {
		t.Fatalf("PendingChallenge.ID = %q, want g-challenge-1", got.PendingChallenge.ID)
	}
}

// TestMergeGlobalReviewSnapshots_ProcessedResolutionTrumpStaleBase verifies
// the WAL-resolution-authoritative rule: even if base carries a stale
// pending entry, a processed resolution clears it.
func TestMergeGlobalReviewSnapshots_ProcessedResolutionTrumpStaleBase(t *testing.T) {
	t.Parallel()
	checkpoint := &GlobalReviewSnapshot{}
	base := &GlobalReviewSnapshot{
		PendingChallenge: &GlobalReviewChallenge{
			ID:              "g-resolved",
			RequestingAgent: GlobalReviewAgentInspector,
		},
	}
	processed := []GlobalReviewValidationProcessing{
		{ChallengeID: "g-resolved", AgentType: GlobalReviewAgentInspector},
	}
	got := mergeGlobalReviewSnapshots(checkpoint, base, processed)
	if got == nil {
		t.Fatal("expected merged snapshot")
	}
	if got.PendingChallenge != nil {
		t.Fatalf("PendingChallenge should be cleared by processed entry; got=%#v", got.PendingChallenge)
	}
}

// TestMergeGlobalReviewSnapshots_CheckpointPreservesHistoricalFields
// verifies the historical-authoritative class: ReviewID and RecentEvents
// come from the checkpoint, not the base.
func TestMergeGlobalReviewSnapshots_CheckpointPreservesHistoricalFields(t *testing.T) {
	t.Parallel()
	checkpoint := &GlobalReviewSnapshot{
		ReviewID: "review-historical",
		RecentEvents: []GlobalReviewEvent{
			{Type: "challenge", AgentType: GlobalReviewAgentInspector, Summary: "event-1"},
		},
	}
	base := &GlobalReviewSnapshot{
		PendingChallenge: &GlobalReviewChallenge{ID: "g-1", RequestingAgent: GlobalReviewAgentInspector},
	}
	got := mergeGlobalReviewSnapshots(checkpoint, base, nil)
	if got == nil {
		t.Fatal("expected merged snapshot")
	}
	if got.ReviewID != "review-historical" {
		t.Fatalf("ReviewID = %q, want review-historical", got.ReviewID)
	}
	if len(got.RecentEvents) != 1 {
		t.Fatalf("RecentEvents = %v, want single event", got.RecentEvents)
	}
	if got.PendingChallenge == nil || got.PendingChallenge.ID != "g-1" {
		t.Fatalf("PendingChallenge not rescued from base; got=%#v", got.PendingChallenge)
	}
}

// TestMergeGlobalReviewSnapshots_BaseWinsOnInFlightFields verifies the
// freshness-authoritative class: AuditLock, ActiveAgents, RequestedBy,
// CurrentRequest come from base when present.
func TestMergeGlobalReviewSnapshots_BaseWinsOnInFlightFields(t *testing.T) {
	t.Parallel()
	checkpoint := &GlobalReviewSnapshot{
		ActiveAgents:   []string{"old-agent"},
		RequestedBy:    "old-requester",
		CurrentRequest: "old-request",
		AuditLock:      &GlobalReviewAuditLock{Phase: "old-phase"},
	}
	base := &GlobalReviewSnapshot{
		ActiveAgents:   []string{"new-agent-1", "new-agent-2"},
		RequestedBy:    "new-requester",
		CurrentRequest: "new-request",
		AuditLock:      &GlobalReviewAuditLock{Phase: "new-phase"},
	}
	got := mergeGlobalReviewSnapshots(checkpoint, base, nil)
	if got == nil {
		t.Fatal("expected merged snapshot")
	}
	if len(got.ActiveAgents) != 2 || got.ActiveAgents[0] != "new-agent-1" {
		t.Fatalf("ActiveAgents = %v, want new-agent-*", got.ActiveAgents)
	}
	if got.RequestedBy != "new-requester" {
		t.Fatalf("RequestedBy = %q, want new-requester", got.RequestedBy)
	}
	if got.CurrentRequest != "new-request" {
		t.Fatalf("CurrentRequest = %q, want new-request", got.CurrentRequest)
	}
	if got.AuditLock == nil || got.AuditLock.Phase != "new-phase" {
		t.Fatalf("AuditLock = %+v, want phase=new-phase", got.AuditLock)
	}
}

// TestMergeGlobalReviewSnapshots_BaseEmptyInFlightKeepsCheckpointValues
// verifies that empty base in-flight fields do NOT clobber populated
// checkpoint values — a non-empty checkpoint with an empty base should
// preserve the checkpoint's in-flight data.
func TestMergeGlobalReviewSnapshots_BaseEmptyInFlightKeepsCheckpointValues(t *testing.T) {
	t.Parallel()
	checkpoint := &GlobalReviewSnapshot{
		ActiveAgents:   []string{"kept-agent"},
		RequestedBy:    "kept-requester",
		CurrentRequest: "kept-request",
		AuditLock:      &GlobalReviewAuditLock{Phase: "kept-phase"},
	}
	base := &GlobalReviewSnapshot{
		PendingChallenge: &GlobalReviewChallenge{ID: "g-x", RequestingAgent: GlobalReviewAgentInspector},
	}
	got := mergeGlobalReviewSnapshots(checkpoint, base, nil)
	if got == nil {
		t.Fatal("expected merged snapshot")
	}
	if len(got.ActiveAgents) != 1 || got.ActiveAgents[0] != "kept-agent" {
		t.Fatalf("ActiveAgents = %v, want kept-agent", got.ActiveAgents)
	}
	if got.RequestedBy != "kept-requester" {
		t.Fatalf("RequestedBy = %q, want kept-requester", got.RequestedBy)
	}
	if got.CurrentRequest != "kept-request" {
		t.Fatalf("CurrentRequest = %q, want kept-request", got.CurrentRequest)
	}
	if got.AuditLock == nil || got.AuditLock.Phase != "kept-phase" {
		t.Fatalf("AuditLock phase = %+v, want kept-phase", got.AuditLock)
	}
}

// TestMergeGlobalReviewSnapshots_DivergingChallengeTakesBase verifies the
// divergence rule: when both sources have different non-equal
// PendingChallenge entries, base wins (it's newer by construction).
func TestMergeGlobalReviewSnapshots_DivergingChallengeTakesBase(t *testing.T) {
	t.Parallel()
	checkpoint := &GlobalReviewSnapshot{
		PendingChallenge: &GlobalReviewChallenge{ID: "g-old", RequestingAgent: GlobalReviewAgentInspector},
	}
	base := &GlobalReviewSnapshot{
		PendingChallenge: &GlobalReviewChallenge{ID: "g-new", RequestingAgent: GlobalReviewAgentInspector},
	}
	got := mergeGlobalReviewSnapshots(checkpoint, base, nil)
	if got == nil || got.PendingChallenge == nil {
		t.Fatal("expected merged snapshot with PendingChallenge")
	}
	if got.PendingChallenge.ID != "g-new" {
		t.Fatalf("PendingChallenge.ID = %q, want g-new (base wins)", got.PendingChallenge.ID)
	}
}

// TestMergeGlobalReviewSnapshots_Deterministic verifies that repeated
// calls to mergeGlobalReviewSnapshots with the same inputs produce
// equivalent outputs (no hidden state, no time-dependent behavior).
func TestMergeGlobalReviewSnapshots_Deterministic(t *testing.T) {
	t.Parallel()
	checkpoint := &GlobalReviewSnapshot{
		ReviewID: "review-det",
		PendingChallenge: &GlobalReviewChallenge{
			ID:              "g-old",
			RequestingAgent: GlobalReviewAgentInspector,
		},
	}
	base := &GlobalReviewSnapshot{
		PendingChallenge: &GlobalReviewChallenge{
			ID:              "g-new",
			RequestingAgent: GlobalReviewAgentInspector,
		},
	}
	a := mergeGlobalReviewSnapshots(checkpoint, base, nil)
	b := mergeGlobalReviewSnapshots(checkpoint, base, nil)
	if a == nil || b == nil {
		t.Fatal("both merges must produce non-nil")
	}
	if a.PendingChallenge == nil || b.PendingChallenge == nil {
		t.Fatal("both merges must retain PendingChallenge")
	}
	if a.PendingChallenge.ID != b.PendingChallenge.ID {
		t.Fatalf("non-deterministic: a=%q b=%q", a.PendingChallenge.ID, b.PendingChallenge.ID)
	}
}
