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
	if err := invokeActivationServiceClaim(context.Background(), board, record); err != nil {
		t.Fatalf("invokeActivationServiceClaim: %v", err)
	}
	assertActivationServiceRecord(t, board, "guide", true, "")
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
	if err := invokeActivationServiceClaim(context.Background(), board, record); err != nil {
		t.Fatalf("invokeActivationServiceClaim failure record: %v", err)
	}
	assertActivationServiceRecord(t, board, "engineer", false, "runtime unavailable")
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
