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
	plannerConversationModeReady        plannerConversationMode = "ready"
	plannerConversationModeConverse         plannerConversationMode = "converse"
	plannerConversationModeFeedback         plannerConversationMode = "feedback"
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
	LayerCount              int                      `json:"layer_count,omitempty"`
	FirstTask               string                   `json:"first_task,omitempty"`
	PlanSummary             string                   `json:"plan_summary,omitempty"`
	ApprovalRequired        bool                     `json:"approval_required,omitempty"`
	SessionID               string                   `json:"session_id,omitempty"`
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
		architectDebugLog().Info("composeUserFacingResponse: TOOL_LOOP_SUCCESS",
			"mode", string(request.normalizedMode()),
			"text_len", len(text),
			"ctx_err", ctx.Err())
		return text, nil
	}
	architectDebugLog().Warn("composeUserFacingResponse: TOOL_LOOP_FAILED",
		"mode", string(request.normalizedMode()),
		"error", err.Error(),
		"ctx_err", ctx.Err())
	a.logWarn("architect tool-loop compose failed, trying text-only", "error", err)

	// Fall through to text-only streaming path.
	planner := a.ensurePlanner(ctx)
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
	planner := a.ensurePlanner(ctx)
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

	// Debug: log the full request construction.
	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.Name
	}
	a.logDebug("compose_with_tools: REQUEST",
		"mode", string(request.normalizedMode()),
		"stage", stage,
		"max_tokens", maxTokens,
		"thinking_budget", 0,
		"tools", strings.Join(toolNames, ","),
		"tools_count", len(tools),
		"prompt_len", len(prompt),
		"system_prompt_len", len(planner.ConversationSystemPrompt()),
		"user_query", truncateString(request.UserQuery, 200),
		"history_turns", len(request.ConversationHistory))

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
		a.logDebug("compose_with_tools: TOOL_LOOP_ERROR",
			"stage", stage,
			"err", err.Error())
		return "", err
	}

	a.logDebug("compose_with_tools: RAW_RESULT",
		"stage", stage,
		"text_len", len(text),
		"text_preview", truncateString(text, 300))

	text = sanitizePlannerConversationResponse(text)
	if text == "" {
		a.logDebug("compose_with_tools: EMPTY_AFTER_SANITIZE",
			"stage", stage)
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
- Check the "approval_required" field in the context JSON:
  - If true: ask the user whether to refine or proceed. End by inviting the user
    to approve or request changes. Use your own natural phrasing — do NOT use a
    scripted template or repeat the same wording each time.
    Do NOT use robotic phrasing like "Do you approve this plan?" or "Please confirm."
    Do NOT automatically invoke route_plan_acceptance, you must receive a response from
    the user.
  - If false: invoke route_plan_acceptance immediately with the plan details.
    Do not ask for approval.
- Do not use canned lead-ins or protocol labels.`
	case plannerConversationModeConverse:
		return `Write the next user-facing response.

Planning flow:
1. When the user confirms they want to proceed with planning, invoke start_planning with a
   comprehensive query synthesizing all gathered requirements.
2. After start_planning returns, it gives you a plan_id and protocol instructions. Follow those
   instructions: invoke plan(analyze), consult(pre_planning), plan(design), plan(generate_tasks)
   in order, passing the plan_id to each.
3. After generate_tasks completes, the plan is automatically rendered in your response.
   Do NOT repeat the plan structure or task list. Write a brief assessment:
   - Highlight one critical tradeoff or risk.
   - Sound like a principal engineer, not a workflow bot.
4. Invite the user to approve or request changes. Use your own natural phrasing —
   do NOT use a scripted template or repeat the same wording each time.
   Do NOT invoke route_plan_acceptance — wait for the user's next message.

The user is in conversation with you — an expert software architect. They may be:
- Requesting implementation — gather requirements and clarify constraints first
- Asking for advice, recommendations, or opinions
- Providing pushback or disagreement on a prior suggestion
- Asking clarifying questions about technology, patterns, or tradeoffs
- Requesting estimates or complexity assessments
- Having general technical discussion

Requirements:
- Respond directly and substantively to whatever the user said.
- Draw on your architectural expertise — be opinionated with clear reasoning.
- For general conversation (no planning intent), engage naturally. If the conversation
  leads to a concrete task, ask if they'd like you to create a plan.
- Keep a natural, collaborative tone.
- Do not use canned lead-ins or boilerplate.`
	case plannerConversationModeFeedback:
		return `The user is responding to a plan you presented. The plan summary is included in the context JSON as "plan_summary".

CRITICAL — Check for approval first:
If the user's response signals acceptance (e.g., "yes", "yep", "looks good", "go ahead", "do it",
"approved", "ship it", or similar affirmative):
1. Invoke route_plan_acceptance with plan_id and the user's verbatim response. Do NOT write a
   text reply first — invoke the tool immediately.
2. When route_plan_acceptance returns the Guide's verdict, invoke handle_plan_acceptance_result
   with the verdict details (plan_id, result, user_response). On "accept", the plan dispatches
   to the orchestrator automatically.
3. After handle_plan_acceptance_result confirms dispatch, write a brief acknowledgment
   (e.g., "Plan dispatched — the orchestrator is picking it up now.").

If the user is NOT approving (they have questions, want changes, or disagree), write a response:
- Address the user's feedback directly.
- If they want refinements, explain specifically what you'll change and why.
- If they're asking for clarification, answer concisely with architectural reasoning.
- Maintain a collaborative tone — the plan is a proposal, not a decree.
- Do not re-present the entire plan — focus on what changes based on their feedback.
- End by inviting the user to approve or request further changes. Use natural phrasing, not a template.`
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
