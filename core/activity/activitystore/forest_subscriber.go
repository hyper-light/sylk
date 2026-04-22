package activitystore

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/activity"
)

// ForestSubscriber is a Memory Forest harvest subscriber. It listens
// for precedent-quality activities — lifecycle closures (consult
// responses, challenge resolutions), acceptance events (validations,
// charter ratifications), knowledge pushes (advisories, narrations,
// precedent emissions), and the high-signal operational primitives
// (tool calls, LLM round-trips, forest consults) — and forwards them
// to a caller-supplied harvest function.
//
// Harvesting is **decoupled from the activity's storage Resolution
// tier**. The Resolution tier decides how long the fabric's own
// SQLite/cache/vectorgraphdb keeps an activity around; the forest
// needs an independent eligibility policy because it mirrors the
// activity into its own persistent content.sqlite and operates on
// cross-session precedent, not this-session storage retention. Put
// differently: a tool call lives in the fabric's Fine tier for 24h,
// but the forest copies the record into its own store at harvest
// time, so the forest doesn't care about fabric aging.
//
// See docs/FABRIC.md Tier 11 and docs/FOREST_FABRIC_INTEGRATION.md.
type ForestSubscriber struct {
	harvest    HarvestFunc
	harvested  atomic.Uint64
	skipped    atomic.Uint64
	candidates atomic.Uint64

	mu sync.Mutex
}

// HarvestFunc is invoked for each candidate activity. The
// implementation is responsible for any blocking work (file IO,
// SQLite write, vectorgraphdb embed) — it should typically dispatch
// to a goroutine if the work isn't bounded.
type HarvestFunc func(ctx context.Context, candidate ForestCandidate) error

// ForestCandidate is a typed wrapper around an AgentActivity that the
// forest subsystem ingests. It carries the activity itself plus the
// reason the subscriber elected it as a candidate.
type ForestCandidate struct {
	Activity activity.AgentActivity
	Reason   string
}

// NewForestSubscriber creates a subscriber that dispatches harvest
// candidates to harvest. Pass a nil HarvestFunc to disable
// harvesting (the subscriber still tracks counters).
func NewForestSubscriber(harvest HarvestFunc) *ForestSubscriber {
	return &ForestSubscriber{harvest: harvest}
}

func (f *ForestSubscriber) Name() string { return "fabric.forest" }

// Receive considers the activity for forest harvest. Election runs
// entirely on the ActionKind allowlist — no Resolution tier gate.
//
// Atomic-tier high-volume infrastructural events (LLM chunks, raw
// file reads, cache hits) are skipped by virtue of not appearing in
// the allowlist; there is no case in electCandidate that matches
// them. Every other interesting operational event is either always
// a candidate or conditional on the activity's Confidence / State.
func (f *ForestSubscriber) Receive(ctx context.Context, a activity.AgentActivity) {
	reason, ok := f.electCandidate(a)
	if !ok {
		f.skipped.Add(1)
		return
	}
	f.candidates.Add(1)
	if f.harvest == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.harvest(ctx, ForestCandidate{Activity: a, Reason: reason}); err == nil {
		f.harvested.Add(1)
	}
}

// electCandidate is the single source of truth for forest
// eligibility. Categories, top to bottom:
//
//  1. Explicit precedent and consensus decisions — the original
//     narrow harvest set. These are terminal, high-confidence
//     signals.
//  2. Lifecycle closures (consult_response, challenge_response,
//     validation outcomes, remediation_resolved) — "what answered
//     what" is the canonical precedent shape.
//  3. Acceptance and ratification (plan_ratified, charter_ratified,
//     advisory_emitted/proactive_advisory, narration_emitted) —
//     the semantic work an agent committed to.
//  4. Artifacts and review completions — concrete products the
//     fabric has witnessed flow through review.
//  5. Operational primitives that carry learning signal
//     (tool_call_completed, llm_response_completed, forest_consult
//     emissions) — when successful, these are "this shape of
//     request actually worked" precedent.
func (f *ForestSubscriber) electCandidate(a activity.AgentActivity) (string, bool) {
	switch a.Action {
	// ── Category 1: explicit precedent + consensus ──
	case activity.ActionPrecedentEmitted:
		return "explicit precedent_emitted", true
	case activity.ActionDecisionPromoted:
		if a.Confidence == activity.ConfidenceConsensus {
			return "decision promoted to consensus", true
		}
	case activity.ActionValidationAccepted:
		return "validation accepted (inspector ratification)", true
	case activity.ActionCharterRatified:
		return "charter ratified by architect plan acceptance", true

	// ── Category 2: lifecycle closures ──
	case activity.ActionConsultResponse:
		return "consult answered — request → answer precedent", true
	case activity.ActionChallengeResponse:
		return "challenge resolved — dispute outcome precedent", true
	case activity.ActionValidationRejected:
		return "validation rejected — adversarial precedent", true
	case activity.ActionRemediationResolved:
		return "remediation resolved — recovery precedent", true

	// ── Category 3: acceptance + ratification + knowledge push ──
	case activity.ActionPlanRatified:
		return "plan ratified — authored strategy precedent", true
	case activity.ActionDecisionDeclared:
		// Even without consensus, a declared decision is a concrete
		// commitment worth indexing. Gardening downstream can elide
		// it if later activities supersede.
		return "decision declared", true
	case activity.ActionAdvisoryEmitted:
		return "advisory emitted — knowledge push", true
	case activity.ActionProactiveAdvisory:
		return "proactive advisory — targeted knowledge signal", true
	case activity.ActionNarrationEmitted:
		return "narration — high-level agent activity summary", true

	// ── Category 4: artifacts + review completions ──
	case activity.ActionArtifactPublished:
		return "artifact published — concrete work product", true
	case activity.ActionReviewCompleted:
		return "review completed — witnessed quality pass", true

	// ── Category 5: operational primitives with learning signal ──
	case activity.ActionToolCallCompleted:
		// Successful tool completions are precedent. Failed ones are
		// also precedent (failure learning), so both states qualify —
		// the forest gardener uses State to decide salience.
		return "tool call completed", true
	case activity.ActionLLMResponseCompleted:
		// Captures model × prompt-shape × outcome. Always-on at
		// Medium resolution, so the forest can learn which models
		// succeed on which prompt shapes.
		return "llm round-trip completed", true
	case activity.ActionForestConsultEmitted:
		// Tier 5 of the forest-fabric integration. Consults are
		// precedent for subsequent outcomes; recording them lets the
		// outcome harvester link consult → outcome.
		return "forest consult emitted", true
	}
	return "", false
}

// HarvestedCount returns the running total of activities the
// HarvestFunc accepted.
func (f *ForestSubscriber) HarvestedCount() uint64 { return f.harvested.Load() }

// CandidateCount returns the running total of activities elected as
// candidates.
func (f *ForestSubscriber) CandidateCount() uint64 { return f.candidates.Load() }

// SkippedCount returns the running total of activities dropped without
// harvest consideration.
func (f *ForestSubscriber) SkippedCount() uint64 { return f.skipped.Load() }

var _ Subscriber = (*ForestSubscriber)(nil)
