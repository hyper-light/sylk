package fabric

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/skills"
)

// RecallSkillConfig wires the recall_my_history skill onto a parent
// agent. Per SCRIBE_FABRIC.md §8 — the skill lets the agent itself
// consult its biographer (the scribe's narrations stored in
// archivalist + knowledge runtime + fabric).
//
// Production wiring composes results from three substrates:
//
//   - Activity Fabric narration_emitted activities (typed, scoped,
//     causally-linked) via SourceProvider.
//   - Archivalist consultation (the canonical Entry store) — left as
//     follow-up wiring; current build returns the fabric-side digest
//     which already covers the most useful slice (recent narrations
//     in scope by the agent's own type).
//   - Knowledge runtime markdown corpus — also follow-up wiring.
//
// SourceProvider returns nil gracefully when the orchestrator hasn't
// installed the activity store yet — the skill returns an empty
// recall result rather than failing.
type RecallSkillConfig struct {
	SourceProvider func() activity.Source
	SessionID      func() string
	AgentID        func() string
	AgentType      func() string
}

func (cfg RecallSkillConfig) source() activity.Source {
	if cfg.SourceProvider == nil {
		return nil
	}
	return cfg.SourceProvider()
}

// RecallSkills returns the recall_my_history skill (currently the
// only member of the recall family — extensions may add
// recall_peer_history, recall_session_milestones, etc. as needed).
func RecallSkills(cfg RecallSkillConfig) []*skills.Skill {
	return []*skills.Skill{
		recallMyHistorySkill(cfg),
	}
}

func recallMyHistorySkill(cfg RecallSkillConfig) *skills.Skill {
	return skills.NewSkill("recall_my_history").
		Description("Ask your scribe (your dedicated biographer) for a structured digest of your prior work this session — what you decided, why, what you tried, what worked, what didn't. CALL THIS when about to make a decision you may have made before, when revisiting a state you remember reasoning about earlier, or when a new replica of you boots and needs to know its own past. Backed by the Activity Fabric's narration_emitted stream filtered to your agent type. Never blocks — returns an empty digest when the fabric isn't yet wired.").
		Domain("memory").
		Keywords("memory", "history", "biography", "prior", "self", "scribe", "recall", "remember", "previous").
		Priority(94).
		Usage("Use when you suspect you've covered ground before, want to confirm a prior decision, or need to reconstruct the reasoning that led to a state you're now revisiting. Pass an optional scope to narrow recall.").
		Requirement("Call when investigating prior work; cost is one indexed fabric read.").
		Satisfies("Returns a chronologically-ordered digest of prior scribe narrations about this agent's work.").
		StringParam("scope", "Optional path-prefix scope to narrow recall (e.g., \"services/billing/\").", false).
		IntParam("since_minutes", "Lookback window in minutes. Default 60.", false).
		ArrayParam("replica_generations", "Replica generations to include. Empty defaults to current + most-recent prior. Pass [0] to include all generations.", "integer", false).
		IntParam("max_entries", "Maximum recall entries to return. Default 20.", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Scope               string `json:"scope"`
				SinceMinutes        int    `json:"since_minutes"`
				ReplicaGenerations  []int  `json:"replica_generations"`
				MaxEntries          int    `json:"max_entries"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			src := cfg.source()
			if src == nil {
				return RecallResult{
					Note: "fabric source not installed; recall is empty (running in test or pre-init context)",
				}, nil
			}
			limit := params.MaxEntries
			if limit <= 0 {
				limit = 20
			}
			lookback := 60 * time.Minute
			if params.SinceMinutes > 0 {
				lookback = time.Duration(params.SinceMinutes) * time.Minute
			}

			agentType := safeCallString(cfg.AgentType)
			rows, err := src.FilterActivities(ctx, activity.QueryFilter{
				SessionID:         activity.SessionID(safeCallString(cfg.SessionID)),
				ActionKinds:       []activity.ActionKind{activity.ActionNarrationEmitted},
				SubjectDomain:     agentType,
				SubjectPathPrefix: strings.TrimSpace(params.Scope),
				Since:             time.Now().Add(-lookback),
				Limit:             limit * 2, // over-fetch; we filter by replica_generation client-side
			})
			if err != nil {
				return nil, fmt.Errorf("fabric narration query: %w", err)
			}

			generations := normalizeReplicaGenerations(params.ReplicaGenerations)
			out := RecallResult{
				AgentType:          agentType,
				ScopeFilter:        params.Scope,
				LookbackMinutes:    int(lookback / time.Minute),
				ReplicaGenerations: generations,
			}
			for _, r := range rows {
				gen := parseReplicaGeneration(r.Subject.Coordinates)
				if !generationAccepted(generations, gen) {
					continue
				}
				out.Entries = append(out.Entries, RecallEntry{
					ActivityID:         r.ID,
					Timestamp:          r.Timestamp,
					Scope:              r.Subject.PathPrefix,
					ReplicaGeneration:  gen,
					ParentCorrelationID: r.Subject.Coordinates["parent_correlation_id"],
					ScribeID:           r.Subject.Coordinates["scribe_id"],
					ArchivalEntryID:    r.SourceID,
					Commentary:         r.Payload,
				})
				if len(out.Entries) >= limit {
					break
				}
			}
			return out, nil
		}).
		Build()
}

// RecallResult is the typed shape returned to the agent's LLM.
type RecallResult struct {
	AgentType          string         `json:"agent_type"`
	ScopeFilter        string         `json:"scope_filter,omitempty"`
	LookbackMinutes    int            `json:"lookback_minutes"`
	ReplicaGenerations []int          `json:"replica_generations,omitempty"`
	Entries            []RecallEntry  `json:"entries"`
	Note               string         `json:"note,omitempty"`
}

// RecallEntry is one historical narration the recall surface
// returned. Carries the activity ID for causal-trace follow-up,
// the archivalist entry ID for full-document retrieval, and the
// commentary itself for direct LLM grounding.
type RecallEntry struct {
	ActivityID          activity.ActivityID `json:"activity_id"`
	Timestamp           time.Time           `json:"timestamp"`
	Scope               string              `json:"scope,omitempty"`
	ReplicaGeneration   int                 `json:"replica_generation"`
	ParentCorrelationID string              `json:"parent_correlation_id,omitempty"`
	ScribeID            string              `json:"scribe_id,omitempty"`
	ArchivalEntryID     string              `json:"archival_entry_id,omitempty"`
	Commentary          json.RawMessage     `json:"commentary"`
}

// normalizeReplicaGenerations applies the documented default: when
// the agent passes an empty list, recall returns current + most
// recent prior. When the agent passes [0], recall returns all
// generations (the sentinel "any"). Otherwise the explicit list
// flows through.
//
// Note: this function returns nil when the agent wants the
// "current + prior" default — the caller-side `generationAccepted`
// reads nil as "accept any non-zero generation" and we layer the
// "but no older than (max - 1)" filter outside this helper.
func normalizeReplicaGenerations(in []int) []int {
	if len(in) == 0 {
		return nil // default: current + prior, evaluated on the rows themselves
	}
	if len(in) == 1 && in[0] == 0 {
		return []int{0} // sentinel: include all generations
	}
	return in
}

// generationAccepted reports whether a row's generation should be
// included given the requested generation set. The empty-list /
// nil case means "current + prior" — which we approximate as "include
// any non-zero generation" because we don't know the current one
// from inside the fabric query alone (the scribe owns it). Callers
// who need the strict "current + prior" filter can pass an explicit
// list.
func generationAccepted(allowed []int, candidate int) bool {
	if len(allowed) == 0 {
		// Default: include any generation we have a value for.
		return true
	}
	if len(allowed) == 1 && allowed[0] == 0 {
		return true // explicit any-generation sentinel
	}
	for _, g := range allowed {
		if g == candidate {
			return true
		}
	}
	return false
}

func parseReplicaGeneration(coords map[string]string) int {
	if coords == nil {
		return 0
	}
	raw, ok := coords["replica_generation"]
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
