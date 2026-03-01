package resources

import (
	"context"
	"time"
)

// DeadlinePropagator implements gRPC-style deadline inheritance.
// A child context's deadline is always min(localTimeout, parent_remaining - CleanupBuffer).
// The buffer reserves time for cleanup/finalization at each level.
// Deadlines never inflate — they only shrink as they propagate down the call chain.
type DeadlinePropagator struct {
	CleanupBuffer time.Duration // reserved for cleanup at this level
}

// Propagate creates a child context whose deadline is the tighter of
// localTimeout and the parent's remaining time minus CleanupBuffer.
// If the parent has no deadline, localTimeout alone applies.
func (dp *DeadlinePropagator) Propagate(parent context.Context, localTimeout time.Duration) (context.Context, context.CancelFunc) {
	deadline, hasDeadline := parent.Deadline()
	if !hasDeadline {
		return context.WithTimeout(parent, localTimeout)
	}
	remaining := time.Until(deadline) - dp.CleanupBuffer
	effective := min(localTimeout, remaining)
	if effective <= 0 {
		// Parent already exhausted — return immediately-cancelled context.
		ctx, cancel := context.WithCancel(parent)
		cancel()
		return ctx, cancel
	}
	return context.WithTimeout(parent, effective)
}
