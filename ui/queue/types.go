package queue

import "time"

// EntryState tracks the lifecycle of a queued prompt.
type EntryState uint8

const (
	StatePending     EntryState = iota // Waiting in queue.
	StateDispatching                   // Being dispatched to Guide bus.
	StateActive                        // Actively streaming a response.
	StateCompleted                     // Response finished successfully.
	StateCancelled                     // Cancelled by user.
	StateFailed                        // Terminal error during processing.
)

// IsTerminal reports whether the state is a final state.
func (s EntryState) IsTerminal() bool {
	return s >= StateCompleted
}

// String returns a human-readable label for the state.
func (s EntryState) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateDispatching:
		return "dispatching"
	case StateActive:
		return "active"
	case StateCompleted:
		return "completed"
	case StateCancelled:
		return "cancelled"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Entry represents a single queued prompt awaiting dispatch.
type Entry struct {
	ID            string
	Text          string     // Raw prompt text.
	TargetAgent   string     // Empty = auto-route, "@agent" = explicit.
	SessionID     string
	State         EntryState
	CreatedAt     time.Time
	DispatchedAt  time.Time  // Zero until dispatched.
	CorrelationID string     // Set when dispatched to Guide bus.
}
