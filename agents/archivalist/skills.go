package archivalist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
)

var ErrWorkflowStoreUnavailable = errors.New("workflow store not available")

func generateMessageID() string {
	return fmt.Sprintf("archivalist_msg_%d", time.Now().UnixNano())
}

type archivalistSkillParams struct {
	Input   string `json:"input"`
	Target  string `json:"target"`
	Message string `json:"message"`
}

func routeToSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("route_to").
		Description("Route input to a specific agent id.").
		Domain("routing").
		Keywords("route", "target").
		Priority(90).
		StringParam("target", "Target agent id", true).
		StringParam("input", "Input to route", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			params := archivalistSkillParams{}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			request := &guide.RouteRequest{
				Input:         params.Input,
				TargetAgentID: params.Target,
			}
			return true, a.PublishRequest(request)
		}).
		Build()
}

func replyToSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("reply_to").
		Description("Reply to a pending request.").
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

			response := &guide.RouteResponse{
				CorrelationID:     params.CorrelationID,
				Success:           true,
				Data:              params.Data,
				RespondingAgentID: "archivalist",
			}
			msg := guide.NewResponseMessage(generateMessageID(), response)
			return true, a.bus.Publish(guide.TopicGuideResponses, msg)
		}).
		Build()
}

func crossSessionQuerySkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("cross_session_query").
		Description("Query across sessions for stored information.").
		Domain("chronicle").
		Keywords("cross", "session", "history", "global").
		Priority(60).
		StringParam("search", "Text to search for", false).
		IntParam("limit", "Maximum number of results", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			if a.crossSession == nil {
				return nil, ErrWorkflowStoreUnavailable
			}
			var params struct {
				Search string `json:"search"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			query := ArchiveQuery{
				SearchText: params.Search,
				Limit:      params.Limit,
			}
			return a.crossSession.QueryCrossSession(query)
		}).
		Build()
}

func workflowHistorySkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("workflow_history").
		Description("Query workflow history for this session.").
		Domain("chronicle").
		Keywords("workflow", "history", "dag").
		Priority(60).
		IntParam("limit", "Maximum number of results", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			if a.workflowStore == nil {
				return nil, ErrWorkflowStoreUnavailable
			}
			var params struct {
				Limit int `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			query := WorkflowQuery{SessionID: a.defaultSessionID, Limit: params.Limit}
			return a.workflowStore.GetSessionHistory(ctx, query)
		}).
		Build()
}

func tokenSavingsSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("token_savings").
		Description("Get token savings report for this session.").
		Domain("memory").
		Keywords("token", "savings", "cache").
		Priority(50).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			return a.GetTokenSavings(a.defaultSessionID), nil
		}).
		Build()
}

func sessionTimelineSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("session_timeline").
		Description("Get session timeline events.").
		Domain("memory").
		Keywords("session", "timeline", "events").
		Priority(50).
		IntParam("limit", "Maximum number of events", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Limit int `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}
			if a.crossSession == nil {
				return nil, ErrWorkflowStoreUnavailable
			}
			return a.crossSession.GetSessionHistory(a.defaultSessionID, params.Limit), nil
		}).
		Build()
}

func patternSearchSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("pattern_search").
		Description("Search stored patterns across sessions.").
		Domain("memory").
		Keywords("pattern", "style", "convention").
		Priority(50).
		StringParam("search", "Text to search for", true).
		IntParam("limit", "Maximum number of results", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Search string `json:"search"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			query := ArchiveQuery{
				SearchText: params.Search,
				Categories: []Category{CategoryCodeStyle},
				Limit:      params.Limit,
			}
			return a.Query(ctx, query)
		}).
		Build()
}

func failureSearchSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("failure_search").
		Description("Search stored failures across sessions.").
		Domain("memory").
		Keywords("failure", "error", "incident").
		Priority(50).
		StringParam("search", "Text to search for", true).
		IntParam("limit", "Maximum number of results", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Search string `json:"search"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			query := ArchiveQuery{
				SearchText: params.Search,
				Categories: []Category{CategoryIssue},
				Limit:      params.Limit,
			}
			return a.Query(ctx, query)
		}).
		Build()
}

func decisionSearchSkill(a *Archivalist) *skills.Skill {
	return skills.NewSkill("decision_search").
		Description("Search stored decisions across sessions.").
		Domain("memory").
		Keywords("decision", "choice", "rationale").
		Priority(50).
		StringParam("search", "Text to search for", true).
		IntParam("limit", "Maximum number of results", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Search string `json:"search"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, err
			}

			query := ArchiveQuery{
				SearchText: params.Search,
				Categories: []Category{CategoryDecision},
				Limit:      params.Limit,
			}
			return a.Query(ctx, query)
		}).
		Build()
}
