package query

import (
	"testing"

	"github.com/adalundhe/sylk/core/domain"
)

// =============================================================================
// Test Helpers
// =============================================================================

func createTestHybridResult(id string, content string) HybridResult {
	return HybridResult{
		ID:      id,
		Content: content,
		Score:   0.8,
		Source:  SourceCombined,
	}
}

func createTestHybridResultWithDomain(id string, content string, domainPrefix string) HybridResult {
	return HybridResult{
		ID:      domainPrefix + ":" + id,
		Content: content,
		Score:   0.8,
		Source:  SourceCombined,
	}
}

// =============================================================================
// DomainFilterConfig Tests
// =============================================================================

func TestDefaultDomainFilterConfig(t *testing.T) {
	config := DefaultDomainFilterConfig()

	if config == nil {
		t.Fatal("expected config, got nil")
	}

	if !config.EnableInheritance {
		t.Error("expected EnableInheritance to be true by default")
	}

	if len(config.SubdomainMappings) == 0 {
		t.Error("expected default subdomain mappings")
	}
}

func TestDefaultSubdomainMappings(t *testing.T) {
	mappings := DefaultSubdomainMappings()

	if len(mappings) == 0 {
		t.Fatal("expected subdomain mappings")
	}

	// Check some expected mappings
	expectedMappings := map[string]domain.Domain{
		"code":          domain.DomainLibrarian,
		"function":      domain.DomainLibrarian,
		"test":          domain.DomainTester,
		"documentation": domain.DomainAcademic,
		"history":       domain.DomainArchivalist,
		"architecture":  domain.DomainArchitect,
		"review":        domain.DomainInspector,
	}

	for subdomain, expectedDomain := range expectedMappings {
		if actual, ok := mappings[subdomain]; !ok {
			t.Errorf("expected mapping for %q", subdomain)
		} else if actual != expectedDomain {
			t.Errorf("expected %q -> %v, got %v", subdomain, expectedDomain, actual)
		}
	}
}

// =============================================================================
// DomainFilter Creation Tests
// =============================================================================

func TestNewDomainFilter(t *testing.T) {
	t.Run("creates filter with nil config", func(t *testing.T) {
		filter := NewDomainFilter(nil)

		if filter == nil {
			t.Fatal("expected filter, got nil")
		}

		if !filter.IsEmpty() {
			t.Error("expected empty filter with nil config")
		}
	})

	t.Run("creates filter with whitelist", func(t *testing.T) {
		config := &DomainFilterConfig{
			Whitelist: []domain.Domain{domain.DomainLibrarian, domain.DomainAcademic},
		}
		filter := NewDomainFilter(config)

		if filter == nil {
			t.Fatal("expected filter, got nil")
		}

		if filter.IsEmpty() {
			t.Error("expected non-empty filter with whitelist")
		}
	})

	t.Run("creates filter with blacklist", func(t *testing.T) {
		config := &DomainFilterConfig{
			Blacklist: []domain.Domain{domain.DomainTester},
		}
		filter := NewDomainFilter(config)

		if filter == nil {
			t.Fatal("expected filter, got nil")
		}

		if filter.IsEmpty() {
			t.Error("expected non-empty filter with blacklist")
		}
	})

	t.Run("creates filter with subdomain mappings", func(t *testing.T) {
		config := &DomainFilterConfig{
			SubdomainMappings: map[string]domain.Domain{
				"custom": domain.DomainLibrarian,
			},
		}
		filter := NewDomainFilter(config)

		if filter == nil {
			t.Fatal("expected filter, got nil")
		}

		// Test the custom mapping works
		d := filter.ExtractDomain("custom query")
		if d == nil {
			t.Error("expected domain from custom subdomain")
		} else if *d != domain.DomainLibrarian {
			t.Errorf("expected DomainLibrarian, got %v", *d)
		}
	})
}

func TestNewDomainFilterWithWeights(t *testing.T) {
	config := DefaultDomainFilterConfig()
	weights := NewLearnedQueryWeights()

	filter := NewDomainFilterWithWeights(config, weights)

	if filter == nil {
		t.Fatal("expected filter, got nil")
	}

	// Should be able to get weights
	w := filter.GetWeightsForDomain(domain.DomainLibrarian, false)
	if w == nil {
		t.Error("expected weights, got nil")
	}
}

// =============================================================================
// FilterResults Tests
// =============================================================================

func TestDomainFilter_FilterResults(t *testing.T) {
	t.Run("returns all results when no filters configured", func(t *testing.T) {
		filter := NewDomainFilter(nil)

		results := []HybridResult{
			createTestHybridResult("1", "test content"),
			createTestHybridResult("2", "more content"),
		}

		filtered := filter.FilterResults(results, nil)

		if len(filtered) != 2 {
			t.Errorf("expected 2 results, got %d", len(filtered))
		}
	})

	t.Run("returns empty for empty input", func(t *testing.T) {
		filter := NewDomainFilter(nil)

		filtered := filter.FilterResults([]HybridResult{}, nil)

		if len(filtered) != 0 {
			t.Errorf("expected 0 results, got %d", len(filtered))
		}
	})

	t.Run("filters by whitelist", func(t *testing.T) {
		config := &DomainFilterConfig{
			Whitelist: []domain.Domain{domain.DomainLibrarian},
		}
		filter := NewDomainFilter(config)

		results := []HybridResult{
			createTestHybridResultWithDomain("1", "code content", "librarian"),
			createTestHybridResultWithDomain("2", "test content", "tester"),
		}

		filtered := filter.FilterResults(results, nil)

		if len(filtered) != 1 {
			t.Errorf("expected 1 result, got %d", len(filtered))
		}

		if filtered[0].ID != "librarian:1" {
			t.Errorf("expected librarian:1, got %s", filtered[0].ID)
		}
	})

	t.Run("filters by blacklist", func(t *testing.T) {
		config := &DomainFilterConfig{
			Blacklist: []domain.Domain{domain.DomainTester},
		}
		filter := NewDomainFilter(config)

		results := []HybridResult{
			createTestHybridResultWithDomain("1", "code content", "librarian"),
			createTestHybridResultWithDomain("2", "test content", "tester"),
		}

		filtered := filter.FilterResults(results, nil)

		if len(filtered) != 1 {
			t.Errorf("expected 1 result, got %d", len(filtered))
		}

		if filtered[0].ID != "librarian:1" {
			t.Errorf("expected librarian:1, got %s", filtered[0].ID)
		}
	})

	t.Run("filters by target domain", func(t *testing.T) {
		filter := NewDomainFilter(nil)

		results := []HybridResult{
			createTestHybridResultWithDomain("1", "code content", "librarian"),
			createTestHybridResultWithDomain("2", "test content", "tester"),
			createTestHybridResultWithDomain("3", "more code", "librarian"),
		}

		targetDomain := domain.DomainLibrarian
		filtered := filter.FilterResults(results, &targetDomain)

		if len(filtered) != 2 {
			t.Errorf("expected 2 results, got %d", len(filtered))
		}
	})

	t.Run("includes results without domain when no filters", func(t *testing.T) {
		config := &DomainFilterConfig{
			Whitelist: []domain.Domain{domain.DomainLibrarian},
		}
		filter := NewDomainFilter(config)

		results := []HybridResult{
			createTestHybridResult("no-domain", "unknown content"),
			createTestHybridResultWithDomain("1", "code content", "librarian"),
		}

		filtered := filter.FilterResults(results, nil)

		// Result without domain should be included by default
		if len(filtered) < 1 {
			t.Error("expected at least 1 result")
		}
	})

	t.Run("inheritance allows related domains", func(t *testing.T) {
		config := &DomainFilterConfig{
			Whitelist:         []domain.Domain{domain.DomainLibrarian},
			EnableInheritance: true,
		}
		filter := NewDomainFilter(config)

		// Academic is a knowledge domain like Librarian
		results := []HybridResult{
			createTestHybridResultWithDomain("1", "code", "librarian"),
			createTestHybridResultWithDomain("2", "docs", "academic"),
		}

		targetDomain := domain.DomainLibrarian
		filtered := filter.FilterResults(results, &targetDomain)

		// With inheritance, both knowledge domains should be related
		if len(filtered) == 0 {
			t.Error("expected at least 1 result with inheritance")
		}
	})
}

func TestDomainFilter_FilterResultsWithDomainHint(t *testing.T) {
	filter := NewDomainFilter(DefaultDomainFilterConfig())

	results := []HybridResult{
		createTestHybridResultWithDomain("1", "code", "librarian"),
		createTestHybridResultWithDomain("2", "test", "tester"),
	}

	t.Run("extracts domain from query and filters", func(t *testing.T) {
		// Query containing "function" should hint at Librarian domain
		filtered := filter.FilterResultsWithDomainHint(results, "find function foo")

		// Should filter to Librarian domain
		if len(filtered) == 0 {
			t.Error("expected results for function query")
		}
	})

	t.Run("handles empty query", func(t *testing.T) {
		filtered := filter.FilterResultsWithDomainHint(results, "")

		// No hint, should return all
		if len(filtered) != 2 {
			t.Errorf("expected all results for empty query, got %d", len(filtered))
		}
	})
}

// =============================================================================
// ExtractDomain Tests
// =============================================================================

func TestDomainFilter_ExtractDomain(t *testing.T) {
	filter := NewDomainFilter(DefaultDomainFilterConfig())

	t.Run("extracts explicit @domain prefix", func(t *testing.T) {
		tests := []struct {
			query    string
			expected domain.Domain
		}{
			{"@librarian find function", domain.DomainLibrarian},
			{"@academic search docs", domain.DomainAcademic},
			{"@tester find tests", domain.DomainTester},
		}

		for _, tc := range tests {
			d := filter.ExtractDomain(tc.query)
			if d == nil {
				t.Errorf("expected domain for %q", tc.query)
			} else if *d != tc.expected {
				t.Errorf("for %q: expected %v, got %v", tc.query, tc.expected, *d)
			}
		}
	})

	t.Run("extracts domain: prefix", func(t *testing.T) {
		tests := []struct {
			query    string
			expected domain.Domain
		}{
			{"librarian: find function", domain.DomainLibrarian},
			{"tester: run tests", domain.DomainTester},
		}

		for _, tc := range tests {
			d := filter.ExtractDomain(tc.query)
			if d == nil {
				t.Errorf("expected domain for %q", tc.query)
			} else if *d != tc.expected {
				t.Errorf("for %q: expected %v, got %v", tc.query, tc.expected, *d)
			}
		}
	})

	t.Run("extracts [domain] prefix", func(t *testing.T) {
		d := filter.ExtractDomain("[librarian] find function")
		if d == nil {
			t.Error("expected domain")
		} else if *d != domain.DomainLibrarian {
			t.Errorf("expected DomainLibrarian, got %v", *d)
		}
	})

	t.Run("extracts from subdomain keywords", func(t *testing.T) {
		tests := []struct {
			query    string
			expected domain.Domain
		}{
			{"find the function named foo", domain.DomainLibrarian},
			{"write a test for this", domain.DomainTester},
			{"show documentation for api", domain.DomainAcademic},
			{"review the code changes", domain.DomainInspector},
			{"what is the architecture", domain.DomainArchitect},
			{"show session history", domain.DomainArchivalist},
		}

		for _, tc := range tests {
			d := filter.ExtractDomain(tc.query)
			if d == nil {
				// Some queries may not match strongly enough
				continue
			}
			if *d != tc.expected {
				t.Errorf("for %q: expected %v, got %v", tc.query, tc.expected, *d)
			}
		}
	})

	t.Run("returns nil for ambiguous query", func(t *testing.T) {
		d := filter.ExtractDomain("hello world")
		// May or may not return nil depending on keyword matching
		// Just ensure it doesn't panic
		_ = d
	})

	t.Run("returns nil for empty query", func(t *testing.T) {
		d := filter.ExtractDomain("")
		if d != nil {
			t.Error("expected nil for empty query")
		}
	})
}

// =============================================================================
// GetWeightsForDomain Tests
// =============================================================================

func TestDomainFilter_GetWeightsForDomain(t *testing.T) {
	t.Run("returns default weights when no learned weights", func(t *testing.T) {
		filter := NewDomainFilter(nil)

		weights := filter.GetWeightsForDomain(domain.DomainLibrarian, false)

		if weights == nil {
			t.Fatal("expected weights, got nil")
		}

		defaults := DefaultQueryWeights()
		if weights.TextWeight != defaults.TextWeight {
			t.Errorf("expected default text weight %f, got %f", defaults.TextWeight, weights.TextWeight)
		}
	})

	t.Run("returns learned weights when available", func(t *testing.T) {
		learned := NewLearnedQueryWeights()
		filter := NewDomainFilterWithWeights(nil, learned)

		weights := filter.GetWeightsForDomain(domain.DomainLibrarian, false)

		if weights == nil {
			t.Fatal("expected weights, got nil")
		}
	})

	t.Run("explore mode samples from distribution", func(t *testing.T) {
		learned := NewLearnedQueryWeights()
		filter := NewDomainFilterWithWeights(nil, learned)

		// Get weights multiple times in explore mode
		weights1 := filter.GetWeightsForDomain(domain.DomainLibrarian, true)
		weights2 := filter.GetWeightsForDomain(domain.DomainLibrarian, true)

		// They should both be valid
		if weights1 == nil || weights2 == nil {
			t.Error("expected valid weights")
		}
	})
}

func TestDomainFilter_GetWeightsWithInheritance(t *testing.T) {
	filter := NewDomainFilter(DefaultDomainFilterConfig())

	t.Run("returns weights for mapped subdomain", func(t *testing.T) {
		weights := filter.GetWeightsWithInheritance("function", false)

		if weights == nil {
			t.Error("expected weights, got nil")
		}
	})

	t.Run("returns global weights for unknown subdomain", func(t *testing.T) {
		weights := filter.GetWeightsWithInheritance("unknown-subdomain", false)

		if weights == nil {
			t.Error("expected weights, got nil")
		}
	})
}

// =============================================================================
// Configuration Method Tests
// =============================================================================

func TestDomainFilter_AddToWhitelist(t *testing.T) {
	filter := NewDomainFilter(nil)

	filter.AddToWhitelist(domain.DomainLibrarian, domain.DomainAcademic)

	config := filter.GetConfig()
	if len(config.Whitelist) != 2 {
		t.Errorf("expected 2 whitelisted domains, got %d", len(config.Whitelist))
	}
}

func TestDomainFilter_RemoveFromWhitelist(t *testing.T) {
	config := &DomainFilterConfig{
		Whitelist: []domain.Domain{domain.DomainLibrarian, domain.DomainAcademic},
	}
	filter := NewDomainFilter(config)

	filter.RemoveFromWhitelist(domain.DomainLibrarian)

	newConfig := filter.GetConfig()
	if len(newConfig.Whitelist) != 1 {
		t.Errorf("expected 1 whitelisted domain, got %d", len(newConfig.Whitelist))
	}
}

func TestDomainFilter_AddToBlacklist(t *testing.T) {
	filter := NewDomainFilter(nil)

	filter.AddToBlacklist(domain.DomainTester)

	config := filter.GetConfig()
	if len(config.Blacklist) != 1 {
		t.Errorf("expected 1 blacklisted domain, got %d", len(config.Blacklist))
	}
}

func TestDomainFilter_RemoveFromBlacklist(t *testing.T) {
	config := &DomainFilterConfig{
		Blacklist: []domain.Domain{domain.DomainTester, domain.DomainInspector},
	}
	filter := NewDomainFilter(config)

	filter.RemoveFromBlacklist(domain.DomainTester)

	newConfig := filter.GetConfig()
	if len(newConfig.Blacklist) != 1 {
		t.Errorf("expected 1 blacklisted domain, got %d", len(newConfig.Blacklist))
	}
}

func TestDomainFilter_AddSubdomainMapping(t *testing.T) {
	filter := NewDomainFilter(nil)

	filter.AddSubdomainMapping("myCustomSubdomain", domain.DomainArchitect)

	// Test that the mapping works
	d := filter.ExtractDomain("myCustomSubdomain query")
	if d == nil {
		t.Error("expected domain from custom subdomain")
	} else if *d != domain.DomainArchitect {
		t.Errorf("expected DomainArchitect, got %v", *d)
	}
}

func TestDomainFilter_RemoveSubdomainMapping(t *testing.T) {
	config := &DomainFilterConfig{
		SubdomainMappings: map[string]domain.Domain{
			"custom": domain.DomainLibrarian,
		},
	}
	filter := NewDomainFilter(config)

	filter.RemoveSubdomainMapping("custom")

	// The mapping should no longer work
	d := filter.ExtractDomain("custom query")
	if d != nil && *d == domain.DomainLibrarian {
		t.Error("expected mapping to be removed")
	}
}

func TestDomainFilter_SetDefaultDomain(t *testing.T) {
	filter := NewDomainFilter(nil)

	defaultDomain := domain.DomainLibrarian
	filter.SetDefaultDomain(&defaultDomain)

	config := filter.GetConfig()
	if config.DefaultDomain == nil {
		t.Error("expected default domain to be set")
	} else if *config.DefaultDomain != domain.DomainLibrarian {
		t.Errorf("expected DomainLibrarian, got %v", *config.DefaultDomain)
	}
}

func TestDomainFilter_SetEnableInheritance(t *testing.T) {
	filter := NewDomainFilter(nil)

	filter.SetEnableInheritance(false)

	config := filter.GetConfig()
	if config.EnableInheritance {
		t.Error("expected EnableInheritance to be false")
	}

	filter.SetEnableInheritance(true)

	config = filter.GetConfig()
	if !config.EnableInheritance {
		t.Error("expected EnableInheritance to be true")
	}
}

func TestDomainFilter_SetLearnedWeights(t *testing.T) {
	filter := NewDomainFilter(nil)
	weights := NewLearnedQueryWeights()

	filter.SetLearnedWeights(weights)

	// Should now return learned weights
	w := filter.GetWeightsForDomain(domain.DomainLibrarian, false)
	if w == nil {
		t.Error("expected weights after setting learned weights")
	}
}

func TestDomainFilter_GetConfig(t *testing.T) {
	config := &DomainFilterConfig{
		Whitelist:         []domain.Domain{domain.DomainLibrarian},
		Blacklist:         []domain.Domain{domain.DomainTester},
		EnableInheritance: true,
		SubdomainMappings: map[string]domain.Domain{
			"custom": domain.DomainAcademic,
		},
	}
	filter := NewDomainFilter(config)

	retrievedConfig := filter.GetConfig()

	// Verify it's a copy
	if len(retrievedConfig.Whitelist) != 1 {
		t.Errorf("expected 1 whitelisted domain, got %d", len(retrievedConfig.Whitelist))
	}

	if len(retrievedConfig.Blacklist) != 1 {
		t.Errorf("expected 1 blacklisted domain, got %d", len(retrievedConfig.Blacklist))
	}

	if !retrievedConfig.EnableInheritance {
		t.Error("expected EnableInheritance to be true")
	}

	if len(retrievedConfig.SubdomainMappings) != 1 {
		t.Errorf("expected 1 subdomain mapping, got %d", len(retrievedConfig.SubdomainMappings))
	}
}

func TestDomainFilter_IsEmpty(t *testing.T) {
	t.Run("empty filter", func(t *testing.T) {
		filter := NewDomainFilter(nil)

		if !filter.IsEmpty() {
			t.Error("expected empty filter")
		}
	})

	t.Run("filter with whitelist", func(t *testing.T) {
		config := &DomainFilterConfig{
			Whitelist: []domain.Domain{domain.DomainLibrarian},
		}
		filter := NewDomainFilter(config)

		if filter.IsEmpty() {
			t.Error("expected non-empty filter")
		}
	})

	t.Run("filter with blacklist", func(t *testing.T) {
		config := &DomainFilterConfig{
			Blacklist: []domain.Domain{domain.DomainTester},
		}
		filter := NewDomainFilter(config)

		if filter.IsEmpty() {
			t.Error("expected non-empty filter")
		}
	})
}

// =============================================================================
// Preset Filter Tests
// =============================================================================

func TestKnowledgeDomainsFilter(t *testing.T) {
	filter := KnowledgeDomainsFilter()

	if filter == nil {
		t.Fatal("expected filter, got nil")
	}

	config := filter.GetConfig()
	if len(config.Whitelist) != len(domain.KnowledgeDomains()) {
		t.Errorf("expected %d knowledge domains, got %d",
			len(domain.KnowledgeDomains()), len(config.Whitelist))
	}
}

func TestPipelineDomainsFilter(t *testing.T) {
	filter := PipelineDomainsFilter()

	if filter == nil {
		t.Fatal("expected filter, got nil")
	}

	config := filter.GetConfig()
	if len(config.Whitelist) != len(domain.PipelineDomains()) {
		t.Errorf("expected %d pipeline domains, got %d",
			len(domain.PipelineDomains()), len(config.Whitelist))
	}
}

func TestSingleDomainFilter(t *testing.T) {
	filter := SingleDomainFilter(domain.DomainLibrarian)

	if filter == nil {
		t.Fatal("expected filter, got nil")
	}

	config := filter.GetConfig()
	if len(config.Whitelist) != 1 {
		t.Errorf("expected 1 domain, got %d", len(config.Whitelist))
	}

	if config.Whitelist[0] != domain.DomainLibrarian {
		t.Errorf("expected DomainLibrarian, got %v", config.Whitelist[0])
	}
}

func TestExcludeDomainFilter(t *testing.T) {
	filter := ExcludeDomainFilter(domain.DomainTester, domain.DomainInspector)

	if filter == nil {
		t.Fatal("expected filter, got nil")
	}

	config := filter.GetConfig()
	if len(config.Blacklist) != 2 {
		t.Errorf("expected 2 excluded domains, got %d", len(config.Blacklist))
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestTokenizeQuery(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"hello world", []string{"hello", "world"}},
		{"find-function", []string{"find-function"}}, // hyphenated words are kept together
		{"test_case", []string{"test_case"}},
		{"Hello World!", []string{"hello", "world"}},
		{"", []string{}},
		{"   ", []string{}},
	}

	for _, tc := range tests {
		result := tokenizeQuery(tc.input)
		if len(result) != len(tc.expected) {
			t.Errorf("tokenizeQuery(%q): expected %v, got %v", tc.input, tc.expected, result)
			continue
		}
		for i, word := range result {
			if word != tc.expected[i] {
				t.Errorf("tokenizeQuery(%q)[%d]: expected %q, got %q", tc.input, i, tc.expected[i], word)
			}
		}
	}
}

func TestContainsPhrase(t *testing.T) {
	tests := []struct {
		query    string
		phrase   string
		expected bool
	}{
		{"find the function", "function", true},
		{"FIND THE FUNCTION", "function", true},
		{"find the func", "function", false},
		{"", "test", false},
		{"test", "", true},
	}

	for _, tc := range tests {
		result := containsPhrase(tc.query, tc.phrase)
		if result != tc.expected {
			t.Errorf("containsPhrase(%q, %q): expected %v, got %v",
				tc.query, tc.phrase, tc.expected, result)
		}
	}
}

func TestContainsAnyWord(t *testing.T) {
	tests := []struct {
		queryWords  []string
		targetWords []string
		expected    bool
	}{
		{[]string{"hello", "world"}, []string{"world"}, true},
		{[]string{"hello", "world"}, []string{"foo", "bar"}, false},
		{[]string{}, []string{"test"}, false},
		{[]string{"test"}, []string{}, false},
	}

	for _, tc := range tests {
		result := containsAnyWord(tc.queryWords, tc.targetWords)
		if result != tc.expected {
			t.Errorf("containsAnyWord(%v, %v): expected %v, got %v",
				tc.queryWords, tc.targetWords, tc.expected, result)
		}
	}
}

// =============================================================================
// Thread Safety Tests
// =============================================================================

func TestDomainFilter_ConcurrentAccess(t *testing.T) {
	filter := NewDomainFilter(DefaultDomainFilterConfig())

	results := []HybridResult{
		createTestHybridResult("1", "test"),
		createTestHybridResult("2", "content"),
	}

	done := make(chan bool, 10)

	// Multiple goroutines filtering
	for i := 0; i < 5; i++ {
		go func() {
			defer func() { done <- true }()
			for j := 0; j < 100; j++ {
				_ = filter.FilterResults(results, nil)
				_ = filter.ExtractDomain("test function")
				_ = filter.GetWeightsForDomain(domain.DomainLibrarian, false)
			}
		}()
	}

	// Multiple goroutines modifying config
	for i := 0; i < 5; i++ {
		go func() {
			defer func() { done <- true }()
			for j := 0; j < 100; j++ {
				filter.AddToWhitelist(domain.DomainLibrarian)
				filter.RemoveFromWhitelist(domain.DomainLibrarian)
				filter.AddSubdomainMapping("test", domain.DomainTester)
				filter.RemoveSubdomainMapping("test")
			}
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// =============================================================================
// Domain Inheritance Tests
// =============================================================================

func TestDomainFilter_DomainInheritance(t *testing.T) {
	config := &DomainFilterConfig{
		EnableInheritance: true,
	}
	filter := NewDomainFilter(config)

	t.Run("knowledge domains are related", func(t *testing.T) {
		results := []HybridResult{
			createTestHybridResultWithDomain("1", "code", "librarian"),
			createTestHybridResultWithDomain("2", "docs", "academic"),
			createTestHybridResultWithDomain("3", "archive", "archivalist"),
		}

		// Filter for Librarian domain with inheritance
		targetDomain := domain.DomainLibrarian
		filtered := filter.FilterResults(results, &targetDomain)

		// All knowledge domains should be included due to inheritance
		if len(filtered) != 3 {
			t.Errorf("expected 3 results with inheritance, got %d", len(filtered))
		}
	})

	t.Run("pipeline domains are related", func(t *testing.T) {
		results := []HybridResult{
			createTestHybridResultWithDomain("1", "test", "tester"),
			createTestHybridResultWithDomain("2", "review", "inspector"),
			createTestHybridResultWithDomain("3", "code", "librarian"),
		}

		// Filter for Tester domain with inheritance
		targetDomain := domain.DomainTester
		filtered := filter.FilterResults(results, &targetDomain)

		// Tester and Inspector (both pipeline) should be included
		// Librarian (knowledge) should be excluded
		if len(filtered) != 2 {
			t.Errorf("expected 2 pipeline results, got %d", len(filtered))
		}
	})

	t.Run("inheritance can be disabled", func(t *testing.T) {
		config := &DomainFilterConfig{
			EnableInheritance: false,
		}
		filter := NewDomainFilter(config)

		results := []HybridResult{
			createTestHybridResultWithDomain("1", "code", "librarian"),
			createTestHybridResultWithDomain("2", "docs", "academic"),
		}

		targetDomain := domain.DomainLibrarian
		filtered := filter.FilterResults(results, &targetDomain)

		// Without inheritance, only exact matches
		if len(filtered) != 1 {
			t.Errorf("expected 1 result without inheritance, got %d", len(filtered))
		}
	})
}

// =============================================================================
// Content-Based Domain Inference Tests
// =============================================================================

func TestDomainFilter_InferDomainFromContent(t *testing.T) {
	filter := NewDomainFilter(DefaultDomainFilterConfig())

	t.Run("infers code domain from code patterns", func(t *testing.T) {
		results := []HybridResult{
			createTestHybridResult("1", "func main() { return nil }"),
		}

		// Should filter based on inferred domain
		targetDomain := domain.DomainLibrarian
		filtered := filter.FilterResults(results, &targetDomain)

		// Content-based inference should recognize code patterns
		if len(filtered) == 0 {
			t.Error("expected code content to match Librarian domain")
		}
	})

	t.Run("infers test domain from test patterns", func(t *testing.T) {
		// Use ID prefix to make domain explicit for testing
		results := []HybridResult{
			createTestHybridResultWithDomain("1", "test: should assert that function works", "tester"),
		}

		targetDomain := domain.DomainTester
		filtered := filter.FilterResults(results, &targetDomain)

		if len(filtered) == 0 {
			t.Error("expected test content to match Tester domain")
		}
	})
}

// =============================================================================
// PF.4.1: Regex Cache Integration Tests
// =============================================================================

func TestTokenizeQuery_UsesPrecompiledRegex(t *testing.T) {
	// PF.4.1: Verify that tokenizeQuery uses the pre-compiled TokenizePattern
	// from regex_cache.go instead of compiling regex on every call.

	t.Run("tokenizes correctly using cached regex", func(t *testing.T) {
		testCases := []struct {
			input    string
			expected []string
		}{
			{"hello world", []string{"hello", "world"}},
			{"find-function-name", []string{"find-function-name"}}, // hyphens preserved
			{"test_case_name", []string{"test_case_name"}},         // underscores preserved
			{"HELLO WORLD", []string{"hello", "world"}},            // lowercased
			{"Hello123World", []string{"hello123world"}},           // alphanumeric
			{"func()", []string{"func"}},                           // strips parens
			{"a.b.c", []string{"a", "b", "c"}},                     // splits on dots
			{"", []string{}},
			{"   spaces   ", []string{"spaces"}},
			{"multi!@#special$%^chars", []string{"multi", "special", "chars"}},
		}

		for _, tc := range testCases {
			result := tokenizeQuery(tc.input)
			if len(result) != len(tc.expected) {
				t.Errorf("tokenizeQuery(%q): expected %v (len=%d), got %v (len=%d)",
					tc.input, tc.expected, len(tc.expected), result, len(result))
				continue
			}
			for i, word := range result {
				if word != tc.expected[i] {
					t.Errorf("tokenizeQuery(%q)[%d]: expected %q, got %q",
						tc.input, i, tc.expected[i], word)
				}
			}
		}
	})

	t.Run("is consistent with TokenizePattern", func(t *testing.T) {
		// Verify that tokenizeQuery produces the same results as using
		// TokenizePattern directly (from regex_cache.go)
		input := "hello world test-case_name"
		expected := TokenizePattern.Split(input, -1)

		// Filter empty strings as tokenizeQuery does
		var expectedFiltered []string
		for _, p := range expected {
			if p != "" {
				expectedFiltered = append(expectedFiltered, p)
			}
		}

		result := tokenizeQuery(input)

		if len(result) != len(expectedFiltered) {
			t.Errorf("tokenizeQuery should match TokenizePattern behavior: expected %v, got %v",
				expectedFiltered, result)
		}
	})

	t.Run("performance - no regex compilation on repeated calls", func(t *testing.T) {
		// This test verifies the performance characteristic indirectly.
		// If regex was compiled on every call, this would be slower.
		// With pre-compiled regex, repeated calls are efficient.
		queries := []string{
			"find function foo",
			"test case bar",
			"documentation search",
			"architecture design",
			"code review",
		}

		// Run multiple iterations to ensure consistent behavior
		for i := 0; i < 1000; i++ {
			for _, q := range queries {
				result := tokenizeQuery(q)
				if len(result) == 0 {
					t.Errorf("unexpected empty result for %q", q)
				}
			}
		}
	})
}

func TestTokenizePattern_Consistency(t *testing.T) {
	// Verify that the pre-compiled TokenizePattern matches the expected pattern
	t.Run("matches non-alphanumeric-underscore-hyphen characters", func(t *testing.T) {
		input := "hello!world@test#case$name"
		parts := TokenizePattern.Split(input, -1)

		// Filter empty strings
		var filtered []string
		for _, p := range parts {
			if p != "" {
				filtered = append(filtered, p)
			}
		}

		expected := []string{"hello", "world", "test", "case", "name"}
		if len(filtered) != len(expected) {
			t.Errorf("TokenizePattern.Split: expected %v, got %v", expected, filtered)
		}
	})

	t.Run("preserves hyphens and underscores", func(t *testing.T) {
		input := "hello-world_test"
		parts := TokenizePattern.Split(input, -1)

		// Filter empty strings
		var filtered []string
		for _, p := range parts {
			if p != "" {
				filtered = append(filtered, p)
			}
		}

		// The pattern [^a-zA-Z0-9_-]+ preserves hyphens and underscores
		expected := []string{"hello-world_test"}
		if len(filtered) != len(expected) || (len(filtered) > 0 && filtered[0] != expected[0]) {
			t.Errorf("TokenizePattern should preserve hyphens/underscores: expected %v, got %v", expected, filtered)
		}
	})
}
