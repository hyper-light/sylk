package academic

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/providers"
)

type conversationModeProvider struct {
	lastReq *providers.Request
}

func (p *conversationModeProvider) Complete(_ context.Context, req *providers.Request) (*providers.Response, error) {
	p.lastReq = req
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
	provider := &conversationModeProvider{}
	a, err := New(Config{ID: "academic"}, provider)
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
	if got := strings.TrimSpace(fmt.Sprint(payload["type"])); got != "recall" {
		t.Fatalf("result type field = %q, want recall", got)
	}
	if provider.lastReq == nil {
		t.Fatal("expected provider request to be captured")
	}
	prompt := provider.lastReq.Messages[0].Content
	for _, needle := range []string{
		"Use an explicit research workflow.",
		"use `web_search` to discover authoritative candidates",
		"`web_fetch`, `fetch_document`, or a bounded `crawl_links` call",
		"Consult the Librarian",
		"Architect planning",
		"`author_research_paper`",
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("worker recall prompt missing %q:\n%s", needle, prompt)
		}
	}
	if !strings.Contains(prompt, "Request:\nResearch Python packaging guidance.") {
		t.Fatalf("worker recall prompt did not include original request:\n%s", prompt)
	}
}

func TestAcademicProcessForwardedRequest_ArchitectConsultExposesResearchPaperTool(t *testing.T) {
	provider := &conversationModeProvider{}
	a, err := New(Config{ID: "academic"}, provider)
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
	if _, ok := result.(map[string]any); !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	if provider.lastReq == nil {
		t.Fatal("expected provider request to be captured")
	}

	foundPaper := false
	for _, tool := range provider.lastReq.Tools {
		if tool.Name == "author_research_paper" {
			foundPaper = true
			break
		}
	}
	if !foundPaper {
		t.Fatalf("expected architect consult surface to include author_research_paper, got %#v", provider.lastReq.Tools)
	}

	prompt := provider.lastReq.Messages[0].Content
	for _, needle := range []string{
		"Prefer durable, cited output",
		"`author_research_paper`",
		"`clone_via_librarian`",
		"`crawl_links`",
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("worker recall prompt missing architect guidance %q:\n%s", needle, prompt)
		}
	}
}

func TestAcademicProcessForwardedRequest_CustomArchitectIDExposesResearchPaperTool(t *testing.T) {
	provider := &conversationModeProvider{}
	a, err := New(Config{ID: "academic"}, provider)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	result, err := a.processForwardedRequest(context.Background(), &guide.ForwardedRequest{
		SourceAgentID:   "architect-primary",
		SourceAgentName: "Architect",
		Intent:          guide.IntentRecall,
		Input:           "Research Python packaging guidance.",
	})
	if err != nil {
		t.Fatalf("processForwardedRequest: %v", err)
	}
	if _, ok := result.(map[string]any); !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	if provider.lastReq == nil {
		t.Fatal("expected provider request to be captured")
	}

	foundPaper := false
	for _, tool := range provider.lastReq.Tools {
		if tool.Name == "author_research_paper" {
			foundPaper = true
			break
		}
	}
	if !foundPaper {
		t.Fatalf("expected author_research_paper for custom architect id, got %#v", provider.lastReq.Tools)
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

func TestAcademicProcessForwardedRequest_UserFacingConversationPromptEncouragesWebSearch(t *testing.T) {
	provider := &conversationModeProvider{}
	a, err := New(Config{ID: "academic"}, provider)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	result, err := a.processForwardedRequest(context.Background(), &guide.ForwardedRequest{
		SourceAgentID: "tui",
		Intent:        guide.IntentRecall,
		Domain:        guide.DomainResearch,
		Input:         "What is the current recommended way to install Playwright in Python?",
	})
	if err != nil {
		t.Fatalf("processForwardedRequest: %v", err)
	}

	if _, ok := result.(*ConversationResult); !ok {
		t.Fatalf("result type = %T, want *ConversationResult", result)
	}
	if provider.lastReq == nil {
		t.Fatal("expected provider request to be captured")
	}
	systemPrompt := provider.lastReq.SystemPrompt
	for _, needle := range []string{
		"use `web_search` to discover authoritative candidates",
		"`web_fetch`, `fetch_document`, or a bounded `crawl_links` call",
		"Do not answer from memory alone",
		"Consult the Librarian",
	} {
		if !strings.Contains(systemPrompt, needle) {
			t.Fatalf("conversation system prompt missing %q:\n%s", needle, systemPrompt)
		}
	}
}
