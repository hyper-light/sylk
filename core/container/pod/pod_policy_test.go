package pod

import (
	"testing"
	"time"
)

func TestPodPolicy_ContainsMember(t *testing.T) {
	p := &PodPolicy{
		MemberTypes: []string{"engineer", "designer"},
	}

	if !p.ContainsMember("engineer") {
		t.Fatal("expected engineer to be a member")
	}
	if p.ContainsMember("architect") {
		t.Fatal("expected architect to not be a member")
	}
}

func TestPodPolicy_MemberCount(t *testing.T) {
	p := &PodPolicy{
		MemberTypes: []string{"engineer", "designer", "inspector-pipeline", "tester-pipeline"},
	}
	if p.MemberCount() != 4 {
		t.Fatalf("expected 4 members, got %d", p.MemberCount())
	}
}

func TestPodPolicy_Fields(t *testing.T) {
	p := &PodPolicy{
		PodID:            "pipeline",
		PodType:          PodTypePipeline,
		MemberTypes:      []string{"engineer", "designer"},
		Priority:         10,
		MinTier:          TierCold,
		IdleToWarm:       15 * time.Second,
		IdleToCool:       150 * time.Second,
		IdleToCold:       900 * time.Second,
		IsDaemon:         false,
		PreWarmOnStartup: true,
	}

	if p.PodID != "pipeline" {
		t.Errorf("expected pipeline, got %q", p.PodID)
	}
	if p.Priority != 10 {
		t.Errorf("expected priority 10, got %d", p.Priority)
	}
	if p.MinTier != TierCold {
		t.Errorf("expected TierCold, got %v", p.MinTier)
	}
	if p.IdleToWarm != 15*time.Second {
		t.Errorf("expected 15s IdleToWarm, got %v", p.IdleToWarm)
	}
	if p.IdleToCool != 150*time.Second {
		t.Errorf("expected 150s IdleToCool, got %v", p.IdleToCool)
	}
	if p.IdleToCold != 900*time.Second {
		t.Errorf("expected 900s IdleToCold, got %v", p.IdleToCold)
	}
	if p.PreWarmOnStartup != true {
		t.Error("expected PreWarmOnStartup")
	}
}

func TestPodType_String(t *testing.T) {
	tests := []struct {
		pt   PodType
		want string
	}{
		{PodTypePipeline, "pipeline"},
		{PodTypeSingleton, "singleton"},
		{PodTypeDaemon, "daemon"},
		{PodType(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.pt.String(); got != tt.want {
			t.Errorf("PodType(%d).String() = %q, want %q", tt.pt, got, tt.want)
		}
	}
}

func TestPodType_IsDaemon(t *testing.T) {
	if PodTypePipeline.IsDaemon() {
		t.Error("pipeline should not be daemon")
	}
	if !PodTypeDaemon.IsDaemon() {
		t.Error("daemon should be daemon")
	}
}
