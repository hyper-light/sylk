package coordinator

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/llm/backpressure"
	"github.com/adalundhe/sylk/core/llm/bulkhead"
	"github.com/adalundhe/sylk/core/llm/correlation"
	"github.com/adalundhe/sylk/core/llm/health"
	"github.com/adalundhe/sylk/core/llm/ratelimit"
	"github.com/adalundhe/sylk/core/llm/timeout"
)

// TestDefaultCoordinatorConfig tests that DefaultCoordinatorConfig returns valid defaults.
func TestDefaultCoordinatorConfig(t *testing.T) {
	t.Parallel()

	config := DefaultCoordinatorConfig()

	// Verify bulkhead configs are present
	if config.BulkheadConfigs == nil {
		t.Error("BulkheadConfigs should not be nil")
	}
	if len(config.BulkheadConfigs) == 0 {
		t.Error("BulkheadConfigs should not be empty")
	}

	// Verify all levels are present
	if _, ok := config.BulkheadConfigs[bulkhead.LevelSession]; !ok {
		t.Error("BulkheadConfigs should contain session level")
	}
	if _, ok := config.BulkheadConfigs[bulkhead.LevelProvider]; !ok {
		t.Error("BulkheadConfigs should contain provider level")
	}
	if _, ok := config.BulkheadConfigs[bulkhead.LevelModel]; !ok {
		t.Error("BulkheadConfigs should contain model level")
	}
}

// TestDefaultCoordinatorConfig_Idempotent tests that defaults are consistent.
func TestDefaultCoordinatorConfig_Idempotent(t *testing.T) {
	t.Parallel()

	config1 := DefaultCoordinatorConfig()
	config2 := DefaultCoordinatorConfig()

	// Compare rate limit config (struct comparison)
	if config1.RateLimitConfig != config2.RateLimitConfig {
		t.Error("RateLimitConfig should be identical between calls")
	}
	if config1.HealthConfig != config2.HealthConfig {
		t.Error("HealthConfig should be identical between calls")
	}
}

// TestCoordinatorConfig_Validate tests configuration validation.
func TestCoordinatorConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  CoordinatorConfig
		wantErr bool
	}{
		{
			name:    "valid default config",
			config:  DefaultCoordinatorConfig(),
			wantErr: false,
		},
		{
			name: "nil bulkhead configs",
			config: CoordinatorConfig{
				BulkheadConfigs:     nil,
				RateLimitConfig:     ratelimit.DefaultMultiLayerConfig(),
				HealthConfig:        health.DefaultHealthConfig(),
				BackpressureConfig:  backpressure.DefaultCostBackpressureConfig(),
				CorrelationConfig:   correlation.DefaultCorrelationConfig(),
				StreamTimeoutConfig: timeout.DefaultStreamingTimeoutConfig(),
			},
			wantErr: true,
		},
		{
			name: "invalid correlation config",
			config: CoordinatorConfig{
				BulkheadConfigs:    bulkhead.DefaultBulkheadConfigs(),
				RateLimitConfig:    ratelimit.DefaultMultiLayerConfig(),
				HealthConfig:       health.DefaultHealthConfig(),
				BackpressureConfig: backpressure.DefaultCostBackpressureConfig(),
				CorrelationConfig: correlation.CorrelationConfig{
					CorrelationWindow:    -1, // Invalid
					MinFailuresForGlobal: 3,
					GlobalBackoffBase:    5 * time.Second,
					GlobalBackoffMax:     60 * time.Second,
				},
				StreamTimeoutConfig: timeout.DefaultStreamingTimeoutConfig(),
			},
			wantErr: true,
		},
		{
			name: "invalid stream timeout config",
			config: CoordinatorConfig{
				BulkheadConfigs:    bulkhead.DefaultBulkheadConfigs(),
				RateLimitConfig:    ratelimit.DefaultMultiLayerConfig(),
				HealthConfig:       health.DefaultHealthConfig(),
				BackpressureConfig: backpressure.DefaultCostBackpressureConfig(),
				CorrelationConfig:  correlation.DefaultCorrelationConfig(),
				StreamTimeoutConfig: timeout.StreamingTimeoutConfig{
					FirstTokenTimeout: 0, // Invalid
				},
			},
			wantErr: true,
		},
		{
			name: "invalid backpressure config",
			config: CoordinatorConfig{
				BulkheadConfigs: bulkhead.DefaultBulkheadConfigs(),
				RateLimitConfig: ratelimit.DefaultMultiLayerConfig(),
				HealthConfig:    health.DefaultHealthConfig(),
				BackpressureConfig: backpressure.CostBackpressureConfig{
					BaseDelay:   -1, // Invalid
					RejectNewAt: 1.0,
				},
				CorrelationConfig:   correlation.DefaultCorrelationConfig(),
				StreamTimeoutConfig: timeout.DefaultStreamingTimeoutConfig(),
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.config.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// TestLLMRequest_NewLLMRequest tests LLMRequest creation.
func TestLLMRequest_NewLLMRequest(t *testing.T) {
	t.Parallel()

	req := NewLLMRequest("session-1", "task-1", "openai", "gpt-4")

	if req.SessionID != "session-1" {
		t.Errorf("SessionID = %q, want %q", req.SessionID, "session-1")
	}
	if req.TaskID != "task-1" {
		t.Errorf("TaskID = %q, want %q", req.TaskID, "task-1")
	}
	if req.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", req.Provider, "openai")
	}
	if req.Model != "gpt-4" {
		t.Errorf("Model = %q, want %q", req.Model, "gpt-4")
	}
	if req.Messages == nil {
		t.Error("Messages should be initialized")
	}
	if req.Stream != false {
		t.Error("Stream should default to false")
	}
}

// TestLLMRequest_AddMessage tests adding messages to a request.
func TestLLMRequest_AddMessage(t *testing.T) {
	t.Parallel()

	req := NewLLMRequest("session-1", "task-1", "openai", "gpt-4")
	req.AddMessage("user", "Hello")
	req.AddMessage("assistant", "Hi there!")

	if len(req.Messages) != 2 {
		t.Errorf("Messages length = %d, want 2", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("Messages[0].Role = %q, want %q", req.Messages[0].Role, "user")
	}
	if req.Messages[0].Content != "Hello" {
		t.Errorf("Messages[0].Content = %q, want %q", req.Messages[0].Content, "Hello")
	}
	if req.Messages[1].Role != "assistant" {
		t.Errorf("Messages[1].Role = %q, want %q", req.Messages[1].Role, "assistant")
	}
}

// TestLLMRequest_WithStreaming tests the fluent streaming setter.
func TestLLMRequest_WithStreaming(t *testing.T) {
	t.Parallel()

	req := NewLLMRequest("session-1", "task-1", "openai", "gpt-4").WithStreaming(true)

	if !req.Stream {
		t.Error("Stream should be true after WithStreaming(true)")
	}

	req = req.WithStreaming(false)
	if req.Stream {
		t.Error("Stream should be false after WithStreaming(false)")
	}
}

// TestLLMRequest_Validate tests request validation.
func TestLLMRequest_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *LLMRequest
		wantErr error
	}{
		{
			name:    "valid request",
			req:     NewLLMRequest("session-1", "task-1", "openai", "gpt-4"),
			wantErr: nil,
		},
		{
			name: "missing session ID",
			req: &LLMRequest{
				Provider: "openai",
				Model:    "gpt-4",
			},
			wantErr: ErrMissingSessionID,
		},
		{
			name: "missing provider",
			req: &LLMRequest{
				SessionID: "session-1",
				Model:     "gpt-4",
			},
			wantErr: ErrMissingProvider,
		},
		{
			name: "missing model",
			req: &LLMRequest{
				SessionID: "session-1",
				Provider:  "openai",
			},
			wantErr: ErrMissingModel,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.req.Validate()
			if err != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// TestMessage_NewMessage tests message creation.
func TestMessage_NewMessage(t *testing.T) {
	t.Parallel()

	msg := NewMessage("user", "Hello, world!")

	if msg.Role != "user" {
		t.Errorf("Role = %q, want %q", msg.Role, "user")
	}
	if msg.Content != "Hello, world!" {
		t.Errorf("Content = %q, want %q", msg.Content, "Hello, world!")
	}
}

// TestMessage_IsEmpty tests the IsEmpty method.
func TestMessage_IsEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		msg      Message
		expected bool
	}{
		{
			name:     "empty content",
			msg:      Message{Role: "user", Content: ""},
			expected: true,
		},
		{
			name:     "non-empty content",
			msg:      Message{Role: "user", Content: "Hello"},
			expected: false,
		},
		{
			name:     "whitespace only",
			msg:      Message{Role: "user", Content: "   "},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.msg.IsEmpty() != tc.expected {
				t.Errorf("IsEmpty() = %v, want %v", tc.msg.IsEmpty(), tc.expected)
			}
		})
	}
}

// TestLLMResponse_NewLLMResponse tests non-streaming response creation.
func TestLLMResponse_NewLLMResponse(t *testing.T) {
	t.Parallel()

	resp := NewLLMResponse("Hello, world!", 100*time.Millisecond)

	if resp.Content != "Hello, world!" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello, world!")
	}
	if resp.Latency != 100*time.Millisecond {
		t.Errorf("Latency = %v, want %v", resp.Latency, 100*time.Millisecond)
	}
	if resp.Chunks != nil {
		t.Error("Chunks should be nil for non-streaming response")
	}
}

// TestLLMResponse_NewStreamingLLMResponse tests streaming response creation.
func TestLLMResponse_NewStreamingLLMResponse(t *testing.T) {
	t.Parallel()

	resp := NewStreamingLLMResponse(100)

	if resp.Chunks == nil {
		t.Error("Chunks should not be nil for streaming response")
	}
	if cap(resp.Chunks) != 100 {
		t.Errorf("Chunks capacity = %d, want 100", cap(resp.Chunks))
	}
}

// TestLLMResponse_WithUsage tests the usage setter.
func TestLLMResponse_WithUsage(t *testing.T) {
	t.Parallel()

	resp := NewLLMResponse("test", time.Second).WithUsage(100, 50)

	if resp.Usage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", resp.Usage.OutputTokens)
	}
}

// TestLLMResponse_IsStreaming tests the streaming detection.
func TestLLMResponse_IsStreaming(t *testing.T) {
	t.Parallel()

	nonStreaming := NewLLMResponse("test", time.Second)
	if nonStreaming.IsStreaming() {
		t.Error("Non-streaming response should return false for IsStreaming()")
	}

	streaming := NewStreamingLLMResponse(10)
	if !streaming.IsStreaming() {
		t.Error("Streaming response should return true for IsStreaming()")
	}
}

// TestTokenUsage_TotalTokens tests total token calculation.
func TestTokenUsage_TotalTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		usage    TokenUsage
		expected int
	}{
		{
			name:     "typical usage",
			usage:    TokenUsage{InputTokens: 100, OutputTokens: 50},
			expected: 150,
		},
		{
			name:     "zero usage",
			usage:    TokenUsage{InputTokens: 0, OutputTokens: 0},
			expected: 0,
		},
		{
			name:     "input only",
			usage:    TokenUsage{InputTokens: 100, OutputTokens: 0},
			expected: 100,
		},
		{
			name:     "output only",
			usage:    TokenUsage{InputTokens: 0, OutputTokens: 50},
			expected: 50,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.usage.TotalTokens() != tc.expected {
				t.Errorf("TotalTokens() = %d, want %d", tc.usage.TotalTokens(), tc.expected)
			}
		})
	}
}

// TestTokenUsage_IsEmpty tests the empty usage detection.
func TestTokenUsage_IsEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		usage    TokenUsage
		expected bool
	}{
		{
			name:     "empty",
			usage:    TokenUsage{},
			expected: true,
		},
		{
			name:     "has input",
			usage:    TokenUsage{InputTokens: 1},
			expected: false,
		},
		{
			name:     "has output",
			usage:    TokenUsage{OutputTokens: 1},
			expected: false,
		},
		{
			name:     "has both",
			usage:    TokenUsage{InputTokens: 1, OutputTokens: 1},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.usage.IsEmpty() != tc.expected {
				t.Errorf("IsEmpty() = %v, want %v", tc.usage.IsEmpty(), tc.expected)
			}
		})
	}
}

// TestCoordinatorStats_NewCoordinatorStats tests stats creation.
func TestCoordinatorStats_NewCoordinatorStats(t *testing.T) {
	t.Parallel()

	stats := NewCoordinatorStats()

	if stats.Sessions == nil {
		t.Error("Sessions should be initialized")
	}
	if stats.Providers == nil {
		t.Error("Providers should be initialized")
	}
	if len(stats.Sessions) != 0 {
		t.Error("Sessions should be empty initially")
	}
	if len(stats.Providers) != 0 {
		t.Error("Providers should be empty initially")
	}
}

// TestCoordinatorStats_Counts tests the count methods.
func TestCoordinatorStats_Counts(t *testing.T) {
	t.Parallel()

	stats := NewCoordinatorStats()
	stats.Sessions["session-1"] = bulkhead.HierarchyStats{}
	stats.Sessions["session-2"] = bulkhead.HierarchyStats{}
	stats.Providers["openai"] = ProviderStats{HealthScore: 0.9}

	if stats.SessionCount() != 2 {
		t.Errorf("SessionCount() = %d, want 2", stats.SessionCount())
	}
	if stats.ProviderCount() != 1 {
		t.Errorf("ProviderCount() = %d, want 1", stats.ProviderCount())
	}
}

// TestProviderStats_IsHealthy tests provider health detection.
func TestProviderStats_IsHealthy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		stats     ProviderStats
		threshold float64
		expected  bool
	}{
		{
			name:      "healthy above threshold",
			stats:     ProviderStats{HealthScore: 0.9},
			threshold: 0.5,
			expected:  true,
		},
		{
			name:      "healthy at threshold",
			stats:     ProviderStats{HealthScore: 0.5},
			threshold: 0.5,
			expected:  true,
		},
		{
			name:      "unhealthy below threshold",
			stats:     ProviderStats{HealthScore: 0.3},
			threshold: 0.5,
			expected:  false,
		},
		{
			name:      "zero health",
			stats:     ProviderStats{HealthScore: 0},
			threshold: 0.1,
			expected:  false,
		},
		{
			name:      "perfect health",
			stats:     ProviderStats{HealthScore: 1.0},
			threshold: 1.0,
			expected:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.stats.IsHealthy(tc.threshold) != tc.expected {
				t.Errorf("IsHealthy(%v) = %v, want %v", tc.threshold, tc.stats.IsHealthy(tc.threshold), tc.expected)
			}
		})
	}
}

// TestErrors_ErrorTypes tests that all error types are distinct.
func TestErrors_ErrorTypes(t *testing.T) {
	t.Parallel()

	errors := []error{
		ErrGlobalBackoff,
		ErrProviderUnhealthy,
		ErrRateLimited,
		ErrBudgetExhausted,
		ErrMissingSessionID,
		ErrMissingProvider,
		ErrMissingModel,
		ErrNilBulkheadConfigs,
		ErrInvalidBackpressureConfig,
	}

	// Verify all errors are non-nil
	for _, err := range errors {
		if err == nil {
			t.Error("All error types should be non-nil")
		}
	}

	// Verify all errors have distinct messages
	seen := make(map[string]bool)
	for _, err := range errors {
		msg := err.Error()
		if seen[msg] {
			t.Errorf("Duplicate error message: %q", msg)
		}
		seen[msg] = true
	}
}

// TestErrors_ErrorMessages tests that error messages are meaningful.
func TestErrors_ErrorMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     error
		contain string
	}{
		{"global backoff", ErrGlobalBackoff, "global backoff"},
		{"provider unhealthy", ErrProviderUnhealthy, "health"},
		{"rate limited", ErrRateLimited, "rate"},
		{"budget exhausted", ErrBudgetExhausted, "budget"},
		{"missing session", ErrMissingSessionID, "session"},
		{"missing provider", ErrMissingProvider, "provider"},
		{"missing model", ErrMissingModel, "model"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			msg := tc.err.Error()
			if len(msg) == 0 {
				t.Error("Error message should not be empty")
			}
		})
	}
}

// mockLLMProvider is a test implementation of LLMProvider.
type mockLLMProvider struct {
	name        string
	response    *LLMResponse
	err         error
	streamChunk string
}

func (m *mockLLMProvider) Complete(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

func (m *mockLLMProvider) Stream(ctx context.Context, req *LLMRequest) <-chan string {
	ch := make(chan string, 1)
	go func() {
		defer close(ch)
		if m.streamChunk != "" {
			ch <- m.streamChunk
		}
	}()
	return ch
}

func (m *mockLLMProvider) Name() string {
	return m.name
}

// TestLLMProvider_Interface tests that the interface works correctly.
func TestLLMProvider_Interface(t *testing.T) {
	t.Parallel()

	resp := NewLLMResponse("Hello!", 50*time.Millisecond)
	provider := &mockLLMProvider{
		name:     "test-provider",
		response: resp,
	}

	var p LLMProvider = provider

	if p.Name() != "test-provider" {
		t.Errorf("Name() = %q, want %q", p.Name(), "test-provider")
	}

	ctx := context.Background()
	req := NewLLMRequest("session", "task", "test", "model")

	result, err := p.Complete(ctx, req)
	if err != nil {
		t.Errorf("Complete() error = %v", err)
	}
	if result.Content != "Hello!" {
		t.Errorf("Complete() content = %q, want %q", result.Content, "Hello!")
	}
}

// TestLLMProvider_Stream tests the streaming interface.
func TestLLMProvider_Stream(t *testing.T) {
	t.Parallel()

	provider := &mockLLMProvider{
		name:        "test-provider",
		streamChunk: "chunk",
	}

	var p LLMProvider = provider

	ctx := context.Background()
	req := NewLLMRequest("session", "task", "test", "model")

	ch := p.Stream(ctx, req)
	chunk := <-ch

	if chunk != "chunk" {
		t.Errorf("Stream() chunk = %q, want %q", chunk, "chunk")
	}

	// Verify channel is closed
	_, ok := <-ch
	if ok {
		t.Error("Stream() channel should be closed after all chunks")
	}
}

// TestLLMRequest_ZeroValue tests zero value behavior.
func TestLLMRequest_ZeroValue(t *testing.T) {
	t.Parallel()

	var req LLMRequest

	if req.SessionID != "" {
		t.Error("Zero value SessionID should be empty")
	}
	if req.Stream != false {
		t.Error("Zero value Stream should be false")
	}
	if req.Messages != nil {
		t.Error("Zero value Messages should be nil")
	}
}

// TestLLMResponse_ZeroValue tests zero value behavior.
func TestLLMResponse_ZeroValue(t *testing.T) {
	t.Parallel()

	var resp LLMResponse

	if resp.Content != "" {
		t.Error("Zero value Content should be empty")
	}
	if resp.Latency != 0 {
		t.Error("Zero value Latency should be 0")
	}
	if resp.Chunks != nil {
		t.Error("Zero value Chunks should be nil")
	}
	if !resp.Usage.IsEmpty() {
		t.Error("Zero value Usage should be empty")
	}
}

// TestCoordinatorConfig_ZeroValue tests zero value behavior.
func TestCoordinatorConfig_ZeroValue(t *testing.T) {
	t.Parallel()

	var config CoordinatorConfig

	if config.BulkheadConfigs != nil {
		t.Error("Zero value BulkheadConfigs should be nil")
	}

	// Zero value should fail validation
	err := config.Validate()
	if err == nil {
		t.Error("Zero value config should fail validation")
	}
}

// TestProviderStats_ZeroValue tests zero value behavior.
func TestProviderStats_ZeroValue(t *testing.T) {
	t.Parallel()

	var stats ProviderStats

	if stats.HealthScore != 0 {
		t.Error("Zero value HealthScore should be 0")
	}
	if stats.IsHealthy(0.1) {
		t.Error("Zero health score should not be healthy with positive threshold")
	}
}

// TestCoordinatorStats_ZeroValue tests zero value behavior.
func TestCoordinatorStats_ZeroValue(t *testing.T) {
	t.Parallel()

	var stats CoordinatorStats

	if stats.Sessions != nil {
		t.Error("Zero value Sessions should be nil")
	}
	if stats.Providers != nil {
		t.Error("Zero value Providers should be nil")
	}
	// SessionCount and ProviderCount should return 0 for nil maps
	if stats.SessionCount() != 0 {
		t.Error("SessionCount on nil map should be 0")
	}
	if stats.ProviderCount() != 0 {
		t.Error("ProviderCount on nil map should be 0")
	}
}

// TestMessage_ZeroValue tests zero value behavior.
func TestMessage_ZeroValue(t *testing.T) {
	t.Parallel()

	var msg Message

	if msg.Role != "" {
		t.Error("Zero value Role should be empty")
	}
	if msg.Content != "" {
		t.Error("Zero value Content should be empty")
	}
	if !msg.IsEmpty() {
		t.Error("Zero value message should be empty")
	}
}

// TestTokenUsage_ZeroValue tests zero value behavior.
func TestTokenUsage_ZeroValue(t *testing.T) {
	t.Parallel()

	var usage TokenUsage

	if usage.InputTokens != 0 {
		t.Error("Zero value InputTokens should be 0")
	}
	if usage.OutputTokens != 0 {
		t.Error("Zero value OutputTokens should be 0")
	}
	if usage.TotalTokens() != 0 {
		t.Error("Zero value TotalTokens should be 0")
	}
	if !usage.IsEmpty() {
		t.Error("Zero value usage should be empty")
	}
}
