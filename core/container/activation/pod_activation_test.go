package activation

import (
	"testing"

	"github.com/adalundhe/sylk/core/container/pod"
	"github.com/adalundhe/sylk/core/handoff"
)

func TestPodActivationPolicies_GroupsPipeline(t *testing.T) {
	descriptors := []handoff.AgentDescriptor{
		{AgentType: "guide", Category: handoff.CategoryStandalone},
		{AgentType: "orchestrator", Category: handoff.CategoryStandalone},
		{AgentType: "architect", Category: handoff.CategoryKnowledge},
		{AgentType: "engineer", Category: handoff.CategoryPipeline},
		{AgentType: "designer", Category: handoff.CategoryPipeline},
		{AgentType: "inspector-pipeline", Category: handoff.CategoryPipeline},
		{AgentType: "tester-pipeline", Category: handoff.CategoryPipeline},
		{AgentType: "inspector", Category: handoff.CategoryStandalone},
		{AgentType: "tester", Category: handoff.CategoryStandalone},
	}

	policies, podPolicies, agentToPod, err := PodActivationPolicies(descriptors)
	if err != nil {
		t.Fatalf("PodActivationPolicies() error = %v", err)
	}

	// Should have: guide, orchestrator, architect, inspector, tester (5 singletons) + 1 pipeline = 6
	if len(policies) != 6 {
		t.Fatalf("expected 6 policies, got %d", len(policies))
	}
	if len(podPolicies) != 6 {
		t.Fatalf("expected 6 pod policies, got %d", len(podPolicies))
	}

	// All pipeline agents should map to the same pod.
	for _, pt := range []string{"engineer", "designer", "inspector-pipeline", "tester-pipeline"} {
		if agentToPod[pt] != "pipeline" {
			t.Errorf("expected %s → pipeline, got %q", pt, agentToPod[pt])
		}
	}

	// Singletons map to their own type.
	for _, st := range []string{"guide", "orchestrator", "architect", "inspector", "tester"} {
		if agentToPod[st] != st {
			t.Errorf("expected %s → %s, got %q", st, st, agentToPod[st])
		}
	}

	// Verify pipeline pod policy.
	var pipelinePP *pod.PodPolicy
	for _, pp := range podPolicies {
		if pp.PodID == "pipeline" {
			pipelinePP = pp
			break
		}
	}
	if pipelinePP == nil {
		t.Fatal("expected pipeline pod policy")
	}
	if pipelinePP.PodType != pod.PodTypePipeline {
		t.Errorf("expected PodTypePipeline, got %v", pipelinePP.PodType)
	}
	if pipelinePP.MemberCount() != 4 {
		t.Errorf("expected 4 members, got %d", pipelinePP.MemberCount())
	}

	// Verify daemon pods.
	var guidePP *pod.PodPolicy
	for _, pp := range podPolicies {
		if pp.PodID == "guide" {
			guidePP = pp
			break
		}
	}
	if guidePP == nil {
		t.Fatal("expected guide pod policy")
	}
	if guidePP.PodType != pod.PodTypeDaemon {
		t.Errorf("expected PodTypeDaemon for guide, got %v", guidePP.PodType)
	}
	if !guidePP.IsDaemon {
		t.Error("expected guide to be daemon")
	}

	// Verify architect gets PreWarmOnStartup.
	var archPP *pod.PodPolicy
	for _, pp := range podPolicies {
		if pp.PodID == "architect" {
			archPP = pp
			break
		}
	}
	if archPP == nil {
		t.Fatal("expected architect pod policy")
	}
	if !archPP.PreWarmOnStartup {
		t.Error("expected architect to have PreWarmOnStartup")
	}
}

func TestPodActivationPolicies_NoPipelineAgents(t *testing.T) {
	descriptors := []handoff.AgentDescriptor{
		{AgentType: "guide", Category: handoff.CategoryStandalone},
		{AgentType: "architect", Category: handoff.CategoryKnowledge},
	}

	policies, _, agentToPod, err := PodActivationPolicies(descriptors)
	if err != nil {
		t.Fatalf("PodActivationPolicies() error = %v", err)
	}

	// Should have: guide + architect = 2 (no pipeline pod).
	if len(policies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(policies))
	}
	if _, ok := agentToPod["pipeline"]; ok {
		t.Fatal("should not have pipeline mapping when no pipeline agents")
	}
}

func TestPodActivationPolicies_UnknownCategoryReturnsError(t *testing.T) {
	descriptors := []handoff.AgentDescriptor{
		{AgentType: "mystery", Category: handoff.AgentCategory(99)},
	}

	_, _, _, err := PodActivationPolicies(descriptors)
	if err == nil {
		t.Fatal("expected error for unknown category")
	}
}

func TestPodPolicyScanPeriod(t *testing.T) {
	podPolicies := []*pod.PodPolicy{
		{PodID: "guide", IsDaemon: true},                 // skipped
		{PodID: "architect", IdleToWarm: 30_000_000_000}, // 30s → scan at 5s
	}

	period := PodPolicyScanPeriod(podPolicies)
	if period != 5_000_000_000 { // 5s
		t.Errorf("expected 5s scan period, got %v", period)
	}
}

func TestRegisterPodMappings(t *testing.T) {
	ctrl, err := NewActivationController(ActivationControllerConfig{
		StorageDir: t.TempDir(),
		Policies:   []*ActivationPolicy{DefaultPolicy("pipeline", DefaultPolicyDefaults())},
	})
	if err != nil {
		t.Fatalf("controller: %v", err)
	}
	activator := NewControllerPodActivator(ctrl)

	agentToPod := map[string]string{
		"engineer": "pipeline",
		"designer": "pipeline",
	}
	RegisterPodMappings(activator, agentToPod)

	if got := activator.PodForAgent("engineer"); got != "pipeline" {
		t.Errorf("expected engineer → pipeline, got %q", got)
	}
	if got := activator.PodForAgent("designer"); got != "pipeline" {
		t.Errorf("expected designer → pipeline, got %q", got)
	}
	// Unmapped agent returns itself.
	if got := activator.PodForAgent("architect"); got != "architect" {
		t.Errorf("expected architect → architect, got %q", got)
	}
}
