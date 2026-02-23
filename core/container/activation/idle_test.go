package activation

import (
	"testing"
	"time"
)

func TestIdleMonitor_DemotesHotToWarm(t *testing.T) {
	// Create a controller with a very short idle timeout.
	cfg := testControllerConfig(t)
	cfg.Policies = []*ActivationPolicy{
		{
			AgentType:  "engineer",
			MinTier:    TierCold,
			IdleToWarm: 10 * time.Millisecond, // very short for testing
			IdleToCool: time.Hour,
			IdleToCold: time.Hour,
		},
	}

	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	// Set the entry to Hot with last activity in the past.
	entry, _ := ac.getEntry("engineer")
	entry.StoreTier(TierHot)
	entry.LastActive.Store(time.Now().Add(-time.Second).UnixNano())

	// Create a mock container.
	c := newMockContainer(t, "engineer")
	entry.Container.Store(c)
	_ = c.Start(testCtx(t))

	// Manually create and run a single scan of the idle monitor.
	idle := NewIdleMonitor(ac, testScope(t), time.Hour)
	idle.scan(testCtx(t))

	tier := entry.LoadTier()
	if tier != TierWarm {
		t.Fatalf("expected TierWarm after idle scan, got %v", tier)
	}
}

func TestIdleMonitor_RespectsMinTier(t *testing.T) {
	cfg := testControllerConfig(t)
	cfg.Policies = []*ActivationPolicy{
		{
			AgentType:  "guide",
			MinTier:    TierHot,
			IsDaemon:   true,
			IdleToWarm: 0,
		},
	}

	ac, err := NewActivationController(cfg)
	if err != nil {
		t.Fatalf("NewActivationController: %v", err)
	}

	entry, _ := ac.getEntry("guide")
	entry.StoreTier(TierHot)
	entry.LastActive.Store(time.Now().Add(-time.Hour).UnixNano())

	idle := NewIdleMonitor(ac, testScope(t), time.Hour)
	idle.scan(testCtx(t))

	// Should remain Hot because MinTier = TierHot.
	if entry.LoadTier() != TierHot {
		t.Fatalf("daemon should remain at Hot, got %v", entry.LoadTier())
	}
}
