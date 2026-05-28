package claims

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceDispatcherSuccessPostsTestamentAndSatisfiesReceiptClaim(t *testing.T) {
	board, claimID := serviceDispatchBoard(t)
	participant := serviceDispatchParticipant(t)
	handler := serviceHandlerFunc(func(context.Context, ServiceClaimRequest) (ServiceClaimResult, error) {
		return ServiceClaimResult{Summary: "service done", Artifacts: []*Artifact{{Kind: ArtifactKindReadiness, Reference: "ready"}}}, nil
	})
	dispatcher := newTestServiceDispatcher(t, board, participant, handler)
	if err := dispatcher.DispatchDelta(context.Background(), serviceClaimPostedDelta(board, claimID, participant)); err != nil {
		t.Fatalf("DispatchDelta: %v", err)
	}
	claim := assertServiceClaimLifecycle(t, board, claimID, ClaimLifecycleSatisfied)
	if claim.Status != ClaimStatusAccepted {
		t.Fatalf("claim status = %s, want accepted", claim.Status)
	}
	testaments := board.TestamentsByClaim(claimID)
	if len(testaments) != 1 {
		t.Fatalf("testaments by claim = %d, want 1", len(testaments))
	}
	if testaments[0].LifecycleStatus != TestamentLifecycleValidated {
		t.Fatalf("testament lifecycle = %s, want validated", testaments[0].LifecycleStatus)
	}
}

func TestServiceDispatcherHandlerErrorRecordsLifecycleFailure(t *testing.T) {
	board, claimID := serviceDispatchBoard(t)
	participant := serviceDispatchParticipant(t)
	handler := serviceHandlerFunc(func(context.Context, ServiceClaimRequest) (ServiceClaimResult, error) {
		return ServiceClaimResult{}, errors.New("handler unavailable")
	})
	dispatcher := newTestServiceDispatcher(t, board, participant, handler)
	if err := dispatcher.DispatchDelta(context.Background(), serviceClaimPostedDelta(board, claimID, participant)); err != nil {
		t.Fatalf("DispatchDelta: %v", err)
	}
	assertServiceClaimLifecycle(t, board, claimID, ClaimLifecycleValidationErrored)
	if len(board.TestamentsByClaim(claimID)) == 0 {
		t.Fatal("handler error produced no error testament")
	}
}

func TestServiceDispatcherDeduplicatesDeltaKey(t *testing.T) {
	board, claimID := serviceDispatchBoard(t)
	participant := serviceDispatchParticipant(t)
	count := 0
	handler := serviceHandlerFunc(func(context.Context, ServiceClaimRequest) (ServiceClaimResult, error) {
		count++
		return ServiceClaimResult{Summary: "service done"}, nil
	})
	dispatcher := newTestServiceDispatcher(t, board, participant, handler)
	delta := serviceClaimPostedDelta(board, claimID, participant)
	if err := dispatcher.DispatchDelta(context.Background(), delta); err != nil {
		t.Fatalf("DispatchDelta first: %v", err)
	}
	if err := dispatcher.DispatchDelta(context.Background(), delta); err != nil {
		t.Fatalf("DispatchDelta second: %v", err)
	}
	if count != 1 {
		t.Fatalf("handler calls = %d, want 1", count)
	}
}

func serviceDispatchBoard(t *testing.T) (*ClaimsBoard, string) {
	t.Helper()
	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "service-board", SessionID: "service-session", TaskID: "service-task"})
	claim := Claim{
		Title:       "Run deterministic service",
		Description: "The service must process this claim.",
		ActionType:  ActionTypeTask,
		Relations: []Relation{
			{Related: "issuer", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "tool_runtime", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		Validations: []*Validation{{
			ID:          "service-receipt",
			Type:        ValidationTypeReceipt,
			Required:    true,
			Description: "service testament received",
			QualityBar:  "receipt.received",
			Status:      ValidationStatusPending,
		}},
	}
	generated, err := board.GenerateClaimAction(context.Background(), Action{AgentID: "issuer", Type: ActionTypeTask}, []Claim{claim}, GenerateClaimActionOptions{IdempotencyKey: "service-claim"})
	if err != nil {
		t.Fatalf("GenerateClaimAction: %v", err)
	}
	claimID := generated.Claims[0].ID
	if err := board.PostGeneratedClaim(context.Background(), claimID, "issuer", ClaimPostOptions{Reason: "posted"}); err != nil {
		t.Fatalf("PostGeneratedClaim: %v", err)
	}
	return board, claimID
}

func serviceDispatchParticipant(t *testing.T) ParticipantRegistration {
	t.Helper()
	participant, err := NewServiceParticipantRegistration("tool_runtime", map[string]string{"session": "service-session"}, 4, 1, time.Second, []ActionType{ActionTypeTask})
	if err != nil {
		t.Fatalf("NewServiceParticipantRegistration: %v", err)
	}
	return participant
}

func newTestServiceDispatcher(t *testing.T, board *ClaimsBoard, participant ParticipantRegistration, handler ServiceHandler) *ServiceDispatcher {
	t.Helper()
	dispatcher, err := NewServiceDispatcher(ServiceDispatcherConfig{Board: board, Scope: immediateScope{}, Participant: participant, Handler: handler})
	if err != nil {
		t.Fatalf("NewServiceDispatcher: %v", err)
	}
	return dispatcher
}

func serviceClaimPostedDelta(board *ClaimsBoard, claimID string, participant ParticipantRegistration) CanonicalDelta {
	return NewCanonicalDelta(
		DeltaActionClaimPosted,
		board.SessionID(),
		board.BoardID(),
		board.HighWaterSequence(),
		time.Now(),
		DegradedAgentRef("issuer", "test"),
		[]DeltaRef{{Role: "claim", Type: RelatedTypeClaim, ID: claimID}},
		&DeltaDelivery{To: []AgentRef{participant.AgentRef()}, Relationship: RelationshipSubject},
		map[string]any{"claim": map[string]any{"action": string(ActionTypeTask)}},
	)
}

func assertServiceClaimLifecycle(t *testing.T, board *ClaimsBoard, claimID string, want ClaimLifecycleStatus) *Claim {
	t.Helper()
	claim, ok := board.CloneClaim(claimID)
	if !ok {
		t.Fatalf("claim %s not found", claimID)
	}
	if claim.LifecycleStatus != want {
		t.Fatalf("claim lifecycle = %s, want %s", claim.LifecycleStatus, want)
	}
	return claim
}

type serviceHandlerFunc func(context.Context, ServiceClaimRequest) (ServiceClaimResult, error)

func (f serviceHandlerFunc) HandleServiceClaim(ctx context.Context, req ServiceClaimRequest) (ServiceClaimResult, error) {
	return f(ctx, req)
}

type immediateScope struct{}

func (immediateScope) Go(_ string, _ time.Duration, fn func(context.Context) error) error {
	return fn(context.Background())
}
