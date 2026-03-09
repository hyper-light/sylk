package architect

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
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
	PlanID                  string                   `json:"plan_id,omitempty"`
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
	textOnly                bool                     `json:"-"` // set internally for text-only fallback
}

// composeRetryTimeoutMultiplier scales LLMRequestTimeout for the retry attempt
// after context cancellation. The retry uses a fresh deadline via
// context.WithoutCancel, so a 2× budget gives the LLM ample room to complete
// a single tool-loop turn without risking an unbounded call.
const composeRetryTimeoutMultiplier = 2

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

	// Check if the request was explicitly cancelled by user interrupt.
	// If so, skip the retry — return immediately so the RequestSerializer
	// releases quickly and the new request can proceed.
	if meta, ok := architectStreamMetadataFromContext(ctx); ok {
		if a.steering.IsCancelled(meta.CorrelationID) {
			return "", ctx.Err()
		}
	}

	// Context cancellation (deadline expiry, not user cancel): retry tool loop
	// with a bounded timeout. context.WithoutCancel preserves values (stream
	// metadata, session ID, thought callbacks) while ignoring the cancellation.
	if ctx.Err() != nil {
		// Reset the UI stream accumulator so the retry replaces (not appends
		// to) partial text from the failed first attempt. Same mechanism as
		// provider-level retries (RetryReset → publishPlanStreamStart).
		a.publishPlanStreamStart(ctx)
		retryTimeout := a.config.LLMRequestTimeout * composeRetryTimeoutMultiplier
		retryCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), retryTimeout)
		defer cancel()
		text, retryErr := a.composeUserFacingResponseWithTools(retryCtx, request)
		if retryErr == nil {
			// Verify the correlation is still alive before returning the result.
			if meta, ok := architectStreamMetadataFromContext(ctx); ok {
				if a.steering.IsCancelled(meta.CorrelationID) {
					return "", context.Canceled
				}
			}
			text = sanitizePlannerConversationResponse(text)
			if text != "" {
				return text, nil
			}
		}
		a.logWarn("composeUserFacingResponse: tool loop retry also failed",
			"retry_error", retryErr)
	}

	// Text-only fallback — use tool-safe prompt to avoid the LLM generating
	// text-based tool calls for tools it doesn't have access to.
	// Reset UI accumulator before fallback streams fresh text.
	a.publishPlanStreamStart(ctx)
	planner := a.ensurePlanner(ctx)
	if planner == nil {
		return "", fmt.Errorf("architect planner not configured (EnableLLM may be false or API key missing)")
	}
	textOnlyRequest := request
	textOnlyRequest.textOnly = true
	text, err = planner.ComposeUserResponse(context.WithoutCancel(ctx), textOnlyRequest)
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

	mode := request.normalizedMode()
	tools = filterToolsByNames(tools, toolsForConversationMode(mode))

	prompt := buildPlannerConversationPrompt(request)
	maxTokens := plannerConversationMaxTokensForMode(mode, a.config.MaxOutputTokens)
	stage := "conversation_" + string(mode)

	// Debug: log the full request construction.
	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.Name
	}
	a.logDebug("compose_with_tools: REQUEST",
		"mode", string(mode),
		"stage", stage,
		"max_tokens", maxTokens,
		"thinking_budget", 0,
		"tools", strings.Join(toolNames, ","),
		"tools_count", len(tools),
		"prompt_len", len(prompt),
		"system_prompt_len", len(planner.ConversationSystemPrompt()),
		"user_query", truncateString(request.UserQuery, 200),
		"history_turns", len(request.ConversationHistory))
	architectDebugLog().Info("handoff: COMPOSE_WITH_TOOLS_REQUEST",
		"mode", string(mode),
		"stage", stage,
		"tools", strings.Join(toolNames, ","),
		"tools_count", len(tools),
		"prompt_len", len(prompt),
		"prompt_preview", truncateString(prompt, 500),
		"plan_id", request.PlanID,
		"current_model", a.config.Model,
		"user_query", truncateString(request.UserQuery, 300))

	req := &providers.Request{
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: prompt},
		},
		MaxTokens:    maxTokens,
		SystemPrompt: planner.ConversationSystemPrompt(),
		Tools:        tools,
	}
	a.applyConversationRuntimeProfile(req, mode, request.SessionID)

	ledger := shared.SteeringLedgerFromContext(ctx)
	text, err := shared.ExecuteTurnLoop(ledger, req, func() (string, error) {
		return a.executeToolLoop(ctx, req, stage, request.OnChunk, ledger)
	})
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
	mode := request.normalizedMode()
	instructions := plannerConversationModeInstructions(mode)
	if request.textOnly {
		instructions = textOnlyModeInstructions(mode)
	}
	return fmt.Sprintf(
		"Conversation context JSON:\n%s\n\n%s",
		mustJSON(request),
		instructions,
	)
}

func normalizePlannerConversationRequest(request plannerConversationRequest) plannerConversationRequest {
	request.Mode = request.normalizedMode()
	request.UserQuery = strings.TrimSpace(request.UserQuery)
	request.PlanID = strings.TrimSpace(request.PlanID)
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
0. If the request is still too vague or underspecified to start responsible planning,
   invoke route_requirements_research instead of start_planning. Use that tool when the
   user first needs help shaping the problem, scope, constraints, or success criteria.
   Use ask_user_question only for one or two narrow decisions after the plan is already
   mostly understood.
1. When the user confirms they want to proceed with planning, invoke start_planning with a
   comprehensive query synthesizing all gathered requirements.
2. After start_planning returns, it gives you a plan_id and protocol instructions. Follow those
   instructions: invoke plan(analyze), consult(pre_planning), plan(design), plan(generate_tasks)
   in order, passing the plan_id to each.
3. After generate_tasks completes, the system renders the plan structure separately in the UI —
   the user already sees it. Do NOT repeat, re-render, or include the plan structure, task list,
   acceptance criteria, file lists, or implementation guides in your text.
   Write ONLY a brief assessment (2-4 sentences):
   - Highlight one critical tradeoff or risk.
   - Sound like a principal engineer, not a workflow bot.
4. Invite the user to approve or request changes. Use your own natural phrasing —
   do NOT use a scripted template or repeat the same wording each time.
   Do NOT invoke route_plan_acceptance — wait for the user's next message.

CRITICAL — Affirmative detection:
If the user's message is an affirmative response to a prior offer to plan (e.g., "yes", "yep",
"go ahead", "do it", "sure", "sounds good", "let's do it"), you MUST invoke start_planning
immediately. Do NOT write text about planning — call the tool. The start_planning query must
synthesize the full conversation context including the original request from prior turns.

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
- If the conversation reveals that the problem is too underspecified to plan safely,
  hand the user to the Academic via route_requirements_research instead of pretending
  the missing requirements are already known.
- If the blocker is missing repository evidence or historical context inside this codebase,
  stay in the Architect and consult the Librarian or Archivalist instead of handing the
  user to the Academic.
- Keep a natural, collaborative tone.
- Do not use canned lead-ins or boilerplate.`
	case plannerConversationModeFeedback:
		return `The user is responding to a plan you presented. The plan_id and plan summary are in the context JSON.

CRITICAL — Approval check:
If the user's response signals acceptance (e.g., "yes", "yep", "looks good", "go ahead", "do it",
"approved", "ship it", or similar affirmative):
1. Invoke route_plan_acceptance with the plan_id from the context JSON and the user's verbatim
   response as user_response. Do NOT write a text reply first — invoke the tool immediately.
2. After invoking route_plan_acceptance, STOP. The acceptance evaluation and any follow-on
   orchestrator handoff resume asynchronously — do not wait in the current turn for a verdict.

If the user is requesting MODIFICATIONS (e.g., "swap steps 2 and 3", "add error handling",
"change the agent type for task 2"):
- Do NOT invoke route_plan_acceptance for modification requests.
- Acknowledge the specific changes they want.
- Reason through the impact on dependencies, ordering, and acceptance criteria.
- Describe exactly what will change and why the modified plan is sound.
- Re-present the updated plan summary (tasks, ordering, key changes) so the user can review.
- End by inviting the user to approve the revised plan or request further changes.

If the user is asking QUESTIONS (e.g., "why did you choose X?", "what about Y?",
"how does task 3 handle Z?"):
- Do NOT invoke route_plan_acceptance for questions.
- Answer concisely with architectural reasoning — be opinionated and specific.
- If the answer reveals a gap in the plan, note what you would adjust.
- Re-present the plan summary only if the answer changes the plan.
- End by inviting the user to approve or continue discussing.

General rules for non-approval responses:
- Maintain a collaborative tone — the plan is a proposal, not a decree.
- Do not dump the full plan structure — focus on what is relevant to their feedback.
- Use natural phrasing, not templates.`
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

// textOnlyModeInstructions returns tool-safe instructions for the text-only
// fallback path. When tools aren't available via the API, the normal mode
// instructions reference tool invocations (start_planning, route_plan_acceptance,
// etc.) which causes the LLM to generate text-based "[tool_call:]" content.
// These instructions preserve the conversational intent without tool language.
func textOnlyModeInstructions(mode plannerConversationMode) string {
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
  - If false: state that the plan is ready and will proceed.
- Do not use canned lead-ins or protocol labels.
- Do not reference any tools or tool invocations.`
	case plannerConversationModeConverse:
		return `Write the next user-facing response.

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
- Do not use canned lead-ins or boilerplate.
- Do not reference any tools or tool invocations.`
	case plannerConversationModeFeedback:
		return `The user is responding to a plan you presented. The plan_id and plan summary are in the context JSON.

Check for approval first:
If the user's response signals acceptance (e.g., "yes", "yep", "looks good", "go ahead",
"approved", "ship it", or similar affirmative):
- Acknowledge their approval and confirm the plan will proceed.

If the user is requesting MODIFICATIONS:
- Acknowledge the specific changes they want.
- Reason through the impact on dependencies, ordering, and acceptance criteria.
- Describe exactly what will change and why the modified plan is sound.
- Re-present the updated plan summary so the user can review.
- Invite the user to approve the revised plan or request further changes.

If the user is asking QUESTIONS:
- Answer concisely with architectural reasoning.
- If the answer reveals a gap, note what you would adjust.
- Re-present the plan summary only if the answer changes the plan.
- Invite the user to approve or continue discussing.

General rules:
- Maintain a collaborative tone.
- Do not dump the full plan structure — focus on what is relevant.
- Do not reference any tools or tool invocations.`
	default:
		return plannerConversationModeInstructions(mode)
	}
}

// toolsForConversationMode returns the allowed tool names for a given mode.
// Feedback and ready modes restrict to acceptance-related tools, reducing the
// tool set from ~10 to 3 so GPT models are less confused about which tool to call.
// Converse and clarification modes return nil (all tools allowed).
func toolsForConversationMode(mode plannerConversationMode) []string {
	switch mode {
	case plannerConversationModeFeedback, plannerConversationModeReady:
		return []string{"route_plan_acceptance", "ask_user_question"}
	default:
		return nil
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
