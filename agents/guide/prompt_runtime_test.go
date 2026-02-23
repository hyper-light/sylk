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
