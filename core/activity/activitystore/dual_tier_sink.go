package activitystore

import (
	"context"
	"sync"
	"time"

	"github.com/dgraph-io/ristretto"

	"github.com/adalundhe/sylk/core/activity"
)

// Resolution constants re-exported for tier comparisons in this file.
const (
	resolutionAtomic = activity.ResolutionAtomic
	resolutionFine   = activity.ResolutionFine
)

// DualTierSink fans every emitted activity into the appropriate tiers:
//
//   - Atomic & Fine: in-memory ring buffer (ringBuffer below) for sub-µs
//     "what happened in the last N seconds" lens reads. Bounded by
//     capacity; oldest entries drop when the buffer fills.
//   - Fine & Medium & Coarse: durable SQLite via the wrapped Source.
//   - Recent-by-ID: Ristretto cache so causal-trace walks resolve
//     activity references without round-tripping SQLite.
//
// All async fan-out (Bleve, vectorgraphdb, Memory Forest, scribe) is
// performed by separate subscriber goroutines that read the activity
// stream from the SQLite store via the Source interface — they don't
// hook into Append directly. That keeps the hot path bounded to one
// indexed SQLite write + two in-memory map operations.
//
// Compile-time guarantee: DualTierSink implements Sink and Source.
type DualTierSink struct {
	durable *SQLiteStore

	// recent holds Coarse + Medium + Fine activities by ID for fast
	// causal-trace lookups. Bounded; eviction is policy-driven by
	// Ristretto's TinyLFU. Stored as any (the v0.2 ristretto API is
	// not generic) and type-asserted on read.
	recent *ristretto.Cache

	// ring holds the rolling window of Atomic + Fine activities for
	// "what happened in the last N seconds" queries. Append-only with
	// drop-oldest semantics when full.
	ring *ringBuffer
}

// recentEntryCostBytes is the per-entry cost charged to the recent
// cache. Set() in Append uses the same value, so MaxCost / this == max
// items the cache will hold. AgentActivity is a heterogeneous struct
// (8 strings averaging ~30B, time.Time, optional Payload/Evidence/
// CausalChain) — empirical avg sits ~700–1100 B with skew toward larger
// entries when Evidence/CausalChain are populated. A flat 1 KiB matches
// the upper-typical and avoids per-Set marshalling.
const recentEntryCostBytes = 1024

// recentRingMultiplier sizes the recent cache as a multiple of
// RingCapacity. The recent cache is the hit path for causal-trace
// walks (Caused/Resolves chains). Walks predominantly stay within the
// ring window (Atomic+Fine), but Medium/Coarse parents may live just
// outside it. 2× covers cross-resolution walks without hoarding stale
// IDs that TinyLFU would prefer to evict anyway.
const recentRingMultiplier = 2

// DualTierSinkConfig configures the dual-tier sink. Defaults are
// derived from RingCapacity (the natural workload bound) — see
// DefaultDualTierSinkConfig for the derivation chain.
type DualTierSinkConfig struct {
	// RecentCacheItems is the maximum number of activities held in
	// the recent-by-ID cache. Default = RingCapacity × recentRingMultiplier.
	RecentCacheItems int

	// RecentCacheBytes bounds the cache by approximate memory size.
	// Default = RecentCacheItems × recentEntryCostBytes.
	RecentCacheBytes int64

	// RingCapacity is the maximum number of Atomic + Fine activities
	// retained in the rolling-window ring buffer. Defaults to 4096
	// (covers ~5min @ ~13 activities/sec sustained, the upper-typical
	// emission rate observed in single-session traces).
	RingCapacity int

	// RingMaxAge bounds how far back the ring buffer retains entries.
	// Defaults to 5 minutes (matching ResolutionAtomic.EphemeralRetention).
	RingMaxAge time.Duration
}

// DefaultDualTierSinkConfig returns derived defaults: every constant is
// tied to a physical anchor (ring capacity, per-entry cost) so changing
// one input propagates through the cache sizing without re-tuning.
func DefaultDualTierSinkConfig() DualTierSinkConfig {
	const defaultRingCapacity = 4096
	items := defaultRingCapacity * recentRingMultiplier
	return DualTierSinkConfig{
		RecentCacheItems: items,
		RecentCacheBytes: int64(items) * recentEntryCostBytes,
		RingCapacity:     defaultRingCapacity,
		RingMaxAge:       5 * time.Minute,
	}
}

// NewDualTierSink builds a fully-configured dual-tier sink wrapping
// the given durable store. The durable store may be nil during tests
// or when the orchestrator is not yet up; in that case durable writes
// are skipped silently and only the in-memory tiers are exercised.
func NewDualTierSink(durable *SQLiteStore, cfg DualTierSinkConfig) (*DualTierSink, error) {
	cfg = normalizeDualTierConfig(cfg)
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: int64(cfg.RecentCacheItems) * 10,
		MaxCost:     cfg.RecentCacheBytes,
		BufferItems: 64,
	})
	if err != nil {
		return nil, err
	}
	return &DualTierSink{
		durable: durable,
		recent:  cache,
		ring:    newRingBuffer(cfg.RingCapacity, cfg.RingMaxAge),
	}, nil
}

func normalizeDualTierConfig(cfg DualTierSinkConfig) DualTierSinkConfig {
	if cfg.RingCapacity <= 0 {
		cfg.RingCapacity = 4096
	}
	if cfg.RingMaxAge <= 0 {
		cfg.RingMaxAge = 5 * time.Minute
	}
	if cfg.RecentCacheItems <= 0 {
		cfg.RecentCacheItems = cfg.RingCapacity * recentRingMultiplier
	}
	if cfg.RecentCacheBytes <= 0 {
		cfg.RecentCacheBytes = int64(cfg.RecentCacheItems) * recentEntryCostBytes
	}
	return cfg
}

// Append fans the activity to every applicable tier.
func (s *DualTierSink) Append(ctx context.Context, a AgentActivity) {
	if s == nil {
		return
	}
	// Recent-by-ID cache: every resolution lands here so causal-trace
	// walks see the activity even if it's still in the durable
	// write-back pipeline.
	if s.recent != nil {
		// Cost matches the per-entry sizing constant used to derive
		// MaxCost in DefaultDualTierSinkConfig — keep the two in sync
		// so the cache holds exactly the items the bound advertises.
		s.recent.Set(string(a.ID), a, recentEntryCostBytes)
	}

	// Atomic + Fine into the rolling ring buffer.
	if s.ring != nil && (a.Resolution == resolutionAtomic || a.Resolution == resolutionFine) {
		s.ring.append(a)
	}

	// Fine + Medium + Coarse into the durable store.
	if s.durable != nil && a.Resolution.IsDurable() {
		s.durable.Append(ctx, a)
	}
}

// FilterActivities serves Source by routing the query through the
// durable store. The ring buffer is a write-side optimization for hot
// reads, not a primary read path; lens code that wants
// rolling-window data uses the dedicated RecentRolling method below.
func (s *DualTierSink) FilterActivities(ctx context.Context, filter QueryFilter) ([]AgentActivity, error) {
	if s == nil || s.durable == nil {
		return nil, nil
	}
	return s.durable.FilterActivities(ctx, filter)
}

// GetActivity serves Source. Tries the recent cache first; on miss,
// falls back to the durable store.
func (s *DualTierSink) GetActivity(ctx context.Context, id ActivityID) (*AgentActivity, error) {
	if s == nil || id == "" {
		return nil, nil
	}
	if s.recent != nil {
		if raw, ok := s.recent.Get(string(id)); ok {
			if a, ok := raw.(AgentActivity); ok {
				return &a, nil
			}
		}
	}
	if s.durable != nil {
		return s.durable.GetActivity(ctx, id)
	}
	return nil, nil
}

// LatestActivityID serves Source.
func (s *DualTierSink) LatestActivityID(ctx context.Context, filter QueryFilter) (ActivityID, error) {
	if s == nil || s.durable == nil {
		return "", nil
	}
	return s.durable.LatestActivityID(ctx, filter)
}

// RecentRolling returns the rolling-window activities matching the
// filter. Lens implementations use this for "what happened in the last
// N seconds" queries that don't warrant a SQLite scan.
func (s *DualTierSink) RecentRolling(filter QueryFilter) []AgentActivity {
	if s == nil || s.ring == nil {
		return nil
	}
	return s.ring.snapshot(filter)
}

// Close releases the underlying caches. The durable store is owned by
// the orchestrator's BunSQLite handle and is closed by that owner.
func (s *DualTierSink) Close() {
	if s == nil {
		return
	}
	if s.recent != nil {
		s.recent.Close()
	}
}

// Compile-time assertions.
var (
	_ Sink   = (*DualTierSink)(nil)
	_ Source = (*DualTierSink)(nil)
)

// ─── ring buffer for Atomic + Fine rolling-window reads ──────────────

type ringBuffer struct {
	mu       sync.Mutex
	capacity int
	maxAge   time.Duration
	entries  []AgentActivity
	head     int // next write position
	count    int
}

func newRingBuffer(capacity int, maxAge time.Duration) *ringBuffer {
	return &ringBuffer{
		capacity: capacity,
		maxAge:   maxAge,
		entries:  make([]AgentActivity, capacity),
	}
}

func (r *ringBuffer) append(a AgentActivity) {
	if r == nil || r.capacity == 0 {
		return
	}
	r.mu.Lock()
	r.entries[r.head] = a
	r.head = (r.head + 1) % r.capacity
	if r.count < r.capacity {
		r.count++
	}
	r.mu.Unlock()
}

func (r *ringBuffer) snapshot(filter QueryFilter) []AgentActivity {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := time.Now().Add(-r.maxAge)
	if !filter.Since.IsZero() && filter.Since.After(cutoff) {
		cutoff = filter.Since
	}

	var out []AgentActivity
	// Walk in chronological order: from the oldest entry forward.
	start := r.head
	if r.count < r.capacity {
		start = 0
	}
	for i := 0; i < r.count; i++ {
		idx := (start + i) % r.capacity
		a := r.entries[idx]
		if a.Timestamp.Before(cutoff) {
			continue
		}
		if !matchesFilter(a, filter) {
			continue
		}
		out = append(out, a)
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out
}

func matchesFilter(a AgentActivity, f QueryFilter) bool {
	if f.SessionID != "" && a.SessionID != f.SessionID {
		return false
	}
	if f.ActorAgentID != "" && a.Actor.AgentID != f.ActorAgentID {
		return false
	}
	if f.ActorAgentType != "" && a.Actor.AgentType != f.ActorAgentType {
		return false
	}
	if f.ActorPipelineID != "" && a.Actor.PipelineID != f.ActorPipelineID {
		return false
	}
	if len(f.ActionKinds) > 0 && !containsKind(f.ActionKinds, a.Action) {
		return false
	}
	if len(f.States) > 0 && !containsState(f.States, a.State) {
		return false
	}
	if f.SubjectPathPrefix != "" {
		// Match prefix relationship in either direction so that a
		// broad query (PathPrefix="services/") matches activities at
		// "services/billing/" and a narrow query
		// (PathPrefix="services/billing/api.go") matches activities
		// scoped to "services/billing/".
		if !hasPathOverlap(a.Subject.PathPrefix, f.SubjectPathPrefix) {
			return false
		}
	}
	if f.SubjectTargetAgent != "" && a.Subject.TargetAgent != f.SubjectTargetAgent {
		return false
	}
	if f.SubjectTargetArtifact != "" && a.Subject.TargetArtifact != f.SubjectTargetArtifact {
		return false
	}
	if f.SubjectDomain != "" && a.Subject.Domain != f.SubjectDomain {
		return false
	}
	if !f.Until.IsZero() && a.Timestamp.After(f.Until) {
		return false
	}
	if len(f.Confidences) > 0 && !containsConfidence(f.Confidences, a.Confidence) {
		return false
	}
	if f.Caused != nil {
		if a.Caused == nil || *a.Caused != *f.Caused {
			return false
		}
	}
	if f.Resolves != nil {
		if a.Resolves == nil || *a.Resolves != *f.Resolves {
			return false
		}
	}
	return true
}

func hasPathOverlap(have, want string) bool {
	if have == "" || want == "" {
		return have == want
	}
	if len(have) >= len(want) {
		return have[:len(want)] == want
	}
	return want[:len(have)] == have
}

func containsKind(s []ActionKind, v ActionKind) bool {
	for _, k := range s {
		if k == v {
			return true
		}
	}
	return false
}

func containsState(s []ActivityState, v ActivityState) bool {
	for _, k := range s {
		if k == v {
			return true
		}
	}
	return false
}

func containsConfidence(s []Confidence, v Confidence) bool {
	for _, k := range s {
		if k == v {
			return true
		}
	}
	return false
}
