package providers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

// LLM dispatch artifact pair contract — same shape as tool-call:
// Kind="llm_started" + matching Kind="llm_completed" with
// Relation{completes} pointing at the started artifact's ID, plus
// outcome (success/failure/timeout/cancelled). UI_DESIGN.md §2.4.

func newAccumulatorOnContextLLM(t *testing.T) (context.Context, *claims.TestamentAccumulator) {
	t.Helper()
	acc := claims.NewTestamentAccumulator("tester-agent", "test-session")
	ctx := claims.WithTestamentAccumulator(context.Background(), acc)
	return ctx, acc
}

func sampleRequest() *Request {
	return &Request{Model: "claude-opus-4-7", Messages: []Message{{Role: RoleUser, Content: "hi"}}, MaxTokens: 1024}
}

func TestRecordLLMDispatchStart_EmitsStartedArtifactWithStableID(t *testing.T) {
	ctx, acc := newAccumulatorOnContextLLM(t)
	trace := recordLLMDispatchStart(ctx, "anthropic", sampleRequest(), "complete")
	if trace.startedArtifactID == "" {
		t.Fatal("recordLLMDispatchStart returned empty startedArtifactID")
	}
	if trace.dispatchID == "" {
		t.Fatal("recordLLMDispatchStart returned empty dispatchID")
	}
	arts := acc.Artifacts()
	if len(arts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(arts))
	}
	a := arts[0]
	if a.ID != trace.startedArtifactID {
		t.Fatalf("artifact.ID = %q, want %q", a.ID, trace.startedArtifactID)
	}
	if a.Kind != "llm_started" {
		t.Fatalf("artifact.Kind = %q, want llm_started", a.Kind)
	}
}

func TestRecordLLMDispatchEnd_PairsViaCompletesRelation(t *testing.T) {
	ctx, acc := newAccumulatorOnContextLLM(t)
	trace := recordLLMDispatchStart(ctx, "anthropic", sampleRequest(), "complete")
	resp := &Response{Content: "ok", StopReason: StopReasonEndTurn}
	recordLLMDispatchEnd(ctx, "anthropic", sampleRequest(), "complete", trace, resp, nil)

	arts := acc.Artifacts()
	if len(arts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(arts))
	}
	completed := arts[1]
	if completed.Kind != "llm_completed" {
		t.Fatalf("completed.Kind = %q, want llm_completed", completed.Kind)
	}
	if got := claims.CompletesArtifactID(completed.Relations); got != trace.startedArtifactID {
		t.Fatalf("completes relation = %q, want %q", got, trace.startedArtifactID)
	}
	if got := completed.Metadata["outcome"]; got != "success" {
		t.Fatalf("outcome = %v, want success", got)
	}
}

func TestRecordLLMDispatchEnd_MapsOutcomes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"failure", errors.New("boom"), "failure"},
		{"cancelled", context.Canceled, "cancelled"},
		{"timeout", context.DeadlineExceeded, "timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, acc := newAccumulatorOnContextLLM(t)
			trace := recordLLMDispatchStart(ctx, "anthropic", sampleRequest(), "complete")
			recordLLMDispatchEnd(ctx, "anthropic", sampleRequest(), "complete", trace, nil, tc.err)
			completed := acc.Artifacts()[1]
			if got := completed.Metadata["outcome"]; got != tc.want {
				t.Fatalf("outcome = %v, want %q", got, tc.want)
			}
		})
	}
}

func TestRecordLLMDispatch_NoAccumulator_NoOps(t *testing.T) {
	trace := recordLLMDispatchStart(context.Background(), "anthropic", sampleRequest(), "complete")
	if trace.startedArtifactID != "" || trace.dispatchID != "" {
		t.Fatalf("expected empty trace, got %+v", trace)
	}
	// must not panic
	recordLLMDispatchEnd(context.Background(), "anthropic", sampleRequest(), "complete", llmDispatchTrace{dispatchID: "x"}, nil, nil)
}

func TestRecordLLMDispatch_ConcurrentInvocationsProduceMatchedPairs(t *testing.T) {
	ctx, acc := newAccumulatorOnContextLLM(t)
	const N = 32
	done := make(chan struct{}, N)
	for i := 0; i < N; i++ {
		go func() {
			trace := recordLLMDispatchStart(ctx, "anthropic", sampleRequest(), "complete")
			recordLLMDispatchEnd(ctx, "anthropic", sampleRequest(), "complete", trace, &Response{Content: "ok"}, nil)
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
		case "llm_started":
			starts[a.ID] = struct{}{}
		case "llm_completed":
			rel := claims.CompletesArtifactID(a.Relations)
			if rel == "" {
				t.Fatalf("llm_completed missing completes relation: %+v", a)
			}
			completes[rel] = struct{}{}
		}
	}
	if len(starts) != N || len(completes) != N {
		t.Fatalf("starts=%d completes=%d, want %d each", len(starts), len(completes), N)
	}
	for id := range completes {
		if _, ok := starts[id]; !ok {
			t.Fatalf("completion references unknown started artifact %q", id)
		}
	}
}

// Ensure benchmark stays cheap — rough budget check.
func BenchmarkRecordToolCallPair(b *testing.B) {
	acc := claims.NewTestamentAccumulator("tester-agent", "test-session")
	ctx := claims.WithTestamentAccumulator(context.Background(), acc)
	now := time.Now().UTC()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		trace := recordLLMDispatchStart(ctx, "anthropic", sampleRequest(), "complete")
		recordLLMDispatchEnd(ctx, "anthropic", sampleRequest(), "complete", trace, &Response{Content: "ok"}, nil)
	}
	_ = now
}
