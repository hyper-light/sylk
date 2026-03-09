package academic

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/providers"
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
