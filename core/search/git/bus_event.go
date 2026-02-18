package git

import "time"

// EventPhase distinguishes pre- and post-operation events.
type EventPhase int

const (
	// PhasePost is emitted after the operation completes (default).
	PhasePost EventPhase = iota
	// PhasePre is emitted before the operation starts.
	PhasePre
)

// GitEvent describes a single bus-observed git operation.
type GitEvent struct {
	// Op identifies the operation.
	Op GitOp

	// Phase indicates whether this event fires before or after the operation.
	Phase EventPhase

	// Params holds the operation-specific input.
	// nil for zero-parameter operations.
	Params any

	// Result holds the primary return value.
	// nil for PhasePre events, error-only returns, and void operations.
	Result any

	// Err is the operation error, nil on success.
	Err error

	// Timestamp is when the event was created.
	Timestamp time.Time

	// Duration measures wall-clock time for PhasePost events.
	// Zero for PhasePre events.
	Duration time.Duration
}

// OK returns true when the operation succeeded (Err == nil).
func (e *GitEvent) OK() bool { return e.Err == nil }

// IsMutation returns true when the operation modifies repository state.
func (e *GitEvent) IsMutation() bool { return IsMutating(e.Op) }
