package toolruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
)

// Guardian checks are claim/testament lifecycle rows. They must not emit
// legacy guardian_check_started/guardian_check_completed artifact pairs.

func guardianInvocation() Invocation {
	return Invocation{
		AgentID:         "tester-agent",
		CapabilityScope: "test",
		CorrelationID:   "corr-g",
		ToolCall: providers.ToolCall{
			ID:        "call-g",
			Name:      "command_execution_control",
			Arguments: `{"cmd":"ls"}`,
		},
	}
}

func TestRecordGuardianCheckStart_DoesNotEmitLegacyArtifact(t *testing.T) {
	ctx, acc := newAccumulatorOnContext(t)
	trace := recordGuardianCheckStart(ctx, guardianInvocation(), "")
	if trace.startedArtifactID != "" {
		t.Fatalf("startedArtifactID = %q, want empty legacy artifact ref", trace.startedArtifactID)
	}
	if arts := acc.Artifacts(); len(arts) != 0 {
		t.Fatalf("expected no legacy guardian artifacts, got %d", len(arts))
	}
}

func TestRecordGuardianCheckEnd_PostsGuardianTestament(t *testing.T) {
	ctx, acc, guardianClaimID := guardianLifecycleContext(t)
	trace := recordGuardianCheckStart(ctx, guardianInvocation(), guardianClaimID)
	recordGuardianCheckEnd(ctx, guardianInvocation(), trace, time.Now().UTC(), nil)

	if arts := acc.Artifacts(); len(arts) != 0 {
		t.Fatalf("expected no legacy guardian artifacts, got %d", len(arts))
	}
	testaments := acc.Board().TestamentsByClaim(guardianClaimID)
	if len(testaments) != 1 {
		t.Fatalf("guardian testaments = %d, want 1", len(testaments))
	}
	if testaments[0].LifecycleStatus != claims.TestamentLifecycleValidated {
		t.Fatalf("testament lifecycle = %q, want validated", testaments[0].LifecycleStatus)
	}
}

func TestRecordGuardianCheckEnd_MapsOutcomes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"failure", errors.New("denied"), "failure"},
		{"cancelled", context.Canceled, "cancelled"},
		{"timeout", context.DeadlineExceeded, "timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, acc, guardianClaimID := guardianLifecycleContext(t)
			trace := recordGuardianCheckStart(ctx, guardianInvocation(), guardianClaimID)
			recordGuardianCheckEnd(ctx, guardianInvocation(), trace, time.Now().UTC(), tc.err)
			testaments := acc.Board().TestamentsByClaim(guardianClaimID)
			if len(testaments) != 1 {
				t.Fatalf("guardian testaments = %d, want 1", len(testaments))
			}
			got := ""
			if len(testaments[0].Artifacts) > 0 {
				got, _ = testaments[0].Artifacts[0].Metadata["outcome"].(string)
			}
			if got != tc.want {
				t.Fatalf("outcome = %v, want %q", got, tc.want)
			}
		})
	}
}

func TestRecordGuardianCheck_NoAccumulator_NoOps(t *testing.T) {
	trace := recordGuardianCheckStart(context.Background(), guardianInvocation(), "")
	if trace.startedArtifactID != "" {
		t.Fatalf("expected empty trace, got %+v", trace)
	}
	// must not panic
	recordGuardianCheckEnd(context.Background(), guardianInvocation(), guardianCheckTrace{startedArtifactID: "x"}, time.Now().UTC(), nil)
}

func guardianLifecycleContext(t *testing.T) (context.Context, *claims.TestamentAccumulator, string) {
	t.Helper()
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "guardian-board", SessionID: "test-session", TaskID: "guardian-test"})
	acc := claims.NewTestamentAccumulator("tester-agent", "test-session").WithBoard(board)
	ctx := claims.WithTestamentAccumulator(context.Background(), acc)
	claimID := postGuardianCheckClaim(ctx, guardianInvocation())
	if claimID == "" {
		t.Fatal("guardian claim was not posted")
	}
	return ctx, acc, claimID
}
