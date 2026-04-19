package scribe

import (
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

// ScribeProfile is the per-agent declarative configuration for a
// scribe sidecar. It replaces the hardcoded `switch parentAgentType`
// in the prompt builder with a registry pattern: each agent type
// declares its own profile and the scribe consults the registry at
// runtime.
//
// See docs/SCRIBE_FABRIC.md §6 for the design rationale.
type ScribeProfile struct {
	// AgentType is the parent agent type this profile applies to
	// ("architect", "engineer", "tester-pipeline", etc.).
	AgentType string

	// ResolutionFilter is the minimum activity Resolution the scribe
	// receives. Defaults to Fine — Atomic events are too high-volume
	// to narrate individually. Stricter profiles can set Medium or
	// Coarse to reduce LLM cost further.
	ResolutionFilter activity.Resolution

	// IgnoreKinds is a per-profile blacklist. Activities of these
	// kinds skip the scribe's batch even if they pass the resolution
	// filter. Useful for high-volume kinds the agent's biographer
	// shouldn't waste tokens on.
	IgnoreKinds []activity.ActionKind

	// EmphasizeKinds force an immediate-narration trigger when one
	// arrives mid-batch — regardless of size or window. Useful for
	// escalations, errors, charter ratifications, plan changes —
	// anything that should never wait for the next batch boundary.
	EmphasizeKinds []activity.ActionKind

	// BatchSize is the activity-count threshold that triggers a
	// narration. Defaults to 10.
	BatchSize int

	// BatchWindow is the time-since-first-activity threshold that
	// triggers a narration. Defaults to 5s.
	BatchWindow time.Duration

	// PeriodicWindow is the rolling-synthesis interval. Even with no
	// activities in the buffer, a synthesis narration fires on this
	// cadence so cold readers joining mid-session see a "where are
	// we now" view. Defaults to 5min.
	PeriodicWindow time.Duration

	// PromptModule names the per-agent prompt module loaded from
	// `prompts/scribe/profiles/<PromptModule>.md` and concatenated
	// after the common base. Defaults to AgentType when empty.
	PromptModule string

	// OutputSchema documents the role-specific commentary fields the
	// scribe LLM should produce. Surfaced into the LLM's prompt so
	// it knows what to emit beyond the generic
	// summary/progress/decisions/state/risk shape.
	OutputSchema map[string]string

	// PrecedentRules surface candidate patterns to the LLM. The LLM
	// still has the final say (via the precedent_worthy commentary
	// field), but the rules pre-evaluate batches and append matched
	// rule descriptions to the prompt so the LLM doesn't have to
	// discover them from scratch.
	PrecedentRules []PrecedentRule
}

// PrecedentRule is a per-profile predicate over (commentary, batch).
// When the predicate returns true, the rule's Description is included
// in the scribe's LLM prompt as a "candidate precedent pattern" hint.
type PrecedentRule struct {
	Description string
	Predicate   func(commentary map[string]any, batch []activity.AgentActivity) bool
}

// ─── Registry ────────────────────────────────────────────────────────

var (
	profileMu       sync.RWMutex
	profileRegistry = map[string]ScribeProfile{}
)

// RegisterProfile adds or replaces a scribe profile in the registry.
// Typically called from each per-agent file's init() function. Safe
// for concurrent callers.
func RegisterProfile(p ScribeProfile) {
	if strings.TrimSpace(p.AgentType) == "" {
		return
	}
	profileMu.Lock()
	profileRegistry[p.AgentType] = p
	profileMu.Unlock()
}

// ProfileFor returns the registered profile for agentType, or a
// reasonable default if no profile has been registered. Safe for
// concurrent callers.
//
// The default profile applies to any unregistered agent type — it
// uses the standard cadence and a generic prompt module ("default").
func ProfileFor(agentType string) ScribeProfile {
	agentType = strings.TrimSpace(agentType)
	profileMu.RLock()
	p, ok := profileRegistry[agentType]
	profileMu.RUnlock()
	if !ok {
		p = ScribeProfile{
			AgentType:    agentType,
			PromptModule: "default",
		}
	}
	return normalizeProfile(p)
}

// RegisteredProfiles returns a snapshot of every registered agent
// type. Used by tests and by tooling that introspects the registry.
func RegisteredProfiles() []string {
	profileMu.RLock()
	defer profileMu.RUnlock()
	out := make([]string, 0, len(profileRegistry))
	for k := range profileRegistry {
		out = append(out, k)
	}
	return out
}

// ─── Defaults ────────────────────────────────────────────────────────

const (
	defaultProfileBatchSize      = 10
	defaultProfileBatchWindow    = 5 * time.Second
	defaultProfilePeriodicWindow = 5 * time.Minute
)

func normalizeProfile(p ScribeProfile) ScribeProfile {
	if p.ResolutionFilter == "" {
		p.ResolutionFilter = activity.ResolutionFine
	}
	if p.BatchSize <= 0 {
		p.BatchSize = defaultProfileBatchSize
	}
	if p.BatchWindow <= 0 {
		p.BatchWindow = defaultProfileBatchWindow
	}
	if p.PeriodicWindow <= 0 {
		p.PeriodicWindow = defaultProfilePeriodicWindow
	}
	if strings.TrimSpace(p.PromptModule) == "" {
		p.PromptModule = strings.TrimSpace(p.AgentType)
		if p.PromptModule == "" {
			p.PromptModule = "default"
		}
	}
	return p
}

// ─── Helpers ─────────────────────────────────────────────────────────

// ShouldIgnoreKind reports whether an activity of kind should skip
// this profile's batch. Returns false for kinds not on the IgnoreKinds
// list.
func (p ScribeProfile) ShouldIgnoreKind(kind activity.ActionKind) bool {
	for _, k := range p.IgnoreKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// IsEmphasized reports whether an activity of kind should trigger
// immediate narration. Returns false for kinds not on EmphasizeKinds.
func (p ScribeProfile) IsEmphasized(kind activity.ActionKind) bool {
	for _, k := range p.EmphasizeKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// AcceptsResolution reports whether this profile's filter passes the
// given resolution. Profiles with ResolutionFilter=Fine accept Fine,
// Medium, and Coarse; profiles with ResolutionFilter=Coarse accept
// only Coarse.
func (p ScribeProfile) AcceptsResolution(r activity.Resolution) bool {
	return resolutionRank(r) >= resolutionRank(p.ResolutionFilter)
}

// resolutionRank maps Resolution to an ordinal so tier comparisons
// are explicit. Higher = more durable.
func resolutionRank(r activity.Resolution) int {
	switch r {
	case activity.ResolutionAtomic:
		return 1
	case activity.ResolutionFine:
		return 2
	case activity.ResolutionMedium:
		return 3
	case activity.ResolutionCoarse:
		return 4
	}
	return 0
}
