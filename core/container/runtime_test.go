package container

import (
	"context"
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/concurrency"
)

func testRuntime(t *testing.T) *DefaultRuntime {
	t.Helper()
	return NewDefaultRuntime(DefaultRuntimeConfig{
		Registry: NewContainerRegistry(),
		CreateAgent: func(_ context.Context, agentType string) (ContainerAgent, error) {
			return &mockAgent{id: "agent-" + agentType, agentType: agentType}, nil
		},
		ParentCtx: context.Background(),
	})
}

func TestRuntime_CreateContainer(t *testing.T) {
	rt := testRuntime(t)
	spec := validContainerSpec()

	c, err := rt.CreateContainer(context.Background(), spec)
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}
	if c.ID() == "" {
		t.Fatal("container should have an ID")
	}
	if c.Agent() == nil {
		t.Fatal("container should have an agent")
	}
	if c.State() != concurrency.StateCreated {
		t.Fatalf("expected Created, got %v", c.State())
	}
}

func TestRuntime_CreateContainerInvalidSpec(t *testing.T) {
	rt := testRuntime(t)
	spec := ContainerSpec{} // empty name/type

	_, err := rt.CreateContainer(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error for invalid spec")
	}
}

func TestRuntime_StartStopContainer(t *testing.T) {
	rt := testRuntime(t)
	spec := validContainerSpec()

	c, err := rt.CreateContainer(context.Background(), spec)
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}

	if err := rt.StartContainer(context.Background(), c); err != nil {
		t.Fatalf("StartContainer failed: %v", err)
	}
	if !c.IsRunning() {
		t.Fatal("should be running")
	}

	if err := rt.StopContainer(context.Background(), c); err != nil {
		t.Fatalf("StopContainer failed: %v", err)
	}
	if !c.IsTerminal() {
		t.Fatal("should be terminal")
	}
}

func TestRuntime_RemoveContainer(t *testing.T) {
	rt := testRuntime(t)
	spec := validContainerSpec()

	c, err := rt.CreateContainer(context.Background(), spec)
	if err != nil {
		t.Fatalf("CreateContainer failed: %v", err)
	}
	id := c.ID()

	if err := rt.RemoveContainer(context.Background(), c); err != nil {
		t.Fatalf("RemoveContainer failed: %v", err)
	}

	_, err = rt.registry.Get(id)
	if !errors.Is(err, ErrContainerNotFound) {
		t.Fatalf("expected ErrContainerNotFound after remove, got %v", err)
	}
}

func TestRuntime_ContainerStatus(t *testing.T) {
	rt := testRuntime(t)
	spec := validContainerSpec()

	c, _ := rt.CreateContainer(context.Background(), spec)
	status := rt.ContainerStatus(c)

	if status.ID != c.ID() {
		t.Fatalf("expected %s, got %s", c.ID(), status.ID)
	}
	if status.State != concurrency.StateCreated {
		t.Fatalf("expected Created, got %v", status.State)
	}
}

func TestRuntime_CreateContainersForPod(t *testing.T) {
	rt := testRuntime(t)
	specs := []ContainerSpec{
		{Name: "worker", AgentType: "engineer"},
		{Name: "sidecar", AgentType: "inspector"},
	}

	containers, err := rt.CreateContainersForPod(context.Background(), "pod-1", specs)
	if err != nil {
		t.Fatalf("CreateContainersForPod failed: %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(containers))
	}
	for _, c := range containers {
		if c.PodID() != "pod-1" {
			t.Fatalf("expected pod ID pod-1, got %s", c.PodID())
		}
	}

	// Verify registry indexing.
	podContainers := rt.registry.ListByPod("pod-1")
	if len(podContainers) != 2 {
		t.Fatalf("expected 2 pod containers in registry, got %d", len(podContainers))
	}
}

func TestRuntime_StartContainers(t *testing.T) {
	rt := testRuntime(t)
	specs := []ContainerSpec{
		{Name: "a", AgentType: "engineer"},
		{Name: "b", AgentType: "designer"},
	}

	containers, _ := rt.CreateContainersForPod(context.Background(), "pod-2", specs)
	if err := rt.StartContainers(context.Background(), containers); err != nil {
		t.Fatalf("StartContainers failed: %v", err)
	}
	for _, c := range containers {
		if !c.IsRunning() {
			t.Fatalf("container %s should be running", c.Spec().Name)
		}
	}
}

func TestRuntime_Closed(t *testing.T) {
	rt := testRuntime(t)
	rt.Close()

	_, err := rt.CreateContainer(context.Background(), validContainerSpec())
	if !errors.Is(err, ErrRuntimeClosed) {
		t.Fatalf("expected ErrRuntimeClosed, got %v", err)
	}
}

func TestRuntime_QuotaEnforcement(t *testing.T) {
	quota := NewResourceQuota(ResourceQuotaConfig{
		ContainerLimit: 2,
	})
	rt := NewDefaultRuntime(DefaultRuntimeConfig{
		Registry: NewContainerRegistry(),
		Quota:    quota,
		CreateAgent: func(_ context.Context, agentType string) (ContainerAgent, error) {
			return &mockAgent{id: "a", agentType: agentType}, nil
		},
		ParentCtx: context.Background(),
	})

	spec := validContainerSpec()
	_, _ = rt.CreateContainer(context.Background(), spec)

	spec2 := validContainerSpec()
	spec2.Name = "second"
	_, _ = rt.CreateContainer(context.Background(), spec2)

	// Third should fail
	spec3 := validContainerSpec()
	spec3.Name = "third"
	_, err := rt.CreateContainer(context.Background(), spec3)
	if err == nil {
		t.Fatal("expected quota exceeded error")
	}
}
