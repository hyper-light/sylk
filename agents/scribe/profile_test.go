package scribe

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

func TestProfileFor_DefaultsForUnregisteredAgentType(t *testing.T) {
	p := ProfileFor("does-not-exist")
	if p.AgentType != "does-not-exist" {
		t.Fatalf("AgentType = %q, want passthrough", p.AgentType)
	}
	if p.ResolutionFilter != activity.ResolutionFine {
		t.Errorf("default ResolutionFilter = %s, want fine", p.ResolutionFilter)
	}
	if p.BatchSize != defaultProfileBatchSize {
		t.Errorf("default BatchSize = %d, want %d", p.BatchSize, defaultProfileBatchSize)
	}
	if p.BatchWindow != defaultProfileBatchWindow {
		t.Errorf("default BatchWindow = %s, want %s", p.BatchWindow, defaultProfileBatchWindow)
	}
	if p.PeriodicWindow != defaultProfilePeriodicWindow {
		t.Errorf("default PeriodicWindow = %s, want %s", p.PeriodicWindow, defaultProfilePeriodicWindow)
	}
}

func TestRegisterProfile_RoundTrip(t *testing.T) {
	defer cleanupRegisteredProfile(t, "scribe-test-roundtrip")
	want := ScribeProfile{
		AgentType:      "scribe-test-roundtrip",
		PromptModule:   "test_module",
		BatchSize:      7,
		BatchWindow:    2 * time.Second,
		EmphasizeKinds: []activity.ActionKind{activity.ActionDecisionDeclared},
	}
	RegisterProfile(want)
	got := ProfileFor(want.AgentType)
	if got.PromptModule != want.PromptModule {
		t.Errorf("PromptModule = %q, want %q", got.PromptModule, want.PromptModule)
	}
	if got.BatchSize != want.BatchSize {
		t.Errorf("BatchSize = %d, want %d", got.BatchSize, want.BatchSize)
	}
	if !got.IsEmphasized(activity.ActionDecisionDeclared) {
		t.Errorf("IsEmphasized should be true for registered emphasize kind")
	}
}

func TestRegisterProfile_EmptyAgentTypeIsNoOp(t *testing.T) {
	before := len(RegisteredProfiles())
	RegisterProfile(ScribeProfile{AgentType: "  "})
	if got := len(RegisteredProfiles()); got != before {
		t.Errorf("registered profile with empty AgentType; count went %d -> %d", before, got)
	}
}

func TestProfile_AcceptsResolution(t *testing.T) {
	p := ProfileFor("does-not-exist") // defaults to Fine
	cases := map[activity.Resolution]bool{
		activity.ResolutionAtomic: false,
		activity.ResolutionFine:   true,
		activity.ResolutionMedium: true,
		activity.ResolutionCoarse: true,
	}
	for r, want := range cases {
		if got := p.AcceptsResolution(r); got != want {
			t.Errorf("AcceptsResolution(%s) = %v, want %v", r, got, want)
		}
	}
}

func TestProfile_AcceptsResolution_StricterFilter(t *testing.T) {
	defer cleanupRegisteredProfile(t, "scribe-test-strict")
	RegisterProfile(ScribeProfile{
		AgentType:        "scribe-test-strict",
		ResolutionFilter: activity.ResolutionCoarse,
	})
	p := ProfileFor("scribe-test-strict")
	if p.AcceptsResolution(activity.ResolutionFine) {
		t.Errorf("strict profile should reject Fine")
	}
	if !p.AcceptsResolution(activity.ResolutionCoarse) {
		t.Errorf("strict profile should accept Coarse")
	}
}

func TestProfile_ShouldIgnoreKind(t *testing.T) {
	defer cleanupRegisteredProfile(t, "scribe-test-ignore")
	RegisterProfile(ScribeProfile{
		AgentType:   "scribe-test-ignore",
		IgnoreKinds: []activity.ActionKind{activity.ActionFileRead},
	})
	p := ProfileFor("scribe-test-ignore")
	if !p.ShouldIgnoreKind(activity.ActionFileRead) {
		t.Errorf("expected ignore-kind true")
	}
	if p.ShouldIgnoreKind(activity.ActionDecisionDeclared) {
		t.Errorf("expected ignore-kind false for non-listed kind")
	}
}

// cleanupRegisteredProfile removes a test-registered profile so other
// tests in the package see the registry unchanged.
func cleanupRegisteredProfile(t *testing.T, agentType string) {
	t.Helper()
	t.Cleanup(func() {
		profileMu.Lock()
		delete(profileRegistry, agentType)
		profileMu.Unlock()
	})
}
