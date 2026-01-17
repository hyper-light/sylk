package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/skills"
	"google.golang.org/genai"
)

// GoogleProvider implements Provider for Google's Gemini models
type GoogleProvider struct {
	client *genai.Client
	config GoogleConfig
	skills []skills.Skill
}

type GoogleModel string

const (
	Gemini3Pro GoogleModel = "gemini-3-pro-preview"
)

// Supported Google models
var googleModels = map[string]bool{
	// Gemini 3 family
	"gemini-3-pro": true, // Google Gemini 3 Pro
}

// NewGoogleProvider creates a new Google provider with the given configuration
func NewGoogleProvider(ctx context.Context, config GoogleConfig, skills ...skills.Skill) (*GoogleProvider, error) {
	if config.Model == "" {
		config.Model = DefaultGoogleConfig().Model
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = DefaultGoogleConfig().MaxTokens
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	clientConfig := &genai.ClientConfig{}

	if config.UseVertexAI {
		clientConfig.Project = config.ProjectID
		clientConfig.Location = config.Location
		clientConfig.Backend = genai.BackendVertexAI
	} else {
		clientConfig.APIKey = config.APIKey
		clientConfig.Backend = genai.BackendGeminiAPI
	}

	client, err := genai.NewClient(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("google provider: failed to create client: %w", err)
	}

	return &GoogleProvider{
		client: client,
		config: config,
		skills: skills,
	}, nil
}

// Name returns the provider identifier
func (g *GoogleProvider) Name() string {
	return string(ProviderTypeGoogle)
}

// Generate performs a non-streaming completion request
func (g *GoogleProvider) Generate(ctx context.Context, req *Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = g.config.Model
	}

	contents := g.convertMessages(req.Messages)
	genConfig := g.buildGenerateConfig(req)

	result, err := g.client.Models.GenerateContent(ctx, model, contents, genConfig)
	if err != nil {
		return nil, fmt.Errorf("google generate: %w", err)
	}

	return g.convertResponse(result, model), nil
}

// Stream performs a streaming completion request
func (g *GoogleProvider) Stream(ctx context.Context, req *Request, handler StreamHandler) error {
	model := req.Model
	if model == "" {
		model = g.config.Model
	}

	contents := g.convertMessages(req.Messages)
	genConfig := g.buildGenerateConfig(req)

	// Send start chunk
	if err := handler(&StreamChunk{
		Index:     0,
		Type:      ChunkTypeStart,
		Timestamp: time.Now(),
	}); err != nil {
		return err
	}

	var chunkIndex int
	var totalInputTokens, totalOutputTokens int
	var stopReason StopReason
	toolCallsSeen := map[string]bool{}

	// Use range-based iteration (Go 1.23+ iterator pattern)
	for resp, err := range g.client.Models.GenerateContentStream(ctx, model, contents, genConfig) {
		if err != nil {
			handler(&StreamChunk{
				Index:     chunkIndex + 1,
				Type:      ChunkTypeError,
				Text:      err.Error(),
				Timestamp: time.Now(),
			})
			return fmt.Errorf("google stream: %w", err)
		}

		chunkIndex++

		// Extract text from candidates
		text := resp.Text()
		if text != "" {
			if err := handler(&StreamChunk{
				Index:     chunkIndex,
				Type:      ChunkTypeText,
				Text:      text,
				Timestamp: time.Now(),
			}); err != nil {
				return err
			}
		}

		// Handle function calls
		if resp.FunctionCalls() != nil {
			for _, fc := range resp.FunctionCalls() {
				argsJSON, _ := json.Marshal(fc.Args)
				if !toolCallsSeen[fc.ID] {
					toolCallsSeen[fc.ID] = true
					if err := handler(&StreamChunk{
						Index: chunkIndex,
						Type:  ChunkTypeToolStart,
						ToolCall: &ToolCallChunk{
							ID:   fc.ID,
							Name: fc.Name,
						},
						Timestamp: time.Now(),
					}); err != nil {
						return err
					}
				}
				if err := handler(&StreamChunk{
					Index: chunkIndex,
					Type:  ChunkTypeToolDelta,
					ToolCall: &ToolCallChunk{
						ID:             fc.ID,
						ArgumentsDelta: string(argsJSON),
					},
					Timestamp: time.Now(),
				}); err != nil {
					return err
				}
			}
		}

		if resp.UsageMetadata != nil {
			totalInputTokens = int(resp.UsageMetadata.PromptTokenCount)
			totalOutputTokens = int(resp.UsageMetadata.CandidatesTokenCount)
		}
		if len(resp.Candidates) > 0 {
			switch resp.Candidates[0].FinishReason {
			case genai.FinishReasonStop:
				stopReason = StopReasonEndTurn
			case genai.FinishReasonMaxTokens:
				stopReason = StopReasonMaxTokens
			case genai.FinishReasonSafety:
				stopReason = StopReasonError
			}
		}
	}

	if stopReason == "" {
		stopReason = StopReasonEndTurn
	}

	return handler(&StreamChunk{
		Index:      chunkIndex + 1,
		Type:       ChunkTypeEnd,
		StopReason: stopReason,
		Usage: &Usage{
			InputTokens:  totalInputTokens,
			OutputTokens: totalOutputTokens,
			TotalTokens:  totalInputTokens + totalOutputTokens,
		},
		Timestamp: time.Now(),
	})
}

// ValidateConfig checks if the provider configuration is valid
func (g *GoogleProvider) ValidateConfig() error {
	return g.config.Validate()
}

// SupportsModel checks if the provider supports the given model
func (g *GoogleProvider) SupportsModel(model string) bool {
	return googleModels[model]
}

// DefaultModel returns the provider's default model
func (g *GoogleProvider) DefaultModel() string {
	return g.config.Model
}

// Close cleans up any resources
func (g *GoogleProvider) Close() error {
	// genai.Client doesn't have a Close method in current SDK
	return nil
}

// buildGenerateConfig constructs generation config from a Request
func (g *GoogleProvider) buildGenerateConfig(req *Request) *genai.GenerateContentConfig {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = g.config.MaxTokens
	}

	config := &genai.GenerateContentConfig{
		MaxOutputTokens: int32(maxTokens),
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{
				genai.NewPartFromText(g.config.SystemPrompt),
			},
		},
	}

	if len(g.skills) > 0 {
		config.SystemInstruction.Parts = append(config.SystemInstruction.Parts, genai.NewPartFromText(
			skills.ToPrompt(g.skills),
		))
	}

	if req.Temperature != nil {
		temp := float32(*req.Temperature)
		config.Temperature = &temp
	} else if g.config.Temperature > 0 {
		temp := float32(g.config.Temperature)
		config.Temperature = &temp
	}

	if req.TopP != nil {
		topP := float32(*req.TopP)
		config.TopP = &topP
	}

	if g.config.TopK != nil {
		topK := float32(*g.config.TopK)
		config.TopK = &topK
	}

	if len(req.StopSequences) > 0 {
		config.StopSequences = req.StopSequences
	}

	if len(req.Tools) > 0 {
		config.Tools = g.convertTools(req.Tools)
	}

	if len(g.config.SafetySettings) > 0 {
		config.SafetySettings = g.convertSafetySettings()
	}

	return config
}

// convertMessages converts generic messages to Gemini format
func (g *GoogleProvider) convertMessages(messages []Message) []*genai.Content {
	result := make([]*genai.Content, 0, len(messages))

	for _, msg := range messages {
		content := &genai.Content{}

		switch msg.Role {
		case RoleUser:
			content.Role = "user"
		case RoleAssistant:
			content.Role = "model"
		case RoleSystem:
			// System messages handled via SystemInstruction in config
			continue
		case RoleTool:
			content.Role = "function"
		}

		// Add text content
		if msg.Content != "" {
			content.Parts = append(content.Parts, genai.NewPartFromText(msg.Content))
		}

		// Add tool calls (function responses)
		if msg.Role == RoleTool && msg.ToolCallID != "" {
			content.Parts = append(content.Parts, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					ID:       msg.ToolCallID,
					Name:     msg.ToolCallID, // Gemini uses name, not ID
					Response: map[string]any{"result": msg.Content},
				},
			})
		}

		// Add function calls from assistant
		for _, tc := range msg.ToolCalls {
			var args map[string]any
			json.Unmarshal([]byte(tc.Arguments), &args)

			content.Parts = append(content.Parts, &genai.Part{
				FunctionCall: &genai.FunctionCall{
					ID:   tc.ID,
					Name: tc.Name,
					Args: args,
				},
			})
		}

		result = append(result, content)
	}

	return result
}

// convertTools converts generic tools to Gemini format
func (g *GoogleProvider) convertTools(tools []Tool) []*genai.Tool {
	declarations := make([]*genai.FunctionDeclaration, len(tools))

	for i, tool := range tools {
		// Convert the full JSON Schema to Gemini schema format
		schema := convertJSONSchemaToGenaiSchema(tool.Parameters)

		declarations[i] = &genai.FunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  schema,
		}
	}

	return []*genai.Tool{
		{FunctionDeclarations: declarations},
	}
}

// convertJSONSchemaToGenaiSchema converts a JSON Schema to genai.Schema
func convertJSONSchemaToGenaiSchema(schemaMap map[string]any) *genai.Schema {
	if schemaMap == nil {
		return &genai.Schema{Type: genai.TypeObject}
	}

	return &genai.Schema{
		Type:        extractSchemaType(schemaMap),
		Description: extractString(schemaMap, "description"),
		Properties:  extractProperties(schemaMap),
		Required:    extractStringSlice(schemaMap, "required"),
		Items:       extractItems(schemaMap),
		Enum:        extractStringSlice(schemaMap, "enum"),
	}
}

func extractSchemaType(schemaMap map[string]any) genai.Type {
	if t, ok := schemaMap["type"].(string); ok {
		return convertToGenaiType(t)
	}
	return genai.TypeObject
}

func extractString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func extractStringSlice(m map[string]any, key string) []string {
	vals, ok := m[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(vals))
	for _, v := range vals {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func extractProperties(schemaMap map[string]any) map[string]*genai.Schema {
	props, ok := schemaMap["properties"].(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]*genai.Schema, len(props))
	for name, propDef := range props {
		if propMap, ok := propDef.(map[string]any); ok {
			result[name] = convertJSONSchemaToGenaiSchema(propMap)
		}
	}
	return result
}

func extractItems(schemaMap map[string]any) *genai.Schema {
	if items, ok := schemaMap["items"].(map[string]any); ok {
		return convertJSONSchemaToGenaiSchema(items)
	}
	return nil
}

// convertSafetySettings converts config safety settings to Gemini format
func (g *GoogleProvider) convertSafetySettings() []*genai.SafetySetting {
	result := make([]*genai.SafetySetting, len(g.config.SafetySettings))

	for i, ss := range g.config.SafetySettings {
		result[i] = &genai.SafetySetting{
			Category:  genai.HarmCategory(ss.Category),
			Threshold: genai.HarmBlockThreshold(ss.Threshold),
		}
	}

	return result
}

// convertResponse converts a Gemini response to generic format
func (g *GoogleProvider) convertResponse(result *genai.GenerateContentResponse, model string) *Response {
	resp := &Response{
		Model:            model,
		ProviderMetadata: make(map[string]any),
	}

	// Extract text content
	resp.Content = result.Text()

	// Extract function calls
	if fcs := result.FunctionCalls(); fcs != nil {
		for _, fc := range fcs {
			argsJSON, _ := json.Marshal(fc.Args)
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID:        fc.ID,
				Name:      fc.Name,
				Arguments: string(argsJSON),
			})
		}
	}

	// Extract usage
	if result.UsageMetadata != nil {
		resp.Usage = Usage{
			InputTokens:  int(result.UsageMetadata.PromptTokenCount),
			OutputTokens: int(result.UsageMetadata.CandidatesTokenCount),
			TotalTokens:  int(result.UsageMetadata.TotalTokenCount),
		}
	}

	// Determine stop reason
	if len(result.Candidates) > 0 {
		candidate := result.Candidates[0]
		switch candidate.FinishReason {
		case genai.FinishReasonStop:
			resp.StopReason = StopReasonEndTurn
		case genai.FinishReasonMaxTokens:
			resp.StopReason = StopReasonMaxTokens
		case genai.FinishReasonSafety:
			resp.StopReason = StopReasonError
		default:
			resp.StopReason = StopReasonEndTurn
		}
	}

	return resp
}

// convertToGenaiType converts a JSON schema type string to genai.Type
func convertToGenaiType(typeStr string) genai.Type {
	switch typeStr {
	case "string":
		return genai.TypeString
	case "number":
		return genai.TypeNumber
	case "integer":
		return genai.TypeInteger
	case "boolean":
		return genai.TypeBoolean
	case "array":
		return genai.TypeArray
	case "object":
		return genai.TypeObject
	default:
		return genai.TypeString
	}
}
