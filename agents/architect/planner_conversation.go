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
	plannerConversationModeExistingReady plannerConversationMode = "existing_ready"
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
	// FreshnessSummary, DriftSignals, OrchestratorStateHint surface
	// the freshness audit's findings to the LLM so it can include them
	// in the narrative response. Populated by the architect's compose
	// path before the LLM runs; the dialog itself shows only buttons,
	// so the chat narrative is the only place this information lives.
	FreshnessSummary       string   `json:"freshness_summary,omitempty"`
	FreshnessDriftSignals  []string `json:"freshness_drift_signals,omitempty"`
	OrchestratorStateHint  string   `json:"orchestrator_state_hint,omitempty"`
	FreshnessRecommendation string  `json:"freshness_recommendation,omitempty"`
	RecentContextSummary    string                   `json:"recent_context_summary,omitempty"`
	RecentContextFocus      []string                 `json:"recent_context_focus,omitempty"`
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
	// plan_acceptance during conversation.
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
		return "", fmt.Errorf("architect planner not configured (EnableLLM may be false or Anthropic auth is unavailable)")
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
// (e.g. plan_acceptance, consult_peer) during conversation turns.
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
	tools = filterToolsByNames(tools, toolsForConversationModeWithContext(ctx, mode))

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
	a.injectForestPreload(ctx, req, request.UserQuery, request.SessionID)

	ledger := shared.SteeringLedgerFromContext(ctx)
	// User-facing turn: the caller (Handle / executeConversation) is
	// synchronously awaiting the architect's response. Do NOT stamp
	// WithContinuationStore here — yielding would return an empty
	// answer to the user while the resume runs in the background and
	// has nowhere to deliver its result. Consult_peer falls back to
	// the legacy synchronous wait (runLegacyConsultWait) which blocks
	// inline until the librarian's testament arrives, the loop
	// continues, and the final response reaches the user.
	//
	// Yield-resume is reserved for claim-inbox-driven paths
	// (processClaimsEntry) where no synchronous caller is waiting.
	loopCtx := ctx
	text, err := shared.ExecuteTurnLoop(loopCtx, ledger, req, func() (string, error) {
		return a.executeToolLoop(loopCtx, req, stage, request.OnChunk, ledger)
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
		ArchitectSystemConsultPrompt,
		ArchitectSystemSkillsPrompt,
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
	request.RecentContextSummary = strings.TrimSpace(request.RecentContextSummary)
	request.RecentContextFocus = normalizePlannerConversationList(request.RecentContextFocus)
	request.SessionID = strings.TrimSpace(request.SessionID)
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
		return `Write the next user-facing response for a Ready plan.

The system has already published an Approve / Modify / Reject dialog in the
input panel — you do NOT need to invoke any acceptance tool yourself. Your
job is the narrative summary that accompanies the dialog.

Requirements:
- Sound like a principal engineer, not a workflow bot.
- Summarize the plan in plain language and include why it is a good default.
- Mention one critical tradeoff or risk the user should validate.
- Do NOT ask the user to "approve in chat" or "type yes" — the dialog buttons
  ARE the decision mechanism. Trust them; let the buttons speak for themselves.
- Do NOT imply that work is already beginning. Avoid phrases like "kick it
  off", "start building", "start implementing", "get started", or "ship it".
- Do NOT invoke plan_acceptance — the dialog routes the verdict directly.
- Do not use canned lead-ins or protocol labels.

When approval_required is false (auto-approve), still skip plan_acceptance
here; the system handles acceptance automatically in that mode.`
	case plannerConversationModeExistingReady:
		return `The user has a previously prepared ready plan available. The plan_id, prior_query, and plan summary are in the context JSON.

The system automatically publishes an Approve / Modify / Reject dialog
whenever a Ready plan is being presented — including this one. Drift signals
and execution-state hints from the freshness audit appear in the dialog body.
You do NOT need to invoke any acceptance tool yourself; just write the
narrative that surrounds the dialog.

If the user's message clearly accepts or resumes that ready plan (for example
"go ahead", "resume it", "ship it", or "use the previous plan"):
- Acknowledge briefly. Trust the dialog buttons; do NOT invoke
  plan_acceptance.

If the user is asking what the earlier plan covered, why it was structured this way, or whether it is still a good idea:
- Answer naturally using the recovered plan context.
- Call out one meaningful risk or stale assumption if it matters.
- Trust the dialog to handle the verdict — do not ask the user to "type yes".

If the user wants changes or a fresh direction:
- Explain whether the existing plan can be revised cleanly or whether a new plan is the better move.
- If the new direction is concrete enough, you may invoke plan(action=start).
- If the request is still too underspecified to plan responsibly, ask focused questions or invoke academic_research(action=request).

General rules:
- Treat the previous plan as available context, not as a commitment the user already made.
- Sound like a principal engineer, not a workflow bot.
- Do not use canned lead-ins or protocol labels.`
	case plannerConversationModeConverse:
		return `Write the next user-facing response.

Planning flow:
0. If the user's message is a terse follow-up, resume request, or reference to earlier discussion
   and the needed context is not already explicit in conversation_history, plan fields, or the
   recovered recent_context fields, invoke recall_recent before claiming the earlier discussion is
   unavailable.
0a. If the request is still too vague or underspecified to start responsible planning,
   invoke academic_research(action=request) instead of plan(action=start). Use that tool
   when the user first needs help shaping the problem, scope, constraints, or success
   criteria. Use ask_user_clarification only for one or two narrow decisions after the
   plan is already mostly understood.
0b. During discussion before planning, use consult_peer(target_agent_type="librarian"),
    consult_peer(target_agent_type="archivalist"), and consult_peer(target_agent_type="academic")
    as new material information arrives. Do not wait until plan(action=start) to gather obvious
    codebase, historical, or Academic evidence. Consult the Librarian for codebase reality and
    local patterns, the Archivalist for precedent and preserved preferences, and the Academic for
    stronger alternatives, best practices, correctness, performance, testing, infrastructure, and
    tradeoffs. On the first substantive implementation, planning, or architecture turn on a new
    problem, start with the most relevant knowledge agent and the narrowest question that can
    materially reduce the next uncertainty. Prefer repeated targeted consult_peer calls over one
    broad omnibus consult. Re-evaluate Academic depth as the user's constraints evolve and your
    own understanding improves: begin with minimal/quick for narrow validation, and escalate only
    when broader corroboration could materially change the decision. Continue consulting as the
    user's constraints or direction materially change.
1. When the user confirms they want to proceed with planning, invoke plan(action=start) with a
   comprehensive query synthesizing all gathered requirements and the consultation evidence you
   accumulated during discussion.
2. After plan(action=start) returns, it gives you a plan_id and protocol instructions. Follow
   those instructions: invoke plan(action=analyze), then any further consult_peer calls needed
   for pre-planning evidence, then plan(action=design), then plan(action=generate_tasks) in
   order, passing the plan_id to each.
3. After generate_tasks completes, the plan reaches Ready and the system
   automatically publishes the Approve / Modify / Reject dialog. The system
   also renders the plan structure separately in the UI — the user already
   sees it. Do NOT repeat, re-render, or include the plan structure, task
   list, acceptance criteria, file lists, or implementation guides in your
   text. Do NOT invoke plan_acceptance — wait for the user's click on the
   dialog buttons.
   Write ONLY a brief assessment (2-4 sentences):
   - Highlight one critical tradeoff or risk.
   - Sound like a principal engineer, not a workflow bot.
4. Invite the user to approve or request changes. Use your own natural phrasing —
   do NOT use a scripted template or repeat the same wording each time.
   Frame it as plan review, not execution kickoff. Do NOT imply that implementation
   is already starting or that their reply will immediately start work in this turn.
   Avoid phrases like "kick it off", "start building", "start implementing",
   "get started", or "ship it".
   Do NOT invoke plan_acceptance — wait for the user's next message.

CRITICAL — Affirmative detection:
If the user's message is an affirmative response to a prior offer to plan (e.g., "yes", "yep",
"go ahead", "do it", "sure", "sounds good", "let's do it"), you MUST invoke plan(action=start)
immediately. Do NOT write text about planning — call the tool. The plan query must synthesize
the full conversation context including the original request from prior turns.

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
  hand the user to the Academic via academic_research(action=request) instead of
  pretending the missing requirements are already known.
- If recent_context_summary or recent_context_focus are present in the context JSON, treat them as
  recovered preserved session context and continue naturally from them instead of claiming the prior
  discussion is missing.
- If the conversation is concrete enough to keep discussing but still lacks codebase,
  historical, or architectural evidence, stay in the Architect and invoke
  consult_peer(target_agent_type=...) against the relevant knowledge agent instead of
  waiting for the formal planning phase.
- If the blocker is missing repository evidence or historical context inside this codebase,
  stay in the Architect and consult_peer the Librarian or Archivalist instead of handing
  the user to the Academic.
- Keep a natural, collaborative tone.
- Do not use canned lead-ins or boilerplate.`
	case plannerConversationModeFeedback:
		return `The user is responding to a plan you previously presented. The
plan_id and plan summary are in the context JSON, along with freshness audit
findings (freshness_summary, freshness_drift_signals, orchestrator_state_hint,
freshness_recommendation) when present.

The system has automatically published the Approve / Modify / Reject dialog
in the input panel. Your job is the chat narrative that surrounds it; do NOT
invoke plan_acceptance and do NOT ask the user to "type yes" — the dialog
handles their verdict.

If the freshness audit fields are populated:
- Surface freshness_summary as your opening line ("Re-checking the plan: ...").
- Quote each entry in freshness_drift_signals as a bullet so the user sees
  what changed since the plan was drafted ("- [WARNING] auth.go no longer
  exists ...").
- If orchestrator_state_hint is non-empty, mention it in plain language so
  the user knows what state the orchestration is in ("The orchestrator
  reports this plan is already running (3/8 nodes done).").
- If freshness_recommendation is "revise" or "replan", say so plainly —
  the user should know the audit thinks this plan needs work, not just
  re-approval.

For any other response (questions about the plan, requests for clarification,
modification suggestions, or general acknowledgement):
- Answer naturally and substantively.
- If the user is asking about the plan structure, focus on what's relevant.
- If the user wants modifications, describe what would change and why; do
  NOT invoke plan_acceptance.

General rules:
- Maintain a collaborative tone — the plan is a proposal, not a decree.
- Do not dump the full plan structure — the dialog is the decision surface,
  the chat is the explanation surface.
- Do not use canned lead-ins or boilerplate.`
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
// instructions reference tool invocations (plan(action=start), plan_acceptance,
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
    Make it explicit that they are reviewing the plan, not starting implementation
    in this turn.
    Do NOT imply that work is already beginning or will begin immediately if they
    reply. Avoid phrases like "kick it off", "start building", "start implementing",
    "get started", or "ship it".
    Do NOT use robotic phrasing like "Do you approve this plan?" or "Please confirm."
  - If false: state that the plan is ready and will proceed.
- Do not use canned lead-ins or protocol labels.
- Do not reference any tools or tool invocations.`
	case plannerConversationModeExistingReady:
		return `The user has a previously prepared ready plan available. The plan_id, prior_query, and plan summary are in the context JSON.

If the user's message clearly accepts or resumes that ready plan:
- Acknowledge that you can continue from that existing plan.
- Briefly state the next step.

If the user is asking about the earlier plan:
- Answer naturally using the recovered plan context.
- Mention a meaningful risk or stale assumption if it matters.

If the user wants changes or a fresh direction:
- Explain whether the existing plan can be revised cleanly or whether a new plan is the better move.
- If details are still missing, ask focused follow-up questions.

General rules:
- Treat the previous plan as available context, not as a commitment.
- Sound like a principal engineer, not a workflow bot.
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
- If recent_context_summary or recent_context_focus are present in the context JSON, treat them as
  recovered preserved context and continue from them instead of claiming the earlier discussion is
  unavailable.
- During substantive implementation, planning, or architecture discussion, reason as if you
  are actively grounding your answer in Librarian, Archivalist, and Academic evidence, using the
  subset most relevant to the current unresolved question. On the first substantive turn for a new
  problem, the tool-enabled path would normally start with the most relevant knowledge agent and
  then continue with targeted follow-up consultations as the uncertainty narrows or the user adds
  new constraints.
- Prefer answers that reflect codebase reality, historical precedent, and stronger architectural
  alternatives instead of defaulting to generic advice.
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

// Phase 1/2.K refactor (docs/PIPELINE_SKILL_REFACTOR.md):
// consult → consult_peer, ask_user_question → ask_user_clarification,
// route_requirements_research → academic_research(action=request),
// start_planning → plan(action=start).
// The four read-side forest skills collapsed into a single `forest(op=…)`
// so the conversation allowlists now reach for `forest` (the planner
// typically wants op=recall_recent, but op=resolve_intent and op=recall
// are harmless reads on the same tool).
var discussionConversationTools = []string{
	"consult_peer",
	"forest",
	"ask_user_clarification",
	"academic_research",
	"plan",
}

var planningConversationTools = []string{
	"consult_peer",
	"forest",
	"ask_user_clarification",
	"academic_research",
	"plan",
}

// toolsForConversationMode returns the allowed tool names for a given mode.
// Conversation modes that can enter or resume planning must include the tools
// needed to finish the planning protocol in the same turn after plan(action=start).
// Feedback/ready modes restrict to chat-only tools.
//
// Note: plan_acceptance is intentionally NOT in any whitelist. The dialog
// publish is unconditional in feedbackReadyDirective so the LLM can't bypass
// it, and the verdict routes from Guardian → architect continuation directly.
// Exposing plan_acceptance to the LLM would let it auto-dispatch the plan
// ahead of the user's click.
func toolsForConversationMode(mode plannerConversationMode) []string {
	switch mode {
	case plannerConversationModeConverse, plannerConversationModeExistingReady, plannerConversationModeClarification:
		return planningConversationTools
	case plannerConversationModeFeedback, plannerConversationModeReady:
		return []string{"ask_user_clarification", "forest"}
	default:
		return planningConversationTools
	}
}

func toolsForConversationModeWithContext(ctx context.Context, mode plannerConversationMode) []string {
	tools := append([]string(nil), toolsForConversationMode(mode)...)
	if architectNeedsGlobalReviewValidateWork(ctx) && !containsConversationTool(tools, "validate_work") {
		tools = append(tools, "validate_work")
	}
	return tools
}

func architectNeedsGlobalReviewValidateWork(ctx context.Context) bool {
	state := shared.GlobalReviewStateFromContext(ctx)
	if state == nil {
		return false
	}
	snapshot := state.Snapshot()
	if snapshot == nil || snapshot.PendingChallenge == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(snapshot.PendingChallenge.TargetAgent), shared.GlobalReviewAgentArchitect)
}

func containsConversationTool(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == strings.TrimSpace(want) {
			return true
		}
	}
	return false
}

func plannerConversationMaxTokensForMode(mode plannerConversationMode, maxTokens int) int {
	switch mode {
	case plannerConversationModeConverse, plannerConversationModeExistingReady, plannerConversationModeReady, plannerConversationModeFeedback:
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
	case plannerConversationModeExistingReady:
		return plannerConversationModeExistingReady
	case plannerConversationModeConverse:
		return plannerConversationModeConverse
	case plannerConversationModeFeedback:
		return plannerConversationModeFeedback
	default:
		return plannerConversationModeClarification
	}
}
