package shared

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/versioning"
)

// SessionAuditWiring holds the per-session audit-loop machinery
// (MergeAuditCoordinator, AuditRejectionSLATracker) that the
// session-lifecycle wiring in cmd/tui.go attaches to each
// SessionVFS. Per docs/PARALLEL_GLOBAL_VFS.md §6.4, these live
// alongside the session they audit, with matching lifetimes.
//
// Wiring ownership: constructed by StartSessionAuditWiring, torn
// down by Stop. Callers register the tear-down via
// Orchestrator.RegisterSessionCloseHook.
type SessionAuditWiring struct {
	Coordinator *MergeAuditCoordinator
	SLATracker  *AuditRejectionSLATracker
	scope       *concurrency.GoroutineScope
	ownedScope  bool
}

// SessionAuditWiringConfig configures the wiring.
type SessionAuditWiringConfig struct {
	// Session is the SessionVFS to attach to. Required.
	Session *versioning.SessionVFS
	// Inspector / Tester are the direct-dispatch spawners the
	// coordinator invokes on every merge callback. Required —
	// without both, the coordinator refuses to Start.
	Inspector AuditReplicaSpawner
	Tester    AuditReplicaSpawner
	// Bus is the observability event bus. Audit results publish on
	// AuditMergeResultTopic; SLA breaches publish on
	// AuditRejectionSLABreachTopic. Nil is acceptable (components
	// degrade to log-only).
	Bus guide.EventBus
	// Logger is the structured logger. Optional.
	Logger *slog.Logger
	// Scope is the parent GoroutineScope this wiring's child
	// goroutines run in. When nil, a fresh child scope is created
	// named "session-audit-wiring:<session_id>" and torn down in
	// Stop.
	Scope *concurrency.GoroutineScope
	// SLAThreshold configures the rejection→remediation SLA. Zero
	// disables SLA tracking entirely (no tracker is started). See
	// docs/PARALLEL_GLOBAL_VFS.md §8.
	SLAThreshold time.Duration
	// SLAPoll is how often the tracker re-evaluates marks. Zero
	// uses the tracker's default.
	SLAPoll time.Duration
}

// StartSessionAuditWiring instantiates and starts a
// MergeAuditCoordinator + optional AuditRejectionSLATracker scoped
// to the supplied session. The returned wiring's Stop method tears
// them down in the correct order.
//
// Call this from the session-open hook in cmd/tui.go:
//
//	orch.RegisterSessionOpenHook(func(svfs *versioning.SessionVFS) {
//	    w, err := shared.StartSessionAuditWiring(ctx, shared.SessionAuditWiringConfig{...})
//	    if err != nil { ... }
//	    wiringsMu.Lock(); wirings[svfs] = w; wiringsMu.Unlock()
//	})
//	orch.RegisterSessionCloseHook(func(svfs *versioning.SessionVFS) {
//	    wiringsMu.Lock(); w := wirings[svfs]; delete(wirings, svfs); wiringsMu.Unlock()
//	    if w != nil { w.Stop() }
//	})
func StartSessionAuditWiring(ctx context.Context, cfg SessionAuditWiringConfig) (*SessionAuditWiring, error) {
	if cfg.Session == nil {
		return nil, fmt.Errorf("session audit wiring: Session required")
	}
	if cfg.Inspector == nil || cfg.Tester == nil {
		return nil, fmt.Errorf("session audit wiring: Inspector and Tester required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	scope := cfg.Scope
	ownedScope := false
	if scope == nil {
		scope = concurrency.NewGoroutineScope(ctx, "session-audit-wiring:"+string(cfg.Session.SessionID()), nil)
		ownedScope = true
	}

	coord := NewMergeAuditCoordinator(MergeAuditCoordinatorConfig{
		Session:   cfg.Session,
		Inspector: cfg.Inspector,
		Tester:    cfg.Tester,
		Bus:       cfg.Bus,
		Logger:    logger,
	})
	// Attach the scope to ctx so downstream dispatches (re-audit
	// spawns, finalizer callbacks, addendum publications) can run
	// under the same tracked scope the wiring owns. Without this,
	// asynchronous work the coordinator triggers would have to fall
	// back to untracked goroutines or inherit no scope at all.
	scopedCtx := concurrency.WithScope(ctx, scope)
	if err := coord.Start(scopedCtx); err != nil {
		if ownedScope {
			_ = scope.Shutdown(100*time.Millisecond, 2*time.Second)
		}
		return nil, fmt.Errorf("start merge audit coordinator: %w", err)
	}

	// Resume in-flight replicas from the reloaded lifecycle log so
	// restarted sessions rehydrate their pending audits. Best-effort —
	// failures are logged, not propagated, because a partial resume
	// doesn't justify refusing to open the session.
	if err := coord.ResumeInFlightReplicas(scopedCtx); err != nil {
		logger.Warn("session audit wiring: resume replicas failed",
			"session_id", string(cfg.Session.SessionID()),
			"error", err.Error(),
		)
	}

	w := &SessionAuditWiring{
		Coordinator: coord,
		scope:       scope,
		ownedScope:  ownedScope,
	}

	if cfg.SLAThreshold > 0 {
		tracker := NewAuditRejectionSLATracker(AuditRejectionSLATrackerConfig{
			Session:   cfg.Session,
			Bus:       cfg.Bus,
			Threshold: cfg.SLAThreshold,
			Poll:      cfg.SLAPoll,
			Scope:     scope,
			Logger:    logger,
		})
		if err := tracker.Start(scopedCtx); err != nil {
			coord.Stop()
			if ownedScope {
				_ = scope.Shutdown(100*time.Millisecond, 2*time.Second)
			}
			return nil, fmt.Errorf("start audit sla tracker: %w", err)
		}
		w.SLATracker = tracker
	}

	return w, nil
}

// Stop tears down the wiring in the correct order:
//   - SLA tracker first (stops emitting escalations).
//   - Coordinator second (stops dispatching new audits; in-flight
//     finalizers may still fire but won't hit a dead queue because
//     the session hasn't closed yet).
//   - Owned goroutine scope last.
//
// Idempotent.
func (w *SessionAuditWiring) Stop() {
	if w == nil {
		return
	}
	if w.SLATracker != nil {
		w.SLATracker.Stop()
		w.SLATracker = nil
	}
	if w.Coordinator != nil {
		w.Coordinator.Stop()
		w.Coordinator = nil
	}
	if w.ownedScope && w.scope != nil {
		_ = w.scope.Shutdown(500*time.Millisecond, 5*time.Second)
		w.scope = nil
	}
}

// SessionAuditWiringRegistry tracks active wirings per session so
// the close hook can look up the right one for teardown. Thread-safe.
type SessionAuditWiringRegistry struct {
	mu       sync.Mutex
	wirings  map[versioning.SessionID]*SessionAuditWiring
}

// NewSessionAuditWiringRegistry constructs an empty registry.
func NewSessionAuditWiringRegistry() *SessionAuditWiringRegistry {
	return &SessionAuditWiringRegistry{
		wirings: make(map[versioning.SessionID]*SessionAuditWiring),
	}
}

// Register stores the wiring under the session's ID.
func (r *SessionAuditWiringRegistry) Register(id versioning.SessionID, w *SessionAuditWiring) {
	if r == nil || w == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.wirings[id] = w
}

// RemoveAndStop looks up the wiring, removes it, and calls Stop.
// Safe to call for unknown sessions.
func (r *SessionAuditWiringRegistry) RemoveAndStop(id versioning.SessionID) {
	if r == nil {
		return
	}
	r.mu.Lock()
	w, ok := r.wirings[id]
	if ok {
		delete(r.wirings, id)
	}
	r.mu.Unlock()
	if ok && w != nil {
		w.Stop()
	}
}

// StopAll tears down every registered wiring. Used on shutdown so no
// wiring outlives its session.
func (r *SessionAuditWiringRegistry) StopAll() {
	if r == nil {
		return
	}
	r.mu.Lock()
	wirings := r.wirings
	r.wirings = make(map[versioning.SessionID]*SessionAuditWiring)
	r.mu.Unlock()
	for _, w := range wirings {
		if w != nil {
			w.Stop()
		}
	}
}
