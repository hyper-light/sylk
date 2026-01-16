package archivalist

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
)

// AnthropicMessagesClient defines the interface for Anthropic API calls
// This allows mocking the Anthropic client in tests
type AnthropicMessagesClient interface {
	New(ctx context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error)
}

// RealAnthropicClient wraps the real Anthropic client
type RealAnthropicClient struct {
	messages *anthropic.MessageService
}

// NewRealAnthropicClient creates a wrapper around the real Anthropic client
func NewRealAnthropicClient(client *anthropic.Client) *RealAnthropicClient {
	return &RealAnthropicClient{messages: &client.Messages}
}

// New calls the real Anthropic API
func (r *RealAnthropicClient) New(ctx context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
	return r.messages.New(ctx, params)
}

// MockAnthropicClient provides a mock implementation for testing
type MockAnthropicClient struct {
	Response *anthropic.Message
	Error    error
	Calls    []anthropic.MessageNewParams
}

// NewMockAnthropicClient creates a new mock client
func NewMockAnthropicClient() *MockAnthropicClient {
	return &MockAnthropicClient{
		Calls: make([]anthropic.MessageNewParams, 0),
	}
}

// New records the call and returns the configured response
func (m *MockAnthropicClient) New(ctx context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
	m.Calls = append(m.Calls, params)
	if m.Error != nil {
		return nil, m.Error
	}
	return m.Response, nil
}

// SetResponse configures the mock to return a specific response
func (m *MockAnthropicClient) SetResponse(content string, inputTokens, outputTokens int64) {
	m.Response = &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{
			{Type: "text", Text: content},
		},
		Usage: anthropic.Usage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
		},
	}
}

// SetError configures the mock to return an error
func (m *MockAnthropicClient) SetError(err error) {
	m.Error = err
}

// Reset clears all recorded calls and responses
func (m *MockAnthropicClient) Reset() {
	m.Calls = m.Calls[:0]
	m.Response = nil
	m.Error = nil
}

// CallCount returns the number of API calls made
func (m *MockAnthropicClient) CallCount() int {
	return len(m.Calls)
}
