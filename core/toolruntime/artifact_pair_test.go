package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
)

// These tests exercise the centralized tool-call artifact pair
// contract from UI_DESIGN.md §2.4 + §4.1 — every tool invocation
// emits a started artifact (Kind="tool_started") and, on every
// termination path, a completed artifact (Kind="tool_completed")
// carrying Relation{completes} pointing at the started artifact's
// stable ID.

func newAccumulatorOnContext(t *testing.T) (context.Context, *claims.TestamentAccumulator) {
	t.Helper()
	acc := claims.NewTestamentAccumulator("tester-agent", "test-session")
	ctx := claims.WithTestamentAccumulator(context.Background(), acc)
	return ctx, acc
}

func toolInvocation(name string) Invocation {
	return Invocation{
		AgentID:         "tester-agent",
		CapabilityScope: "test",
		CorrelationID:   "corr-1",
		ToolCall: providers.ToolCall{
			ID:        "call-1",
			Name:      name,
			Arguments: `{"foo":"bar"}`,
		},
	}
}

func TestRecordToolCallStart_EmitsStartedArtifactWithStableID(t *testing.T) {
	ctx, acc := newAccumulatorOnContext(t)
	trace := recordToolCallStart(ctx, toolInvocation("read_file"), "tester-agent")
	if trace.startedArtifactID == "" {
		t.Fatal("recordToolCallStart returned empty startedArtifactID")
	}
	if trace.callID == "" {
		t.Fatal("recordToolCallStart returned empty callID")
	}
	arts := acc.Artifacts()
	if len(arts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(arts))
	}
	a := arts[0]
	if a.ID != trace.startedArtifactID {
		t.Fatalf("artifact.ID = %q, want %q", a.ID, trace.startedArtifactID)
	}
	if a.Kind != "tool_started" {
		t.Fatalf("artifact.Kind = %q, want tool_started", a.Kind)
	}
	if a.Reference != "read_file" {
		t.Fatalf("artifact.Reference = %q, want read_file", a.Reference)
	}
	if got := a.Metadata["call_id"]; got != trace.callID {
		t.Fatalf("metadata.call_id = %v, want %q", got, trace.callID)
	}
}

func TestRecordToolCallEnd_PairsViaCompletesRelation(t *testing.T) {
	ctx, acc := newAccumulatorOnContext(t)
	trace := recordToolCallStart(ctx, toolInvocation("read_file"), "tester-agent")
	recordToolCallEnd(ctx, "tester-agent", trace, "read_file", map[string]any{"ok": true}, nil, time.Now().UTC())

	arts := acc.Artifacts()
	if len(arts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(arts))
	}
	completed := arts[1]
	if completed.Kind != "tool_completed" {
		t.Fatalf("completed.Kind = %q, want tool_completed", completed.Kind)
	}
	if got := claims.CompletesArtifactID(completed.Relations); got != trace.startedArtifactID {
		t.Fatalf("completes relation = %q, want %q", got, trace.startedArtifactID)
	}
	if got := completed.Metadata["outcome"]; got != "success" {
		t.Fatalf("outcome = %v, want success", got)
	}
	data, err := claims.ArtifactData[claims.ToolRuntimeExecutionArtifactData](completed)
	if err != nil {
		t.Fatalf("typed tool execution data: %v", err)
	}
	if data.ToolName != "read_file" || data.Status != claims.InfrastructureStatusOK || data.OutputSummary == "" {
		t.Fatalf("typed execution data = %+v, want successful read_file summary", data)
	}
}

func TestRecordToolCall_RedactsArgsAndBoundsTypedOutput(t *testing.T) {
	ctx, acc := newAccumulatorOnContext(t)
	inv := toolInvocation("deploy")
	inv.ToolCall.Arguments = `{"token":"secret","path":"./cmd"}`
	trace := recordToolCallStart(ctx, inv, "tester-agent")
	started := acc.Artifacts()[0]
	args, ok := started.Metadata["args"].(map[string]any)
	if !ok {
		t.Fatalf("started args metadata = %#v, want map", started.Metadata["args"])
	}
	if args["token"] != "[redacted]" || args["path"] != "./cmd" {
		t.Fatalf("redacted args = %+v, want token redacted and path retained", args)
	}

	recordToolCallEnd(ctx, "tester-agent", trace, "deploy", strings.Repeat("x", toolCallOutputSummaryLimit+len("x")), nil, time.Now().UTC())
	completed := acc.Artifacts()[1]
	data, err := claims.ArtifactData[claims.ToolRuntimeExecutionArtifactData](completed)
	if err != nil {
		t.Fatalf("typed execution data: %v", err)
	}
	if data.RedactedArgs["token"] != "[redacted]" || !data.ContentTruncated {
		t.Fatalf("typed data = %+v, want redacted truncated output", data)
	}
}

func TestExecuteRaw_ToolOutcomeYieldedSkipsCompletedArtifact(t *testing.T) {
	registry := skills.NewRegistry()
	mustRegisterSkill(t, registry, skills.NewSkill("consult_peer").
		Description("Consult a peer").
		Handler(func(context.Context, json.RawMessage) (any, error) {
			return skills.YieldToolOutcome(&skills.YieldContinuation{
				Kind:       "consult",
				AwaitedIDs: []string{"consult-1"},
				ToolCallID: "call-1",
				ToolName:   "consult_peer",
			}), nil
		}).
		Build())
	rt := mustNewRuntime(t, registry, NewManifest("tester-agent", "test",
		NewToolPolicy("consult_peer", EffectReadOnly, DomainConsultation, ExecutionModeLocal, WithVisibleByDefault()),
	))
	ctx, acc := newAccumulatorOnContext(t)

	result, err := rt.ExecuteRaw(ctx, toolInvocation("consult_peer"))
	if err != nil {
		t.Fatalf("ExecuteRaw returned error for yielded tool: %v", err)
	}
	if !result.Yielded() {
		t.Fatalf("result status = %q, want yielded", result.Status)
	}
	arts := acc.Artifacts()
	if len(arts) != 1 {
		t.Fatalf("yielded tool should emit only tool_started, got %d artifacts: %#v", len(arts), arts)
	}
	if arts[0].Kind != "tool_started" {
		t.Fatalf("artifact kind = %q, want tool_started", arts[0].Kind)
	}
}

func TestRecordToolCallEnd_MapsErrorToOutcomeFailure(t *testing.T) {
	ctx, acc := newAccumulatorOnContext(t)
	trace := recordToolCallStart(ctx, toolInvocation("write_file"), "tester-agent")
	recordToolCallEnd(ctx, "tester-agent", trace, "write_file", nil, errors.New("disk full"), time.Now().UTC())

	completed := acc.Artifacts()[1]
	if got := completed.Metadata["outcome"]; got != "failure" {
		t.Fatalf("outcome = %v, want failure", got)
	}
	if got := completed.Metadata["error"]; got != "disk full" {
		t.Fatalf("error metadata = %v, want disk full", got)
	}
}

func TestRecordToolCallEnd_MapsContextCanceledToOutcomeCancelled(t *testing.T) {
	ctx, acc := newAccumulatorOnContext(t)
	trace := recordToolCallStart(ctx, toolInvocation("slow_tool"), "tester-agent")
	recordToolCallEnd(ctx, "tester-agent", trace, "slow_tool", nil, context.Canceled, time.Now().UTC())

	completed := acc.Artifacts()[1]
	if got := completed.Metadata["outcome"]; got != "cancelled" {
		t.Fatalf("outcome = %v, want cancelled", got)
	}
}

func TestRecordToolCallEnd_MapsDeadlineExceededToOutcomeTimeout(t *testing.T) {
	ctx, acc := newAccumulatorOnContext(t)
	trace := recordToolCallStart(ctx, toolInvocation("slow_tool"), "tester-agent")
	recordToolCallEnd(ctx, "tester-agent", trace, "slow_tool", nil, context.DeadlineExceeded, time.Now().UTC())

	completed := acc.Artifacts()[1]
	if got := completed.Metadata["outcome"]; got != "timeout" {
		t.Fatalf("outcome = %v, want timeout", got)
	}
}

func TestRecordToolCallStart_NoAccumulator_ReturnsEmptyTrace(t *testing.T) {
	trace := recordToolCallStart(context.Background(), toolInvocation("read_file"), "tester-agent")
	if trace.startedArtifactID != "" || trace.callID != "" {
		t.Fatalf("expected empty trace when accumulator absent, got %+v", trace)
	}
}

func TestRecordToolCallEnd_NoAccumulator_NoOps(t *testing.T) {
	// Should not panic.
	recordToolCallEnd(context.Background(), "tester-agent", toolCallTrace{startedArtifactID: "x", callID: "y"}, "tool", nil, nil, time.Now().UTC())
}

// Concurrency: many tool wraps against one accumulator must produce
// matched pairs without races. Run under -race.
func TestRecordToolCall_ConcurrentInvocationsProduceMatchedPairs(t *testing.T) {
	ctx, acc := newAccumulatorOnContext(t)
	const N = 64
	done := make(chan struct{}, N)
	for i := 0; i < N; i++ {
		go func() {
			trace := recordToolCallStart(ctx, toolInvocation("p"), "tester-agent")
			recordToolCallEnd(ctx, "tester-agent", trace, "p", "ok", nil, time.Now().UTC())
			done <- struct{}{}
		}()
	}
	for i := 0; i < N; i++ {
		<-done
	}
	arts := acc.Artifacts()
	if len(arts) != N*2 {
		t.Fatalf("expected %d artifacts, got %d", N*2, len(arts))
	}
	starts := map[string]struct{}{}
	completes := map[string]struct{}{}
	for _, a := range arts {
		switch a.Kind {
		case "tool_started":
			starts[a.ID] = struct{}{}
		case "tool_completed":
			rel := claims.CompletesArtifactID(a.Relations)
			if rel == "" {
				t.Fatalf("tool_completed missing completes relation: %+v", a)
			}
			completes[rel] = struct{}{}
		default:
			t.Fatalf("unexpected kind %q", a.Kind)
		}
	}
	if len(starts) != N || len(completes) != N {
		t.Fatalf("starts=%d completes=%d, both want %d", len(starts), len(completes), N)
	}
	for id := range completes {
		if _, ok := starts[id]; !ok {
			t.Fatalf("completion references unknown started artifact %q", id)
		}
	}
}
