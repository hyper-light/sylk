package guide

import (
	"context"
	"testing"
)

type recordingPodActivator struct {
	ensureCalls []string
}

func (a *recordingPodActivator) EnsurePodActive(_ context.Context, podID string) error {
	a.ensureCalls = append(a.ensureCalls, podID)
	return nil
}

func (*recordingPodActivator) TouchPodActivity(string) {}

func (*recordingPodActivator) HoldPodActive(context.Context, string) (func(), error) {
	return func() {}, nil
}

func (*recordingPodActivator) PodForAgent(agentType string) string { return agentType }

func newActivationRecoveryGuide() *Guide {
	return &Guide{
		registry:    NewRegistry(),
		routing:     NewRoutingAggregator(),
		readyAgents: NewStringMap[bool](DefaultShardCount),
		typeIndex:   NewStringMap[string](DefaultShardCount),
		podIndex:    NewStringMap[string](DefaultShardCount),
		agentSubs:   NewStringMap[*agentSubscriptions](DefaultShardCount),
	}
}

func registerActivationRecoveryAgent(g *Guide, id, agentType, name string, ready bool) {
	registration := &AgentRegistration{
		ID:   id,
		Name: name,
	}
	g.registry.Register(registration)
	g.routing.RegisterAgent(&AgentRoutingInfo{
		ID:           id,
		Type:         agentType,
		Name:         name,
		Registration: registration,
	})
	g.typeIndex.Set(id, agentType)
	g.readyAgents.Set(id, ready)
}

func TestEnsureExplicitTargetReady_ReturnsReplacementReadyAgentAfterActivation(t *testing.T) {
	g := newActivationRecoveryGuide()
	registerActivationRecoveryAgent(g, "arch-old", "architect", "architect", false)

	activator := &recordingPodActivator{}
	g.activator = activator
	g.agentRegistrar = func(_ string, agentType string) {
		registerActivationRecoveryAgent(g, "arch-new", agentType, agentType, true)
	}

	resolved, err := g.ensureExplicitTargetReady(context.Background(), "arch-old")
	if err != nil {
		t.Fatalf("ensureExplicitTargetReady error = %v", err)
	}
	if resolved != "arch-new" {
		t.Fatalf("resolved = %q, want arch-new", resolved)
	}
	if len(activator.ensureCalls) != 1 || activator.ensureCalls[0] != "architect" {
		t.Fatalf("EnsurePodActive calls = %v, want [architect]", activator.ensureCalls)
	}
}

func TestEnsureExplicitTargetReady_TaskScopedAliasActivatesPodAndReturnsRuntimeID(t *testing.T) {
	g := newActivationRecoveryGuide()

	activator := &recordingPodActivator{}
	g.activator = activator
	g.agentRegistrar = func(podID, agentType string) {
		if podID != "task-7" || agentType != "tester-pipeline" {
			t.Fatalf("agentRegistrar(%q, %q), want (%q, %q)", podID, agentType, "task-7", "tester-pipeline")
		}
		registration := &AgentRegistration{
			ID:   "tester-runtime-1",
			Name: "task-7-tester-pipeline",
		}
		g.registry.Register(registration)
		g.routing.RegisterAgent(&AgentRoutingInfo{
			ID:           "tester-runtime-1",
			Type:         "tester-pipeline",
			Name:         "task-7-tester-pipeline",
			PodID:        "task-7",
			Registration: registration,
		})
		g.typeIndex.Set("tester-runtime-1", "tester-pipeline")
		g.readyAgents.Set("tester-runtime-1", true)
	}

	resolved, err := g.ensureExplicitTargetReady(context.Background(), "task-7-tester-pipeline")
	if err != nil {
		t.Fatalf("ensureExplicitTargetReady error = %v", err)
	}
	if resolved != "tester-runtime-1" {
		t.Fatalf("resolved = %q, want tester-runtime-1", resolved)
	}
	if len(activator.ensureCalls) != 1 || activator.ensureCalls[0] != "task-7" {
		t.Fatalf("EnsurePodActive calls = %v, want [task-7]", activator.ensureCalls)
	}
}

func TestEnsureClassifiedTargetReady_PrefersReadyReplacementOverStaleID(t *testing.T) {
	g := newActivationRecoveryGuide()
	registerActivationRecoveryAgent(g, "arch-old", "architect", "architect", false)
	registerActivationRecoveryAgent(g, "arch-new", "architect", "architect", true)

	activator := &recordingPodActivator{}
	g.activator = activator

	resolved := g.ensureClassifiedTargetReady(context.Background(), "arch-old")
	if resolved != "arch-new" {
		t.Fatalf("resolved = %q, want arch-new", resolved)
	}
	if len(activator.ensureCalls) != 0 {
		t.Fatalf("EnsurePodActive calls = %v, want none", activator.ensureCalls)
	}
}
