package guide

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/genai"
)

// GeminiClassifier implements ClassifierService using Gemini's structured outputs.
type GeminiClassifier struct {
	client      *genai.Client
	config      RouterConfig
	corrections *correctionMemory
}

func NewGeminiClassifier(client *genai.Client, config RouterConfig) *GeminiClassifier {
	return &GeminiClassifier{
		client:      client,
		config:      config,
		corrections: newCorrectionMemory(config.MaxCorrections),
	}
}

// AddCorrection adds a correction for learning
func (c *GeminiClassifier) AddCorrection(correction CorrectionRecord) {
	if c.corrections == nil {
		return
	}
	c.corrections.add(correction)
}

func (c *GeminiClassifier) formatCorrections(input string) string {
	if c.corrections == nil {
		return ""
	}
	records := c.corrections.selectForPrompt(input, c.maxPromptCorrections())
	return formatCorrectionExamples(records)
}

func (c *GeminiClassifier) maxPromptCorrections() int {
	if c.config.MaxPromptCorrections > 0 {
		return c.config.MaxPromptCorrections
	}
	return defaultMaxPromptCorrections
}

// Classify classifies a natural language query using structured outputs
func (c *GeminiClassifier) Classify(ctx context.Context, input string) (*ClassificationResult, error) {
	systemPrompt := FormatClassificationPrompt(c.formatCorrections(input))

	if c.config.ClassificationTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.config.ClassificationTimeout)
		defer cancel()
	}

	// Default to the explicitly requested model, or use config if provided differently
	model := "gemini-3.1-pro-preview"
	if c.config.Model != "" && c.config.Model != "claude-haiku-4-5-20251001" {
		model = c.config.Model
	}

	var temp *float32
	if c.config.Temperature != 0 {
		t := float32(c.config.Temperature)
		temp = &t
	}

	thinkingLvl := genai.ThinkingLevelHigh
	if c.config.ThinkingLevel == "LOW" {
		thinkingLvl = genai.ThinkingLevelLow
	} else if c.config.ThinkingLevel == "MEDIUM" {
		thinkingLvl = genai.ThinkingLevelMedium
	} else if c.config.ThinkingLevel == "MINIMAL" {
		thinkingLvl = genai.ThinkingLevelMinimal
	}

	resp, err := c.client.Models.GenerateContent(ctx, model, genai.Text(input), &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Role: "system",
			Parts: []*genai.Part{
				{Text: systemPrompt},
			},
		},
		ResponseMIMEType: "application/json",
		ResponseSchema:   c.buildResponseSchema(),
		Temperature:      temp,
		ThinkingConfig: &genai.ThinkingConfig{
			ThinkingLevel: thinkingLvl,
		},
	})

	if err != nil {
		return nil, formatGeminiError("gemini classification (model "+model+")", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("no response candidates from gemini")
	}

	textPart := resp.Candidates[0].Content.Parts[0].Text

	var result ClassificationResult
	if err := json.Unmarshal([]byte(textPart), &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal structured output: %w", err)
	}

	return normalizeClassificationResult(&result), nil
}

func (c *GeminiClassifier) buildResponseSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"is_retrospective": {
				Type:        genai.TypeBoolean,
				Description: "True if query is about PAST actions, observations, or learnings. False if about FUTURE needs, plans, or requirements.",
			},
			"rejection_reason": {
				Type:        genai.TypeString,
				Description: "If not retrospective and target is archivalist, explain why the query cannot be handled",
			},
			"intent": {
				Type:        genai.TypeString,
				Enum:        []string{"recall", "store", "check", "declare", "complete", "find", "search", "locate", "plan", "design", "help", "status", "chat", "unknown"},
				Description: "The classified intent of the query",
			},
			"domain": {
				Type:        genai.TypeString,
				Enum:        []string{"local", "history", "research", "planning", "system", "compliance", "testing", "general", "unknown"},
				Description: "The domain/category of the query",
			},
			"target_agent": {
				Type:        genai.TypeString,
				Enum:        []string{"librarian", "engineer", "designer", "tester", "inspector", "archivalist", "academic", "orchestrator", "architect", "guide", "unknown"},
				Description: "Which agent should handle this query",
			},
			"entities": {
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"scope": {
						Type:        genai.TypeString,
						Description: "Area/component being queried (e.g., 'authentication', 'database')",
					},
					"timeframe": {
						Type:        genai.TypeString,
						Description: "Time reference if any (e.g., 'yesterday', 'last week')",
					},
					"agent_id": {
						Type:        genai.TypeString,
						Description: "Specific agent ID if mentioned",
					},
					"agent_name": {
						Type:        genai.TypeString,
						Description: "Specific agent name if mentioned",
					},
					"file_paths": {
						Type:        genai.TypeArray,
						Items:       &genai.Schema{Type: genai.TypeString},
						Description: "File paths mentioned in the query",
					},
					"error_type": {
						Type:        genai.TypeString,
						Description: "Type of error if failure-related",
					},
					"error_message": {
						Type:        genai.TypeString,
						Description: "Error message if provided",
					},
					"query": {
						Type:        genai.TypeString,
						Description: "Free-form query text for context searches",
					},
				},
			},
			"confidence": {
				Type:        genai.TypeNumber,
				Description: "Classification confidence from 0.0 to 1.0",
			},
			"rejected": {
				Type:        genai.TypeBoolean,
				Description: "True when the request is too ambiguous to safely route",
			},
			"reason": {
				Type:        genai.TypeString,
				Description: "Clarifying question or rejection reason when rejected=true",
			},
			"multi_intent": {
				Type:        genai.TypeBoolean,
				Description: "True if the query contains multiple intents",
			},
			"sub_results": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"is_retrospective": {Type: genai.TypeBoolean},
						"intent":           {Type: genai.TypeString, Enum: []string{"recall", "store", "check", "declare", "complete", "find", "search", "locate", "plan", "design", "help", "status", "chat", "unknown"}},
						"domain":           {Type: genai.TypeString, Enum: []string{"local", "history", "research", "planning", "system", "compliance", "testing", "general", "unknown"}},
						"target_agent":     {Type: genai.TypeString, Enum: []string{"librarian", "engineer", "designer", "tester", "inspector", "archivalist", "academic", "orchestrator", "architect", "guide", "unknown"}},
						"confidence":       {Type: genai.TypeNumber},
					},
					Required: []string{"is_retrospective", "intent", "domain", "target_agent", "confidence"},
				},
				Description: "Sub-intents if multi_intent is true",
			},
		},
		Required: []string{"is_retrospective", "intent", "domain", "target_agent", "confidence"},
	}
}

// Ensure GeminiClassifier implements ClassifierService
var _ ClassifierService = (*GeminiClassifier)(nil)
