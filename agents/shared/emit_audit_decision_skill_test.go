package shared

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adalundhe/sylk/core/versioning"
)

func TestEmitAuditDecisionSkill_AcceptedCallsFinalizer(t *testing.T) {
	received := make(chan *AuditMergeResult, 1)
	finalizer := func(r *AuditMergeResult) { received <- r }

	amc := AuditMergeContext{
		SessionID:     "sess-1",
		ReplicaID:     "inspector-global#replica-sess-1:3.7",
		AgentType:     "inspector-global",
		MergedVersion: versioning.SemanticVersion{Major: 3, Minor: 7},
		BaseVersion:   versioning.SemanticVersion{Major: 3, Minor: 6},
	}
	skill := NewEmitAuditDecisionSkill(EmitAuditDecisionSkillConfig{
		ContextResolver: func(ctx context.Context) (AuditMergeContext, bool) { return AuditMergeContextFromContext(ctx) },
	})
	ctx := WithAuditDecisionFinalizer(
		WithAuditMergeContext(context.Background(), amc),
		finalizer,
	)
	input := json.RawMessage(`{"decision": "accepted", "summary": "ok", "concerns": []}`)
	result, err := skill.Handler(ctx, input)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	m := result.(map[string]any)
	if m["emitted"] != true {
		t.Fatal("emitted = false")
	}

	r := <-received
	if r.Decision != versioning.ReplicaDecisionAccepted {
		t.Errorf("decision = %v, want accepted", r.Decision)
	}
	if r.ReplicaID != amc.ReplicaID {
		t.Errorf("replica_id = %q", r.ReplicaID)
	}
	if r.MergedVersion != amc.MergedVersion {
		t.Errorf("merged_version = %v", r.MergedVersion)
	}
}

func TestEmitAuditDecisionSkill_RejectedCarriesConcerns(t *testing.T) {
	received := make(chan *AuditMergeResult, 1)
	amc := AuditMergeContext{
		SessionID:     "sess-1",
		ReplicaID:     "tester-global#replica-sess-1:5.2",
		AgentType:     "tester-global",
		MergedVersion: versioning.SemanticVersion{Major: 5, Minor: 2},
	}
	skill := NewEmitAuditDecisionSkill(EmitAuditDecisionSkillConfig{
		ContextResolver: func(ctx context.Context) (AuditMergeContext, bool) { return AuditMergeContextFromContext(ctx) },
	})
	ctx := WithAuditDecisionFinalizer(
		WithAuditMergeContext(context.Background(), amc),
		func(r *AuditMergeResult) { received <- r },
	)
	input := json.RawMessage(`{"decision": "rejected", "summary": "regression", "concerns": ["dep cycle", "missing test"]}`)
	if _, err := skill.Handler(ctx, input); err != nil {
		t.Fatalf("handler: %v", err)
	}
	r := <-received
	if r.Decision != versioning.ReplicaDecisionRejected {
		t.Errorf("decision = %v", r.Decision)
	}
	if len(r.Concerns) != 2 || r.Concerns[0] != "dep cycle" {
		t.Errorf("concerns = %v", r.Concerns)
	}
}

func TestEmitAuditDecisionSkill_RejectsOutsideAuditScope(t *testing.T) {
	skill := NewEmitAuditDecisionSkill(EmitAuditDecisionSkillConfig{
		ContextResolver: func(ctx context.Context) (AuditMergeContext, bool) { return AuditMergeContextFromContext(ctx) },
	})
	input := json.RawMessage(`{"decision": "accepted", "summary": "x"}`)
	if _, err := skill.Handler(context.Background(), input); err == nil {
		t.Fatal("expected error for call outside audit scope")
	}
}

func TestEmitAuditDecisionSkill_RejectsMissingFinalizer(t *testing.T) {
	amc := AuditMergeContext{ReplicaID: "r"}
	skill := NewEmitAuditDecisionSkill(EmitAuditDecisionSkillConfig{
		ContextResolver: func(ctx context.Context) (AuditMergeContext, bool) { return AuditMergeContextFromContext(ctx) },
	})
	ctx := WithAuditMergeContext(context.Background(), amc)
	input := json.RawMessage(`{"decision": "accepted", "summary": "x"}`)
	if _, err := skill.Handler(ctx, input); err == nil {
		t.Fatal("expected error when finalizer missing from ctx")
	}
}

func TestEmitAuditDecisionSkill_RejectsBadDecision(t *testing.T) {
	amc := AuditMergeContext{ReplicaID: "r"}
	skill := NewEmitAuditDecisionSkill(EmitAuditDecisionSkillConfig{
		ContextResolver: func(ctx context.Context) (AuditMergeContext, bool) { return AuditMergeContextFromContext(ctx) },
	})
	ctx := WithAuditDecisionFinalizer(
		WithAuditMergeContext(context.Background(), amc),
		func(*AuditMergeResult) {},
	)
	input := json.RawMessage(`{"decision": "maybe", "summary": "x"}`)
	if _, err := skill.Handler(ctx, input); err == nil {
		t.Fatal("expected error for bad decision value")
	}
}
