package llm

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mockLLMExecutor struct {
	executeFunc func(ctx context.Context, req *LLMRequest) (any, *TokenUsage, error)
	calls       atomic.Int32
}

func (m *mockLLMExecutor) Execute(ctx context.Context, req *LLMRequest) (any, *TokenUsage, error) {
	m.calls.Add(1)
	if m.executeFunc != nil {
		return m.executeFunc(ctx, req)
	}
	return "response", &TokenUsage{TotalTokens: 100}, nil
}

func TestNewRequestGate(t *testing.T) {
	executor := &mockLLMExecutor{}
	gate := NewRequestGate(DefaultGateConfig(), nil, nil, executor)
	defer gate.Close()

	if gate == nil {
		t.Fatal("expected non-nil gate")
	}

	stats := gate.Stats()
	if stats.WorkerPool != 4 {
		t.Errorf("expected 4 workers, got %d", stats.WorkerPool)
	}
}

func TestRequestGate_Submit(t *testing.T) {
	executor := &mockLLMExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, *TokenUsage, error) {
			return "test-response", &TokenUsage{TotalTokens: 50}, nil
		},
	}

	gate := NewRequestGate(DefaultGateConfig(), nil, nil, executor)
	defer gate.Close()

	req := NewLLMRequest(
		"session-1", "pipeline-1", "task-1", "agent-1", "guide",
		"anthropic", "claude-3",
		[]Message{{Role: "user", Content: "Hello"}},
		PriorityUserInteractive,
		100,
	)

	result, err := gate.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Response != "test-response" {
		t.Errorf("expected test-response, got %v", result.Response)
	}
	if result.Usage.TotalTokens != 50 {
		t.Errorf("expected 50 tokens, got %d", result.Usage.TotalTokens)
	}
}

func TestRequestGate_SubmitAsync(t *testing.T) {
	executor := &mockLLMExecutor{}

	gate := NewRequestGate(DefaultGateConfig(), nil, nil, executor)
	defer gate.Close()

	req := NewLLMRequest(
		"session-1", "", "", "", "guide",
		"anthropic", "claude-3",
		nil, PriorityUserInteractive, 100,
	)

	resultCh, err := gate.SubmitAsync(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case result := <-resultCh:
		if result.Error != nil {
			t.Errorf("unexpected error: %v", result.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for result")
	}
}

func TestRequestGate_Close(t *testing.T) {
	executor := &mockLLMExecutor{}
	gate := NewRequestGate(DefaultGateConfig(), nil, nil, executor)

	err := gate.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = gate.Close()
	if err != nil {
		t.Error("double close should not error")
	}

	req := NewLLMRequest("", "", "", "", "", "", "", nil, 0, 0)
	_, err = gate.Submit(context.Background(), req)
	if err != ErrGateClosed {
		t.Errorf("expected ErrGateClosed, got %v", err)
	}
}

func TestRequestGate_ContextCancellation(t *testing.T) {
	blockCh := make(chan struct{})
	executor := &mockLLMExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, *TokenUsage, error) {
			<-blockCh
			return nil, nil, nil
		},
	}

	config := DefaultGateConfig()
	config.WorkerPoolSize = 1
	gate := NewRequestGate(config, nil, nil, executor)
	defer gate.Close()

	blocker := NewLLMRequest("", "", "", "", "", "", "", nil, PriorityUserInteractive, 0)
	go func() {
		gate.Submit(context.Background(), blocker)
	}()

	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	req := NewLLMRequest("", "", "", "", "", "", "", nil, PriorityBackground, 0)
	_, err := gate.Submit(ctx, req)
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}

	close(blockCh)
}

func TestRequestGate_MaxQueueSize(t *testing.T) {
	blockCh := make(chan struct{})
	executor := &mockLLMExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, *TokenUsage, error) {
			<-blockCh
			return nil, nil, nil
		},
	}

	config := GateConfig{
		WorkerPoolSize: 1,
		MaxQueueSize:   2,
		RequestTimeout: time.Minute,
	}
	gate := NewRequestGate(config, nil, nil, executor)
	defer func() {
		close(blockCh)
		gate.Close()
	}()

	for i := 0; i < 3; i++ {
		req := NewLLMRequest("", "", "", "", "", "", "", nil, 0, 0)
		go gate.SubmitAsync(context.Background(), req)
	}

	time.Sleep(50 * time.Millisecond)

	req := NewLLMRequest("", "", "", "", "", "", "", nil, 0, 0)
	_, err := gate.SubmitAsync(context.Background(), req)
	if err != ErrWorkerPoolFull {
		t.Errorf("expected ErrWorkerPoolFull, got %v", err)
	}
}

func TestRequestGate_Stats(t *testing.T) {
	blockCh := make(chan struct{})
	executor := &mockLLMExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, *TokenUsage, error) {
			<-blockCh
			return nil, nil, nil
		},
	}

	config := DefaultGateConfig()
	config.WorkerPoolSize = 2
	gate := NewRequestGate(config, nil, nil, executor)
	defer func() {
		close(blockCh)
		gate.Close()
	}()

	req1 := NewLLMRequest("", "", "", "", "", "", "", nil, 0, 0)
	req2 := NewLLMRequest("", "", "", "", "", "", "", nil, 0, 0)
	gate.SubmitAsync(context.Background(), req1)
	gate.SubmitAsync(context.Background(), req2)

	time.Sleep(50 * time.Millisecond)

	stats := gate.Stats()
	if stats.ActiveWorkers != 2 {
		t.Errorf("expected 2 active workers, got %d", stats.ActiveWorkers)
	}
	if stats.PendingCount != 2 {
		t.Errorf("expected 2 pending, got %d", stats.PendingCount)
	}
}

func TestRequestGate_IsHealthy(t *testing.T) {
	executor := &mockLLMExecutor{}

	config := GateConfig{
		WorkerPoolSize: 4,
		MaxQueueSize:   10,
		RequestTimeout: time.Minute,
	}
	gate := NewRequestGate(config, nil, nil, executor)
	defer gate.Close()

	if !gate.IsHealthy() {
		t.Error("expected healthy gate")
	}

	gate.Close()
	if gate.IsHealthy() {
		t.Error("expected unhealthy after close")
	}
}

func TestRequestGate_Concurrent(t *testing.T) {
	executor := &mockLLMExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, *TokenUsage, error) {
			time.Sleep(time.Millisecond)
			return nil, &TokenUsage{TotalTokens: 10}, nil
		},
	}

	gate := NewRequestGate(DefaultGateConfig(), nil, nil, executor)
	defer gate.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := NewLLMRequest("", "", "", "", "", "", "", nil, PriorityExecution, 0)
			_, _ = gate.Submit(context.Background(), req)
		}()
	}

	wg.Wait()

	if executor.calls.Load() != 50 {
		t.Errorf("expected 50 executions, got %d", executor.calls.Load())
	}
}

func TestRequestGate_WithBudget(t *testing.T) {
	tracker := NewUsageTracker()
	budget := NewTokenBudget(tracker)
	budget.SetGlobalLimit(100)

	executor := &mockLLMExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, *TokenUsage, error) {
			return nil, &TokenUsage{TotalTokens: 50}, nil
		},
	}

	config := DefaultGateConfig()
	config.BudgetCheckEnabled = true
	gate := NewRequestGate(config, budget, nil, executor)
	defer gate.Close()

	req := NewLLMRequest("session-1", "", "task-1", "", "", "anthropic", "", nil, 0, 200)

	result, err := gate.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}
	if result.Error != ErrGlobalBudgetExceeded {
		t.Errorf("expected ErrGlobalBudgetExceeded, got %v", result.Error)
	}
}

func TestRequestGate_WithRateLimiter(t *testing.T) {
	limiter := NewProviderRateLimiter("test")
	limiter.mu.Lock()
	limiter.state = RateLimitExceeded
	limiter.backoffUntil = time.Now().Add(time.Hour)
	limiter.mu.Unlock()

	executor := &mockLLMExecutor{}

	config := DefaultGateConfig()
	config.RateLimitEnabled = true
	gate := NewRequestGate(config, nil, limiter, executor)
	defer gate.Close()

	req := NewLLMRequest("", "", "", "", "", "", "", nil, 0, 0)
	result, err := gate.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}
	if result.Error != ErrRateLimited {
		t.Errorf("expected ErrRateLimited, got %v", result.Error)
	}
}

func TestRequestGate_SetBudget(t *testing.T) {
	executor := &mockLLMExecutor{}
	gate := NewRequestGate(DefaultGateConfig(), nil, nil, executor)
	defer gate.Close()

	tracker := NewUsageTracker()
	budget := NewTokenBudget(tracker)
	gate.SetBudget(budget)

	if gate.budget != budget {
		t.Error("budget not set")
	}
}

func TestRequestGate_SetRateLimiter(t *testing.T) {
	executor := &mockLLMExecutor{}
	gate := NewRequestGate(DefaultGateConfig(), nil, nil, executor)
	defer gate.Close()

	limiter := NewProviderRateLimiter("test")
	gate.SetRateLimiter(limiter)

	if gate.rateLimiter != limiter {
		t.Error("rate limiter not set")
	}
}
