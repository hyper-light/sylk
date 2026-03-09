package academic

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/providers"
)

type conversationModeProvider struct{}

func (p *conversationModeProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	return &providers.Response{
		Content: "My recommendation is to use a `pyproject.toml`-based package layout and publish wheels.",
		Model:   "gpt-5.4-pro",
	}, nil
}

func TestAcademicProcessForwardedRequest_UserFacingUsesConversationMode(t *testing.T) {
	a, err := New(Config{ID: "academic"}, &conversationModeProvider{})
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	result, err := a.processForwardedRequest(context.Background(), &guide.ForwardedRequest{
		SourceAgentID: "tui",
		Intent:        guide.IntentRecall,
		Input:         "What's an ideal way to build a Python package?",
	})
	if err != nil {
		t.Fatalf("processForwardedRequest: %v", err)
	}

	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("result type = %T, want *ConversationResult", result)
	}
	if conv.Response == "" {
		t.Fatal("expected non-empty conversational response")
	}
}

func TestAcademicProcessForwardedRequest_AgentConsultUsesWorkerMode(t *testing.T) {
	a, err := New(Config{ID: "academic"}, &conversationModeProvider{})
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	result, err := a.processForwardedRequest(context.Background(), &guide.ForwardedRequest{
		SourceAgentID: "architect",
		Intent:        guide.IntentRecall,
		Input:         "Research Python packaging guidance.",
	})
	if err != nil {
		t.Fatalf("processForwardedRequest: %v", err)
	}

	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	if payload["type"] != "recall" {
		t.Fatalf("payload type = %v, want recall", payload["type"])
	}
}

func TestAcademicProcessForwardedRequest_ArchitectHandoffUsesConversationMode(t *testing.T) {
	a, err := New(Config{ID: "academic"}, &conversationModeProvider{})
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	result, err := a.processForwardedRequest(context.Background(), &guide.ForwardedRequest{
		SourceAgentID: "architect",
		Intent:        guide.IntentRecall,
		Input:         "Build our observability platform.",
		Metadata: map[string]any{
			"user_facing_handoff":       true,
			"handoff_kind":              "requirements_clarification",
			"handoff_reason":            "The request is missing scope and operational constraints.",
			"handoff_missing_questions": []string{"Target services", "Retention requirements"},
		},
	})
	if err != nil {
		t.Fatalf("processForwardedRequest: %v", err)
	}

	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("result type = %T, want *ConversationResult", result)
	}
	if conv.Response == "" {
		t.Fatal("expected non-empty conversational response")
	}
	for _, tool := range a.buildToolDefinitions() {
		if tool.Name == "reroute_request" {
			t.Fatal("request-scoped reroute tool leaked into shared academic tool definitions")
		}
	}
}
