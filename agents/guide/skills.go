package guide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

var ErrPendingNotFound = errors.New("pending request not found")

// isNonGuideHandoff returns true when a forwarded request was routed to a
// non-guide agent, indicating that the self-response tool loop should yield
// control via ErrRerouteRequested.
func isNonGuideHandoff(g *Guide, forwarded *ForwardedRequest) bool {
	if g == nil || forwarded == nil {
		return false
	}
	pending := g.pending.Get(forwarded.CorrelationID)
	return pending != nil && !g.isGuideTarget(pending.TargetAgentID)
}

type skillParams struct {
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
	Input     string `json:"input"`
	Target    string `json:"target"`
	Broadcast string `json:"broadcast"`
}

func (g *Guide) registerCoreSkills() {
	g.skills.Register(routeSkill(g))
	g.skills.Register(clarifySkill(g))
	g.skills.Register(guideRouteSkill(g))
	g.skills.Register(helpSkill(g))
	g.skills.Register(selfDiagnosticSkill(g))
	g.skills.Register(statusSkill(g))
	g.skills.Register(agentsSkill(g))
	g.skills.Register(conversationContextSkill(g))
	g.skills.Register(routeToSkill(g))
	g.skills.Register(replyToSkill(g))
	g.skills.Register(broadcastSkill(g))
	g.skills.Register(taskInteractSkill(g))
	g.skills.Register(routingHistorySkill(g))
	g.skills.Register(agentCapabilitySkill(g))
	g.skills.Register(evaluatePlanAcceptanceSkill(g))
	g.skills.Register(versioning.NewReadWorkspaceFileSkill(func() versioning.WorkspaceViewAccess { return g.workspaceViews }, nil))
	g.skills.Register(versioning.NewWorkspaceGlobSkill(func() versioning.WorkspaceViewAccess { return g.workspaceViews }, nil))
	g.skills.Register(versioning.NewWorkspaceGrepSkill(func() versioning.WorkspaceViewAccess { return g.workspaceViews }, nil))
	g.skills.Register(versioning.NewInspectWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return g.workspaceViews }, nil))
	g.skills.Register(versioning.NewSummarizeWorkspaceStateSkill(func() versioning.WorkspaceViewAccess { return g.workspaceViews }, nil))

	// Activity Fabric: uniform awareness skills + recall. Now safe to
	// wire here because the awareness/recall helpers live in
	// core/fabric — the prior agents/shared ↔ agents/guide cycle that
	// blocked this is gone.
	g.registerFabricSkills()

	// ── Claims skills (unconditional) ──────────────────────────────
	//
	// The Guide is a full conversational agent — it classifies intent,
	// routes, and responds. It needs to see and interact with the
	// session claims board, not just create it. Board resolved via
	// session registry at invocation time (session-scoped, not static).
	boardProvider := func() *claims.ClaimsBoard {
		return claims.DefaultSessionBoardRegistry().Lookup(g.sessionID)
	}
	inboxProvider := func() *claims.ClaimsInbox { return g.claimsInbox }
	g.skills.Register(claims.QueryClaimsBoardSkill(boardProvider))
	g.skills.Register(claims.PostActionSkill(boardProvider, inboxProvider))
	g.skills.Register(claims.SubmitTestamentsSkill(boardProvider))
	g.skills.Register(claims.EvaluateValidationSkill(boardProvider))
	g.skills.Register(claims.UpdateClaimProgressSkill(boardProvider))
	g.skills.Register(claims.InspectClaimConflictsSkill(boardProvider))
	g.skills.Register(claims.TraverseSkill(boardProvider))

	fabricCfg := fabric.AwarenessSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      func() string { return strings.TrimSpace(g.sessionID) },
		AgentID:        func() string { return "guide" },
		AgentType:      func() string { return "guide" },
	}
	for _, skill := range fabric.ClaimsAwarenessSkills(fabricCfg) {
		g.skills.Register(skill)
	}
}

// registerFabricSkills wires the guide into the Activity Fabric so it
// can introspect peer activity, surface causal traces, and recall its
// own prior routing decisions across turns. Kept in a separate method
// so the wiring stays self-contained and easy to read.
func (g *Guide) registerFabricSkills() {
	sessionID := func() string { return strings.TrimSpace(g.sessionID) }
	agentID := func() string { return "guide" }
	agentType := func() string { return "guide" }

	for _, skill := range fabric.AwarenessSkills(fabric.AwarenessSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      sessionID,
		AgentID:        agentID,
		AgentType:      agentType,
	}) {
		g.skills.Register(skill)
	}
	for _, skill := range fabric.RecallSkills(fabric.RecallSkillConfig{
		SourceProvider: activity.DefaultSource,
		SessionID:      sessionID,
		AgentID:        agentID,
		AgentType:      agentType,
	}) {
		g.skills.Register(skill)
	}
}

func (g *Guide) registerExtendedSkills() {
	g.skills.Register(sessionsSkill(g))
	g.skills.Register(metricsSkill(g))
	g.skills.Register(switchSessionSkill(g))
	g.skills.Register(createSessionSkill(g))
	g.skills.Register(closeSessionSkill(g))
}

func routeSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("route").
		Description("Routes a validated request to a specific agent based on intent and domain. Must only be used when the request is clear and actionable.").
		Domain("routing").
		Keywords("route", "dispatch", "send").
		Priority(100).
		Usage("Use this skill ONLY when the user's request is unambiguous, contains a clear 'done' criteria, and perfectly matches the capabilities of a known agent. If the request spans multiple domains or is vague, DO NOT use this skill; use `clarify` or route to the `architect`.").
		StringParam("input", "The exact payload or user prompt to route.", true).
		StringParam("target", "The target agent ID (e.g., 'librarian', 'engineer', 'architect').", true).
		StringParam("intent", "The classified intent (e.g., 'find', 'store', 'plan').", true).
		StringParam("domain", "The classified domain (e.g., 'code', 'design', 'tasks').", true).
		StringParam("correlation_id", "Used to resume a previously suspended request chain.", false).
		Example("{\n  \"input\": \"Where is the authentication middleware defined?\",\n  \"target\": \"librarian\",\n  \"intent\": \"find\",\n  \"domain\": \"code\"\n}").
		BestPractice("Never guess the target: If multiple agents overlap (e.g., 'tester' vs 'inspector'), use `clarify` instead.").
		BestPractice("Compound Tasks: If the input is 'Find the bug and fix it', the target MUST be `architect` with intent `plan`. Do not attempt to route to the engineer directly.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Input         string `json:"input"`
				Target        string `json:"target"`
				Intent        string `json:"intent"`
				Domain        string `json:"domain"`
				SessionID     string `json:"session_id"`
				CorrelationID string `json:"correlation_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			sessionID := params.SessionID
			if sessionID == "" {
				sessionID = g.sessionID
			}
			request := &RouteRequest{
				Input:         params.Input,
				TargetAgentID: params.Target,
				CorrelationID: params.CorrelationID,
				SourceAgentID: g.agentID,
				SessionID:     sessionID,
			}
			forwarded, err := g.RouteAndForward(ctx, request)
			if err != nil {
				return nil, err
			}
			if isNonGuideHandoff(g, forwarded) {
				return forwarded, skills.ErrRerouteRequested
			}
			return forwarded, nil
		}).
		Build()
}

func clarifySkill(g *Guide) *skills.Skill {
	return skills.NewSkill("clarify").
		Description("Halts the routing process and asks the user for required context to resolve an ambiguous or multi-agent overlapping request.").
		Domain("routing").
		Keywords("clarify", "ask", "question").
		Priority(90).
		Usage("Invoke this skill when a user's prompt lacks sufficient detail to safely route, or when the request evenly overlaps the responsibilities of two or more agents.").
		StringParam("question", "The exact, polite question to present to the user.", true).
		ArrayParam("signals", "The detected ambiguity signals (e.g., ['unbounded_scope', 'agent_overlap', 'missing_context']).", "string", true).
		ArrayParam("candidate_agents", "If the issue is overlap, list the conflicting agents here.", "string", false).
		Example("{\n  \"question\": \"Your request could be handled by the Inspector or the Tester. Which would you prefer to focus on?\",\n  \"signals\": [\"agent_overlap\"],\n  \"candidate_agents\": [\"inspector\", \"tester\"]\n}").
		BestPractice("Be specific: Do not just say 'Please clarify.' Explain *why* the request cannot be routed and what specific detail is missing.").
		BestPractice("Provide options: When possible, give the user multiple-choice options based on the `candidate_agents`.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return map[string]any{"status": "clarification_requested", "input": input}, nil
		}).
		Build()
}

func taskInteractSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("task_interact").
		Description("Dispatches a complex, multi-step pipeline task to the Orchestrator for background execution.").
		Domain("tasks").
		Keywords("task", "pipeline", "orchestrator").
		Priority(80).
		Usage("Use this skill when the user explicitly requests an automated, multi-step execution pipeline, or uses the `/task` command modifier.").
		StringParam("task_description", "The high-level goal of the pipeline task.", true).
		BoolParam("bypass_tests", "If true, instructs the pipeline to skip the Tester agent phase.", false).
		BoolParam("bypass_inspector", "If true, instructs the pipeline to skip the Inspector agent phase.", false).
		Example("{\n  \"task_description\": \"Implement the new Redis caching layer for the user service\",\n  \"bypass_tests\": false\n}").
		BestPractice("Scope: Ensure the `task_description` is a high-level objective. The Architect will handle the granular breakdown.").
		BestPractice("Safety: Only set bypass flags to `true` if the user explicitly requested it (e.g., `/task --no-tests`).").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				TaskDescription string `json:"task_description"`
				BypassTests     bool   `json:"bypass_tests"`
				BypassInspector bool   `json:"bypass_inspector"`
				SessionID       string `json:"session_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			sessionID := params.SessionID
			if sessionID == "" {
				sessionID = g.sessionID
			}

			request := &RouteRequest{
				Input:         params.TaskDescription,
				TargetAgentID: "orchestrator",
				SourceAgentID: g.agentID,
				SessionID:     sessionID,
			}
			forwarded, err := g.RouteAndForward(ctx, request)
			if err != nil {
				return nil, err
			}
			if isNonGuideHandoff(g, forwarded) {
				return forwarded, skills.ErrRerouteRequested
			}
			return forwarded, nil
		}).
		Build()
}

func routingHistorySkill(g *Guide) *skills.Skill {
	return skills.NewSkill("get_routing_history").
		Description("Fetch past routing decisions and learned preferences for similar queries from the Archivalist.").
		Domain("routing").
		Keywords("history", "past", "previous").
		Priority(70).
		Usage("Use this skill BEFORE falling back to `clarify` if you suspect the user has established a personal convention or preference for certain types of phrasing.").
		StringParam("query", "The current user query to match against historical records.", true).
		IntParam("limit", "Maximum number of historical routing examples to return.", false).
		Example("{\n  \"query\": \"fix the db\",\n  \"limit\": 3\n}").
		BestPractice("Use as a tie-breaker: If two agents overlap, check routing history to see if the user historically prefers one over the other for this phrasing.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return map[string]any{"status": "history_requested", "query": input}, nil
		}).
		Build()
}

func agentCapabilitySkill(g *Guide) *skills.Skill {
	return skills.NewSkill("get_agent_capabilities").
		Description("Retrieves the declared capabilities, domains, and health status of registered agents.").
		Domain("agents").
		Keywords("capabilities", "can", "what").
		Priority(50).
		Usage("Use this skill if a user asks a meta-question about what the system can do, or if you need to verify an agent's constraints before routing.").
		StringParam("agent_id", "The ID of the agent (e.g., 'librarian'). If omitted, returns all agents.", false).
		BoolParam("include_health", "Whether to include active circuit breaker / health status.", false).
		StringParam("format", "Response format: 'text' (default) or 'json'.", false).
		Example("{\n  \"agent_id\": \"academic\",\n  \"include_health\": true\n}").
		BestPractice("Use include_health=true only when diagnosing routing failures — health checks add latency to the response.").
		BestPractice("When the user asks 'can Sylk do X?', query all agents (omit agent_id) and filter by relevant intents/domains.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				AgentID       string `json:"agent_id"`
				IncludeHealth bool   `json:"include_health"`
				Format        string `json:"format"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			if params.AgentID != "" {
				agent := g.registry.Get(params.AgentID)
				if agent == nil {
					return nil, fmt.Errorf("agent not found")
				}
				if isJSONSkillFormat(params.Format) {
					return agent.Capabilities, nil
				}
				return describeAgentCapabilities(agent), nil
			}
			all := g.registry.GetAll()
			if isJSONSkillFormat(params.Format) {
				return all, nil
			}
			return describeAllAgentCapabilities(all), nil
		}).
		Build()
}

func guideRouteSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("guide_route").
		Description("Route input explicitly through the Guide for classification.").
		Domain("routing").
		Keywords("guide", "route").
		Priority(90).
		StringParam("input", "Input to route", true).
		StringParam("session_id", "Session id", false).
		Usage("Use when input needs full Guide classification before routing. This re-enters the Guide's classifier pipeline — useful when an agent returns a response that itself needs routing, or when the user's intent was initially misclassified. Do NOT use for direct agent-targeted requests — use `route_to` instead.").
		Example("{\"input\": \"Can you also run the linter on that file?\", \"session_id\": \"sess_abc\"}").
		BestPractice("Prefer `route` over `guide_route` for new user requests — `guide_route` is for re-classification of intermediate results.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params := skillParams{}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			request := &RouteRequest{
				Input:         params.Input,
				SourceAgentID: g.agentID,
				SessionID:     params.SessionID,
			}
			forwarded, err := g.RouteAndForward(ctx, request)
			if err != nil {
				return nil, err
			}
			if isNonGuideHandoff(g, forwarded) {
				return forwarded, skills.ErrRerouteRequested
			}
			return forwarded, nil
		}).
		Build()
}

func helpSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("help").
		Description("Provide available routing help and usage details.").
		Domain("routing").
		Keywords("help", "usage").
		Priority(80).
		StringParam("format", "Response format: 'text' (default) or 'json'.", false).
		Usage("Use when the user asks what the Guide can do, what agents are available, or how to interact with the system. Returns a human-readable overview in text mode, or structured skill definitions in JSON mode.").
		Example("{\"format\": \"json\"}").
		BestPractice("Default to text format unless the caller explicitly needs structured data for programmatic consumption.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Format string `json:"format"`
			}
			if len(input) > 0 {
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, err
				}
			}
			if isJSONSkillFormat(params.Format) {
				return map[string]any{
					"skills": g.skills.ToToolDefinitions(g.skills.GetAll()),
				}, nil
			}
			return "Guide can answer Sylk meta questions and route work to specialist agents. Try '@guide agents', '@guide status', ask naturally, or ask which agent is currently active.", nil
		}).
		Build()
}

func selfDiagnosticSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("self_diagnostic").
		Description("Diagnose recent guide failures and explain what happened. Use when the user says \"you hit an error\", \"you crashed\", or \"you seem stuck\".").
		Domain("diagnostics").
		Keywords("diagnostic", "crashed", "error", "stuck", "failed", "debug").
		Priority(95).
		StringParam("query", "Original user wording that prompted diagnostics.", false).
		StringParam("session_id", "Session id for conversation context.", false).
		StringParam("format", "Response format: 'text' (default) or 'json'.", false).
		Usage("Use when the user reports an error, says the system crashed, seems stuck, or asks what went wrong. Builds a diagnostic report from recent routing failures, classifier errors, and conversation state. Do NOT use for general status queries — use `status` instead.").
		Example("{\"query\": \"why did my last request fail?\", \"session_id\": \"sess_abc\"}").
		BestPractice("Always include the user's original wording in the query param — it provides context for the diagnostic report.").
		BestPractice("If the diagnostic report shows no recent errors, suggest the user check agent-specific diagnostics via the target agent's self_diagnostic skill.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Query     string `json:"query"`
				SessionID string `json:"session_id"`
				Format    string `json:"format"`
			}
			if len(input) > 0 {
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, err
				}
			}
			sessionID := resolveGuideSkillSessionID(g, params.SessionID)
			report := buildGuideDiagnosticReport(g, params.Query, sessionID)
			if isJSONSkillFormat(params.Format) {
				return report, nil
			}
			return formatGuideDiagnosticReport(report), nil
		}).
		Build()
}

func statusSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("status").
		Description("Return routing system status including pending counts.").
		Domain("routing").
		Keywords("status", "health").
		Priority(80).
		StringParam("session_id", "Session id for conversation status.", false).
		StringParam("format", "Response format: 'text' (default) or 'json'.", false).
		Usage("Use when the user asks about system health, pending request counts, or the active conversation state. Returns a point-in-time snapshot of the routing system. For per-agent detail, use `get_agent_capabilities` instead.").
		Example("{\"session_id\": \"sess_abc\", \"format\": \"text\"}").
		BestPractice("Check pending count — if non-zero, there are unresolved routing requests that may need attention.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				SessionID string `json:"session_id"`
				Format    string `json:"format"`
			}
			if len(input) > 0 {
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, err
				}
			}
			sessionID := resolveGuideSkillSessionID(g, params.SessionID)
			request := g.newGuideSelfResponseRequest("status", sessionID)
			if isJSONSkillFormat(params.Format) {
				return map[string]any{
					"pending":                    request.PendingRequests,
					"registered_agents":          len(request.RegisteredAgentIDs),
					"active_conversation_agent":  request.ActiveConversationAgent,
					"active_conversation_turns":  request.ActiveConversationTurns,
					"active_conversation_age":    request.ActiveConversationAge,
					"active_conversation_signal": request.ActiveConversationScore,
				}, nil
			}
			return staticGuideStatusReply(request), nil
		}).
		Build()
}

func agentsSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("agents").
		Description("List registered agents and their status.").
		Domain("routing").
		Keywords("agents", "registry").
		Priority(80).
		StringParam("format", "Response format: 'text' (default) or 'json'.", false).
		Usage("Use when the user asks which agents exist, what the system can do, or needs an overview of registered capabilities. Returns agent IDs and their status. For detailed per-agent capabilities, use `get_agent_capabilities` with a specific agent_id.").
		Example("{\"format\": \"json\"}").
		BestPractice("Use text format for user-facing responses and JSON format when the data feeds into another skill's decision logic.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Format string `json:"format"`
			}
			if len(input) > 0 {
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, err
				}
			}
			if isJSONSkillFormat(params.Format) {
				return g.registry.GetAll(), nil
			}
			return staticGuideAgentsReply(g.registeredAgentIDs()), nil
		}).
		Build()
}

func conversationContextSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("conversation_context").
		Description("Return active conversation context and follow-up weighting for a session.").
		Domain("routing").
		Keywords("conversation", "context", "follow-up", "session", "active agent").
		Priority(78).
		StringParam("session_id", "Session id (defaults to current guide session).", false).
		StringParam("format", "Response format: 'text' (default) or 'json'.", false).
		Usage("Use when the classifier needs to determine if a new message is a follow-up to an active conversation, or when debugging why a message was routed to a particular agent. Returns the active agent, turn count, age, and activation score for the session. Do NOT use to list all sessions — use `sessions` instead.").
		Example("{\"session_id\": \"sess_abc\", \"format\": \"json\"}").
		BestPractice("A high activation_score (>0.7) strongly suggests the message is a follow-up — route to the active agent rather than re-classifying.").
		BestPractice("If the conversation age exceeds 5 minutes with no recent turns, treat the context as stale and re-classify the message.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				SessionID string `json:"session_id"`
				Format    string `json:"format"`
			}
			if len(input) > 0 {
				if err := json.Unmarshal(input, &params); err != nil {
					return nil, err
				}
			}
			sessionID := resolveGuideSkillSessionID(g, params.SessionID)
			if g == nil || g.conversation == nil {
				if isJSONSkillFormat(params.Format) {
					return map[string]any{"session_id": sessionID, "active": false}, nil
				}
				return "Conversation context is unavailable.", nil
			}
			snapshot, ok := g.conversation.Snapshot(sessionID)
			if !ok {
				if isJSONSkillFormat(params.Format) {
					return map[string]any{"session_id": sessionID, "active": false}, nil
				}
				return "No active conversation context for this session.", nil
			}
			if isJSONSkillFormat(params.Format) {
				return map[string]any{
					"session_id":       sessionID,
					"active":           true,
					"active_agent":     snapshot.ActiveAgentID,
					"turns":            snapshot.Turns,
					"age_seconds":      int(snapshot.Age.Seconds()),
					"activation_score": snapshot.ActivationHint,
				}, nil
			}
			return staticGuideConversationReply(GuideSelfResponseRequest{
				ActiveConversationAgent: snapshot.ActiveAgentID,
				ActiveConversationTurns: snapshot.Turns,
				ActiveConversationAge:   int(snapshot.Age.Seconds()),
			}), nil
		}).
		Build()
}

func routeToSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("route_to").
		Description("Route input to a specific agent id.").
		Domain("routing").
		Keywords("route", "target").
		Priority(90).
		StringParam("target", "Target agent id", true).
		StringParam("input", "Input to route", true).
		StringParam("session_id", "Session id", false).
		Usage("Use when the target agent is already known and no classification is needed. This bypasses the Guide's classifier entirely and routes directly. Use `route` (with intent/domain) for classified routing, or `guide_route` for re-classification.").
		Example("{\"target\": \"engineer\", \"input\": \"Implement the Redis caching layer\", \"session_id\": \"sess_abc\"}").
		BestPractice("Verify the target agent is registered before calling — an unknown target will fail at the router level.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params := skillParams{}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			request := &RouteRequest{
				Input:         params.Input,
				TargetAgentID: params.Target,
				SourceAgentID: g.agentID,
				SessionID:     resolveGuideSkillSessionID(g, params.SessionID),
			}
			forwarded, err := g.RouteAndForward(ctx, request)
			if err != nil {
				return nil, err
			}
			if isNonGuideHandoff(g, forwarded) {
				return forwarded, skills.ErrRerouteRequested
			}
			return forwarded, nil
		}).
		Build()
}

func replyToSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("reply_to").
		Description("Create a response to a pending request.").
		Domain("routing").
		Keywords("reply", "respond").
		Priority(80).
		StringParam("correlation_id", "Correlation id", true).
		StringParam("data", "Response data", false).
		Usage("Use to complete a pending request/response cycle identified by a correlation_id. Typically used when a clarification response arrives from the user or when an agent produces a result that fulfills a pending route. Do NOT use for new requests — use `route` or `route_to` instead.").
		Example("{\"correlation_id\": \"corr_abc123\", \"data\": \"The user chose the Inspector agent.\"}").
		BestPractice("Always verify the correlation_id corresponds to an active pending request — expired or unknown IDs will return ErrPendingNotFound.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				CorrelationID string `json:"correlation_id"`
				Data          string `json:"data"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			pending := g.pending.Get(params.CorrelationID)
			if pending == nil {
				return nil, ErrPendingNotFound
			}

			return &RouteResponse{
				CorrelationID:     params.CorrelationID,
				Success:           true,
				Data:              params.Data,
				RespondingAgentID: g.agentID,
			}, nil
		}).
		Build()
}

func broadcastSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("broadcast").
		Description("Broadcast a message to all agents.").
		Domain("routing").
		Keywords("broadcast", "announce").
		Priority(70).
		StringParam("message", "Message to broadcast", true).
		Usage("Use for system-wide announcements that all agents should receive — such as session lifecycle events, global status changes, or coordinator directives. This is fire-and-forget. Do NOT use for targeted communication — use `route_to` instead.").
		Example("{\"message\": \"Session ending — all agents should flush pending work.\"}").
		BestPractice("Keep broadcast messages concise and actionable — all registered agents receive them, so avoid noise.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			request := &RouteRequest{
				Input:           params.Message,
				SourceAgentID:   g.agentID,
				SourceAgentName: g.agentID,
				FireAndForget:   true,
			}
			msg := NewRequestMessage(generateMessageID(), request)
			if err := g.bus.Publish(TopicGuideRequests, msg); err != nil {
				return nil, err
			}
			return true, nil
		}).
		Build()
}

func sessionsSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("sessions").
		Description("List active sessions.").
		Domain("routing").
		Keywords("sessions", "list").
		Priority(60).
		Usage("Use when the user asks about active sessions or when another skill needs to enumerate available session contexts. Currently returns the Guide's own session ID.").
		Example("{}").
		BestPractice("The session list reflects the Guide's perspective — individual agents may track their own session state independently.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return []string{g.sessionID}, nil
		}).
		Build()
}

func metricsSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("metrics").
		Description("Return routing metrics.").
		Domain("routing").
		Keywords("metrics", "stats").
		Priority(60).
		Usage("Use when investigating routing performance, pending request backlogs, or overall Guide throughput. Returns aggregate statistics from the pending request store.").
		Example("{}").
		BestPractice("Cross-reference metrics with `status` output for a complete picture — metrics focuses on routing internals while status shows conversation state.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return map[string]any{
				"pending": g.pending.Stats(),
			}, nil
		}).
		Build()
}

func switchSessionSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("switch_session").
		Description("Switch Guide session context.").
		Domain("routing").
		Keywords("switch", "session").
		Priority(60).
		StringParam("session_id", "Session id", true).
		Usage("Use when the user explicitly switches to a different session context. Updates the Guide's active session ID, which affects all subsequent routing, conversation tracking, and status queries. Do NOT switch sessions implicitly — only when the user requests it.").
		Example("{\"session_id\": \"sess_new_feature\"}").
		BestPractice("Switching sessions does not close the previous session — use `close_session` first if teardown is needed.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params := skillParams{}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			g.sessionID = params.SessionID
			return true, nil
		}).
		Build()
}

func createSessionSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("create_session").
		Description("Create a new session identifier.").
		Domain("routing").
		Keywords("create", "session").
		Priority(60).
		StringParam("session_id", "Session id", false).
		Usage("Use when the user explicitly requests a new session context. If no session_id is provided, the Guide's current session ID is returned as the created session. Do NOT create sessions for every new message — sessions are long-lived conversation contexts.").
		Example("{\"session_id\": \"sess_feature_auth\"}").
		BestPractice("Session IDs should be descriptive and stable — avoid UUIDs for user-created sessions.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params := skillParams{}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			if params.SessionID == "" {
				params.SessionID = g.sessionID
			}
			return map[string]string{"session_id": params.SessionID}, nil
		}).
		Build()
}

func closeSessionSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("close_session").
		Description("Close the current session context.").
		Domain("routing").
		Keywords("close", "session").
		Priority(60).
		Usage("Use when the user explicitly ends a session or when session cleanup is needed. Clears the Guide's active session ID. Do NOT close sessions that other agents may still reference — broadcast a session-end event first.").
		Example("{}").
		BestPractice("After closing, subsequent routing calls will use an empty session ID — ensure a new session is created or switched to before continuing.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			g.sessionID = ""
			return true, nil
		}).
		Build()
}

func resolveGuideSkillSessionID(g *Guide, candidate string) string {
	sessionID := strings.TrimSpace(candidate)
	if sessionID != "" {
		return sessionID
	}
	if g == nil {
		return ""
	}
	return g.sessionID
}

func isJSONSkillFormat(format string) bool {
	return strings.EqualFold(strings.TrimSpace(format), "json")
}

func describeAgentCapabilities(agent *AgentRegistration) string {
	if agent == nil {
		return "Agent information is unavailable."
	}
	return fmt.Sprintf(
		"Agent %s supports intents: %s. Domains: %s.",
		agent.ID,
		intentList(agent.Capabilities.Intents),
		domainList(agent.Capabilities.Domains),
	)
}

func describeAllAgentCapabilities(registrations []*AgentRegistration) string {
	if len(registrations) == 0 {
		return "No agents are currently registered."
	}
	lines := make([]string, 0, len(registrations))
	for _, reg := range registrations {
		lines = append(lines, describeAgentCapabilities(reg))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func intentList(intents []Intent) string {
	if len(intents) == 0 {
		return "all"
	}
	values := make([]string, 0, len(intents))
	for _, intent := range intents {
		values = append(values, string(intent))
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}

func domainList(domains []Domain) string {
	if len(domains) == 0 {
		return "all"
	}
	values := make([]string, 0, len(domains))
	for _, domain := range domains {
		values = append(values, string(domain))
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}
