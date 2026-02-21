package guide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/adalundhe/sylk/core/skills"
)

var ErrPendingNotFound = errors.New("pending request not found")

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
	g.skills.Register(statusSkill(g))
	g.skills.Register(agentsSkill(g))
	g.skills.Register(routeToSkill(g))
	g.skills.Register(replyToSkill(g))
	g.skills.Register(broadcastSkill(g))
	g.skills.Register(taskInteractSkill(g))
	g.skills.Register(routingHistorySkill(g))
	g.skills.Register(agentCapabilitySkill(g))

	g.skills.Load("route")
	g.skills.Load("clarify")
	g.skills.Load("guide_route")
	g.skills.Load("help")
	g.skills.Load("status")
	g.skills.Load("agents")
	g.skills.Load("route_to")
	g.skills.Load("reply_to")
	g.skills.Load("broadcast")
	g.skills.Load("task_interact")
	g.skills.Load("get_routing_history")
	g.skills.Load("get_agent_capabilities")
}

func (g *Guide) registerExtendedSkills() {
	g.skills.Register(sessionsSkill(g))
	g.skills.Register(metricsSkill(g))
	g.skills.Register(switchSessionSkill(g))
	g.skills.Register(createSessionSkill(g))
	g.skills.Register(closeSessionSkill(g))

	g.skills.Load("sessions")
	g.skills.Load("metrics")
	g.skills.Load("switch_session")
	g.skills.Load("create_session")
	g.skills.Load("close_session")
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
				CorrelationID string `json:"correlation_id"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			request := &RouteRequest{
				Input:         params.Input,
				TargetAgentID: params.Target,
				CorrelationID: params.CorrelationID,
				SessionID:     g.sessionID,
			}
			return g.Route(ctx, request)
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
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			request := &RouteRequest{
				Input:         params.TaskDescription,
				TargetAgentID: "orchestrator",
				SessionID:     g.sessionID,
			}
			return g.Route(ctx, request)
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
		Example("{\n  \"agent_id\": \"academic\",\n  \"include_health\": true\n}").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				AgentID       string `json:"agent_id"`
				IncludeHealth bool   `json:"include_health"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			if params.AgentID != "" {
				agent := g.registry.Get(params.AgentID)
				if agent == nil {
					return nil, fmt.Errorf("agent not found")
				}
				return agent.Capabilities, nil
			}
			return g.registry.GetAll(), nil
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
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params := skillParams{}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			request := &RouteRequest{
				Input:     params.Input,
				SessionID: params.SessionID,
			}
			return g.Route(ctx, request)
		}).
		Build()
}

func helpSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("help").
		Description("Provide available routing help and usage details.").
		Domain("routing").
		Keywords("help", "usage").
		Priority(80).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return map[string]any{
				"skills": g.skills.GetToolDefinitions(),
			}, nil
		}).
		Build()
}

func statusSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("status").
		Description("Return routing system status including pending counts.").
		Domain("routing").
		Keywords("status", "health").
		Priority(80).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return map[string]any{
				"pending": g.pending.Count(),
			}, nil
		}).
		Build()
}

func agentsSkill(g *Guide) *skills.Skill {
	return skills.NewSkill("agents").
		Description("List registered agents and their status.").
		Domain("routing").
		Keywords("agents", "registry").
		Priority(80).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return g.registry.GetAll(), nil
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
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params := skillParams{}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			request := &RouteRequest{
				Input:         params.Input,
				TargetAgentID: params.Target,
			}
			return g.Route(ctx, request)
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
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			g.sessionID = ""
			return true, nil
		}).
		Build()
}
