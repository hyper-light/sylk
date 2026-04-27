package activation

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container/pod"
)

// idlePollMinDivisor is the recovery-floor divisor applied to
// PolicyDefaults().IdleToWarm to bound the minimum interval between
// scans. Derived from the canonical shortest threshold so that a
// missed Signal() — possible only via a race between StoreTier and
// the wake channel send — is recovered within at most one
// (IdleToWarm / 6) window. Anchoring to PolicyDefaults keeps the
// floor in sync if those defaults change.
const idlePollMinDivisor = 6

// IdleMonitor schedules tier demotion using adaptive polling: the
// next timer fire is computed from each entry's remaining
// time-to-threshold across registered policies. When no entries are
// demotable the timer relaxes to the longest configured threshold;
// when an entry is approaching its threshold the timer tightens to
// that exact moment.
//
// Signal() lets controller state changes (EnsureActive promotion,
// AdoptContainer, RegisterPolicy, Stop) collapse the timer
// immediately rather than waiting for the previously-computed fire.
// The wake channel is cap-1 (coalescing): multiple Signals between
// scans collapse to one.
//
// The previous fixed-period (shortest/6) scanner ran ~12×/min even
// when every entry was at MinTier and nothing could be demoted. The
// adaptive design polls only when work is actually due: a fully-idle
// system polls once per maxPoll (default IdleToCold = 30 min),
// returning idle CPU to baseline.
type IdleMonitor struct {
	controller *ActivationController
	scope      *concurrency.GoroutineScope
	logger     *slog.Logger
	minPoll    time.Duration
	maxPoll    time.Duration
	wake       chan struct{}
	stopped    atomic.Bool
}

// NewIdleMonitor constructs a monitor with adaptive polling bounds
// derived from PolicyDefaults — minPoll = IdleToWarm / idlePollMinDivisor
// (recovery floor for missed Signals), maxPoll = IdleToCold (longest
// threshold; no eligible demotion can require attention later than
// this when the system is fully idle).
//
// The scanPeriod parameter is retained for backward signature
// compatibility but is now ignored; period is computed per scan from
// observed entry state instead of being a fixed cadence.
func NewIdleMonitor(controller *ActivationController, scope *concurrency.GoroutineScope, _ time.Duration) *IdleMonitor {
	defaults := DefaultPolicyDefaults()
	return &IdleMonitor{
		controller: controller,
		scope:      scope,
		logger:     controller.logger,
		minPoll:    defaults.IdleToWarm / idlePollMinDivisor,
		maxPoll:    defaults.IdleToCold,
		wake:       make(chan struct{}, 1),
	}
}

// Signal coalescingly nudges the loop to scan now. Safe from any
// goroutine; never blocks (cap-1 channel + non-blocking send absorbs
// concurrent signals between scans).
func (im *IdleMonitor) Signal() {
	if im == nil {
		return
	}
	select {
	case im.wake <- struct{}{}:
	default:
	}
}

// Start launches the loop as a tracked goroutine.
func (im *IdleMonitor) Start() error {
	return im.scope.Go("activation-idle-monitor", 0, im.loop)
}

// Stop signals the monitor to exit on the next iteration. The
// accompanying Signal() unblocks the loop immediately so it observes
// the stopped flag without waiting for the timer.
func (im *IdleMonitor) Stop() {
	im.stopped.Store(true)
	im.Signal()
}

func (im *IdleMonitor) loop(ctx context.Context) error {
	timer := time.NewTimer(im.minPoll)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		case <-im.wake:
			drainTimer(timer)
		}
		if im.stopped.Load() {
			return nil
		}
		next := im.scan(ctx)
		timer.Reset(im.clampPoll(next))
	}
}

// drainTimer halts a timer and discards any pending fire so it can
// be Reset cleanly. Idiomatic guard for the "wake arrived before
// timer.C" path.
func drainTimer(t *time.Timer) {
	if t.Stop() {
		return
	}
	select {
	case <-t.C:
	default:
	}
}

// scan walks all registered entries: demotes those past threshold
// and returns the minimum time-to-next-demotion across the rest,
// used to schedule the next timer fire.
func (im *IdleMonitor) scan(ctx context.Context) time.Duration {
	entries := im.controller.allEntries()
	next := im.maxPoll
	for _, entry := range entries {
		if ctx.Err() != nil {
			return im.maxPoll
		}
		remaining := im.evaluateEntry(ctx, entry)
		if remaining > 0 && remaining < next {
			next = remaining
		}
	}
	return next
}

// evaluateEntry processes one entry: demotes when idle threshold has
// crossed, otherwise returns how long until the entry would become
// eligible for demotion. Returns maxPoll for entries not subject to
// demotion (at MinTier, daemon, no threshold), so they don't pull
// the next-scan target shorter.
func (im *IdleMonitor) evaluateEntry(ctx context.Context, entry *ActivationEntry) time.Duration {
	if entry.HasActiveRequests() {
		entry.TouchActivity()
		return im.maxPoll
	}

	tier := entry.LoadTier()
	policy := entry.Policy
	if tier <= policy.MinTier {
		return im.maxPoll
	}

	threshold := policy.IdleThreshold(tier)
	if threshold <= 0 {
		return im.maxPoll
	}

	idle := entry.IdleDuration()
	if idle < threshold {
		return threshold - idle
	}

	return im.demoteEntry(ctx, entry, tier, policy)
}

// demoteEntry attempts a one-tier demotion and returns the next
// recommended poll delay. minPoll on success enables fast cascade
// re-evaluation; minPoll on error retries soon. maxPoll when the
// policy disallows the target tier (entry is now stuck).
func (im *IdleMonitor) demoteEntry(ctx context.Context, entry *ActivationEntry, tier ActivationTier, policy *ActivationPolicy) time.Duration {
	targetTier := tier - 1
	if !policy.CanDemoteTo(targetTier) {
		return im.maxPoll
	}
	if err := im.controller.DemoteTo(ctx, entry.AgentType, targetTier); err != nil {
		im.logger.Warn("idle demotion failed",
			"agent_type", entry.AgentType,
			"from_tier", pod.TierString(tier),
			"target_tier", pod.TierString(targetTier),
			"error", err,
		)
		return im.minPoll
	}
	return im.minPoll
}

func (im *IdleMonitor) clampPoll(d time.Duration) time.Duration {
	if d < im.minPoll {
		return im.minPoll
	}
	if d > im.maxPoll {
		return im.maxPoll
	}
	return d
}
