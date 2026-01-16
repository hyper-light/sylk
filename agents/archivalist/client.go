package archivalist

import (
	"context"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	// MaxContextTokens is the 1M token context window for Sonnet 4.5
	MaxContextTokens = 1_000_000

	// DefaultMaxOutputTokens for summary generation
	DefaultMaxOutputTokens = 8192

	// ModelSonnet45 is the model identifier for Claude Sonnet 4.5
	ModelSonnet45 = "claude-sonnet-4-5-20250929"
)

// Client wraps the Anthropic SDK for summary generation
type Client struct {
	anthropic       anthropic.Client
	systemPrompt    string
	maxOutputTokens int64
}

// ClientConfig configures the AI client
type ClientConfig struct {
	AnthropicAPIKey string
	SystemPrompt    string
	MaxOutputTokens int
}

// NewClient creates a new AI client for summary generation
func NewClient(cfg ClientConfig) (*Client, error) {
	opts := []option.RequestOption{}
	if cfg.AnthropicAPIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.AnthropicAPIKey))
	}

	client := anthropic.NewClient(opts...)

	maxTokens := int64(DefaultMaxOutputTokens)
	if cfg.MaxOutputTokens > 0 {
		maxTokens = int64(cfg.MaxOutputTokens)
	}

	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = DefaultSystemPrompt
	}

	return &Client{
		anthropic:       client,
		systemPrompt:    systemPrompt,
		maxOutputTokens: maxTokens,
	}, nil
}

// GenerateSummary creates a summary using Claude Sonnet 4.5
func (c *Client) GenerateSummary(ctx context.Context, content string) (*GeneratedSummary, error) {
	prompt := FormatSummaryPrompt(content)
	return c.generate(ctx, prompt, nil)
}

// GenerateSummaryFromSubmissions creates a summary from multiple submissions
func (c *Client) GenerateSummaryFromSubmissions(ctx context.Context, submissions []Submission) (*GeneratedSummary, error) {
	if len(submissions) == 0 {
		return nil, fmt.Errorf("no submissions provided")
	}

	// Build content from submissions
	var content string
	var sourceIDs []string

	for i, sub := range submissions {
		content += fmt.Sprintf("\n--- Submission %d ---\n", i+1)
		if sub.Summary != nil {
			content += fmt.Sprintf("Type: Summary\nSource: %s\nContent:\n%s\n", sub.Summary.Source, sub.Summary.Content)
			sourceIDs = append(sourceIDs, sub.Summary.ID)
		} else if sub.PromptResponse != nil {
			content += fmt.Sprintf("Type: Prompt/Response\nSource: %s\nPrompt:\n%s\nResponse:\n%s\n",
				sub.PromptResponse.Source, sub.PromptResponse.Prompt, sub.PromptResponse.Response)
			sourceIDs = append(sourceIDs, sub.PromptResponse.ID)
		}
	}

	prompt := FormatMultiSourcePrompt(len(submissions), content)
	return c.generate(ctx, prompt, sourceIDs)
}

// generate performs the actual API call to Claude Sonnet 4.5
func (c *Client) generate(ctx context.Context, prompt string, sourceIDs []string) (*GeneratedSummary, error) {
	message, err := c.anthropic.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(ModelSonnet45),
		MaxTokens: c.maxOutputTokens,
		System: []anthropic.TextBlockParam{
			{Text: c.systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate summary: %w", err)
	}

	// Extract text content from response
	var responseContent string
	for _, block := range message.Content {
		if block.Type == "text" {
			responseContent += block.Text
		}
	}

	tokensUsed := int(message.Usage.InputTokens + message.Usage.OutputTokens)

	return &GeneratedSummary{
		Content:    responseContent,
		SourceIDs:  sourceIDs,
		CreatedAt:  time.Now(),
		TokensUsed: tokensUsed,
	}, nil
}

// GeneratedSummary represents a summary created by Sonnet 4.5
type GeneratedSummary struct {
	ID         string    `json:"id"`
	Content    string    `json:"content"`
	SourceIDs  []string  `json:"source_ids"`
	CreatedAt  time.Time `json:"created_at"`
	TokensUsed int       `json:"tokens_used"`
}
