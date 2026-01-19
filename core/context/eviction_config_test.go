package context

import (
	"context"
	"testing"
	"time"
)

// mockStrategy implements EvictionStrategyWithPriority for testing.
type mockStrategy struct {
	name     string
	priority int
	entries  []*ContentEntry
}

func newMockStrategy(name string, priority int) *mockStrategy {
	return &mockStrategy{name: name, priority: priority}
}

func (m *mockStrategy) Name() string {
	return m.name
}

func (m *mockStrategy) Priority() int {
	return m.priority
}

func (m *mockStrategy) SelectForEviction(
	_ context.Context,
	entries []*ContentEntry,
	targetTokens int,
) ([]*ContentEntry, error) {
	if len(entries) == 0 || targetTokens <= 0 {
		return nil, nil
	}

	var result []*ContentEntry
	tokens := 0
	for _, e := range entries {
		if tokens >= targetTokens {
			break
		}
		result = append(result, e)
		tokens += e.TokenCount
	}
	return result, nil
}

func TestAgentEvictionConfig_ShouldEvict(t *testing.T) {
	tests := []struct {
		name          string
		maxTokens     int
		trigger       float64
		currentTokens int
		want          bool
	}{
		{
			name:          "below threshold",
			maxTokens:     1000,
			trigger:       0.8,
			currentTokens: 700,
			want:          false,
		},
		{
			name:          "at threshold",
			maxTokens:     1000,
			trigger:       0.8,
			currentTokens: 800,
			want:          true,
		},
		{
			name:          "above threshold",
			maxTokens:     1000,
			trigger:       0.8,
			currentTokens: 900,
			want:          true,
		},
		{
			name:          "zero max tokens",
			maxTokens:     0,
			trigger:       0.8,
			currentTokens: 100,
			want:          false,
		},
		{
			name:          "negative max tokens",
			maxTokens:     -100,
			trigger:       0.8,
			currentTokens: 100,
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &AgentEvictionConfig{
				MaxTokens:       tt.maxTokens,
				EvictionTrigger: tt.trigger,
			}
			got := config.ShouldEvict(tt.currentTokens)
			if got != tt.want {
				t.Errorf("ShouldEvict() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgentEvictionConfig_TokensToFree(t *testing.T) {
	tests := []struct {
		name          string
		maxTokens     int
		trigger       float64
		currentTokens int
		want          int
	}{
		{
			name:          "needs to free tokens",
			maxTokens:     1000,
			trigger:       0.8,
			currentTokens: 900,
			want:          200,
		},
		{
			name:          "no need to free",
			maxTokens:     1000,
			trigger:       0.8,
			currentTokens: 500,
			want:          0,
		},
		{
			name:          "zero current tokens",
			maxTokens:     1000,
			trigger:       0.8,
			currentTokens: 0,
			want:          0,
		},
		{
			name:          "zero max tokens",
			maxTokens:     0,
			trigger:       0.8,
			currentTokens: 100,
			want:          0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &AgentEvictionConfig{
				MaxTokens:       tt.maxTokens,
				EvictionTrigger: tt.trigger,
			}
			got := config.TokensToFree(tt.currentTokens)
			if got != tt.want {
				t.Errorf("TokensToFree() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgentEvictionConfig_IsPreserved(t *testing.T) {
	config := &AgentEvictionConfig{
		PreserveTypes: []ContentType{
			ContentTypeCodeFile,
			ContentTypeUserPrompt,
		},
	}

	tests := []struct {
		name        string
		contentType ContentType
		want        bool
	}{
		{
			name:        "preserved code file",
			contentType: ContentTypeCodeFile,
			want:        true,
		},
		{
			name:        "preserved user prompt",
			contentType: ContentTypeUserPrompt,
			want:        true,
		},
		{
			name:        "not preserved tool result",
			contentType: ContentTypeToolResult,
			want:        false,
		},
		{
			name:        "not preserved assistant reply",
			contentType: ContentTypeAssistantReply,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.IsPreserved(tt.contentType)
			if got != tt.want {
				t.Errorf("IsPreserved() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgentEvictionConfig_Clone(t *testing.T) {
	original := &AgentEvictionConfig{
		AgentType:       "engineer",
		MaxTokens:       128000,
		EvictionTrigger: 0.75,
		Strategies:      []EvictionStrategy{newMockStrategy("test", 1)},
		PreserveTypes:   []ContentType{ContentTypeCodeFile},
	}

	clone := original.Clone()

	if clone.AgentType != original.AgentType {
		t.Errorf("Clone AgentType = %v, want %v", clone.AgentType, original.AgentType)
	}
	if clone.MaxTokens != original.MaxTokens {
		t.Errorf("Clone MaxTokens = %v, want %v", clone.MaxTokens, original.MaxTokens)
	}
	if clone.EvictionTrigger != original.EvictionTrigger {
		t.Errorf("Clone EvictionTrigger = %v, want %v", clone.EvictionTrigger, original.EvictionTrigger)
	}

	// Verify deep copy of slices
	clone.PreserveTypes[0] = ContentTypeToolResult
	if original.PreserveTypes[0] == ContentTypeToolResult {
		t.Error("Clone PreserveTypes not deep copied")
	}
}

func TestAgentEvictionConfig_CloneNil(t *testing.T) {
	var config *AgentEvictionConfig
	clone := config.Clone()
	if clone != nil {
		t.Error("Clone of nil should return nil")
	}
}

func TestDefaultEngineerConfig(t *testing.T) {
	config := DefaultEngineerConfig()

	if config.AgentType != "engineer" {
		t.Errorf("AgentType = %v, want engineer", config.AgentType)
	}
	if config.MaxTokens != 128000 {
		t.Errorf("MaxTokens = %v, want 128000", config.MaxTokens)
	}
	if config.EvictionTrigger != 0.75 {
		t.Errorf("EvictionTrigger = %v, want 0.75", config.EvictionTrigger)
	}
	if !config.IsPreserved(ContentTypeCodeFile) {
		t.Error("CodeFile should be preserved for engineer")
	}
	if !config.IsPreserved(ContentTypeToolResult) {
		t.Error("ToolResult should be preserved for engineer")
	}
}

func TestDefaultArchitectConfig(t *testing.T) {
	config := DefaultArchitectConfig()

	if config.AgentType != "architect" {
		t.Errorf("AgentType = %v, want architect", config.AgentType)
	}
	if config.MaxTokens != 200000 {
		t.Errorf("MaxTokens = %v, want 200000", config.MaxTokens)
	}
	if config.EvictionTrigger != 0.80 {
		t.Errorf("EvictionTrigger = %v, want 0.80", config.EvictionTrigger)
	}
	if !config.IsPreserved(ContentTypePlanWorkflow) {
		t.Error("PlanWorkflow should be preserved for architect")
	}
}

func TestDefaultLibrarianConfig(t *testing.T) {
	config := DefaultLibrarianConfig()

	if config.AgentType != "librarian" {
		t.Errorf("AgentType = %v, want librarian", config.AgentType)
	}
	if config.MaxTokens != 1000000 {
		t.Errorf("MaxTokens = %v, want 1000000", config.MaxTokens)
	}
	if config.EvictionTrigger != 0.85 {
		t.Errorf("EvictionTrigger = %v, want 0.85", config.EvictionTrigger)
	}
	if !config.IsPreserved(ContentTypeResearchPaper) {
		t.Error("ResearchPaper should be preserved for librarian")
	}
}

func TestDefaultGuideConfig(t *testing.T) {
	config := DefaultGuideConfig()

	if config.AgentType != "guide" {
		t.Errorf("AgentType = %v, want guide", config.AgentType)
	}
	if config.MaxTokens != 64000 {
		t.Errorf("MaxTokens = %v, want 64000", config.MaxTokens)
	}
	if config.EvictionTrigger != 0.90 {
		t.Errorf("EvictionTrigger = %v, want 0.90", config.EvictionTrigger)
	}
	if !config.IsPreserved(ContentTypeUserPrompt) {
		t.Error("UserPrompt should be preserved for guide")
	}
	if !config.IsPreserved(ContentTypeAssistantReply) {
		t.Error("AssistantReply should be preserved for guide")
	}
}

func TestDefaultTesterConfig(t *testing.T) {
	config := DefaultTesterConfig()

	if config.AgentType != "tester" {
		t.Errorf("AgentType = %v, want tester", config.AgentType)
	}
	if config.MaxTokens != 128000 {
		t.Errorf("MaxTokens = %v, want 128000", config.MaxTokens)
	}
	if !config.IsPreserved(ContentTypeToolResult) {
		t.Error("ToolResult should be preserved for tester")
	}
}

func TestDefaultInspectorConfig(t *testing.T) {
	config := DefaultInspectorConfig()

	if config.AgentType != "inspector" {
		t.Errorf("AgentType = %v, want inspector", config.AgentType)
	}
	if config.MaxTokens != 128000 {
		t.Errorf("MaxTokens = %v, want 128000", config.MaxTokens)
	}
	if !config.IsPreserved(ContentTypeToolResult) {
		t.Error("ToolResult should be preserved for inspector")
	}
	if !config.IsPreserved(ContentTypeContextRef) {
		t.Error("ContextRef should be preserved for inspector")
	}
}

func TestAgentConfigRegistry_NewRegistry(t *testing.T) {
	registry := NewAgentConfigRegistry()

	if registry == nil {
		t.Fatal("NewAgentConfigRegistry returned nil")
	}

	// Check that defaults are initialized
	agentTypes := registry.ListAgentTypes()
	expectedTypes := []string{"architect", "engineer", "guide", "inspector", "librarian", "tester"}

	if len(agentTypes) != len(expectedTypes) {
		t.Errorf("ListAgentTypes() returned %d types, want %d", len(agentTypes), len(expectedTypes))
	}
}

func TestAgentConfigRegistry_GetConfig(t *testing.T) {
	registry := NewAgentConfigRegistry()

	config := registry.GetConfig("engineer")
	if config == nil {
		t.Fatal("GetConfig returned nil for engineer")
	}
	if config.AgentType != "engineer" {
		t.Errorf("AgentType = %v, want engineer", config.AgentType)
	}
}

func TestAgentConfigRegistry_GetConfigUnknown(t *testing.T) {
	registry := NewAgentConfigRegistry()

	config := registry.GetConfig("unknown_agent")
	if config != nil {
		t.Error("GetConfig should return nil for unknown agent type")
	}
}

func TestAgentConfigRegistry_SetConfig(t *testing.T) {
	registry := NewAgentConfigRegistry()

	customConfig := &AgentEvictionConfig{
		AgentType:       "custom_agent",
		MaxTokens:       50000,
		EvictionTrigger: 0.70,
		PreserveTypes:   []ContentType{ContentTypeUserPrompt},
	}

	registry.SetConfig(customConfig)

	retrieved := registry.GetConfig("custom_agent")
	if retrieved == nil {
		t.Fatal("GetConfig returned nil for custom agent")
	}
	if retrieved.MaxTokens != 50000 {
		t.Errorf("MaxTokens = %v, want 50000", retrieved.MaxTokens)
	}
}

func TestAgentConfigRegistry_SetConfigNil(t *testing.T) {
	registry := NewAgentConfigRegistry()

	// Should not panic
	registry.SetConfig(nil)

	// Verify registry is still functional
	config := registry.GetConfig("engineer")
	if config == nil {
		t.Error("Registry should still return defaults after setting nil")
	}
}

func TestAgentConfigRegistry_ResetToDefault(t *testing.T) {
	registry := NewAgentConfigRegistry()

	// Set custom config
	customConfig := &AgentEvictionConfig{
		AgentType:       "engineer",
		MaxTokens:       50000,
		EvictionTrigger: 0.50,
	}
	registry.SetConfig(customConfig)

	// Reset to default
	registry.ResetToDefault("engineer")

	// Should return default
	config := registry.GetConfig("engineer")
	if config.MaxTokens != 128000 {
		t.Errorf("After reset, MaxTokens = %v, want 128000", config.MaxTokens)
	}
}

func TestAgentConfigRegistry_ListAgentTypes(t *testing.T) {
	registry := NewAgentConfigRegistry()

	// Add custom agent type
	registry.SetConfig(&AgentEvictionConfig{
		AgentType: "custom_agent",
		MaxTokens: 50000,
	})

	types := registry.ListAgentTypes()

	found := false
	for _, t := range types {
		if t == "custom_agent" {
			found = true
			break
		}
	}

	if !found {
		t.Error("ListAgentTypes should include custom agent type")
	}
}

func TestStrategyChain_NewStrategyChain(t *testing.T) {
	s1 := newMockStrategy("high", 1)
	s2 := newMockStrategy("low", 10)
	s3 := newMockStrategy("medium", 5)

	chain := NewStrategyChain(s1, s2, s3)

	strategies := chain.Strategies()
	if len(strategies) != 3 {
		t.Fatalf("Expected 3 strategies, got %d", len(strategies))
	}

	// Should be sorted by priority
	if strategies[0].Name() != "high" {
		t.Error("First strategy should be 'high' (priority 1)")
	}
	if strategies[1].Name() != "medium" {
		t.Error("Second strategy should be 'medium' (priority 5)")
	}
	if strategies[2].Name() != "low" {
		t.Error("Third strategy should be 'low' (priority 10)")
	}
}

func TestStrategyChain_NilStrategies(t *testing.T) {
	chain := NewStrategyChain(nil, newMockStrategy("valid", 1), nil)

	strategies := chain.Strategies()
	if len(strategies) != 1 {
		t.Errorf("Expected 1 strategy, got %d", len(strategies))
	}
}

func TestStrategyChain_SelectForEviction(t *testing.T) {
	now := time.Now()
	entries := []*ContentEntry{
		{ID: "1", TokenCount: 100, Timestamp: now.Add(-3 * time.Hour)},
		{ID: "2", TokenCount: 200, Timestamp: now.Add(-2 * time.Hour)},
		{ID: "3", TokenCount: 150, Timestamp: now.Add(-1 * time.Hour)},
	}

	strategy := newMockStrategy("test", 1)
	chain := NewStrategyChain(strategy)

	selected, err := chain.SelectForEviction(context.Background(), entries, 250)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(selected) != 2 {
		t.Errorf("Expected 2 selected entries, got %d", len(selected))
	}

	tokens := 0
	for _, e := range selected {
		tokens += e.TokenCount
	}
	if tokens < 250 {
		t.Errorf("Expected at least 250 tokens selected, got %d", tokens)
	}
}

func TestStrategyChain_SelectForEvictionEmpty(t *testing.T) {
	chain := NewStrategyChain(newMockStrategy("test", 1))
	ctx := context.Background()

	selected, err := chain.SelectForEviction(ctx, nil, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected != nil {
		t.Error("Expected nil for empty entries")
	}

	selected, err = chain.SelectForEviction(ctx, []*ContentEntry{}, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected != nil {
		t.Error("Expected nil for empty entries slice")
	}
}

func TestStrategyChain_SelectForEvictionZeroTarget(t *testing.T) {
	entries := []*ContentEntry{
		{ID: "1", TokenCount: 100},
	}

	chain := NewStrategyChain(newMockStrategy("test", 1))
	ctx := context.Background()

	selected, err := chain.SelectForEviction(ctx, entries, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected != nil {
		t.Error("Expected nil for zero target tokens")
	}

	selected, err = chain.SelectForEviction(ctx, entries, -100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected != nil {
		t.Error("Expected nil for negative target tokens")
	}
}

func TestStrategyChain_Name(t *testing.T) {
	chain := NewStrategyChain(newMockStrategy("test", 1))
	if chain.Name() != "strategy_chain" {
		t.Errorf("Name() = %v, want strategy_chain", chain.Name())
	}
}

func TestStrategyChain_Priority(t *testing.T) {
	chain := NewStrategyChain(
		newMockStrategy("low", 10),
		newMockStrategy("high", 2),
	)

	if chain.Priority() != 2 {
		t.Errorf("Priority() = %v, want 2", chain.Priority())
	}
}

func TestStrategyChain_PriorityEmpty(t *testing.T) {
	chain := NewStrategyChain()
	if chain.Priority() != 100 {
		t.Errorf("Priority() for empty chain = %v, want 100", chain.Priority())
	}
}

func TestConfigBuilder_Build(t *testing.T) {
	strategy := newMockStrategy("test", 1)

	config := NewConfigBuilder("custom_agent").
		WithMaxTokens(75000).
		WithEvictionTrigger(0.85).
		WithStrategy(strategy).
		WithPreserveType(ContentTypeCodeFile).
		WithPreserveType(ContentTypeUserPrompt).
		Build()

	if config.AgentType != "custom_agent" {
		t.Errorf("AgentType = %v, want custom_agent", config.AgentType)
	}
	if config.MaxTokens != 75000 {
		t.Errorf("MaxTokens = %v, want 75000", config.MaxTokens)
	}
	if config.EvictionTrigger != 0.85 {
		t.Errorf("EvictionTrigger = %v, want 0.85", config.EvictionTrigger)
	}
	if len(config.Strategies) != 1 {
		t.Errorf("Expected 1 strategy, got %d", len(config.Strategies))
	}
	if len(config.PreserveTypes) != 2 {
		t.Errorf("Expected 2 preserve types, got %d", len(config.PreserveTypes))
	}
}

func TestConfigBuilder_Defaults(t *testing.T) {
	config := NewConfigBuilder("test").Build()

	if config.MaxTokens != 128000 {
		t.Errorf("Default MaxTokens = %v, want 128000", config.MaxTokens)
	}
	if config.EvictionTrigger != 0.80 {
		t.Errorf("Default EvictionTrigger = %v, want 0.80", config.EvictionTrigger)
	}
}

func TestConfigBuilder_NilStrategy(t *testing.T) {
	config := NewConfigBuilder("test").
		WithStrategy(nil).
		Build()

	if len(config.Strategies) != 0 {
		t.Error("Nil strategy should not be added")
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name       string
		config     *AgentEvictionConfig
		wantIssues int
	}{
		{
			name:       "nil config",
			config:     nil,
			wantIssues: 1,
		},
		{
			name: "valid config",
			config: &AgentEvictionConfig{
				AgentType:       "test",
				MaxTokens:       100,
				EvictionTrigger: 0.5,
			},
			wantIssues: 0,
		},
		{
			name: "empty agent type",
			config: &AgentEvictionConfig{
				AgentType:       "",
				MaxTokens:       100,
				EvictionTrigger: 0.5,
			},
			wantIssues: 1,
		},
		{
			name: "invalid max tokens",
			config: &AgentEvictionConfig{
				AgentType:       "test",
				MaxTokens:       0,
				EvictionTrigger: 0.5,
			},
			wantIssues: 1,
		},
		{
			name: "invalid trigger low",
			config: &AgentEvictionConfig{
				AgentType:       "test",
				MaxTokens:       100,
				EvictionTrigger: 0,
			},
			wantIssues: 1,
		},
		{
			name: "invalid trigger high",
			config: &AgentEvictionConfig{
				AgentType:       "test",
				MaxTokens:       100,
				EvictionTrigger: 1.5,
			},
			wantIssues: 1,
		},
		{
			name: "multiple issues",
			config: &AgentEvictionConfig{
				AgentType:       "",
				MaxTokens:       -1,
				EvictionTrigger: 2.0,
			},
			wantIssues: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := ValidateConfig(tt.config)
			if len(issues) != tt.wantIssues {
				t.Errorf("ValidateConfig() returned %d issues, want %d: %v", len(issues), tt.wantIssues, issues)
			}
		})
	}
}

func TestAgentConfigRegistry_ConcurrentAccess(t *testing.T) {
	registry := NewAgentConfigRegistry()

	done := make(chan bool, 10)

	// Concurrent reads
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = registry.GetConfig("engineer")
				_ = registry.ListAgentTypes()
			}
			done <- true
		}()
	}

	// Concurrent writes
	for i := 0; i < 5; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				registry.SetConfig(&AgentEvictionConfig{
					AgentType: "test",
					MaxTokens: id * 1000,
				})
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}
