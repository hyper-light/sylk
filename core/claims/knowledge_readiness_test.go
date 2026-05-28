package claims

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	knowledgeReadyTestDeadline      = time.Second
	knowledgeReadyNoEarlyReturnWait = 20 * time.Millisecond
)

func TestEnsureKnowledgeBackendReadyClaim(t *testing.T) {
	board := testBoard()

	if err := EnsureKnowledgeBackendReadyClaim(context.Background(), board); err != nil {
		t.Fatalf("EnsureKnowledgeBackendReadyClaim returned error: %v", err)
	}
	if err := EnsureKnowledgeBackendReadyClaim(context.Background(), board); err != nil {
		t.Fatalf("EnsureKnowledgeBackendReadyClaim idempotence error: %v", err)
	}

	claim, ok := board.CloneClaim(KnowledgeBackendReadyClaimID)
	if !ok {
		t.Fatal("readiness claim was not posted")
	}
	if claim.ActionType != ActionTypeBoot {
		t.Fatalf("claim action type = %q, want %q", claim.ActionType, ActionTypeBoot)
	}
	if SubjectAgentID(claim.Relations) != KnowledgeBackendAgentID {
		t.Fatalf("claim subject = %q, want %q", SubjectAgentID(claim.Relations), KnowledgeBackendAgentID)
	}
	if len(claim.Validations) != 1 || claim.Validations[0].Type != ValidationTypeReceipt {
		t.Fatalf("claim validations = %+v, want one receipt validation", claim.Validations)
	}
}

func TestSubmitKnowledgeBackendReadyTestamentAcceptsReadinessClaim(t *testing.T) {
	board := testBoard()

	testament, err := SubmitKnowledgeBackendReadyTestament(context.Background(), board, map[string]any{"level": "partial"})
	if err != nil {
		t.Fatalf("SubmitKnowledgeBackendReadyTestament returned error: %v", err)
	}
	if testament == nil {
		t.Fatal("expected readiness testament")
	}
	if got, ok := KnowledgeBackendReadyTestament(board); !ok || got.ID != testament.ID {
		t.Fatalf("KnowledgeBackendReadyTestament = (%v, %v), want submitted testament", got, ok)
	}

	claim, ok := board.CloneClaim(KnowledgeBackendReadyClaimID)
	if !ok {
		t.Fatal("readiness claim missing")
	}
	if claim.Status != ClaimStatusAccepted {
		t.Fatalf("claim status = %q, want %q", claim.Status, ClaimStatusAccepted)
	}
	if claim.Validations[0].Status != ValidationStatusPassed {
		t.Fatalf("receipt status = %q, want %q", claim.Validations[0].Status, ValidationStatusPassed)
	}

	again, err := SubmitKnowledgeBackendReadyTestament(context.Background(), board, nil)
	if err != nil {
		t.Fatalf("second SubmitKnowledgeBackendReadyTestament returned error: %v", err)
	}
	if again.ID != testament.ID {
		t.Fatalf("second testament ID = %q, want existing %q", again.ID, testament.ID)
	}
}

func TestWaitForKnowledgeBackendReadyImmediate(t *testing.T) {
	board := testBoard()
	submitted, err := SubmitKnowledgeBackendReadyTestament(context.Background(), board, nil)
	if err != nil {
		t.Fatalf("SubmitKnowledgeBackendReadyTestament returned error: %v", err)
	}

	got, err := WaitForKnowledgeBackendReady(context.Background(), board)
	if err != nil {
		t.Fatalf("WaitForKnowledgeBackendReady returned error: %v", err)
	}
	if got.ID != submitted.ID {
		t.Fatalf("wait returned testament ID %q, want %q", got.ID, submitted.ID)
	}
}

func TestWaitForKnowledgeBackendReadyWaitsOnDelta(t *testing.T) {
	board := testBoard()
	ctx, cancel := context.WithTimeout(context.Background(), knowledgeReadyTestDeadline)
	defer cancel()

	var once sync.Once
	claimPosted := make(chan struct{})
	unsubscribe := board.SubscribeDelta(func(delta BoardMutationDelta) error {
		if delta.Kind == "claim_posted" && delta.ClaimID == KnowledgeBackendReadyClaimID {
			once.Do(func() { close(claimPosted) })
		}
		return nil
	})
	defer unsubscribe()

	type waitResult struct {
		testament *Testament
		err       error
	}
	waited := make(chan waitResult, 1)
	go func() {
		testament, err := WaitForKnowledgeBackendReady(ctx, board)
		waited <- waitResult{testament: testament, err: err}
	}()

	select {
	case <-claimPosted:
	case result := <-waited:
		t.Fatalf("wait returned before posting readiness claim: testament=%v err=%v", result.testament, result.err)
	case <-ctx.Done():
		t.Fatalf("readiness claim was not posted: %v", ctx.Err())
	}

	select {
	case result := <-waited:
		t.Fatalf("wait returned before readiness testament: testament=%v err=%v", result.testament, result.err)
	case <-time.After(knowledgeReadyNoEarlyReturnWait):
	}

	submitted, err := SubmitKnowledgeBackendReadyTestament(ctx, board, nil)
	if err != nil {
		t.Fatalf("SubmitKnowledgeBackendReadyTestament returned error: %v", err)
	}

	select {
	case result := <-waited:
		if result.err != nil {
			t.Fatalf("wait returned error: %v", result.err)
		}
		if result.testament.ID != submitted.ID {
			t.Fatalf("wait returned testament ID %q, want %q", result.testament.ID, submitted.ID)
		}
	case <-ctx.Done():
		t.Fatalf("wait did not observe readiness testament delta: %v", ctx.Err())
	}
}

func TestWaitForKnowledgeBackendReadyContextDeadline(t *testing.T) {
	board := testBoard()
	ctx, cancel := context.WithTimeout(context.Background(), knowledgeReadyNoEarlyReturnWait)
	defer cancel()

	_, err := WaitForKnowledgeBackendReady(ctx, board)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait error = %v, want context deadline", err)
	}
	if _, ok := board.CloneClaim(KnowledgeBackendReadyClaimID); !ok {
		t.Fatal("wait did not post readiness claim")
	}
}
