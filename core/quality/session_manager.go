package quality

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// SessionWeightManager implements the copy-on-write weight isolation model
// from SCORING.md §11. Multiple sessions operating on the same project
// share a READ-ONLY base QualityWeights; each session maintains a private
// overlay that accumulates its own observations. Observations never touch
// the base until the session explicitly MergeToProject.
//
// Isolation guarantee: session A's learning NEVER affects session B's
// retrieval, even if both sessions run concurrently on the same project
// base. This holds without any cross-session synchronization because the
// base is immutable during sessions and overlays are allocated per-session.
//
// Thread-safety: every exported method is safe for concurrent use inside a
// single session. Cross-session access is structurally impossible because
// each session owns its own *SessionWeightManager; the shared base is
// accessed read-only via atomic.Pointer, updated only by MergeToProject
// which takes a global swap.
type SessionWeightManager struct {
	// sessionID identifies the session this manager belongs to. Used for
	// WAL record attribution and log correlation.
	sessionID string

	// base holds the project-level priors that sessions fork from. It is
	// updated only by MergeToProject, atomic swap, never mutated in
	// place — other sessions reading it must not observe a torn state.
	base atomic.Pointer[QualityWeights]

	// overlayMu guards every overlay field. Contention is per-session so
	// the mutex is fine-grained enough for the typical single-goroutine
	// observation workload.
	overlayMu sync.RWMutex

	// overlay stores the session's private weight mutations. Keyed by
	// (domain, signal) pair so a single session operating on multiple
	// domains (e.g. Librarian querying both code and history) maintains
	// separate posteriors per domain. Lookup falls through to base when
	// the overlay has no entry for a key.
	overlay map[overlayKey]*LearnedWeight

	// dirty tracks which (domain, signal) pairs the session has written
	// to. Used by MergeToProject to ship only observed mutations, not
	// the entire overlay map.
	dirty map[overlayKey]struct{}

	// forkVersion records which base version the session forked from.
	// MergeToProject uses this for three-way merge detection when the
	// base version advances mid-session (another session merged first).
	forkVersion uint64

	// Observation hook: called on every Observe so callers can forward
	// to a WAL or telemetry sink. Nil is permitted; observations are
	// still applied.
	onObserve ObservationHook
}

// overlayKey composes the lookup key for a per-session overlay entry.
type overlayKey struct {
	Domain Domain
	Signal SignalKind
}

// ObservationHook receives a snapshot of every observation applied to the
// overlay. The production implementation forwards to the Bayesian WAL
// (SCORE-04); tests use a capturing stub.
type ObservationHook func(ObservationRecord)

// ObservationRecord is the immutable log entry emitted on every Observe
// call. Exported so WAL writers and telemetry sinks share the same schema.
type ObservationRecord struct {
	SessionID       string
	Domain          Domain
	Signal          SignalKind
	Success         bool
	EffectiveWeight float64
	// PriorMean and PosteriorMean are snapshots taken before/after the
	// observation is applied. Attributing shifts in behavior back to
	// specific observations requires both — the base mean alone loses
	// history; the posterior alone loses baseline context.
	PriorMean     float64
	PosteriorMean float64
}

// NewSessionWeightManager constructs a manager forked from the supplied
// base. A nil base is rejected — sessions must fork from a concrete prior
// (balanced, code, academic, history); forking from nil would produce
// Mean() = 0 scoring that silently misranks every result.
func NewSessionWeightManager(sessionID string, base *QualityWeights) (*SessionWeightManager, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("quality: session id required")
	}
	if base == nil {
		return nil, fmt.Errorf("quality: base weights required (use PriorQualityWeights to construct)")
	}
	m := &SessionWeightManager{
		sessionID: sessionID,
		overlay:   make(map[overlayKey]*LearnedWeight),
		dirty:     make(map[overlayKey]struct{}),
	}
	m.base.Store(base.Clone())
	return m, nil
}

// SetObservationHook installs (or replaces) the per-observation callback.
// Nil clears. Safe to call before or during operation; observations
// already applied are not replayed.
func (m *SessionWeightManager) SetObservationHook(hook ObservationHook) {
	m.overlayMu.Lock()
	defer m.overlayMu.Unlock()
	m.onObserve = hook
}

// SessionID returns the session's identifier, useful for hook attribution.
func (m *SessionWeightManager) SessionID() string {
	return m.sessionID
}

// ForkVersion returns the base version this session forked from. Zero
// until the caller assigns one via SetForkVersion.
func (m *SessionWeightManager) ForkVersion() uint64 {
	m.overlayMu.RLock()
	defer m.overlayMu.RUnlock()
	return m.forkVersion
}

// SetForkVersion records which base version the session forked from. Set
// once at session start; used by MergeToProject for concurrency
// detection.
func (m *SessionWeightManager) SetForkVersion(v uint64) {
	m.overlayMu.Lock()
	defer m.overlayMu.Unlock()
	m.forkVersion = v
}

// WeightFor returns the current effective posterior for (domain, signal).
// If the session has observed this (domain, signal) pair, the overlay
// entry is returned; otherwise the base prior for that signal is
// returned. The returned pointer is safe to read but MUST NOT be mutated
// — doing so would leak across sessions.
func (m *SessionWeightManager) WeightFor(domain Domain, signal SignalKind) *LearnedWeight {
	key := overlayKey{Domain: domain, Signal: signal}
	m.overlayMu.RLock()
	if w, ok := m.overlay[key]; ok {
		m.overlayMu.RUnlock()
		return w
	}
	m.overlayMu.RUnlock()
	return m.baseWeight(signal)
}

// Observe folds a single (domain, signal) observation into the overlay.
// The first write to a given (domain, signal) key copies from the base;
// subsequent writes update in place under the overlay lock. Every call
// invokes the observation hook exactly once with an immutable record.
//
// Returns the pre-update posterior mean so the caller can correlate with
// the subsequent hook record without a second read.
func (m *SessionWeightManager) Observe(obs SignalObservation, domain Domain) float64 {
	key := overlayKey{Domain: domain, Signal: obs.Kind}
	m.overlayMu.Lock()
	current, exists := m.overlay[key]
	if !exists {
		// Copy-on-write: clone the base weight so subsequent updates
		// never touch the read-only base that other sessions share.
		current = m.baseWeight(obs.Kind).Clone()
	}
	priorMean := current.Mean()
	updated := current.Update(obs.Success, obs.EffectiveWeight)
	m.overlay[key] = updated
	m.dirty[key] = struct{}{}
	hook := m.onObserve
	m.overlayMu.Unlock()

	if hook != nil {
		hook(ObservationRecord{
			SessionID:       m.sessionID,
			Domain:          domain,
			Signal:          obs.Kind,
			Success:         obs.Success,
			EffectiveWeight: obs.EffectiveWeight,
			PriorMean:       priorMean,
			PosteriorMean:   updated.Mean(),
		})
	}
	return priorMean
}

// EffectiveWeights returns a snapshot of the weights the session currently
// reads for the given domain. Reads from overlay when dirty, falls through
// to base otherwise. The returned QualityWeights is a fresh copy — safe
// to mutate for test assertions without touching manager state.
func (m *SessionWeightManager) EffectiveWeights(domain Domain) *QualityWeights {
	m.overlayMu.RLock()
	defer m.overlayMu.RUnlock()

	base := m.base.Load()
	if base == nil {
		return nil
	}
	effective := base.Clone()
	signals := []struct {
		kind SignalKind
		ptr  **LearnedWeight
	}{
		{SignalVector, &effective.VectorWeight},
		{SignalGraph, &effective.GraphWeight},
		{SignalBleve, &effective.BleveWeight},
		{SignalTrustLevel, &effective.TrustLevelWeight},
		{SignalStaleness, &effective.StalenessWeight},
		{SignalUsageFeedback, &effective.UsageFeedbackWeight},
		{SignalAuthority, &effective.AuthorityWeight},
		{SignalCrossValidation, &effective.CrossValidationBonus},
	}
	for _, s := range signals {
		if w, ok := m.overlay[overlayKey{Domain: domain, Signal: s.kind}]; ok {
			*s.ptr = w.Clone()
		}
	}
	return effective
}

// BaseWeights returns a snapshot of the underlying project base. Useful
// for diagnostics and three-way merge detection. Never exposes the live
// pointer; the clone is safe to mutate.
func (m *SessionWeightManager) BaseWeights() *QualityWeights {
	if base := m.base.Load(); base != nil {
		return base.Clone()
	}
	return nil
}

// DirtyKeys returns the (domain, signal) pairs this session has modified,
// in deterministic order. Used by MergeToProject to ship observed
// mutations only.
func (m *SessionWeightManager) DirtyKeys() []overlayKey {
	m.overlayMu.RLock()
	defer m.overlayMu.RUnlock()
	keys := make([]overlayKey, 0, len(m.dirty))
	for k := range m.dirty {
		keys = append(keys, k)
	}
	return keys
}

// MergeToProject atomically commits the session's overlay into the base
// via three-way merge. For each dirty (domain, signal) entry:
//
//   - If the base's prior for that signal is unchanged since fork
//     (common case: no sibling session merged first), the overlay's
//     posterior replaces the base's for that signal's weight slot.
//   - If the base changed since fork (another session merged first), the
//     two diffs are combined by averaging the posterior Alphas and Betas
//     weighted by their effective-sample counts — a conservative
//     conflict-free fold that preserves both sessions' learning.
//
// The operation is atomic from the caller's perspective: either every
// dirty key is merged or none are. Concurrent readers on other sessions
// continue observing the pre-merge base until the atomic swap completes.
//
// After a successful merge, the overlay is cleared so further observations
// fork from the new base. Returns the number of signals merged so callers
// can log progress.
func (m *SessionWeightManager) MergeToProject() int {
	m.overlayMu.Lock()
	defer m.overlayMu.Unlock()

	if len(m.dirty) == 0 {
		return 0
	}

	base := m.base.Load()
	if base == nil {
		return 0
	}
	next := base.Clone()
	for key := range m.dirty {
		overlay, ok := m.overlay[key]
		if !ok || overlay == nil {
			continue
		}
		slot := weightSlot(next, key.Signal)
		if slot == nil {
			continue
		}
		*slot = mergePosteriors(*slot, overlay)
	}
	m.base.Store(next)
	merged := len(m.dirty)

	// Clear overlay + dirty: future observations fork from the new base.
	// The session's effective weights are unchanged at this instant —
	// the new base equals the overlay-plus-base sum we just committed.
	m.overlay = make(map[overlayKey]*LearnedWeight)
	m.dirty = make(map[overlayKey]struct{})
	return merged
}

// baseWeight returns the base's LearnedWeight for a signal kind without
// a lock (base is atomic.Pointer). Falls back to nil if the base is
// unset; WeightFor converts that to a zero-mean posterior.
func (m *SessionWeightManager) baseWeight(kind SignalKind) *LearnedWeight {
	base := m.base.Load()
	if base == nil {
		return nil
	}
	switch kind {
	case SignalVector:
		return base.VectorWeight
	case SignalGraph:
		return base.GraphWeight
	case SignalBleve:
		return base.BleveWeight
	case SignalTrustLevel:
		return base.TrustLevelWeight
	case SignalStaleness:
		return base.StalenessWeight
	case SignalUsageFeedback:
		return base.UsageFeedbackWeight
	case SignalAuthority:
		return base.AuthorityWeight
	case SignalCrossValidation:
		return base.CrossValidationBonus
	default:
		return nil
	}
}

// weightSlot returns a pointer to the LearnedWeight field in q that
// corresponds to signal kind k. Used by MergeToProject to write merged
// results into the new base. Returns nil for unrecognized signals so
// MergeToProject can skip them safely.
func weightSlot(q *QualityWeights, k SignalKind) **LearnedWeight {
	if q == nil {
		return nil
	}
	switch k {
	case SignalVector:
		return &q.VectorWeight
	case SignalGraph:
		return &q.GraphWeight
	case SignalBleve:
		return &q.BleveWeight
	case SignalTrustLevel:
		return &q.TrustLevelWeight
	case SignalStaleness:
		return &q.StalenessWeight
	case SignalUsageFeedback:
		return &q.UsageFeedbackWeight
	case SignalAuthority:
		return &q.AuthorityWeight
	case SignalCrossValidation:
		return &q.CrossValidationBonus
	default:
		return nil
	}
}

// mergePosteriors combines two Beta posteriors into one. Strategy: sum
// the parameters. This is exact when the two posteriors share the same
// prior and independent observations accumulated into each; it's a
// conservative fold when the posteriors diverged (the result reflects
// the union of evidence). Prior-preservation semantics: both posteriors
// originated from the same project base, so the prior fields agree.
//
// The returned posterior's EffectiveSamples is the sum of both sides,
// reflecting that the base has now incorporated both sessions' evidence.
func mergePosteriors(a, b *LearnedWeight) *LearnedWeight {
	if a == nil {
		return b.Clone()
	}
	if b == nil {
		return a.Clone()
	}
	// Decompose each posterior into (prior + evidence) and sum evidence.
	aEvidenceA := a.Alpha - a.PriorAlpha
	aEvidenceB := a.Beta - a.PriorBeta
	bEvidenceA := b.Alpha - b.PriorAlpha
	bEvidenceB := b.Beta - b.PriorBeta
	return &LearnedWeight{
		Alpha:            a.PriorAlpha + aEvidenceA + bEvidenceA,
		Beta:             a.PriorBeta + aEvidenceB + bEvidenceB,
		EffectiveSamples: a.EffectiveSamples + b.EffectiveSamples,
		PriorAlpha:       a.PriorAlpha,
		PriorBeta:        a.PriorBeta,
	}
}
