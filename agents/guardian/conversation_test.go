package guardian

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/security"
	"github.com/adalundhe/sylk/core/versioning"
)

type stubGuardianProvider struct {
	streamCalls   int
	completeCalls int
	lastReq       *providers.Request
	chunks        []*providers.StreamChunk
}

func (s *stubGuardianProvider) Complete(context.Context, *providers.Request) (*providers.Response, error) {
	s.completeCalls++
	return nil, errors.New("complete should not be called")
}

func (s *stubGuardianProvider) Stream(ctx context.Context, req *providers.Request) (<-chan *providers.StreamChunk, error) {
	s.streamCalls++
	cloned := *req
	s.lastReq = &cloned

	ch := make(chan *providers.StreamChunk, len(s.chunks))
	go func() {
		defer close(ch)
		for _, chunk := range s.chunks {
			select {
			case ch <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func TestGuardianExecuteConversation_AssessUsesStreamingOpenAIControls(t *testing.T) {
	provider := &stubGuardianProvider{
		chunks: []*providers.StreamChunk{
			{Type: providers.ChunkTypeStart, Timestamp: time.Now()},
			{Type: providers.ChunkTypeText, Text: "assessment ready", Timestamp: time.Now()},
			{
				Type:       providers.ChunkTypeEnd,
				Usage:      &providers.Usage{InputTokens: 12, OutputTokens: 5, ReasoningTokens: 2},
				StopReason: providers.StopReasonEndTurn,
				Timestamp:  time.Now(),
			},
		},
	}

	g := newTestGuardian(t, provider)
	result, err := g.executeConversation(context.Background(), &guide.ForwardedRequest{
		Input:     "Please give me a security assessment of the current repo",
		SessionID: "sess-123",
	})
	if err != nil {
		t.Fatalf("executeConversation() error = %v", err)
	}

	if provider.streamCalls != 1 {
		t.Fatalf("expected one stream call, got %d", provider.streamCalls)
	}
	if provider.completeCalls != 0 {
		t.Fatalf("expected no complete calls, got %d", provider.completeCalls)
	}
	if result.Response != "assessment ready" {
		t.Fatalf("unexpected response %q", result.Response)
	}
	if result.Usage == nil || result.Usage.InputTokens != 12 || result.Usage.OutputTokens != 5 || result.Usage.ReasoningTokens != 2 {
		t.Fatalf("unexpected usage: %#v", result.Usage)
	}
	if provider.lastReq == nil {
		t.Fatal("expected captured request")
	}
	if provider.lastReq.ReasoningEffort != "high" {
		t.Fatalf("expected high reasoning effort, got %q", provider.lastReq.ReasoningEffort)
	}
	if provider.lastReq.ReasoningSummary != "none" {
		t.Fatalf("expected reasoning summary none, got %q", provider.lastReq.ReasoningSummary)
	}
	if provider.lastReq.Verbosity != "medium" {
		t.Fatalf("expected medium verbosity, got %q", provider.lastReq.Verbosity)
	}
	if provider.lastReq.PromptCacheKey != "guardian:sess-123" {
		t.Fatalf("unexpected prompt cache key %q", provider.lastReq.PromptCacheKey)
	}
	if provider.lastReq.ParallelToolCalls == nil || !*provider.lastReq.ParallelToolCalls {
		t.Fatal("expected parallel tool calls to be enabled")
	}

	tools := toolNames(provider.lastReq.Tools)
	for _, name := range []string{"content_scan", "review_gate", "git_safety", "read_file", "glob", "grep"} {
		if !tools[name] {
			t.Fatalf("expected assess conversation to expose tool %q", name)
		}
	}
}

func TestGuardianExecuteConversation_ReportUsesObservabilityTools(t *testing.T) {
	provider := &stubGuardianProvider{
		chunks: []*providers.StreamChunk{
			{Type: providers.ChunkTypeStart, Timestamp: time.Now()},
			{Type: providers.ChunkTypeText, Text: "report ready", Timestamp: time.Now()},
			{
				Type:       providers.ChunkTypeEnd,
				Usage:      &providers.Usage{InputTokens: 8, OutputTokens: 4},
				StopReason: providers.StopReasonEndTurn,
				Timestamp:  time.Now(),
			},
		},
	}

	g := newTestGuardian(t, provider)
	result, err := g.executeConversation(context.Background(), &guide.ForwardedRequest{
		Input:     "Give me a health report for the agents",
		SessionID: "sess-report",
	})
	if err != nil {
		t.Fatalf("executeConversation() error = %v", err)
	}

	if result.Response != "report ready" {
		t.Fatalf("unexpected response %q", result.Response)
	}
	if provider.lastReq == nil {
		t.Fatal("expected captured request")
	}
	if provider.lastReq.ReasoningEffort != "medium" {
		t.Fatalf("expected medium reasoning effort, got %q", provider.lastReq.ReasoningEffort)
	}

	tools := toolNames(provider.lastReq.Tools)
	for _, name := range []string{"agent_health", "agent_logs", "system_status", "pipeline_status", "usage_breakdown", "quarantine_status"} {
		if !tools[name] {
			t.Fatalf("expected report conversation to expose tool %q", name)
		}
	}
}

func newTestGuardian(t *testing.T, provider guardianProvider) *Guardian {
	t.Helper()

	g, err := New(Config{
		ActivityPub: events.NewTestActivityCollector(),
		FileAccess:  versioning.NewDiskFileAccess(t.TempDir(), true),
		Sanitizer:   security.NewSecretSanitizer(),
	}, provider)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return g
}

func toolNames(tools []providers.Tool) map[string]bool {
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Name] = true
	}
	return names
}
