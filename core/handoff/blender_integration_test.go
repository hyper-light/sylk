package handoff

import (
	"testing"
)

// TestSupervisor_SyncBlenderFromProfile_RegistersAtBothLevels verifies
// the HAND-02 plumbing contract: once a supervisor observes a learned
// threshold on a profile, the blender carries it at both the instance
// level (UID-keyed) and the agent-model level (kind+model-keyed). A
// regression here means a sibling agent of the same kind/model cannot
// pick up the learning — the hierarchy collapses to per-instance.
func TestSupervisor_SyncBlenderFromProfile_RegistersAtBothLevels(t *testing.T) {
	sup := NewHandoffSupervisor(nil)
	if sup.Blender() == nil {
		t.Fatal("supervisor.Blender() returned nil — HAND-02 blender not initialized")
	}
	profile := NewAgentHandoffProfile("engineer", "opus-4.7", "engineer-uid-1")
	// Skew the threshold posterior away from the neutral (2,2) prior so
	// we can tell the registered value apart from a fresh default.
	profile.OptimalHandoffThreshold.Alpha = 18
	profile.OptimalHandoffThreshold.Beta = 2

	sup.SyncBlenderFromProfile("engineer", "opus-4.7", "engineer-uid-1", profile)

	agentModel, ok := sup.Blender().GetAgentModel("engineer", "opus-4.7", BlenderParamHandoffThreshold)
	if !ok {
		t.Fatal("expected agent-model-level registration; missing")
	}
	if got := agentModel.Mean(); got < 0.85 {
		t.Errorf("agent-model threshold mean = %v, want ~0.9", got)
	}

	instance, ok := sup.Blender().GetInstance("engineer", "opus-4.7", "engineer-uid-1", BlenderParamHandoffThreshold)
	if !ok {
		t.Fatal("expected instance-level registration; missing")
	}
	if got := instance.Mean(); got < 0.85 {
		t.Errorf("instance threshold mean = %v, want ~0.9", got)
	}
}

// TestSupervisor_SyncBlenderFromProfile_ValueCopyIsolation catches the
// subtle regression where the blender registration would share the
// *LearnedWeight pointer with the profile. Later updates to the
// profile (routine observation learning) would then silently mutate
// the blender's posteriors without going through the supervisor sync
// path, breaking the "blender is a snapshot" invariant.
func TestSupervisor_SyncBlenderFromProfile_ValueCopyIsolation(t *testing.T) {
	sup := NewHandoffSupervisor(nil)
	profile := NewAgentHandoffProfile("engineer", "opus-4.7", "uid-1")
	profile.OptimalHandoffThreshold.Alpha = 10
	profile.OptimalHandoffThreshold.Beta = 10

	sup.SyncBlenderFromProfile("engineer", "opus-4.7", "uid-1", profile)

	// Mutate the profile's posterior after registration.
	profile.OptimalHandoffThreshold.Alpha = 90
	profile.OptimalHandoffThreshold.Beta = 10

	registered, ok := sup.Blender().GetAgentModel("engineer", "opus-4.7", BlenderParamHandoffThreshold)
	if !ok {
		t.Fatal("expected agent-model registration")
	}
	// Before the profile mutation the mean was 0.5; after, 0.9. The
	// blender's copy must still read ~0.5.
	if got := registered.Mean(); got > 0.6 {
		t.Errorf("blender leaked profile mutation: mean = %v, want ~0.5", got)
	}
}

// TestHandoffController_BlenderLookupPrefersBlendedOverProfile is the
// HAND-02 end-to-end check: a controller with a blender lookup must
// return the blended value from getEffectiveThreshold even when the
// controller's local profile has different (and high-confidence)
// learning. If this fails, the hierarchical blender is wired but not
// consulted — the effective behavior reverts to per-(agent,model).
func TestHandoffController_BlenderLookupPrefersBlendedOverProfile(t *testing.T) {
	profile := NewAgentHandoffProfile("engineer", "opus-4.7", "uid-1")
	// Force the profile to speak with high confidence around 0.3 so the
	// profile-confidence gate would otherwise fire. Confidence() is
	// driven by the PROFILE-level EffectiveSamples, not the weight's
	// — set it high enough to clear MinConfidenceForDecision (0.3).
	profile.OptimalHandoffThreshold.Alpha = 30
	profile.OptimalHandoffThreshold.Beta = 70
	profile.EffectiveSamples = 100

	controller := NewHandoffController(nil, profile, nil)

	// Sanity: without a blender the profile's 0.3 mean flows through.
	if got := controller.getEffectiveThreshold(); got < 0.25 || got > 0.35 {
		t.Fatalf("baseline threshold without blender = %v, want ~0.3", got)
	}

	// Install a lookup that returns a very different blended value.
	controller.SetBlenderLookup(func() (float64, bool) { return 0.82, true })
	if got := controller.getEffectiveThreshold(); got != 0.82 {
		t.Errorf("with blender lookup: effective threshold = %v, want 0.82", got)
	}

	// Clearing the lookup restores profile-driven behavior.
	controller.SetBlenderLookup(nil)
	if got := controller.getEffectiveThreshold(); got < 0.25 || got > 0.35 {
		t.Errorf("after clearing lookup: threshold = %v, want ~0.3", got)
	}
}

// TestHandoffController_BlenderLookupFallsBackWhenNotReady ensures the
// fallback layering holds: if the lookup returns ok=false (cold
// start, blender not yet populated), the controller uses the profile
// or static default rather than silently producing 0.
func TestHandoffController_BlenderLookupFallsBackWhenNotReady(t *testing.T) {
	profile := NewAgentHandoffProfile("engineer", "opus-4.7", "uid-1")
	profile.OptimalHandoffThreshold.Alpha = 40
	profile.OptimalHandoffThreshold.Beta = 10
	profile.EffectiveSamples = 50

	controller := NewHandoffController(nil, profile, nil)
	controller.SetBlenderLookup(func() (float64, bool) { return 0, false })

	got := controller.getEffectiveThreshold()
	if got < 0.7 || got > 0.85 {
		t.Errorf("fallback threshold = %v, want ~0.8 (profile mean)", got)
	}
}
