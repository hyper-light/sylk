package archivalist

import (
	"context"
	"sync"

	"github.com/adalundhe/sylk/core/providers"
)

// MockProvider provides a mock implementation of archivalistProvider for testing
type MockProvider struct {
	mu       sync.Mutex
	Response *providers.Response
	Error    error
	Calls    []*providers.Request
}

// NewMockProvider creates a new mock provider
func NewMockProvider() *MockProvider {
	return &MockProvider{
		Calls: make([]*providers.Request, 0),
	}
}

// Complete records the call and returns the configured response
func (m *MockProvider) Complete(ctx context.Context, req *providers.Request) (*providers.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Calls = append(m.Calls, req)
	if m.Error != nil {
		return nil, m.Error
	}
	return m.Response, nil
}

// SetResponse configures the mock to return a specific response
func (m *MockProvider) SetResponse(content string, inputTokens, outputTokens int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Response = &providers.Response{
		Content: content,
		Usage: providers.Usage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
		},
	}
}

// SetError configures the mock to return an error
func (m *MockProvider) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Error = err
}

// Reset clears all recorded calls and responses
func (m *MockProvider) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Calls = m.Calls[:0]
	m.Response = nil
	m.Error = nil
}

// CallCount returns the number of calls made
func (m *MockProvider) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.Calls)
}
