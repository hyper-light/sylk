package activation

import (
	"testing"
	"time"
)

func TestPressureEvaluator_NoPressure(t *testing.T) {
	pe := NewPressureEvaluator(nil, nil, PressureConfig{})
	if pe.IsUnderPressure() {
		t.Fatal("should not be under pressure with nil budget and quota")
	}
}

func TestPressureEvaluator_EvaluateExcludesDaemons(t *testing.T) {
	pe := NewPressureEvaluator(nil, nil, PressureConfig{})
	defaults := DefaultPolicyDefaults()

	daemonEntry := &ActivationEntry{
		AgentType: "guide",
		Policy:    DaemonPolicy("guide"),
	}
	daemonEntry.StoreTier(TierHot)
	daemonEntry.LastActive.Store(time.Now().Add(-time.Hour).UnixNano())

	normalEntry := &ActivationEntry{
		AgentType: "engineer",
		Policy:    DefaultPolicy("engineer", defaults),
	}
	normalEntry.StoreTier(TierHot)
	normalEntry.LastActive.Store(time.Now().Add(-time.Minute).UnixNano())

	candidates := pe.Evaluate([]*ActivationEntry{daemonEntry, normalEntry})

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate (daemon excluded), got %d", len(candidates))
	}
	if candidates[0].Entry.AgentType != "engineer" {
		t.Fatal("expected engineer as the only candidate")
	}
}

func TestPressureEvaluator_EvaluateExcludesAtMinTier(t *testing.T) {
	pe := NewPressureEvaluator(nil, nil, PressureConfig{})

	entry := &ActivationEntry{
		AgentType: "engineer",
		Policy: &ActivationPolicy{
			AgentType: "engineer",
			MinTier:   TierWarm,
		},
	}
	entry.StoreTier(TierWarm) // at MinTier

	candidates := pe.Evaluate([]*ActivationEntry{entry})
	if len(candidates) != 0 {
		t.Fatalf("expected 0 candidates (at min tier), got %d", len(candidates))
	}
}

func TestPressureEvaluator_EvictionOrder(t *testing.T) {
	pe := NewPressureEvaluator(nil, nil, PressureConfig{})

	// High priority, recently active.
	highPri := &ActivationEntry{
		AgentType: "architect",
		Policy: &ActivationPolicy{
			AgentType: "architect",
			Priority:  100,
		},
	}
	highPri.StoreTier(TierHot)
	highPri.LastActive.Store(time.Now().UnixNano())

	// Low priority, idle for a long time.
	lowPri := &ActivationEntry{
		AgentType: "tester",
		Policy: &ActivationPolicy{
			AgentType: "tester",
			Priority:  1,
		},
	}
	lowPri.StoreTier(TierHot)
	lowPri.LastActive.Store(time.Now().Add(-10 * time.Minute).UnixNano())

	candidates := pe.Evaluate([]*ActivationEntry{highPri, lowPri})

	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	// Low priority idle should be evicted first (lower score).
	if candidates[0].Entry.AgentType != "tester" {
		t.Fatalf("expected tester first (lowest score), got %s", candidates[0].Entry.AgentType)
	}
	if candidates[1].Entry.AgentType != "architect" {
		t.Fatalf("expected architect second (highest score), got %s", candidates[1].Entry.AgentType)
	}
}
