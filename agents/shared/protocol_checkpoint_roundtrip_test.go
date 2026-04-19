package shared

import (
	"encoding/json"
	"testing"
)

// TestGlobalReviewCheckpoint_SerializesTerminalAction is the DUR-02
// acceptance test. Before the fix the checkpoint struct had no
// TerminalAction field, so a process that crashed after
// TryApplyTurnAction set the guard but before the next turn boundary
// would lose it on resume — the in-memory guard protected the first
// run; the persisted checkpoint did not protect subsequent runs.
//
// The test serializes a checkpoint with a non-nil TerminalAction,
// deserializes it, and confirms the field survives the round-trip.
// A regression that drops TerminalAction from the struct would
// silently pass JSON encode (omitempty) and fail this test on
// decode.
func TestGlobalReviewCheckpoint_SerializesTerminalAction(t *testing.T) {
	original := globalReviewCheckpoint{
		RequiredAction: "commit_to_disk",
		RequiredReason: "tester validated",
		TerminalAction: &GlobalReviewTurnAction{
			Type:      GlobalReviewActionCommit,
			AgentType: "inspector",
			Reason:    "post-validation commit",
		},
	}

	data, err := json.Marshal(&original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round globalReviewCheckpoint
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round.TerminalAction == nil {
		t.Fatal("TerminalAction dropped in round-trip — DUR-02 regression")
	}
	if round.TerminalAction.Type != GlobalReviewActionCommit {
		t.Errorf("TerminalAction.Type = %q, want commit_to_disk", round.TerminalAction.Type)
	}
	if round.TerminalAction.AgentType != "inspector" {
		t.Errorf("TerminalAction.AgentType = %q, want inspector", round.TerminalAction.AgentType)
	}
}

// TestGlobalReviewCheckpoint_NilTerminalActionOmitted protects JSON
// size: a checkpoint with no terminal action must not pay a nonzero
// byte cost for the field. omitempty is the contract.
func TestGlobalReviewCheckpoint_NilTerminalActionOmitted(t *testing.T) {
	empty := globalReviewCheckpoint{RequiredAction: "accept_checkpoint"}
	data, err := json.Marshal(&empty)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(data); contains(got, "terminal_action") {
		t.Errorf("nil TerminalAction leaked into JSON: %s", got)
	}
}

// TestPipelineProtocolCheckpoint_SerializesTerminalAction is the
// pipeline half of the DUR-02 acceptance check — same invariant as
// the global-review test, different protocol.
func TestPipelineProtocolCheckpoint_SerializesTerminalAction(t *testing.T) {
	original := pipelineProtocolCheckpoint{
		RequiredAction: "handoff_to_ot",
		TerminalAction: &PipelineTurnAction{
			Type:      PipelineProtocolActionHandoff,
			AgentType: "tester-pipeline",
			Reason:    "validation landed",
		},
	}

	data, err := json.Marshal(&original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round pipelineProtocolCheckpoint
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round.TerminalAction == nil {
		t.Fatal("pipeline TerminalAction dropped in round-trip — DUR-02 regression")
	}
	if round.TerminalAction.Type != PipelineProtocolActionHandoff {
		t.Errorf("TerminalAction.Type = %q, want handoff", round.TerminalAction.Type)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) != -1
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
