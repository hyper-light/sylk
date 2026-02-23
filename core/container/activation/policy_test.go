package activation

import (
	"math"
	"testing"
	"time"
)

func TestDefaultPolicy(t *testing.T) {
	defaults := DefaultPolicyDefaults()
	p := DefaultPolicy("engineer", defaults)

	if p.AgentType != "engineer" {
		t.Fatalf("expected engineer, got %s", p.AgentType)
	}
	if p.MinTier != TierCold {
		t.Fatalf("expected TierCold, got %v", p.MinTier)
	}
	if p.IsDaemon {
		t.Fatal("default policy should not be daemon")
	}
	if p.IdleToWarm != defaults.IdleToWarm {
		t.Fatalf("expected %v, got %v", defaults.IdleToWarm, p.IdleToWarm)
	}
}

func TestDaemonPolicy(t *testing.T) {
	p := DaemonPolicy("guide")

	if p.Priority != math.MaxInt {
		t.Fatalf("expected MaxInt priority, got %d", p.Priority)
	}
	if p.MinTier != TierHot {
		t.Fatalf("expected TierHot, got %v", p.MinTier)
	}
	if !p.IsDaemon {
		t.Fatal("daemon policy should be daemon")
	}
}

func TestPolicy_CanDemoteTo(t *testing.T) {
	p := &ActivationPolicy{MinTier: TierWarm}

	if !p.CanDemoteTo(TierWarm) {
		t.Fatal("should allow demotion to MinTier")
	}
	if !p.CanDemoteTo(TierHot) {
		t.Fatal("should allow demotion to tier above MinTier")
	}
	if p.CanDemoteTo(TierCool) {
		t.Fatal("should not allow demotion below MinTier")
	}
}

func TestPolicy_IdleThreshold(t *testing.T) {
	defaults := DefaultPolicyDefaults()
	p := DefaultPolicy("engineer", defaults)

	if p.IdleThreshold(TierHot) != defaults.IdleToWarm {
		t.Fatalf("expected %v for Hot, got %v", defaults.IdleToWarm, p.IdleThreshold(TierHot))
	}
	if p.IdleThreshold(TierWarm) != defaults.IdleToCool {
		t.Fatalf("expected %v for Warm, got %v", defaults.IdleToCool, p.IdleThreshold(TierWarm))
	}
	if p.IdleThreshold(TierCool) != defaults.IdleToCold {
		t.Fatalf("expected %v for Cool, got %v", defaults.IdleToCold, p.IdleThreshold(TierCool))
	}
	if p.IdleThreshold(TierCold) != 0 {
		t.Fatalf("expected 0 for Cold, got %v", p.IdleThreshold(TierCold))
	}
}

func TestPolicy_DaemonIdleThresholds(t *testing.T) {
	p := DaemonPolicy("guide")

	for _, tier := range []ActivationTier{TierHot, TierWarm, TierCool} {
		if p.IdleThreshold(tier) != 0 {
			t.Fatalf("daemon should have 0 idle threshold for %v, got %v",
				tier, p.IdleThreshold(tier))
		}
	}
}

func TestTier_String(t *testing.T) {
	tests := map[ActivationTier]string{
		TierCold: "cold",
		TierCool: "cool",
		TierWarm: "warm",
		TierHot:  "hot",
	}
	for tier, expected := range tests {
		if tier.String() != expected {
			t.Fatalf("expected %q, got %q", expected, tier.String())
		}
	}
}

func TestActivationEntry_IdleDuration(t *testing.T) {
	defaults := DefaultPolicyDefaults()
	entry := &ActivationEntry{Policy: DefaultPolicy("test", defaults)}

	// No activity yet.
	if entry.IdleDuration() != 0 {
		t.Fatal("expected 0 idle duration before any activity")
	}

	entry.TouchActivity()
	time.Sleep(10 * time.Millisecond)

	idle := entry.IdleDuration()
	if idle < 10*time.Millisecond {
		t.Fatalf("expected idle >= 10ms, got %v", idle)
	}
}

func TestPolicyDefaults_Derivation(t *testing.T) {
	defaults := DefaultPolicyDefaults()

	// IdleToCool should be 10× IdleToWarm.
	if defaults.IdleToCool != defaults.IdleToWarm*10 {
		t.Fatalf("expected IdleToCool = 10×IdleToWarm, got %v", defaults.IdleToCool)
	}
	// IdleToCold should be 6× IdleToCool.
	if defaults.IdleToCold != defaults.IdleToCool*6 {
		t.Fatalf("expected IdleToCold = 6×IdleToCool, got %v", defaults.IdleToCold)
	}
}
