package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/activity/lenses"
	"github.com/adalundhe/sylk/core/skills"
)

// AwarenessSkillConfig wires the four uniform awareness skills onto a
// fabric Source. Provided once per agent during skill setup. The
// Source is provided as a getter so per-agent wiring can supply it
// lazily (typically the orchestrator's activitystore.SQLiteStore is
// not yet up at agent-construction time; the getter resolves at
// skill-invocation time when the orchestrator is guaranteed to be
// running).
//
// AgentID and AgentType identify the calling agent so the skills can
// auto-emit a lightweight `consulted` activity for causal traceability
// of "who-asked-what" — never a precondition for primary work.
//
// When SourceProvider returns nil (test fixtures, startup ordering),
// the skills return empty results gracefully — they never block the
// agent's primary work.
type AwarenessSkillConfig struct {
	SourceProvider func() activity.Source
	SessionID      func() string
	AgentID        func() string
	AgentType      func() string
}

func (cfg AwarenessSkillConfig) source() activity.Source {
	if cfg.SourceProvider == nil {
		return nil
	}
	return cfg.SourceProvider()
}

// AwarenessSkills returns the four uniform awareness primitives that
// every pipeline agent gets. They replace the per-domain query
// proliferation with one consistent surface.
//
// See docs/FABRIC.md §"Vector 4 — Active awareness skills (pulled)".
func AwarenessSkills(cfg AwarenessSkillConfig) []*skills.Skill {
	return []*skills.Skill{
		queryPeerActivitySkill(cfg),
		causalTraceSkill(cfg),
		findRelatedActivitySkill(cfg),
		inspectOpenConflictsSkill(cfg),
	}
}

func queryPeerActivitySkill(cfg AwarenessSkillConfig) *skills.Skill {
	return skills.NewSkill("query_peer_activity").
		Description("See what peer agents in other pipelines have been doing in a scope recently. Returns the most recent typed activities (decisions, claims, validations, advisories) by other agents in the queried scope. Filter by ActionKind when you only care about specific kinds. Auto-publishes a `consulted` activity for causal traceability. Never blocks your primary work — purely informational.").
		Domain("fabric").
		Keywords("awareness", "peers", "fabric", "cross-pipeline", "query").
		Priority(94).
		Usage("Use when ambient context shows activity in your scope and you want to dig deeper. Pass scope (path prefix) and optionally specific kinds (e.g. \"decision_declared\", \"claim_acquired\"). The default lookback is the last 5 minutes; pass `since_minutes` for a wider window.").
		Requirement("Call when you want to inspect peer pipeline state. Cost is one indexed read on the fabric.").
		Satisfies("Returns recent peer activity in scope, ordered by recency.").
		StringParam("scope", "Path prefix locating your context (e.g., \"services/billing/\").", false).
		ArrayParam("kinds", "Optional list of ActionKind names to filter to (e.g. [\"decision_declared\", \"claim_acquired\"]). Empty = any kind.", "string", false).
		IntParam("since_minutes", "Lookback window in minutes. Default 5.", false).
		IntParam("limit", "Maximum activities to return. Default 50.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Scope        string   `json:"scope"`
				Kinds        []string `json:"kinds"`
				SinceMinutes int      `json:"since_minutes"`
				Limit        int      `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			emitConsulted(ctx, cfg, "query_peer_activity", params.Scope)
			lookback := 5 * time.Minute
			if params.SinceMinutes > 0 {
				lookback = time.Duration(params.SinceMinutes) * time.Minute
			}
			kinds := make([]activity.ActionKind, 0, len(params.Kinds))
			for _, k := range params.Kinds {
				kinds = append(kinds, activity.ActionKind(strings.TrimSpace(k)))
			}
			return lenses.WhatAreTheyDoing(ctx, cfg.source(), lenses.PeerActivityQuery{
				SessionID:    activity.SessionID(cfg.SessionID()),
				Scope:        strings.TrimSpace(params.Scope),
				Kinds:        kinds,
				Since:        time.Now().Add(-lookback),
				ExcludeAgent: cfg.AgentID(),
				Limit:        params.Limit,
			})
		}).
		Build()
}

func causalTraceSkill(cfg AwarenessSkillConfig) *skills.Skill {
	return skills.NewSkill("causal_trace").
		Description("Walk the cause/caused DAG anchored at a specific activity. Returns the chain of ancestors (root → parent → target) and immediate descendants. Useful for understanding 'what led to this state' — e.g., why a decision exists, what triggered an inspector hold, what chain produced a given artifact.").
		Domain("fabric").
		Keywords("awareness", "causal", "audit", "fabric", "trace").
		Priority(91).
		Usage("Use when you need to understand the lineage of an activity. Pass the activity_id (commonly surfaced in ambient_context). Returns ancestors in oldest-first order and direct children.").
		Requirement("Call when investigating why state exists or who triggered which work.").
		Satisfies("Returns ancestors + direct descendants of the targeted activity.").
		StringParam("activity_id", "The activity ID to trace from (typically from ambient_context or a prior query result).", true).
		IntParam("max_depth", "Maximum ancestor depth to walk. Default 32.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				ActivityID string `json:"activity_id"`
				MaxDepth   int    `json:"max_depth"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			emitConsulted(ctx, cfg, "causal_trace", params.ActivityID)
			return lenses.CausalContext(ctx, cfg.source(), activity.ActivityID(strings.TrimSpace(params.ActivityID)), params.MaxDepth)
		}).
		Build()
}

func findRelatedActivitySkill(cfg AwarenessSkillConfig) *skills.Skill {
	return skills.NewSkill("find_related_activity").
		Description("Find activities matching a free-text query OR a target scope. The current implementation falls back to a scope-prefix search (full-text via Bleve and semantic search via vectorgraphdb come online when those subscribers ship; see docs/FABRIC.md Tier 10). Returns matching activities ordered by recency.").
		Domain("fabric").
		Keywords("awareness", "search", "fabric", "related").
		Priority(89).
		Usage("Use when you want a broader sweep than query_peer_activity — e.g., 'find every activity touching services/billing/auth.go in the last hour' or 'show me what's been happening across this session that involves pytest.'").
		Requirement("Call to broaden investigation beyond a specific scope.").
		Satisfies("Returns matching activities ordered by recency.").
		StringParam("query", "Free-text query (substring match against payload + subject when available).", false).
		StringParam("scope", "Optional path-prefix scope to narrow the search.", false).
		IntParam("since_minutes", "Lookback window in minutes. Default 60.", false).
		IntParam("limit", "Maximum results. Default 50.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Query        string `json:"query"`
				Scope        string `json:"scope"`
				SinceMinutes int    `json:"since_minutes"`
				Limit        int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			emitConsulted(ctx, cfg, "find_related_activity", params.Query)
			lookback := 60 * time.Minute
			if params.SinceMinutes > 0 {
				lookback = time.Duration(params.SinceMinutes) * time.Minute
			}
			limit := params.Limit
			if limit <= 0 {
				limit = 50
			}
			src := cfg.source()
			if src == nil {
				return []activity.AgentActivity{}, nil
			}
			rows, err := src.FilterActivities(ctx, activity.QueryFilter{
				SessionID:         activity.SessionID(cfg.SessionID()),
				SubjectPathPrefix: strings.TrimSpace(params.Scope),
				Since:             time.Now().Add(-lookback),
				Limit:             limit,
			})
			if err != nil {
				return nil, err
			}
			// Best-effort substring filter on payload + subject when a
			// query is provided (Bleve subscriber will replace this in
			// Tier 10).
			query := strings.ToLower(strings.TrimSpace(params.Query))
			if query == "" {
				return rows, nil
			}
			out := make([]activity.AgentActivity, 0, len(rows))
			for _, r := range rows {
				hay := strings.ToLower(string(r.Payload) + " " + r.Subject.PathPrefix + " " + r.Subject.Domain + " " + r.Subject.TargetArtifact + " " + string(r.Action))
				if strings.Contains(hay, query) {
					out = append(out, r)
				}
			}
			return out, nil
		}).
		Build()
}

func inspectOpenConflictsSkill(cfg AwarenessSkillConfig) *skills.Skill {
	return skills.NewSkill("inspect_open_conflicts").
		Description("Return what is currently contested in your scope: open challenges (in_flight, awaiting response), unanswered consults past their deadline, and stalled validation holds. Use to decide whether to challenge, adopt, or proceed without knowing.").
		Domain("fabric").
		Keywords("awareness", "conflicts", "fabric", "challenge").
		Priority(90).
		Usage("Use when ambient context surfaced a conflict marker, or proactively before introducing a divergent commitment in a scope you suspect is contested.").
		Requirement("Call to understand current contention before acting on a contested scope.").
		Satisfies("Returns open challenges, unresolved consults, and stalled holds in scope.").
		StringParam("scope", "Path prefix to scope the conflicts (empty = whole session).", false).
		IntParam("limit", "Maximum items per conflict category. Default 20.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Scope string `json:"scope"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			emitConsulted(ctx, cfg, "inspect_open_conflicts", params.Scope)
			return lenses.ConflictsOpen(ctx, cfg.source(), lenses.OpenConflictsQuery{
				SessionID: activity.SessionID(cfg.SessionID()),
				Scope:     strings.TrimSpace(params.Scope),
				Limit:     params.Limit,
			})
		}).
		Build()
}

// emitConsulted records that an agent invoked an awareness skill — a
// lightweight Fine-resolution activity for causal traceability of
// who-asked-what. Never blocks; never errors.
func emitConsulted(ctx context.Context, cfg AwarenessSkillConfig, skillName, target string) {
	payload, _ := json.Marshal(map[string]any{
		"skill":  skillName,
		"target": target,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(safeCallString(cfg.SessionID)),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionConsulted),
		Action:     activity.ActionConsulted,
		Actor: activity.Actor{
			AgentID:   safeCallString(cfg.AgentID),
			AgentType: safeCallString(cfg.AgentType),
		},
		Subject: activity.Subject{
			PathPrefix: target,
		},
		Payload: payload,
		State:   activity.StatePoint,
	}
	activity.Append(ctx, act)
}

func safeCallString(fn func() string) string {
	if fn == nil {
		return ""
	}
	return fn()
}
