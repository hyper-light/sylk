package boot

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
)

func TestOperationsPhase1CommitsDurableLifecycleAndReplaysIdempotently(t *testing.T) {
	cfg := operationsDurableConfig(t)
	db, seq := openOperationsBoard(t, cfg)
	result, err := seq.CommitPhase1(context.Background(), readyPhase1Status(db.Board()))
	if err != nil {
		t.Fatalf("CommitPhase1: %v", err)
	}
	assertClaimSatisfied(t, db.Board(), result.ClaimID)
	assertTestamentValidated(t, db.Board(), result.TestamentID, "boot.phase_1_complete")
	assertProjectionCounts(t, db.Board(), 1, 1)
	if err := db.Close(); err != nil {
		t.Fatalf("close first durable board: %v", err)
	}

	reopened, seq := openOperationsBoard(t, cfg)
	t.Cleanup(func() { _ = reopened.Close() })
	again, err := seq.CommitPhase1(context.Background(), readyPhase1Status(reopened.Board()))
	if err != nil {
		t.Fatalf("CommitPhase1 replay: %v", err)
	}
	if again.ClaimID != result.ClaimID || again.TestamentID != result.TestamentID {
		t.Fatalf("idempotent replay result = (%s,%s), want (%s,%s)", again.ClaimID, again.TestamentID, result.ClaimID, result.TestamentID)
	}
	assertClaimSatisfied(t, reopened.Board(), again.ClaimID)
	assertProjectionCounts(t, reopened.Board(), 1, 1)
}

func TestOperationsPhase1ReadinessFailurePostsFailedTestament(t *testing.T) {
	db, seq := openOperationsBoard(t, operationsDurableConfig(t))
	t.Cleanup(func() { _ = db.Close() })
	result, err := seq.CommitPhase1(context.Background(), Phase1Status{GuideBusOpened: true, WALReplayed: true})
	if !errors.Is(err, ErrBootReadinessIncomplete) {
		t.Fatalf("CommitPhase1 error = %v, want ErrBootReadinessIncomplete", err)
	}
	assertClaimLifecycle(t, db.Board(), result.ClaimID, claims.ClaimLifecycleValidationFailed)
	assertTestamentLifecycle(t, db.Board(), result.TestamentID, claims.TestamentLifecycleValidationFailed, "boot.phase_1_failed")
	assertProjectionCounts(t, db.Board(), 1, 1)
}

func TestOperationsPhase2ActivatesRequiredSystemParticipantsAndCompletes(t *testing.T) {
	db, seq := openOperationsBoard(t, operationsDurableConfig(t))
	t.Cleanup(func() { _ = db.Close() })
	if _, err := seq.CommitPhase1(context.Background(), readyPhase1Status(db.Board())); err != nil {
		t.Fatalf("CommitPhase1: %v", err)
	}
	result, err := seq.CommitPhase2(context.Background(), Phase2Status{Participants: RequiredSystemParticipants()})
	if err != nil {
		t.Fatalf("CommitPhase2: %v", err)
	}
	if got, want := len(result.ParticipantClaimIDs), len(RequiredSystemParticipants()); got != want {
		t.Fatalf("participant claims = %d, want %d", got, want)
	}
	assertClaimSatisfied(t, db.Board(), result.ClaimID)
	assertTestamentValidated(t, db.Board(), result.TestamentID, "boot.phase_2_complete")
	for _, claimID := range result.ParticipantClaimIDs {
		assertActivationClaimSatisfied(t, db.Board(), claimID)
	}
	assertProjectionCounts(t, db.Board(), 5, 5)
}

func TestOperationsPhase2RequiresPhase1Satisfied(t *testing.T) {
	db, seq := openOperationsBoard(t, operationsDurableConfig(t))
	t.Cleanup(func() { _ = db.Close() })
	_, err := seq.CommitPhase2(context.Background(), Phase2Status{Participants: RequiredSystemParticipants()})
	if !errors.Is(err, ErrBootPhaseNotSatisfied) {
		t.Fatalf("CommitPhase2 error = %v, want ErrBootPhaseNotSatisfied", err)
	}
	assertProjectionCounts(t, db.Board(), 0, 0)
}

func TestOperationsPhase2ParticipantFailureFailsPhase(t *testing.T) {
	db, seq := openOperationsBoard(t, operationsDurableConfig(t))
	t.Cleanup(func() { _ = db.Close() })
	if _, err := seq.CommitPhase1(context.Background(), readyPhase1Status(db.Board())); err != nil {
		t.Fatalf("CommitPhase1: %v", err)
	}
	participants := RequiredSystemParticipants()
	participants[len(participants)-1].Ready = false
	result, err := seq.CommitPhase2(context.Background(), Phase2Status{Participants: participants})
	if !errors.Is(err, ErrBootReadinessIncomplete) {
		t.Fatalf("CommitPhase2 error = %v, want ErrBootReadinessIncomplete", err)
	}
	assertClaimLifecycle(t, db.Board(), result.ClaimID, claims.ClaimLifecycleValidationFailed)
	assertTestamentLifecycle(t, db.Board(), result.TestamentID, claims.TestamentLifecycleValidationFailed, "boot.phase_2_failed")
	assertClaimLifecycle(t, db.Board(), result.ParticipantClaimIDs[len(result.ParticipantClaimIDs)-1], claims.ClaimLifecycleValidationFailed)
	assertProjectionCounts(t, db.Board(), 5, 5)
}

func TestOperationsPhase1ConcurrentCallsRemainIdempotent(t *testing.T) {
	db, seq := openOperationsBoard(t, operationsDurableConfig(t))
	t.Cleanup(func() { _ = db.Close() })
	attempts := RequiredSystemParticipants()
	errs := make(chan error, len(attempts))
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := seq.CommitPhase1(context.Background(), readyPhase1Status(db.Board()))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("CommitPhase1 concurrent error: %v", err)
		}
	}
	assertProjectionCounts(t, db.Board(), 1, 1)
}

func operationsDurableConfig(t *testing.T) claims.ClaimsBoardConfig {
	t.Helper()
	return claims.ClaimsBoardConfig{
		BoardID:       "operations-board",
		SessionID:     "operations-session",
		TaskID:        "boot",
		SessionDir:    t.TempDir(),
		DisableOutbox: true,
	}
}

func openOperationsBoard(t *testing.T, cfg claims.ClaimsBoardConfig) (*claims.DurableBoard, *OperationsSequencer) {
	t.Helper()
	db, err := claims.OpenDurableBoard(cfg)
	if err != nil {
		t.Fatalf("OpenDurableBoard: %v", err)
	}
	seq, err := NewOperationsSequencer(OperationsConfig{Board: db.Board(), ProcessUID: "proc-test"})
	if err != nil {
		_ = db.Close()
		t.Fatalf("NewOperationsSequencer: %v", err)
	}
	return db, seq
}

func readyPhase1Status(board *claims.ClaimsBoard) Phase1Status {
	return Phase1Status{
		WALOpened:      true,
		GuideBusOpened: true,
		WALReplayed:    true,
		WALPath:        board.SessionDir(),
		ReplaySequence: board.HighWaterSequence(),
	}
}

func assertProjectionCounts(t *testing.T, board *claims.ClaimsBoard, claimsCount, testamentCount int) {
	t.Helper()
	proj := board.Projection()
	if proj.TotalClaims != claimsCount || proj.TotalTestaments != testamentCount {
		t.Fatalf("projection counts = claims:%d testaments:%d, want claims:%d testaments:%d", proj.TotalClaims, proj.TotalTestaments, claimsCount, testamentCount)
	}
}

func assertActivationClaimSatisfied(t *testing.T, board *claims.ClaimsBoard, claimID string) {
	t.Helper()
	claim := assertClaimLifecycle(t, board, claimID, claims.ClaimLifecycleSatisfied)
	if claim.ActionType != claims.ActionTypeActivation {
		t.Fatalf("claim %s action type = %s, want activation", claimID, claim.ActionType)
	}
	if got := claims.SubjectAgentID(claim.Relations); got != ActivationControllerAgentID {
		t.Fatalf("claim %s subject = %s, want %s", claimID, got, ActivationControllerAgentID)
	}
}

func assertClaimSatisfied(t *testing.T, board *claims.ClaimsBoard, claimID string) {
	t.Helper()
	claim := assertClaimLifecycle(t, board, claimID, claims.ClaimLifecycleSatisfied)
	if claim.Status != claims.ClaimStatusAccepted {
		t.Fatalf("claim %s status = %s, want accepted", claimID, claim.Status)
	}
	for _, validation := range claim.Validations {
		if validation.Status != claims.ValidationStatusPassed {
			t.Fatalf("claim %s validation %s = %s, want passed", claimID, validation.ID, validation.Status)
		}
	}
}

func assertClaimLifecycle(t *testing.T, board *claims.ClaimsBoard, claimID string, want claims.ClaimLifecycleStatus) *claims.Claim {
	t.Helper()
	claim, ok := board.CloneClaim(claimID)
	if !ok {
		t.Fatalf("claim %s not found", claimID)
	}
	if claim.LifecycleStatus != want {
		t.Fatalf("claim %s lifecycle = %s, want %s", claimID, claim.LifecycleStatus, want)
	}
	return claim
}

func assertTestamentValidated(t *testing.T, board *claims.ClaimsBoard, testamentID, summary string) {
	t.Helper()
	assertTestamentLifecycle(t, board, testamentID, claims.TestamentLifecycleValidated, summary)
}

func assertTestamentLifecycle(t *testing.T, board *claims.ClaimsBoard, testamentID string, want claims.TestamentLifecycleStatus, summary string) *claims.Testament {
	t.Helper()
	testament, ok := board.CloneTestament(testamentID)
	if !ok {
		t.Fatalf("testament %s not found", testamentID)
	}
	if testament.LifecycleStatus != want {
		t.Fatalf("testament %s lifecycle = %s, want %s", testamentID, testament.LifecycleStatus, want)
	}
	if testament.Summary != summary {
		t.Fatalf("testament %s summary = %q, want %q", testamentID, testament.Summary, summary)
	}
	return testament
}
