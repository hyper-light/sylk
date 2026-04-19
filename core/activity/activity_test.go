package activity

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNewID_Unique(t *testing.T) {
	const n = 10000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := NewID()
		if id == "" {
			t.Fatalf("empty ID at iteration %d", i)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID %q at iteration %d", id, i)
		}
		seen[id] = struct{}{}
	}
}

func TestNewID_Sortable(t *testing.T) {
	prev := ""
	for i := 0; i < 1000; i++ {
		cur := NewID()
		if cur <= prev {
			t.Fatalf("ID not monotonically increasing at iteration %d: prev=%s cur=%s", i, prev, cur)
		}
		prev = cur
	}
}

func TestEnsureFabricContext_MintsRoot(t *testing.T) {
	ctx := context.Background()
	if fc := FabricContextFromContext(ctx); fc.TraceID != "" {
		t.Fatalf("bare context should not carry a FabricContext; got TraceID=%s", fc.TraceID)
	}
	ctx = EnsureFabricContext(ctx)
	fc := FabricContextFromContext(ctx)
	if fc.TraceID == "" {
		t.Fatal("EnsureFabricContext must mint a TraceID")
	}
}

func TestEnsureFabricContext_PreservesExisting(t *testing.T) {
	ctx := WithFabricContext(context.Background(), FabricContext{TraceID: "preexisting"})
	ctx = EnsureFabricContext(ctx)
	if got := FabricContextFromContext(ctx).TraceID; got != "preexisting" {
		t.Fatalf("EnsureFabricContext clobbered existing trace; got %q", got)
	}
}

func TestWithChildSpan_BuildsCausalChain(t *testing.T) {
	ctx := EnsureFabricContext(context.Background())
	ctx, child := WithChildSpan(ctx)
	if child.ParentSpanID != "" {
		t.Fatalf("first child should have empty parent SpanID (root has none); got %s", child.ParentSpanID)
	}
	if child.SpanID == "" {
		t.Fatal("child must have its own SpanID")
	}
	_, grandchild := WithChildSpan(ctx)
	if grandchild.ParentSpanID != child.SpanID {
		t.Fatalf("grandchild parent (%s) must be child SpanID (%s)", grandchild.ParentSpanID, child.SpanID)
	}
	if len(grandchild.CausalChain) != 1 || grandchild.CausalChain[0] != child.SpanID {
		t.Fatalf("grandchild causal chain should contain child SpanID; got %v", grandchild.CausalChain)
	}
}

func TestSpan_PointKind_EmitsOnce(t *testing.T) {
	col := NewTestCollector()
	prev := SetDefaultSink(col)
	defer SetDefaultSink(prev)

	ctx := EnsureFabricContext(context.Background())
	span := StartSpan(ctx, ActionFileRead, Subject{PathPrefix: "main.go"})
	span.End()

	got := col.Snapshot()
	if len(got) != 1 {
		t.Fatalf("point-kind span should emit exactly one activity; got %d", len(got))
	}
	if got[0].Action != ActionFileRead {
		t.Fatalf("activity kind = %q, want %q", got[0].Action, ActionFileRead)
	}
	if got[0].State != StatePoint {
		t.Fatalf("activity state = %q, want %q", got[0].State, StatePoint)
	}
	if got[0].Resolution != ResolutionAtomic {
		t.Fatalf("activity resolution = %q, want %q", got[0].Resolution, ResolutionAtomic)
	}
}

func TestSpan_PairedKind_EmitsStartAndEnd(t *testing.T) {
	col := NewTestCollector()
	prev := SetDefaultSink(col)
	defer SetDefaultSink(prev)

	ctx := EnsureFabricContext(context.Background())
	span := StartSpan(ctx, ActionLLMRequestEmitted, Subject{Domain: "model.test"})
	span.End()

	got := col.Snapshot()
	if len(got) != 2 {
		t.Fatalf("paired-kind span should emit two activities; got %d", len(got))
	}
	if got[0].Action != ActionLLMRequestEmitted || got[0].State != StateInFlight {
		t.Fatalf("first activity should be in-flight LLMRequestEmitted; got kind=%q state=%q", got[0].Action, got[0].State)
	}
	if got[1].Action != ActionLLMResponseCompleted || got[1].State != StateResolved {
		t.Fatalf("second activity should be resolved LLMResponseCompleted; got kind=%q state=%q", got[1].Action, got[1].State)
	}
	if got[1].Resolves == nil || *got[1].Resolves != got[0].ID {
		t.Fatal("second activity must Resolve the first by ID")
	}
}

func TestSpan_End_Idempotent(t *testing.T) {
	col := NewTestCollector()
	prev := SetDefaultSink(col)
	defer SetDefaultSink(prev)

	ctx := EnsureFabricContext(context.Background())
	span := StartSpan(ctx, ActionFileRead, Subject{PathPrefix: "main.go"})
	span.End()
	span.End()
	span.EndWithError(errors.New("late error"))

	if got := col.Count(); got != 1 {
		t.Fatalf("repeated End calls should be no-ops; got %d activities", got)
	}
}

func TestSpan_StoreWriteFailed_ChangesKindOnError(t *testing.T) {
	col := NewTestCollector()
	prev := SetDefaultSink(col)
	defer SetDefaultSink(prev)

	ctx := EnsureFabricContext(context.Background())
	span := StartSpan(ctx, ActionStoreWriteCommitted, Subject{TargetArtifact: "decision_manifest"})
	span.EndWithError(errors.New("locked"))

	got := col.Snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d activities", len(got))
	}
	if got[0].Action != ActionStoreWriteFailed {
		t.Fatalf("error path should remap to ActionStoreWriteFailed; got %q", got[0].Action)
	}
	if !strings.Contains(string(got[0].Payload), "locked") {
		t.Fatalf("error payload should embed message; got %s", string(got[0].Payload))
	}
}

func TestResolutionFor_AllInfraKindsCovered(t *testing.T) {
	// Every documented infrastructural kind must have a non-Medium
	// resolution mapped (Medium is the safe-default fallback for
	// unknown kinds, so an infra kind landing there indicates the
	// mapping was forgotten).
	expectedFine := []ActionKind{
		ActionStoreWriteCommitted, ActionStoreWriteFailed,
		ActionBusMessageDelivered, ActionFileWritten, ActionFileDeleted,
		ActionCommandExecuted, ActionCommandApproved, ActionCommandDenied,
		ActionToolCallStarted, ActionToolCallCompleted,
		ActionGoroutineStarted, ActionGoroutineStopped,
		ActionAgentPromoted, ActionAgentDemoted, ActionErrorEmitted,
	}
	for _, kind := range expectedFine {
		if got := ResolutionFor(kind); got != ResolutionFine {
			t.Errorf("%s: expected Fine, got %s", kind, got)
		}
	}

	expectedAtomic := []ActionKind{
		ActionLLMChunkReceived, ActionFileRead, ActionCacheHit, ActionCacheMiss,
	}
	for _, kind := range expectedAtomic {
		if got := ResolutionFor(kind); got != ResolutionAtomic {
			t.Errorf("%s: expected Atomic, got %s", kind, got)
		}
	}
}

func TestEmissionCount_Increments(t *testing.T) {
	prev := SetDefaultSink(NewTestCollector())
	defer SetDefaultSink(prev)

	before := EmissionCount()
	ctx := EnsureFabricContext(context.Background())
	span := StartSpan(ctx, ActionFileRead, Subject{})
	span.End()
	after := EmissionCount()
	if after-before == 0 {
		t.Fatal("EmissionCount should advance after a span emission")
	}
}
