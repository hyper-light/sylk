package shared

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
)

func TestContinuationDeadlineUsesConsultInactivity(t *testing.T) {
	board := testContinuationBoard("board-cont-idle")
	postContinuationConsultClaim(t, board, "consult-idle")

	resumed := make(chan map[string]*AwaitedClaimResult, 1)
	store := NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "architect",
		SessionID: "sess-cont-idle",
		Board:     board,
		ResumeFn: func(_ context.Context, _ *TurnSnapshot, results map[string]*AwaitedClaimResult) error {
			resumed <- results
			return nil
		},
	})
	if store == nil {
		t.Fatal("expected continuation store")
	}

	_, yielded, err := store.AwaitConsultsOrYield(context.Background(), AwaitOptions{
		ConsultIDs:      []string{"consult-idle"},
		AwaitToolCallID: "tool-call-consult",
		AwaitToolName:   "consult_peer",
		Deadline:        time.Now().Add(120 * time.Millisecond),
		Snapshot:        &TurnSnapshot{Request: &providers.Request{}},
	})
	if err != ErrConsultYielded || !yielded {
		t.Fatalf("AwaitConsultsOrYield yielded=%v err=%v, want ErrConsultYielded", yielded, err)
	}

	time.Sleep(60 * time.Millisecond)
	if err := board.SetClaimContext(context.Background(), "consult-idle", "Librarian is reasoning deeply..."); err != nil {
		t.Fatalf("SetClaimContext: %v", err)
	}

	select {
	case results := <-resumed:
		t.Fatalf("resumed before inactivity window elapsed: %#v", results)
	case <-time.After(100 * time.Millisecond):
	}

	select {
	case results := <-resumed:
		got := results["consult-idle"]
		if got == nil {
			t.Fatalf("results = %#v, want consult-idle timeout", results)
		}
		if got.Status != claims.ConsultStatusTimeout {
			t.Fatalf("status = %q, want timeout", got.Status)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("expected timeout after refreshed inactivity window")
	}
}

func TestContinuationDeadlineTimesOutWithoutActivity(t *testing.T) {
	board := testContinuationBoard("board-cont-timeout")
	postContinuationConsultClaim(t, board, "consult-timeout")

	resumed := make(chan map[string]*AwaitedClaimResult, 1)
	store := NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "architect",
		SessionID: "sess-cont-timeout",
		Board:     board,
		ResumeFn: func(_ context.Context, _ *TurnSnapshot, results map[string]*AwaitedClaimResult) error {
			resumed <- results
			return nil
		},
	})

	_, yielded, err := store.AwaitConsultsOrYield(context.Background(), AwaitOptions{
		ConsultIDs:      []string{"consult-timeout"},
		AwaitToolCallID: "tool-call-consult",
		AwaitToolName:   "consult_peer",
		Deadline:        time.Now().Add(80 * time.Millisecond),
		Snapshot:        &TurnSnapshot{Request: &providers.Request{}},
	})
	if err != ErrConsultYielded || !yielded {
		t.Fatalf("AwaitConsultsOrYield yielded=%v err=%v, want ErrConsultYielded", yielded, err)
	}

	select {
	case results := <-resumed:
		got := results["consult-timeout"]
		if got == nil || got.Status != claims.ConsultStatusTimeout {
			t.Fatalf("results = %#v, want timeout", results)
		}
		testaments := board.TestamentsByClaim("consult-timeout")
		if len(testaments) == 0 {
			t.Fatal("timeout did not submit an error testament against the awaited claim")
		}
		found := false
		for _, testament := range testaments {
			for _, artifact := range testament.Artifacts {
				if artifact != nil && artifact.Kind == claims.ArtifactKindToolTimeout {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("timeout testaments = %#v, want %q artifact", testaments, claims.ArtifactKindToolTimeout)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected timeout without consult activity")
	}
}

func TestAwaitClaimResultsSynchronouslyResolvesCanonicalResult(t *testing.T) {
	board := testContinuationBoard("board-cont-sync")
	store := NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "architect",
		SessionID: "sess-cont-sync",
		Board:     board,
	})
	if store == nil {
		t.Fatal("expected continuation store")
	}
	done := make(chan map[string]*AwaitedClaimResult, 1)
	errs := make(chan error, 1)
	go func() {
		results, err := store.AwaitClaimResults(context.Background(), []string{"claim-sync"}, time.Now().Add(time.Second))
		if err != nil {
			errs <- err
			return
		}
		done <- results
	}()

	store.DeliverClaimResult(context.Background(), &AwaitedClaimResult{
		ClaimID:          "claim-sync",
		Status:           claims.ConsultStatusCompleted,
		ResponseSummary:  "peer answered",
		ResponderAgentID: "librarian",
	})

	select {
	case err := <-errs:
		t.Fatalf("AwaitClaimResults returned error: %v", err)
	case results := <-done:
		got := results["claim-sync"]
		if got == nil || got.ResponseSummary != "peer answered" {
			t.Fatalf("results = %#v", results)
		}
	case <-time.After(time.Second):
		t.Fatal("AwaitClaimResults did not return")
	}
}

func testContinuationBoard(id string) *claims.ClaimsBoard {
	return claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   id,
		SessionID: "sess-" + id,
		TaskID:    "task-" + id,
	})
}

func postContinuationConsultClaim(t *testing.T, board *claims.ClaimsBoard, id string) {
	t.Helper()
	err := board.PostAction(context.Background(), claims.Action{
		AgentID: "architect",
		Type:    claims.ActionTypeConsultation,
	}, []claims.Claim{{
		ID:          id,
		Title:       "Consult librarian",
		Description: "consult",
		ActionType:  claims.ActionTypeConsultation,
		Scope: []claims.ClaimScopeEntry{
			{Kind: "consult_id", Key: id},
		},
		Relations: []claims.Relation{
			{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "librarian", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
	}})
	if err != nil {
		t.Fatalf("PostAction: %v", err)
	}
}
