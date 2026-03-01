package activation

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

// IdleMonitor periodically scans activation entries and demotes idle
// agents to lower tiers based on their policy thresholds.
type IdleMonitor struct {
	controller *ActivationController
	scope      *concurrency.GoroutineScope
	logger     *slog.Logger
	scanPeriod time.Duration
	stopped    atomic.Bool
}

// NewIdleMonitor creates a monitor that will scan at the given period.
// A zero scanPeriod defaults to 1/6 of the shortest non-zero idle
// threshold across all policies, ensuring demotion latency is bounded
// to at most ~17% of the configured threshold. Falls back to 5s if
// no policies have idle thresholds set.
func NewIdleMonitor(controller *ActivationController, scope *concurrency.GoroutineScope, scanPeriod time.Duration) *IdleMonitor {
	if scanPeriod <= 0 {
		scanPeriod = deriveScanPeriod(controller)
	}
	return &IdleMonitor{
		controller: controller,
		scope:      scope,
		logger:     controller.logger,
		scanPeriod: scanPeriod,
	}
}

// deriveScanPeriod finds the shortest idle threshold across all
// registered policies and returns 1/6 of it. This ensures we detect
// idle entries within one scan period after they cross the threshold.
func deriveScanPeriod(controller *ActivationController) time.Duration {
	entries := controller.allEntries()
	var shortest time.Duration
	for _, entry := range entries {
		for _, tier := range []ActivationTier{TierHot, TierWarm, TierCool} {
			threshold := entry.Policy.IdleThreshold(tier)
			if threshold > 0 && (shortest == 0 || threshold < shortest) {
				shortest = threshold
			}
		}
	}
	if shortest <= 0 {
		return 5 * time.Second // fallback when no policies have idle thresholds
	}
	// 1/6 of shortest threshold — scan 6× per threshold period.
	derived := shortest / 6
	if derived < time.Second {
		return time.Second // floor at 1s to avoid CPU waste
	}
	return derived
}

// Start launches the idle scan loop as a tracked goroutine.
func (im *IdleMonitor) Start() error {
	return im.scope.Go("activation-idle-monitor", 0, im.loop)
}

// Stop signals the idle monitor to exit on the next iteration.
func (im *IdleMonitor) Stop() {
	im.stopped.Store(true)
}

func (im *IdleMonitor) loop(ctx context.Context) error {
	ticker := time.NewTicker(im.scanPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if im.stopped.Load() {
				return nil
			}
			im.scan(ctx)
		}
	}
}

func (im *IdleMonitor) scan(ctx context.Context) {
	entries := im.controller.allEntries()
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		im.evaluateEntry(ctx, entry)
	}
}

func (im *IdleMonitor) evaluateEntry(ctx context.Context, entry *ActivationEntry) {
	if entry.HasActiveRequests() {
		entry.TouchActivity()
		return
	}

	tier := entry.LoadTier()
	policy := entry.Policy

	if tier <= policy.MinTier {
		return
	}

	threshold := policy.IdleThreshold(tier)
	if threshold <= 0 {
		return
	}

	idle := entry.IdleDuration()
	if idle < threshold {
		return
	}

	targetTier := tier - 1
	if !policy.CanDemoteTo(targetTier) {
		return
	}

	if err := im.controller.DemoteTo(ctx, entry.AgentType, targetTier); err != nil {
		im.logger.Warn("idle demotion failed",
			"agent_type", entry.AgentType,
			"from_tier", tier.String(),
			"target_tier", targetTier.String(),
			"error", err,
		)
	}
}
