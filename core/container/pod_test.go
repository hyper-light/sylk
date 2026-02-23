package container

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/concurrency"
)

func testPod(t *testing.T) *Pod {
	t.Helper()
	scope := concurrency.NewGoroutineScope(context.Background(), "test-pod", nil)
	return NewPod(PodConfig{
		ID:   "pod-1",
		Spec: PodSpec{Name: "test-pod"},
		Scope: scope,
	})
}

func testContainerForPod(t *testing.T, name string) *Container {
	t.Helper()
	scope := concurrency.NewGoroutineScope(context.Background(), name, nil)
	return NewContainer(ContainerConfig{
		ID:    ContainerID(name),
		Spec:  ContainerSpec{Name: name, AgentType: "engineer"},
		Scope: scope,
	})
}

func TestPod_InitialState(t *testing.T) {
	pod := testPod(t)

	if pod.Phase() != PodPending {
		t.Fatalf("expected Pending, got %v", pod.Phase())
	}
	if pod.ID() != "pod-1" {
		t.Fatalf("expected pod-1, got %s", pod.ID())
	}
	if pod.IsTerminal() {
		t.Fatal("new pod should not be terminal")
	}
}

func TestPod_SetAndGetPhase(t *testing.T) {
	pod := testPod(t)

	pod.SetPhase(PodRunning)
	if pod.Phase() != PodRunning {
		t.Fatalf("expected Running, got %v", pod.Phase())
	}

	pod.SetPhase(PodSucceeded)
	if pod.Phase() != PodSucceeded {
		t.Fatalf("expected Succeeded, got %v", pod.Phase())
	}
}

func TestPod_IsTerminal(t *testing.T) {
	pod := testPod(t)

	pod.SetPhase(PodSucceeded)
	if !pod.IsTerminal() {
		t.Fatal("Succeeded should be terminal")
	}

	pod.SetPhase(PodFailed)
	if !pod.IsTerminal() {
		t.Fatal("Failed should be terminal")
	}

	pod.SetPhase(PodRunning)
	if pod.IsTerminal() {
		t.Fatal("Running should not be terminal")
	}
}

func TestPod_Conditions(t *testing.T) {
	pod := testPod(t)

	pod.SetCondition(PodCondition{
		Type:    PodInitialized,
		Status:  true,
		Reason:  "AllInitComplete",
		Message: "all init containers completed",
	})

	cond := pod.Condition(PodInitialized)
	if cond == nil {
		t.Fatal("condition should exist")
	}
	if !cond.Status {
		t.Fatal("condition should be true")
	}
	if cond.Reason != "AllInitComplete" {
		t.Fatalf("expected AllInitComplete, got %s", cond.Reason)
	}
	if cond.LastTransitionTime.IsZero() {
		t.Fatal("transition time should be set")
	}
}

func TestPod_ConditionNotSet(t *testing.T) {
	pod := testPod(t)

	cond := pod.Condition(PodReady)
	if cond != nil {
		t.Fatal("unset condition should be nil")
	}
}

func TestPod_ContainerLists(t *testing.T) {
	pod := testPod(t)

	init1 := testContainerForPod(t, "init-1")
	main1 := testContainerForPod(t, "main-1")
	main2 := testContainerForPod(t, "main-2")
	sidecar1 := testContainerForPod(t, "sidecar-1")

	pod.SetInitContainers([]*Container{init1})
	pod.SetMainContainers([]*Container{main1, main2})
	pod.SetSidecarContainers([]*Container{sidecar1})

	if len(pod.InitContainers()) != 1 {
		t.Fatalf("expected 1 init, got %d", len(pod.InitContainers()))
	}
	if len(pod.MainContainers()) != 2 {
		t.Fatalf("expected 2 main, got %d", len(pod.MainContainers()))
	}
	if len(pod.SidecarContainers()) != 1 {
		t.Fatalf("expected 1 sidecar, got %d", len(pod.SidecarContainers()))
	}

	all := pod.AllContainers()
	if len(all) != 4 {
		t.Fatalf("expected 4 total, got %d", len(all))
	}
}

func TestPod_DerivePhase_NoContainers(t *testing.T) {
	pod := testPod(t)

	phase := pod.DerivePhaseFromContainers()
	if phase != PodPending {
		t.Fatalf("expected Pending with no containers, got %v", phase)
	}
}

func TestPod_DerivePhase_Running(t *testing.T) {
	pod := testPod(t)

	main1 := testContainerForPod(t, "main-1")
	_ = main1.Start(context.Background())
	pod.SetMainContainers([]*Container{main1})

	phase := pod.DerivePhaseFromContainers()
	if phase != PodRunning {
		t.Fatalf("expected Running, got %v", phase)
	}
}

func TestPod_DerivePhase_Succeeded(t *testing.T) {
	pod := testPod(t)

	main1 := testContainerForPod(t, "main-1")
	_ = main1.Start(context.Background())
	_ = main1.Stop(context.Background())
	pod.SetMainContainers([]*Container{main1})

	phase := pod.DerivePhaseFromContainers()
	if phase != PodSucceeded {
		t.Fatalf("expected Succeeded, got %v", phase)
	}
}

func TestPod_DerivePhase_Failed(t *testing.T) {
	pod := testPod(t)

	main1 := testContainerForPod(t, "main-1")
	_ = main1.Start(context.Background())
	_ = main1.Fail(context.DeadlineExceeded)
	pod.SetMainContainers([]*Container{main1})

	phase := pod.DerivePhaseFromContainers()
	if phase != PodFailed {
		t.Fatalf("expected Failed, got %v", phase)
	}
}

func TestPod_Scope(t *testing.T) {
	pod := testPod(t)
	if pod.Scope() == nil {
		t.Fatal("scope should not be nil")
	}
}

func TestPod_Lifecycle(t *testing.T) {
	pod := testPod(t)
	if pod.Lifecycle() == nil {
		t.Fatal("lifecycle should not be nil")
	}
	if pod.Lifecycle().State() != concurrency.StateCreated {
		t.Fatalf("expected Created, got %v", pod.Lifecycle().State())
	}
}

func TestPodPhase_String(t *testing.T) {
	cases := []struct {
		phase PodPhase
		want  string
	}{
		{PodPending, "pending"},
		{PodRunning, "running"},
		{PodSucceeded, "succeeded"},
		{PodFailed, "failed"},
		{PodPhase(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.phase.String(); got != tc.want {
			t.Fatalf("PodPhase(%d).String() = %s, want %s", tc.phase, got, tc.want)
		}
	}
}
