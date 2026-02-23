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
// Search Query LLM Routing Tests
// =============================================================================

// TestRouter_SearchQueriesRoutedViaLLM verifies that search-intent queries are
// classified by the LLM rather than a keyword bypass.
func TestRouter_SearchQueriesRoutedViaLLM(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "find keyword", input: "find the authentication module"},
		{name: "search keyword", input: "search for error handling code"},
		{name: "locate keyword", input: "locate the database connection logic"},
		{name: "where is keyword", input: "where is the main entry point"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mocks.NewMockClassifierClient(t)
			mockClient.EXPECT().
				New(mock.Anything, mock.Anything).
				Return(createSearchMockMessage(map[string]any{
					"is_retrospective": false,
					"intent":           "find",
					"domain":           "local",
					"target_agent":     "librarian",
					"confidence":       0.92,
				}), nil).
				Once()

			router := guide.NewRouter(mockClient, guide.RouterConfig{
				DSLPrefix:        "@",
				Model:            "claude-sonnet-4-6",
				MaxTokens:        1024,
				ExecuteThreshold: 0.90,
				LogThreshold:     0.75,
				SuggestThreshold: 0.50,
			})

			req := &guide.RouteRequest{
				Input:         tt.input,
				SourceAgentID: "test-agent",
			}

			result, err := router.Route(context.Background(), req)
			require.NoError(t, err)

			assert.Equal(t, guide.TargetLibrarian, result.TargetAgent)
			assert.Equal(t, "llm", result.ClassificationMethod)
			mockClient.AssertExpectations(t)
		})
	}
}

// TestRouter_SearchWithDSL tests that DSL commands still take priority
func TestRouter_SearchWithDSL(t *testing.T) {
	mockClient := mocks.NewMockClassifierClient(t)

	router := guide.NewRouter(mockClient, guide.RouterConfig{
		DSLPrefix:        "@",
		Model:            "claude-sonnet-4-6",
		MaxTokens:        1024,
		ExecuteThreshold: 0.90,
		LogThreshold:     0.75,
		SuggestThreshold: 0.50,
	})

	req := &guide.RouteRequest{
		Input:         "@to:archivalist find patterns",
		SourceAgentID: "test-agent",
	}

	result, err := router.Route(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, guide.TargetAgent("archivalist"), result.TargetAgent)
	assert.Equal(t, "dsl", result.ClassificationMethod)

	mockClient.AssertNotCalled(t, "New")
}

// =============================================================================
// Helper Functions
// =============================================================================

func createSearchMockMessage(classification map[string]any) *anthropic.Message {
	jsonBytes, _ := json.Marshal(classification)
	return &anthropic.Message{
		ID:    "msg-search-test-123",
		Model: "claude-sonnet-4-6",
		Role:  "assistant",
		Content: []anthropic.ContentBlockUnion{
			{
				Type: "text",
				Text: string(jsonBytes),
			},
		},
	}
}
