package architect

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type clarificationDecision struct {
	Needed      bool
	Questions   []string
	Assumptions []string
}

type clarificationContext struct {
	PriorQuery string
	PriorScope string
}

func (r *planningProtocolRunner) stepClarify() (bool, error) {
	if shouldSkipClarification(r.request) {
		return false, nil
	}
	request := r.buildClarifyDecisionRequest()
	request.OnChunk = func(text string) {
		r.architect.publishPlanStreamChunk(r.ctx, text)
	}
	response, err := r.architect.composeUserFacingResponse(r.ctx, request)
	if err != nil {
		// LLM unavailable — skip clarification, proceed with planning.
		return false, nil
	}
	// The ask_user_question skill handler populates ClarificationQuestions
	// on the plan when invoked during the tool loop. If the LLM decided
	// not to ask, the slice stays empty.
	if len(r.plan.ClarificationQuestions) == 0 {
		return false, nil
	}
	r.plan.UserResponse = response
	r.plan.RiskSummary = append(r.plan.RiskSummary, "planning paused pending user clarification")
	if err := r.transition(PlanStatusClarifying); err != nil {
		return false, err
	}
	return true, nil
}

func (r *planningProtocolRunner) buildClarifyDecisionRequest() plannerConversationRequest {
	requirements := requirementsFromPlan(r.plan)
	return plannerConversationRequest{
		Mode:                    plannerConversationModeClarifyDecision,
		UserQuery:               reqQuery(r.request),
		PriorQuery:              requirementsQuery(requirements),
		Scope:                   requirementsScope(requirements),
		RecommendationNarrative: clarificationRecommendationNarrative(requirements),
		Recommendations:         clarificationRecommendationItems(requirements),
		Tradeoffs:               clarificationTradeoffItems(requirements),
		Assumptions:             assumptionsFromPlan(r.plan),
		ClarificationQuestions:  clarificationQuestionsFromMetadata(requirements),
		SessionID:               reqSessionID(r.request),
		ConversationHistory:     reqConversationHistory(r.request),
	}
}

func (a *Architect) clarificationDecisionForRequest(
	ctx context.Context,
	req *ArchitectRequest,
	requirements *Requirements,
) clarificationDecision {
	if a.requiresConsultationBeforeClarification() {
		return clarificationDecision{}
	}
	if shouldSkipClarification(req) {
		return clarificationDecision{}
	}
	flowCtx := a.clarificationContextForRequest(req)
	a.enrichRecommendationMetadata(ctxOrBackground(ctx), req, requirements, flowCtx)
	questions := clarificationQuestionsFromMetadata(requirements)
	if len(questions) == 0 {
		return clarificationDecision{}
	}
	if !requiresClarification(requirements) {
		return clarificationDecision{}
	}
	limit := a.clarificationQuestionLimit()
	assumptions := clarificationAssumptionsFromMetadata(requirements)
	return clarificationDecision{
		Needed:      true,
		Questions:   capQuestions(questions, limit),
		Assumptions: assumptions,
	}
}

// requiresClarification returns true only when the analysis produced
// significantly more uncertainty signals than well-defined goals. The
// ratio of ambiguity signals (clarification questions + unknowns) to
// goals determines whether clarification is warranted — we only
// clarify when unknowns outweigh knowns. This prevents trivially
// simple requests (e.g. "hello world Python script") from entering
// clarification mode when the LLM speculatively generates questions
// as part of thorough analysis.
func requiresClarification(requirements *Requirements) bool {
	if requirements == nil {
		return false
	}
	goals := len(requirements.Goals)
	if goals == 0 {
		return true
	}
	questions := len(clarificationQuestionsFromMetadata(requirements))
	unknowns := len(unknownsFromMetadata(requirements))
	ambiguitySignals := questions + unknowns
	return ambiguitySignals > goals
}

func unknownsFromMetadata(requirements *Requirements) []string {
	if requirements == nil || requirements.Metadata == nil {
		return nil
	}
	return stringSliceFromAny(requirements.Metadata["unknowns"])
}

func ctxOrBackground(ctx context.Context) context.Context {
	if ctx != nil && ctx.Err() == nil {
		return ctx
	}
	return context.Background()
}

func (a *Architect) enrichRecommendationMetadata(
	ctx context.Context,
	req *ArchitectRequest,
	requirements *Requirements,
	_ clarificationContext,
) {
	if a == nil || req == nil || requirements == nil {
		return
	}
	if hasRecommendationMetadata(requirements.Metadata) {
		return
	}
	params := cloneParams(req.Params)
	params["force_recommendations"] = true
	analysisCtx, cancel := context.WithTimeout(ctxOrBackground(ctx), recommendationHydrationTimeout(a.config.LLMRequestTimeout))
	defer cancel()
	enriched, ok := a.tryAnalyzeRequirementsWithLLM(analysisCtx, req.Query, params)
	if !ok || enriched == nil || len(enriched.Metadata) == 0 {
		return
	}
	requirements.Metadata = mergeRecommendationMetadata(requirements.Metadata, enriched.Metadata)
	if strings.TrimSpace(requirements.Scope) == "" && strings.TrimSpace(enriched.Scope) != "" {
		requirements.Scope = enriched.Scope
	}
}

func recommendationHydrationTimeout(base time.Duration) time.Duration {
	if base <= 0 {
		return 20 * time.Second
	}
	if base > 20*time.Second {
		return 20 * time.Second
	}
	return base
}

func hasRecommendationMetadata(metadata map[string]any) bool {
	if len(metadata) == 0 {
		return false
	}
	if values := stringSliceFromAny(metadata["provisional_recommendations"]); len(values) > 0 {
		return true
	}
	if values := stringSliceFromAny(metadata["tradeoffs"]); len(values) > 0 {
		return true
	}
	return strings.TrimSpace(stringFromAny(metadata["recommendation_narrative"])) != ""
}

func mergeRecommendationMetadata(dst map[string]any, src map[string]any) map[string]any {
	if len(dst) == 0 {
		dst = map[string]any{}
	}
	if len(src) == 0 {
		return dst
	}
	mergeRecommendationMetadataKey(dst, src, "provisional_recommendations")
	mergeRecommendationMetadataKey(dst, src, "tradeoffs")
	if strings.TrimSpace(stringFromAny(dst["recommendation_narrative"])) == "" {
		if narrative := strings.TrimSpace(stringFromAny(src["recommendation_narrative"])); narrative != "" {
			dst["recommendation_narrative"] = narrative
		}
	}
	return dst
}

func mergeRecommendationMetadataKey(dst map[string]any, src map[string]any, key string) {
	if len(stringSliceFromAny(dst[key])) > 0 {
		return
	}
	values := stringSliceFromAny(src[key])
	if len(values) == 0 {
		return
	}
	dst[key] = values
}

func (a *Architect) requiresConsultationBeforeClarification() bool {
	if a == nil {
		return false
	}
	if !a.config.MandatoryConsultation {
		return false
	}
	return !(a.running && a.bus != nil)
}

func shouldSkipClarification(req *ArchitectRequest) bool {
	if req == nil || req.Params == nil {
		return false
	}
	if value, ok := req.Params["skip_clarification"].(bool); ok && value {
		return true
	}
	if value, ok := req.Params["ready_to_execute"].(bool); ok && value {
		return true
	}
	return false
}

func clarificationQuestionsFromMetadata(requirements *Requirements) []string {
	if requirements == nil || requirements.Metadata == nil {
		return nil
	}
	raw := requirements.Metadata["clarification_questions"]
	return stringSliceFromAny(raw)
}

func stringSliceFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return sanitizeQuestions(typed)
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text == "" || text == "<nil>" {
				continue
			}
			items = append(items, text)
		}
		return sanitizeQuestions(items)
	default:
		return nil
	}
}

func (a *Architect) clarificationUserResponse(
	ctx context.Context,
	req *ArchitectRequest,
	requirements *Requirements,
	decision clarificationDecision,
) string {
	conversation := buildClarificationConversationRequest(req, requirements, decision)
	if response, err := a.composeUserFacingResponse(ctx, conversation); err == nil {
		return response
	}
	return fallbackClarificationResponse(decision.Questions)
}

func buildClarificationConversationRequest(
	req *ArchitectRequest,
	requirements *Requirements,
	decision clarificationDecision,
) plannerConversationRequest {
	return plannerConversationRequest{
		Mode:                    plannerConversationModeClarification,
		UserQuery:               reqQuery(req),
		PriorQuery:              requirementsQuery(requirements),
		Scope:                   requirementsScope(requirements),
		RecommendationNarrative: clarificationRecommendationNarrative(requirements),
		Recommendations:         clarificationRecommendationItems(requirements),
		Tradeoffs:               clarificationTradeoffItems(requirements),
		Assumptions:             decision.Assumptions,
		ClarificationQuestions:  decision.Questions,
		ConversationHistory:     reqConversationHistory(req),
	}
}

func fallbackClarificationResponse(questions []string) string {
	if len(questions) > 0 {
		return strings.Join(questions, "\n")
	}
	return "I need more information before I can plan this. What are the key requirements?"
}

func clarificationRecommendationNarrative(requirements *Requirements) string {
	return strings.TrimSpace(stringFromAny(requirementsMetadataValue(requirements, "recommendation_narrative")))
}

func clarificationRecommendationItems(requirements *Requirements) []string {
	return stringSliceFromAny(requirementsMetadataValue(requirements, "provisional_recommendations"))
}

func clarificationTradeoffItems(requirements *Requirements) []string {
	return stringSliceFromAny(requirementsMetadataValue(requirements, "tradeoffs"))
}

func requirementsMetadataValue(requirements *Requirements, key string) any {
	if requirements == nil || requirements.Metadata == nil {
		return nil
	}
	return requirements.Metadata[key]
}

func reqQuery(req *ArchitectRequest) string {
	if req == nil {
		return ""
	}
	return req.Query
}

func requirementsQuery(requirements *Requirements) string {
	if requirements == nil {
		return ""
	}
	return requirements.Query
}

func requirementsScope(requirements *Requirements) string {
	if requirements == nil {
		return ""
	}
	return requirements.Scope
}

func (a *Architect) clarificationContextForRequest(req *ArchitectRequest) clarificationContext {
	if a == nil || req == nil {
		return clarificationContext{}
	}
	if ctx := clarificationContextFromRequestParams(req.Params); ctx != (clarificationContext{}) {
		return ctx
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		return clarificationContext{}
	}
	plan := a.latestHistoricalPlanForSession(sessionID)
	if plan == nil {
		return clarificationContext{}
	}
	ctx := clarificationContext{}
	if plan.Requirements != nil {
		ctx.PriorQuery = plan.Requirements.Query
		ctx.PriorScope = plan.Requirements.Scope
	}
	if strings.TrimSpace(ctx.PriorQuery) == "" {
		ctx.PriorQuery = plan.Query
	}
	return ctx
}

// latestConsultingPlan returns the most recently updated plan in Consulting
// state for the given session. Used by the ask_user_question skill handler
// to attach clarification questions to the in-flight plan.
func (a *Architect) latestConsultingPlan(sessionID string) *DesignPlan {
	if a == nil {
		return nil
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil
	}
	a.activePlansMu.RLock()
	defer a.activePlansMu.RUnlock()
	var best *DesignPlan
	for _, plan := range a.activePlans {
		if plan == nil || plan.SM().State() != PlanStatusConsulting {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(plan.SessionID), trimmed) {
			continue
		}
		if best == nil || plan.UpdatedAt.After(best.UpdatedAt) {
			best = plan
		}
	}
	return best
}

func (a *Architect) latestHistoricalPlanForSession(sessionID string) *DesignPlan {
	if a == nil {
		return nil
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil
	}
	a.activePlansMu.RLock()
	defer a.activePlansMu.RUnlock()
	var latest *DesignPlan
	for _, plan := range a.activePlans {
		if plan == nil || !strings.EqualFold(strings.TrimSpace(plan.SessionID), trimmed) {
			continue
		}
		if !hasHistoricalContext(plan) {
			continue
		}
		if latest == nil || plan.UpdatedAt.After(latest.UpdatedAt) {
			latest = plan
		}
	}
	return latest
}

func hasHistoricalContext(plan *DesignPlan) bool {
	if plan == nil {
		return false
	}
	if plan.Requirements != nil && strings.TrimSpace(plan.Requirements.Query) != "" {
		return true
	}
	return strings.TrimSpace(plan.Query) != ""
}

func clarificationContextFromRequestParams(params map[string]any) clarificationContext {
	if len(params) == 0 {
		return clarificationContext{}
	}
	value, ok := params["session_context"]
	if !ok {
		return clarificationContext{}
	}
	ctxMap, ok := value.(map[string]any)
	if !ok {
		return clarificationContext{}
	}
	return clarificationContext{
		PriorQuery: firstNonEmptyContextString(
			stringFromAny(ctxMap["prior_plan_query"]),
		),
		PriorScope: firstNonEmptyContextString(
			stringFromAny(ctxMap["prior_scope"]),
		),
	}
}

func clarificationAssumptionsFromMetadata(requirements *Requirements) []string {
	if requirements == nil || len(requirements.Metadata) == 0 {
		return nil
	}
	recommendations := stringSliceFromAny(requirements.Metadata["provisional_recommendations"])
	tradeoffs := stringSliceFromAny(requirements.Metadata["tradeoffs"])
	combined := append([]string{}, recommendations...)
	combined = append(combined, tradeoffs...)
	return sanitizeQuestions(combined)
}

func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func firstNonEmptyContextString(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || trimmed == "<nil>" {
			continue
		}
		return trimmed
	}
	return ""
}

func capQuestions(questions []string, limit int) []string {
	clean := sanitizeQuestions(questions)
	if limit <= 0 || len(clean) <= limit {
		return clean
	}
	return clean[:limit]
}

func (a *Architect) clarificationQuestionLimit() int {
	if a == nil || a.config.MaxOutputTokens <= 0 {
		return 2
	}
	limit := a.config.MaxOutputTokens / 2048
	if limit < 2 {
		return 2
	}
	if limit > 5 {
		return 5
	}
	return limit
}

func sanitizeQuestions(questions []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(questions))
	for _, question := range questions {
		trimmed := strings.TrimSpace(question)
		if trimmed == "" {
			continue
		}
		key := normalizeText(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func normalizeText(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

