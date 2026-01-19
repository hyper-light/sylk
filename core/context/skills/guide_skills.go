// Package skills provides AR.8.5: Guide Retrieval Skills.
//
// The Guide agent specializes in onboarding and tutorials.
// These skills provide:
// - Onboarding steps and guidance
// - Usage examples retrieval
// - Concept explanations with context
package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/skills"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// GuideDomain is the skill domain for guide skills.
	GuideDomain = "guide"

	// DefaultExamplesLimit is the default max examples to return.
	DefaultExamplesLimit = 5

	// DefaultOnboardingSteps is the default max onboarding steps.
	DefaultOnboardingSteps = 10

	// DefaultExplanationTokens is the default max tokens for explanations.
	DefaultExplanationTokens = 800
)

// =============================================================================
// Interfaces
// =============================================================================

// OnboardingProvider provides onboarding guidance.
type OnboardingProvider interface {
	GetOnboardingSteps(
		ctx context.Context,
		topic string,
		opts *OnboardingOptions,
	) (*OnboardingGuide, error)
}

// ExampleFinder finds usage examples.
type ExampleFinder interface {
	FindExamples(
		ctx context.Context,
		query string,
		opts *ExampleSearchOptions,
	) ([]*UsageExample, error)
}

// ConceptExplainer explains concepts with context.
type ConceptExplainer interface {
	ExplainConcept(
		ctx context.Context,
		concept string,
		opts *ExplanationOptions,
	) (*ConceptExplanation, error)
}

// GuideContentStore is the interface for content store operations.
type GuideContentStore interface {
	Search(query string, filters *ctxpkg.SearchFilters, limit int) ([]*ctxpkg.ContentEntry, error)
}

// GuideSearcher is the interface for tiered search operations.
type GuideSearcher interface {
	SearchWithBudget(ctx context.Context, query string, budget time.Duration) *ctxpkg.TieredSearchResult
}

// =============================================================================
// Types
// =============================================================================

// OnboardingOptions configures onboarding retrieval.
type OnboardingOptions struct {
	MaxSteps       int
	SkillLevel     string
	IncludeLinks   bool
	FocusAreas     []string
}

// OnboardingGuide represents onboarding guidance.
type OnboardingGuide struct {
	Topic       string           `json:"topic"`
	Steps       []*OnboardingStep `json:"steps"`
	Prerequisites []string        `json:"prerequisites,omitempty"`
	EstimatedTime string          `json:"estimated_time,omitempty"`
	NextTopics    []string        `json:"next_topics,omitempty"`
}

// OnboardingStep represents a single onboarding step.
type OnboardingStep struct {
	Number      int      `json:"number"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Commands    []string `json:"commands,omitempty"`
	Links       []string `json:"links,omitempty"`
	Tips        []string `json:"tips,omitempty"`
}

// ExampleSearchOptions configures example search.
type ExampleSearchOptions struct {
	MaxResults  int
	Languages   []string
	ContextType string
	MinQuality  float64
}

// UsageExample represents a code usage example.
type UsageExample struct {
	ExampleID   string   `json:"example_id"`
	Title       string   `json:"title"`
	Code        string   `json:"code"`
	Language    string   `json:"language"`
	Description string   `json:"description"`
	FilePath    string   `json:"file_path,omitempty"`
	Quality     float64  `json:"quality"`
	Tags        []string `json:"tags,omitempty"`
}

// ExplanationOptions configures explanation generation.
type ExplanationOptions struct {
	MaxTokens     int
	DetailLevel   string
	IncludeCode   bool
	RelatedTopics bool
}

// ConceptExplanation represents a concept explanation.
type ConceptExplanation struct {
	Concept       string   `json:"concept"`
	Explanation   string   `json:"explanation"`
	KeyPoints     []string `json:"key_points"`
	CodeExamples  []string `json:"code_examples,omitempty"`
	RelatedTopics []string `json:"related_topics,omitempty"`
	Resources     []string `json:"resources,omitempty"`
	TokenCount    int      `json:"token_count"`
}

// =============================================================================
// Dependencies
// =============================================================================

// GuideDependencies holds dependencies for guide skills.
type GuideDependencies struct {
	OnboardingProvider OnboardingProvider
	ExampleFinder      ExampleFinder
	ConceptExplainer   ConceptExplainer
	ContentStore       GuideContentStore
	Searcher           GuideSearcher
}

// =============================================================================
// Input/Output Types
// =============================================================================

// GetOnboardingInput is input for guide_get_onboarding.
type GetOnboardingInput struct {
	Topic      string   `json:"topic"`
	MaxSteps   int      `json:"max_steps,omitempty"`
	SkillLevel string   `json:"skill_level,omitempty"`
	FocusAreas []string `json:"focus_areas,omitempty"`
}

// GetOnboardingOutput is output for guide_get_onboarding.
type GetOnboardingOutput struct {
	Found bool             `json:"found"`
	Guide *OnboardingGuide `json:"guide,omitempty"`
}

// FindExamplesInput is input for guide_find_examples.
type FindExamplesInput struct {
	Query       string   `json:"query"`
	MaxResults  int      `json:"max_results,omitempty"`
	Languages   []string `json:"languages,omitempty"`
	ContextType string   `json:"context_type,omitempty"`
}

// FindExamplesOutput is output for guide_find_examples.
type FindExamplesOutput struct {
	Examples   []*UsageExample `json:"examples"`
	TotalFound int             `json:"total_found"`
}

// ExplainConceptInput is input for guide_explain_concept.
type ExplainConceptInput struct {
	Concept       string `json:"concept"`
	MaxTokens     int    `json:"max_tokens,omitempty"`
	DetailLevel   string `json:"detail_level,omitempty"`
	IncludeCode   bool   `json:"include_code,omitempty"`
	RelatedTopics bool   `json:"related_topics,omitempty"`
}

// ExplainConceptOutput is output for guide_explain_concept.
type ExplainConceptOutput struct {
	Found       bool                `json:"found"`
	Explanation *ConceptExplanation `json:"explanation,omitempty"`
}

// =============================================================================
// Skill Constructors
// =============================================================================

// NewGetOnboardingSkill creates the guide_get_onboarding skill.
func NewGetOnboardingSkill(deps *GuideDependencies) *skills.Skill {
	return skills.NewSkill("guide_get_onboarding").
		Domain(GuideDomain).
		Description("Get onboarding steps and guidance for a topic").
		Keywords("onboarding", "tutorial", "getting started", "guide").
		Handler(createGetOnboardingHandler(deps)).
		Build()
}

// NewFindExamplesSkill creates the guide_find_examples skill.
func NewFindExamplesSkill(deps *GuideDependencies) *skills.Skill {
	return skills.NewSkill("guide_find_examples").
		Domain(GuideDomain).
		Description("Find usage examples for code patterns or concepts").
		Keywords("example", "usage", "sample", "code").
		Handler(createFindExamplesHandler(deps)).
		Build()
}

// NewExplainConceptSkill creates the guide_explain_concept skill.
func NewExplainConceptSkill(deps *GuideDependencies) *skills.Skill {
	return skills.NewSkill("guide_explain_concept").
		Domain(GuideDomain).
		Description("Explain a concept with context and examples").
		Keywords("explain", "concept", "understand", "learn").
		Handler(createExplainConceptHandler(deps)).
		Build()
}

// =============================================================================
// Handlers
// =============================================================================

func createGetOnboardingHandler(deps *GuideDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in GetOnboardingInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if in.Topic == "" {
			return nil, fmt.Errorf("topic is required")
		}

		maxSteps := in.MaxSteps
		if maxSteps <= 0 {
			maxSteps = DefaultOnboardingSteps
		}

		// Use dedicated provider if available
		if deps.OnboardingProvider != nil {
			return getOnboardingWithProvider(ctx, deps, in, maxSteps)
		}

		// Fall back to building onboarding from content store
		if deps.ContentStore != nil {
			return buildOnboardingFromStore(deps, in, maxSteps)
		}

		// Fall back to tiered searcher
		if deps.Searcher != nil {
			return buildOnboardingFromSearcher(ctx, deps, in, maxSteps)
		}

		return nil, fmt.Errorf("no onboarding provider or content store configured")
	}
}

func getOnboardingWithProvider(
	ctx context.Context,
	deps *GuideDependencies,
	in GetOnboardingInput,
	maxSteps int,
) (*GetOnboardingOutput, error) {
	opts := &OnboardingOptions{
		MaxSteps:   maxSteps,
		SkillLevel: in.SkillLevel,
		FocusAreas: in.FocusAreas,
	}

	guide, err := deps.OnboardingProvider.GetOnboardingSteps(ctx, in.Topic, opts)
	if err != nil {
		return &GetOnboardingOutput{Found: false}, nil
	}

	return &GetOnboardingOutput{
		Found: true,
		Guide: guide,
	}, nil
}

func buildOnboardingFromStore(
	deps *GuideDependencies,
	in GetOnboardingInput,
	maxSteps int,
) (*GetOnboardingOutput, error) {
	query := fmt.Sprintf("%s tutorial guide onboarding", in.Topic)
	filters := &ctxpkg.SearchFilters{
		Keywords: []string{"tutorial", "guide", "onboarding", "getting started"},
	}

	entries, err := deps.ContentStore.Search(query, filters, maxSteps*2)
	if err != nil {
		return nil, fmt.Errorf("content store search failed: %w", err)
	}

	if len(entries) == 0 {
		return &GetOnboardingOutput{Found: false}, nil
	}

	guide := buildGuideFromEntries(entries, in.Topic, maxSteps)
	return &GetOnboardingOutput{
		Found: true,
		Guide: guide,
	}, nil
}

func buildOnboardingFromSearcher(
	ctx context.Context,
	deps *GuideDependencies,
	in GetOnboardingInput,
	maxSteps int,
) (*GetOnboardingOutput, error) {
	query := fmt.Sprintf("%s tutorial guide", in.Topic)
	results := deps.Searcher.SearchWithBudget(ctx, query, ctxpkg.TierWarmBudget)

	if len(results.Results) == 0 {
		return &GetOnboardingOutput{Found: false}, nil
	}

	guide := buildGuideFromEntries(results.Results, in.Topic, maxSteps)
	return &GetOnboardingOutput{
		Found: true,
		Guide: guide,
	}, nil
}

func buildGuideFromEntries(
	entries []*ctxpkg.ContentEntry,
	topic string,
	maxSteps int,
) *OnboardingGuide {
	guide := &OnboardingGuide{
		Topic:         topic,
		Steps:         make([]*OnboardingStep, 0, maxSteps),
		Prerequisites: make([]string, 0),
		NextTopics:    make([]string, 0),
	}

	for i, entry := range entries {
		if i >= maxSteps {
			break
		}

		step := &OnboardingStep{
			Number:      i + 1,
			Title:       extractStepTitle(entry.Content, i+1),
			Description: extractStepDescription(entry.Content),
			Commands:    extractCommands(entry.Content),
			Tips:        extractTips(entry.Content),
		}
		guide.Steps = append(guide.Steps, step)
	}

	return guide
}

func createFindExamplesHandler(deps *GuideDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in FindExamplesInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if in.Query == "" {
			return nil, fmt.Errorf("query is required")
		}

		maxResults := in.MaxResults
		if maxResults <= 0 {
			maxResults = DefaultExamplesLimit
		}

		// Use dedicated finder if available
		if deps.ExampleFinder != nil {
			return findExamplesWithFinder(ctx, deps, in, maxResults)
		}

		// Fall back to content store search
		if deps.ContentStore != nil {
			return findExamplesWithContentStore(deps, in, maxResults)
		}

		// Fall back to tiered searcher
		if deps.Searcher != nil {
			return findExamplesWithSearcher(ctx, deps, in, maxResults)
		}

		return nil, fmt.Errorf("no example finder or content store configured")
	}
}

func findExamplesWithFinder(
	ctx context.Context,
	deps *GuideDependencies,
	in FindExamplesInput,
	maxResults int,
) (*FindExamplesOutput, error) {
	opts := &ExampleSearchOptions{
		MaxResults:  maxResults,
		Languages:   in.Languages,
		ContextType: in.ContextType,
	}

	examples, err := deps.ExampleFinder.FindExamples(ctx, in.Query, opts)
	if err != nil {
		return nil, fmt.Errorf("example search failed: %w", err)
	}

	return &FindExamplesOutput{
		Examples:   examples,
		TotalFound: len(examples),
	}, nil
}

func findExamplesWithContentStore(
	deps *GuideDependencies,
	in FindExamplesInput,
	maxResults int,
) (*FindExamplesOutput, error) {
	query := fmt.Sprintf("%s example usage", in.Query)
	filters := &ctxpkg.SearchFilters{
		ContentTypes: []ctxpkg.ContentType{ctxpkg.ContentTypeCodeFile},
	}

	entries, err := deps.ContentStore.Search(query, filters, maxResults*2)
	if err != nil {
		return nil, fmt.Errorf("content store search failed: %w", err)
	}

	examples := make([]*UsageExample, 0, maxResults)
	for i, entry := range entries {
		if len(examples) >= maxResults {
			break
		}

		example := convertEntryToExample(entry, i)
		if matchesExampleCriteria(example, in) {
			examples = append(examples, example)
		}
	}

	return &FindExamplesOutput{
		Examples:   examples,
		TotalFound: len(examples),
	}, nil
}

func findExamplesWithSearcher(
	ctx context.Context,
	deps *GuideDependencies,
	in FindExamplesInput,
	maxResults int,
) (*FindExamplesOutput, error) {
	query := fmt.Sprintf("%s example usage code", in.Query)
	results := deps.Searcher.SearchWithBudget(ctx, query, ctxpkg.TierWarmBudget)

	examples := make([]*UsageExample, 0, maxResults)
	for i, entry := range results.Results {
		if len(examples) >= maxResults {
			break
		}

		example := convertEntryToExample(entry, i)
		if matchesExampleCriteria(example, in) {
			examples = append(examples, example)
		}
	}

	return &FindExamplesOutput{
		Examples:   examples,
		TotalFound: len(examples),
	}, nil
}

func convertEntryToExample(entry *ctxpkg.ContentEntry, position int) *UsageExample {
	quality := 1.0 - float64(position)*0.1
	if quality < 0.3 {
		quality = 0.3
	}

	language := entry.Metadata["language"]
	if language == "" {
		language = detectLanguageFromContent(entry.Content)
	}

	return &UsageExample{
		ExampleID:   entry.ID,
		Title:       extractExampleTitle(entry.Content),
		Code:        extractCodeBlock(entry.Content),
		Language:    language,
		Description: extractExampleDescription(entry.Content),
		FilePath:    entry.Metadata["file_path"],
		Quality:     quality,
		Tags:        entry.Keywords,
	}
}

func matchesExampleCriteria(example *UsageExample, in FindExamplesInput) bool {
	// Check languages if specified
	if len(in.Languages) > 0 {
		found := false
		for _, lang := range in.Languages {
			if strings.EqualFold(example.Language, lang) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

func createExplainConceptHandler(deps *GuideDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in ExplainConceptInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if in.Concept == "" {
			return nil, fmt.Errorf("concept is required")
		}

		maxTokens := in.MaxTokens
		if maxTokens <= 0 {
			maxTokens = DefaultExplanationTokens
		}

		// Use dedicated explainer if available
		if deps.ConceptExplainer != nil {
			return explainWithExplainer(ctx, deps, in, maxTokens)
		}

		// Fall back to building explanation from content store
		if deps.ContentStore != nil {
			return buildExplanationFromStore(deps, in, maxTokens)
		}

		// Fall back to tiered searcher
		if deps.Searcher != nil {
			return buildExplanationFromSearcher(ctx, deps, in, maxTokens)
		}

		return nil, fmt.Errorf("no concept explainer or content store configured")
	}
}

func explainWithExplainer(
	ctx context.Context,
	deps *GuideDependencies,
	in ExplainConceptInput,
	maxTokens int,
) (*ExplainConceptOutput, error) {
	opts := &ExplanationOptions{
		MaxTokens:     maxTokens,
		DetailLevel:   in.DetailLevel,
		IncludeCode:   in.IncludeCode,
		RelatedTopics: in.RelatedTopics,
	}

	explanation, err := deps.ConceptExplainer.ExplainConcept(ctx, in.Concept, opts)
	if err != nil {
		return &ExplainConceptOutput{Found: false}, nil
	}

	return &ExplainConceptOutput{
		Found:       true,
		Explanation: explanation,
	}, nil
}

func buildExplanationFromStore(
	deps *GuideDependencies,
	in ExplainConceptInput,
	maxTokens int,
) (*ExplainConceptOutput, error) {
	query := fmt.Sprintf("%s explanation definition", in.Concept)
	entries, err := deps.ContentStore.Search(query, nil, 5)
	if err != nil {
		return nil, fmt.Errorf("content store search failed: %w", err)
	}

	if len(entries) == 0 {
		return &ExplainConceptOutput{Found: false}, nil
	}

	explanation := buildExplanationFromEntries(entries, in, maxTokens)
	return &ExplainConceptOutput{
		Found:       true,
		Explanation: explanation,
	}, nil
}

func buildExplanationFromSearcher(
	ctx context.Context,
	deps *GuideDependencies,
	in ExplainConceptInput,
	maxTokens int,
) (*ExplainConceptOutput, error) {
	query := fmt.Sprintf("%s explanation", in.Concept)
	results := deps.Searcher.SearchWithBudget(ctx, query, ctxpkg.TierWarmBudget)

	if len(results.Results) == 0 {
		return &ExplainConceptOutput{Found: false}, nil
	}

	explanation := buildExplanationFromEntries(results.Results, in, maxTokens)
	return &ExplainConceptOutput{
		Found:       true,
		Explanation: explanation,
	}, nil
}

func buildExplanationFromEntries(
	entries []*ctxpkg.ContentEntry,
	in ExplainConceptInput,
	maxTokens int,
) *ConceptExplanation {
	explanation := &ConceptExplanation{
		Concept:       in.Concept,
		KeyPoints:     make([]string, 0),
		CodeExamples:  make([]string, 0),
		RelatedTopics: make([]string, 0),
		Resources:     make([]string, 0),
	}

	var contentBuilder strings.Builder
	for _, entry := range entries {
		contentBuilder.WriteString(entry.Content)
		contentBuilder.WriteString("\n\n")

		// Extract code examples if requested
		if in.IncludeCode {
			if code := extractCodeBlock(entry.Content); code != "" {
				explanation.CodeExamples = append(explanation.CodeExamples, code)
			}
		}

		// Collect related topics
		if in.RelatedTopics {
			for _, keyword := range entry.Keywords {
				if !strings.EqualFold(keyword, in.Concept) {
					explanation.RelatedTopics = append(explanation.RelatedTopics, keyword)
				}
			}
		}
	}

	// Build main explanation
	explanation.Explanation = truncateGuideContent(contentBuilder.String(), maxTokens)
	explanation.KeyPoints = extractKeyPoints(contentBuilder.String())
	explanation.TokenCount = len(explanation.Explanation) / 4

	return explanation
}

// =============================================================================
// Helper Functions
// =============================================================================

func extractStepTitle(content string, stepNum int) string {
	lines := strings.SplitN(content, "\n", 2)
	title := strings.TrimSpace(lines[0])
	if title == "" {
		title = fmt.Sprintf("Step %d", stepNum)
	}
	if len(title) > 100 {
		title = title[:100] + "..."
	}
	return title
}

func extractStepDescription(content string) string {
	lines := strings.SplitN(content, "\n", 3)
	if len(lines) > 1 {
		desc := strings.TrimSpace(lines[1])
		if len(desc) > 300 {
			desc = desc[:300] + "..."
		}
		return desc
	}
	if len(content) > 300 {
		return content[:300] + "..."
	}
	return content
}

func extractCommands(content string) []string {
	commands := make([]string, 0)
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Look for command patterns
		if strings.HasPrefix(trimmed, "$ ") ||
			strings.HasPrefix(trimmed, "> ") ||
			strings.HasPrefix(trimmed, "```") {
			cmd := strings.TrimPrefix(trimmed, "$ ")
			cmd = strings.TrimPrefix(cmd, "> ")
			cmd = strings.TrimPrefix(cmd, "```")
			if cmd != "" && len(cmd) < 200 {
				commands = append(commands, cmd)
			}
		}
		if len(commands) >= 5 {
			break
		}
	}

	return commands
}

func extractTips(content string) []string {
	tips := make([]string, 0)
	lowerContent := strings.ToLower(content)

	// Look for tip indicators
	for _, indicator := range []string{"tip:", "note:", "hint:", "💡"} {
		if idx := strings.Index(lowerContent, indicator); idx >= 0 {
			// Extract tip content
			start := idx + len(indicator)
			end := start + 200
			if end > len(content) {
				end = len(content)
			}

			// Find end of tip (next newline or section)
			remaining := content[start:end]
			if nlIdx := strings.Index(remaining, "\n\n"); nlIdx > 0 {
				remaining = remaining[:nlIdx]
			}

			tip := strings.TrimSpace(remaining)
			if tip != "" {
				tips = append(tips, tip)
			}
		}
		if len(tips) >= 3 {
			break
		}
	}

	return tips
}

func extractExampleTitle(content string) string {
	lines := strings.SplitN(content, "\n", 2)
	title := strings.TrimSpace(lines[0])

	// Remove common prefixes
	for _, prefix := range []string{"//", "#", "/*", "/**"} {
		title = strings.TrimPrefix(title, prefix)
	}
	title = strings.TrimSpace(title)

	if title == "" {
		title = "Code Example"
	}
	if len(title) > 80 {
		title = title[:80] + "..."
	}
	return title
}

func extractCodeBlock(content string) string {
	// Look for code block markers
	if idx := strings.Index(content, "```"); idx >= 0 {
		start := idx + 3
		// Skip language identifier
		if nlIdx := strings.Index(content[start:], "\n"); nlIdx >= 0 {
			start += nlIdx + 1
		}
		if end := strings.Index(content[start:], "```"); end > 0 {
			return strings.TrimSpace(content[start : start+end])
		}
	}

	// Return first code-like block (indented content)
	lines := strings.Split(content, "\n")
	var codeLines []string
	for _, line := range lines {
		if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			codeLines = append(codeLines, line)
		} else if len(codeLines) > 0 {
			break
		}
	}

	if len(codeLines) > 0 {
		return strings.Join(codeLines, "\n")
	}

	return ""
}

func extractExampleDescription(content string) string {
	// Look for description after code block or in comments
	lines := strings.Split(content, "\n")
	var descLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			desc := strings.TrimPrefix(trimmed, "//")
			desc = strings.TrimPrefix(desc, "#")
			desc = strings.TrimSpace(desc)
			if desc != "" && !strings.HasPrefix(desc, "!") {
				descLines = append(descLines, desc)
			}
		}
		if len(descLines) >= 3 {
			break
		}
	}

	return strings.Join(descLines, " ")
}

func detectLanguageFromContent(content string) string {
	// Simple language detection based on keywords
	lowerContent := strings.ToLower(content)

	if strings.Contains(lowerContent, "func ") && strings.Contains(lowerContent, "package ") {
		return "go"
	}
	if strings.Contains(lowerContent, "def ") && strings.Contains(lowerContent, "import ") {
		return "python"
	}
	if strings.Contains(lowerContent, "function ") || strings.Contains(lowerContent, "const ") {
		return "javascript"
	}
	if strings.Contains(lowerContent, "class ") && strings.Contains(lowerContent, "public ") {
		return "java"
	}

	return "unknown"
}

func extractKeyPoints(content string) []string {
	points := make([]string, 0)
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Look for bullet points or numbered items
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") ||
			(len(trimmed) > 2 && trimmed[1] == '.' && trimmed[0] >= '1' && trimmed[0] <= '9') {
			point := strings.TrimLeft(trimmed, "-*0123456789. ")
			if len(point) > 20 && len(point) < 200 {
				points = append(points, point)
			}
		}
		if len(points) >= 5 {
			break
		}
	}

	return points
}

func truncateGuideContent(content string, maxTokens int) string {
	// Rough estimate: 1 token ≈ 4 characters
	maxChars := maxTokens * 4
	if len(content) <= maxChars {
		return content
	}

	// Find a good break point
	breakPoint := maxChars
	for i := maxChars - 1; i > maxChars-100 && i > 0; i-- {
		if content[i] == '.' || content[i] == '\n' {
			breakPoint = i + 1
			break
		}
	}

	return content[:breakPoint] + "..."
}

// =============================================================================
// Registration
// =============================================================================

// RegisterGuideSkills registers all guide skills with the registry.
func RegisterGuideSkills(
	registry *skills.Registry,
	deps *GuideDependencies,
) error {
	skillsToRegister := []*skills.Skill{
		NewGetOnboardingSkill(deps),
		NewFindExamplesSkill(deps),
		NewExplainConceptSkill(deps),
	}

	for _, skill := range skillsToRegister {
		if err := registry.Register(skill); err != nil {
			return fmt.Errorf("failed to register %s: %w", skill.Name, err)
		}
	}

	return nil
}
