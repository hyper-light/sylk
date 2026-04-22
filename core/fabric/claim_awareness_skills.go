package fabric

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/skills"
)

// ClaimsAwarenessSkills returns the claims-specific Fabric awareness
// skills. These query the Fabric activity stream for claim/testament
// activities — they complement the direct board query skills in
// core/claims/skills.go by providing cross-pipeline visibility through
// the Fabric lens.
func ClaimsAwarenessSkills(cfg AwarenessSkillConfig) []*skills.Skill {
	return []*skills.Skill{
		fabricQueryClaimsBoardSkill(cfg),
		fabricQueryPeerClaimsSkill(cfg),
		fabricInspectClaimConflictsSkill(cfg),
	}
}

func fabricQueryClaimsBoardSkill(cfg AwarenessSkillConfig) *skills.Skill {
	return skills.NewSkill("fabric_query_claims_board").
		Description("Query the Fabric activity stream for claims board state across all pipelines. Returns recent claim, testament, and validation activities. Use for cross-pipeline visibility — for your own board, prefer the direct query_claims_board skill.").
		Domain("claims").
		Keywords("claims", "board", "fabric", "cross-pipeline").
		Priority(93).
		StringParam("scope", "Optional path prefix to filter by", false).
		StringParam("task_id", "Optional task ID to filter by", false).
		IntParam("since_minutes", "How far back to look (default 10)", false).
		IntParam("limit", "Max results per category (default 10)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			src := cfg.source()
			if src == nil {
				return map[string]any{"claims": []any{}, "testaments": []any{}, "note": "Fabric source not available"}, nil
			}
			var params struct {
				Scope        string `json:"scope"`
				TaskID       string `json:"task_id"`
				SinceMinutes int    `json:"since_minutes"`
				Limit        int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			sinceMinutes := params.SinceMinutes
			if sinceMinutes <= 0 {
				sinceMinutes = 10
			}
			limit := params.Limit
			if limit <= 0 {
				limit = 10
			}
			since := time.Now().Add(-time.Duration(sinceMinutes) * time.Minute)

			filter := activity.QueryFilter{
				SessionID: activity.SessionID(cfg.SessionID()),
				ActionKinds: []activity.ActionKind{
					activity.ActionClaimIssued,
					activity.ActionClaimUpdated,
					activity.ActionTestamentSubmitted,
					activity.ActionClaimAccepted,
					activity.ActionClaimRejected,
				},
				Since: since,
				Limit: limit,
			}
			if scope := strings.TrimSpace(params.Scope); scope != "" {
				filter.SubjectPathPrefix = scope
			}

			activities, err := src.FilterActivities(ctx, filter)
			if err != nil {
				return nil, err
			}

			// Filter by task_id if provided (via Subject.Coordinates).
			taskID := strings.TrimSpace(params.TaskID)
			var filtered []activity.AgentActivity
			for _, a := range activities {
				if taskID != "" {
					if a.Subject.Coordinates == nil || a.Subject.Coordinates["task_id"] != taskID {
						continue
					}
				}
				filtered = append(filtered, a)
			}

			return map[string]any{
				"activities": filtered,
				"count":      len(filtered),
				"scope":      params.Scope,
				"task_id":    taskID,
			}, nil
		}).
		Build()
}

func fabricQueryPeerClaimsSkill(cfg AwarenessSkillConfig) *skills.Skill {
	return skills.NewSkill("fabric_query_peer_claims").
		Description("Query a specific peer's claims and testaments from the Fabric activity stream. Returns that peer's recent claim activities for cross-pipeline awareness.").
		Domain("claims").
		Keywords("peer", "claims", "fabric").
		Priority(90).
		StringParam("target_agent_type", "The agent type to query", true).
		StringParam("scope", "Optional path prefix", false).
		IntParam("since_minutes", "How far back to look (default 10)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			src := cfg.source()
			if src == nil {
				return map[string]any{"activities": []any{}, "note": "Fabric source not available"}, nil
			}
			var params struct {
				TargetAgentType string `json:"target_agent_type"`
				Scope           string `json:"scope"`
				SinceMinutes    int    `json:"since_minutes"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			targetType := strings.TrimSpace(params.TargetAgentType)
			if targetType == "" {
				return nil, fmt.Errorf("target_agent_type is required")
			}
			sinceMinutes := params.SinceMinutes
			if sinceMinutes <= 0 {
				sinceMinutes = 10
			}
			since := time.Now().Add(-time.Duration(sinceMinutes) * time.Minute)

			filter := activity.QueryFilter{
				SessionID: activity.SessionID(cfg.SessionID()),
				ActionKinds: []activity.ActionKind{
					activity.ActionClaimIssued,
					activity.ActionClaimUpdated,
					activity.ActionTestamentSubmitted,
					activity.ActionClaimAccepted,
				},
				ActorAgentType: targetType,
				Since:          since,
				Limit:          10,
			}
			if scope := strings.TrimSpace(params.Scope); scope != "" {
				filter.SubjectPathPrefix = scope
			}

			activities, err := src.FilterActivities(ctx, filter)
			if err != nil {
				return nil, err
			}

			return map[string]any{
				"activities":        activities,
				"count":             len(activities),
				"target_agent_type": targetType,
			}, nil
		}).
		Build()
}

func fabricInspectClaimConflictsSkill(cfg AwarenessSkillConfig) *skills.Skill {
	return skills.NewSkill("fabric_inspect_claim_conflicts").
		Description("Surface claims that conflict or overlap across pipelines: competing implementations targeting the same scope, contradicting testaments, or mutual dependency blocks. Queries the Fabric activity stream for cross-pipeline conflict detection.").
		Domain("claims").
		Keywords("conflicts", "overlap", "cross-pipeline", "competing").
		Priority(91).
		StringParam("scope", "Path prefix to search for conflicts", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			src := cfg.source()
			if src == nil {
				return map[string]any{"conflicts": []any{}, "count": 0, "note": "Fabric source not available"}, nil
			}
			var params struct {
				Scope string `json:"scope"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			since := time.Now().Add(-10 * time.Minute)
			filter := activity.QueryFilter{
				SessionID: activity.SessionID(cfg.SessionID()),
				ActionKinds: []activity.ActionKind{
					activity.ActionClaimIssued,
					activity.ActionClaimUpdated,
				},
				Since: since,
				Limit: 50,
			}
			if scope := strings.TrimSpace(params.Scope); scope != "" {
				filter.SubjectPathPrefix = scope
			}

			activities, err := src.FilterActivities(ctx, filter)
			if err != nil {
				return nil, err
			}

			// Group by scope to find overlapping claims from different
			// pipelines/agents.
			type scopeKey struct{ kind, path string }
			scopeMap := make(map[scopeKey][]activity.AgentActivity)
			for _, a := range activities {
				if a.Subject.PathPrefix != "" {
					key := scopeKey{kind: "file", path: a.Subject.PathPrefix}
					scopeMap[key] = append(scopeMap[key], a)
				}
			}

			var conflicts []map[string]any
			for key, acts := range scopeMap {
				if len(acts) < 2 {
					continue
				}
				// Check if different pipelines/agents are touching the same scope.
				agents := make(map[string]bool)
				for _, a := range acts {
					agents[a.Actor.PipelineID+"/"+a.Actor.AgentID] = true
				}
				if len(agents) >= 2 {
					conflicts = append(conflicts, map[string]any{
						"type":          "cross_pipeline_overlap",
						"scope":         key.path,
						"agent_count":   len(agents),
						"activity_count": len(acts),
					})
				}
			}

			return map[string]any{
				"conflicts": conflicts,
				"count":     len(conflicts),
				"scope":     params.Scope,
			}, nil
		}).
		Build()
}
