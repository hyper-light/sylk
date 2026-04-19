package guide

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// =============================================================================
// Classification Tool Definition
// =============================================================================

// ClassificationToolName is the name of the classification tool
const ClassificationToolName = "classify_query"

// ClassificationToolSchema returns the JSON schema for the classification tool
var ClassificationToolSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"is_retrospective": map[string]any{
			"type":        "boolean",
			"description": "True if query is about PAST actions, observations, or learnings. False if about FUTURE needs, plans, or requirements.",
		},
		"rejection_reason": map[string]any{
			"type":        "string",
			"description": "If not retrospective and target is archivalist, explain why the query cannot be handled",
		},
		"intent": map[string]any{
			"type":        "string",
			"enum":        []string{"recall", "store", "check", "declare", "complete", "find", "search", "locate", "plan", "design", "execute", "help", "status", "chat", "unknown"},
			"description": "The classified intent of the query",
		},
		"domain": map[string]any{
			"type":        "string",
			"enum":        []string{"local", "history", "research", "planning", "system", "compliance", "testing", "general", "unknown"},
			"description": "The domain/category of the query",
		},
		"target_agent": map[string]any{
			"type":        "string",
			"enum":        []string{"librarian", "engineer", "designer", "tester", "inspector", "archivalist", "academic", "orchestrator", "architect", "guide", "unknown"},
			"description": "Which agent should handle this query",
		},
		"entities": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"scope": map[string]any{
					"type":        "string",
					"description": "Area/component being queried (e.g., 'authentication', 'database')",
				},
				"timeframe": map[string]any{
					"type":        "string",
					"description": "Time reference if any (e.g., 'yesterday', 'last week')",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Specific agent ID if mentioned",
				},
				"agent_name": map[string]any{
					"type":        "string",
					"description": "Specific agent name if mentioned",
				},
				"file_paths": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "File paths mentioned in the query",
				},
				"error_type": map[string]any{
					"type":        "string",
					"description": "Type of error if failure-related",
				},
				"error_message": map[string]any{
					"type":        "string",
					"description": "Error message if provided",
				},
				"data": map[string]any{
					"type":        "object",
					"description": "Data payload for store operations",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "Free-form query text for context searches",
				},
			},
		},
		"confidence": map[string]any{
			"type":        "number",
			"minimum":     0,
			"maximum":     1,
			"description": "Classification confidence from 0.0 to 1.0",
		},
		"multi_intent": map[string]any{
			"type":        "boolean",
			"description": "True if the query contains multiple intents",
		},
		"sub_results": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"is_retrospective": map[string]any{"type": "boolean"},
					"intent":           map[string]any{"type": "string"},
					"domain":           map[string]any{"type": "string"},
					"target_agent":     map[string]any{"type": "string"},
					"confidence":       map[string]any{"type": "number"},
				},
			},
			"description": "Sub-results if multi_intent is true",
		},
	},
	"required": []string{"is_retrospective", "intent", "domain", "target_agent", "confidence"},
}

// =============================================================================
// Classifier
// =============================================================================

// ClassifierClient defines the interface for LLM API calls
type ClassifierClient interface {
	New(ctx context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error)
}

// RealClassifierClient (raw anthropic.Client adapter) has been
// removed. It bypassed the provider gateway and emitted no
// accounting events. Callers that need an LLM-backed classifier
// should use LLMClassifier (llm_classifier.go), which uses a
// providers.ProviderAdapter wrapped by the gateway.

// failingClassifierClient is the fallback ClassifierClient used by
// NewWithAPIKey — it returns an auth error on every call so tests
// that construct a Guide without a real provider observe the same
// fast-fail behavior as the prior empty-api-key raw-client path.
type failingClassifierClient struct{}

func (failingClassifierClient) New(ctx context.Context, _ anthropic.MessageNewParams) (*anthropic.Message, error) {
	return nil, fmt.Errorf("guide: classifier unavailable (NewWithAPIKey stub); use NewWithProvider for LLM-backed classification")
}

// Classifier handles LLM-based query classification
type Classifier struct {
	client ClassifierClient
	config RouterConfig

	corrections *correctionMemory
}

// NewClassifierWithClient creates a new classifier with a custom
// ClassifierClient. Used by tests with a rule-based or mock client.
// The raw anthropic.Client construction paths (NewClassifier and
// NewClassifierWithAPIKey) have been removed: they bypassed the
// provider gateway and emitted no accounting events, violating the
// single-dispatch invariant in docs/FIX_ID_AND_TOKENS.md. LLM-backed
// classification now lives exclusively in LLMClassifier (see
// llm_classifier.go) which uses a providers.ProviderAdapter wrapped
// by the gateway.
func NewClassifierWithClient(client ClassifierClient, config RouterConfig) *Classifier {
	return &Classifier{
		client:      client,
		config:      config,
		corrections: newCorrectionMemory(config.MaxCorrections),
	}
}

// Classify classifies a natural language query
func (c *Classifier) Classify(ctx context.Context, input string) (*ClassificationResult, error) {
	// Keep prompt modules stable and minimal for cacheability + token efficiency.
	systemPrompt := BuildClassificationPromptWithRuntime(input, classificationPromptRuntimeFromContext(ctx))

	// Create context with timeout
	if c.config.ClassificationTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.config.ClassificationTimeout)
		defer cancel()
	}

	// Call LLM with tool-use
	resp, err := c.client.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.config.Model),
		MaxTokens: int64(c.config.MaxTokens),
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(input)),
		},
		// Note: Tool use requires proper SDK setup. For now, we'll parse from text response
		// and add tool support when the SDK interface is confirmed
	})
	if err != nil {
		return nil, fmt.Errorf("classification request failed: %w", err)
	}

	// Extract classification from response and apply local correction overrides.
	result, err := c.extractClassificationFromText(resp)
	if err != nil {
		return nil, err
	}
	return c.applyCorrectionOverride(input, result), nil
}

func (c *Classifier) applyCorrectionOverride(input string, result *ClassificationResult) *ClassificationResult {
	if c == nil || c.corrections == nil || result == nil {
		return result
	}
	correction := c.corrections.bestMatch(input)
	if correction == nil {
		return result
	}
	if !matchesWrongClassification(result, correction) {
		return result
	}
	result.Intent = correction.CorrectIntent
	result.Domain = correction.CorrectDomain
	result.TargetAgent = correction.CorrectTarget
	result.Confidence = normalizeConfidence(maxFloat(result.Confidence, 0.95))
	return result
}

func matchesWrongClassification(result *ClassificationResult, correction *CorrectionRecord) bool {
	if result == nil || correction == nil {
		return false
	}
	return result.Intent == correction.WrongIntent &&
		result.Domain == correction.WrongDomain &&
		result.TargetAgent == correction.WrongTarget
}

func maxFloat(left float64, right float64) float64 {
	if left > right {
		return left
	}
	return right
}

// extractClassificationFromText extracts classification from text response
func (c *Classifier) extractClassificationFromText(resp *anthropic.Message) (*ClassificationResult, error) {
	text := extractTextContent(resp)
	if text == "" {
		return nil, fmt.Errorf("no text content in response")
	}
	for _, candidate := range classificationJSONCandidates(text) {
		result, err := c.parseToolUseResult([]byte(candidate))
		if err == nil {
			return result, nil
		}
	}
	return c.parseTextHeuristically(text)
}

func extractTextContent(resp *anthropic.Message) string {
	var text string
	for _, block := range resp.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return text
}

func extractJSONBlock(text string) string {
	start, end := findJSONBounds(text)
	if start == -1 || end == -1 {
		return ""
	}
	return text[start:end]
}

func classificationJSONCandidates(text string) []string {
	candidates := []string{strings.TrimSpace(text)}
	if block := extractJSONBlock(text); block != "" {
		candidates = append(candidates, block)
	}
	if fenced := extractFencedJSONBlock(text); fenced != "" {
		candidates = append(candidates, fenced)
	}
	return uniqueClassificationCandidates(candidates)
}

func extractFencedJSONBlock(text string) string {
	start := strings.Index(text, "```")
	if start == -1 {
		return ""
	}
	rest := text[start+3:]
	if strings.HasPrefix(strings.ToLower(rest), "json") {
		rest = rest[4:]
	}
	end := strings.Index(rest, "```")
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func uniqueClassificationCandidates(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func findJSONBounds(text string) (int, int) {
	start := -1
	braceCount := 0
	for i, r := range text {
		if r == '{' {
			start = setStart(start, i)
			braceCount++
			continue
		}
		if r == '}' {
			braceCount--
			if braceCount == 0 && start != -1 {
				return start, i + 1
			}
		}
	}
	return -1, -1
}

func setStart(current int, candidate int) int {
	if current == -1 {
		return candidate
	}
	return current
}

// parseTextHeuristically attempts to extract classification from unstructured text
func (c *Classifier) parseTextHeuristically(text string) (*ClassificationResult, error) {
	result := c.newHeuristicResult()
	textLower := strings.ToLower(text)

	c.applyHeuristicIntent(textLower, result)
	c.applyHeuristicDomain(textLower, result)
	c.applyHeuristicRetrospective(textLower, result)

	return result, nil
}

func (c *Classifier) newHeuristicResult() *ClassificationResult {
	return &ClassificationResult{
		IsRetrospective: true,
		Intent:          IntentUnknown,
		Domain:          DomainUnknown,
		Confidence:      0.3,
	}
}

func (c *Classifier) applyHeuristicIntent(textLower string, result *ClassificationResult) {
	intent, confidence := c.detectIntent(textLower)
	if intent == IntentUnknown {
		return
	}
	result.Intent = intent
	result.Confidence = confidence
}

func (c *Classifier) detectIntent(textLower string) (Intent, float64) {
	if c.containsAny(textLower, []string{"recall", "retrieve", "query"}) {
		return IntentRecall, 0.6
	}
	if c.containsAny(textLower, []string{"store", "record", "log"}) {
		return IntentStore, 0.6
	}
	if c.containsAny(textLower, []string{"check", "verify"}) {
		return IntentCheck, 0.6
	}
	if c.containsAny(textLower, []string{"chat", "hello", "hi", "hey"}) {
		return IntentChat, 0.6
	}
	return IntentUnknown, 0.0
}

func (c *Classifier) applyHeuristicDomain(textLower string, result *ClassificationResult) {
	if domain := c.detectDomain(textLower); domain != DomainUnknown {
		result.Domain = domain
	}
}

func (c *Classifier) detectDomain(textLower string) Domain {
	if c.containsAny(textLower, []string{"pattern", "failure", "error", "decision", "learning", "lesson", "history"}) {
		return DomainHistory
	}
	if c.containsAny(textLower, []string{"code", "file", "local", "read", "search", "modify"}) {
		return DomainLocal
	}
	if c.containsAny(textLower, []string{"research", "paper", "best practice", "academic"}) {
		return DomainResearch
	}
	if c.containsAny(textLower, []string{"plan", "task", "break down", "workflow"}) {
		return DomainPlanning
	}
	if c.containsAny(textLower, []string{"system", "status", "health", "agent"}) {
		return DomainSystem
	}
	if c.containsAny(textLower, []string{"compliance", "complete", "quality", "review", "lint"}) {
		return DomainCompliance
	}
	if c.containsAny(textLower, []string{"test", "qa", "performance test"}) {
		return DomainTesting
	}
	if c.containsAny(textLower, []string{"chat", "hello", "hi", "hey"}) {
		return DomainGeneral
	}
	return DomainUnknown
}

func (c *Classifier) applyHeuristicRetrospective(textLower string, result *ClassificationResult) {
	if c.containsAny(textLower, []string{"prospective", "future", "should"}) {
		result.IsRetrospective = false
	}
}

func (c *Classifier) containsAny(textLower string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(textLower, needle) {
			return true
		}
	}
	return false
}

// parseToolUseResult parses the JSON input from tool use
func (c *Classifier) parseToolUseResult(inputJSON []byte) (*ClassificationResult, error) {
	var raw classifierRawResult
	if err := json.Unmarshal(inputJSON, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse classification result: %w", err)
	}

	result := c.baseClassificationResult(raw)
	c.applyRawEntities(result, raw)
	c.applyRawSubIntents(result, raw)

	return result, nil
}

type classifierRawResult struct {
	IsRetrospective bool   `json:"is_retrospective"`
	RejectionReason string `json:"rejection_reason"`
	Rejected        bool   `json:"rejected"`
	Reason          string `json:"reason"`
	Intent          string `json:"intent"`
	Domain          string `json:"domain"`
	TargetAgent     string `json:"target_agent"`
	Entities        *struct {
		Scope        string         `json:"scope"`
		Timeframe    string         `json:"timeframe"`
		AgentID      string         `json:"agent_id"`
		AgentName    string         `json:"agent_name"`
		FilePaths    []string       `json:"file_paths"`
		ErrorType    string         `json:"error_type"`
		ErrorMessage string         `json:"error_message"`
		Data         map[string]any `json:"data"`
		Query        string         `json:"query"`
	} `json:"entities"`
	Confidence  float64                `json:"confidence"`
	MultiIntent bool                   `json:"multi_intent"`
	SubIntents  []classifierRawSubItem `json:"sub_intents"`
	SubResults  []classifierRawSubItem `json:"sub_results"`
}

type classifierRawSubItem struct {
	IsRetrospective *bool   `json:"is_retrospective,omitempty"`
	Intent          string  `json:"intent"`
	Domain          string  `json:"domain"`
	TargetAgent     string  `json:"target_agent"`
	Confidence      float64 `json:"confidence"`
}

func (c *Classifier) baseClassificationResult(raw classifierRawResult) *ClassificationResult {
	result := &ClassificationResult{
		IsRetrospective: raw.IsRetrospective,
		RejectionReason: raw.RejectionReason,
		Rejected:        raw.Rejected,
		Reason:          strings.TrimSpace(raw.Reason),
		Intent:          normalizeIntent(raw.Intent),
		Domain:          normalizeDomain(raw.Domain),
		TargetAgent:     normalizeTargetAgent(raw.TargetAgent),
		Confidence:      raw.Confidence,
		MultiIntent:     raw.MultiIntent,
	}
	return normalizeClassificationResult(result)
}

func (c *Classifier) applyRawEntities(result *ClassificationResult, raw classifierRawResult) {
	if raw.Entities == nil {
		return
	}

	result.Entities = &ExtractedEntities{
		Scope:        raw.Entities.Scope,
		Timeframe:    raw.Entities.Timeframe,
		AgentID:      raw.Entities.AgentID,
		AgentName:    raw.Entities.AgentName,
		FilePaths:    raw.Entities.FilePaths,
		ErrorType:    raw.Entities.ErrorType,
		ErrorMessage: raw.Entities.ErrorMessage,
		Data:         raw.Entities.Data,
		Query:        raw.Entities.Query,
	}
}

func (c *Classifier) applyRawSubIntents(result *ClassificationResult, raw classifierRawResult) {
	items := rawSubItems(raw)
	if !raw.MultiIntent || len(items) == 0 {
		return
	}

	result.SubResults = make([]*ClassificationResult, 0, len(items))
	for _, sub := range items {
		result.SubResults = append(result.SubResults, normalizeClassificationResult(&ClassificationResult{
			IsRetrospective: resolveSubRetrospective(raw.IsRetrospective, sub.IsRetrospective),
			Intent:          normalizeIntent(sub.Intent),
			Domain:          normalizeDomain(sub.Domain),
			TargetAgent:     normalizeTargetAgent(sub.TargetAgent),
			Confidence:      sub.Confidence,
		}))
	}
	result.MultiIntent = len(result.SubResults) > 0
}

func rawSubItems(raw classifierRawResult) []classifierRawSubItem {
	if len(raw.SubResults) > 0 {
		return raw.SubResults
	}
	return raw.SubIntents
}

func resolveSubRetrospective(parent bool, value *bool) bool {
	if value == nil {
		return parent
	}
	return *value
}

func normalizeClassificationResult(result *ClassificationResult) *ClassificationResult {
	if result == nil {
		return &ClassificationResult{
			IsRetrospective: true,
			Intent:          IntentUnknown,
			Domain:          DomainUnknown,
			TargetAgent:     TargetUnknown,
			Confidence:      0,
		}
	}
	result.Intent = normalizeIntent(string(result.Intent))
	result.Domain = normalizeDomain(string(result.Domain))
	result.TargetAgent = normalizeTargetAgent(string(result.TargetAgent))
	result.Confidence = normalizeConfidence(result.Confidence)
	result.Reason = strings.TrimSpace(result.Reason)
	if result.MultiIntent && len(result.SubResults) == 0 {
		result.MultiIntent = false
	}
	return result
}

func normalizeIntent(raw string) Intent {
	intent := Intent(strings.ToLower(strings.TrimSpace(raw)))
	if intent == IntentUnknown {
		return intent
	}
	for _, candidate := range AllIntents() {
		if candidate == intent {
			return intent
		}
	}
	return IntentUnknown
}

func normalizeDomain(raw string) Domain {
	domain := Domain(strings.ToLower(strings.TrimSpace(raw)))
	if domain == DomainUnknown {
		return domain
	}
	for _, candidate := range AllDomains() {
		if candidate == domain {
			return domain
		}
	}
	return DomainUnknown
}

func normalizeTargetAgent(raw string) TargetAgent {
	target := TargetAgent(strings.ToLower(strings.TrimSpace(raw)))
	if target == TargetUnknown {
		return target
	}
	for _, candidate := range AllTargetAgents() {
		if candidate == target {
			return target
		}
	}
	return TargetUnknown
}

func normalizeConfidence(value float64) float64 {
	switch {
	case value < 0:
		return 0
	case value > 1:
		return 1
	default:
		return value
	}
}

// AddCorrection adds a correction for learning
func (c *Classifier) AddCorrection(correction CorrectionRecord) {
	if c.corrections == nil {
		return
	}
	c.corrections.add(correction)
}

// formatCorrections formats corrections for few-shot learning
func (c *Classifier) formatCorrections(input string) string {
	if c.corrections == nil {
		return ""
	}
	candidates := c.corrections.selectForPrompt(input, c.maxPromptCorrections())
	return formatCorrectionExamples(candidates)
}

func (c *Classifier) maxPromptCorrections() int {
	if c.config.MaxPromptCorrections > 0 {
		return c.config.MaxPromptCorrections
	}
	return defaultMaxPromptCorrections
}

// =============================================================================
// Classification Result Methods
// =============================================================================

// ToRouteResult converts a classification result to a route result.
// The RouterConfig thresholds control the confidence-to-action mapping.
func (cr *ClassificationResult) ToRouteResult(processingTime time.Duration, cfg RouterConfig) *RouteResult {
	result := &RouteResult{
		Intent:               cr.Intent,
		Domain:               cr.Domain,
		TargetAgent:          cr.TargetAgent,
		Entities:             cr.Entities,
		Confidence:           cr.Confidence,
		Rejected:             cr.Rejected,
		Reason:               strings.TrimSpace(cr.Reason),
		ClassificationMethod: "llm",
		ProcessingTime:       processingTime,
	}

	cr.assignTargetRouting(result)
	result.Action = cr.determineRouteAction(result, cfg)
	cr.applyMultiIntent(result, cfg)

	return result
}

func (cr *ClassificationResult) determineRouteAction(result *RouteResult, cfg RouterConfig) RouteAction {
	if result != nil && result.Rejected {
		return RouteActionReject
	}
	return determineActionFromConfig(cr.Confidence, cfg)
}

func (cr *ClassificationResult) assignTargetRouting(result *RouteResult) {
	// Let the LLM select the target agent if it provided one
	if result.TargetAgent != "" && result.TargetAgent != TargetUnknown {
		if cr.IsRetrospective {
			result.TemporalFocus = TemporalPast
		} else {
			result.TemporalFocus = TemporalPresent
		}
		return
	}

	if cr.Domain.IsHistoricalDomain() && cr.IsRetrospective {
		result.TargetAgent = TargetArchivalist
		result.TemporalFocus = TemporalPast
		return
	}
	if cr.Domain == DomainSystem || cr.Domain == DomainGeneral {
		result.TargetAgent = TargetGuide
		result.TemporalFocus = TemporalPresent
		return
	}
	result.TargetAgent = TargetUnknown
	result.TemporalFocus = TemporalUnknown
}

func (cr *ClassificationResult) applyMultiIntent(result *RouteResult, cfg RouterConfig) {
	if !cr.MultiIntent || len(cr.SubResults) == 0 {
		return
	}
	result.SubResults = make([]*RouteResult, 0, len(cr.SubResults))
	for _, sub := range cr.SubResults {
		result.SubResults = append(result.SubResults, sub.ToRouteResult(0, cfg))
	}
}

// determineActionFromConfig maps confidence to a routing action using the
// thresholds declared in RouterConfig.
func determineActionFromConfig(confidence float64, cfg RouterConfig) RouteAction {
	switch {
	case confidence >= cfg.ExecuteThreshold:
		return RouteActionExecute
	case confidence >= cfg.LogThreshold:
		return RouteActionLog
	case confidence >= cfg.SuggestThreshold:
		return RouteActionSuggest
	default:
		return RouteActionReject
	}
}
