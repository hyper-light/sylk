package orchestrator

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/dag"
)

func TestActivateAgents_WithPod(t *testing.T) {
	act := &trackingActivator{}
	reg := &trackingRegistrar{}

	pod := NewPipelinePod(PipelinePodConfig{
		DAGID:     "dag-1",
		Activator: act,
		Registrar: reg.register,
	})

	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, act, pod)

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "build it",
	})

	if err := d.activateAgents(context.Background(), node); err != nil {
		t.Fatalf("activateAgents: %v", err)
	}

	if got := act.holdCount(); got != 1 {
		t.Errorf("expected 1 HoldPodActive call, got %d", got)
	}
	if got := reg.callCount(); got != 1 {
		t.Errorf("expected 1 registrar call, got %d", got)
	}
	if got := pod.ActiveGuardCount(); got != 1 {
		t.Errorf("expected 1 active guard in pod, got %d", got)
	}
}

func TestActivateAgents_WithPod_CoAgents(t *testing.T) {
	act := &trackingActivator{}
	reg := &trackingRegistrar{}

	pod := NewPipelinePod(PipelinePodConfig{
		DAGID:     "dag-1",
		Activator: act,
		Registrar: reg.register,
	})

	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, act, pod)

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "build it",
		CoAgents:  []string{"designer", "inspector-pipeline"},
	})

	if err := d.activateAgents(context.Background(), node); err != nil {
		t.Fatalf("activateAgents: %v", err)
	}

	// engineer + designer + inspector-pipeline = 3
	if got := act.holdCount(); got != 3 {
		t.Errorf("expected 3 HoldPodActive calls, got %d: %v", got, act.calledTypes())
	}
	if got := reg.callCount(); got != 3 {
		t.Errorf("expected 3 registrar calls, got %d", got)
	}
}

func TestActivateAgents_WithPod_GuardReleasedOnNodeComplete(t *testing.T) {
	act := &trackingActivator{}

	pod := NewPipelinePod(PipelinePodConfig{
		DAGID:     "dag-1",
		Activator: act,
	})

	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, act, pod)

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "build it",
	})

	if err := d.activateAgents(context.Background(), node); err != nil {
		t.Fatalf("activateAgents: %v", err)
	}

	if got := pod.ActiveGuardCount(); got != 1 {
		t.Fatalf("expected 1 active guard, got %d", got)
	}

	// OnNodeComplete releases the guard (even without a pending dispatch
	// channel — logs a warning but still releases).
	d.OnNodeComplete("n1", &dag.NodeResult{State: dag.NodeStateSucceeded})

	if got := act.releaseCount(); got != 1 {
		t.Errorf("expected 1 release from OnNodeComplete, got %d", got)
	}
	if got := pod.ActiveGuardCount(); got != 0 {
		t.Errorf("expected 0 active guards after OnNodeComplete, got %d", got)
	}
}

func TestActivateAgents_WithPod_FailureIsolation(t *testing.T) {
	act := &trackingActivator{failOn: "designer"}
	reg := &trackingRegistrar{}

	pod := NewPipelinePod(PipelinePodConfig{
		DAGID:     "dag-1",
		Activator: act,
		Registrar: reg.register,
	})

	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, act, pod)

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "build it",
		CoAgents:  []string{"designer"},
	})

	err := d.activateAgents(context.Background(), node)
	if err == nil {
		t.Fatal("expected error from failed activation")
	}

	// engineer guard was acquired then released during rollback.
	if got := act.releaseCount(); got != 1 {
		t.Errorf("expected 1 rollback release, got %d", got)
	}
	if got := pod.ActiveGuardCount(); got != 0 {
		t.Errorf("expected 0 active guards after failed activation, got %d", got)
	}
}

func TestReleaseAllGuards_DelegatesToPod(t *testing.T) {
	act := &trackingActivator{}

	pod := NewPipelinePod(PipelinePodConfig{
		DAGID:     "dag-1",
		Activator: act,
	})

	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, act, pod)

	for _, id := range []string{"n1", "n2", "n3"} {
		node := dag.NewNode(dag.NodeConfig{
			ID:        id,
			AgentType: "engineer",
			Prompt:    "task",
		})
		if err := d.activateAgents(context.Background(), node); err != nil {
			t.Fatalf("activateAgents(%s): %v", id, err)
		}
	}

	d.ReleaseAllGuards()

	if got := act.releaseCount(); got != 3 {
		t.Errorf("expected 3 releases from ReleaseAllGuards, got %d", got)
	}

	// Idempotent.
	d.ReleaseAllGuards()
	if got := act.releaseCount(); got != 3 {
		t.Errorf("expected still 3 releases after second ReleaseAllGuards, got %d", got)
	}
}

func TestActivateAgents_FallbackWithoutPod(t *testing.T) {
	act := &trackingActivator{}

	// No pod — falls back to activator.EnsurePodActive.
	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, act, nil)

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "build it",
		CoAgents:  []string{"designer"},
	})

	if err := d.activateAgents(context.Background(), node); err != nil {
		t.Fatalf("activateAgents with nil pod: %v", err)
	}

	// Fallback uses EnsurePodActive, not HoldPodActive.
	if got := act.holdCount(); got != 0 {
		t.Errorf("expected 0 HoldPodActive calls (fallback uses EnsurePodActive), got %d", got)
	}
}

func TestActivateAgents_NilPodNilActivator(t *testing.T) {
	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, nil, nil)

	node := dag.NewNode(dag.NodeConfig{
		ID:        "n1",
		AgentType: "engineer",
		Prompt:    "build it",
	})

	if err := d.activateAgents(context.Background(), node); err != nil {
		t.Fatalf("expected double-nil to be a no-op, got: %v", err)
	}
}

func TestReleaseGuard_NilPod(t *testing.T) {
	d := NewBusNodeDispatcher(nil, "orch", "sess", "dag1", nil, nil, nil)
	// Should not panic.
	d.ReleaseGuard("n1")
	d.ReleaseAllGuards()
}
