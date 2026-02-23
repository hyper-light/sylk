package daemon

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/container"
)

func testDaemonSetSpec(name, agentType string) DaemonSetSpec {
	return DaemonSetSpec{
		Name: name,
		ContainerSpec: container.ContainerSpec{
			Name:      name,
			AgentType: agentType,
		},
		Priority: 100,
	}
}

func testDaemonController(t *testing.T) (*DaemonSetController, *container.DefaultRuntime) {
	t.Helper()
	rt := container.NewDefaultRuntime(container.DefaultRuntimeConfig{
		Registry: container.NewContainerRegistry(),
		CreateAgent: func(_ context.Context, agentType string) (container.ContainerAgent, error) {
			return &stubAgent{id: "d-" + agentType, agentType: agentType}, nil
		},
		ParentCtx: context.Background(),
	})
	dc := NewDaemonSetController(rt, rt.Registry())
	return dc, rt
}

type stubAgent struct {
	id        string
	agentType string
}

func (s *stubAgent) AgentID() string                        { return s.id }
func (s *stubAgent) AgentType() string                      { return s.agentType }
func (s *stubAgent) Terminate(_ context.Context) error       { return nil }

func TestDaemonSetController_ReconcileCreatesInstance(t *testing.T) {
	dc, _ := testDaemonController(t)

	dc.Apply(testDaemonSetSpec("guide-daemon", "guide"))

	if err := dc.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	if dc.InstanceCount() != 1 {
		t.Fatalf("expected 1 instance, got %d", dc.InstanceCount())
	}
}

func TestDaemonSetController_ReconcileRemovesStale(t *testing.T) {
	dc, _ := testDaemonController(t)

	dc.Apply(testDaemonSetSpec("guide-daemon", "guide"))
	dc.Reconcile(context.Background())

	// Remove spec
	dc.Remove("guide-daemon")
	dc.Reconcile(context.Background())

	if dc.InstanceCount() != 0 {
		t.Fatalf("expected 0 instances after removal, got %d", dc.InstanceCount())
	}
}

func TestDaemonSetController_Status(t *testing.T) {
	dc, _ := testDaemonController(t)

	dc.Apply(testDaemonSetSpec("guide-daemon", "guide"))
	dc.Reconcile(context.Background())

	statuses := dc.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Name != "guide-daemon" {
		t.Fatalf("expected guide-daemon, got %s", statuses[0].Name)
	}
	if !statuses[0].Running {
		t.Fatal("daemon should be running")
	}
}

func TestDaemonSetController_MultipleSpecs(t *testing.T) {
	dc, _ := testDaemonController(t)

	dc.Apply(testDaemonSetSpec("guide-daemon", "guide"))
	dc.Apply(testDaemonSetSpec("inspector-daemon", "inspector"))
	dc.Apply(testDaemonSetSpec("tester-daemon", "tester"))

	dc.Reconcile(context.Background())

	if dc.InstanceCount() != 3 {
		t.Fatalf("expected 3 instances, got %d", dc.InstanceCount())
	}
	if dc.SpecCount() != 3 {
		t.Fatalf("expected 3 specs, got %d", dc.SpecCount())
	}
}

func TestDaemonSetController_IdempotentReconcile(t *testing.T) {
	dc, _ := testDaemonController(t)

	dc.Apply(testDaemonSetSpec("guide-daemon", "guide"))

	// Multiple reconciles should be idempotent
	dc.Reconcile(context.Background())
	dc.Reconcile(context.Background())
	dc.Reconcile(context.Background())

	if dc.InstanceCount() != 1 {
		t.Fatalf("expected 1 instance after idempotent reconciles, got %d", dc.InstanceCount())
	}
}

func TestToleration_Matches(t *testing.T) {
	tol := Toleration{
		Key:      "resource-pressure",
		Operator: "Exists",
		Effect:   "NoSchedule",
	}
	if !tol.Matches("resource-pressure", "", "NoSchedule") {
		t.Fatal("Exists operator should match any value")
	}
	if tol.Matches("other-key", "", "NoSchedule") {
		t.Fatal("should not match different key")
	}
	if tol.Matches("resource-pressure", "", "NoExecute") {
		t.Fatal("should not match different effect")
	}
}

func TestToleration_EqualOperator(t *testing.T) {
	tol := Toleration{
		Key:      "tier",
		Operator: "Equal",
		Value:    "system",
		Effect:   "",
	}
	if !tol.Matches("tier", "system", "NoSchedule") {
		t.Fatal("Equal should match exact value")
	}
	if tol.Matches("tier", "user", "NoSchedule") {
		t.Fatal("Equal should not match different value")
	}
}

func TestHasToleration(t *testing.T) {
	tolerations := []Toleration{
		{Key: "a", Operator: "Exists"},
		{Key: "b", Operator: "Equal", Value: "v"},
	}
	if !HasToleration(tolerations, "a", "", "") {
		t.Fatal("should find toleration for key 'a'")
	}
	if !HasToleration(tolerations, "b", "v", "") {
		t.Fatal("should find toleration for key 'b' with value 'v'")
	}
	if HasToleration(tolerations, "c", "", "") {
		t.Fatal("should not find toleration for key 'c'")
	}
}
