package domain

import "time"

type DomainConfig struct {
	CrossDomainThreshold  float64
	SingleDomainThreshold float64
	CacheMaxSize          int
	CacheTTL              time.Duration
	LexicalKeywords       map[Domain][]string
	EmbeddingThreshold    float64
	LLMFallbackEnabled    bool
	DefaultDomains        []Domain
}

func DefaultDomainConfig() *DomainConfig {
	return &DomainConfig{
		CrossDomainThreshold:  0.65,
		SingleDomainThreshold: 0.75,
		CacheMaxSize:          10000,
		CacheTTL:              5 * time.Minute,
		LexicalKeywords:       DefaultLexicalKeywords(),
		EmbeddingThreshold:    0.70,
		LLMFallbackEnabled:    true,
		DefaultDomains:        []Domain{DomainLibrarian},
	}
}

func DefaultLexicalKeywords() map[Domain][]string {
	return map[Domain][]string{
		DomainLibrarian:    librarianKeywords(),
		DomainAcademic:     academicKeywords(),
		DomainArchivalist:  archivalistKeywords(),
		DomainArchitect:    architectKeywords(),
		DomainEngineer:     engineerKeywords(),
		DomainDesigner:     designerKeywords(),
		DomainInspector:    inspectorKeywords(),
		DomainTester:       testerKeywords(),
		DomainOrchestrator: orchestratorKeywords(),
		DomainGuide:        guideKeywords(),
	}
}

func librarianKeywords() []string {
	return []string{
		"our code", "this repo", "our implementation", "in the codebase",
		"this function", "our pattern", "file", "function", "class",
		"import", "package", "module", "method", "variable", "struct",
		"interface", "type", "constant", "how does our", "what is our",
		"find the", "where is", "show me the", "source code",
	}
}

func academicKeywords() []string {
	return []string{
		"best practice", "documentation", "RFC", "spec", "specification",
		"standard approach", "according to", "research", "paper", "official",
		"recommended", "convention", "pattern", "guideline", "tutorial",
		"how should", "what is the standard", "industry standard",
	}
}

func archivalistKeywords() []string {
	return []string{
		"we decided", "last session", "previously", "why did we",
		"the decision was", "history", "session", "past", "earlier",
		"remember when", "what happened", "before", "ago", "decision",
		"workflow", "outcome", "failure", "success", "tried",
	}
}

func architectKeywords() []string {
	return []string{
		"the plan", "current task", "next step", "DAG", "workflow",
		"architecture", "design", "structure", "organize", "decompose",
		"breakdown", "task", "milestone", "dependency", "coordinate",
	}
}

func engineerKeywords() []string {
	return []string{
		"implement", "code change", "modification", "fix", "bug",
		"feature", "refactor", "optimize", "write", "edit",
	}
}

func designerKeywords() []string {
	return []string{
		"UI", "UX", "component", "style", "layout", "design token",
		"accessibility", "a11y", "responsive", "theme", "color",
		"typography", "spacing", "animation", "visual",
	}
}

func inspectorKeywords() []string {
	return []string{
		"review", "validate", "check", "quality", "issue", "finding",
		"lint", "static analysis", "code review", "inspection",
	}
}

func testerKeywords() []string {
	return []string{
		"test", "coverage", "unit test", "integration test", "e2e",
		"assertion", "mock", "fixture", "test case", "test suite",
	}
}

func orchestratorKeywords() []string {
	return []string{
		"pipeline", "coordinate", "schedule", "parallel", "sequential",
		"workflow state", "task coordination", "agent", "dispatch",
	}
}

func guideKeywords() []string {
	return []string{
		"help", "guide", "route", "conversation", "clarify", "explain",
		"what do you mean", "user intent", "preference",
	}
}

func (c *DomainConfig) Clone() *DomainConfig {
	if c == nil {
		return nil
	}

	clone := &DomainConfig{
		CrossDomainThreshold:  c.CrossDomainThreshold,
		SingleDomainThreshold: c.SingleDomainThreshold,
		CacheMaxSize:          c.CacheMaxSize,
		CacheTTL:              c.CacheTTL,
		EmbeddingThreshold:    c.EmbeddingThreshold,
		LLMFallbackEnabled:    c.LLMFallbackEnabled,
	}

	clone.LexicalKeywords = cloneKeywordsMap(c.LexicalKeywords)
	clone.DefaultDomains = cloneDomainSlice(c.DefaultDomains)

	return clone
}

func cloneKeywordsMap(m map[Domain][]string) map[Domain][]string {
	if m == nil {
		return nil
	}
	clone := make(map[Domain][]string, len(m))
	for k, v := range m {
		clonedSlice := make([]string, len(v))
		copy(clonedSlice, v)
		clone[k] = clonedSlice
	}
	return clone
}

func (c *DomainConfig) SetLexicalKeywords(d Domain, keywords []string) {
	if c.LexicalKeywords == nil {
		c.LexicalKeywords = make(map[Domain][]string)
	}
	c.LexicalKeywords[d] = keywords
}

func (c *DomainConfig) GetLexicalKeywords(d Domain) []string {
	if c.LexicalKeywords == nil {
		return nil
	}
	return c.LexicalKeywords[d]
}

func (c *DomainConfig) Validate() []string {
	var issues []string

	if c.CrossDomainThreshold <= 0 || c.CrossDomainThreshold > 1 {
		issues = append(issues, "CrossDomainThreshold must be between 0 and 1")
	}
	if c.SingleDomainThreshold <= 0 || c.SingleDomainThreshold > 1 {
		issues = append(issues, "SingleDomainThreshold must be between 0 and 1")
	}
	if c.CrossDomainThreshold >= c.SingleDomainThreshold {
		issues = append(issues, "CrossDomainThreshold should be less than SingleDomainThreshold")
	}
	if c.CacheMaxSize <= 0 {
		issues = append(issues, "CacheMaxSize must be positive")
	}
	if c.CacheTTL <= 0 {
		issues = append(issues, "CacheTTL must be positive")
	}
	if c.EmbeddingThreshold <= 0 || c.EmbeddingThreshold > 1 {
		issues = append(issues, "EmbeddingThreshold must be between 0 and 1")
	}

	return issues
}
