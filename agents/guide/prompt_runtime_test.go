package guide

import (
	"strings"
	"testing"
)

func TestBuildClassificationPromptWithRuntime_IncludesConversationContext(t *testing.T) {
	prompt := BuildClassificationPromptWithRuntime("can we continue this", &ClassificationPromptRuntime{
		SessionID:                "sess-1",
		ActiveConversationAgent:  "architect",
		ActiveConversationTurns:  3,
		ActiveConversationAgeSec: 42,
		ActiveConversationScore:  0.88,
	})
	if !strings.Contains(prompt, "Runtime Conversation Context") {
		t.Fatalf("expected runtime context section in prompt")
	}
	if !strings.Contains(prompt, "active_conversation_agent: architect") {
		t.Fatalf("expected active conversation agent in prompt")
	}
}

func TestBuildClassificationPromptWithRuntime_EmptyRuntimeOmitted(t *testing.T) {
	prompt := BuildClassificationPromptWithRuntime("hello", &ClassificationPromptRuntime{})
	if strings.Contains(prompt, "active_conversation_agent:") {
		t.Fatalf("did not expect runtime agent hint for empty runtime")
	}
}

func TestClassificationRuntimeSection_IncludesPendingPlan(t *testing.T) {
	prompt := BuildClassificationPromptWithRuntime("yes", &ClassificationPromptRuntime{
		SessionID:        "sess-2",
		PendingPlanID:    "plan-abc",
		PendingPlanAgent: "architect",
	})
	if !strings.Contains(prompt, "Pending Plan Approval") {
		t.Fatalf("expected Pending Plan Approval section in prompt")
	}
	if !strings.Contains(prompt, "pending_plan_id: plan-abc") {
		t.Fatalf("expected pending_plan_id in prompt")
	}
	if !strings.Contains(prompt, "pending_plan_agent: architect") {
		t.Fatalf("expected pending_plan_agent in prompt")
	}
}
