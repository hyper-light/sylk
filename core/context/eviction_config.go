// Package context provides ES.5 AgentEvictionConfig for per-agent eviction configuration.
// AgentEvictionConfig defines per-agent-type eviction configurations including
// token thresholds, strategies to use, and content types to preserve.
package context

import (
	"context"
	"sort"
	"sync"
)

// AgentEvictionConfig defines eviction behavior for a specific agent type.
type AgentEvictionConfig struct {
	// AgentType identifies the agent type this config applies to.
	AgentType string

	// MaxTokens is the maximum token capacity for the agent context.
	MaxTokens int

	// EvictionTrigger is the percentage of MaxTokens that triggers eviction.
	// For example, 0.8 means eviction starts when context reaches 80% capacity.
	EvictionTrigger float64

	// Strategies defines the eviction strategies to use, in priority order.
	Strategies []EvictionStrategy

	// PreserveTypes lists content types that should never be evicted.
	PreserveTypes []ContentType
}

// ShouldEvict returns true if current token count triggers eviction.
func (c *AgentEvictionConfig) ShouldEvict(currentTokens int) bool {
	if c.MaxTokens <= 0 {
		return false
	}
	threshold := int(float64(c.MaxTokens) * c.EvictionTrigger)
	return currentTokens >= threshold
}

// TokensToFree returns the number of tokens to free to reach target capacity.
func (c *AgentEvictionConfig) TokensToFree(currentTokens int) int {
	if c.MaxTokens <= 0 || currentTokens <= 0 {
		return 0
	}

	targetTokens := int(float64(c.MaxTokens) * (c.EvictionTrigger - 0.1))
	toFree := currentTokens - targetTokens

	if toFree < 0 {
		return 0
	}
	return toFree
}

// IsPreserved returns true if the content type should never be evicted.
func (c *AgentEvictionConfig) IsPreserved(contentType ContentType) bool {
	for _, pt := range c.PreserveTypes {
		if pt == contentType {
			return true
		}
	}
	return false
}

// Clone creates a deep copy of the config.
func (c *AgentEvictionConfig) Clone() *AgentEvictionConfig {
	if c == nil {
		return nil
	}

	clone := &AgentEvictionConfig{
		AgentType:       c.AgentType,
		MaxTokens:       c.MaxTokens,
		EvictionTrigger: c.EvictionTrigger,
	}

	clone.Strategies = cloneStrategies(c.Strategies)
	clone.PreserveTypes = cloneContentTypes(c.PreserveTypes)

	return clone
}

func cloneStrategies(strategies []EvictionStrategy) []EvictionStrategy {
	if strategies == nil {
		return nil
	}
	result := make([]EvictionStrategy, len(strategies))
	copy(result, strategies)
	return result
}

func cloneContentTypes(types []ContentType) []ContentType {
	if types == nil {
		return nil
	}
	result := make([]ContentType, len(types))
	copy(result, types)
	return result
}

// AgentConfigRegistry manages eviction configs for all agent types.
type AgentConfigRegistry struct {
	mu       sync.RWMutex
	configs  map[string]*AgentEvictionConfig
	defaults map[string]*AgentEvictionConfig
}

// NewAgentConfigRegistry creates a new registry with default configs.
func NewAgentConfigRegistry() *AgentConfigRegistry {
	r := &AgentConfigRegistry{
		configs:  make(map[string]*AgentEvictionConfig),
		defaults: make(map[string]*AgentEvictionConfig),
	}
	r.initDefaults()
	return r
}

func (r *AgentConfigRegistry) initDefaults() {
	r.defaults["engineer"] = DefaultEngineerConfig()
	r.defaults["architect"] = DefaultArchitectConfig()
	r.defaults["librarian"] = DefaultLibrarianConfig()
	r.defaults["guide"] = DefaultGuideConfig()
	r.defaults["tester"] = DefaultTesterConfig()
	r.defaults["inspector"] = DefaultInspectorConfig()
}

// GetConfig returns the config for an agent type, or nil if not found.
func (r *AgentConfigRegistry) GetConfig(agentType string) *AgentEvictionConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if cfg, ok := r.configs[agentType]; ok {
		return cfg.Clone()
	}
	return r.getDefaultConfig(agentType)
}

func (r *AgentConfigRegistry) getDefaultConfig(agentType string) *AgentEvictionConfig {
	if cfg, ok := r.defaults[agentType]; ok {
		return cfg.Clone()
	}
	return nil
}

// SetConfig registers a custom config for an agent type.
func (r *AgentConfigRegistry) SetConfig(config *AgentEvictionConfig) {
	if config == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.configs[config.AgentType] = config.Clone()
}

// ResetToDefault removes custom config and reverts to default.
func (r *AgentConfigRegistry) ResetToDefault(agentType string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.configs, agentType)
}

// ListAgentTypes returns all agent types with configs.
func (r *AgentConfigRegistry) ListAgentTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	typeSet := make(map[string]struct{})
	for t := range r.defaults {
		typeSet[t] = struct{}{}
	}
	for t := range r.configs {
		typeSet[t] = struct{}{}
	}

	return mapKeysToSortedSlice(typeSet)
}

func mapKeysToSortedSlice(m map[string]struct{}) []string {
	result := make([]string, 0, len(m))
	for k := range m {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// DefaultEngineerConfig returns the default config for Engineer agents.
// Engineers are more aggressive with eviction but preserve code context.
func DefaultEngineerConfig() *AgentEvictionConfig {
	return &AgentEvictionConfig{
		AgentType:       "engineer",
		MaxTokens:       128000,
		EvictionTrigger: 0.75,
		Strategies:      nil, // Will use registry's default strategies
		PreserveTypes: []ContentType{
			ContentTypeCodeFile,
			ContentTypeToolResult,
		},
	}
}

// DefaultArchitectConfig returns the default config for Architect agents.
// Architects preserve planning context and workflows.
func DefaultArchitectConfig() *AgentEvictionConfig {
	return &AgentEvictionConfig{
		AgentType:       "architect",
		MaxTokens:       200000,
		EvictionTrigger: 0.80,
		Strategies:      nil,
		PreserveTypes: []ContentType{
			ContentTypePlanWorkflow,
			ContentTypeContextRef,
		},
	}
}

// DefaultLibrarianConfig returns the default config for Librarian agents.
// Librarians preserve research context and knowledge.
func DefaultLibrarianConfig() *AgentEvictionConfig {
	return &AgentEvictionConfig{
		AgentType:       "librarian",
		MaxTokens:       1000000,
		EvictionTrigger: 0.85,
		Strategies:      nil,
		PreserveTypes: []ContentType{
			ContentTypeResearchPaper,
			ContentTypeWebFetch,
		},
	}
}

// DefaultGuideConfig returns the default config for Guide agents.
// Guides use minimal eviction to maintain conversation continuity.
func DefaultGuideConfig() *AgentEvictionConfig {
	return &AgentEvictionConfig{
		AgentType:       "guide",
		MaxTokens:       64000,
		EvictionTrigger: 0.90,
		Strategies:      nil,
		PreserveTypes: []ContentType{
			ContentTypeUserPrompt,
			ContentTypeAssistantReply,
		},
	}
}

// DefaultTesterConfig returns the default config for Tester agents.
// Testers preserve test results and related tool outputs.
func DefaultTesterConfig() *AgentEvictionConfig {
	return &AgentEvictionConfig{
		AgentType:       "tester",
		MaxTokens:       128000,
		EvictionTrigger: 0.80,
		Strategies:      nil,
		PreserveTypes: []ContentType{
			ContentTypeToolResult,
			ContentTypeCodeFile,
		},
	}
}

// DefaultInspectorConfig returns the default config for Inspector agents.
// Inspectors preserve findings and analysis results.
func DefaultInspectorConfig() *AgentEvictionConfig {
	return &AgentEvictionConfig{
		AgentType:       "inspector",
		MaxTokens:       128000,
		EvictionTrigger: 0.80,
		Strategies:      nil,
		PreserveTypes: []ContentType{
			ContentTypeToolResult,
			ContentTypeContextRef,
		},
	}
}

// StrategyChain chains multiple eviction strategies together.
type StrategyChain struct {
	strategies []EvictionStrategyWithPriority
}

// NewStrategyChain creates a new strategy chain from the given strategies.
// Strategies are sorted by priority (lower priority value = higher precedence).
func NewStrategyChain(strategies ...EvictionStrategyWithPriority) *StrategyChain {
	chain := &StrategyChain{
		strategies: make([]EvictionStrategyWithPriority, 0, len(strategies)),
	}

	for _, s := range strategies {
		if s != nil {
			chain.strategies = append(chain.strategies, s)
		}
	}

	chain.sortByPriority()
	return chain
}

func (c *StrategyChain) sortByPriority() {
	sort.Slice(c.strategies, func(i, j int) bool {
		return c.strategies[i].Priority() < c.strategies[j].Priority()
	})
}

// SelectForEviction applies strategies in order until target tokens are selected.
func (c *StrategyChain) SelectForEviction(
	ctx context.Context,
	entries []*ContentEntry,
	targetTokens int,
) ([]*ContentEntry, error) {
	if len(entries) == 0 || targetTokens <= 0 {
		return nil, nil
	}

	var selected []*ContentEntry
	remaining := make([]*ContentEntry, len(entries))
	copy(remaining, entries)
	tokensSelected := 0

	for _, strategy := range c.strategies {
		if tokensSelected >= targetTokens {
			break
		}

		needed := targetTokens - tokensSelected
		result, err := strategy.SelectForEviction(ctx, remaining, needed)
		if err != nil {
			return selected, err
		}
		selected, tokensSelected = appendSelected(selected, result, tokensSelected)
		remaining = removeSelected(remaining, result)
	}

	return selected, nil
}

func appendSelected(
	selected []*ContentEntry,
	result []*ContentEntry,
	tokensSelected int,
) ([]*ContentEntry, int) {
	for _, e := range result {
		selected = append(selected, e)
		tokensSelected += e.TokenCount
	}
	return selected, tokensSelected
}

func removeSelected(
	remaining []*ContentEntry,
	selected []*ContentEntry,
) []*ContentEntry {
	if len(selected) == 0 {
		return remaining
	}

	selectedSet := makeEntryIDSet(selected)
	return filterOutSelected(remaining, selectedSet)
}

func makeEntryIDSet(entries []*ContentEntry) map[string]struct{} {
	set := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		set[e.ID] = struct{}{}
	}
	return set
}

func filterOutSelected(
	entries []*ContentEntry,
	selectedSet map[string]struct{},
) []*ContentEntry {
	result := make([]*ContentEntry, 0, len(entries))
	for _, e := range entries {
		if _, ok := selectedSet[e.ID]; !ok {
			result = append(result, e)
		}
	}
	return result
}

// Name returns the chain identifier.
func (c *StrategyChain) Name() string {
	return "strategy_chain"
}

// Priority returns the highest priority (lowest value) among all strategies.
func (c *StrategyChain) Priority() int {
	if len(c.strategies) == 0 {
		return 100
	}
	return c.strategies[0].Priority()
}

// Strategies returns the list of strategies in the chain.
func (c *StrategyChain) Strategies() []EvictionStrategyWithPriority {
	result := make([]EvictionStrategyWithPriority, len(c.strategies))
	copy(result, c.strategies)
	return result
}

// ConfigBuilder provides a fluent API for building AgentEvictionConfig.
type ConfigBuilder struct {
	config *AgentEvictionConfig
}

// NewConfigBuilder creates a new config builder.
func NewConfigBuilder(agentType string) *ConfigBuilder {
	return &ConfigBuilder{
		config: &AgentEvictionConfig{
			AgentType:       agentType,
			MaxTokens:       128000,
			EvictionTrigger: 0.80,
			Strategies:      make([]EvictionStrategy, 0),
			PreserveTypes:   make([]ContentType, 0),
		},
	}
}

// WithMaxTokens sets the max token capacity.
func (b *ConfigBuilder) WithMaxTokens(tokens int) *ConfigBuilder {
	b.config.MaxTokens = tokens
	return b
}

// WithEvictionTrigger sets the eviction trigger percentage.
func (b *ConfigBuilder) WithEvictionTrigger(trigger float64) *ConfigBuilder {
	b.config.EvictionTrigger = trigger
	return b
}

// WithStrategy adds a strategy to the config.
func (b *ConfigBuilder) WithStrategy(strategy EvictionStrategy) *ConfigBuilder {
	if strategy != nil {
		b.config.Strategies = append(b.config.Strategies, strategy)
	}
	return b
}

// WithPreserveType adds a content type to preserve.
func (b *ConfigBuilder) WithPreserveType(contentType ContentType) *ConfigBuilder {
	b.config.PreserveTypes = append(b.config.PreserveTypes, contentType)
	return b
}

// Build returns the constructed config.
func (b *ConfigBuilder) Build() *AgentEvictionConfig {
	return b.config.Clone()
}

// ValidateConfig checks if a config is valid and returns any issues.
func ValidateConfig(config *AgentEvictionConfig) []string {
	var issues []string

	if config == nil {
		return []string{"config is nil"}
	}

	issues = append(issues, validateAgentType(config)...)
	issues = append(issues, validateTokens(config)...)
	issues = append(issues, validateTrigger(config)...)

	return issues
}

func validateAgentType(config *AgentEvictionConfig) []string {
	if config.AgentType == "" {
		return []string{"agent type is empty"}
	}
	return nil
}

func validateTokens(config *AgentEvictionConfig) []string {
	if config.MaxTokens <= 0 {
		return []string{"max tokens must be positive"}
	}
	return nil
}

func validateTrigger(config *AgentEvictionConfig) []string {
	if config.EvictionTrigger <= 0 || config.EvictionTrigger > 1.0 {
		return []string{"eviction trigger must be between 0 and 1"}
	}
	return nil
}
