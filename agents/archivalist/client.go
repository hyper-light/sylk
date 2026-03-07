package archivalist

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/providers"
)

const (
	// MaxContextTokens is the 1M token context window for Sonnet 4.5
	MaxContextTokens = 1_000_000

	// DefaultMaxOutputTokens for summary generation
	DefaultMaxOutputTokens = 8192

	// ModelSonnet45 is the model identifier for Claude Sonnet 4.6
	ModelSonnet45 = "claude-sonnet-4-6"
)

// Client wraps a provider for summary generation
type Client struct {
	provider        archivalistProvider
	model           string
	systemPrompt    string
	maxOutputTokens int
}

// ClientConfig configures the AI client
type ClientConfig struct {
	Provider        archivalistProvider
	Model           string
	SystemPrompt    string
	MaxOutputTokens int
}

// NewClient creates a new AI client for summary generation
func NewClient(cfg ClientConfig) *Client {
	maxTokens := DefaultMaxOutputTokens
	if cfg.MaxOutputTokens > 0 {
		maxTokens = cfg.MaxOutputTokens
	}

	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = DefaultSystemPrompt
	}

	model := cfg.Model
	if model == "" {
		model = ModelSonnet45
	}

	return &Client{
		provider:        cfg.Provider,
		model:           model,
		systemPrompt:    systemPrompt,
		maxOutputTokens: maxTokens,
	}
}

// GenerateSummary creates a summary using the configured provider
func (c *Client) GenerateSummary(ctx context.Context, content string) (*GeneratedSummary, error) {
	prompt := FormatSummaryPrompt(content)
	return c.generate(ctx, prompt, nil)
}

// GenerateSummaryFromSubmissions creates a summary from multiple submissions
func (c *Client) GenerateSummaryFromSubmissions(ctx context.Context, submissions []Submission) (*GeneratedSummary, error) {
	if len(submissions) == 0 {
		return nil, fmt.Errorf("no submissions provided")
	}

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

// generate performs the actual LLM call via the provider
func (c *Client) generate(ctx context.Context, prompt string, sourceIDs []string) (*GeneratedSummary, error) {
	if c.provider == nil {
		return nil, fmt.Errorf("client: no LLM provider configured")
	}

	req := &providers.Request{
		SystemPrompt: c.systemPrompt,
		Model:        c.model,
		MaxTokens:    c.maxOutputTokens,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: prompt}},
	}
	c.applyGenerationRuntimeProfile(req)

	resp, err := c.provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to generate summary: %w", err)
	}

	return &GeneratedSummary{
		Content:    strings.TrimSpace(resp.Content),
		SourceIDs:  sourceIDs,
		CreatedAt:  time.Now(),
		TokensUsed: resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}, nil
}

// GeneratedSummary represents a summary created by the LLM
type GeneratedSummary struct {
	ID         string    `json:"id"`
	Content    string    `json:"content"`
	SourceIDs  []string  `json:"source_ids"`
	CreatedAt  time.Time `json:"created_at"`
	TokensUsed int       `json:"tokens_used"`
}
