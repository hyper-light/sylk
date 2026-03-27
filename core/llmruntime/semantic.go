package llmruntime

import (
	"math"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/providers"
)

const (
	metadataStageKey             = "llm_stage"
	metadataRuntimeProfileKey    = "llm_runtime_profile"
	metadataDeliberationKey      = "llm_deliberation"
	metadataResponseDetailKey    = "llm_response_detail"
	metadataToolParallelismKey   = "llm_tool_parallelism"
	metadataCacheAffinityKey     = "llm_cache_affinity"
	metadataThoughtVisibilityKey = "llm_thought_visibility"
	metadataEmitThoughtsKey      = "llm_emit_thoughts"
	metadataAgentIDKey           = "agent_id"
	metadataSessionIDKey         = "session_id"
	anthropicMinThinkingBudget   = 1024
)

type ProviderFamily string

const (
	ProviderUnknown   ProviderFamily = ""
	ProviderOpenAI    ProviderFamily = "openai"
	ProviderAnthropic ProviderFamily = "anthropic"
	ProviderGoogle    ProviderFamily = "google"
)

func ProviderFamilyForModel(model string) ProviderFamily {
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(model, "claude"):
		return ProviderAnthropic
	case strings.HasPrefix(model, "gemini"):
		return ProviderGoogle
	case strings.HasPrefix(model, "gpt-"), strings.HasPrefix(model, "o"):
		return ProviderOpenAI
	default:
		return ProviderUnknown
	}
}

type Deliberation string

const (
	DeliberationDefault Deliberation = ""
	DeliberationLow     Deliberation = "low"
	DeliberationMedium  Deliberation = "medium"
	DeliberationHigh    Deliberation = "high"
	DeliberationMax     Deliberation = "max"
)

type ResponseDetail string

const (
	ResponseDetailDefault  ResponseDetail = ""
	ResponseDetailTerse    ResponseDetail = "terse"
	ResponseDetailNormal   ResponseDetail = "normal"
	ResponseDetailDetailed ResponseDetail = "detailed"
)

type ToolParallelism string

const (
	ToolParallelismDefault  ToolParallelism = ""
	ToolParallelismAuto     ToolParallelism = "auto"
	ToolParallelismParallel ToolParallelism = "parallel"
	ToolParallelismSerial   ToolParallelism = "serial"
)

type CacheAffinity string

const (
	CacheAffinityDefault CacheAffinity = ""
	CacheAffinityNone    CacheAffinity = "none"
	CacheAffinitySession CacheAffinity = "session"
	CacheAffinitySticky  CacheAffinity = "sticky"
)

type ThoughtVisibility string

const (
	ThoughtVisibilityDefault ThoughtVisibility = ""
	ThoughtVisibilityHidden  ThoughtVisibility = "hidden"
	ThoughtVisibilitySummary ThoughtVisibility = "summary"
	ThoughtVisibilityFull    ThoughtVisibility = "full"
)

// StageProfile captures provider-neutral runtime behavior for a specific
// agent stage. The RequestProfile field carries explicit provider-level
// overrides when the semantic settings are insufficient.
type StageProfile struct {
	Name              string            `json:"name"`
	Description       string            `json:"description,omitempty"`
	Intents           []string          `json:"intents,omitempty"`
	Domains           []string          `json:"domains,omitempty"`
	Deliberation      Deliberation      `json:"deliberation,omitempty"`
	ResponseDetail    ResponseDetail    `json:"response_detail,omitempty"`
	ToolParallelism   ToolParallelism   `json:"tool_parallelism,omitempty"`
	CacheAffinity     CacheAffinity     `json:"cache_affinity,omitempty"`
	ThoughtVisibility ThoughtVisibility `json:"thought_visibility,omitempty"`
	RequestProfile    Profile           `json:"request_profile,omitempty"`
}

type ApplyOptions struct {
	Model     string
	MaxTokens int
	AgentID   string
	SessionID string
}

func (o ApplyOptions) providerFamily() ProviderFamily {
	return ProviderFamilyForModel(o.Model)
}

// ApplyStage compiles a semantic stage profile into provider request fields,
// applies the result, and stamps lightweight metadata for downstream systems.
func ApplyStage(req *providers.Request, stage StageProfile, opts ApplyOptions) {
	if req == nil {
		return
	}
	Apply(req, CompileStage(stage, opts))
	applyPromptDirectives(req, stage, opts.providerFamily())
	stampStageMetadata(req, stage, opts)
}

// CompileStage resolves a semantic stage profile into provider request fields.
func CompileStage(stage StageProfile, opts ApplyOptions) Profile {
	profile := stage.RequestProfile
	provider := opts.providerFamily()

	if strings.TrimSpace(profile.ReasoningEffort) == "" {
		profile.ReasoningEffort = reasoningEffortFor(provider, stage.Deliberation)
	}
	if profile.ThinkingBudget == nil {
		if budget, ok := thinkingBudgetFor(provider, stage, opts.MaxTokens); ok {
			profile.ThinkingBudget = Int(budget)
		}
	}
	if strings.TrimSpace(profile.Verbosity) == "" && provider == ProviderOpenAI {
		profile.Verbosity = verbosityFor(stage.ResponseDetail)
	}
	if strings.TrimSpace(profile.ReasoningSummary) == "" && provider == ProviderOpenAI {
		profile.ReasoningSummary = reasoningSummaryFor(stage.ThoughtVisibility)
	}
	if profile.IncludeThoughts == nil {
		if include, ok := includeThoughtsFor(provider, stage.ThoughtVisibility); ok {
			profile.IncludeThoughts = Bool(include)
		}
	}
	applyToolParallelism(&profile, provider, stage.ToolParallelism)
	applyCacheAffinity(&profile, stage.CacheAffinity, opts)
	return profile
}

func applyPromptDirectives(req *providers.Request, stage StageProfile, provider ProviderFamily) {
	if req == nil {
		return
	}
	directives := make([]string, 0, 2)
	if provider != ProviderOpenAI {
		if detail := responseDetailDirective(stage.ResponseDetail); detail != "" {
			directives = append(directives, detail)
		}
	}
	if provider == ProviderGoogle {
		if directive := toolParallelismDirective(stage.ToolParallelism); directive != "" {
			directives = append(directives, directive)
		}
	}
	if len(directives) == 0 {
		return
	}
	block := strings.Join(directives, "\n")
	if strings.TrimSpace(req.SystemPrompt) == "" {
		req.SystemPrompt = block
		return
	}
	req.SystemPrompt = strings.TrimSpace(req.SystemPrompt) + "\n\n" + block
}

func responseDetailDirective(detail ResponseDetail) string {
	switch detail {
	case ResponseDetailTerse:
		return "Response style: keep the final answer concise and direct. Include only the detail needed to complete the task."
	case ResponseDetailDetailed:
		return "Response style: provide a fuller explanation with concrete reasoning, tradeoffs, and the important implementation details."
	default:
		return ""
	}
}

func toolParallelismDirective(mode ToolParallelism) string {
	switch mode {
	case ToolParallelismSerial:
		return "Tool execution policy: call at most one tool at a time unless the task explicitly requires otherwise."
	default:
		return ""
	}
}

func reasoningEffortFor(provider ProviderFamily, deliberation Deliberation) string {
	switch deliberation {
	case DeliberationLow:
		return "low"
	case DeliberationMedium:
		return "medium"
	case DeliberationHigh:
		return "high"
	case DeliberationMax:
		if provider == ProviderOpenAI {
			return "xhigh"
		}
		return "high"
	default:
		return ""
	}
}

func thinkingBudgetFor(provider ProviderFamily, stage StageProfile, maxTokens int) (int, bool) {
	if provider != ProviderAnthropic {
		return 0, false
	}
	if maxTokens <= 1 {
		maxTokens = 8192
	}
	switch stage.Deliberation {
	case DeliberationLow:
		return 0, true
	case DeliberationMedium:
		if budget := clampThinkingBudget(max(maxTokens/4, anthropicMinThinkingBudget), maxTokens); budget > 0 {
			return budget, true
		}
		return 0, false
	case DeliberationHigh:
		if budget := clampThinkingBudget(max(maxTokens/2, 2048), maxTokens); budget > 0 {
			return budget, true
		}
		return 0, false
	case DeliberationMax:
		if budget := clampThinkingBudget(max((maxTokens*3)/4, 4096), maxTokens); budget > 0 {
			return budget, true
		}
		return 0, false
	default:
		return 0, false
	}
}

func clampThinkingBudget(budget int, maxTokens int) int {
	if budget < 0 {
		return 0
	}
	if maxTokens <= anthropicMinThinkingBudget {
		return 0
	}
	if budget > 0 && budget < anthropicMinThinkingBudget {
		budget = anthropicMinThinkingBudget
	}
	if budget >= maxTokens {
		return maxTokens - 1
	}
	if budget > 0 && budget < anthropicMinThinkingBudget {
		return 0
	}
	return budget
}

func verbosityFor(detail ResponseDetail) string {
	switch detail {
	case ResponseDetailTerse:
		return "low"
	case ResponseDetailNormal:
		return "medium"
	case ResponseDetailDetailed:
		return "high"
	default:
		return ""
	}
}

func reasoningSummaryFor(visibility ThoughtVisibility) string {
	switch visibility {
	case ThoughtVisibilityHidden:
		return "none"
	case ThoughtVisibilitySummary:
		return "concise"
	case ThoughtVisibilityFull:
		return "detailed"
	default:
		return ""
	}
}

func includeThoughtsFor(provider ProviderFamily, visibility ThoughtVisibility) (bool, bool) {
	if provider != ProviderGoogle {
		return false, false
	}
	switch visibility {
	case ThoughtVisibilityHidden:
		return false, true
	case ThoughtVisibilitySummary, ThoughtVisibilityFull:
		return true, true
	default:
		return false, false
	}
}

func applyToolParallelism(profile *Profile, provider ProviderFamily, mode ToolParallelism) {
	if profile == nil {
		return
	}
	if profile.ParallelToolCalls != nil || profile.DisableParallelTools != nil {
		return
	}
	switch mode {
	case ToolParallelismParallel:
		if provider == ProviderOpenAI {
			profile.ParallelToolCalls = Bool(true)
		}
	case ToolParallelismSerial:
		if provider == ProviderOpenAI {
			profile.ParallelToolCalls = Bool(false)
		}
		profile.DisableParallelTools = Bool(true)
	}
}

func applyCacheAffinity(profile *Profile, affinity CacheAffinity, opts ApplyOptions) {
	if profile == nil || affinity == CacheAffinityDefault {
		return
	}
	switch affinity {
	case CacheAffinityNone:
		if profile.UsePromptCache == nil {
			profile.UsePromptCache = Bool(false)
		}
	case CacheAffinitySession:
		if profile.UsePromptCache == nil {
			profile.UsePromptCache = Bool(true)
		}
		if strings.TrimSpace(profile.PromptCacheKey) == "" {
			profile.PromptCacheKey = SessionPromptCacheKey(opts.AgentID, opts.SessionID)
		}
		if strings.TrimSpace(profile.PromptCacheRetention) == "" {
			profile.PromptCacheRetention = "in_memory"
		}
	case CacheAffinitySticky:
		if profile.UsePromptCache == nil {
			profile.UsePromptCache = Bool(true)
		}
		if strings.TrimSpace(profile.PromptCacheKey) == "" {
			profile.PromptCacheKey = SessionPromptCacheKey(opts.AgentID, opts.SessionID)
		}
		if strings.TrimSpace(profile.PromptCacheRetention) == "" {
			profile.PromptCacheRetention = "24h"
		}
	}
}

func stampStageMetadata(req *providers.Request, stage StageProfile, opts ApplyOptions) {
	if req == nil {
		return
	}
	if req.Metadata == nil {
		req.Metadata = make(map[string]any, 10)
	}
	if agentID := strings.TrimSpace(opts.AgentID); agentID != "" {
		req.Metadata[metadataAgentIDKey] = agentID
	}
	if sessionID := strings.TrimSpace(opts.SessionID); sessionID != "" {
		req.Metadata[metadataSessionIDKey] = sessionID
	}
	if stage.Name != "" {
		req.Metadata[metadataStageKey] = stage.Name
		req.Metadata[metadataRuntimeProfileKey] = stage.Name
	}
	if stage.Deliberation != "" {
		req.Metadata[metadataDeliberationKey] = string(stage.Deliberation)
	}
	if stage.ResponseDetail != "" {
		req.Metadata[metadataResponseDetailKey] = string(stage.ResponseDetail)
	}
	if stage.ToolParallelism != "" {
		req.Metadata[metadataToolParallelismKey] = string(stage.ToolParallelism)
	}
	if stage.CacheAffinity != "" {
		req.Metadata[metadataCacheAffinityKey] = string(stage.CacheAffinity)
	}
	if stage.ThoughtVisibility != "" {
		req.Metadata[metadataThoughtVisibilityKey] = string(stage.ThoughtVisibility)
		req.Metadata[metadataEmitThoughtsKey] = stage.ThoughtVisibility != ThoughtVisibilityHidden
	}
}

// DeliberationFromRequest extracts the deliberation level stamped by ApplyStage.
func DeliberationFromRequest(req *providers.Request) Deliberation {
	if req == nil || req.Metadata == nil {
		return DeliberationDefault
	}
	value, _ := req.Metadata[metadataDeliberationKey].(string)
	switch Deliberation(strings.ToLower(strings.TrimSpace(value))) {
	case DeliberationLow:
		return DeliberationLow
	case DeliberationMedium:
		return DeliberationMedium
	case DeliberationHigh:
		return DeliberationHigh
	case DeliberationMax:
		return DeliberationMax
	default:
		return DeliberationDefault
	}
}

func StageFromRequest(req *providers.Request) string {
	if req == nil || req.Metadata == nil {
		return ""
	}
	value, _ := req.Metadata[metadataStageKey].(string)
	return strings.TrimSpace(value)
}

func ProfileNameFromRequest(req *providers.Request) string {
	if req == nil || req.Metadata == nil {
		return ""
	}
	value, _ := req.Metadata[metadataRuntimeProfileKey].(string)
	return strings.TrimSpace(value)
}

func ThoughtVisibilityFromRequest(req *providers.Request) ThoughtVisibility {
	if req == nil || req.Metadata == nil {
		return ThoughtVisibilityDefault
	}
	value, _ := req.Metadata[metadataThoughtVisibilityKey].(string)
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(ThoughtVisibilityHidden):
		return ThoughtVisibilityHidden
	case string(ThoughtVisibilitySummary):
		return ThoughtVisibilitySummary
	case string(ThoughtVisibilityFull):
		return ThoughtVisibilityFull
	default:
		return ThoughtVisibilityDefault
	}
}

func EmitsThoughts(req *providers.Request) bool {
	if req == nil || req.Metadata == nil {
		return true
	}
	if value, ok := req.Metadata[metadataEmitThoughtsKey].(bool); ok {
		return value
	}
	return ThoughtVisibilityFromRequest(req) != ThoughtVisibilityHidden
}

func IsUserFacingSource(sourceAgentID string) bool {
	return strings.EqualFold(strings.TrimSpace(sourceAgentID), "tui")
}

func PromoteForUserFacingTurn(req *providers.Request, sourceAgentID string, visibility ThoughtVisibility) {
	if !IsUserFacingSource(sourceAgentID) {
		return
	}
	PromoteThoughtVisibility(req, visibility)
}

func PromoteThoughtVisibility(req *providers.Request, visibility ThoughtVisibility) {
	if req == nil || visibility == ThoughtVisibilityDefault {
		return
	}
	current := ThoughtVisibilityFromRequest(req)
	if thoughtVisibilityRank(current) > thoughtVisibilityRank(visibility) {
		visibility = current
	}
	if req.Metadata == nil {
		req.Metadata = make(map[string]any)
	}
	req.Metadata[metadataThoughtVisibilityKey] = string(visibility)
	req.Metadata[metadataEmitThoughtsKey] = visibility != ThoughtVisibilityHidden

	switch visibility {
	case ThoughtVisibilityHidden:
		req.ReasoningSummary = "none"
		req.IncludeThoughts = Bool(false)
	case ThoughtVisibilitySummary:
		if strings.TrimSpace(req.ReasoningSummary) == "" || strings.EqualFold(strings.TrimSpace(req.ReasoningSummary), "none") {
			req.ReasoningSummary = "concise"
		}
		req.IncludeThoughts = Bool(true)
	case ThoughtVisibilityFull:
		if strings.TrimSpace(req.ReasoningSummary) == "" || strings.EqualFold(strings.TrimSpace(req.ReasoningSummary), "none") || strings.EqualFold(strings.TrimSpace(req.ReasoningSummary), "concise") {
			req.ReasoningSummary = "detailed"
		}
		req.IncludeThoughts = Bool(true)
	}
}

func thoughtVisibilityRank(visibility ThoughtVisibility) int {
	switch visibility {
	case ThoughtVisibilityHidden:
		return 1
	case ThoughtVisibilitySummary:
		return 2
	case ThoughtVisibilityFull:
		return 3
	default:
		return 0
	}
}

func MatchStage(profiles []StageProfile, intent, domain string) *StageProfile {
	intent = strings.ToLower(strings.TrimSpace(intent))
	domain = strings.ToLower(strings.TrimSpace(domain))
	var intentMatch *StageProfile
	var domainMatch *StageProfile
	var fallback *StageProfile
	for i := range profiles {
		profile := &profiles[i]
		matchesIntent := matchesAny(profile.Intents, intent)
		matchesDomain := matchesAny(profile.Domains, domain)
		switch {
		case matchesIntent && matchesDomain:
			return profile
		case matchesIntent && intentMatch == nil:
			intentMatch = profile
		case matchesDomain && domainMatch == nil:
			domainMatch = profile
		case len(profile.Intents) == 0 && len(profile.Domains) == 0 && fallback == nil:
			fallback = profile
		}
	}
	if intentMatch != nil {
		return intentMatch
	}
	if domainMatch != nil {
		return domainMatch
	}
	return fallback
}

func matchesAny(values []string, target string) bool {
	if target == "" || len(values) == 0 {
		return false
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func RouteScoreBonus(profiles []StageProfile, intent, domain string) int {
	profile := MatchStage(profiles, intent, domain)
	if profile == nil {
		return 0
	}
	bonus := 4
	if matchesAny(profile.Intents, intent) {
		bonus += 2
	}
	if matchesAny(profile.Domains, domain) {
		bonus++
	}
	switch strings.ToLower(strings.TrimSpace(intent)) {
	case "chat", "help", "status":
		bonus += conversationalScore(*profile)
	case "check", "plan", "design", "execute", "complete", "declare":
		bonus += analyticalScore(*profile)
	case "recall", "search", "find", "locate", "fetch":
		bonus += retrievalScore(*profile)
	}
	return bonus
}

func conversationalScore(profile StageProfile) int {
	score := 0
	switch profile.Deliberation {
	case DeliberationLow:
		score += 4
	case DeliberationMedium:
		score += 2
	}
	switch profile.ResponseDetail {
	case ResponseDetailTerse:
		score += 3
	case ResponseDetailNormal:
		score += 2
	}
	if profile.ThoughtVisibility == ThoughtVisibilityHidden || profile.ThoughtVisibility == ThoughtVisibilitySummary {
		score++
	}
	return score
}

func analyticalScore(profile StageProfile) int {
	score := 0
	switch profile.Deliberation {
	case DeliberationMax:
		score += 5
	case DeliberationHigh:
		score += 4
	case DeliberationMedium:
		score += 2
	}
	if profile.ResponseDetail == ResponseDetailDetailed {
		score += 2
	}
	if profile.ToolParallelism == ToolParallelismSerial {
		score++
	}
	return score
}

func retrievalScore(profile StageProfile) int {
	score := 0
	switch profile.Deliberation {
	case DeliberationMedium:
		score += 3
	case DeliberationHigh:
		score += 2
	case DeliberationLow:
		score++
	}
	if profile.ToolParallelism == ToolParallelismParallel {
		score += 2
	}
	return score
}

type ComplexityHints struct {
	ContextPressure float64 `json:"context_pressure"`
	LatencyPressure float64 `json:"latency_pressure"`
	RetentionBias   float64 `json:"retention_bias"`
}

func DeriveComplexity(profiles []StageProfile) ComplexityHints {
	var hints ComplexityHints
	for _, profile := range profiles {
		stage := stageComplexity(profile)
		hints.ContextPressure = math.Max(hints.ContextPressure, stage.ContextPressure)
		hints.LatencyPressure = math.Max(hints.LatencyPressure, stage.LatencyPressure)
		hints.RetentionBias = math.Max(hints.RetentionBias, stage.RetentionBias)
	}
	return hints
}

func stageComplexity(profile StageProfile) ComplexityHints {
	var hints ComplexityHints
	switch profile.Deliberation {
	case DeliberationLow:
		hints.LatencyPressure += 0.05
	case DeliberationMedium:
		hints.LatencyPressure += 0.15
	case DeliberationHigh:
		hints.LatencyPressure += 0.30
	case DeliberationMax:
		hints.LatencyPressure += 0.45
	}
	switch profile.ResponseDetail {
	case ResponseDetailNormal:
		hints.ContextPressure += 0.08
	case ResponseDetailDetailed:
		hints.ContextPressure += 0.18
	}
	switch profile.ThoughtVisibility {
	case ThoughtVisibilitySummary:
		hints.ContextPressure += 0.03
		hints.LatencyPressure += 0.04
		hints.RetentionBias += 0.02
	case ThoughtVisibilityFull:
		hints.ContextPressure += 0.07
		hints.LatencyPressure += 0.09
		hints.RetentionBias += 0.05
	}
	switch profile.CacheAffinity {
	case CacheAffinitySession:
		hints.ContextPressure += 0.03
		hints.RetentionBias += 0.05
	case CacheAffinitySticky:
		hints.ContextPressure += 0.05
		hints.RetentionBias += 0.10
	}
	switch profile.ToolParallelism {
	case ToolParallelismSerial:
		hints.LatencyPressure += 0.05
	case ToolParallelismParallel:
		hints.ContextPressure += 0.02
	}
	return hints
}

func AdjustEvalInterval(base time.Duration, profiles []StageProfile) time.Duration {
	hints := DeriveComplexity(profiles)
	scale := 1.0 - minFloat(0.35, hints.ContextPressure*0.35+hints.LatencyPressure*0.25)
	if scale < 0.55 {
		scale = 0.55
	}
	return time.Duration(float64(base) * scale)
}

func AdjustContextThreshold(base float64, profiles []StageProfile) float64 {
	hints := DeriveComplexity(profiles)
	adjusted := base - minFloat(0.15, hints.ContextPressure*0.12+hints.RetentionBias*0.06)
	if adjusted < 0.55 {
		return 0.55
	}
	return adjusted
}

func AdjustPreserveRecentTurns(base int, profiles []StageProfile) int {
	hints := DeriveComplexity(profiles)
	return base + int(math.Round(hints.RetentionBias*4+hints.LatencyPressure*2))
}

func AdjustOverlapTimeout(base time.Duration, profiles []StageProfile) time.Duration {
	hints := DeriveComplexity(profiles)
	scale := 1.0 + minFloat(0.50, hints.LatencyPressure*0.70+hints.RetentionBias*0.30)
	return time.Duration(float64(base) * scale)
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
