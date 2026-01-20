package providers

import (
	"encoding/json"
	"errors"
	"sync"
)

// Token count validation errors.
var (
	ErrNegativeTokenCount = errors.New("token count cannot be negative")
	ErrTokenCountOverflow = errors.New("token count exceeds maximum safe value")
)

// MaxSafeTokenCount is the maximum token count that can be safely processed.
// This prevents overflow issues and unrealistic token counts.
const MaxSafeTokenCount = 100_000_000

type TokenCounter interface {
	Count(messages []Message) (int, error)
	CountText(text string) (int, error)
	MaxContextTokens(model string) int
}

type TokenCounterConfig struct {
	FallbackCharsPerToken  int
	IncludeToolDefinitions bool
}

func DefaultTokenCounterConfig() TokenCounterConfig {
	return TokenCounterConfig{
		FallbackCharsPerToken:  4,
		IncludeToolDefinitions: true,
	}
}

type CharacterBasedCounter struct {
	config TokenCounterConfig
}

func NewCharacterBasedCounter(config TokenCounterConfig) *CharacterBasedCounter {
	if config.FallbackCharsPerToken <= 0 {
		config.FallbackCharsPerToken = 4
	}
	return &CharacterBasedCounter{config: config}
}

func (c *CharacterBasedCounter) Count(messages []Message) (int, error) {
	total := 0
	for _, msg := range messages {
		total += c.countMessage(msg)
	}
	return total, nil
}

func (c *CharacterBasedCounter) countMessage(msg Message) int {
	tokens := c.charsToTokens(len(msg.Content))
	tokens += c.countToolCalls(msg.ToolCalls)
	return tokens
}

func (c *CharacterBasedCounter) countToolCalls(calls []ToolCall) int {
	tokens := 0
	for _, call := range calls {
		tokens += c.charsToTokens(len(call.Name))
		tokens += c.charsToTokens(len(call.Arguments))
	}
	return tokens
}

func (c *CharacterBasedCounter) CountText(text string) (int, error) {
	return c.charsToTokens(len(text)), nil
}

func (c *CharacterBasedCounter) charsToTokens(chars int) int {
	return (chars + c.config.FallbackCharsPerToken - 1) / c.config.FallbackCharsPerToken
}

func (c *CharacterBasedCounter) MaxContextTokens(model string) int {
	return getModelContextLimit(model)
}

type ProviderTokenCounter struct {
	mu       sync.RWMutex
	config   TokenCounterConfig
	counters map[string]TokenCounter
	fallback *CharacterBasedCounter
}

func NewProviderTokenCounter(config TokenCounterConfig) *ProviderTokenCounter {
	return &ProviderTokenCounter{
		config:   config,
		counters: make(map[string]TokenCounter),
		fallback: NewCharacterBasedCounter(config),
	}
}

func (p *ProviderTokenCounter) Count(messages []Message) (int, error) {
	return p.fallback.Count(messages)
}

func (p *ProviderTokenCounter) CountText(text string) (int, error) {
	return p.fallback.CountText(text)
}

func (p *ProviderTokenCounter) CountWithTools(messages []Message, tools []Tool) (int, error) {
	msgTokens, err := p.Count(messages)
	if err != nil {
		return 0, err
	}
	if !p.config.IncludeToolDefinitions {
		return msgTokens, nil
	}
	toolTokens := p.countTools(tools)
	return msgTokens + toolTokens, nil
}

func (p *ProviderTokenCounter) countTools(tools []Tool) int {
	tokens := 0
	for _, tool := range tools {
		tokens += p.countTool(tool)
	}
	return tokens
}

func (p *ProviderTokenCounter) countTool(tool Tool) int {
	tokens := p.fallback.charsToTokens(len(tool.Name))
	tokens += p.fallback.charsToTokens(len(tool.Description))
	tokens += p.countParameters(tool.Parameters)
	return tokens
}

func (p *ProviderTokenCounter) countParameters(params map[string]any) int {
	data, err := json.Marshal(params)
	if err != nil {
		return 0
	}
	return p.fallback.charsToTokens(len(data))
}

func (p *ProviderTokenCounter) MaxContextTokens(model string) int {
	return getModelContextLimit(model)
}

func (p *ProviderTokenCounter) GetCounter(model string) TokenCounter {
	p.mu.RLock()
	counter, exists := p.counters[model]
	p.mu.RUnlock()
	if exists {
		return counter
	}
	return p.fallback
}

func (p *ProviderTokenCounter) RegisterCounter(model string, counter TokenCounter) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counters[model] = counter
}

// ValidateTokenCount checks if a token count is within valid bounds.
// Returns an error if the count is negative or exceeds MaxSafeTokenCount.
func ValidateTokenCount(count int) error {
	if count < 0 {
		return ErrNegativeTokenCount
	}
	if count > MaxSafeTokenCount {
		return ErrTokenCountOverflow
	}
	return nil
}

// ValidatedCount counts tokens and validates the result is within bounds.
func (p *ProviderTokenCounter) ValidatedCount(messages []Message) (int, error) {
	count, err := p.Count(messages)
	if err != nil {
		return 0, err
	}
	if err := ValidateTokenCount(count); err != nil {
		return 0, err
	}
	return count, nil
}

// ValidatedCountText counts text tokens and validates the result is within bounds.
func (p *ProviderTokenCounter) ValidatedCountText(text string) (int, error) {
	count, err := p.CountText(text)
	if err != nil {
		return 0, err
	}
	if err := ValidateTokenCount(count); err != nil {
		return 0, err
	}
	return count, nil
}

// ValidatedCountWithTools counts tokens including tool definitions and validates bounds.
func (p *ProviderTokenCounter) ValidatedCountWithTools(messages []Message, tools []Tool) (int, error) {
	count, err := p.CountWithTools(messages, tools)
	if err != nil {
		return 0, err
	}
	if err := ValidateTokenCount(count); err != nil {
		return 0, err
	}
	return count, nil
}

// ValidatingCounter wraps a TokenCounter and validates all returned counts.
// W12.47: Ensures token counts are within valid bounds.
type ValidatingCounter struct {
	inner TokenCounter
}

// NewValidatingCounter creates a counter that validates all token counts.
func NewValidatingCounter(inner TokenCounter) *ValidatingCounter {
	return &ValidatingCounter{inner: inner}
}

// Count returns validated token count for messages.
func (v *ValidatingCounter) Count(messages []Message) (int, error) {
	count, err := v.inner.Count(messages)
	if err != nil {
		return 0, err
	}
	if err := ValidateTokenCount(count); err != nil {
		return 0, err
	}
	return count, nil
}

// CountText returns validated token count for text.
func (v *ValidatingCounter) CountText(text string) (int, error) {
	count, err := v.inner.CountText(text)
	if err != nil {
		return 0, err
	}
	if err := ValidateTokenCount(count); err != nil {
		return 0, err
	}
	return count, nil
}

// MaxContextTokens returns the maximum context tokens for a model.
func (v *ValidatingCounter) MaxContextTokens(model string) int {
	return v.inner.MaxContextTokens(model)
}

// GetValidatedCounter returns a counter wrapped with validation for the given model.
func (p *ProviderTokenCounter) GetValidatedCounter(model string) TokenCounter {
	counter := p.GetCounter(model)
	return NewValidatingCounter(counter)
}

var modelContextLimits = map[string]int{
	"claude-opus-4-5-20251101":   200000,
	"claude-sonnet-4-5-20250901": 1000000,
	"claude-haiku-4-5-20251001":  200000,
	"codex-5-2-20250901":         128000,
	"gpt-4o":                     128000,
	"gpt-4o-mini":                128000,
	"gemini-3-pro":               2000000,
	"gemini-3-pro-preview":       2000000,
	"gemini-3-flash":             1000000,
}

func getModelContextLimit(model string) int {
	if limit, exists := modelContextLimits[model]; exists {
		return limit
	}
	return 128000
}
