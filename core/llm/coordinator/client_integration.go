// Package coordinator provides the LLM request coordination system.
// This file integrates the coordinator with existing LLM client infrastructure.
package coordinator

import (
	"context"
	"time"

	"github.com/adalundhe/sylk/core/llm"
)

// streamingChunkBufferSize is the buffer size for streaming response channels.
const streamingChunkBufferSize = 100

// defaultMaxTokens is the default max tokens for requests without explicit setting.
const defaultMaxTokens = 4096

// defaultTemperature is the default temperature for requests without explicit setting.
const defaultTemperature = 0.7

// defaultRequestTimeout is the default timeout for LLM requests.
const defaultRequestTimeout = 2 * time.Minute

// CoordinatedClient wraps an existing LLM client with coordinator protections.
// It ensures all requests pass through health checks, rate limiting, backpressure,
// and bulkhead isolation before reaching the underlying provider.
type CoordinatedClient struct {
	coordinator *LLMRequestCoordinator
	provider    LLMProvider
	sessionID   string
}

// NewCoordinatedClient creates a client that routes through the coordinator.
// The coordinator provides all protection layers while the provider handles actual LLM calls.
func NewCoordinatedClient(
	coordinator *LLMRequestCoordinator,
	provider LLMProvider,
	sessionID string,
) *CoordinatedClient {
	return &CoordinatedClient{
		coordinator: coordinator,
		provider:    provider,
		sessionID:   sessionID,
	}
}

// Complete executes a non-streaming request with all protections.
// The request passes through health checks, rate limits, and backpressure before execution.
func (cc *CoordinatedClient) Complete(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	cc.populateRequestDefaults(req)
	return cc.coordinator.ExecuteRequest(ctx, req, cc.provider)
}

// Stream executes a streaming request with all protections.
// Returns a channel of string chunks that will be closed when the stream completes.
func (cc *CoordinatedClient) Stream(ctx context.Context, req *LLMRequest) (<-chan string, error) {
	cc.populateRequestDefaults(req)
	req.Stream = true

	resp, err := cc.coordinator.ExecuteRequest(ctx, req, cc.provider)
	if err != nil {
		return nil, err
	}

	return resp.Chunks, nil
}

// populateRequestDefaults sets session ID and provider if not already set.
func (cc *CoordinatedClient) populateRequestDefaults(req *LLMRequest) {
	if req.SessionID == "" {
		req.SessionID = cc.sessionID
	}
	if req.Provider == "" {
		req.Provider = cc.provider.Name()
	}
}

// SessionID returns the session ID associated with this client.
func (cc *CoordinatedClient) SessionID() string {
	return cc.sessionID
}

// ProviderName returns the name of the underlying provider.
func (cc *CoordinatedClient) ProviderName() string {
	return cc.provider.Name()
}

// TrackedClientAdapter adapts TrackedLLMClient to the LLMProvider interface.
// This allows existing TrackedLLMClient instances to be used with the coordinator.
type TrackedClientAdapter struct {
	client       *llm.TrackedLLMClient
	supervisor   llm.LLMSupervisor
	providerName string
}

// NewTrackedClientAdapter creates an adapter that wraps a TrackedLLMClient.
// The supervisor is required for operation tracking within the TrackedLLMClient.
func NewTrackedClientAdapter(
	client *llm.TrackedLLMClient,
	supervisor llm.LLMSupervisor,
	providerName string,
) *TrackedClientAdapter {
	return &TrackedClientAdapter{
		client:       client,
		supervisor:   supervisor,
		providerName: providerName,
	}
}

// Complete executes a non-streaming LLM request through the tracked client.
func (a *TrackedClientAdapter) Complete(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	compReq := a.convertToCompletionRequest(req)
	start := time.Now()

	compResp, err := a.client.Complete(a.supervisor, compReq)
	if err != nil {
		return nil, err
	}

	return a.convertToLLMResponse(compResp, time.Since(start)), nil
}

// Stream executes a streaming LLM request through the tracked client.
// Returns a channel of content chunks that will be closed when streaming completes.
func (a *TrackedClientAdapter) Stream(ctx context.Context, req *LLMRequest) <-chan string {
	compReq := a.convertToCompletionRequest(req)

	chunks, err := a.client.Stream(a.supervisor, compReq)
	if err != nil {
		return a.createErrorChannel()
	}

	return a.forwardStreamChunks(chunks)
}

// Name returns the provider name for this adapter.
func (a *TrackedClientAdapter) Name() string {
	return a.providerName
}

// convertToCompletionRequest converts a coordinator LLMRequest to a tracked client CompletionRequest.
func (a *TrackedClientAdapter) convertToCompletionRequest(req *LLMRequest) *llm.CompletionRequest {
	return &llm.CompletionRequest{
		Model:       req.Model,
		Messages:    a.convertMessages(req.Messages),
		MaxTokens:   defaultMaxTokens,
		Temperature: defaultTemperature,
		Timeout:     defaultRequestTimeout,
	}
}

// convertMessages converts coordinator messages to tracked client messages.
func (a *TrackedClientAdapter) convertMessages(msgs []Message) []llm.Message {
	result := make([]llm.Message, len(msgs))
	for i, msg := range msgs {
		result[i] = llm.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}
	return result
}

// convertToLLMResponse converts a tracked client CompletionResponse to a coordinator LLMResponse.
func (a *TrackedClientAdapter) convertToLLMResponse(
	resp *llm.CompletionResponse,
	latency time.Duration,
) *LLMResponse {
	return &LLMResponse{
		Content: resp.Content,
		Latency: latency,
		Usage: TokenUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}
}

// createErrorChannel creates a closed channel for error cases.
func (a *TrackedClientAdapter) createErrorChannel() <-chan string {
	ch := make(chan string)
	close(ch)
	return ch
}

// forwardStreamChunks forwards StreamChunks to a string channel.
func (a *TrackedClientAdapter) forwardStreamChunks(chunks <-chan llm.StreamChunk) <-chan string {
	output := make(chan string, streamingChunkBufferSize)
	go a.runStreamForwarder(chunks, output)
	return output
}

// runStreamForwarder reads chunks and forwards content to the output channel.
func (a *TrackedClientAdapter) runStreamForwarder(input <-chan llm.StreamChunk, output chan<- string) {
	defer close(output)

	for chunk := range input {
		if a.shouldStopForwarding(chunk) {
			return
		}
		output <- chunk.Content
	}
}

// shouldStopForwarding determines if forwarding should stop based on chunk state.
func (a *TrackedClientAdapter) shouldStopForwarding(chunk llm.StreamChunk) bool {
	return chunk.Done || chunk.Error != nil
}
