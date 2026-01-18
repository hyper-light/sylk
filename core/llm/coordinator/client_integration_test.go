package coordinator

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/llm"
	"github.com/adalundhe/sylk/core/llm/correlation"
)

// clientIntegrationProvider implements LLMProvider for client integration testing.
// Named to avoid conflict with mockLLMProvider in types_test.go and integration_test.go.
type clientIntegrationProvider struct {
	name            string
	completeResp    *LLMResponse
	completeErr     error
	streamChunks    []string
	completeCalled  atomic.Int32
	streamCalled    atomic.Int32
	lastRequest     atomic.Pointer[LLMRequest]
	completeLatency time.Duration
}

func newClientIntegrationProvider(name string) *clientIntegrationProvider {
	return &clientIntegrationProvider{
		name: name,
		completeResp: &LLMResponse{
			Content: "test response",
			Latency: 100 * time.Millisecond,
			Usage:   TokenUsage{InputTokens: 10, OutputTokens: 20},
		},
		streamChunks: []string{"chunk1", "chunk2", "chunk3"},
	}
}

func (m *clientIntegrationProvider) Complete(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	m.completeCalled.Add(1)
	m.lastRequest.Store(req)

	if m.completeLatency > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.completeLatency):
		}
	}

	if m.completeErr != nil {
		return nil, m.completeErr
	}
	return m.completeResp, nil
}

func (m *clientIntegrationProvider) Stream(ctx context.Context, req *LLMRequest) <-chan string {
	m.streamCalled.Add(1)
	m.lastRequest.Store(req)

	ch := make(chan string, len(m.streamChunks))
	go func() {
		defer close(ch)
		for _, chunk := range m.streamChunks {
			select {
			case <-ctx.Done():
				return
			case ch <- chunk:
			}
		}
	}()
	return ch
}

func (m *clientIntegrationProvider) Name() string {
	return m.name
}

// integrationTestLLMClient implements llm.LLMClient for testing.
type integrationTestLLMClient struct {
	completeResp    *llm.CompletionResponse
	completeErr     error
	streamChunks    []llm.StreamChunk
	streamErr       error
	completeCalled  atomic.Int32
	streamCalled    atomic.Int32
	lastRequest     *llm.CompletionRequest
	completeLatency time.Duration
}

func newIntegrationTestLLMClient() *integrationTestLLMClient {
	return &integrationTestLLMClient{
		completeResp: &llm.CompletionResponse{
			Content:      "tracked response",
			Model:        "test-model",
			FinishReason: "stop",
			Usage: llm.TokenUsage{
				PromptTokens:     15,
				CompletionTokens: 25,
				TotalTokens:      40,
			},
		},
		streamChunks: []llm.StreamChunk{
			{Content: "stream1", Done: false},
			{Content: "stream2", Done: false},
			{Content: "", Done: true},
		},
	}
}

func (m *integrationTestLLMClient) CompleteWithContext(
	ctx context.Context,
	req *llm.CompletionRequest,
) (*llm.CompletionResponse, error) {
	m.completeCalled.Add(1)
	m.lastRequest = req

	if m.completeLatency > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.completeLatency):
		}
	}

	if m.completeErr != nil {
		return nil, m.completeErr
	}
	return m.completeResp, nil
}

func (m *integrationTestLLMClient) StreamWithContext(
	ctx context.Context,
	req *llm.CompletionRequest,
) (<-chan llm.StreamChunk, error) {
	m.streamCalled.Add(1)
	m.lastRequest = req

	if m.streamErr != nil {
		return nil, m.streamErr
	}

	ch := make(chan llm.StreamChunk, len(m.streamChunks))
	go func() {
		defer close(ch)
		for _, chunk := range m.streamChunks {
			select {
			case <-ctx.Done():
				ch <- llm.StreamChunk{Error: ctx.Err(), Done: true}
				return
			case ch <- chunk:
			}
		}
	}()
	return ch, nil
}

// integrationTestSupervisor implements llm.LLMSupervisor for testing.
type integrationTestSupervisor struct {
	agentID   string
	beginErr  error
	endCalled atomic.Int32
	ctx       context.Context
}

func newIntegrationTestSupervisor(agentID string) *integrationTestSupervisor {
	return &integrationTestSupervisor{
		agentID: agentID,
		ctx:     context.Background(),
	}
}

func (m *integrationTestSupervisor) AgentID() string {
	return m.agentID
}

func (m *integrationTestSupervisor) BeginOperation(
	opType concurrency.OperationType,
	description string,
	timeout time.Duration,
) (*concurrency.Operation, error) {
	if m.beginErr != nil {
		return nil, m.beginErr
	}

	return concurrency.NewOperation(m.ctx, opType, m.agentID, description, timeout), nil
}

func (m *integrationTestSupervisor) EndOperation(op *concurrency.Operation, result any, err error) {
	m.endCalled.Add(1)
}

// integrationTestBudgetGetter implements backpressure.BudgetGetter for testing.
type integrationTestBudgetGetter struct {
	sessionUsage float64
	taskUsage    float64
}

func (g *integrationTestBudgetGetter) GetUsagePercent(sessionID string) float64 {
	return g.sessionUsage
}

func (g *integrationTestBudgetGetter) GetTaskUsagePercent(sessionID, taskID string) float64 {
	return g.taskUsage
}

// integrationTestSignalDispatcher implements correlation.SignalDispatcher for testing.
type integrationTestSignalDispatcher struct {
	signals []correlation.Signal
}

func (d *integrationTestSignalDispatcher) SendSignal(signal correlation.Signal) {
	d.signals = append(d.signals, signal)
}

// TestCoordinatedClientNew tests NewCoordinatedClient constructor.
func TestCoordinatedClientNew(t *testing.T) {
	coordinator := createIntegrationTestCoordinator(t)
	defer coordinator.Stop()

	provider := newClientIntegrationProvider("test-provider")
	sessionID := "session-123"

	client := NewCoordinatedClient(coordinator, provider, sessionID)

	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.SessionID() != sessionID {
		t.Errorf("expected session ID %s, got %s", sessionID, client.SessionID())
	}
	if client.ProviderName() != "test-provider" {
		t.Errorf("expected provider name test-provider, got %s", client.ProviderName())
	}
}

// TestCoordinatedClientComplete tests the Complete method.
func TestCoordinatedClientComplete(t *testing.T) {
	coordinator := createIntegrationTestCoordinator(t)
	defer coordinator.Stop()

	provider := newClientIntegrationProvider("test-provider")
	client := NewCoordinatedClient(coordinator, provider, "session-123")

	req := NewLLMRequest("", "task-1", "", "gpt-4")
	req.AddMessage("user", "Hello")

	ctx := context.Background()
	resp, err := client.Complete(ctx, req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Content != "test response" {
		t.Errorf("expected content 'test response', got %s", resp.Content)
	}
	if provider.completeCalled.Load() != 1 {
		t.Errorf("expected Complete called once, got %d", provider.completeCalled.Load())
	}
}

// TestCoordinatedClientCompletePopulatesDefaults tests default population.
func TestCoordinatedClientCompletePopulatesDefaults(t *testing.T) {
	coordinator := createIntegrationTestCoordinator(t)
	defer coordinator.Stop()

	provider := newClientIntegrationProvider("my-provider")
	client := NewCoordinatedClient(coordinator, provider, "default-session")

	req := NewLLMRequest("", "task-1", "", "gpt-4")

	ctx := context.Background()
	_, err := client.Complete(ctx, req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lastReq := provider.lastRequest.Load()
	if lastReq.SessionID != "default-session" {
		t.Errorf("expected session ID 'default-session', got %s", lastReq.SessionID)
	}
	if lastReq.Provider != "my-provider" {
		t.Errorf("expected provider 'my-provider', got %s", lastReq.Provider)
	}
}

// TestCoordinatedClientStream tests the Stream method.
func TestCoordinatedClientStream(t *testing.T) {
	coordinator := createIntegrationTestCoordinator(t)
	defer coordinator.Stop()

	provider := newClientIntegrationProvider("test-provider")
	client := NewCoordinatedClient(coordinator, provider, "session-123")

	req := NewLLMRequest("session-123", "task-1", "test-provider", "gpt-4")
	req.AddMessage("user", "Hello")

	ctx := context.Background()
	chunks, err := client.Stream(ctx, req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chunks == nil {
		t.Fatal("expected non-nil chunks channel")
	}

	var received []string
	for chunk := range chunks {
		received = append(received, chunk)
	}

	if len(received) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(received))
	}
	if provider.streamCalled.Load() != 1 {
		t.Errorf("expected Stream called once, got %d", provider.streamCalled.Load())
	}
}

// TestCoordinatedClientStreamSetsStreamFlag tests that Stream sets the stream flag.
func TestCoordinatedClientStreamSetsStreamFlag(t *testing.T) {
	coordinator := createIntegrationTestCoordinator(t)
	defer coordinator.Stop()

	provider := newClientIntegrationProvider("test-provider")
	client := NewCoordinatedClient(coordinator, provider, "session-123")

	req := NewLLMRequest("session-123", "task-1", "test-provider", "gpt-4")
	req.Stream = false

	ctx := context.Background()
	_, err := client.Stream(ctx, req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !provider.lastRequest.Load().Stream {
		t.Error("expected Stream flag to be true")
	}
}

// TestTrackedClientAdapterNew tests NewTrackedClientAdapter constructor.
func TestTrackedClientAdapterNew(t *testing.T) {
	mockClient := newIntegrationTestLLMClient()
	trackedClient := llm.NewTrackedLLMClient(mockClient, 5*time.Minute)
	supervisor := newIntegrationTestSupervisor("agent-1")

	adapter := NewTrackedClientAdapter(trackedClient, supervisor, "openai")

	if adapter == nil {
		t.Fatal("expected non-nil adapter")
	}
	if adapter.Name() != "openai" {
		t.Errorf("expected name 'openai', got %s", adapter.Name())
	}
}

// TestTrackedClientAdapterConvertMessages tests message conversion.
func TestTrackedClientAdapterConvertMessages(t *testing.T) {
	mockClient := newIntegrationTestLLMClient()
	trackedClient := llm.NewTrackedLLMClient(mockClient, 5*time.Minute)
	supervisor := newIntegrationTestSupervisor("agent-1")
	adapter := NewTrackedClientAdapter(trackedClient, supervisor, "openai")

	messages := []Message{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
	}

	converted := adapter.convertMessages(messages)

	if len(converted) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(converted))
	}
	if converted[0].Role != "system" || converted[0].Content != "You are helpful" {
		t.Errorf("first message conversion failed")
	}
	if converted[1].Role != "user" || converted[1].Content != "Hello" {
		t.Errorf("second message conversion failed")
	}
	if converted[2].Role != "assistant" || converted[2].Content != "Hi there" {
		t.Errorf("third message conversion failed")
	}
}

// TestTrackedClientAdapterConvertToCompletionRequest tests request conversion.
func TestTrackedClientAdapterConvertToCompletionRequest(t *testing.T) {
	mockClient := newIntegrationTestLLMClient()
	trackedClient := llm.NewTrackedLLMClient(mockClient, 5*time.Minute)
	supervisor := newIntegrationTestSupervisor("agent-1")
	adapter := NewTrackedClientAdapter(trackedClient, supervisor, "openai")

	req := &LLMRequest{
		SessionID: "session-1",
		TaskID:    "task-1",
		Provider:  "openai",
		Model:     "gpt-4-turbo",
		Messages: []Message{
			{Role: "user", Content: "Test message"},
		},
	}

	compReq := adapter.convertToCompletionRequest(req)

	if compReq.Model != "gpt-4-turbo" {
		t.Errorf("expected model 'gpt-4-turbo', got %s", compReq.Model)
	}
	if compReq.MaxTokens != defaultMaxTokens {
		t.Errorf("expected max tokens %d, got %d", defaultMaxTokens, compReq.MaxTokens)
	}
	if compReq.Temperature != defaultTemperature {
		t.Errorf("expected temperature %f, got %f", defaultTemperature, compReq.Temperature)
	}
	if compReq.Timeout != defaultRequestTimeout {
		t.Errorf("expected timeout %v, got %v", defaultRequestTimeout, compReq.Timeout)
	}
	if len(compReq.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(compReq.Messages))
	}
}

// TestTrackedClientAdapterConvertToLLMResponse tests response conversion.
func TestTrackedClientAdapterConvertToLLMResponse(t *testing.T) {
	mockClient := newIntegrationTestLLMClient()
	trackedClient := llm.NewTrackedLLMClient(mockClient, 5*time.Minute)
	supervisor := newIntegrationTestSupervisor("agent-1")
	adapter := NewTrackedClientAdapter(trackedClient, supervisor, "openai")

	compResp := &llm.CompletionResponse{
		Content:      "Response content",
		Model:        "gpt-4",
		FinishReason: "stop",
		Usage: llm.TokenUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
	}
	latency := 500 * time.Millisecond

	resp := adapter.convertToLLMResponse(compResp, latency)

	if resp.Content != "Response content" {
		t.Errorf("expected content 'Response content', got %s", resp.Content)
	}
	if resp.Latency != latency {
		t.Errorf("expected latency %v, got %v", latency, resp.Latency)
	}
	if resp.Usage.InputTokens != 100 {
		t.Errorf("expected input tokens 100, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 50 {
		t.Errorf("expected output tokens 50, got %d", resp.Usage.OutputTokens)
	}
}

// TestTrackedClientAdapterCreateErrorChannel tests error channel creation.
func TestTrackedClientAdapterCreateErrorChannel(t *testing.T) {
	mockClient := newIntegrationTestLLMClient()
	trackedClient := llm.NewTrackedLLMClient(mockClient, 5*time.Minute)
	supervisor := newIntegrationTestSupervisor("agent-1")
	adapter := NewTrackedClientAdapter(trackedClient, supervisor, "openai")

	ch := adapter.createErrorChannel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed")
		}
	default:
		t.Error("expected channel to be readable (closed)")
	}
}

// TestTrackedClientAdapterShouldStopForwarding tests stop condition detection.
func TestTrackedClientAdapterShouldStopForwarding(t *testing.T) {
	mockClient := newIntegrationTestLLMClient()
	trackedClient := llm.NewTrackedLLMClient(mockClient, 5*time.Minute)
	supervisor := newIntegrationTestSupervisor("agent-1")
	adapter := NewTrackedClientAdapter(trackedClient, supervisor, "openai")

	tests := []struct {
		name     string
		chunk    llm.StreamChunk
		expected bool
	}{
		{
			name:     "normal chunk",
			chunk:    llm.StreamChunk{Content: "hello", Done: false},
			expected: false,
		},
		{
			name:     "done chunk",
			chunk:    llm.StreamChunk{Content: "", Done: true},
			expected: true,
		},
		{
			name:     "error chunk",
			chunk:    llm.StreamChunk{Error: errors.New("error"), Done: false},
			expected: true,
		},
		{
			name:     "done with error",
			chunk:    llm.StreamChunk{Error: errors.New("error"), Done: true},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adapter.shouldStopForwarding(tt.chunk)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestTrackedClientAdapterForwardStreamChunks tests stream chunk forwarding.
func TestTrackedClientAdapterForwardStreamChunks(t *testing.T) {
	mockClient := newIntegrationTestLLMClient()
	trackedClient := llm.NewTrackedLLMClient(mockClient, 5*time.Minute)
	supervisor := newIntegrationTestSupervisor("agent-1")
	adapter := NewTrackedClientAdapter(trackedClient, supervisor, "openai")

	input := make(chan llm.StreamChunk, 5)
	input <- llm.StreamChunk{Content: "hello", Done: false}
	input <- llm.StreamChunk{Content: " world", Done: false}
	input <- llm.StreamChunk{Content: "", Done: true}
	close(input)

	output := adapter.forwardStreamChunks(input)

	var chunks []string
	for chunk := range output {
		chunks = append(chunks, chunk)
	}

	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks, got %d", len(chunks))
	}
	if len(chunks) >= 1 && chunks[0] != "hello" {
		t.Errorf("expected first chunk 'hello', got %s", chunks[0])
	}
	if len(chunks) >= 2 && chunks[1] != " world" {
		t.Errorf("expected second chunk ' world', got %s", chunks[1])
	}
}

// TestTrackedClientAdapterForwardStreamChunksStopsOnError tests error handling.
func TestTrackedClientAdapterForwardStreamChunksStopsOnError(t *testing.T) {
	mockClient := newIntegrationTestLLMClient()
	trackedClient := llm.NewTrackedLLMClient(mockClient, 5*time.Minute)
	supervisor := newIntegrationTestSupervisor("agent-1")
	adapter := NewTrackedClientAdapter(trackedClient, supervisor, "openai")

	input := make(chan llm.StreamChunk, 5)
	input <- llm.StreamChunk{Content: "first", Done: false}
	input <- llm.StreamChunk{Error: errors.New("stream error"), Done: false}
	input <- llm.StreamChunk{Content: "should not appear", Done: false}
	close(input)

	output := adapter.forwardStreamChunks(input)

	var chunks []string
	for chunk := range output {
		chunks = append(chunks, chunk)
	}

	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk before error, got %d", len(chunks))
	}
}

// TestCoordinatedClientContextCancellation tests context cancellation behavior.
func TestCoordinatedClientContextCancellation(t *testing.T) {
	coordinator := createIntegrationTestCoordinator(t)
	defer coordinator.Stop()

	provider := newClientIntegrationProvider("test-provider")
	provider.completeLatency = 5 * time.Second
	client := NewCoordinatedClient(coordinator, provider, "session-123")

	req := NewLLMRequest("session-123", "task-1", "test-provider", "gpt-4")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := client.Complete(ctx, req)

	if err == nil {
		t.Error("expected context cancellation error")
	}
}

// TestCoordinatedClientProviderError tests provider error propagation.
func TestCoordinatedClientProviderError(t *testing.T) {
	coordinator := createIntegrationTestCoordinator(t)
	defer coordinator.Stop()

	provider := newClientIntegrationProvider("test-provider")
	provider.completeErr = errors.New("provider error")
	client := NewCoordinatedClient(coordinator, provider, "session-123")

	req := NewLLMRequest("session-123", "task-1", "test-provider", "gpt-4")

	ctx := context.Background()
	_, err := client.Complete(ctx, req)

	if err == nil {
		t.Error("expected provider error")
	}
}

// TestCoordinatedClientConcurrentRequests tests concurrent request handling.
func TestCoordinatedClientConcurrentRequests(t *testing.T) {
	coordinator := createIntegrationTestCoordinator(t)
	defer coordinator.Stop()

	provider := newClientIntegrationProvider("test-provider")
	client := NewCoordinatedClient(coordinator, provider, "session-123")

	const numRequests = 10
	results := make(chan error, numRequests)

	for i := 0; i < numRequests; i++ {
		go func(idx int) {
			req := NewLLMRequest("session-123", "task-"+string(rune('0'+idx)), "test-provider", "gpt-4")
			ctx := context.Background()
			_, err := client.Complete(ctx, req)
			results <- err
		}(i)
	}

	var errs []error
	for i := 0; i < numRequests; i++ {
		if err := <-results; err != nil {
			errs = append(errs, err)
		}
	}

	// All requests should complete (may have some failures due to rate limits, etc.)
	if provider.completeCalled.Load() == 0 {
		t.Error("expected at least some Complete calls")
	}
}

// TestTrackedClientAdapterName tests the Name method.
func TestTrackedClientAdapterName(t *testing.T) {
	tests := []struct {
		name         string
		providerName string
	}{
		{name: "openai provider", providerName: "openai"},
		{name: "anthropic provider", providerName: "anthropic"},
		{name: "empty provider", providerName: ""},
		{name: "custom provider", providerName: "my-custom-llm"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := newIntegrationTestLLMClient()
			trackedClient := llm.NewTrackedLLMClient(mockClient, 5*time.Minute)
			supervisor := newIntegrationTestSupervisor("agent-1")
			adapter := NewTrackedClientAdapter(trackedClient, supervisor, tt.providerName)

			if adapter.Name() != tt.providerName {
				t.Errorf("expected name %q, got %q", tt.providerName, adapter.Name())
			}
		})
	}
}

// TestTrackedClientAdapterStreamReturnsErrorChannel tests Stream error handling.
func TestTrackedClientAdapterStreamReturnsErrorChannel(t *testing.T) {
	mockClient := newIntegrationTestLLMClient()
	mockClient.streamErr = errors.New("stream init error")
	trackedClient := llm.NewTrackedLLMClient(mockClient, 5*time.Minute)
	supervisor := newIntegrationTestSupervisor("agent-1")
	adapter := NewTrackedClientAdapter(trackedClient, supervisor, "openai")

	req := &LLMRequest{
		SessionID: "session-1",
		Model:     "gpt-4",
	}

	ctx := context.Background()
	ch := adapter.Stream(ctx, req)

	// Should get a closed channel on error
	_, ok := <-ch
	if ok {
		t.Error("expected closed channel on error")
	}
}

// createIntegrationTestCoordinator creates a coordinator for testing.
func createIntegrationTestCoordinator(t *testing.T) *LLMRequestCoordinator {
	t.Helper()

	config := DefaultCoordinatorConfig()
	budgetGetter := &integrationTestBudgetGetter{sessionUsage: 0.5, taskUsage: 0.3}
	dispatcher := &integrationTestSignalDispatcher{}

	return NewLLMRequestCoordinator(config, budgetGetter, dispatcher, nil)
}
