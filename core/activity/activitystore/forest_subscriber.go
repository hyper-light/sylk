package activitystore

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/activity"
)

// ForestSubscriber is a Memory Forest traversal/context subscriber. Claims
// lifecycle truth is ingested from canonical claims deltas, not Fabric activity
// payloads. This subscriber forwards only non-authoritative operational and
// traversal observations that help the forest learn how context was used.
//
// The harvest function MUST be non-blocking. The canonical wiring
// pairs this subscriber with HarvestDispatcher (in this package)
// which provides bounded queue + worker pool + activity-ID dedupe +
// drop/error counters in front of the synchronous forest write. This
// satisfies the substrate's "async by default" invariant — caller
// goroutines emitting Fabric activities never block on a SQLite
// ledger write.
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
	errors     atomic.Uint64
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
// Receive evaluates the activity for forest eligibility and, when
// elected, dispatches it to the harvest function. The harvest call
// MUST be non-blocking; pair this subscriber with HarvestDispatcher
// in production so the call is queue-enqueue-only. Errors from the
// harvest call increment the errors counter and are logged; per
// CLAIMS.md §5.11 errors-as-artifacts, the dispatcher's error sink
// is responsible for surfacing the failure as a kind=error_harvest_*
// artifact on the in-flight testament.
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
	if err := f.harvest(ctx, ForestCandidate{Activity: a, Reason: reason}); err != nil {
		f.errors.Add(1)
		slog.Error("forest_subscriber_harvest_error",
			"activity_id", string(a.ID),
			"action_kind", string(a.Action),
			"reason", reason,
			"err", err.Error(),
		)
		return
	}
	f.harvested.Add(1)
}

// electCandidate is the single source of truth for Fabric-side forest
// eligibility. It deliberately excludes claims, testament, artifact, and
// validation lifecycle actions because those are authoritative only through
// canonical claims deltas.
func (f *ForestSubscriber) electCandidate(a activity.AgentActivity) (string, bool) {
	switch a.Action {
	// Explicit operational precedent and consensus decisions.
	case activity.ActionPrecedentEmitted:
		return "explicit precedent_emitted", true
	case activity.ActionDecisionPromoted:
		if a.Confidence == activity.ConfidenceConsensus {
			return "decision promoted to consensus", true
		}
	case activity.ActionCharterRatified:
		return "charter ratified by architect plan acceptance", true

	// Agent collaboration observations. These are context traversal facts,
	// not claim lifecycle truth.
	case activity.ActionConsultResponse:
		return "consult response observed as fabric traversal", true
	case activity.ActionChallengeResponse:
		return "challenge response observed as fabric traversal", true
	case activity.ActionRemediationResolved:
		return "remediation resolved observation", true

	// Knowledge push and authored strategy observations.
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

	// Operational primitives with learning signal.
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

	// Claims graph traversal observation only.
	case activity.ActionTraversalObserved:
		return "traversal observed — graph walk precedent", true
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

// ErrorCount returns the running total of harvest calls that returned
// a non-nil error. Each was logged at slog.Error level; pair with the
// dispatcher's error sink to surface them as testament artifacts.
func (f *ForestSubscriber) ErrorCount() uint64 { return f.errors.Load() }

var _ Subscriber = (*ForestSubscriber)(nil)
