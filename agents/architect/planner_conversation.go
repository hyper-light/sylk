package architect

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/providers"
)

type plannerConversationMode string

const (
	plannerConversationModeClarification plannerConversationMode = "clarification"
	plannerConversationModeReady         plannerConversationMode = "ready"
	plannerConversationModeConverse      plannerConversationMode = "converse"
	plannerConversationModeFeedback      plannerConversationMode = "feedback"
)

type plannerConversationRequest struct {
	Mode                    plannerConversationMode  `json:"mode"`
	UserQuery               string                   `json:"user_query"`
	IntentHint              string                   `json:"intent_hint,omitempty"`
	PriorQuery              string                   `json:"prior_query,omitempty"`
	Scope                   string                   `json:"scope,omitempty"`
	RecommendationNarrative string                   `json:"recommendation_narrative,omitempty"`
	Recommendations         []string                 `json:"recommendations,omitempty"`
	Tradeoffs               []string                 `json:"tradeoffs,omitempty"`
	Assumptions             []string                 `json:"assumptions,omitempty"`
	ClarificationQuestions  []string                 `json:"clarification_questions,omitempty"`
	TaskCount               int                      `json:"task_count,omitempty"`
	FirstTask               string                   `json:"first_task,omitempty"`
	PlanSummary             string                   `json:"plan_summary,omitempty"`
	ConversationHistory     []guide.ConversationTurn `json:"conversation_history,omitempty"`
	OnChunk                 func(string)             `json:"-"`
}

func (a *Architect) composeUserFacingResponse(
	ctx context.Context,
	request plannerConversationRequest,
) (string, error) {
	// Try tool-loop path first — enables the LLM to invoke skills like
	// route_plan_acceptance during conversation.
	text, err := a.composeUserFacingResponseWithTools(ctx, request)
	if err == nil {
		return text, nil
	}
	a.logWarn("architect tool-loop compose failed, trying text-only", "error", err)

	// Fall through to text-only streaming path.
	planner := a.ensurePlanner()
	if planner == nil {
		return "", fmt.Errorf("architect planner not configured (EnableLLM may be false or API key missing)")
	}
	text, err = planner.ComposeUserResponse(ctxOrBackground(ctx), request)
	if err != nil {
		a.logger.Warn("architect llm conversation fallback", "mode", request.normalizedMode(), "error", err)
		return "", fmt.Errorf("architect planner: %w", err)
	}
	text = sanitizePlannerConversationResponse(text)
	if text == "" {
		return "", fmt.Errorf("architect planner returned empty response")
	}
	return text, nil
}

// composeUserFacingResponseWithTools builds a request with tool definitions
// and runs the tool dispatch loop, enabling the LLM to invoke architect skills
// (e.g. route_plan_acceptance, consult_librarian) during conversation turns.
func (a *Architect) composeUserFacingResponseWithTools(
	ctx context.Context,
	request plannerConversationRequest,
) (string, error) {
	planner := a.ensurePlanner()
	if planner == nil {
		return "", fmt.Errorf("architect planner not configured")
	}

	a.ensureToolLoopSkillsLoaded()
	tools := a.buildToolDefinitions()
	if len(tools) == 0 {
		return "", fmt.Errorf("no tool definitions available")
	}

	prompt := buildPlannerConversationPrompt(request)
	maxTokens := plannerConversationMaxTokensForMode(request.normalizedMode(), a.config.MaxOutputTokens)
	stage := "conversation_" + string(request.normalizedMode())

	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: prompt},
		},
		MaxTokens:      maxTokens,
		SystemPrompt:   planner.ConversationSystemPrompt(),
		ThinkingBudget: 0, // Adaptive — let the provider handle allocation.
		Tools:          tools,
	}

	text, err := a.executeToolLoop(ctx, req, stage, request.OnChunk)
	if err != nil {
		return "", err
	}

	text = sanitizePlannerConversationResponse(text)
	if text == "" {
		return "", fmt.Errorf("architect planner returned empty response")
	}
	return text, nil
}

func (p *anthropicPlanner) ComposeUserResponse(
	ctx context.Context,
	request plannerConversationRequest,
) (string, error) {
	prompt := buildPlannerConversationPrompt(request)
	maxTokens := plannerConversationMaxTokensForMode(request.normalizedMode(), p.maxTokens)
	stage := "conversation_" + string(request.normalizedMode())
	text, _, err := p.requestTextStreamingWithMaxTokens(
		ctx,
		prompt,
		maxTokens,
		p.conversationSystem,
		stage,
		request.OnChunk,
	)
	if err != nil {
		return "", err
	}
	return sanitizePlannerConversationResponse(text), nil
}

func buildPlannerConversationSystemPrompt(base string) string {
	modules := []string{
		ArchitectSystemCorePrompt,
		ArchitectSystemProtocolPrompt,
		ArchitectSystemGuardrailsPrompt,
		ArchitectConversationPrompt,
	}
	if custom := customConversationContext(base); custom != "" {
		modules = append(modules, "Project-specific directives:\n"+custom)
	}
	return strings.Join(nonEmptyPlannerSections(modules), "\n\n---\n\n")
}

func customConversationContext(base string) string {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		return ""
	}
	if trimmed == strings.TrimSpace(DefaultSystemPrompt) {
		return ""
	}
	return trimmed
}

func nonEmptyPlannerSections(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}

func buildPlannerConversationPrompt(request plannerConversationRequest) string {
	request = normalizePlannerConversationRequest(request)
	return fmt.Sprintf(
		"Conversation context JSON:\n%s\n\n%s",
		mustJSON(request),
		plannerConversationModeInstructions(request.normalizedMode()),
	)
}

func normalizePlannerConversationRequest(request plannerConversationRequest) plannerConversationRequest {
	request.Mode = request.normalizedMode()
	request.UserQuery = strings.TrimSpace(request.UserQuery)
	request.PriorQuery = strings.TrimSpace(request.PriorQuery)
	request.Scope = strings.TrimSpace(request.Scope)
	request.RecommendationNarrative = strings.TrimSpace(request.RecommendationNarrative)
	request.Recommendations = normalizePlannerConversationList(request.Recommendations)
	request.Tradeoffs = normalizePlannerConversationList(request.Tradeoffs)
	request.Assumptions = normalizePlannerConversationList(request.Assumptions)
	request.ClarificationQuestions = normalizePlannerConversationList(request.ClarificationQuestions)
	if request.TaskCount < 0 {
		request.TaskCount = 0
	}
	request.FirstTask = strings.TrimSpace(request.FirstTask)
	request.PlanSummary = strings.TrimSpace(request.PlanSummary)
	return request
}

func normalizePlannerConversationList(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func plannerConversationModeInstructions(mode plannerConversationMode) string {
	switch mode {
	case plannerConversationModeReady:
		return `Write the next user-facing response.

Requirements:
- Sound like a principal engineer, not a workflow bot.
- Summarize the plan in plain language and include why it is a good default.
- Mention one critical tradeoff or risk the user should validate.
- Ask one concise next-step question: refine now or proceed to execution.
- Do not use canned lead-ins or protocol labels.`
	case plannerConversationModeConverse:
		return `Write the next user-facing response.

The user is in conversation with you — an expert software architect. They may be:
- Asking for advice, recommendations, or opinions
- Providing pushback or disagreement on a prior suggestion
- Asking clarifying questions about technology, patterns, or tradeoffs
- Requesting estimates or complexity assessments
- Having general technical discussion

Requirements:
- Respond directly and substantively to whatever the user said.
- Draw on your architectural expertise — be opinionated with clear reasoning.
- If the conversation naturally leads to a concrete implementation task, mention that you can help plan it, but do not force the conversation into a planning protocol.
- Keep a natural, collaborative tone.
- Do not use canned lead-ins or boilerplate.`
	case plannerConversationModeFeedback:
		return `The user is responding to a plan you presented. The plan summary is included in the context JSON as "plan_summary".

CRITICAL — Check for approval first:
If the user's response signals acceptance (e.g., "yes", "yep", "looks good", "go ahead", "do it", "approved", "ship it", or similar affirmative), you MUST invoke the route_plan_acceptance tool with their verbatim response. Do NOT write a text reply — route immediately.

If the user is NOT approving (they have questions, want changes, or disagree), write a response:
- Address the user's feedback directly.
- If they want refinements, explain specifically what you'll change and why.
- If they're asking for clarification, answer concisely with architectural reasoning.
- Maintain a collaborative tone — the plan is a proposal, not a decree.
- Do not re-present the entire plan — focus on what changes based on their feedback.
- End with a brief re-approval cue (e.g., "Say **go ahead** to proceed, or let me know what else to adjust.").`
	default:
		return `Write the next user-facing response.

Requirements:
- Answer the user's actual question first with a concrete recommendation.
- Explain key tradeoffs and failure modes in plain language.
- Ask at most two targeted follow-up questions if decisions are still needed.
- Keep the tone natural and collaborative, without boilerplate.
- Do not use canned lead-ins (for example "Before I finalize").`
	}
}

func plannerConversationMaxTokensForMode(mode plannerConversationMode, maxTokens int) int {
	switch mode {
	case plannerConversationModeConverse, plannerConversationModeReady, plannerConversationModeFeedback:
		return converseMaxTokens(maxTokens)
	default:
		return plannerConversationMaxTokens(maxTokens)
	}
}

func plannerConversationMaxTokens(maxTokens int) int {
	if maxTokens <= 0 {
		return 768
	}
	budget := maxTokens / 4
	if budget < 640 {
		return 640
	}
	if budget > 1536 {
		return 1536
	}
	return budget
}

func converseMaxTokens(maxTokens int) int {
	if maxTokens <= 0 {
		return DefaultMaxOutputTokens
	}
	return maxTokens
}

func sanitizePlannerConversationResponse(text string) string {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.Trim(trimmed, "`")
	trimmed = strings.TrimSpace(trimmed)
	if unquoted, err := strconv.Unquote(trimmed); err == nil {
		trimmed = strings.TrimSpace(unquoted)
	}
	return strings.TrimSpace(trimmed)
}

func (r plannerConversationRequest) normalizedMode() plannerConversationMode {
	switch r.Mode {
	case plannerConversationModeReady:
		return plannerConversationModeReady
	case plannerConversationModeConverse:
		return plannerConversationModeConverse
	case plannerConversationModeFeedback:
		return plannerConversationModeFeedback
	default:
		return plannerConversationModeClarification
	}
}
