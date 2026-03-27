package shared

import (
	"context"
	"errors"
)

// WithoutDeadlineCancellation returns a child context that preserves values
// from parent and forwards explicit cancellation, but ignores parent deadline
// expiry. This keeps long-running agent work alive while activity continues,
// while still allowing shutdowns or user interrupts to stop it.
func WithoutDeadlineCancellation(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		return context.WithCancel(context.Background())
	}

	base := context.WithoutCancel(parent)
	ctx, cancelCause := context.WithCancelCause(base)
	stop := context.AfterFunc(parent, func() {
		cause := context.Cause(parent)
		if cause == nil {
			cause = parent.Err()
		}
		if cause == nil || errors.Is(cause, context.DeadlineExceeded) {
			return
		}
		cancelCause(cause)
	})

	return ctx, func() {
		stop()
		cancelCause(context.Canceled)
	}
}
