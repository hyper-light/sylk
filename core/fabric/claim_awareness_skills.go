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
//
// Per docs/CLAIMS_BUS.md §11, the canonical fabric lens query set is:
//
//   - query_peer_activity   (defined in awareness_skills.go)
//   - query_peer_claims     (fabricQueryPeerClaimsSkill)
//   - query_advisories      (fabricQueryAdvisoriesSkill)
//   - query_challenge_history (fabricQueryChallengeHistorySkill)
//
// query_peer_activity is registered from the base AwarenessSkills
// factory and remains there; the three claim-adjacent queries are
// provided here and must be registered alongside.
func ClaimsAwarenessSkills(cfg AwarenessSkillConfig) []*skills.Skill {
	return []*skills.Skill{
		fabricQueryClaimsBoardSkill(cfg),
		fabricQueryPeerClaimsSkill(cfg),
		fabricInspectClaimConflictsSkill(cfg),
		fabricQueryAdvisoriesSkill(cfg),
		fabricQueryChallengeHistorySkill(cfg),
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

// ────────────────────────────────────────────────────────────────────
// query_advisories
// ────────────────────────────────────────────────────────────────────

// fabricQueryAdvisoriesSkill surfaces knowledge-agent and guardian
// advisories filtered by scope. Queries the Fabric activity stream
// for ActionAdvisoryEmitted records — these are the notes the
// knowledge tier (librarian, academic, archivalist, guardian)
// publishes as ambient guidance, captured in the ambient envelope
// and surfaced for explicit per-scope recall.
func fabricQueryAdvisoriesSkill(cfg AwarenessSkillConfig) *skills.Skill {
	return skills.NewSkill("query_advisories").
		Description("List knowledge-agent and guardian advisories in scope. Returns every advisory the fabric has captured (ambient guidance, hotness notes, safety cautions) for the queried path prefix within the lookback window.").
		Domain("claims").
		Keywords("advisory", "knowledge", "guardian", "ambient", "guidance").
		Priority(89).
		StringParam("scope", "Path prefix to filter advisories", false).
		IntParam("since_minutes", "Lookback window (default "+defaultAdvisoryLookbackStr+")", false).
		IntParam("limit", "Max results (default "+defaultAdvisoryLimitStr+")", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			src := cfg.source()
			if src == nil {
				return map[string]any{"advisories": []any{}, "count": 0, "note": "Fabric source not available"}, nil
			}
			var params struct {
				Scope        string `json:"scope"`
				SinceMinutes int    `json:"since_minutes"`
				Limit        int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			lookback := durationOrDefault(params.SinceMinutes, defaultAdvisoryLookback)
			limit := intOrDefault(params.Limit, defaultAdvisoryLimit)

			filter := activity.QueryFilter{
				SessionID:   activity.SessionID(cfg.SessionID()),
				ActionKinds: []activity.ActionKind{activity.ActionAdvisoryEmitted},
				Since:       time.Now().Add(-lookback),
				Limit:       limit,
			}
			if scope := strings.TrimSpace(params.Scope); scope != "" {
				filter.SubjectPathPrefix = scope
			}

			activities, err := src.FilterActivities(ctx, filter)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"advisories": activities,
				"count":      len(activities),
				"scope":      params.Scope,
			}, nil
		}).
		Build()
}

// ────────────────────────────────────────────────────────────────────
// query_challenge_history
// ────────────────────────────────────────────────────────────────────

// fabricQueryChallengeHistorySkill returns past challenge outcomes
// with peers of a given type. Walks both the emission kind
// (ActionChallengeEmitted) and the paired response/unresolved kinds
// so a challenge's full lifecycle is visible.
func fabricQueryChallengeHistorySkill(cfg AwarenessSkillConfig) *skills.Skill {
	return skills.NewSkill("query_challenge_history").
		Description("List past challenge outcomes with peers of a given agent type. Returns each challenge's emission, response, and resolution so you can spot recurring contention patterns before issuing a new one.").
		Domain("claims").
		Keywords("challenge", "history", "peer", "resolution").
		Priority(88).
		StringParam("peer_agent_type", "Agent type whose challenge history to surface", true).
		IntParam("since_minutes", "Lookback window (default "+defaultChallengeLookbackStr+")", false).
		IntParam("limit", "Max results (default "+defaultChallengeLimitStr+")", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			src := cfg.source()
			if src == nil {
				return map[string]any{"challenges": []any{}, "count": 0, "note": "Fabric source not available"}, nil
			}
			var params struct {
				PeerAgentType string `json:"peer_agent_type"`
				SinceMinutes  int    `json:"since_minutes"`
				Limit         int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			peerType := strings.TrimSpace(params.PeerAgentType)
			if peerType == "" {
				return nil, fmt.Errorf("peer_agent_type is required")
			}
			lookback := durationOrDefault(params.SinceMinutes, defaultChallengeLookback)
			limit := intOrDefault(params.Limit, defaultChallengeLimit)

			filter := activity.QueryFilter{
				SessionID: activity.SessionID(cfg.SessionID()),
				ActionKinds: []activity.ActionKind{
					activity.ActionChallengeEmitted,
					activity.ActionChallengeResponse,
					activity.ActionChallengeUnresolved,
				},
				ActorAgentType: peerType,
				Since:          time.Now().Add(-lookback),
				Limit:          limit,
			}

			activities, err := src.FilterActivities(ctx, filter)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"challenges":      activities,
				"count":           len(activities),
				"peer_agent_type": peerType,
			}, nil
		}).
		Build()
}

// ────────────────────────────────────────────────────────────────────
// Tunable defaults
// ────────────────────────────────────────────────────────────────────

const (
	defaultAdvisoryLookback     = 30 * time.Minute
	defaultAdvisoryLookbackStr  = "30"
	defaultAdvisoryLimit        = 20
	defaultAdvisoryLimitStr     = "20"
	defaultChallengeLookback    = 60 * time.Minute
	defaultChallengeLookbackStr = "60"
	defaultChallengeLimit       = 20
	defaultChallengeLimitStr    = "20"
)

func durationOrDefault(minutes int, fallback time.Duration) time.Duration {
	if minutes <= 0 {
		return fallback
	}
	return time.Duration(minutes) * time.Minute
}

func intOrDefault(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}
