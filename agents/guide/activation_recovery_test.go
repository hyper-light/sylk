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
	registerActivationRecoveryAgentWithPod(g, id, agentType, name, "", ready)
}

func registerActivationRecoveryAgentWithPod(g *Guide, id, agentType, name, podID string, ready bool) {
	registration := &AgentRegistration{
		ID:   id,
		Name: name,
	}
	g.registry.Register(registration)
	g.routing.RegisterAgent(&AgentRoutingInfo{
		ID:           id,
		Type:         agentType,
		Name:         name,
		PodID:        podID,
		Registration: registration,
	})
	g.typeIndex.Set(id, agentType)
	if podID != "" {
		g.podIndex.Set(id, podID)
	}
	g.readyAgents.Set(id, ready)
}

func TestEnsureExplicitTargetReady_ReturnsReplacementReadyAgentAfterActivation(t *testing.T) {
	g := newActivationRecoveryGuide()
	registerActivationRecoveryAgent(g, "arch-old", "architect", "architect", false)

	activator := &recordingPodActivator{}
	g.activator = activator
	g.agentRegistrar = func(_ context.Context, _ string, _ string, agentType string) {
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

func TestEnsureExplicitTargetReady_UsesRecordedPodIDForTaskScopedWorker(t *testing.T) {
	g := newActivationRecoveryGuide()
	registerActivationRecoveryAgentWithPod(g, "7f5e9e9d", "engineer", "engineer-task-7", "task-7", false)

	activator := &recordingPodActivator{}
	g.activator = activator
	g.agentRegistrar = func(_ context.Context, _ string, _ string, agentType string) {
		registerActivationRecoveryAgentWithPod(g, "7f5e9e9d", agentType, "engineer-task-7", "task-7", true)
	}

	resolved, err := g.ensureExplicitTargetReady(context.Background(), "7f5e9e9d")
	if err != nil {
		t.Fatalf("ensureExplicitTargetReady error = %v", err)
	}
	if resolved != "7f5e9e9d" {
		t.Fatalf("resolved = %q, want 7f5e9e9d", resolved)
	}
	if len(activator.ensureCalls) != 1 || activator.ensureCalls[0] != "task-7" {
		t.Fatalf("EnsurePodActive calls = %v, want [task-7]", activator.ensureCalls)
	}
}

func TestEnsureExplicitTargetReady_SeedsTaskScopedWorkerFromRequestMetadata(t *testing.T) {
	g := newActivationRecoveryGuide()
	g.seedExplicitTargetActivationMapping(&RouteRequest{
		TargetAgentID: "0d23f483",
		Metadata: map[string]any{
			"task_id":    "task-7",
			"agent_type": "engineer",
		},
	})

	activator := &recordingPodActivator{}
	g.activator = activator
	g.agentRegistrar = func(_ context.Context, targetAgentID, podID, agentType string) {
		if targetAgentID != "0d23f483" {
			t.Fatalf("targetAgentID = %q, want 0d23f483", targetAgentID)
		}
		if podID != "task-7" {
			t.Fatalf("podID = %q, want task-7", podID)
		}
		if agentType != "engineer" {
			t.Fatalf("agentType = %q, want engineer", agentType)
		}
		registerActivationRecoveryAgentWithPod(g, "0d23f483", agentType, "engineer-task-7", podID, true)
	}

	resolved, err := g.ensureExplicitTargetReady(context.Background(), "0d23f483")
	if err != nil {
		t.Fatalf("ensureExplicitTargetReady error = %v", err)
	}
	if resolved != "0d23f483" {
		t.Fatalf("resolved = %q, want 0d23f483", resolved)
	}
	if len(activator.ensureCalls) != 1 || activator.ensureCalls[0] != "task-7" {
		t.Fatalf("EnsurePodActive calls = %v, want [task-7]", activator.ensureCalls)
	}
}
