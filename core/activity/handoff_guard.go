package activity

import (
	"context"
	"sync"
)

// HandoffGuard is the publish-side enforcement plane for handoff
// activities (UI_DESIGN.md §4.7.1). When set via SetHandoffGuard, the
// global Append path consults the guard for any activity classified
// as a handoff. Refusal causes the activity to be dropped before it
// reaches any sink, with the rejection logged back to the publishing
// agent's testament accumulator.
//
// The guard is intentionally narrow: it sees the actor + session +
// the handoff metadata that an emitter must opt into via
// Subject.Coordinates (key: "handoff_from_claim_id"). This keeps the
// activity package free of any direct dependency on core/claims while
// still allowing the bootstrap to wire a board-aware enforcer.
type HandoffGuard interface {
	// Verify reports nil if the activity is allowed to propagate, or
	// a typed rejection error otherwise. The error's Error() string
	// is logged; the activity is dropped from the propagation path
	// (sinks never see it).
	Verify(ctx context.Context, agentID, sessionID, predecessorClaimID string) error
}

var (
	handoffGuardMu sync.Mutex
	handoffGuard   HandoffGuard
)

// SetHandoffGuard installs the process-wide guard. Returns the
// previous guard for defer-restore in tests. Thread-safe.
func SetHandoffGuard(g HandoffGuard) HandoffGuard {
	handoffGuardMu.Lock()
	defer handoffGuardMu.Unlock()
	prev := handoffGuard
	handoffGuard = g
	return prev
}

func loadHandoffGuard() HandoffGuard {
	handoffGuardMu.Lock()
	defer handoffGuardMu.Unlock()
	return handoffGuard
}

// classifyHandoff inspects an AgentActivity to decide whether it
// should be subject to the publish-side handoff guard. Two signals
// are accepted, matching how dispatchers stamp handoff activities:
//   - Action == ActionHandoffEmitted (canonical signal).
//   - Subject.Coordinates["handoff_from_claim_id"] non-empty (legacy
//     opt-in for activities posted before ActionHandoffEmitted is
//     adopted at every site).
//
// Returns the predecessor claim ID and true when the activity is a
// handoff, or empty string + false when it isn't.
func classifyHandoff(a AgentActivity) (predecessor string, ok bool) {
	if a.Action == ActionHandoffEmitted {
		// Even when the action is HandoffEmitted, we still need the
		// predecessor ID to scope the verify call. Fall back to
		// Subject.Coordinates if not on the action itself.
		if pred := coordValue(a.Subject.Coordinates, "handoff_from_claim_id"); pred != "" {
			return pred, true
		}
		// Action set but no predecessor — treat as a handoff with no
		// predecessor (defensive; guard sees empty predecessor).
		return "", true
	}
	if pred := coordValue(a.Subject.Coordinates, "handoff_from_claim_id"); pred != "" {
		return pred, true
	}
	return "", false
}

func coordValue(coords map[string]string, key string) string {
	if coords == nil {
		return ""
	}
	return coords[key]
}
