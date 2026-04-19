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

// InspectorAuditSkillConfig wires the audit-time fabric inspection
// skills. Used by both pipeline inspector and global inspector.
//
// SourceProvider is a getter (not a direct field) so per-agent wiring
// can supply the orchestrator's activity store lazily after the
// orchestrator finishes startup. When SourceProvider returns nil
// (test fixtures, startup ordering), the audit skill returns an
// empty result set with a recommendation that says so — never
// blocking the agent's primary work.
type InspectorAuditSkillConfig struct {
	SourceProvider func() activity.Source
	SessionID      func() string
	AgentID        func() string
	AgentType      func() string
}

func (cfg InspectorAuditSkillConfig) source() activity.Source {
	if cfg.SourceProvider == nil {
		return nil
	}
	return cfg.SourceProvider()
}

// InspectorAuditSkills returns the inspector-only audit primitives
// over the fabric. Currently: inspect_open_activity (generalizes the
// original inspect_decision_conflicts to all in-flight activity in
// scope).
//
// At finalize time, the inspector calls inspect_open_activity to
// detect:
//   - open challenges that need resolution before acceptance
//   - hot scopes that signal contention
//   - stale cross-pipeline consults indicating unaware peers
//   - conflicting decisions across parallel pipelines
//   - validation holds blocking progress
//
// See docs/FABRIC.md §"Inspector becomes the cross-pipeline audit role".
func InspectorAuditSkills(cfg InspectorAuditSkillConfig) []*skills.Skill {
	return []*skills.Skill{
		inspectOpenActivitySkill(cfg),
	}
}

func inspectOpenActivitySkill(cfg InspectorAuditSkillConfig) *skills.Skill {
	return skills.NewSkill("inspect_open_activity").
		Description("Audit-time fabric inspection. Returns ALL in-flight activity in the audited scope: open challenges (in_flight, awaiting response), unresolved consults past their deadline, stalled validation holds, hot scopes (contested), and unresolved decision conflicts. Use BEFORE accepting or grading a pipeline's work — open conflicts in scope are blocking quality issues even when the work itself looks good in isolation.").
		Domain("audit").
		Keywords("audit", "fabric", "inspector", "conflicts", "open").
		Priority(96).
		Usage("Call BEFORE finalize / accept / grade. Pass the scope you're auditing (the pipeline's working area). Returns a structured digest the inspector reasons over to decide accept / request_correction / escalate.").
		Requirement("Always call at finalize time. Cost is a few indexed reads; the cost of skipping is silent cross-pipeline divergence reaching production.").
		Satisfies("Returns a structured digest: open challenges, stalled holds, unresolved consults, hotness signal, recent decisions in scope.").
		StringParam("scope", "Path scope to audit (the pipeline's working area).", false).
		IntParam("limit", "Max items per category. Default 20.", false).
		IntParam("hotness_window_minutes", "Window for hotness computation. Default 5 min.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Scope                string `json:"scope"`
				Limit                int    `json:"limit"`
				HotnessWindowMinutes int    `json:"hotness_window_minutes"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			scope := strings.TrimSpace(params.Scope)
			sessionID := activity.SessionID(safeCallString(cfg.SessionID))

			src := cfg.source()
			if src == nil {
				return AuditOpenActivityResult{
					Scope:             scope,
					RecommendedAction: "FABRIC_UNAVAILABLE: activity store not yet wired; proceed on the work's own merits.",
				}, nil
			}

			// Open conflicts.
			conflicts, err := lenses.ConflictsOpen(ctx, src, lenses.OpenConflictsQuery{
				SessionID: sessionID,
				Scope:     scope,
				Limit:     params.Limit,
			})
			if err != nil {
				return nil, fmt.Errorf("conflicts open: %w", err)
			}

			// Hotness signal.
			hotnessWindow := 5 * time.Minute
			if params.HotnessWindowMinutes > 0 {
				hotnessWindow = time.Duration(params.HotnessWindowMinutes) * time.Minute
			}
			hot, err := lenses.ScopeHotness(ctx, src, lenses.ScopeHotnessQuery{
				SessionID: sessionID,
				Scope:     scope,
				Window:    hotnessWindow,
			})
			if err != nil {
				return nil, fmt.Errorf("scope hotness: %w", err)
			}

			// Recent decisions in scope.
			decisions, err := src.FilterActivities(ctx, activity.QueryFilter{
				SessionID: sessionID,
				ActionKinds: []activity.ActionKind{
					activity.ActionDecisionDeclared,
					activity.ActionDecisionPromoted,
					activity.ActionDecisionSuperseded,
				},
				SubjectPathPrefix: scope,
				Since:             time.Now().Add(-30 * time.Minute),
				Limit:             20,
			})
			if err != nil {
				return nil, fmt.Errorf("recent decisions: %w", err)
			}

			// Active validation holds in scope.
			holds, err := src.FilterActivities(ctx, activity.QueryFilter{
				SessionID:         sessionID,
				ActionKinds:       []activity.ActionKind{activity.ActionHoldAcquired},
				States:            []activity.ActivityState{activity.StateInFlight},
				SubjectPathPrefix: scope,
				Limit:             10,
			})
			if err != nil {
				return nil, fmt.Errorf("active holds: %w", err)
			}

			// Audit-emitted "consulted" record so we know who audited what.
			emitConsulted(ctx, AwarenessSkillConfig{
				SessionID: cfg.SessionID,
				AgentID:   cfg.AgentID,
				AgentType: cfg.AgentType,
			}, "inspect_open_activity", scope)

			// Build the audit response.
			result := AuditOpenActivityResult{
				Scope:               scope,
				HotnessScore:        hot.HotnessScore,
				HotnessWindowStart:  hot.WindowStart,
				ChallengeCount:      hot.ChallengeCount,
				Hotness:             hot,
				OpenChallenges:      conflicts.Challenges,
				UnresolvedConsults:  conflicts.UnresolvedConsults,
				StalledHolds:        conflicts.StalledHolds,
				RecentDecisions:     decisions,
				ActiveHolds:         holds,
				BlockingForAcceptance: len(conflicts.Challenges) > 0 || len(conflicts.StalledHolds) > 0,
			}
			result.RecommendedAction = recommendAuditAction(result)
			return result, nil
		}).
		Build()
}

// AuditOpenActivityResult is the structured digest returned to the
// inspector LLM by inspect_open_activity. Designed to drive the
// accept / request_correction / escalate decision.
type AuditOpenActivityResult struct {
	Scope                 string
	HotnessScore          float64
	HotnessWindowStart    time.Time
	ChallengeCount        int
	Hotness               lenses.ScopeHotnessResult
	OpenChallenges        []activity.AgentActivity
	UnresolvedConsults    []activity.AgentActivity
	StalledHolds          []activity.AgentActivity
	RecentDecisions       []activity.AgentActivity
	ActiveHolds           []activity.AgentActivity
	BlockingForAcceptance bool
	RecommendedAction     string
}

// recommendAuditAction generates a string hint the inspector LLM can
// reason over. Not prescriptive — the LLM decides.
func recommendAuditAction(r AuditOpenActivityResult) string {
	switch {
	case len(r.OpenChallenges) > 0:
		return "DO NOT ACCEPT: open cross-pipeline challenges in scope must be resolved first. Use request_correction with the challenge activity_id(s) in evidence."
	case len(r.StalledHolds) > 0:
		return "DO NOT ACCEPT: stalled validation holds in scope. Resolve the hold before grading."
	case r.HotnessScore >= 15:
		return "PROCEED WITH CAUTION: scope is contested. Verify the work is consistent with the hot peers' commitments before accepting."
	case len(r.UnresolvedConsults) > 0:
		return "PROCEED: unresolved consults exist but are non-blocking. Note them in the audit trail so the next session sees the gap."
	default:
		return "OK: no blocking conflicts in scope. Accept on the work's own merits."
	}
}
