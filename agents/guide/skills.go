package guide

import (
	"context"
	"encoding/json"
	"errors"

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
	g.skills.Register(guideRouteSkill(g))
	g.skills.Register(helpSkill(g))
	g.skills.Register(statusSkill(g))
	g.skills.Register(agentsSkill(g))
	g.skills.Register(routeToSkill(g))
	g.skills.Register(replyToSkill(g))
	g.skills.Register(broadcastSkill(g))

	g.skills.Load("route")
	g.skills.Load("guide_route")
	g.skills.Load("help")
	g.skills.Load("status")
	g.skills.Load("agents")
	g.skills.Load("route_to")
	g.skills.Load("reply_to")
	g.skills.Load("broadcast")
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
		Description("Route input to the appropriate agent based on intent and domain.").
		Domain("routing").
		Keywords("route", "dispatch", "send").
		Priority(100).
		StringParam("input", "Input to route", true).
		StringParam("source_agent_id", "Source agent id", false).
		StringParam("session_id", "Session id", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params := skillParams{}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			request := &RouteRequest{
				Input:         params.Input,
				SourceAgentID: params.AgentID,
				SessionID:     params.SessionID,
			}
			return g.Route(ctx, request)
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
