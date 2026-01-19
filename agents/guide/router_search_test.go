package guide_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/guide/mocks"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Search Query Detection Tests
// =============================================================================

// TestRouter_SearchKeywordDetection tests that search keywords are detected correctly
func TestRouter_SearchKeywordDetection(t *testing.T) {
	// Mock client should NOT be called for keyword-matched search queries
	mockClient := mocks.NewMockClassifierClient(t)

	router := guide.NewRouter(mockClient, guide.RouterConfig{
		DSLPrefix: "@",
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 1024,
	})

	tests := []struct {
		name           string
		input          string
		expectLibrarian bool
		expectMethod   string
	}{
		{
			name:           "find keyword",
			input:          "find the authentication module",
			expectLibrarian: true,
			expectMethod:   "keyword",
		},
		{
			name:           "search keyword",
			input:          "search for error handling code",
			expectLibrarian: true,
			expectMethod:   "keyword",
		},
		{
			name:           "locate keyword",
			input:          "locate the database connection logic",
			expectLibrarian: true,
			expectMethod:   "keyword",
		},
		{
			name:           "where is keyword",
			input:          "where is the main entry point",
			expectLibrarian: true,
			expectMethod:   "keyword",
		},
		{
			name:           "Find uppercase",
			input:          "Find all test files",
			expectLibrarian: true,
			expectMethod:   "keyword",
		},
		{
			name:           "SEARCH uppercase",
			input:          "SEARCH for the config file",
			expectLibrarian: true,
			expectMethod:   "keyword",
		},
		{
			name:           "mixed case locate",
			input:          "Locate the Router class",
			expectLibrarian: true,
			expectMethod:   "keyword",
		},
		{
			name:           "where is mixed case",
			input:          "Where Is the handler",
			expectLibrarian: true,
			expectMethod:   "keyword",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &guide.RouteRequest{
				Input:         tt.input,
				SourceAgentID: "test-agent",
			}

			result, err := router.Route(context.Background(), req)
			require.NoError(t, err)

			if tt.expectLibrarian {
				assert.Equal(t, guide.TargetLibrarian, result.TargetAgent)
				assert.Equal(t, tt.expectMethod, result.ClassificationMethod)
				assert.Equal(t, guide.DomainCode, result.Domain)
			}
		})
	}

	// Verify mock was NOT called (keyword matching bypasses LLM)
	mockClient.AssertNotCalled(t, "New")
}

// TestRouter_SearchRouteToLibrarian tests that search queries route to Librarian
func TestRouter_SearchRouteToLibrarian(t *testing.T) {
	mockClient := mocks.NewMockClassifierClient(t)

	router := guide.NewRouter(mockClient, guide.RouterConfig{
		DSLPrefix: "@",
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 1024,
	})

	req := &guide.RouteRequest{
		Input:         "find the user authentication handler",
		SourceAgentID: "engineer-agent",
	}

	result, err := router.Route(context.Background(), req)
	require.NoError(t, err)

	// Verify routing to Librarian
	assert.Equal(t, guide.TargetLibrarian, result.TargetAgent)
	assert.Equal(t, guide.IntentRecall, result.Intent)
	assert.Equal(t, guide.DomainCode, result.Domain)
	assert.Equal(t, "keyword", result.ClassificationMethod)
	assert.Equal(t, guide.RouteActionExecute, result.Action)
	assert.GreaterOrEqual(t, result.Confidence, 0.8)

	// Verify mock was NOT called
	mockClient.AssertNotCalled(t, "New")
}

// TestRouter_NonSearchFallbackToLLM tests that non-search queries fall back to LLM
func TestRouter_NonSearchFallbackToLLM(t *testing.T) {
	mockClient := mocks.NewMockClassifierClient(t)

	// Configure mock to return a classification response for non-search queries
	classificationJSON := map[string]any{
		"is_retrospective": true,
		"intent":           "recall",
		"domain":           "patterns",
		"target_agent":     "archivalist",
		"confidence":       0.92,
	}

	mockClient.EXPECT().
		New(mock.Anything, mock.Anything).
		Return(createSearchMockMessage(classificationJSON), nil).
		Once()

	router := guide.NewRouter(mockClient, guide.RouterConfig{
		DSLPrefix: "@",
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 1024,
	})

	// This query does NOT contain search keywords
	req := &guide.RouteRequest{
		Input:         "what patterns do we use for authentication?",
		SourceAgentID: "test-agent",
	}

	result, err := router.Route(context.Background(), req)
	require.NoError(t, err)

	// Should route to Archivalist via LLM classification
	assert.Equal(t, guide.TargetAgent("archivalist"), result.TargetAgent)
	assert.Equal(t, guide.IntentRecall, result.Intent)
	assert.Equal(t, guide.DomainPatterns, result.Domain)
	assert.Equal(t, "llm", result.ClassificationMethod)

	// Verify mock WAS called
	mockClient.AssertExpectations(t)
}

// TestRouter_SearchKeywordPriority tests that search keywords take priority over LLM
func TestRouter_SearchKeywordPriority(t *testing.T) {
	// Mock should NOT be called even if query could be classified differently
	mockClient := mocks.NewMockClassifierClient(t)

	router := guide.NewRouter(mockClient, guide.RouterConfig{
		DSLPrefix: "@",
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 1024,
	})

	// Query with search keyword should route to Librarian even if it
	// mentions patterns (which would normally go to Archivalist)
	req := &guide.RouteRequest{
		Input:         "find the authentication patterns",
		SourceAgentID: "test-agent",
	}

	result, err := router.Route(context.Background(), req)
	require.NoError(t, err)

	// Search keyword takes priority - routes to Librarian
	assert.Equal(t, guide.TargetLibrarian, result.TargetAgent)
	assert.Equal(t, guide.DomainCode, result.Domain)
	assert.Equal(t, "keyword", result.ClassificationMethod)

	// Verify mock was NOT called
	mockClient.AssertNotCalled(t, "New")
}

// =============================================================================
// Edge Cases Tests
// =============================================================================

// TestRouter_SearchWithDSL tests that DSL commands still take priority over search keywords
func TestRouter_SearchWithDSL(t *testing.T) {
	mockClient := mocks.NewMockClassifierClient(t)

	router := guide.NewRouter(mockClient, guide.RouterConfig{
		DSLPrefix: "@",
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 1024,
	})

	// DSL command should take priority over keyword matching
	req := &guide.RouteRequest{
		Input:         "@to:archivalist find patterns",
		SourceAgentID: "test-agent",
	}

	result, err := router.Route(context.Background(), req)
	require.NoError(t, err)

	// DSL takes priority - routes to archivalist as specified
	assert.Equal(t, guide.TargetAgent("archivalist"), result.TargetAgent)
	assert.Equal(t, "dsl", result.ClassificationMethod)

	mockClient.AssertNotCalled(t, "New")
}

// TestRouter_NoSearchKeyword tests queries without search keywords
func TestRouter_NoSearchKeyword(t *testing.T) {
	mockClient := mocks.NewMockClassifierClient(t)

	classificationJSON := map[string]any{
		"is_retrospective": true,
		"intent":           "help",
		"domain":           "system",
		"target_agent":     "guide",
		"confidence":       0.95,
	}

	mockClient.EXPECT().
		New(mock.Anything, mock.Anything).
		Return(createSearchMockMessage(classificationJSON), nil).
		Once()

	router := guide.NewRouter(mockClient, guide.RouterConfig{
		DSLPrefix: "@",
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 1024,
	})

	// Query without any search keywords
	req := &guide.RouteRequest{
		Input:         "help me understand the routing system",
		SourceAgentID: "test-agent",
	}

	result, err := router.Route(context.Background(), req)
	require.NoError(t, err)

	// Should go through LLM classification
	assert.Equal(t, guide.TargetAgent("guide"), result.TargetAgent)
	assert.Equal(t, "llm", result.ClassificationMethod)

	mockClient.AssertExpectations(t)
}

// TestRouter_SearchResultProperties tests the properties of search route results
func TestRouter_SearchResultProperties(t *testing.T) {
	mockClient := mocks.NewMockClassifierClient(t)

	router := guide.NewRouter(mockClient, guide.RouterConfig{
		DSLPrefix: "@",
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 1024,
	})

	req := &guide.RouteRequest{
		Input:         "search for database connection code",
		SourceAgentID: "test-agent",
	}

	result, err := router.Route(context.Background(), req)
	require.NoError(t, err)

	// Verify all expected properties
	assert.Equal(t, guide.TargetLibrarian, result.TargetAgent)
	assert.Equal(t, guide.IntentRecall, result.Intent)
	assert.Equal(t, guide.DomainCode, result.Domain)
	assert.Equal(t, guide.TemporalPresent, result.TemporalFocus)
	assert.Equal(t, guide.RouteActionExecute, result.Action)
	assert.Equal(t, "keyword", result.ClassificationMethod)
	assert.Equal(t, 0.85, result.Confidence)
	assert.False(t, result.Rejected)
	assert.Greater(t, result.ProcessingTime.Nanoseconds(), int64(0))
}

// =============================================================================
// Helper Functions
// =============================================================================

// createSearchMockMessage creates a mock anthropic.Message with JSON classification content
func createSearchMockMessage(classification map[string]any) *anthropic.Message {
	jsonBytes, _ := json.Marshal(classification)
	return &anthropic.Message{
		ID:    "msg-search-test-123",
		Model: "claude-sonnet-4-5-20250929",
		Role:  "assistant",
		Content: []anthropic.ContentBlockUnion{
			{
				Type: "text",
				Text: string(jsonBytes),
			},
		},
	}
}
