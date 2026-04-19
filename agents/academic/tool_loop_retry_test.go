package academic

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/toolruntime"
)

type deadlineRetryProvider struct {
	calls          int
	requestTimeout time.Duration
}

func (p *deadlineRetryProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	p.calls++
	if p.calls == 1 {
		return nil, fmt.Errorf("openai generate: %w", context.DeadlineExceeded)
	}
	return &providers.Response{
		Content: "Prefer `pyproject.toml`, use PEP 517/518 build backends, and publish wheels.",
		Model:   "gpt-5.4-pro",
		Usage: providers.Usage{
			InputTokens:  128,
			OutputTokens: 48,
		},
	}, nil
}

func (p *deadlineRetryProvider) RequestTimeout() time.Duration {
	return p.requestTimeout
}

func TestAcademicExecuteToolLoop_RetriesDeadlineExceededOnce(t *testing.T) {
	provider := &deadlineRetryProvider{requestTimeout: 2 * time.Minute}
	a, err := New(Config{Factory: newTestFactory(t), ID: "academic"}, provider)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	req := &providers.Request{
		Messages:  []providers.Message{{Role: providers.RoleUser, Content: "What are ideal methods for Python packaging?"}},
		Model:     "gpt-5.4-pro",
		MaxTokens: 512,
	}

	content, err := a.executeToolLoop(context.Background(), req, nil, nil)
	if err != nil {
		t.Fatalf("executeToolLoop: %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	if content == "" {
		t.Fatal("expected non-empty content after retry")
	}
}

type nativeWebSearchStreamingProvider struct{}

func (p *nativeWebSearchStreamingProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	return &providers.Response{
		Content: "Search-backed answer.",
		Model:   "gpt-5.4-pro",
	}, nil
}

func (p *nativeWebSearchStreamingProvider) Stream(_ context.Context, _ *providers.Request) (<-chan *providers.StreamChunk, error) {
	ch := make(chan *providers.StreamChunk, 8)
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeStart, Timestamp: time.Now()}
	ch <- &providers.StreamChunk{
		Type: providers.ChunkTypeToolStart,
		ToolCall: &providers.ToolCallChunk{
			ID:   "ws_1",
			Name: "web_search",
			Kind: providers.ToolKindNativeWebSearch,
		},
		Timestamp: time.Now(),
	}
	ch <- &providers.StreamChunk{
		Type: providers.ChunkTypeToolDelta,
		ToolCall: &providers.ToolCallChunk{
			ID:             "ws_1",
			Kind:           providers.ToolKindNativeWebSearch,
			ArgumentsDelta: `{"query":"python packaging pep 621","action":"search"}`,
		},
		Timestamp: time.Now(),
	}
	ch <- &providers.StreamChunk{
		Type:      providers.ChunkTypeToolEnd,
		ToolCall:  &providers.ToolCallChunk{ID: "ws_1", Kind: providers.ToolKindNativeWebSearch},
		Timestamp: time.Now(),
	}
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeText, Text: "Search-backed answer.", Timestamp: time.Now()}
	ch <- &providers.StreamChunk{
		Type:       providers.ChunkTypeEnd,
		StopReason: providers.StopReasonEndTurn,
		Usage:      &providers.Usage{InputTokens: 12, OutputTokens: 8},
		Timestamp:  time.Now(),
	}
	close(ch)
	return ch, nil
}

func TestAcademicExecuteToolLoop_StreamedNativeWebSearchDoesNotExecuteLocally(t *testing.T) {
	a, err := New(Config{Factory: newTestFactory(t), ID: "academic"}, &nativeWebSearchStreamingProvider{})
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	req := &providers.Request{
		Messages:  []providers.Message{{Role: providers.RoleUser, Content: "Research Python packaging guidance."}},
		Model:     "gpt-5.4-pro",
		MaxTokens: 512,
		Tools:     a.buildToolDefinitions(),
	}

	content, err := a.executeToolLoop(context.Background(), req, nil, nil)
	if err != nil {
		t.Fatalf("executeToolLoop: %v", err)
	}
	if content != "Search-backed answer." {
		t.Fatalf("content = %q, want Search-backed answer.", content)
	}
}

type scriptedAcademicSurface struct {
	outputs map[string]string
	errs    map[string]error
	calls   []string
}

func (s *scriptedAcademicSurface) AgentID() string                              { return "academic" }
func (s *scriptedAcademicSurface) CapabilityScope() string                      { return "" }
func (s *scriptedAcademicSurface) Allows(string) bool                           { return true }
func (s *scriptedAcademicSurface) SyncActiveFromLoaded() []string               { return nil }
func (s *scriptedAcademicSurface) BuildToolDefinitions() []providers.Tool       { return nil }
func (s *scriptedAcademicSurface) ValidateBatch([]toolruntime.Invocation) error { return nil }

func (s *scriptedAcademicSurface) Execute(_ context.Context, inv toolruntime.Invocation) (toolruntime.ExecutionResult, error) {
	name := strings.TrimSpace(inv.ToolCall.Name)
	s.calls = append(s.calls, name)
	output, hasOutput := s.outputs[name]
	execErr, hasErr := s.errs[name]
	if !hasOutput && !hasErr {
		return toolruntime.ExecutionResult{}, fmt.Errorf("unexpected tool %q", inv.ToolCall.Name)
	}
	return toolruntime.ExecutionResult{
		Output:   output,
		ToolName: inv.ToolCall.Name,
	}, execErr
}

func (s *scriptedAcademicSurface) ExecuteApproved(ctx context.Context, inv toolruntime.Invocation, _ *toolruntime.GuardianControlGrant) (toolruntime.ExecutionResult, error) {
	return s.Execute(ctx, inv)
}

type delegatedFetchProvider struct {
	calls int
}

func (p *delegatedFetchProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	p.calls++
	return &providers.Response{
		Model: "gpt-5.4-pro",
		ToolCalls: []providers.ToolCall{{
			ID:        "fetch_document_1",
			Name:      "fetch_document",
			Arguments: `{"url":"https://example.com/spec","reason":"Ground the answer with the authoritative spec."}`,
		}},
	}, nil
}

func TestAcademicExecuteToolLoop_DelegatedFetchReturnsUserMessage(t *testing.T) {
	provider := &delegatedFetchProvider{}
	a, err := New(Config{Factory: newTestFactory(t), ID: "academic"}, provider)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	req := &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "What does the spec require?"}},
		Model:    "gpt-5.4-pro",
	}
	surface := &scriptedAcademicSurface{
		errs: map[string]error{
			"fetch_document": shared.ApprovalDeniedDelegatedError("fetch_document", "fetch denied by user"),
		},
	}

	content, err := a.executeToolLoop(context.Background(), req, nil, surface)
	if err != nil {
		t.Fatalf("executeToolLoop: %v", err)
	}
	if !strings.Contains(content, "denied approval for fetch_document") {
		t.Fatalf("content = %q, want delegated approval-denied message", content)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 after delegated fetch", provider.calls)
	}
}

type failedConsultThenAnswerProvider struct {
	calls int
}

func (p *failedConsultThenAnswerProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.Response{
			Model: "gpt-5.4-pro",
			ToolCalls: []providers.ToolCall{{
				ID:        "consult_1",
				Name:      "consult",
				Arguments: `{"target":"librarian","query":"What patterns exist for packaging configuration?"}`,
			}},
		}, nil
	}
	return &providers.Response{
		Content: "I could not reach Librarian, so I’m proceeding with direct research and provisional guidance.",
		Model:   "gpt-5.4-pro",
	}, nil
}

func TestAcademicExecuteToolLoop_ConsultFailureReturnsStructuredToolResultAndContinues(t *testing.T) {
	provider := &failedConsultThenAnswerProvider{}
	a, err := New(Config{Factory: newTestFactory(t), ID: "academic"}, provider)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	req := &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "Research packaging configuration guidance."}},
		Model:    "gpt-5.4-pro",
	}

	content, err := a.executeToolLoop(context.Background(), req, nil, nil)
	if err != nil {
		t.Fatalf("executeToolLoop: %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	if !strings.Contains(content, "proceeding with direct research") {
		t.Fatalf("content = %q, want final provider answer after consult failure", content)
	}

	var toolMsg *providers.Message
	for i := range req.Messages {
		msg := &req.Messages[i]
		if msg.Role == providers.RoleTool && msg.ToolName == "consult" {
			toolMsg = msg
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("expected consult tool message to be appended")
	}
	if toolMsg.IsError {
		t.Fatalf("consult tool message should not be marked as error: %#v", toolMsg)
	}
	if !strings.Contains(toolMsg.Content, `"success":false`) {
		t.Fatalf("consult tool message = %q, want structured failure payload", toolMsg.Content)
	}
	if !strings.Contains(toolMsg.Content, `"status":"failed"`) {
		t.Fatalf("consult tool message = %q, want failed status", toolMsg.Content)
	}
	if !strings.Contains(toolMsg.Content, `"error":"academic bus is unavailable"`) {
		t.Fatalf("consult tool message = %q, want bus-unavailable error", toolMsg.Content)
	}
}

type surfacedSearchResultProvider struct {
	calls int
}

func (p *surfacedSearchResultProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.Response{
			Content: "I found a promising official source and need to inspect it before answering.",
			Model:   "gpt-5.4-pro",
			ProviderMetadata: map[string]any{
				providers.ProviderMetadataNativeWebSearchCallsKey: []providers.NativeWebSearchCall{
					{
						ID:       "search_1",
						Provider: "anthropic",
						Action:   "search",
						Query:    "oauth token validation boundary",
					},
				},
				providers.ProviderMetadataNativeWebSearchResultsKey: []providers.NativeWebSearchResult{
					{
						SearchID: "search_1",
						Provider: "anthropic",
						Source:   "search_result",
						Query:    "oauth token validation boundary",
						URL:      "https://docs.example.com/oauth",
						Title:    "OAuth Deployment Guide",
					},
				},
			},
		}, nil
	}
	return &providers.Response{
		Content: "Use a dedicated token validation boundary and keep verification centralized.",
		Model:   "gpt-5.4-pro",
	}, nil
}

func TestAcademicExecuteToolLoop_AutoFetchesSurfacedNativeSearchResult(t *testing.T) {
	provider := &surfacedSearchResultProvider{}
	a, err := New(Config{Factory: newTestFactory(t), ID: "academic"}, provider)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	req := &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "Research oauth token validation guidance."}},
		Model:    "gpt-5.4-pro",
	}
	surface := &scriptedAcademicSurface{
		outputs: map[string]string{
			"web_fetch": `{"success":true,"url":"https://docs.example.com/oauth","title":"OAuth Deployment Guide","content":"Use a dedicated token validation boundary and keep verification centralized.","grounded":true,"ingested":true}`,
		},
	}
	execState := newAcademicResearchExecutionState("sess-auto-fetch")
	ctx := WithAcademicResearchExecutionState(context.Background(), execState)

	content, err := a.executeToolLoop(ctx, req, nil, surface)
	if err != nil {
		t.Fatalf("executeToolLoop: %v", err)
	}
	if content != "Use a dedicated token validation boundary and keep verification centralized." {
		t.Fatalf("content = %q, want final provider answer", content)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	if got := strings.Join(surface.calls, ","); got != "web_fetch" {
		t.Fatalf("executed tools = %q, want web_fetch", got)
	}
	if !execState.hasGroundedSources() {
		t.Fatal("expected surfaced native search result to be grounded automatically")
	}
}

type surfacedOpenPageCallProvider struct {
	calls int
}

func (p *surfacedOpenPageCallProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.Response{
			Content: "I found the official OAuth deployment guide.",
			Model:   "gpt-5.4-pro",
			ProviderMetadata: map[string]any{
				providers.ProviderMetadataNativeWebSearchCallsKey: []providers.NativeWebSearchCall{
					{
						ID:       "search_1",
						Provider: "openai",
						Action:   "search",
						Query:    "oauth token validation boundary",
					},
					{
						ID:       "open_1",
						Provider: "openai",
						Action:   "open_page",
						Query:    "oauth token validation boundary",
						URL:      "https://docs.example.com/oauth",
					},
				},
			},
		}, nil
	}
	return &providers.Response{
		Content: "Use a dedicated token validation boundary and keep verification centralized.",
		Model:   "gpt-5.4-pro",
	}, nil
}

func TestAcademicExecuteToolLoop_AutoFetchesSurfacedNativeOpenPageCall(t *testing.T) {
	provider := &surfacedOpenPageCallProvider{}
	a, err := New(Config{Factory: newTestFactory(t), ID: "academic"}, provider)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	req := &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "Research oauth token validation guidance."}},
		Model:    "gpt-5.4-pro",
	}
	surface := &scriptedAcademicSurface{
		outputs: map[string]string{
			"web_fetch": `{"success":true,"url":"https://docs.example.com/oauth","title":"OAuth Deployment Guide","content":"Use a dedicated token validation boundary and keep verification centralized.","grounded":true,"ingested":true}`,
		},
	}
	execState := newAcademicResearchExecutionState("sess-auto-fetch-open-page")
	ctx := WithAcademicResearchExecutionState(context.Background(), execState)

	content, err := a.executeToolLoop(ctx, req, nil, surface)
	if err != nil {
		t.Fatalf("executeToolLoop: %v", err)
	}
	if content != "Use a dedicated token validation boundary and keep verification centralized." {
		t.Fatalf("content = %q, want final provider answer", content)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	if got := strings.Join(surface.calls, ","); got != "web_fetch" {
		t.Fatalf("executed tools = %q, want web_fetch", got)
	}
	if !execState.hasGroundedSources() {
		t.Fatal("expected surfaced native open_page call to be grounded automatically")
	}
}

type duplicateConsultProvider struct {
	calls int
}

func (p *duplicateConsultProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	p.calls++
	return &providers.Response{
		Content: "Consulting Librarian for codebase patterns.",
		Model:   "gpt-5.4-pro",
		ToolCalls: []providers.ToolCall{{
			ID:        fmt.Sprintf("consult_%d", p.calls),
			Name:      "consult",
			Arguments: `{"target":"librarian","query":"What patterns exist for packaging configuration?"}`,
		}},
	}, nil
}

func TestAcademicExecuteToolLoop_RepeatedToolBatchFailsFast(t *testing.T) {
	provider := &duplicateConsultProvider{}
	a, err := New(Config{Factory: newTestFactory(t), ID: "academic"}, provider)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	req := &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "Research packaging configuration guidance."}},
		Model:    "gpt-5.4-pro",
	}
	surface := &scriptedAcademicSurface{
		outputs: map[string]string{
			"consult": `{"target":"librarian","success":true,"data":{"summary":"Prefer project-level packaging metadata."}}`,
		},
	}

	_, err = a.executeToolLoop(context.Background(), req, nil, surface)
	if err == nil {
		t.Fatal("expected repeated tool batch to fail")
	}
	if !strings.Contains(err.Error(), "academic repeated tool call: consult") {
		t.Fatalf("error = %v, want repeated consult failure", err)
	}
}

type researchPaperProvider struct {
	calls int
}

func (p *researchPaperProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.Response{
			Model: "gpt-5.4-pro",
			ToolCalls: []providers.ToolCall{
				{
					ID:   "paper_1",
					Name: "author_research_paper",
					Arguments: `{
					"topic":"Python packaging guidance",
					"context":"Architect research request",
					"title":"Python Packaging Guidance",
					"research_slug":"python-packaging-guidance"
				}`,
				},
				{
					ID:        "consult_1",
					Name:      "consult",
					Arguments: `{"target_agent":"librarian","query":"This should never run."}`,
				},
			},
		}, nil
	}
	return &providers.Response{
		Content: "This second provider turn should never happen.",
		Model:   "gpt-5.4-pro",
	}, nil
}

func TestAcademicExecuteToolLoop_AuthorResearchPaperTerminatesRequiredConsultation(t *testing.T) {
	provider := &researchPaperProvider{}
	a, err := New(Config{Factory: newTestFactory(t), ID: "academic"}, provider)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	req := &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "Research Python packaging guidance for the architect."}},
		Model:    "gpt-5.4-pro",
	}
	surface := &scriptedAcademicSurface{
		outputs: map[string]string{
			"author_research_paper": `{"paper_id":"paper_1","paper_path":"/tmp/python-packaging-guidance_v1.md","summary":"The research paper is ready for Architect consumption.","stored_in_archivalist":true}`,
			"consult":               `{"summary":"unexpected consult"}`,
		},
	}

	ctx := WithAcademicTurnState(context.Background(), newAcademicTurnState(
		academicTurnActionResearchPaper,
		"Architect requires a terminal research artifact.",
	))
	content, err := a.executeToolLoop(ctx, req, nil, surface)
	if err != nil {
		t.Fatalf("executeToolLoop: %v", err)
	}
	if !strings.Contains(content, "research paper is ready") {
		t.Fatalf("content = %q, want terminal research paper response", content)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if got := strings.Join(surface.calls, ","); got != "author_research_paper" {
		t.Fatalf("executed tools = %q, want only author_research_paper", got)
	}
}
