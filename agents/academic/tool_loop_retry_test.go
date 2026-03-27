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
	a, err := New(Config{ID: "academic"}, provider)
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
	a, err := New(Config{ID: "academic"}, &nativeWebSearchStreamingProvider{})
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
}

func (s *scriptedAcademicSurface) AgentID() string { return "academic" }
func (s *scriptedAcademicSurface) CapabilityScope() string { return "" }
func (s *scriptedAcademicSurface) Allows(string) bool { return true }
func (s *scriptedAcademicSurface) SyncActiveFromLoaded() []string { return nil }
func (s *scriptedAcademicSurface) BuildToolDefinitions() []providers.Tool { return nil }
func (s *scriptedAcademicSurface) ValidateBatch([]toolruntime.Invocation) error { return nil }

func (s *scriptedAcademicSurface) Execute(_ context.Context, inv toolruntime.Invocation) (toolruntime.ExecutionResult, error) {
	name := strings.TrimSpace(inv.ToolCall.Name)
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
	a, err := New(Config{ID: "academic"}, provider)
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
	a, err := New(Config{ID: "academic"}, provider)
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
			ToolCalls: []providers.ToolCall{{
				ID:   "paper_1",
				Name: "author_research_paper",
				Arguments: `{
					"topic":"Python packaging guidance",
					"context":"Architect research request",
					"title":"Python Packaging Guidance",
					"research_slug":"python-packaging-guidance"
				}`,
			}},
		}, nil
	}
	return &providers.Response{
		Content: "The research paper is ready for Architect consumption.",
		Model:   "gpt-5.4-pro",
	}, nil
}

func TestAcademicExecuteToolLoop_AuthorResearchPaperBehavesLikeNormalTool(t *testing.T) {
	provider := &researchPaperProvider{}
	a, err := New(Config{ID: "academic"}, provider)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	req := &providers.Request{
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "Research Python packaging guidance for the architect."}},
		Model:    "gpt-5.4-pro",
	}
	surface := &scriptedAcademicSurface{
		outputs: map[string]string{
			"author_research_paper": `{"paper_id":"paper_1","paper_path":"/tmp/python-packaging-guidance_v1.md","stored_in_archivalist":true}`,
		},
	}

	content, err := a.executeToolLoop(context.Background(), req, nil, surface)
	if err != nil {
		t.Fatalf("executeToolLoop: %v", err)
	}
	if !strings.Contains(content, "research paper is ready") {
		t.Fatalf("content = %q, want final response after paper authoring", content)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
}
