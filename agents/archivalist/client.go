package archivalist

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
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

// Client wraps a provider for summary generation.
//
// Both provider AND model are accessed via lookup functions rather than
// stored values so the client always sees the live state on the
// archivalist. The historical pattern (snapshot + manual rebuild in
// SetProvider/SwapModel) works as long as every hot-swap call site
// remembers to rebuild — but that's a footgun: every new sub-component
// that takes a provider has to be remembered, and a stale model on the
// next request after SwapModel goes silently to the wrong endpoint. The
// lookup pattern makes both bug classes structurally impossible. See
// agents/archivalist/synthesis.go for the same discipline applied to the
// synthesizer.
type Client struct {
	providerLookup  func() archivalistProvider
	modelLookup     func() string
	systemPrompt    string
	maxOutputTokens int
}

// ClientConfig configures the AI client.
type ClientConfig struct {
	// ProviderLookup returns the live LLM provider from the owning agent.
	// Resolved per call so a SetProvider / SwapModel on the agent is
	// observed immediately without the client having to be re-bound.
	// Required — see NewClient.
	ProviderLookup func() archivalistProvider
	// ModelLookup returns the live model identifier from the owning agent.
	// Resolved per call so SwapModel takes effect on the next request
	// without the client having to be re-bound. Required.
	ModelLookup     func() string
	SystemPrompt    string
	MaxOutputTokens int
}

// NewClient creates a new AI client for summary generation. Panics if
// ProviderLookup or ModelLookup is nil — half-constructed clients that
// pretend to be live but cannot generate are the worst possible failure
// mode.
func NewClient(cfg ClientConfig) *Client {
	if cfg.ProviderLookup == nil {
		panic("archivalist.NewClient: ProviderLookup is required (see client.go for rationale)")
	}
	if cfg.ModelLookup == nil {
		panic("archivalist.NewClient: ModelLookup is required (see client.go for rationale)")
	}

	maxTokens := DefaultMaxOutputTokens
	if cfg.MaxOutputTokens > 0 {
		maxTokens = cfg.MaxOutputTokens
	}

	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = DefaultSystemPrompt
	}

	return &Client{
		providerLookup:  cfg.ProviderLookup,
		modelLookup:     cfg.ModelLookup,
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
	provider := c.providerLookup()
	if provider == nil {
		return nil, fmt.Errorf("client: %w: LLM provider not yet wired", agentshared.ErrAgentNotReady)
	}
	model := c.modelLookup()
	if strings.TrimSpace(model) == "" {
		model = ModelSonnet45
	}

	req := &providers.Request{
		SystemPrompt: c.systemPrompt,
		Model:        model,
		MaxTokens:    c.maxOutputTokens,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: prompt}},
	}
	c.applyGenerationRuntimeProfile(req)

	agentshared.LogLLMCallStartedFromContext(ctx, req)
	llmStart := time.Now()
	resp, err := provider.Complete(ctx, req)
	agentshared.LogLLMCallFromContext(ctx, req.Model, resp, time.Since(llmStart), err)
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
