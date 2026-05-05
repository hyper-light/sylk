package toolruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
)

// Guardian-check artifact pair contract (UI_DESIGN.md §2.4 + §4.1).
// Every approval-gated tool emits a guardian_check_started artifact
// onto the caller's accumulator the moment the gate is invoked, and a
// matching guardian_check_completed artifact via Relation{completes}
// the moment the grant returns (any outcome).

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

func TestRecordGuardianCheckStart_EmitsStartedArtifact(t *testing.T) {
	ctx, acc := newAccumulatorOnContext(t)
	trace := recordGuardianCheckStart(ctx, guardianInvocation(), "")
	if trace.startedArtifactID == "" {
		t.Fatal("expected non-empty startedArtifactID")
	}
	arts := acc.Artifacts()
	if len(arts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(arts))
	}
	a := arts[0]
	if a.ID != trace.startedArtifactID {
		t.Fatalf("artifact.ID = %q, want %q", a.ID, trace.startedArtifactID)
	}
	if a.Kind != "guardian_check_started" {
		t.Fatalf("artifact.Kind = %q, want guardian_check_started", a.Kind)
	}
	if a.Reference != "command_execution_control" {
		t.Fatalf("artifact.Reference = %q, want command_execution_control", a.Reference)
	}
}

func TestRecordGuardianCheckEnd_PairsViaCompletesRelation(t *testing.T) {
	ctx, acc := newAccumulatorOnContext(t)
	trace := recordGuardianCheckStart(ctx, guardianInvocation(), "")
	recordGuardianCheckEnd(ctx, guardianInvocation(), trace, time.Now().UTC(), nil)

	arts := acc.Artifacts()
	if len(arts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(arts))
	}
	completed := arts[1]
	if completed.Kind != "guardian_check_completed" {
		t.Fatalf("completed.Kind = %q, want guardian_check_completed", completed.Kind)
	}
	if got := claims.CompletesArtifactID(completed.Relations); got != trace.startedArtifactID {
		t.Fatalf("completes relation = %q, want %q", got, trace.startedArtifactID)
	}
	if got := completed.Metadata["outcome"]; got != "success" {
		t.Fatalf("outcome = %v, want success", got)
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
			ctx, acc := newAccumulatorOnContext(t)
			trace := recordGuardianCheckStart(ctx, guardianInvocation(), "")
			recordGuardianCheckEnd(ctx, guardianInvocation(), trace, time.Now().UTC(), tc.err)
			completed := acc.Artifacts()[1]
			if got := completed.Metadata["outcome"]; got != tc.want {
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
