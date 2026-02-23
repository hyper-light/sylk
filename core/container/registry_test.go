package container

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/concurrency"
	csecurity "github.com/adalundhe/sylk/core/container/security"
)

func testContainerWithID(t *testing.T, id string, agentType string, pod *Pod) *Container {
	t.Helper()
	ctx := context.Background()
	scope := concurrency.NewGoroutineScope(ctx, id, nil)
	secCtx := csecurity.NewSecurityContext(csecurity.SecurityContextConfig{
		ContainerID: id,
		AgentID:     id,
		Role:        "worker",
	})

	spec := ContainerSpec{
		Name:      id,
		AgentType: agentType,
	}

	return NewContainer(ContainerConfig{
		ID:     ContainerID(id),
		Spec:   spec,
		Scope:  scope,
		SecCtx: secCtx,
		Agent:  &mockAgent{id: id, agentType: agentType},
		Pod:    pod,
	})
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewContainerRegistry()
	c := testContainerWithID(t, "c1", "engineer", nil)

	if err := reg.Register(c); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	got, err := reg.Get("c1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.ID() != "c1" {
		t.Fatalf("expected c1, got %s", got.ID())
	}
}

func TestRegistry_DuplicateReturnsError(t *testing.T) {
	reg := NewContainerRegistry()
	c := testContainerWithID(t, "c1", "engineer", nil)

	_ = reg.Register(c)
	err := reg.Register(c)
	if !errors.Is(err, ErrContainerExists) {
		t.Fatalf("expected ErrContainerExists, got %v", err)
	}
}

func TestRegistry_Unregister(t *testing.T) {
	reg := NewContainerRegistry()
	c := testContainerWithID(t, "c1", "engineer", nil)

	_ = reg.Register(c)
	reg.Unregister("c1")

	_, err := reg.Get("c1")
	if !errors.Is(err, ErrContainerNotFound) {
		t.Fatalf("expected ErrContainerNotFound, got %v", err)
	}
}

func TestRegistry_ListByType(t *testing.T) {
	reg := NewContainerRegistry()
	c1 := testContainerWithID(t, "c1", "engineer", nil)
	c2 := testContainerWithID(t, "c2", "engineer", nil)
	c3 := testContainerWithID(t, "c3", "inspector", nil)

	_ = reg.Register(c1)
	_ = reg.Register(c2)
	_ = reg.Register(c3)

	engineers := reg.ListByType("engineer")
	if len(engineers) != 2 {
		t.Fatalf("expected 2 engineers, got %d", len(engineers))
	}

	inspectors := reg.ListByType("inspector")
	if len(inspectors) != 1 {
		t.Fatalf("expected 1 inspector, got %d", len(inspectors))
	}
}

func TestRegistry_ListByPod(t *testing.T) {
	reg := NewContainerRegistry()

	pod := NewPod(PodConfig{
		ID:   "pod-1",
		Spec: PodSpec{Name: "test-pod", MainContainers: []ContainerSpec{{Name: "m", AgentType: "e"}}},
		Scope: concurrency.NewGoroutineScope(context.Background(), "pod-1", nil),
	})

	c1 := testContainerWithID(t, "c1", "engineer", pod)
	c2 := testContainerWithID(t, "c2", "inspector", pod)
	c3 := testContainerWithID(t, "c3", "engineer", nil) // standalone

	_ = reg.Register(c1)
	_ = reg.Register(c2)
	_ = reg.Register(c3)

	podContainers := reg.ListByPod("pod-1")
	if len(podContainers) != 2 {
		t.Fatalf("expected 2 pod containers, got %d", len(podContainers))
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	reg := NewContainerRegistry()
	const goroutines = 32

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			id := ContainerID(string(rune('a' + idx)))
			c := testContainerWithID(t, string(id), "engineer", nil)
			_ = reg.Register(c)
			_ = reg.ListByType("engineer")
			reg.All()
		}(i)
	}
	wg.Wait()

	if reg.Count() != goroutines {
		t.Fatalf("expected %d, got %d", goroutines, reg.Count())
	}
}

func TestRegistry_Pod(t *testing.T) {
	reg := NewContainerRegistry()
	pod := NewPod(PodConfig{
		ID:    "pod-1",
		Spec:  PodSpec{Name: "test-pod", MainContainers: []ContainerSpec{{Name: "m", AgentType: "e"}}},
		Scope: concurrency.NewGoroutineScope(context.Background(), "pod-1", nil),
	})

	reg.RegisterPod(pod)
	got, err := reg.GetPod("pod-1")
	if err != nil {
		t.Fatalf("GetPod failed: %v", err)
	}
	if got.ID() != "pod-1" {
		t.Fatalf("expected pod-1, got %s", got.ID())
	}

	reg.UnregisterPod("pod-1")
	_, err = reg.GetPod("pod-1")
	if !errors.Is(err, ErrPodNotFound) {
		t.Fatalf("expected ErrPodNotFound, got %v", err)
	}
}
