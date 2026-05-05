package container

import (
	"sync"
	"sync/atomic"
	"time"
)

// PodRestartCoordinator aggregates per-container restart events to a
// pod-level decision: when one agent in a pipeline pod restarts
// repeatedly, the per-container backoff in RestartController only
// punishes that one agent — but in a pipeline pod, restarting one
// agent into broken peer state is worse than demoting the whole pod
// and letting the architect author a corrective action (the canonical
// authority for remediation per the project's standing rule).
//
// Singletons and daemons keep using the per-container RestartController
// directly; only pipeline pods route through this coordinator.
//
// The coordinator does not perform the demote — it only reports
// whether the pod has crossed the failure threshold. The lifecycle
// layer is responsible for translating that into a demote + an
// architect-authored corrective action.
type PodRestartCoordinator struct {
	threshold int           // back-to-back failures within window before pod demotes
	window    time.Duration // sliding window for "back to back"

	mu     sync.Mutex
	events []podRestartEvent // sorted by time, pruned on insert
}

// podRestartEvent is one container restart in the pod.
type podRestartEvent struct {
	ContainerID string
	At          time.Time
}

// PodRestartCoordinatorConfig configures the coordinator.
type PodRestartCoordinatorConfig struct {
	// Threshold is the number of failures within Window required to
	// trigger a pod-level demote signal. Default: 3.
	Threshold int
	// Window is the sliding window over which Threshold counts.
	// Default: 60 seconds — long enough to catch a tight crash loop
	// but short enough that genuine intermittent failures don't trip.
	Window time.Duration
}

// DefaultPodRestartCoordinatorConfig returns sensible defaults.
func DefaultPodRestartCoordinatorConfig() PodRestartCoordinatorConfig {
	return PodRestartCoordinatorConfig{
		Threshold: 3,
		Window:    60 * time.Second,
	}
}

// NewPodRestartCoordinator constructs a coordinator with the given
// config; zero values fall back to defaults.
func NewPodRestartCoordinator(cfg PodRestartCoordinatorConfig) *PodRestartCoordinator {
	if cfg.Threshold <= 0 {
		cfg.Threshold = 3
	}
	if cfg.Window <= 0 {
		cfg.Window = 60 * time.Second
	}
	return &PodRestartCoordinator{
		threshold: cfg.Threshold,
		window:    cfg.Window,
	}
}

// RecordRestart logs a per-container restart in the pod and reports
// whether the pod has crossed the demote threshold. The returned
// boolean is the demote signal — true means the lifecycle layer
// should stop the pod and request architect-authored remediation
// rather than continuing per-container backoff.
//
// RecordRestart prunes events older than the window before counting
// so a long-quiet pod that finally hiccups doesn't carry stale events.
func (p *PodRestartCoordinator) RecordRestart(containerID string) (shouldDemote bool) {
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()

	cutoff := now.Add(-p.window)
	keep := p.events[:0]
	for _, ev := range p.events {
		if ev.At.After(cutoff) {
			keep = append(keep, ev)
		}
	}
	p.events = append(keep, podRestartEvent{ContainerID: containerID, At: now})

	return len(p.events) >= p.threshold
}

// RecentFailures returns the events still inside the window. Used by
// the architect when authoring corrective action so it has the
// container-level breakdown of which agents failed and when.
func (p *PodRestartCoordinator) RecentFailures() []podRestartEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := time.Now().Add(-p.window)
	out := make([]podRestartEvent, 0, len(p.events))
	for _, ev := range p.events {
		if ev.At.After(cutoff) {
			out = append(out, ev)
		}
	}
	return out
}

// Reset clears the failure history. Called after a successful demote
// + remediation cycle so the next promotion starts with a clean
// slate.
func (p *PodRestartCoordinator) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = nil
}

// demotePending is a one-shot signal that a coordinator has tripped.
// Embedded in higher-level types that need to coalesce racing
// "demote requested" calls into a single action.
type demotePending struct {
	tripped atomic.Bool
}

// trip sets the pending flag; returns true on the first trip so the
// caller knows it owns the demote request.
func (d *demotePending) trip() bool {
	return d.tripped.CompareAndSwap(false, true)
}

// reset clears the pending flag for the next cycle.
func (d *demotePending) reset() {
	d.tripped.Store(false)
}
