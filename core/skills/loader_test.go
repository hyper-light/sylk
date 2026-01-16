package skills

import (
	"context"
	"encoding/json"
	"testing"
)

func createTestRegistry(names []string, domains map[string]string, keywords map[string][]string) *Registry {
	registry := NewRegistry()
	for _, name := range names {
		builder := NewSkill(name).
			Description("Test skill " + name).
			Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
				return nil, nil
			})

		if domain, ok := domains[name]; ok {
			builder.Domain(domain)
		}
		if kws, ok := keywords[name]; ok {
			builder.Keywords(kws...)
		}

		registry.Register(builder.Build())
	}
	return registry
}

func TestLoader_CoreSkillsLoadedOnCreate(t *testing.T) {
	registry := createTestRegistry(
		[]string{"core1", "core2", "other"},
		nil, nil,
	)

	config := DefaultLoaderConfig()
	config.CoreSkills = []string{"core1", "core2"}

	loader := NewLoader(registry, config)

	if !registry.Get("core1").Loaded {
		t.Error("core1 should be loaded")
	}
	if !registry.Get("core2").Loaded {
		t.Error("core2 should be loaded")
	}
	if registry.Get("other").Loaded {
		t.Error("other should not be loaded")
	}

	_ = loader // Use loader
}

func TestLoader_AutoLoadDomains(t *testing.T) {
	registry := createTestRegistry(
		[]string{"s1", "s2", "s3"},
		map[string]string{"s1": "auto_domain", "s2": "auto_domain", "s3": "other"},
		nil,
	)

	config := DefaultLoaderConfig()
	config.AutoLoadDomains = []string{"auto_domain"}

	NewLoader(registry, config)

	if !registry.Get("s1").Loaded {
		t.Error("s1 should be loaded (auto domain)")
	}
	if !registry.Get("s2").Loaded {
		t.Error("s2 should be loaded (auto domain)")
	}
	if registry.Get("s3").Loaded {
		t.Error("s3 should not be loaded")
	}
}

func TestLoader_LoadForInput_KeywordMatch(t *testing.T) {
	registry := createTestRegistry(
		[]string{"search", "database", "file"},
		nil,
		map[string][]string{
			"search":   {"find", "search", "query"},
			"database": {"db", "sql", "database"},
			"file":     {"file", "read", "write"},
		},
	)

	config := DefaultLoaderConfig()
	loader := NewLoader(registry, config)

	loaded := loader.LoadForInput("I need to find something in the database")

	// Should load search (find) and database
	foundSearch := false
	foundDatabase := false
	for _, name := range loaded {
		if name == "search" {
			foundSearch = true
		}
		if name == "database" {
			foundDatabase = true
		}
	}

	if !foundSearch {
		t.Error("expected search skill to be loaded")
	}
	if !foundDatabase {
		t.Error("expected database skill to be loaded")
	}
	if registry.Get("file").Loaded {
		t.Error("file skill should not be loaded")
	}
}

func TestLoader_LoadForInput_RespectsMaxSkills(t *testing.T) {
	registry := createTestRegistry(
		[]string{"s1", "s2", "s3", "s4", "s5"},
		nil,
		map[string][]string{
			"s1": {"keyword"},
			"s2": {"keyword"},
			"s3": {"keyword"},
			"s4": {"keyword"},
			"s5": {"keyword"},
		},
	)

	config := DefaultLoaderConfig()
	config.MaxLoadedSkills = 3

	loader := NewLoader(registry, config)

	loaded := loader.LoadForInput("keyword keyword keyword")

	if len(loaded) > 3 {
		t.Errorf("expected at most 3 skills loaded, got %d", len(loaded))
	}
}

func TestLoader_LoadForInput_RespectsTokenBudget(t *testing.T) {
	registry := createTestRegistry(
		[]string{"s1", "s2", "s3", "s4", "s5"},
		nil,
		map[string][]string{
			"s1": {"keyword"},
			"s2": {"keyword"},
			"s3": {"keyword"},
			"s4": {"keyword"},
			"s5": {"keyword"},
		},
	)

	config := DefaultLoaderConfig()
	config.TokenBudget = 400            // Only room for 2 skills
	config.EstimatedTokensPerSkill = 200
	config.MaxLoadedSkills = 0 // No limit on count

	loader := NewLoader(registry, config)

	loaded := loader.LoadForInput("keyword")

	if len(loaded) > 2 {
		t.Errorf("expected at most 2 skills (token budget), got %d", len(loaded))
	}
}

func TestLoader_LoadDomain(t *testing.T) {
	registry := createTestRegistry(
		[]string{"s1", "s2", "s3"},
		map[string]string{"s1": "domain_a", "s2": "domain_a", "s3": "domain_b"},
		nil,
	)

	config := DefaultLoaderConfig()
	loader := NewLoader(registry, config)

	count, ok := loader.LoadDomain("domain_a")

	if !ok {
		t.Error("expected LoadDomain to return ok=true")
	}
	if count != 2 {
		t.Errorf("expected 2 skills loaded, got %d", count)
	}
	if !registry.Get("s1").Loaded {
		t.Error("s1 should be loaded")
	}
	if !registry.Get("s2").Loaded {
		t.Error("s2 should be loaded")
	}
	if registry.Get("s3").Loaded {
		t.Error("s3 should not be loaded")
	}
}

func TestLoader_UnloadDomain(t *testing.T) {
	registry := createTestRegistry(
		[]string{"s1", "s2"},
		map[string]string{"s1": "domain_a", "s2": "domain_a"},
		nil,
	)

	config := DefaultLoaderConfig()
	loader := NewLoader(registry, config)

	registry.LoadAll()

	count := loader.UnloadDomain("domain_a")

	if count != 2 {
		t.Errorf("expected 2 unloaded, got %d", count)
	}
	if registry.Get("s1").Loaded {
		t.Error("s1 should be unloaded")
	}
}

func TestLoader_UnloadDomain_PreservesCoreSkills(t *testing.T) {
	registry := createTestRegistry(
		[]string{"core", "other"},
		map[string]string{"core": "domain_a", "other": "domain_a"},
		nil,
	)

	config := DefaultLoaderConfig()
	config.CoreSkills = []string{"core"}
	loader := NewLoader(registry, config)

	registry.LoadAll()

	count := loader.UnloadDomain("domain_a")

	if count != 1 {
		t.Errorf("expected 1 unloaded (core preserved), got %d", count)
	}
	if !registry.Get("core").Loaded {
		t.Error("core skill should remain loaded")
	}
	if registry.Get("other").Loaded {
		t.Error("other skill should be unloaded")
	}
}

func TestLoader_UnloadUnused(t *testing.T) {
	registry := createTestRegistry(
		[]string{"used", "unused1", "unused2"},
		nil, nil,
	)

	config := DefaultLoaderConfig()
	loader := NewLoader(registry, config)

	registry.LoadAll()

	// Invoke one skill
	registry.Invoke(context.Background(), "used", nil)

	count := loader.UnloadUnused()

	if count != 2 {
		t.Errorf("expected 2 unused unloaded, got %d", count)
	}
	if !registry.Get("used").Loaded {
		t.Error("used skill should remain loaded")
	}
}

func TestLoader_OptimizeForBudget(t *testing.T) {
	registry := createTestRegistry(
		[]string{"s1", "s2", "s3", "s4"},
		nil, nil,
	)

	config := DefaultLoaderConfig()
	config.TokenBudget = 400
	config.EstimatedTokensPerSkill = 200
	loader := NewLoader(registry, config)

	registry.LoadAll() // 4 skills = 800 tokens, over budget

	unloaded := loader.OptimizeForBudget()

	if unloaded < 2 {
		t.Errorf("expected at least 2 skills unloaded to fit budget, got %d", unloaded)
	}

	stats := loader.Stats()
	if stats.EstimatedTokens > config.TokenBudget {
		t.Errorf("still over budget: %d > %d", stats.EstimatedTokens, config.TokenBudget)
	}
}

func TestLoader_LoadForContext(t *testing.T) {
	registry := createTestRegistry(
		[]string{"recent", "domain_skill", "keyword_skill", "other"},
		map[string]string{"domain_skill": "active_domain"},
		map[string][]string{"keyword_skill": {"special"}},
	)

	config := DefaultLoaderConfig()
	config.TokenBudget = 1000
	config.EstimatedTokensPerSkill = 200
	loader := NewLoader(registry, config)

	ctx := LoadContext{
		RecentInputs:    []string{"something special here"},
		ActiveDomains:   []string{"active_domain"},
		RecentlyInvoked: []string{"recent"},
		TokenBudget:     0, // Use default
	}

	result := loader.LoadForContext(ctx)

	// Should have loaded recently invoked, domain skill, and keyword skill
	loadedMap := make(map[string]bool)
	for _, name := range result.Loaded {
		loadedMap[name] = true
	}

	if !loadedMap["recent"] {
		t.Error("expected 'recent' to be loaded")
	}
	if !loadedMap["domain_skill"] {
		t.Error("expected 'domain_skill' to be loaded")
	}
	if !loadedMap["keyword_skill"] {
		t.Error("expected 'keyword_skill' to be loaded")
	}
}

func TestLoader_LoadForContext_BudgetOptimization(t *testing.T) {
	registry := createTestRegistry(
		[]string{"important", "less_important1", "less_important2", "less_important3"},
		nil, nil,
	)

	config := DefaultLoaderConfig()
	config.TokenBudget = 400 // Room for 2 skills
	config.EstimatedTokensPerSkill = 200
	loader := NewLoader(registry, config)

	// Load all initially
	registry.LoadAll()

	ctx := LoadContext{
		RecentlyInvoked: []string{"important"},
		TokenBudget:     400,
	}

	result := loader.LoadForContext(ctx)

	// Should have unloaded some skills
	if len(result.Unloaded) < 2 {
		t.Errorf("expected at least 2 unloaded for budget, got %d", len(result.Unloaded))
	}

	// Important should still be loaded
	if !registry.Get("important").Loaded {
		t.Error("important skill should remain loaded")
	}
}

func TestLoader_Stats(t *testing.T) {
	registry := createTestRegistry(
		[]string{"s1", "s2", "s3"},
		nil, nil,
	)

	config := DefaultLoaderConfig()
	config.TokenBudget = 1000
	config.EstimatedTokensPerSkill = 200
	loader := NewLoader(registry, config)

	registry.Load("s1")
	registry.Load("s2")

	stats := loader.Stats()

	if stats.TotalSkills != 3 {
		t.Errorf("expected total 3, got %d", stats.TotalSkills)
	}
	if stats.LoadedSkills != 2 {
		t.Errorf("expected loaded 2, got %d", stats.LoadedSkills)
	}
	if stats.EstimatedTokens != 400 {
		t.Errorf("expected 400 tokens, got %d", stats.EstimatedTokens)
	}
	if stats.TokenBudget != 1000 {
		t.Errorf("expected budget 1000, got %d", stats.TokenBudget)
	}
	if stats.BudgetUsage != 40.0 {
		t.Errorf("expected 40%% usage, got %.1f%%", stats.BudgetUsage)
	}
}

func TestLoader_GetLoadHistory(t *testing.T) {
	registry := createTestRegistry(
		[]string{"s1", "s2"},
		nil,
		map[string][]string{"s1": {"trigger"}},
	)

	config := DefaultLoaderConfig()
	config.CoreSkills = []string{"s2"}
	loader := NewLoader(registry, config)

	loader.LoadForInput("trigger word")

	history := loader.GetLoadHistory()

	if len(history) < 2 {
		t.Errorf("expected at least 2 events, got %d", len(history))
	}

	// Should have core skill load and keyword load
	hasCore := false
	hasKeyword := false
	for _, event := range history {
		if event.Reason == "core_skill" {
			hasCore = true
		}
		if event.Reason == "keyword_match" {
			hasKeyword = true
		}
	}

	if !hasCore {
		t.Error("expected core_skill event")
	}
	if !hasKeyword {
		t.Error("expected keyword_match event")
	}
}

func TestLoader_PriorityBasedLoading(t *testing.T) {
	registry := NewRegistry()

	// Create skills with different priorities
	for i, name := range []string{"low", "medium", "high"} {
		priority := (i + 1) * 10
		skill := NewSkill(name).
			Description("Test").
			Priority(priority).
			Keywords("trigger").
			Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
				return nil, nil
			}).
			Build()
		registry.Register(skill)
	}

	config := DefaultLoaderConfig()
	config.MaxLoadedSkills = 2 // Only room for 2
	loader := NewLoader(registry, config)

	loaded := loader.LoadForInput("trigger")

	// Should load high and medium priority, not low
	loadedMap := make(map[string]bool)
	for _, name := range loaded {
		loadedMap[name] = true
	}

	if !loadedMap["high"] {
		t.Error("expected 'high' priority skill to be loaded")
	}
	if !loadedMap["medium"] {
		t.Error("expected 'medium' priority skill to be loaded")
	}
	if loadedMap["low"] {
		t.Error("'low' priority skill should not be loaded (exceeded max)")
	}
}
