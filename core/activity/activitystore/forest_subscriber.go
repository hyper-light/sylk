package activitystore

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/activity"
)

// ForestSubscriber is a Memory Forest harvest subscriber. It listens
// for high-quality precedent activities — typically those promoted to
// Consensus or explicitly emitted as ActionPrecedentEmitted by
// inspector audit acceptance paths — and forwards them to a
// caller-supplied harvest function.
//
// The actual Memory Forest persistence stays sovereign in
// core/forest; this subscriber's job is to filter the activity stream
// down to harvest candidates and dispatch them. The Forest subsystem
// then ingests them on its own cadence (batched, deduplicated,
// indexed against existing precedent).
//
// See docs/FABRIC.md Tier 11.
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

// Receive considers the activity for forest harvest. The current
// rules:
//
//   - ActionPrecedentEmitted: always a candidate.
//   - ActionDecisionPromoted with Confidence=Consensus: candidate
//     (cross-pipeline corroboration produced consensus, the
//     reasoning chain is precedent-quality).
//   - ActionValidationAccepted at Consensus confidence: candidate
//     (an inspector accepted the work; reasoning chain leading here
//     is precedent).
//
// Other activities are skipped. Atomic and Fine resolutions never
// reach the forest by design — they're operational telemetry, not
// precedent.
func (f *ForestSubscriber) Receive(ctx context.Context, a activity.AgentActivity) {
	if !a.Resolution.ShouldHarvestForest() {
		f.skipped.Add(1)
		return
	}
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

func (f *ForestSubscriber) electCandidate(a activity.AgentActivity) (string, bool) {
	switch a.Action {
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
