package academic

import (
	"context"
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

type conversationSearchOnlyProvider struct{}

func (p *conversationSearchOnlyProvider) Complete(_ context.Context, req *providers.Request) (*providers.Response, error) {
	_ = req
	return &providers.Response{
		Content: "Typer is the best choice.",
		Model:   "gpt-5.4-pro",
		ProviderMetadata: map[string]any{
			providers.ProviderMetadataNativeWebSearchCallsKey: []providers.NativeWebSearchCall{
				{ID: "ws_1", Provider: "openai", Action: "search", Query: "best python cli libraries 2025"},
			},
		},
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

func TestAcademicProcessForwardedRequest_AgentConsultUsesConsultationMode(t *testing.T) {
	provider := &conversationModeProvider{}
	a, err := New(Config{ID: "academic"}, provider)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	result, err := a.processForwardedRequest(context.Background(), &guide.ForwardedRequest{
		SourceAgentID: "engineer",
		Intent:        guide.IntentRecall,
		Input:         "Research Python packaging guidance.",
	})
	if err != nil {
		t.Fatalf("processForwardedRequest: %v", err)
	}
	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("result type = %T, want *ConversationResult", result)
	}
	if conv.Response == "" {
		t.Fatal("expected non-empty consultation response")
	}
	if provider.lastReq == nil {
		t.Fatal("expected provider request to be captured")
	}
	if got := strings.TrimSpace(provider.lastReq.Messages[0].Content); got != "Research Python packaging guidance." {
		t.Fatalf("consultation query = %q, want original forwarded input", got)
	}
	systemPrompt := provider.lastReq.SystemPrompt
	for _, needle := range []string{
		"non-user-facing consultation",
		"Use an explicit research workflow.",
		"use `web_search` to discover authoritative candidates",
		"Provider-native `web_search` is not just a title/snippet lookup",
		"decisive surfaced exact URL coming out of native `web_search`",
	} {
		if !strings.Contains(systemPrompt, needle) {
			t.Fatalf("consultation system prompt missing %q:\n%s", needle, systemPrompt)
		}
	}
}

func TestAcademicProcessForwardedRequest_ArchitectConsultExposesResearchPaperTool(t *testing.T) {
	provider := &conversationModeProvider{}
	a, err := New(Config{ID: "academic"}, provider)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	fwd := &guide.ForwardedRequest{
		SourceAgentID: "architect",
		Intent:        guide.IntentRecall,
		Input:         "Research Python packaging guidance.",
	}
	toolSurface := a.requestToolSurfaceForForwardedRequest(fwd, academicForwardedResearchExtraTools(fwd)...)
	req := &providers.Request{
		SystemPrompt: buildAcademicConsultationSystemPrompt(a.config.SystemPrompt, fwd),
		Tools:        a.buildToolDefinitionsWithSurface(toolSurface),
	}

	foundPaper := false
	for _, tool := range req.Tools {
		if tool.Name == "author_research_paper" {
			foundPaper = true
			break
		}
	}
	if !foundPaper {
		t.Fatalf("expected architect consult surface to include author_research_paper")
	}

	for _, needle := range []string{
		"non-user-facing consultation",
		"informs Architect planning",
		"`author_research_paper`",
		"`clone_via_librarian`",
		"`crawl_links`",
		"end the consultation immediately",
	} {
		if !strings.Contains(req.SystemPrompt, needle) {
			t.Fatalf("consultation system prompt missing architect guidance %q:\n%s", needle, req.SystemPrompt)
		}
	}
	_ = provider
}

func TestAcademicProcessForwardedRequest_CustomArchitectIDExposesResearchPaperTool(t *testing.T) {
	provider := &conversationModeProvider{}
	a, err := New(Config{ID: "academic"}, provider)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	fwd := &guide.ForwardedRequest{
		SourceAgentID:   "architect-primary",
		SourceAgentName: "Architect",
		Intent:          guide.IntentRecall,
		Input:           "Research Python packaging guidance.",
	}
	toolSurface := a.requestToolSurfaceForForwardedRequest(fwd, academicForwardedResearchExtraTools(fwd)...)
	req := &providers.Request{
		SystemPrompt: buildAcademicConsultationSystemPrompt(a.config.SystemPrompt, fwd),
		Tools:        a.buildToolDefinitionsWithSurface(toolSurface),
	}

	foundPaper := false
	for _, tool := range req.Tools {
		if tool.Name == "author_research_paper" {
			foundPaper = true
			break
		}
	}
	if !foundPaper {
		t.Fatal("expected author_research_paper for custom architect id")
	}
	if !strings.Contains(req.SystemPrompt, "`author_research_paper`") {
		t.Fatalf("consultation system prompt did not require author_research_paper:\n%s", req.SystemPrompt)
	}
	_ = provider
}

func TestAcademicWithConsultationTurnState_ArchitectRequiresTerminalResearchPaper(t *testing.T) {
	a, err := New(Config{ID: "academic"}, &conversationModeProvider{})
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	ctx := a.withConsultationTurnState(context.Background(), &guide.ForwardedRequest{
		SourceAgentID: "architect",
		Intent:        guide.IntentRecall,
		Input:         "Research Python packaging guidance.",
		SessionID:     "session-1",
	})

	state := AcademicTurnStateFromContext(ctx)
	if state == nil {
		t.Fatal("expected architect consultation to install academic turn state")
	}
	action, _ := state.RequiredAction()
	if action != academicTurnActionResearchPaper {
		t.Fatalf("required action = %q, want %q", action, academicTurnActionResearchPaper)
	}
	if academicResearchExecutionStateFromContext(ctx) == nil {
		t.Fatal("expected architect consultation to install research execution state")
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
		"Provider-native `web_search` is not just a title/snippet lookup",
		"Confirm which cited URL is the decisive surfaced exact URL",
		"investigate multiple credible options or counterexamples",
		"Do not answer from memory alone",
		"Consult the Librarian",
	} {
		if !strings.Contains(systemPrompt, needle) {
			t.Fatalf("conversation system prompt missing %q:\n%s", needle, systemPrompt)
		}
	}
}

func TestAcademicProcessForwardedRequest_UserFacingSearchOnlyConversationWithoutSurfacedCandidateCanFinalize(t *testing.T) {
	a, err := New(Config{ID: "academic", MaxToolRuns: 1}, &conversationSearchOnlyProvider{})
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	result, err := a.processForwardedRequest(context.Background(), &guide.ForwardedRequest{
		SourceAgentID: "tui",
		Intent:        guide.IntentRecall,
		Input:         "What is the best Python CLI library today?",
	})
	if err != nil {
		t.Fatalf("processForwardedRequest: %v", err)
	}
	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("result type = %T, want *ConversationResult", result)
	}
	if strings.TrimSpace(conv.Response) == "" {
		t.Fatal("expected non-empty conversation response")
	}
}

func TestAcademicProcessForwardedRequest_ChildConsultSearchOnlyCanFinalize(t *testing.T) {
	a, err := New(Config{ID: "academic", MaxToolRuns: 1}, &conversationSearchOnlyProvider{})
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	result, err := a.processForwardedRequest(context.Background(), &guide.ForwardedRequest{
		SourceAgentID: "engineer",
		Intent:        guide.IntentRecall,
		Input:         "What is the best Python CLI library today?",
	})
	if err != nil {
		t.Fatalf("processForwardedRequest: %v", err)
	}
	conv, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("result type = %T, want *ConversationResult", result)
	}
	if strings.TrimSpace(conv.Response) == "" {
		t.Fatal("expected non-empty consultation response")
	}
}
