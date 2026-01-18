package coordinator

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/llm/bulkhead"
	"github.com/adalundhe/sylk/core/llm/correlation"
)

// --- Mock LLMProvider ---

type mockProvider struct {
	name         string
	completeFunc func(ctx context.Context, req *LLMRequest) (*LLMResponse, error)
	streamFunc   func(ctx context.Context, req *LLMRequest) <-chan string
}

func newMockProvider(name string) *mockProvider {
	return &mockProvider{
		name: name,
		completeFunc: func(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
			return NewLLMResponse("test response", 100*time.Millisecond), nil
		},
		streamFunc: func(ctx context.Context, req *LLMRequest) <-chan string {
			ch := make(chan string, 3)
			go func() {
				defer close(ch)
				ch <- "chunk1"
				ch <- "chunk2"
				ch <- "chunk3"
			}()
			return ch
		},
	}
}

func (m *mockProvider) Complete(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	return m.completeFunc(ctx, req)
}

func (m *mockProvider) Stream(ctx context.Context, req *LLMRequest) <-chan string {
	return m.streamFunc(ctx, req)
}

func (m *mockProvider) Name() string {
	return m.name
}

// --- Mock BudgetGetter ---

type mockBudgetGetter struct {
	sessionUsage float64
	taskUsage    float64
}

func newMockBudgetGetter() *mockBudgetGetter {
	return &mockBudgetGetter{
		sessionUsage: 0.5, // 50% usage by default
		taskUsage:    0.5,
	}
}

func (m *mockBudgetGetter) GetUsagePercent(sessionID string) float64 {
	return m.sessionUsage
}

func (m *mockBudgetGetter) GetTaskUsagePercent(sessionID, taskID string) float64 {
	return m.taskUsage
}

// --- Mock SignalDispatcher ---

type mockDispatcher struct {
	signals []correlation.Signal
	mu      sync.Mutex
}

func newMockDispatcher() *mockDispatcher {
	return &mockDispatcher{
		signals: make([]correlation.Signal, 0),
	}
}

func (m *mockDispatcher) SendSignal(signal correlation.Signal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.signals = append(m.signals, signal)
}

func (m *mockDispatcher) SignalCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.signals)
}

// --- Test Helpers ---

func newTestCoordinator(budget *mockBudgetGetter, dispatcher *mockDispatcher) *LLMRequestCoordinator {
	config := DefaultCoordinatorConfig()
	return NewLLMRequestCoordinator(config, budget, dispatcher, nil)
}

func newTestRequest(sessionID, provider, model string) *LLMRequest {
	req := NewLLMRequest(sessionID, "task-1", provider, model)
	req.AddMessage("user", "Hello")
	return req
}

// --- Tests ---

func TestNewLLMRequestCoordinator(t *testing.T) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	config := DefaultCoordinatorConfig()

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	if coord == nil {
		t.Fatal("expected coordinator to be created")
	}

	if coord.costBackpressure == nil {
		t.Error("expected costBackpressure to be initialized")
	}
	if coord.correlationEngine == nil {
		t.Error("expected correlationEngine to be initialized")
	}
}

func TestExecuteRequest_Success(t *testing.T) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)
	defer coord.Stop()

	provider := newMockProvider("openai")
	req := newTestRequest("session-1", "openai", "gpt-4")

	resp, err := coord.ExecuteRequest(context.Background(), req, provider)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Content != "test response" {
		t.Errorf("expected 'test response', got %q", resp.Content)
	}
}

func TestExecuteRequest_Streaming(t *testing.T) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)
	defer coord.Stop()

	provider := newMockProvider("openai")
	req := newTestRequest("session-1", "openai", "gpt-4").WithStreaming(true)

	resp, err := coord.ExecuteRequest(context.Background(), req, provider)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected response")
	}
	if !resp.IsStreaming() {
		t.Error("expected streaming response")
	}

	// Collect chunks
	var chunks []string
	for chunk := range resp.Chunks {
		chunks = append(chunks, chunk)
	}
	if len(chunks) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(chunks))
	}
}

func TestExecuteRequest_ProviderError(t *testing.T) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)
	defer coord.Stop()

	provider := newMockProvider("openai")
	provider.completeFunc = func(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
		return nil, errors.New("provider error")
	}

	req := newTestRequest("session-1", "openai", "gpt-4")

	resp, err := coord.ExecuteRequest(context.Background(), req, provider)
	if err == nil {
		t.Fatal("expected error")
	}
	if resp != nil {
		t.Error("expected nil response on error")
	}
}

func TestExecuteRequest_RateLimitError(t *testing.T) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)
	defer coord.Stop()

	provider := newMockProvider("openai")
	provider.completeFunc = func(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
		return nil, errors.New("429 rate limited")
	}

	req := newTestRequest("session-1", "openai", "gpt-4")

	_, err := coord.ExecuteRequest(context.Background(), req, provider)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExecuteRequest_BudgetExhausted(t *testing.T) {
	budget := newMockBudgetGetter()
	budget.sessionUsage = 1.1 // Over budget
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)
	defer coord.Stop()

	provider := newMockProvider("openai")
	req := newTestRequest("session-1", "openai", "gpt-4")

	_, err := coord.ExecuteRequest(context.Background(), req, provider)
	if err == nil {
		t.Fatal("expected error for budget exhaustion")
	}
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Errorf("expected ErrBudgetExhausted, got %v", err)
	}
}

func TestExecuteRequest_ContextCancellation(t *testing.T) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)
	defer coord.Stop()

	provider := newMockProvider("openai")
	provider.completeFunc = func(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	req := newTestRequest("session-1", "openai", "gpt-4")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := coord.ExecuteRequest(ctx, req, provider)
	if err == nil {
		t.Fatal("expected context error")
	}
}

func TestExecuteRequest_ConcurrentSessions(t *testing.T) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)
	defer coord.Stop()

	provider := newMockProvider("openai")

	var wg sync.WaitGroup
	var successCount atomic.Int32

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(sessionIdx int) {
			defer wg.Done()
			sessionID := "session-" + string(rune('A'+sessionIdx))
			req := newTestRequest(sessionID, "openai", "gpt-4")

			resp, err := coord.ExecuteRequest(context.Background(), req, provider)
			if err == nil && resp != nil {
				successCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	if successCount.Load() != 10 {
		t.Errorf("expected 10 successful requests, got %d", successCount.Load())
	}
}

func TestGetOrCreateBulkhead(t *testing.T) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)
	defer coord.Stop()

	bulk1 := coord.getOrCreateBulkhead("session-1")
	bulk2 := coord.getOrCreateBulkhead("session-1")
	bulk3 := coord.getOrCreateBulkhead("session-2")

	if bulk1 != bulk2 {
		t.Error("expected same bulkhead for same session")
	}
	if bulk1 == bulk3 {
		t.Error("expected different bulkhead for different session")
	}
}

func TestGetOrCreateRateLimiter(t *testing.T) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)
	defer coord.Stop()

	limiter1 := coord.getOrCreateRateLimiter("openai")
	limiter2 := coord.getOrCreateRateLimiter("openai")
	limiter3 := coord.getOrCreateRateLimiter("anthropic")

	if limiter1 != limiter2 {
		t.Error("expected same limiter for same provider")
	}
	if limiter1 == limiter3 {
		t.Error("expected different limiter for different provider")
	}
}

func TestGetOrCreateHealthMonitor(t *testing.T) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)
	defer coord.Stop()

	monitor1 := coord.getOrCreateHealthMonitor("openai")
	monitor2 := coord.getOrCreateHealthMonitor("openai")
	monitor3 := coord.getOrCreateHealthMonitor("anthropic")

	if monitor1 != monitor2 {
		t.Error("expected same monitor for same provider")
	}
	if monitor1 == monitor3 {
		t.Error("expected different monitor for different provider")
	}
}

func TestStats(t *testing.T) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)
	defer coord.Stop()

	provider := newMockProvider("openai")

	// Execute a request to create bulkhead and health monitor
	req := newTestRequest("session-1", "openai", "gpt-4")
	_, err := coord.ExecuteRequest(context.Background(), req, provider)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stats := coord.Stats()

	if stats.SessionCount() != 1 {
		t.Errorf("expected 1 session, got %d", stats.SessionCount())
	}
	if stats.ProviderCount() != 1 {
		t.Errorf("expected 1 provider, got %d", stats.ProviderCount())
	}

	sessionStats, ok := stats.Sessions["session-1"]
	if !ok {
		t.Error("expected session-1 in stats")
	}
	if sessionStats.SessionID != "session-1" {
		t.Errorf("expected session ID 'session-1', got %q", sessionStats.SessionID)
	}

	providerStats, ok := stats.Providers["openai"]
	if !ok {
		t.Error("expected openai in provider stats")
	}
	if providerStats.HealthScore < 0 || providerStats.HealthScore > 1 {
		t.Errorf("expected health score between 0 and 1, got %f", providerStats.HealthScore)
	}
}

func TestStop(t *testing.T) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)

	provider := newMockProvider("openai")

	// Execute requests to create resources
	req1 := newTestRequest("session-1", "openai", "gpt-4")
	req2 := newTestRequest("session-2", "anthropic", "claude-3")

	_, _ = coord.ExecuteRequest(context.Background(), req1, provider)
	_, _ = coord.ExecuteRequest(context.Background(), req2, provider)

	// Stop should not panic
	coord.Stop()
}

func TestIsRateLimitError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"429 error", errors.New("HTTP 429 Too Many Requests"), true},
		{"rate limit error", errors.New("rate limit exceeded"), true},
		{"other error", errors.New("connection refused"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRateLimitError(tt.err)
			if result != tt.expected {
				t.Errorf("isRateLimitError(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		expectDelay bool
	}{
		{"nil error", nil, false},
		{"retry hint", errors.New("retry after 5s"), true},
		{"no retry hint", errors.New("server error"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delay := parseRetryAfter(tt.err)
			hasDelay := delay > 0
			if hasDelay != tt.expectDelay {
				t.Errorf("parseRetryAfter(%v) delay=%v, expected delay=%v", tt.err, delay, tt.expectDelay)
			}
		})
	}
}

func TestExecuteRequest_MultipleSessions_DifferentBulkheads(t *testing.T) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)
	defer coord.Stop()

	provider := newMockProvider("openai")

	// Execute requests from different sessions
	req1 := newTestRequest("session-A", "openai", "gpt-4")
	req2 := newTestRequest("session-B", "openai", "gpt-4")

	_, err1 := coord.ExecuteRequest(context.Background(), req1, provider)
	_, err2 := coord.ExecuteRequest(context.Background(), req2, provider)

	if err1 != nil {
		t.Errorf("session-A request failed: %v", err1)
	}
	if err2 != nil {
		t.Errorf("session-B request failed: %v", err2)
	}

	stats := coord.Stats()
	if stats.SessionCount() != 2 {
		t.Errorf("expected 2 sessions, got %d", stats.SessionCount())
	}
}

func TestCoordinatorStats_Methods(t *testing.T) {
	stats := NewCoordinatorStats()

	if stats.SessionCount() != 0 {
		t.Errorf("expected 0 sessions, got %d", stats.SessionCount())
	}
	if stats.ProviderCount() != 0 {
		t.Errorf("expected 0 providers, got %d", stats.ProviderCount())
	}

	stats.Sessions["session-1"] = bulkhead.HierarchyStats{SessionID: "session-1"}
	stats.Providers["openai"] = ProviderStats{HealthScore: 0.95}

	if stats.SessionCount() != 1 {
		t.Errorf("expected 1 session, got %d", stats.SessionCount())
	}
	if stats.ProviderCount() != 1 {
		t.Errorf("expected 1 provider, got %d", stats.ProviderCount())
	}
}

func TestProviderStats_IsHealthy_Coordinator(t *testing.T) {
	tests := []struct {
		name      string
		score     float64
		threshold float64
		expected  bool
	}{
		{"healthy above threshold", 0.9, 0.5, true},
		{"healthy at threshold", 0.5, 0.5, true},
		{"unhealthy below threshold", 0.3, 0.5, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps := ProviderStats{HealthScore: tt.score}
			result := ps.IsHealthy(tt.threshold)
			if result != tt.expected {
				t.Errorf("IsHealthy(%f) = %v, expected %v", tt.threshold, result, tt.expected)
			}
		})
	}
}

func TestLLMRequest_Methods(t *testing.T) {
	req := NewLLMRequest("session-1", "task-1", "openai", "gpt-4")

	if req.SessionID != "session-1" {
		t.Errorf("expected session ID 'session-1', got %q", req.SessionID)
	}
	if req.TaskID != "task-1" {
		t.Errorf("expected task ID 'task-1', got %q", req.TaskID)
	}
	if req.Provider != "openai" {
		t.Errorf("expected provider 'openai', got %q", req.Provider)
	}
	if req.Model != "gpt-4" {
		t.Errorf("expected model 'gpt-4', got %q", req.Model)
	}

	req.AddMessage("user", "Hello")
	if len(req.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(req.Messages))
	}

	req.WithStreaming(true)
	if !req.Stream {
		t.Error("expected streaming to be enabled")
	}
}

func TestLLMRequest_Validate_Coordinator(t *testing.T) {
	tests := []struct {
		name      string
		req       *LLMRequest
		expectErr error
	}{
		{
			name:      "valid request",
			req:       NewLLMRequest("session-1", "task-1", "openai", "gpt-4"),
			expectErr: nil,
		},
		{
			name:      "missing session ID",
			req:       NewLLMRequest("", "task-1", "openai", "gpt-4"),
			expectErr: ErrMissingSessionID,
		},
		{
			name:      "missing provider",
			req:       NewLLMRequest("session-1", "task-1", "", "gpt-4"),
			expectErr: ErrMissingProvider,
		},
		{
			name:      "missing model",
			req:       NewLLMRequest("session-1", "task-1", "openai", ""),
			expectErr: ErrMissingModel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.expectErr == nil && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
			if tt.expectErr != nil && !errors.Is(err, tt.expectErr) {
				t.Errorf("expected error %v, got %v", tt.expectErr, err)
			}
		})
	}
}

func TestLLMResponse_Methods(t *testing.T) {
	resp := NewLLMResponse("content", 100*time.Millisecond)
	if resp.Content != "content" {
		t.Errorf("expected content 'content', got %q", resp.Content)
	}
	if resp.Latency != 100*time.Millisecond {
		t.Errorf("expected latency 100ms, got %v", resp.Latency)
	}
	if resp.IsStreaming() {
		t.Error("expected non-streaming response")
	}

	resp.WithUsage(100, 50)
	if resp.Usage.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 50 {
		t.Errorf("expected 50 output tokens, got %d", resp.Usage.OutputTokens)
	}
	if resp.Usage.TotalTokens() != 150 {
		t.Errorf("expected 150 total tokens, got %d", resp.Usage.TotalTokens())
	}
}

func TestNewStreamingLLMResponse(t *testing.T) {
	resp := NewStreamingLLMResponse(10)
	if !resp.IsStreaming() {
		t.Error("expected streaming response")
	}
	if cap(resp.Chunks) != 10 {
		t.Errorf("expected channel capacity 10, got %d", cap(resp.Chunks))
	}
}

func TestTokenUsage_Methods(t *testing.T) {
	usage := TokenUsage{InputTokens: 100, OutputTokens: 50}
	if usage.TotalTokens() != 150 {
		t.Errorf("expected 150 total tokens, got %d", usage.TotalTokens())
	}
	if usage.IsEmpty() {
		t.Error("expected non-empty usage")
	}

	emptyUsage := TokenUsage{}
	if !emptyUsage.IsEmpty() {
		t.Error("expected empty usage")
	}
}

func TestMessage_Methods(t *testing.T) {
	msg := NewMessage("user", "Hello")
	if msg.Role != "user" {
		t.Errorf("expected role 'user', got %q", msg.Role)
	}
	if msg.Content != "Hello" {
		t.Errorf("expected content 'Hello', got %q", msg.Content)
	}
	if msg.IsEmpty() {
		t.Error("expected non-empty message")
	}

	emptyMsg := NewMessage("user", "")
	if !emptyMsg.IsEmpty() {
		t.Error("expected empty message")
	}
}

func TestCoordinatorConfig_Validate_Coordinator(t *testing.T) {
	config := DefaultCoordinatorConfig()
	if err := config.Validate(); err != nil {
		t.Errorf("default config should be valid: %v", err)
	}

	invalidConfig := CoordinatorConfig{}
	if err := invalidConfig.Validate(); !errors.Is(err, ErrNilBulkheadConfigs) {
		t.Errorf("expected ErrNilBulkheadConfigs, got %v", err)
	}
}

// Benchmark tests

func BenchmarkExecuteRequest(b *testing.B) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)
	defer coord.Stop()

	provider := newMockProvider("openai")
	req := newTestRequest("session-1", "openai", "gpt-4")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = coord.ExecuteRequest(context.Background(), req, provider)
	}
}

func BenchmarkGetOrCreateBulkhead(b *testing.B) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)
	defer coord.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = coord.getOrCreateBulkhead("session-1")
	}
}

func BenchmarkGetOrCreateRateLimiter(b *testing.B) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)
	defer coord.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = coord.getOrCreateRateLimiter("openai")
	}
}

func BenchmarkStats(b *testing.B) {
	budget := newMockBudgetGetter()
	dispatcher := newMockDispatcher()
	coord := newTestCoordinator(budget, dispatcher)
	defer coord.Stop()

	provider := newMockProvider("openai")

	// Setup some state
	for i := 0; i < 5; i++ {
		req := newTestRequest("session-"+string(rune('A'+i)), "openai", "gpt-4")
		_, _ = coord.ExecuteRequest(context.Background(), req, provider)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = coord.Stats()
	}
}
