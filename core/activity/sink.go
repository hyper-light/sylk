package activity

import (
	"context"
	"sync"
	"sync/atomic"
)

// Sink is the destination for emitted activities. Implementations vary
// by deployment surface:
//
//   - The production sink (built in tier 1.5) is dual-tier: Ristretto
//     for Atomic + Fine + a hot recent-N cache for everything; SQLite
//     agent_activity for Fine (ephemeral hour-partitioned) + Medium +
//     Coarse (durable). It also fans out to async subscribers (Bleve,
//     vectorgraphdb, Memory Forest, scribe) via the channel bus.
//
//   - The test sink is in-memory only, captures every emitted activity
//     in order, and exposes a snapshot for assertions.
//
//   - The discard sink ignores everything; used when fabric capture is
//     intentionally disabled or before the production sink is wired up.
//
// Append must be safe for concurrent callers. Implementations should
// never block the caller for I/O; durable writes belong in the source
// store's RunInWriteTx, not here. When called from a chokepoint span,
// Append's contract is "best-effort persistent emission with bounded
// hot-cache visibility" — losing the activity is preferable to blocking
// the source operation.
type Sink interface {
	Append(ctx context.Context, activity AgentActivity)
}

// ─── Default sink registry ────────────────────────────────────────────
//
// The fabric uses a process-wide default sink because chokepoint
// instrumentation lives inside packages (database, events, providers,
// versioning, purevfs, commandapproval) that cannot be expected to
// thread a sink through every call signature. Tests substitute a sink
// via SetDefaultSink with deferred restoration.

var (
	defaultSinkMu sync.RWMutex
	defaultSink   Sink = discardSink{}
	emissionsTot  atomic.Uint64
)

// DefaultSink returns the currently-installed process-wide sink. Never
// returns nil — falls back to a discard sink if nothing is installed.
func DefaultSink() Sink {
	defaultSinkMu.RLock()
	s := defaultSink
	defaultSinkMu.RUnlock()
	if s == nil {
		return discardSink{}
	}
	return s
}

// SetDefaultSink installs sink as the process-wide default and returns
// the previous sink so callers can defer-restore in tests.
//
// Production wiring calls SetDefaultSink exactly once at startup with
// the dual-tier sink. Tests may swap it temporarily; concurrent test
// runs that need isolation should rely on the test sink's per-instance
// snapshots rather than process-wide installation.
func SetDefaultSink(sink Sink) (previous Sink) {
	if sink == nil {
		sink = discardSink{}
	}
	defaultSinkMu.Lock()
	previous = defaultSink
	defaultSink = sink
	defaultSinkMu.Unlock()
	return previous
}

// EmissionCount returns the total number of activities ever appended
// through DefaultSink during this process. Useful for production
// telemetry and as a smoke-test signal that capture is wired up.
func EmissionCount() uint64 {
	return emissionsTot.Load()
}

// Append routes an activity to the default sink. Increments the
// process-wide counter unconditionally so metrics reflect emission
// intent even when sinks discard.
func Append(ctx context.Context, a AgentActivity) {
	emissionsTot.Add(1)
	DefaultSink().Append(ctx, a)
}

// ─── Discard sink ─────────────────────────────────────────────────────

type discardSink struct{}

func (discardSink) Append(context.Context, AgentActivity) {}

// ─── Test collector sink ──────────────────────────────────────────────

// TestCollector is an in-memory Sink implementation for tests. It
// records every appended activity in order and exposes a snapshot for
// assertions. Safe for concurrent appenders.
type TestCollector struct {
	mu         sync.Mutex
	activities []AgentActivity
}

// NewTestCollector returns a new test collector with no activities.
func NewTestCollector() *TestCollector {
	return &TestCollector{}
}

// Append records the activity.
func (c *TestCollector) Append(_ context.Context, a AgentActivity) {
	c.mu.Lock()
	c.activities = append(c.activities, a)
	c.mu.Unlock()
}

// Snapshot returns a copy of all recorded activities in append order.
func (c *TestCollector) Snapshot() []AgentActivity {
	c.mu.Lock()
	out := make([]AgentActivity, len(c.activities))
	copy(out, c.activities)
	c.mu.Unlock()
	return out
}

// Reset clears all recorded activities.
func (c *TestCollector) Reset() {
	c.mu.Lock()
	c.activities = nil
	c.mu.Unlock()
}

// Count returns the number of recorded activities.
func (c *TestCollector) Count() int {
	c.mu.Lock()
	n := len(c.activities)
	c.mu.Unlock()
	return n
}

// CountByKind returns the number of recorded activities matching kind.
func (c *TestCollector) CountByKind(kind ActionKind) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, a := range c.activities {
		if a.Action == kind {
			n++
		}
	}
	return n
}

// FilterByKind returns a copy of recorded activities matching kind, in
// append order.
func (c *TestCollector) FilterByKind(kind ActionKind) []AgentActivity {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []AgentActivity
	for _, a := range c.activities {
		if a.Action == kind {
			out = append(out, a)
		}
	}
	return out
}
