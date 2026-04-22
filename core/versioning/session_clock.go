package versioning

import (
	"errors"
	"sync"
	"time"
)

// ErrSessionClockOpen is returned by SeedAccumulated when the clock
// is already running. Seeding a running clock would corrupt the
// accumulator by overwriting the current interval's progress, so the
// call is rejected. Callers (SessionVFS replay) seed BEFORE Start,
// so this error indicates a programming ordering bug upstream.
var ErrSessionClockOpen = errors.New("session clock: seed attempted while open")

// SessionClock is a monotonic wall-clock accumulator that advances
// only while the session is open. It's the timebase for
// docs/PARALLEL_GLOBAL_VFS.md §8 SLA timers: a rejected merge whose
// architect remediation SLA is "15 minutes" must not advance the
// timer while the session is closed, because the architect cannot
// act during that interval.
//
// Semantics:
//   - Start(now) marks the session as open as of `now`.
//   - Stop(now) marks it as closed; the interval [lastStart, now] is
//     folded into the accumulator.
//   - Elapsed(now) returns the total open time as of `now`. When the
//     session is currently open, the result includes the in-progress
//     interval.
//   - All operations are safe for concurrent use.
//
// The clock is independent of wall-clock drift; it integrates
// real-time deltas between Start/Stop events. Tests inject a
// deterministic time source via the `now` parameters.
type SessionClock struct {
	mu          sync.Mutex
	accumulated time.Duration
	openSince   time.Time // zero when closed
}

// NewSessionClock constructs a clock in the closed state. Call Start
// to begin accumulating.
func NewSessionClock() *SessionClock {
	return &SessionClock{}
}

// Start marks the session as open as of `now`. Idempotent — calling
// Start while already open is a no-op (the earlier openSince wins).
func (c *SessionClock) Start(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.openSince.IsZero() {
		c.openSince = now
	}
}

// Stop marks the session as closed at `now`, folding the current
// open interval into the accumulator. Idempotent — calling Stop
// while already closed is a no-op.
func (c *SessionClock) Stop(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.openSince.IsZero() {
		return
	}
	if delta := now.Sub(c.openSince); delta > 0 {
		c.accumulated += delta
	}
	c.openSince = time.Time{}
}

// Elapsed returns the total session-open time as of `now`. When the
// clock is open, the result includes the in-progress interval.
func (c *SessionClock) Elapsed(now time.Time) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := c.accumulated
	if !c.openSince.IsZero() {
		if delta := now.Sub(c.openSince); delta > 0 {
			total += delta
		}
	}
	return total
}

// IsOpen reports whether the clock is currently accumulating.
func (c *SessionClock) IsOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.openSince.IsZero()
}

// Mark captures a point-in-time open-clock value. Used by SLA
// trackers: record a Mark when the SLA-bearing event (e.g. a merge
// rejection) lands; later compute (Elapsed(now) - mark) to get the
// open-time-only delta.
type Mark struct {
	at time.Duration
}

// Since returns the open-time duration between the mark and now. The
// delta counts only wall-clock time during which the session was
// open; intervals during which the session was closed are excluded.
func (m Mark) Since(clock *SessionClock, now time.Time) time.Duration {
	if clock == nil {
		return 0
	}
	elapsed := clock.Elapsed(now)
	if elapsed <= m.at {
		return 0
	}
	return elapsed - m.at
}

// MarkNow captures an open-clock Mark at the current open-time
// cumulative position. The returned Mark is stable across
// session-close / session-open cycles.
func (c *SessionClock) MarkNow(now time.Time) Mark {
	return Mark{at: c.Elapsed(now)}
}

// Accumulated returns the total accumulated open-time from finalized
// intervals (excluding any currently-open interval). Used by clock
// reconstruction after WAL replay — the caller replays past epoch
// pairs by calling SeedAccumulated with the reconstructed sum, then
// Start(now) to begin the new interval.
func (c *SessionClock) Accumulated() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accumulated
}

// SeedAccumulated sets the accumulated open-time to `d`. Used by
// SessionVFS.Open's WAL replay to reconstruct the clock from prior
// session-open / session-close pairs before the new session-open
// interval begins. Returns ErrSessionClockOpen if called while the
// clock is currently running — seeding a running clock would corrupt
// the in-progress interval. Callers must seed BEFORE Start.
//
// Negative durations are coerced to zero (no "negative accumulated
// time" invariant violation).
func (c *SessionClock) SeedAccumulated(d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.openSince.IsZero() {
		return ErrSessionClockOpen
	}
	if d < 0 {
		d = 0
	}
	c.accumulated = d
	return nil
}
