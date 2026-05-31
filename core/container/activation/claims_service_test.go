package activation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

func TestInvokeActivationServiceClaimPostsTypedActivationRecord(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "activation-service-board", SessionID: "activation-session", TaskID: "task"})
	record := claims.ActivationRecordArtifactData{
		ParticipantID:   "guide",
		ParticipantType: "agent",
		Tier:            "hot",
		ReplicaCount:    1,
		Ready:           true,
		Duration:        time.Millisecond,
	}
	if err := invokeActivationServiceClaim(context.Background(), board, claims.ActivationControllerToolActivate, record); err != nil {
		t.Fatalf("invokeActivationServiceClaim: %v", err)
	}
	assertActivationServiceRecord(t, board, "guide", true, "")
}

func TestInvokeActivationServiceClaimUsesResolvableActivationControllerIdentity(t *testing.T) {
	sessionID := "activation-session"
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:          "activation-service-identity-board",
		SessionID:        sessionID,
		TaskID:           "task",
		AgentRefResolver: activationControllerTestResolver(t, sessionID),
	})
	record := claims.ActivationRecordArtifactData{
		ParticipantID:   "guide",
		ParticipantType: "agent",
		Operation:       "activate",
		Tier:            "hot",
		ReplicaCount:    1,
		Ready:           true,
		Duration:        time.Millisecond,
	}
	if err := invokeActivationServiceClaim(context.Background(), board, claims.ActivationControllerToolActivate, record); err != nil {
		t.Fatalf("invokeActivationServiceClaim with resolver: %v", err)
	}
	projection := board.Projection()
	if len(projection.Claims) != 1 {
		t.Fatalf("claims = %d, want 1", len(projection.Claims))
	}
	claim := projection.Claims[0]
	if got := claims.IssuerAgentID(claim.Relations); got != activationControllerParticipantID {
		t.Fatalf("issuer = %q, want %q", got, activationControllerParticipantID)
	}
	if got := claims.SubjectAgentID(claim.Relations); got != activationControllerParticipantID {
		t.Fatalf("subject = %q, want %q", got, activationControllerParticipantID)
	}
}

func TestInvokeActivationServiceClaimRecordsFailedActivationWithoutReplica(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "activation-service-failed-board", SessionID: "activation-session", TaskID: "task"})
	record := claims.ActivationRecordArtifactData{
		ParticipantID:   "engineer",
		ParticipantType: "agent",
		Tier:            "cold",
		ReplicaCount:    0,
		Ready:           false,
		FailureReason:   errors.New("runtime unavailable").Error(),
		Duration:        time.Millisecond,
	}
	if err := invokeActivationServiceClaim(context.Background(), board, claims.ActivationControllerToolActivate, record); err != nil {
		t.Fatalf("invokeActivationServiceClaim failure record: %v", err)
	}
	assertActivationServiceRecord(t, board, "engineer", false, "runtime unavailable")
}

func TestInvokeActivationServiceClaimPostsTransitionAndQueryRecords(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "activation-service-transition-board", SessionID: "activation-session", TaskID: "task"})
	transition := claims.ActivationRecordArtifactData{
		ParticipantID:   "engineer",
		ParticipantType: "agent",
		Operation:       "tier_transition",
		PreviousTier:    "hot",
		TargetTier:      "warm",
		Tier:            "warm",
		ReplicaCount:    1,
		Ready:           true,
		Duration:        time.Millisecond,
	}
	if err := invokeActivationServiceClaim(context.Background(), board, claims.ActivationControllerToolDeactivate, transition); err != nil {
		t.Fatalf("transition invokeActivationServiceClaim: %v", err)
	}
	query := claims.ActivationRecordArtifactData{
		ParticipantID:   "engineer",
		ParticipantType: "agent",
		Operation:       "query_tier",
		Tier:            "warm",
		TargetTier:      "warm",
		ReplicaCount:    1,
		Ready:           true,
	}
	if err := invokeActivationServiceClaim(context.Background(), board, claims.ActivationControllerToolQueryTier, query); err != nil {
		t.Fatalf("query invokeActivationServiceClaim: %v", err)
	}
	projection := board.Projection()
	if len(projection.Claims) != 2 {
		t.Fatalf("claims = %d, want 2", len(projection.Claims))
	}
	for _, claim := range projection.Claims {
		if claim.ActionType != claims.ActionTypeActivation || claim.LifecycleStatus != claims.ClaimLifecycleSatisfied {
			t.Fatalf("claim = %+v, want satisfied activation service claim", claim)
		}
	}
}

func assertActivationServiceRecord(t *testing.T, board *claims.ClaimsBoard, participantID string, ready bool, reason string) {
	t.Helper()
	projection := board.Projection()
	if len(projection.Claims) != 1 {
		t.Fatalf("claims = %d, want 1", len(projection.Claims))
	}
	if projection.Claims[0].LifecycleStatus != claims.ClaimLifecycleSatisfied {
		t.Fatalf("claim lifecycle = %s, want satisfied", projection.Claims[0].LifecycleStatus)
	}
	if projection.Claims[0].ActionType != claims.ActionTypeActivation {
		t.Fatalf("claim action type = %s, want activation", projection.Claims[0].ActionType)
	}
	testaments := board.TestamentsByClaim(projection.Claims[0].ID)
	if len(testaments) != 1 {
		t.Fatalf("testaments = %d, want 1", len(testaments))
	}
	data, err := claims.ArtifactData[claims.ActivationRecordArtifactData](testaments[0].Artifacts[0])
	if err != nil {
		t.Fatalf("activation artifact data: %v", err)
	}
	if data.ParticipantID != participantID || data.Ready != ready || data.FailureReason != reason {
		t.Fatalf("activation record = %+v, want participant=%s ready=%t reason=%q", data, participantID, ready, reason)
	}
}

func activationControllerTestResolver(t *testing.T, sessionID string) claims.AgentRefResolver {
	t.Helper()
	scope := map[string]string{"session_id": sessionID}
	uid, err := claims.DeriveParticipantUID(claims.ParticipantCategoryService, activationControllerParticipantID, scope)
	if err != nil {
		t.Fatalf("DeriveParticipantUID: %v", err)
	}
	return claims.AgentRefResolverFunc(func(_ context.Context, gotSessionID, agentID string) (claims.AgentRef, bool) {
		if gotSessionID != sessionID || agentID != activationControllerParticipantID {
			return claims.AgentRef{}, false
		}
		return claims.AgentRef{
			UID:        uid,
			Type:       activationControllerParticipantID,
			Name:       "activation_controller",
			Category:   string(claims.ParticipantCategoryService),
			Generation: claims.InitialParticipantGeneration,
			Labels:     scope,
		}.Normalized(), true
	})
}
