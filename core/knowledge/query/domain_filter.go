// Package query provides HQ.5.2: Domain-Filtered Hybrid Query.
// DomainFilter provides filtering of hybrid query results by domain with
// whitelist/blacklist support and domain inheritance (subdomain -> domain -> global).
package query

import (
	"strings"
	"sync"

	"github.com/adalundhe/sylk/core/domain"
)

// =============================================================================
// Domain Filter Configuration
// =============================================================================

// DomainFilterConfig configures the domain filter behavior.
type DomainFilterConfig struct {
	// Whitelist of allowed domains (empty means allow all)
	Whitelist []domain.Domain `json:"whitelist,omitempty"`

	// Blacklist of excluded domains (applied after whitelist)
	Blacklist []domain.Domain `json:"blacklist,omitempty"`

	// EnableInheritance enables domain inheritance (subdomain -> domain -> global)
	EnableInheritance bool `json:"enable_inheritance"`

	// SubdomainMappings maps subdomain strings to parent domains
	SubdomainMappings map[string]domain.Domain `json:"subdomain_mappings,omitempty"`

	// DefaultDomain is used when no domain can be determined
	DefaultDomain *domain.Domain `json:"default_domain,omitempty"`
}

// DefaultDomainFilterConfig returns a config that allows all domains.
func DefaultDomainFilterConfig() *DomainFilterConfig {
	return &DomainFilterConfig{
		EnableInheritance: true,
		SubdomainMappings: DefaultSubdomainMappings(),
	}
}

// DefaultSubdomainMappings returns the default subdomain to domain mappings.
func DefaultSubdomainMappings() map[string]domain.Domain {
	return map[string]domain.Domain{
		// Code-related subdomains
		"code":        domain.DomainLibrarian,
		"file":        domain.DomainLibrarian,
		"function":    domain.DomainLibrarian,
		"method":      domain.DomainLibrarian,
		"class":       domain.DomainLibrarian,
		"package":     domain.DomainLibrarian,
		"module":      domain.DomainLibrarian,
		"struct":      domain.DomainLibrarian,
		"interface":   domain.DomainLibrarian,
		"type":        domain.DomainLibrarian,
		"variable":    domain.DomainLibrarian,
		"constant":    domain.DomainLibrarian,
		"import":      domain.DomainLibrarian,
		"dependency":  domain.DomainLibrarian,
		"syntax":      domain.DomainLibrarian,
		"implementation": domain.DomainLibrarian,

		// Academic/research subdomains
		"paper":          domain.DomainAcademic,
		"research":       domain.DomainAcademic,
		"documentation":  domain.DomainAcademic,
		"doc":            domain.DomainAcademic,
		"docs":           domain.DomainAcademic,
		"tutorial":       domain.DomainAcademic,
		"guide":          domain.DomainAcademic,
		"reference":      domain.DomainAcademic,
		"api":            domain.DomainAcademic,
		"specification":  domain.DomainAcademic,
		"rfc":            domain.DomainAcademic,
		"standard":       domain.DomainAcademic,
		"best-practice":  domain.DomainAcademic,
		"best_practice":  domain.DomainAcademic,

		// History/context subdomains
		"history":       domain.DomainArchivalist,
		"session":       domain.DomainArchivalist,
		"conversation":  domain.DomainArchivalist,
		"context":       domain.DomainArchivalist,
		"workflow":      domain.DomainArchivalist,
		"decision":      domain.DomainArchivalist,
		"outcome":       domain.DomainArchivalist,
		"change":        domain.DomainArchivalist,
		"modification":  domain.DomainArchivalist,
		"edit":          domain.DomainArchivalist,

		// Architecture subdomains
		"architecture":  domain.DomainArchitect,
		"design":        domain.DomainArchitect,
		"pattern":       domain.DomainArchitect,
		"diagram":       domain.DomainArchitect,
		"system":        domain.DomainArchitect,
		"component":     domain.DomainArchitect,
		"structure":     domain.DomainArchitect,

		// Testing subdomains
		"test":         domain.DomainTester,
		"testing":      domain.DomainTester,
		"unittest":     domain.DomainTester,
		"unit_test":    domain.DomainTester,
		"integration":  domain.DomainTester,
		"e2e":          domain.DomainTester,
		"assertion":    domain.DomainTester,
		"mock":         domain.DomainTester,
		"stub":         domain.DomainTester,
		"fixture":      domain.DomainTester,
		"coverage":     domain.DomainTester,

		// Inspector subdomains
		"review":        domain.DomainInspector,
		"code_review":   domain.DomainInspector,
		"inspection":    domain.DomainInspector,
		"quality":       domain.DomainInspector,
		"lint":          domain.DomainInspector,
		"metric":        domain.DomainInspector,
		"analysis":      domain.DomainInspector,
		"smell":         domain.DomainInspector,
		"issue":         domain.DomainInspector,
		"bug":           domain.DomainInspector,
		"vulnerability": domain.DomainInspector,
		"security":      domain.DomainInspector,
	}
}

// =============================================================================
// Domain Filter
// =============================================================================

// DomainFilter filters hybrid query results by domain.
type DomainFilter struct {
	config           *DomainFilterConfig
	whitelist        map[domain.Domain]bool
	blacklist        map[domain.Domain]bool
	subdomainMapping map[string]domain.Domain
	learnedWeights   *LearnedQueryWeights

	mu sync.RWMutex
}

// NewDomainFilter creates a new DomainFilter with the provided configuration.
func NewDomainFilter(config *DomainFilterConfig) *DomainFilter {
	if config == nil {
		config = DefaultDomainFilterConfig()
	}

	df := &DomainFilter{
		config:           config,
		whitelist:        make(map[domain.Domain]bool),
		blacklist:        make(map[domain.Domain]bool),
		subdomainMapping: make(map[string]domain.Domain),
	}

	// Build whitelist map
	for _, d := range config.Whitelist {
		df.whitelist[d] = true
	}

	// Build blacklist map
	for _, d := range config.Blacklist {
		df.blacklist[d] = true
	}

	// Build subdomain mapping
	if config.SubdomainMappings != nil {
		for k, v := range config.SubdomainMappings {
			df.subdomainMapping[strings.ToLower(k)] = v
		}
	}

	return df
}

// NewDomainFilterWithWeights creates a DomainFilter with learned weights support.
func NewDomainFilterWithWeights(config *DomainFilterConfig, weights *LearnedQueryWeights) *DomainFilter {
	df := NewDomainFilter(config)
	df.learnedWeights = weights
	return df
}

// =============================================================================
// Filtering Methods
// =============================================================================

// FilterResults filters hybrid results by domain according to the configuration.
// Results from blacklisted domains are removed, and if a whitelist is configured,
// only results from whitelisted domains are retained.
func (df *DomainFilter) FilterResults(results []HybridResult, targetDomain *domain.Domain) []HybridResult {
	df.mu.RLock()
	defer df.mu.RUnlock()

	if len(results) == 0 {
		return results
	}

	// If no filters configured and no target domain, return as-is
	if len(df.whitelist) == 0 && len(df.blacklist) == 0 && targetDomain == nil {
		return results
	}

	filtered := make([]HybridResult, 0, len(results))
	for _, result := range results {
		resultDomain := df.extractResultDomain(result)
		if df.shouldInclude(resultDomain, targetDomain) {
			filtered = append(filtered, result)
		}
	}

	return filtered
}

// FilterResultsWithDomainHint filters results using a domain hint extracted from the query.
func (df *DomainFilter) FilterResultsWithDomainHint(results []HybridResult, query string) []HybridResult {
	domainHint := df.ExtractDomain(query)
	return df.FilterResults(results, domainHint)
}

// extractResultDomain extracts the domain from a hybrid result.
// This examines the result's metadata or ID to determine its domain.
func (df *DomainFilter) extractResultDomain(result HybridResult) *domain.Domain {
	// First, try to get domain from result ID prefix (if following convention)
	parts := strings.Split(result.ID, ":")
	if len(parts) >= 2 {
		if d, ok := domain.ParseDomain(parts[0]); ok {
			return &d
		}
	}

	// Try to infer from content
	if result.Content != "" {
		if d := df.inferDomainFromContent(result.Content); d != nil {
			return d
		}
	}

	// Use default if configured
	return df.config.DefaultDomain
}

// inferDomainFromContent attempts to infer domain from content text.
func (df *DomainFilter) inferDomainFromContent(content string) *domain.Domain {
	// Check for code patterns
	codePatterns := []string{
		"func ", "function ", "def ", "class ", "struct ", "interface ",
		"import ", "package ", "module ", "return ", "if ", "for ", "while ",
	}
	for _, pattern := range codePatterns {
		if strings.Contains(content, pattern) {
			d := domain.DomainLibrarian
			return &d
		}
	}

	// Check for test patterns
	testPatterns := []string{
		"test", "assert", "expect", "should", "describe", "it(",
	}
	lower := strings.ToLower(content)
	for _, pattern := range testPatterns {
		if strings.Contains(lower, pattern) {
			d := domain.DomainTester
			return &d
		}
	}

	return nil
}

// shouldInclude determines if a result with the given domain should be included.
func (df *DomainFilter) shouldInclude(resultDomain, targetDomain *domain.Domain) bool {
	// If no domain can be determined, include by default
	if resultDomain == nil {
		return true
	}

	d := *resultDomain

	// Check blacklist first
	if df.blacklist[d] {
		return false
	}

	// If whitelist is configured, check if domain is whitelisted
	if len(df.whitelist) > 0 && !df.whitelist[d] {
		// Check for domain inheritance if enabled
		if df.config.EnableInheritance {
			parent := df.getParentDomain(d)
			if parent != nil && df.whitelist[*parent] {
				return true
			}
		}
		return false
	}

	// If target domain is specified, check match (with inheritance)
	if targetDomain != nil {
		if d == *targetDomain {
			return true
		}

		// Check inheritance
		if df.config.EnableInheritance {
			return df.isRelatedDomain(d, *targetDomain)
		}

		return false
	}

	return true
}

// getParentDomain returns the parent domain for inheritance purposes.
func (df *DomainFilter) getParentDomain(d domain.Domain) *domain.Domain {
	// Knowledge domains inherit from Librarian
	if d.IsKnowledge() && d != domain.DomainLibrarian {
		parent := domain.DomainLibrarian
		return &parent
	}

	// Pipeline domains have no parent (are leaf domains)
	if d.IsPipeline() {
		return nil
	}

	return nil
}

// isRelatedDomain checks if two domains are related through inheritance.
func (df *DomainFilter) isRelatedDomain(d1, d2 domain.Domain) bool {
	// Same category check
	if d1.IsKnowledge() && d2.IsKnowledge() {
		return true
	}
	if d1.IsPipeline() && d2.IsPipeline() {
		return true
	}
	if d1.IsStandalone() && d2.IsStandalone() {
		return true
	}

	return false
}

// =============================================================================
// Domain Extraction
// =============================================================================

// ExtractDomain extracts a domain hint from a query string.
// It looks for domain keywords, patterns, and context clues.
func (df *DomainFilter) ExtractDomain(query string) *domain.Domain {
	df.mu.RLock()
	defer df.mu.RUnlock()

	if query == "" {
		return nil
	}

	query = strings.ToLower(strings.TrimSpace(query))

	// Check for explicit domain prefix (e.g., "code: find function" or "@code find function")
	if d := df.extractExplicitDomain(query); d != nil {
		return d
	}

	// Check subdomain mappings
	if d := df.extractFromSubdomains(query); d != nil {
		return d
	}

	// Check for domain keywords
	if d := df.extractFromKeywords(query); d != nil {
		return d
	}

	return nil
}

// extractExplicitDomain checks for explicit domain prefixes in the query.
func (df *DomainFilter) extractExplicitDomain(query string) *domain.Domain {
	// Check for "@domain" prefix
	if strings.HasPrefix(query, "@") {
		parts := strings.SplitN(query[1:], " ", 2)
		if len(parts) > 0 {
			if d, ok := domain.ParseDomain(parts[0]); ok {
				return &d
			}
		}
	}

	// Check for "domain:" prefix
	colonIdx := strings.Index(query, ":")
	if colonIdx > 0 && colonIdx < 20 {
		prefix := strings.TrimSpace(query[:colonIdx])
		if d, ok := domain.ParseDomain(prefix); ok {
			return &d
		}
	}

	// Check for "[domain]" prefix
	if strings.HasPrefix(query, "[") {
		endIdx := strings.Index(query, "]")
		if endIdx > 1 {
			domainStr := query[1:endIdx]
			if d, ok := domain.ParseDomain(domainStr); ok {
				return &d
			}
		}
	}

	return nil
}

// extractFromSubdomains checks for subdomain keywords in the query.
func (df *DomainFilter) extractFromSubdomains(query string) *domain.Domain {
	words := tokenizeQuery(query)

	// Check each word against subdomain mappings
	for _, word := range words {
		if d, ok := df.subdomainMapping[word]; ok {
			return &d
		}
	}

	return nil
}

// extractFromKeywords checks for domain-indicative keywords.
func (df *DomainFilter) extractFromKeywords(query string) *domain.Domain {
	// Define keyword patterns for each domain
	patterns := map[domain.Domain][]string{
		domain.DomainLibrarian: {
			"function", "method", "class", "struct", "interface", "variable",
			"implement", "code", "file", "package", "module", "syntax",
			"how to", "how do", "write", "create", "define",
		},
		domain.DomainAcademic: {
			"document", "tutorial", "guide", "reference", "api", "spec",
			"what is", "explain", "describe", "definition", "concept",
			"paper", "research", "rfc", "standard",
		},
		domain.DomainArchivalist: {
			"history", "previous", "last", "session", "conversation",
			"earlier", "before", "change", "modification", "edit",
			"when did", "what changed", "who",
		},
		domain.DomainArchitect: {
			"architecture", "design", "pattern", "diagram", "system",
			"component", "structure", "overview", "high-level",
		},
		domain.DomainTester: {
			"test", "testing", "unittest", "coverage", "assert",
			"mock", "stub", "fixture", "e2e", "integration test",
		},
		domain.DomainInspector: {
			"review", "inspection", "quality", "lint", "metric",
			"issue", "bug", "security", "vulnerability", "smell",
		},
	}

	// Score each domain based on keyword matches
	scores := make(map[domain.Domain]int)
	queryWords := tokenizeQuery(query)

	for d, keywords := range patterns {
		for _, keyword := range keywords {
			if containsPhrase(query, keyword) || containsAnyWord(queryWords, strings.Fields(keyword)) {
				scores[d]++
			}
		}
	}

	// Find domain with highest score
	var bestDomain *domain.Domain
	bestScore := 0
	for d, score := range scores {
		if score > bestScore {
			bestScore = score
			domainCopy := d
			bestDomain = &domainCopy
		}
	}

	if bestScore >= 2 {
		return bestDomain
	}

	return nil
}

// =============================================================================
// Domain-Specific Weights
// =============================================================================

// GetWeightsForDomain retrieves domain-specific weights from LearnedQueryWeights.
// If no learned weights are available or no domain-specific weights exist,
// returns default weights.
func (df *DomainFilter) GetWeightsForDomain(d domain.Domain, explore bool) *QueryWeights {
	df.mu.RLock()
	lw := df.learnedWeights
	df.mu.RUnlock()

	if lw == nil {
		return DefaultQueryWeights()
	}

	return lw.GetWeights(d, explore)
}

// GetWeightsWithInheritance retrieves weights using domain inheritance.
// First checks for subdomain weights, then domain weights, then global.
func (df *DomainFilter) GetWeightsWithInheritance(subdomain string, explore bool) *QueryWeights {
	df.mu.RLock()
	lw := df.learnedWeights
	mapping := df.subdomainMapping
	df.mu.RUnlock()

	// Try subdomain mapping first
	if d, ok := mapping[strings.ToLower(subdomain)]; ok {
		if lw != nil && lw.HasDomainWeights(d) {
			return lw.GetWeights(d, explore)
		}
	}

	// Fall back to default
	if lw != nil {
		return lw.GetGlobalWeights()
	}

	return DefaultQueryWeights()
}

// SetLearnedWeights sets the learned weights for domain-specific weight retrieval.
func (df *DomainFilter) SetLearnedWeights(weights *LearnedQueryWeights) {
	df.mu.Lock()
	defer df.mu.Unlock()
	df.learnedWeights = weights
}

// =============================================================================
// Configuration Methods
// =============================================================================

// AddToWhitelist adds domains to the whitelist.
func (df *DomainFilter) AddToWhitelist(domains ...domain.Domain) {
	df.mu.Lock()
	defer df.mu.Unlock()

	for _, d := range domains {
		df.whitelist[d] = true
	}
	df.config.Whitelist = append(df.config.Whitelist, domains...)
}

// RemoveFromWhitelist removes domains from the whitelist.
func (df *DomainFilter) RemoveFromWhitelist(domains ...domain.Domain) {
	df.mu.Lock()
	defer df.mu.Unlock()

	for _, d := range domains {
		delete(df.whitelist, d)
	}

	// Rebuild config whitelist
	df.config.Whitelist = nil
	for d := range df.whitelist {
		df.config.Whitelist = append(df.config.Whitelist, d)
	}
}

// AddToBlacklist adds domains to the blacklist.
func (df *DomainFilter) AddToBlacklist(domains ...domain.Domain) {
	df.mu.Lock()
	defer df.mu.Unlock()

	for _, d := range domains {
		df.blacklist[d] = true
	}
	df.config.Blacklist = append(df.config.Blacklist, domains...)
}

// RemoveFromBlacklist removes domains from the blacklist.
func (df *DomainFilter) RemoveFromBlacklist(domains ...domain.Domain) {
	df.mu.Lock()
	defer df.mu.Unlock()

	for _, d := range domains {
		delete(df.blacklist, d)
	}

	// Rebuild config blacklist
	df.config.Blacklist = nil
	for d := range df.blacklist {
		df.config.Blacklist = append(df.config.Blacklist, d)
	}
}

// AddSubdomainMapping adds a subdomain to domain mapping.
func (df *DomainFilter) AddSubdomainMapping(subdomain string, d domain.Domain) {
	df.mu.Lock()
	defer df.mu.Unlock()

	df.subdomainMapping[strings.ToLower(subdomain)] = d
	if df.config.SubdomainMappings == nil {
		df.config.SubdomainMappings = make(map[string]domain.Domain)
	}
	df.config.SubdomainMappings[subdomain] = d
}

// RemoveSubdomainMapping removes a subdomain mapping.
func (df *DomainFilter) RemoveSubdomainMapping(subdomain string) {
	df.mu.Lock()
	defer df.mu.Unlock()

	delete(df.subdomainMapping, strings.ToLower(subdomain))
	delete(df.config.SubdomainMappings, subdomain)
}

// SetDefaultDomain sets the default domain for results without a domain.
func (df *DomainFilter) SetDefaultDomain(d *domain.Domain) {
	df.mu.Lock()
	defer df.mu.Unlock()

	df.config.DefaultDomain = d
}

// SetEnableInheritance enables or disables domain inheritance.
func (df *DomainFilter) SetEnableInheritance(enabled bool) {
	df.mu.Lock()
	defer df.mu.Unlock()

	df.config.EnableInheritance = enabled
}

// GetConfig returns a copy of the current configuration.
func (df *DomainFilter) GetConfig() *DomainFilterConfig {
	df.mu.RLock()
	defer df.mu.RUnlock()

	// Make a shallow copy
	config := *df.config
	config.Whitelist = make([]domain.Domain, len(df.config.Whitelist))
	copy(config.Whitelist, df.config.Whitelist)
	config.Blacklist = make([]domain.Domain, len(df.config.Blacklist))
	copy(config.Blacklist, df.config.Blacklist)
	if df.config.SubdomainMappings != nil {
		config.SubdomainMappings = make(map[string]domain.Domain)
		for k, v := range df.config.SubdomainMappings {
			config.SubdomainMappings[k] = v
		}
	}

	return &config
}

// IsEmpty returns true if no filters are configured.
func (df *DomainFilter) IsEmpty() bool {
	df.mu.RLock()
	defer df.mu.RUnlock()

	return len(df.whitelist) == 0 && len(df.blacklist) == 0
}

// =============================================================================
// Helper Functions
// =============================================================================

// tokenizeQuery splits a query into lowercase words.
// PF.4.1: Uses pre-compiled regex from RegexCache for performance optimization.
// This replaces the previous implementation that compiled regex on every call.
func tokenizeQuery(query string) []string {
	// Use pre-compiled TokenizePattern from regex_cache.go instead of
	// compiling regexp.MustCompile(`[^a-zA-Z0-9_-]+`) on every call.
	// TokenizePattern is initialized at package load time, providing O(1) access.
	parts := TokenizePattern.Split(strings.ToLower(query), -1)

	// Filter empty strings
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// containsPhrase checks if the query contains the phrase (case-insensitive).
func containsPhrase(query, phrase string) bool {
	return strings.Contains(strings.ToLower(query), strings.ToLower(phrase))
}

// containsAnyWord checks if any of the words appear in the query words.
func containsAnyWord(queryWords, targetWords []string) bool {
	for _, target := range targetWords {
		for _, word := range queryWords {
			if word == target {
				return true
			}
		}
	}
	return false
}

// =============================================================================
// Preset Filters
// =============================================================================

// KnowledgeDomainsFilter returns a filter that only allows knowledge domains.
func KnowledgeDomainsFilter() *DomainFilter {
	return NewDomainFilter(&DomainFilterConfig{
		Whitelist:         domain.KnowledgeDomains(),
		EnableInheritance: true,
		SubdomainMappings: DefaultSubdomainMappings(),
	})
}

// PipelineDomainsFilter returns a filter that only allows pipeline domains.
func PipelineDomainsFilter() *DomainFilter {
	return NewDomainFilter(&DomainFilterConfig{
		Whitelist:         domain.PipelineDomains(),
		EnableInheritance: false,
		SubdomainMappings: DefaultSubdomainMappings(),
	})
}

// SingleDomainFilter returns a filter that only allows a single domain.
func SingleDomainFilter(d domain.Domain) *DomainFilter {
	return NewDomainFilter(&DomainFilterConfig{
		Whitelist:         []domain.Domain{d},
		EnableInheritance: false,
		SubdomainMappings: DefaultSubdomainMappings(),
	})
}

// ExcludeDomainFilter returns a filter that excludes specific domains.
func ExcludeDomainFilter(excluded ...domain.Domain) *DomainFilter {
	return NewDomainFilter(&DomainFilterConfig{
		Blacklist:         excluded,
		EnableInheritance: true,
		SubdomainMappings: DefaultSubdomainMappings(),
	})
}
