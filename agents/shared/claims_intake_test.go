package shared

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
)

func TestClaimsIntakeExpectedConsultTestamentDeliversContinuation(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "board-intake-consult",
		SessionID: "sess-intake-consult",
		TaskID:    "task-intake-consult",
	})
	var resumed map[string]*claims.ConsultResolvedDelta
	store := NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "architect-1",
		SessionID: "sess-intake-consult",
		Board:     board,
		ResumeFn: func(_ context.Context, _ *TurnSnapshot, results map[string]*claims.ConsultResolvedDelta) error {
			resumed = results
			return nil
		},
	})
	_, yielded, err := store.AwaitConsultsOrYield(context.Background(), AwaitOptions{
		ConsultIDs:      []string{"consult-123"},
		AwaitToolCallID: "tool-call-1",
		AwaitToolName:   "consult_peer",
		Deadline:        time.Now().Add(time.Hour),
		Snapshot: &TurnSnapshot{
			Request: &providers.Request{},
		},
	})
	if !yielded || !IsConsultYielded(err) {
		t.Fatalf("AwaitConsultsOrYield yielded=%v err=%v, want ErrConsultYielded", yielded, err)
	}

	entry := &claims.GraphEntryPoint{
		Delta: claims.TestamentDelta{
			SessionID:      "sess-intake-consult",
			BoardID:        "board-intake-consult",
			ClaimID:        "claim-1",
			TestamentID:    "testament-1",
			ActionKind:     claims.ActionTypeConsultation,
			Verdict:        claims.TestamentVerdictWorkComplete,
			SubjectAgentID: "librarian",
			Summary:        "The repository is currently a Go project.",
		},
		Node: claims.GraphNode{
			Claim: &claims.Claim{
				ID:         "claim-1",
				Title:      "Consult librarian",
				ActionType: claims.ActionTypeConsultation,
				Scope: []claims.ClaimScopeEntry{
					{Kind: "consultation", Key: "librarian"},
					{Kind: "consult_id", Key: "consult-123"},
				},
			},
			Testament: &claims.Testament{
				ID:      "testament-1",
				AgentID: "librarian",
				Summary: "The repository is currently a Go project.",
				Artifacts: []*claims.Artifact{{
					ID:        "artifact-1",
					Kind:      "workspace_state",
					Reference: "No Python package metadata exists.",
				}},
			},
		},
		Expectation: &claims.Expectation{
			ClaimID:       "claim-1",
			ExpectedDelta: claims.DeltaKindTestament,
		},
	}
	if !deliverExpectedPeerTestamentToContinuation(ClaimsIntakeConfig{
		AgentID:           "architect-1",
		SessionID:         "sess-intake-consult",
		ContinuationStore: store,
	}, entry) {
		t.Fatal("expected peer testament to be consumed as a continuation resolution")
	}
	if resumed == nil {
		t.Fatal("resume was not invoked")
	}
	got := resumed["consult-123"]
	if got == nil {
		t.Fatalf("results = %#v, want consult-123", resumed)
	}
	if got.Status != claims.ConsultStatusCompleted {
		t.Fatalf("status = %q", got.Status)
	}
	if got.ResponderAgentID != "librarian" {
		t.Fatalf("responder = %q", got.ResponderAgentID)
	}
	if got.ResponseSummary != "The repository is currently a Go project." {
		t.Fatalf("summary = %q", got.ResponseSummary)
	}
	var payload map[string]any
	if err := json.Unmarshal(got.ResponsePayload, &payload); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if payload["response"] != "The repository is currently a Go project." {
		t.Fatalf("payload.response = %#v", payload["response"])
	}
}

func TestClaimsIntakeExpectedConsultTestamentWithoutResolutionIDIsConsumed(t *testing.T) {
	consumed := deliverExpectedPeerTestamentToContinuation(ClaimsIntakeConfig{
		AgentID:           "architect-1",
		SessionID:         "sess-intake-consult-missing-id",
		ContinuationStore: &ContinuationStore{},
	}, &claims.GraphEntryPoint{
		Delta: claims.TestamentDelta{
			ActionKind:  claims.ActionTypeConsultation,
			ClaimID:     "claim-1",
			TestamentID: "testament-1",
			Summary:     "done",
		},
		Node: claims.GraphNode{
			Claim: &claims.Claim{
				ID:         "",
				ActionType: claims.ActionTypeConsultation,
			},
		},
		Expectation: &claims.Expectation{
			ClaimID:       "",
			ExpectedDelta: claims.DeltaKindTestament,
		},
	})
	if !consumed {
		t.Fatal("expected missing-id peer testament to be consumed rather than dispatched as fresh inference")
	}
}
