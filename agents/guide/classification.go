package guide

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// =============================================================================
// Classification Tool Definition
// =============================================================================

// ClassificationToolName is the name of the classification tool
const ClassificationToolName = "classify_query"

// ClassificationToolSchema returns the JSON schema for the classification tool
var ClassificationToolSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"is_retrospective": map[string]any{
			"type":        "boolean",
			"description": "True if query is about PAST actions, observations, or learnings. False if about FUTURE needs, plans, or requirements.",
		},
		"rejection_reason": map[string]any{
			"type":        "string",
			"description": "If not retrospective and target is archivalist, explain why the query cannot be handled",
		},
		"intent": map[string]any{
			"type":        "string",
			"enum":        []string{"recall", "store", "check", "declare", "complete", "help", "status", "unknown"},
			"description": "The classified intent of the query",
		},
		"domain": map[string]any{
			"type":        "string",
			"enum":        []string{"patterns", "failures", "decisions", "files", "learnings", "intents", "agents", "system", "unknown"},
			"description": "The domain/category of the query",
		},
		"target_agent": map[string]any{
			"type":        "string",
			"enum":        []string{"archivalist", "guide", "unknown"},
			"description": "Which agent should handle this query",
		},
		"entities": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"scope": map[string]any{
					"type":        "string",
					"description": "Area/component being queried (e.g., 'authentication', 'database')",
				},
				"timeframe": map[string]any{
					"type":        "string",
					"description": "Time reference if any (e.g., 'yesterday', 'last week')",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Specific agent ID if mentioned",
				},
				"agent_name": map[string]any{
					"type":        "string",
					"description": "Specific agent name if mentioned",
				},
				"file_paths": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "File paths mentioned in the query",
				},
				"error_type": map[string]any{
					"type":        "string",
					"description": "Type of error if failure-related",
				},
				"error_message": map[string]any{
					"type":        "string",
					"description": "Error message if provided",
				},
				"data": map[string]any{
					"type":        "object",
					"description": "Data payload for store operations",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "Free-form query text for context searches",
				},
			},
		},
		"confidence": map[string]any{
			"type":        "number",
			"minimum":     0,
			"maximum":     1,
			"description": "Classification confidence from 0.0 to 1.0",
		},
		"multi_intent": map[string]any{
			"type":        "boolean",
			"description": "True if the query contains multiple intents",
		},
		"sub_intents": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"intent":     map[string]any{"type": "string"},
					"domain":     map[string]any{"type": "string"},
					"confidence": map[string]any{"type": "number"},
				},
			},
			"description": "Sub-intents if multi_intent is true",
		},
	},
	"required": []string{"is_retrospective", "intent", "domain", "target_agent", "confidence"},
}

// =============================================================================
// Classifier
// =============================================================================

// ClassifierClient defines the interface for LLM API calls
type ClassifierClient interface {
	New(ctx context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error)
}

// RealClassifierClient wraps the real Anthropic client
type RealClassifierClient struct {
	messages *anthropic.MessageService
}

// NewRealClassifierClient creates a wrapper around the real Anthropic client
func NewRealClassifierClient(client *anthropic.Client) *RealClassifierClient {
	return &RealClassifierClient{messages: &client.Messages}
}

// New calls the real Anthropic API
func (r *RealClassifierClient) New(ctx context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
	return r.messages.New(ctx, params)
}

// Classifier handles LLM-based query classification
type Classifier struct {
	client ClassifierClient
	config RouterConfig

	// Few-shot corrections
	corrections []CorrectionRecord
}

// NewClassifier creates a new classifier
func NewClassifier(client *anthropic.Client, config RouterConfig) *Classifier {
	return &Classifier{
		client:      NewRealClassifierClient(client),
		config:      config,
		corrections: make([]CorrectionRecord, 0),
	}
}

// NewClassifierWithClient creates a new classifier with a custom client (for testing)
func NewClassifierWithClient(client ClassifierClient, config RouterConfig) *Classifier {
	return &Classifier{
		client:      client,
		config:      config,
		corrections: make([]CorrectionRecord, 0),
	}
}

// NewClassifierWithAPIKey creates a new classifier with an API key
func NewClassifierWithAPIKey(apiKey string, config RouterConfig) *Classifier {
	opts := []option.RequestOption{}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	client := anthropic.NewClient(opts...)

	return &Classifier{
		client:      NewRealClassifierClient(&client),
		config:      config,
		corrections: make([]CorrectionRecord, 0),
	}
}

// Classify classifies a natural language query
func (c *Classifier) Classify(ctx context.Context, input string) (*ClassificationResult, error) {
	// Build prompt with corrections
	systemPrompt := FormatClassificationPrompt(c.formatCorrections())

	// Create context with timeout
	if c.config.ClassificationTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.config.ClassificationTimeout)
		defer cancel()
	}

	// Call LLM with tool-use
	resp, err := c.client.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.config.Model),
		MaxTokens: int64(c.config.MaxTokens),
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(input)),
		},
		// Note: Tool use requires proper SDK setup. For now, we'll parse from text response
		// and add tool support when the SDK interface is confirmed
	})
	if err != nil {
		return nil, fmt.Errorf("classification request failed: %w", err)
	}

	// Extract classification from response
	return c.extractClassificationFromText(resp)
}

// extractClassificationFromText extracts classification from text response
func (c *Classifier) extractClassificationFromText(resp *anthropic.Message) (*ClassificationResult, error) {
	// Get text content from response
	var text string
	for _, block := range resp.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}

	if text == "" {
		return nil, fmt.Errorf("no text content in response")
	}

	// Try to parse as JSON (the prompt asks for JSON output)
	// Look for JSON block in the response
	jsonStart := -1
	jsonEnd := -1
	braceCount := 0

	for i, r := range text {
		if r == '{' {
			if jsonStart == -1 {
				jsonStart = i
			}
			braceCount++
		} else if r == '}' {
			braceCount--
			if braceCount == 0 && jsonStart != -1 {
				jsonEnd = i + 1
				break
			}
		}
	}

	if jsonStart == -1 || jsonEnd == -1 {
		// No JSON found, try to parse the text heuristically
		return c.parseTextHeuristically(text)
	}

	jsonStr := text[jsonStart:jsonEnd]
	return c.parseToolUseResult([]byte(jsonStr))
}

// parseTextHeuristically attempts to extract classification from unstructured text
func (c *Classifier) parseTextHeuristically(text string) (*ClassificationResult, error) {
	// Default result with low confidence
	result := &ClassificationResult{
		IsRetrospective: true,
		Intent:          IntentUnknown,
		Domain:          DomainUnknown,
		Confidence:      0.3,
	}

	textLower := strings.ToLower(text)

	// Try to detect intent
	if strings.Contains(textLower, "recall") || strings.Contains(textLower, "retrieve") || strings.Contains(textLower, "query") {
		result.Intent = IntentRecall
		result.Confidence = 0.6
	} else if strings.Contains(textLower, "store") || strings.Contains(textLower, "record") || strings.Contains(textLower, "log") {
		result.Intent = IntentStore
		result.Confidence = 0.6
	} else if strings.Contains(textLower, "check") || strings.Contains(textLower, "verify") {
		result.Intent = IntentCheck
		result.Confidence = 0.6
	}

	// Try to detect domain
	if strings.Contains(textLower, "pattern") {
		result.Domain = DomainPatterns
	} else if strings.Contains(textLower, "failure") || strings.Contains(textLower, "error") {
		result.Domain = DomainFailures
	} else if strings.Contains(textLower, "decision") {
		result.Domain = DomainDecisions
	} else if strings.Contains(textLower, "file") {
		result.Domain = DomainFiles
	} else if strings.Contains(textLower, "learning") || strings.Contains(textLower, "lesson") {
		result.Domain = DomainLearnings
	}

	// Check for retrospective/prospective
	if strings.Contains(textLower, "prospective") || strings.Contains(textLower, "future") || strings.Contains(textLower, "should") {
		result.IsRetrospective = false
	}

	return result, nil
}

// parseToolUseResult parses the JSON input from tool use
func (c *Classifier) parseToolUseResult(inputJSON []byte) (*ClassificationResult, error) {
	var raw struct {
		IsRetrospective bool   `json:"is_retrospective"`
		RejectionReason string `json:"rejection_reason"`
		Intent          string `json:"intent"`
		Domain          string `json:"domain"`
		TargetAgent     string `json:"target_agent"`
		Entities        *struct {
			Scope        string         `json:"scope"`
			Timeframe    string         `json:"timeframe"`
			AgentID      string         `json:"agent_id"`
			AgentName    string         `json:"agent_name"`
			FilePaths    []string       `json:"file_paths"`
			ErrorType    string         `json:"error_type"`
			ErrorMessage string         `json:"error_message"`
			Data         map[string]any `json:"data"`
			Query        string         `json:"query"`
		} `json:"entities"`
		Confidence  float64 `json:"confidence"`
		MultiIntent bool    `json:"multi_intent"`
		SubIntents  []struct {
			Intent     string  `json:"intent"`
			Domain     string  `json:"domain"`
			Confidence float64 `json:"confidence"`
		} `json:"sub_intents"`
	}

	if err := json.Unmarshal(inputJSON, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse classification result: %w", err)
	}

	result := &ClassificationResult{
		IsRetrospective: raw.IsRetrospective,
		RejectionReason: raw.RejectionReason,
		Intent:          Intent(raw.Intent),
		Domain:          Domain(raw.Domain),
		Confidence:      raw.Confidence,
		MultiIntent:     raw.MultiIntent,
	}

	// Convert entities
	if raw.Entities != nil {
		result.Entities = &ExtractedEntities{
			Scope:        raw.Entities.Scope,
			Timeframe:    raw.Entities.Timeframe,
			AgentID:      raw.Entities.AgentID,
			AgentName:    raw.Entities.AgentName,
			FilePaths:    raw.Entities.FilePaths,
			ErrorType:    raw.Entities.ErrorType,
			ErrorMessage: raw.Entities.ErrorMessage,
			Data:         raw.Entities.Data,
			Query:        raw.Entities.Query,
		}
	}

	// Convert sub-intents
	if raw.MultiIntent && len(raw.SubIntents) > 0 {
		result.SubResults = make([]*ClassificationResult, 0, len(raw.SubIntents))
		for _, sub := range raw.SubIntents {
			result.SubResults = append(result.SubResults, &ClassificationResult{
				IsRetrospective: raw.IsRetrospective,
				Intent:          Intent(sub.Intent),
				Domain:          Domain(sub.Domain),
				Confidence:      sub.Confidence,
			})
		}
	}

	return result, nil
}

// AddCorrection adds a correction for learning
func (c *Classifier) AddCorrection(correction CorrectionRecord) {
	c.corrections = append(c.corrections, correction)

	// Keep only recent corrections
	if len(c.corrections) > c.config.MaxCorrections {
		c.corrections = c.corrections[1:]
	}
}

// formatCorrections formats corrections for few-shot learning
func (c *Classifier) formatCorrections() string {
	if len(c.corrections) == 0 {
		return ""
	}

	var sb string
	for _, corr := range c.corrections {
		sb += fmt.Sprintf(
			"Input: %q\nWRONG: intent=%s, domain=%s, target=%s\nCORRECT: intent=%s, domain=%s, target=%s\nReason: %s\n\n",
			corr.Input,
			corr.WrongIntent, corr.WrongDomain, corr.WrongTarget,
			corr.CorrectIntent, corr.CorrectDomain, corr.CorrectTarget,
			corr.Reason,
		)
	}

	return sb
}

// =============================================================================
// Classification Result Methods
// =============================================================================

// ToRouteResult converts a classification result to a route result
func (cr *ClassificationResult) ToRouteResult(processingTime time.Duration) *RouteResult {
	result := &RouteResult{
		Intent:               cr.Intent,
		Domain:               cr.Domain,
		Entities:             cr.Entities,
		Confidence:           cr.Confidence,
		ClassificationMethod: "llm",
		ProcessingTime:       processingTime,
	}

	// Determine target agent
	if cr.Domain.IsHistoricalDomain() && cr.IsRetrospective {
		result.TargetAgent = TargetArchivalist
		result.TemporalFocus = TemporalPast
	} else if cr.Domain == DomainSystem || cr.Domain == DomainAgents {
		result.TargetAgent = TargetGuide
		result.TemporalFocus = TemporalPresent
	} else {
		result.TargetAgent = TargetUnknown
		result.TemporalFocus = TemporalUnknown
	}

	// Check for rejection
	if !cr.IsRetrospective && cr.Domain.IsHistoricalDomain() {
		result.Rejected = true
		result.Reason = cr.RejectionReason
		if result.Reason == "" {
			result.Reason = "Query is prospective (about future), not retrospective (about past)"
		}
	}

	// Determine action based on confidence
	result.Action = determineAction(cr.Confidence)

	// Handle multi-intent
	if cr.MultiIntent && len(cr.SubResults) > 0 {
		result.SubResults = make([]*RouteResult, 0, len(cr.SubResults))
		for _, sub := range cr.SubResults {
			result.SubResults = append(result.SubResults, sub.ToRouteResult(0))
		}
	}

	return result
}

// determineAction determines the routing action based on confidence
func determineAction(confidence float64) RouteAction {
	switch {
	case confidence >= 0.90:
		return RouteActionExecute
	case confidence >= 0.75:
		return RouteActionLog
	case confidence >= 0.50:
		return RouteActionSuggest
	default:
		return RouteActionReject
	}
}
