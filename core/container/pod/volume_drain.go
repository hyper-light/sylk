package pod

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

var (
	// ErrVolumeDraining is returned by BeginVolumeOp when the volume is
	// already in the drain phase of UnmountAll. Callers must not start
	// new operations against the volume — the unmount is committed.
	ErrVolumeDraining = errors.New("pod: volume is draining; new operations are rejected")

	// ErrDrainTimeout is returned by UnmountAll when in-flight ops on a
	// volume do not drain within the deadline. Replaces the prior silent
	// swallow; callers can now distinguish a clean unmount from one that
	// raced live writers, and react (escalate, retry, surface to the
	// architect) rather than silently dropping work.
	ErrDrainTimeout = errors.New("pod: volume drain timeout; in-flight operations did not complete")
)

// drainTracker counts in-flight operations on one volume and provides
// a one-shot drain barrier used by UnmountAll. The counter is atomic
// so BeginVolumeOp / endOp cost a single Add on the hot path.
//
// Lifecycle:
//   - Normal: draining=false, BeginVolumeOp succeeds, endOp decrements.
//   - Draining: draining=true, BeginVolumeOp returns ErrVolumeDraining,
//     endOp decrements and signals waiters when inflight hits zero.
//
// The drain channel is created on first WaitDrain call and reused; only
// one drain can be in progress per tracker (UnmountAll is serialized
// by the VolumeManager's transitionMu).
type drainTracker struct {
	inflight atomic.Int64
	draining atomic.Bool
	mu       sync.Mutex
	drainCh  chan struct{}
}

func newDrainTracker() *drainTracker {
	return &drainTracker{}
}

// BeginVolumeOp registers a new in-flight operation. Returns a release
// function the caller MUST call (typically with defer) when the op
// completes. Returns ErrVolumeDraining if the volume is already
// draining; in that case release is a no-op and must still be called.
func (t *drainTracker) BeginVolumeOp() (release func(), err error) {
	if t.draining.Load() {
		return func() {}, ErrVolumeDraining
	}
	t.inflight.Add(1)
	// Re-check after the increment: if drain raced our begin, the
	// decrement is still required to let the drain finish.
	if t.draining.Load() {
		t.endOp()
		return func() {}, ErrVolumeDraining
	}
	return t.endOp, nil
}

// endOp decrements the in-flight counter and signals the drain channel
// when the count reaches zero in draining mode.
func (t *drainTracker) endOp() {
	if t.inflight.Add(-1) == 0 && t.draining.Load() {
		t.signalDrainComplete()
	}
}

// signalDrainComplete closes the drain channel exactly once. Idempotent
// across multiple endOp calls that race the drain transition.
func (t *drainTracker) signalDrainComplete() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.drainCh != nil {
		select {
		case <-t.drainCh:
			// already closed
		default:
			close(t.drainCh)
		}
	}
}

// startDrain transitions the tracker into draining mode. After this
// call, BeginVolumeOp returns ErrVolumeDraining. Returns the channel
// callers should select on to wait for drain completion (closed when
// inflight hits zero).
func (t *drainTracker) startDrain() <-chan struct{} {
	t.mu.Lock()
	if t.drainCh == nil {
		t.drainCh = make(chan struct{})
	}
	ch := t.drainCh
	t.mu.Unlock()

	t.draining.Store(true)
	// If no ops are in flight at the moment we transition, signal
	// immediately so WaitDrain returns without blocking.
	if t.inflight.Load() == 0 {
		t.signalDrainComplete()
	}
	return ch
}

// WaitDrain blocks until inflight hits zero or ctx is cancelled. Must
// be preceded by startDrain. Returns ctx.Err() (typically
// DeadlineExceeded → wraps to ErrDrainTimeout in the caller) on
// timeout.
func (t *drainTracker) WaitDrain(ctx context.Context) error {
	ch := t.startDrain()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// resetForRemount returns the tracker to the non-draining state after
// an UnmountAll completes. Called by VolumeManager so the next
// MountAll can begin accepting ops again.
func (t *drainTracker) resetForRemount() {
	t.mu.Lock()
	t.drainCh = nil
	t.mu.Unlock()
	t.draining.Store(false)
}
