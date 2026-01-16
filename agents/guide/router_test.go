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

// createMockMessage creates a mock anthropic.Message with JSON classification content
func createMockMessage(classification map[string]any) *anthropic.Message {
	jsonBytes, _ := json.Marshal(classification)
	return &anthropic.Message{
		ID:    "msg-test-123",
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

// TestRouter_DSLRouting tests that DSL commands are routed correctly without LLM calls
func TestRouter_DSLRouting(t *testing.T) {
	// Setup mock client - should NOT be called for DSL routing
	mockClient := mocks.NewMockClassifierClient(t)

	router := guide.NewRouter(mockClient, guide.RouterConfig{
		DSLPrefix: "@",
	})

	tests := []struct {
		name         string
		input        string
		expectAgent  guide.TargetAgent
		expectMethod string
	}{
		{
			name:         "direct to archivalist",
			input:        "@to:archivalist what patterns do we use?",
			expectAgent:  "archivalist",
			expectMethod: "dsl",
		},
		{
			name:         "direct to guide",
			input:        "@to:guide help me",
			expectAgent:  "guide",
			expectMethod: "dsl",
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

			assert.Equal(t, tt.expectAgent, result.TargetAgent)
			assert.Equal(t, tt.expectMethod, result.ClassificationMethod)
		})
	}

	// Verify mock was NOT called (DSL bypasses LLM)
	mockClient.AssertNotCalled(t, "New")
}

// TestRouter_LLMClassification tests LLM-based routing with mocked client
func TestRouter_LLMClassification(t *testing.T) {
	// Setup mock classifier client
	mockClient := mocks.NewMockClassifierClient(t)

	// Configure mock to return a specific classification response
	classificationJSON := map[string]any{
		"is_retrospective": true,
		"intent":           "recall",
		"domain":           "patterns",
		"target_agent":     "archivalist",
		"confidence":       0.95,
		"entities": map[string]any{
			"query": "error handling patterns",
		},
	}

	mockClient.EXPECT().
		New(mock.Anything, mock.Anything).
		Return(createMockMessage(classificationJSON), nil).
		Once()

	router := guide.NewRouter(mockClient, guide.RouterConfig{
		DSLPrefix: "@",
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 1024,
	})

	req := &guide.RouteRequest{
		Input:         "what error handling patterns do we follow?",
		SourceAgentID: "test-agent",
	}

	result, err := router.Route(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, guide.IntentRecall, result.Intent)
	assert.Equal(t, guide.DomainPatterns, result.Domain)
	assert.Equal(t, 0.95, result.Confidence)
	assert.Equal(t, "llm", result.ClassificationMethod)

	// Verify the mock was called
	mockClient.AssertExpectations(t)
}

// TestRouter_FallbackOnLowConfidence tests that low confidence triggers appropriate action
func TestRouter_FallbackOnLowConfidence(t *testing.T) {
	mockClient := mocks.NewMockClassifierClient(t)

	// Return low confidence classification
	classificationJSON := map[string]any{
		"is_retrospective": true,
		"intent":           "unknown",
		"domain":           "unknown",
		"target_agent":     "unknown",
		"confidence":       0.3, // Below typical thresholds
	}

	mockClient.EXPECT().
		New(mock.Anything, mock.Anything).
		Return(createMockMessage(classificationJSON), nil).
		Once()

	router := guide.NewRouter(mockClient, guide.RouterConfig{
		DSLPrefix: "@",
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 1024,
	})

	req := &guide.RouteRequest{
		Input:         "something ambiguous",
		SourceAgentID: "test-agent",
	}

	result, err := router.Route(context.Background(), req)
	require.NoError(t, err)

	// Should have low confidence
	assert.Less(t, result.Confidence, 0.5)
	assert.Equal(t, guide.RouteActionReject, result.Action)
}

// TestClassifier_WithMockedClient tests the classifier with mocked LLM responses
func TestClassifier_WithMockedClient(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		classification map[string]any
		expectIntent   guide.Intent
		expectDomain   guide.Domain
	}{
		{
			name:  "pattern recall",
			input: "what patterns do we use for logging?",
			classification: map[string]any{
				"is_retrospective": true,
				"intent":           "recall",
				"domain":           "patterns",
				"target_agent":     "archivalist",
				"confidence":       0.92,
			},
			expectIntent: guide.IntentRecall,
			expectDomain: guide.DomainPatterns,
		},
		{
			name:  "failure recall",
			input: "what went wrong with the deployment?",
			classification: map[string]any{
				"is_retrospective": true,
				"intent":           "recall",
				"domain":           "failures",
				"target_agent":     "archivalist",
				"confidence":       0.88,
			},
			expectIntent: guide.IntentRecall,
			expectDomain: guide.DomainFailures,
		},
		{
			name:  "decision declaration",
			input: "let's use PostgreSQL for the database",
			classification: map[string]any{
				"is_retrospective": true,
				"intent":           "declare",
				"domain":           "decisions",
				"target_agent":     "archivalist",
				"confidence":       0.85,
			},
			expectIntent: guide.IntentDeclare,
			expectDomain: guide.DomainDecisions,
		},
		{
			name:  "help request",
			input: "what can you help me with?",
			classification: map[string]any{
				"is_retrospective": true,
				"intent":           "help",
				"domain":           "system",
				"target_agent":     "guide",
				"confidence":       0.98,
			},
			expectIntent: guide.IntentHelp,
			expectDomain: guide.DomainSystem,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := mocks.NewMockClassifierClient(t)

			mockClient.EXPECT().
				New(mock.Anything, mock.Anything).
				Return(createMockMessage(tt.classification), nil).
				Once()

			classifier := guide.NewClassifierWithClient(mockClient, guide.RouterConfig{
				Model:     "claude-sonnet-4-5-20250929",
				MaxTokens: 1024,
			})

			result, err := classifier.Classify(context.Background(), tt.input)
			require.NoError(t, err)

			assert.Equal(t, tt.expectIntent, result.Intent)
			assert.Equal(t, tt.expectDomain, result.Domain)
		})
	}
}

// TestRouter_IsDSL tests DSL detection
func TestRouter_IsDSL(t *testing.T) {
	mockClient := mocks.NewMockClassifierClient(t)
	router := guide.NewRouter(mockClient, guide.RouterConfig{DSLPrefix: "@"})

	tests := []struct {
		input    string
		expected bool
	}{
		{"@to:archivalist hello", true},
		{"@guide query", true},
		{"hello world", false},
		{"email@example.com", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := router.IsDSL(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestRouter_ParseDSL tests DSL parsing
func TestRouter_ParseDSL(t *testing.T) {
	mockClient := mocks.NewMockClassifierClient(t)
	router := guide.NewRouter(mockClient, guide.RouterConfig{DSLPrefix: "@"})

	tests := []struct {
		input       string
		expectError bool
		expectType  guide.DSLCommandType
		expectAgent string
	}{
		{
			input:       "@to:archivalist query",
			expectError: false,
			expectType:  guide.DSLCommandTypeDirect,
			expectAgent: "archivalist",
		},
		{
			input:       "@guide what patterns?",
			expectError: false,
			expectType:  guide.DSLCommandTypeIntent,
			expectAgent: "",
		},
		{
			input:       "not a dsl command",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			cmd, err := router.ParseDSL(tt.input)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectType, cmd.Type)
				assert.Equal(t, tt.expectAgent, cmd.TargetAgent)
			}
		})
	}
}

// TestRouter_FormatAsDSL tests formatting route results as DSL
func TestRouter_FormatAsDSL(t *testing.T) {
	mockClient := mocks.NewMockClassifierClient(t)
	router := guide.NewRouter(mockClient, guide.RouterConfig{DSLPrefix: "@"})

	result := &guide.RouteResult{
		TargetAgent: guide.TargetAgent("archivalist"),
		Intent:      guide.IntentRecall,
		Domain:      guide.DomainPatterns,
		Entities: &guide.ExtractedEntities{
			Scope: "error-handling",
		},
	}

	dsl := router.FormatAsDSL(result)
	assert.Contains(t, dsl, "@archivalist:")
	assert.Contains(t, dsl, "recall")
	assert.Contains(t, dsl, "patterns")
}

// TestRouter_MultipleDSLCommands tests routing multiple DSL commands
func TestRouter_MultipleDSLCommands(t *testing.T) {
	mockClient := mocks.NewMockClassifierClient(t)
	router := guide.NewRouter(mockClient, guide.RouterConfig{DSLPrefix: "@"})

	req := &guide.RouteRequest{
		Input:         "@to:archivalist query1; @to:guide query2",
		SourceAgentID: "test-agent",
	}

	result, err := router.Route(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, "dsl", result.ClassificationMethod)
	assert.Len(t, result.SubResults, 2)

	// Verify mock was NOT called
	mockClient.AssertNotCalled(t, "New")
}

// TestRouter_IntentDSLTriggersLLM tests that @guide <query> triggers LLM classification
func TestRouter_IntentDSLTriggersLLM(t *testing.T) {
	mockClient := mocks.NewMockClassifierClient(t)

	// Mock should be called for the inner query
	classificationJSON := map[string]any{
		"is_retrospective": true,
		"intent":           "recall",
		"domain":           "patterns",
		"target_agent":     "archivalist",
		"confidence":       0.9,
	}

	mockClient.EXPECT().
		New(mock.Anything, mock.Anything).
		Return(createMockMessage(classificationJSON), nil).
		Once()

	router := guide.NewRouter(mockClient, guide.RouterConfig{
		DSLPrefix: "@",
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 1024,
	})

	req := &guide.RouteRequest{
		Input:         "@guide what patterns do we use?",
		SourceAgentID: "test-agent",
	}

	result, err := router.Route(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, guide.IntentRecall, result.Intent)
	assert.Equal(t, guide.DomainPatterns, result.Domain)
	assert.Equal(t, "llm", result.ClassificationMethod)

	mockClient.AssertExpectations(t)
}

// TestRouter_RetrospectiveCheck tests that non-retrospective queries are flagged
func TestRouter_RetrospectiveCheck(t *testing.T) {
	classificationJSON := map[string]any{
		"is_retrospective": false,
		"rejection_reason": "Query is about future requirements, not past observations",
		"intent":           "recall",
		"domain":           "patterns",
		"target_agent":     "archivalist",
		"confidence":       0.85,
	}

	router, mockClient := newRouterWithMock(t, classificationJSON)

	req := &guide.RouteRequest{
		Input:         "what patterns should we use for the new feature?",
		SourceAgentID: "test-agent",
	}

	result, err := router.Route(context.Background(), req)
	require.NoError(t, err)

	assert.True(t, result.Rejected)
	assert.Contains(t, result.Reason, "future")
	mockClient.AssertExpectations(t)
}

// TestClassifier_MultiIntent tests handling of multi-intent queries
func TestClassifier_MultiIntent(t *testing.T) {
	classificationJSON := map[string]any{
		"is_retrospective": true,
		"intent":           "recall",
		"domain":           "patterns",
		"target_agent":     "archivalist",
		"confidence":       0.85,
		"multi_intent":     true,
		"sub_intents": []map[string]any{
			{
				"intent":     "recall",
				"domain":     "patterns",
				"confidence": 0.90,
			},
			{
				"intent":     "recall",
				"domain":     "failures",
				"confidence": 0.80,
			},
		},
	}

	classifier, mockClient := newClassifierWithMock(t, classificationJSON)

	result, err := classifier.Classify(context.Background(), "what patterns and failures happened?")
	require.NoError(t, err)

	assert.True(t, result.MultiIntent)
	assert.Len(t, result.SubResults, 2)
	assert.Equal(t, guide.DomainPatterns, result.SubResults[0].Domain)
	assert.Equal(t, guide.DomainFailures, result.SubResults[1].Domain)
	mockClient.AssertExpectations(t)
}

// TestRouter_EntityExtraction tests that entities are properly extracted
func TestRouter_EntityExtraction(t *testing.T) {
	classificationJSON := map[string]any{
		"is_retrospective": true,
		"intent":           "recall",
		"domain":           "failures",
		"target_agent":     "archivalist",
		"confidence":       0.92,
		"entities": map[string]any{
			"scope":         "authentication",
			"timeframe":     "yesterday",
			"error_type":    "timeout",
			"error_message": "connection timed out",
			"file_paths":    []string{"src/auth/login.go", "src/auth/session.go"},
		},
	}

	router, mockClient := newRouterWithMock(t, classificationJSON)

	req := &guide.RouteRequest{
		Input:         "what timeout errors happened yesterday in authentication?",
		SourceAgentID: "test-agent",
	}

	result, err := router.Route(context.Background(), req)
	require.NoError(t, err)

	require.NotNil(t, result.Entities)
	assert.Equal(t, "authentication", result.Entities.Scope)
	assert.Equal(t, "yesterday", result.Entities.Timeframe)
	assert.Equal(t, "timeout", result.Entities.ErrorType)
	assert.Equal(t, "connection timed out", result.Entities.ErrorMessage)
	assert.Len(t, result.Entities.FilePaths, 2)
	mockClient.AssertExpectations(t)
}

func newRouterWithMock(t *testing.T, classification map[string]any) (*guide.Router, *mocks.MockClassifierClient) {
	mockClient := mocks.NewMockClassifierClient(t)
	mockClient.EXPECT().
		New(mock.Anything, mock.Anything).
		Return(createMockMessage(classification), nil).
		Once()
	return guide.NewRouter(mockClient, guide.RouterConfig{
		DSLPrefix: "@",
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 1024,
	}), mockClient
}

func newClassifierWithMock(t *testing.T, classification map[string]any) (*guide.Classifier, *mocks.MockClassifierClient) {
	mockClient := mocks.NewMockClassifierClient(t)
	mockClient.EXPECT().
		New(mock.Anything, mock.Anything).
		Return(createMockMessage(classification), nil).
		Once()
	return guide.NewClassifierWithClient(mockClient, guide.RouterConfig{
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 1024,
	}), mockClient
}
