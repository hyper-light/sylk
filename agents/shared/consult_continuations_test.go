package shared

import (
	"context"
	"fmt"
	"strings"
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

func TestAwaitClaimResultsSynchronouslyReconcilesPostedTestamentFromBoard(t *testing.T) {
	board := testContinuationBoard("board-cont-sync-replay")
	postContinuationConsultClaim(t, board, "claim-sync-replay")
	submitContinuationConsultTestament(t, board, "claim-sync-replay", "librarian", "peer already answered")
	store := NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "architect",
		SessionID: "sess-cont-sync-replay",
		Board:     board,
	})

	results, err := store.AwaitClaimResults(context.Background(), []string{"claim-sync-replay"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("AwaitClaimResults returned error: %v", err)
	}
	got := results["claim-sync-replay"]
	if got == nil || got.ResponseSummary != "peer already answered" {
		t.Fatalf("results = %#v, want posted board testament", results)
	}
	if got.Status != claims.ConsultStatusCompleted {
		t.Fatalf("status = %q, want completed", got.Status)
	}
}

func TestRecoverPendingContinuationsReconcilesPostedTestamentFromBoard(t *testing.T) {
	board := testContinuationBoard("board-cont-recover-replay")
	postContinuationConsultClaim(t, board, "claim-recover-replay")
	crashStore := NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "architect",
		SessionID: "sess-cont-recover-replay",
		Board:     board,
	})
	crashStore.mu.Lock()
	continuationID, err := crashStore.persistContinuationLocked(
		context.Background(),
		&TurnSnapshot{Request: &providers.Request{}},
		[]string{"claim-recover-replay"},
		time.Now().Add(time.Hour),
		time.Hour,
		nil,
	)
	crashStore.mu.Unlock()
	if err != nil {
		t.Fatalf("persistContinuationLocked: %v", err)
	}
	submitContinuationConsultTestament(t, board, "claim-recover-replay", "librarian", "answer survived restart")

	resumed := make(chan map[string]*AwaitedClaimResult, 1)
	store := NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "architect",
		SessionID: "sess-cont-recover-replay",
		Board:     board,
		ResumeFn: func(_ context.Context, _ *TurnSnapshot, results map[string]*AwaitedClaimResult) error {
			resumed <- results
			return nil
		},
	})
	if got := store.RecoverPendingContinuations(context.Background()); got != 1 {
		t.Fatalf("recovered continuations = %d, want 1", got)
	}

	select {
	case results := <-resumed:
		got := results["claim-recover-replay"]
		if got == nil || got.ResponseSummary != "answer survived restart" {
			t.Fatalf("results = %#v, want posted board testament", results)
		}
	case <-time.After(time.Second):
		t.Fatal("recovery did not resume from posted board testament")
	}
	if claim, ok := board.CloneClaim(continuationID); !ok || claimHasPendingReceipt(*claim) {
		t.Fatalf("continuation %q still has pending receipt validation after replay resume", continuationID)
	}
}

func TestContinuationStore_StopReleasesSynchronousWait(t *testing.T) {
	board := testContinuationBoard("board-cont-stop-sync")
	store := NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "architect",
		SessionID: "sess-cont-stop-sync",
		Board:     board,
	})
	if store == nil {
		t.Fatal("expected continuation store")
	}

	done := make(chan map[string]*AwaitedClaimResult, 1)
	errs := make(chan error, 1)
	go func() {
		results, err := store.AwaitClaimResults(context.Background(), []string{"claim-stop"}, time.Now().Add(time.Hour))
		if err != nil {
			errs <- err
			return
		}
		done <- results
	}()

	waitForContinuationPending(t, store)
	store.Stop("operator interrupt")

	select {
	case err := <-errs:
		t.Fatalf("AwaitClaimResults returned error: %v", err)
	case results := <-done:
		got := results["claim-stop"]
		if got == nil {
			t.Fatalf("results = %#v, want claim-stop cancellation", results)
		}
		if got.Status != claims.ConsultStatusCancelled {
			t.Fatalf("status = %q, want cancelled", got.Status)
		}
		if !strings.Contains(got.ErrorMessage, "operator interrupt") {
			t.Fatalf("error message = %q, want interrupt reason", got.ErrorMessage)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not release synchronous continuation wait")
	}
}

func TestContinuationStore_OrphanResolutionsAreGloballyBounded(t *testing.T) {
	board := testContinuationBoard("board-cont-orphan-bound")
	store := NewContinuationStore(ContinuationStoreConfig{
		AgentID:   "architect",
		SessionID: "sess-cont-orphan-bound",
		Board:     board,
	})
	if store == nil {
		t.Fatal("expected continuation store")
	}
	limit := store.orphanLimit
	for i := range limit + continuationOrphanLimitMin {
		store.DeliverClaimResult(context.Background(), &AwaitedClaimResult{
			ClaimID: fmt.Sprintf("orphan-%d", i),
			Status:  claims.ConsultStatusCompleted,
		})
	}
	store.mu.Lock()
	got := len(store.orphans)
	store.mu.Unlock()
	if got > limit {
		t.Fatalf("orphan resolutions = %d, want <= %d", got, limit)
	}
}

func waitForContinuationPending(t *testing.T, store *ContinuationStore) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		pending := len(store.pending)
		store.mu.Unlock()
		if pending > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("continuation wait was not registered")
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

func submitContinuationConsultTestament(t *testing.T, board *claims.ClaimsBoard, claimID, agentID, summary string) {
	t.Helper()
	err := board.SubmitTestaments(context.Background(), claims.Action{
		AgentID: agentID,
		Type:    claims.ActionTypeTestament,
	}, []claims.Testament{{
		ID:      "testament-" + claimID,
		AgentID: agentID,
		Summary: summary,
		Relations: []claims.Relation{{
			Related:      claimID,
			RelatedType:  claims.RelatedTypeClaim,
			Relationship: claims.RelationshipClaim,
		}},
		Artifacts: []*claims.Artifact{{
			ID:        "artifact-" + claimID,
			AgentID:   agentID,
			Kind:      claims.ArtifactKindResponseText,
			Reference: summary,
		}},
	}})
	if err != nil {
		t.Fatalf("SubmitTestaments: %v", err)
	}
}
