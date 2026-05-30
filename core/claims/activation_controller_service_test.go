package claims

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestActivationControllerServiceActivateDeactivateAndQuery(t *testing.T) {
	service := NewActivationControllerService(ActivationControllerServiceConfig{})
	record, err := service.Activate(context.Background(), ActivationRecordArtifactData{
		ParticipantID:   "guide",
		ParticipantType: "agent",
		Tier:            "always_hot",
		ReplicaCount:    2,
		Duration:        time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if !record.Ready || record.ReplicaCount != 2 || record.Tier != "always_hot" {
		t.Fatalf("activation record = %+v, want ready always_hot replicas", record)
	}
	got, ok := service.QueryTier(context.Background(), "guide")
	if !ok || got.ParticipantID != record.ParticipantID || !got.Ready {
		t.Fatalf("QueryTier = %+v ok=%v, want ready guide", got, ok)
	}
	deactivated, err := service.Deactivate(context.Background(), "guide", "shutdown")
	if err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	if deactivated.Ready || deactivated.ReplicaCount != 0 || deactivated.FailureReason != "shutdown" {
		t.Fatalf("deactivated record = %+v, want not ready with reason", deactivated)
	}
}

func TestActivationControllerServiceRejectsInvalidRecordsAndCapacity(t *testing.T) {
	service := NewActivationControllerService(ActivationControllerServiceConfig{MaxRecords: 1})
	if _, err := service.Activate(context.Background(), ActivationRecordArtifactData{ReplicaCount: 1}); !errors.Is(err, ErrActivationControllerServiceInvalid) {
		t.Fatalf("missing participant error = %v, want invalid", err)
	}
	if _, err := service.Activate(context.Background(), ActivationRecordArtifactData{ParticipantID: "guide"}); !errors.Is(err, ErrActivationControllerServiceInvalid) {
		t.Fatalf("missing replicas error = %v, want invalid", err)
	}
	if _, err := service.Activate(context.Background(), ActivationRecordArtifactData{ParticipantID: "guide", ReplicaCount: 1}); err != nil {
		t.Fatalf("Activate first: %v", err)
	}
	if _, err := service.Activate(context.Background(), ActivationRecordArtifactData{ParticipantID: "engineer", ReplicaCount: 1}); !errors.Is(err, ErrActivationControllerServiceInvalid) {
		t.Fatalf("capacity error = %v, want invalid", err)
	}
}

func TestActivationControllerServiceHandlerProducesTypedArtifacts(t *testing.T) {
	service := NewActivationControllerService(ActivationControllerServiceConfig{})
	claim := &Claim{ExpectedToolCalls: []ExpectedToolCall{{
		Tool: ActivationControllerToolActivate,
		Arguments: map[string]any{
			"participant_id":   "guardian",
			"participant_type": "service",
			"tier":             "always_hot",
			"replica_count":    1,
		},
	}}}
	result, err := service.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: claim})
	if err != nil {
		t.Fatalf("HandleServiceClaim activate: %v", err)
	}
	if len(result.Artifacts) < 1 {
		t.Fatalf("artifacts = %d, want at least 1", len(result.Artifacts))
	}
	data, err := ArtifactData[ActivationRecordArtifactData](result.Artifacts[0])
	if err != nil {
		t.Fatalf("ArtifactData activation: %v", err)
	}
	if data.ParticipantID != "guardian" || !data.Ready || data.ReplicaCount != 1 {
		t.Fatalf("activation artifact = %+v, want ready guardian", data)
	}
	if result.Artifacts[0].Kind != ArtifactKindActivationRecord {
		t.Fatalf("activation artifact kind = %s, want %s", result.Artifacts[0].Kind, ArtifactKindActivationRecord)
	}

	query := &Claim{ExpectedToolCalls: []ExpectedToolCall{{Tool: ActivationControllerToolQueryTier, Arguments: map[string]any{"participant_id": "guardian"}}}}
	result, err = service.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: query})
	if err != nil {
		t.Fatalf("HandleServiceClaim query: %v", err)
	}
	data, err = ArtifactData[ActivationRecordArtifactData](result.Artifacts[0])
	if err != nil || data.Tier != "always_hot" {
		t.Fatalf("query artifact = %+v err=%v, want always_hot", data, err)
	}
}

func TestActivationControllerServiceTransitionAndQueryArtifacts(t *testing.T) {
	service := NewActivationControllerService(ActivationControllerServiceConfig{})
	if _, err := service.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: &Claim{ExpectedToolCalls: []ExpectedToolCall{{
		Tool: ActivationControllerToolActivate,
		Arguments: map[string]any{
			"participant_id": "engineer",
			"tier":           "hot",
			"target_tier":    "hot",
			"replica_count":  float64(1),
		},
	}}}}); err != nil {
		t.Fatalf("activate setup: %v", err)
	}

	result, err := service.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: &Claim{ExpectedToolCalls: []ExpectedToolCall{{
		Tool: ActivationControllerToolDeactivate,
		Arguments: map[string]any{
			"participant_id": "engineer",
			"operation":      "tier_transition",
			"previous_tier":  "hot",
			"target_tier":    "warm",
			"tier":           "warm",
			"replica_count":  float64(1),
			"ready":          true,
		},
	}}}})
	if err != nil {
		t.Fatalf("transition HandleServiceClaim: %v", err)
	}
	data, err := ArtifactData[ActivationRecordArtifactData](result.Artifacts[0])
	if err != nil {
		t.Fatalf("transition artifact data: %v", err)
	}
	if result.Artifacts[0].Kind != ArtifactKindTierTransition || data.PreviousTier != "hot" || data.TargetTier != "warm" || !data.Ready {
		t.Fatalf("transition artifact = %+v kind=%s, want hot->warm ready transition", data, result.Artifacts[0].Kind)
	}

	query, err := service.HandleServiceClaim(context.Background(), ServiceClaimRequest{Claim: &Claim{ExpectedToolCalls: []ExpectedToolCall{{
		Tool:      ActivationControllerToolQueryTier,
		Arguments: map[string]any{"participant_id": "engineer"},
	}}}})
	if err != nil {
		t.Fatalf("query HandleServiceClaim: %v", err)
	}
	data, err = ArtifactData[ActivationRecordArtifactData](query.Artifacts[0])
	if err != nil || data.Operation != "query_tier" || data.Tier != "warm" {
		t.Fatalf("query artifact = %+v err=%v, want query_tier warm", data, err)
	}
}

func TestActivationControllerServiceE2EDispatchPostsActivationTestament(t *testing.T) {
	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "activation-board", SessionID: "activation-session", TaskID: "task"})
	participant, err := NewServiceParticipantRegistration("activation_controller", map[string]string{"session": "activation-session"}, 4, 1, time.Second, []ActionType{ActionTypeTask})
	if err != nil {
		t.Fatalf("NewServiceParticipantRegistration: %v", err)
	}
	generated, err := board.GenerateClaimAction(context.Background(), Action{AgentID: "issuer", Type: ActionTypeTask}, []Claim{{
		Title:       "Activate guardian",
		Description: "Activation controller should report readiness.",
		ActionType:  ActionTypeTask,
		Relations: []Relation{
			{Related: "issuer", RelatedType: RelatedTypeAgent, Relationship: RelationshipIssuer},
			{Related: "activation_controller", RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
		},
		ExpectedToolCalls: []ExpectedToolCall{{
			Tool: ActivationControllerToolActivate,
			Arguments: map[string]any{
				"participant_id": "guardian",
				"tier":           "always_hot",
				"replica_count":  1,
			},
		}},
		Validations: []*Validation{{ID: "receipt", Type: ValidationTypeReceipt, Required: true, Description: "receipt", QualityBar: "received"}},
	}}, GenerateClaimActionOptions{IdempotencyKey: "activation-e2e"})
	if err != nil {
		t.Fatalf("GenerateClaimAction: %v", err)
	}
	claimID := generated.Claims[0].ID
	if err := board.PostGeneratedClaim(context.Background(), claimID, "issuer", ClaimPostOptions{Reason: "activation"}); err != nil {
		t.Fatalf("PostGeneratedClaim: %v", err)
	}
	dispatcher, err := NewServiceDispatcher(ServiceDispatcherConfig{Board: board, Scope: immediateScope{}, Participant: participant, Handler: NewActivationControllerService(ActivationControllerServiceConfig{})})
	if err != nil {
		t.Fatalf("NewServiceDispatcher: %v", err)
	}
	if err := dispatcher.DispatchDelta(context.Background(), serviceClaimPostedDeltaForParticipant(board, claimID, participant, ActionTypeTask)); err != nil {
		t.Fatalf("DispatchDelta: %v", err)
	}
	claim := assertServiceClaimLifecycle(t, board, claimID, ClaimLifecycleSatisfied)
	if claim.Status != ClaimStatusAccepted {
		t.Fatalf("claim status = %s, want accepted", claim.Status)
	}
	artifact := findTestamentArtifactByName(t, board, claimID, "activation_record")
	data, err := ArtifactData[ActivationRecordArtifactData](artifact)
	if err != nil || data.ParticipantID != "guardian" || !data.Ready {
		t.Fatalf("activation testament data = %+v err=%v, want ready guardian", data, err)
	}
}

func TestActivationControllerServiceConcurrentActivationConverges(t *testing.T) {
	service := NewActivationControllerService(ActivationControllerServiceConfig{})
	const attempts = 32
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.Activate(context.Background(), ActivationRecordArtifactData{ParticipantID: "guide", Tier: "always_hot", ReplicaCount: 1})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Activate concurrent: %v", err)
		}
	}
	got, ok := service.QueryTier(context.Background(), "guide")
	if !ok || !got.Ready || got.ReplicaCount != 1 {
		t.Fatalf("converged activation = %+v ok=%v, want one ready state", got, ok)
	}
}

func serviceClaimPostedDeltaForParticipant(board *ClaimsBoard, claimID string, participant ParticipantRegistration, action ActionType) CanonicalDelta {
	return NewCanonicalDelta(
		DeltaActionClaimPosted,
		board.SessionID(),
		board.BoardID(),
		board.HighWaterSequence(),
		time.Now(),
		DegradedAgentRef("issuer", "test"),
		[]DeltaRef{{Role: "claim", Type: RelatedTypeClaim, ID: claimID}},
		&DeltaDelivery{To: []AgentRef{participant.AgentRef()}, Relationship: RelationshipSubject},
		map[string]any{"claim": map[string]any{"id": claimID, "action": string(action)}},
	)
}

func findTestamentArtifactByName(t *testing.T, board *ClaimsBoard, claimID, name string) *Artifact {
	t.Helper()
	for _, testament := range board.TestamentsByClaim(claimID) {
		for _, artifact := range testament.Artifacts {
			if artifact != nil && artifact.ArtifactName == name {
				return artifact
			}
		}
	}
	t.Fatalf("artifact %s for claim %s not found", name, claimID)
	return nil
}
