package guide

import (
	"testing"
	"time"
)

func TestApplyPendingPlanMetadata_ExecuteAttachesAndClears(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.SetPendingPlan("s1", &ResponseDirective{
		Phase:   PhasePlanApproval,
		AgentID: "architect",
		Metadata: map[string]any{
			"plan_id": "plan-abc",
			"epoch":   uint64(42),
		},
		TTL: 5 * time.Minute,
	})

	g := &Guide{conversation: flow}
	classification := &RouteResult{
		Intent:      IntentExecute,
		Domain:      DomainPlanning,
		TargetAgent: TargetAgent("architect"),
	}

	g.applyPendingPlanMetadata("s1", classification)

	if classification.PhaseMetadata == nil {
		t.Fatal("expected PhaseMetadata to be set")
	}
	if classification.PhaseMetadata["plan_id"] != "plan-abc" {
		t.Fatalf("plan_id = %v, want plan-abc", classification.PhaseMetadata["plan_id"])
	}
	if classification.PhaseMetadata["epoch"] != uint64(42) {
		t.Fatalf("epoch = %v, want 42", classification.PhaseMetadata["epoch"])
	}
	if flow.PendingPlan("s1") != nil {
		t.Fatal("expected pending plan to be cleared after execute")
	}
}

func TestApplyPendingPlanMetadata_PlanFeedbackAttachesAndClears(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.SetPendingPlan("s1", &ResponseDirective{
		Phase:   PhasePlanApproval,
		AgentID: "architect",
		Metadata: map[string]any{
			"plan_id": "plan-xyz",
			"epoch":   uint64(7),
		},
		TTL: 5 * time.Minute,
	})

	g := &Guide{conversation: flow}
	classification := &RouteResult{
		Intent:      IntentPlan,
		Domain:      DomainPlanning,
		TargetAgent: TargetAgent("architect"),
	}

	g.applyPendingPlanMetadata("s1", classification)

	if classification.PhaseMetadata == nil {
		t.Fatal("expected PhaseMetadata to be set for plan feedback")
	}
	if classification.PhaseMetadata["plan_id"] != "plan-xyz" {
		t.Fatalf("plan_id = %v, want plan-xyz", classification.PhaseMetadata["plan_id"])
	}
	if flow.PendingPlan("s1") != nil {
		t.Fatal("expected pending plan to be cleared after feedback")
	}
}

func TestApplyPendingPlanMetadata_DifferentTargetLeavesInPlace(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.SetPendingPlan("s1", &ResponseDirective{
		Phase:   PhasePlanApproval,
		AgentID: "architect",
		Metadata: map[string]any{
			"plan_id": "plan-keep",
			"epoch":   uint64(1),
		},
		TTL: 5 * time.Minute,
	})

	g := &Guide{conversation: flow}
	classification := &RouteResult{
		Intent:      IntentExecute,
		Domain:      DomainPlanning,
		TargetAgent: TargetAgent("librarian"),
	}

	g.applyPendingPlanMetadata("s1", classification)

	if classification.PhaseMetadata != nil {
		t.Fatal("expected PhaseMetadata to be nil when target differs")
	}
	if flow.PendingPlan("s1") == nil {
		t.Fatal("expected pending plan to remain when target differs")
	}
}

func TestApplyPendingPlanMetadata_NoPendingNoop(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	g := &Guide{conversation: flow}
	classification := &RouteResult{
		Intent:      IntentExecute,
		Domain:      DomainPlanning,
		TargetAgent: TargetAgent("architect"),
	}

	g.applyPendingPlanMetadata("s1", classification)

	if classification.PhaseMetadata != nil {
		t.Fatal("expected PhaseMetadata to be nil when no pending plan")
	}
}

func TestApplyPendingPlanMetadata_OffTopicLeavesInPlace(t *testing.T) {
	flow := NewConversationFlowManager(ConversationFlowConfig{})
	flow.SetPendingPlan("s1", &ResponseDirective{
		Phase:   PhasePlanApproval,
		AgentID: "architect",
		Metadata: map[string]any{
			"plan_id": "plan-stay",
			"epoch":   uint64(3),
		},
		TTL: 5 * time.Minute,
	})

	g := &Guide{conversation: flow}
	classification := &RouteResult{
		Intent:      IntentChat,
		Domain:      DomainGeneral,
		TargetAgent: TargetAgent("guide"),
	}

	g.applyPendingPlanMetadata("s1", classification)

	if classification.PhaseMetadata != nil {
		t.Fatal("expected PhaseMetadata to be nil for off-topic")
	}
	if flow.PendingPlan("s1") == nil {
		t.Fatal("expected pending plan to remain for off-topic")
	}
}
