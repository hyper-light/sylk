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

	c, err := rt.CreateContainer(context.Background(), spec, nil)
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

	_, err := rt.CreateContainer(context.Background(), spec, nil)
	if err == nil {
		t.Fatal("expected error for invalid spec")
	}
}

func TestRuntime_StartStopContainer(t *testing.T) {
	rt := testRuntime(t)
	spec := validContainerSpec()

	c, err := rt.CreateContainer(context.Background(), spec, nil)
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

	c, err := rt.CreateContainer(context.Background(), spec, nil)
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

	c, _ := rt.CreateContainer(context.Background(), spec, nil)
	status := rt.ContainerStatus(c)

	if status.ID != c.ID() {
		t.Fatalf("expected %s, got %s", c.ID(), status.ID)
	}
	if status.State != concurrency.StateCreated {
		t.Fatalf("expected Created, got %v", status.State)
	}
}

func TestRuntime_CreatePod(t *testing.T) {
	rt := testRuntime(t)
	podSpec := PodSpec{
		Name: "test-pod",
		MainContainers: []ContainerSpec{
			{Name: "worker", AgentType: "engineer"},
		},
		SidecarContainers: []ContainerSpec{
			{Name: "sidecar", AgentType: "inspector"},
		},
	}

	pod, err := rt.CreatePod(context.Background(), podSpec)
	if err != nil {
		t.Fatalf("CreatePod failed: %v", err)
	}

	if pod.Phase() != PodPending {
		t.Fatalf("expected Pending, got %v", pod.Phase())
	}
	if len(pod.MainContainers()) != 1 {
		t.Fatalf("expected 1 main container, got %d", len(pod.MainContainers()))
	}
	if len(pod.SidecarContainers()) != 1 {
		t.Fatalf("expected 1 sidecar, got %d", len(pod.SidecarContainers()))
	}
	if rt.registry.PodCount() != 1 {
		t.Fatalf("expected 1 pod in registry, got %d", rt.registry.PodCount())
	}
}

func TestRuntime_CreatePodInvalidSpec(t *testing.T) {
	rt := testRuntime(t)
	podSpec := PodSpec{Name: ""} // empty name

	_, err := rt.CreatePod(context.Background(), podSpec)
	if err == nil {
		t.Fatal("expected error for invalid pod spec")
	}
}

func TestRuntime_StopAndRemovePod(t *testing.T) {
	rt := testRuntime(t)
	podSpec := PodSpec{
		Name: "test-pod",
		MainContainers: []ContainerSpec{
			{Name: "worker", AgentType: "engineer"},
		},
	}

	pod, _ := rt.CreatePod(context.Background(), podSpec)
	_ = rt.StartPod(context.Background(), pod)

	if err := rt.StopPod(context.Background(), pod); err != nil {
		t.Fatalf("StopPod failed: %v", err)
	}

	if err := rt.RemovePod(context.Background(), pod); err != nil {
		t.Fatalf("RemovePod failed: %v", err)
	}

	if rt.registry.PodCount() != 0 {
		t.Fatalf("expected 0 pods, got %d", rt.registry.PodCount())
	}
}

func TestRuntime_PodStatus(t *testing.T) {
	rt := testRuntime(t)
	podSpec := PodSpec{
		Name: "test-pod",
		MainContainers: []ContainerSpec{
			{Name: "worker", AgentType: "engineer"},
		},
	}

	pod, _ := rt.CreatePod(context.Background(), podSpec)
	status := rt.PodStatus(pod)

	if status.ID != pod.ID() {
		t.Fatalf("expected %s, got %s", pod.ID(), status.ID)
	}
	if status.Phase != PodPending {
		t.Fatalf("expected Pending, got %v", status.Phase)
	}
	if len(status.Containers) != 1 {
		t.Fatalf("expected 1 container status, got %d", len(status.Containers))
	}
}

func TestRuntime_Closed(t *testing.T) {
	rt := testRuntime(t)
	rt.Close()

	_, err := rt.CreateContainer(context.Background(), validContainerSpec(), nil)
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
	_, _ = rt.CreateContainer(context.Background(), spec, nil)

	spec2 := validContainerSpec()
	spec2.Name = "second"
	_, _ = rt.CreateContainer(context.Background(), spec2, nil)

	// Third should fail
	spec3 := validContainerSpec()
	spec3.Name = "third"
	_, err := rt.CreateContainer(context.Background(), spec3, nil)
	if err == nil {
		t.Fatal("expected quota exceeded error")
	}
}
