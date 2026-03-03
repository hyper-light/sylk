package shared

import "context"

// RequestSerializer is a channel-based semaphore(1) that ensures at most one
// forwarded request executes at a time per agent. Cancel actions do NOT acquire
// the serializer — they only fire cancel() on the active request's context.
// The old request detects ctx.Err(), unwinds through its defer chain (including
// Release()), and THEN the new request's Acquire succeeds.
type RequestSerializer struct {
	gate chan struct{} // capacity 1
}

// NewRequestSerializer creates a serializer with the token pre-seeded.
func NewRequestSerializer() *RequestSerializer {
	gate := make(chan struct{}, 1)
	gate <- struct{}{} // seed the token
	return &RequestSerializer{gate: gate}
}

// Acquire blocks until the previous request's Release() runs, or ctx is done.
// Returns false if ctx was cancelled before acquiring.
func (rs *RequestSerializer) Acquire(ctx context.Context) bool {
	select {
	case <-rs.gate:
		return true
	case <-ctx.Done():
		return false
	}
}

// Release returns the token to the gate. Idempotent — a double-release is a
// no-op (defensive against panics in defer chains).
func (rs *RequestSerializer) Release() {
	select {
	case rs.gate <- struct{}{}:
	default: // double-release is a no-op
	}
}
