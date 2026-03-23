package cmd

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
	csecurity "github.com/adalundhe/sylk/core/container/security"
	"github.com/adalundhe/sylk/core/providers"
)

type testModelSwappableAgent struct {
	id        string
	agentType string
	model     string
}

func (a *testModelSwappableAgent) AgentID() string   { return a.id }
func (a *testModelSwappableAgent) AgentType() string { return a.agentType }
func (a *testModelSwappableAgent) Terminate(context.Context) error {
	return nil
}
func (a *testModelSwappableAgent) SwapModel(_ context.Context, modelID string, _ providers.ProviderAdapter) error {
	a.model = modelID
	return nil
}
func (a *testModelSwappableAgent) CurrentModel() string { return a.model }
func (a *testModelSwappableAgent) SupportedModels() []container.ModelOption {
	return []container.ModelOption{
		{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6"},
		{ID: "gpt-5.4-pro", DisplayName: "GPT-5.4 Pro"},
	}
}

type recordingModelSwapActivator struct {
	reg   *container.ContainerRegistry
	calls []string
}

func (a *recordingModelSwapActivator) EnsureActive(ctx context.Context, agentType string) (*container.Container, error) {
	a.calls = append(a.calls, agentType)
	ctr := newTestSwappableContainer(agentType+"-1", agentType)
	if err := a.reg.Register(ctr); err != nil && err != container.ErrContainerExists {
		return nil, err
	}
	return ctr, nil
}

func newTestSwappableContainer(id, agentType string) *container.Container {
	scope := concurrency.NewGoroutineScope(context.Background(), id, nil)
	secCtx := csecurity.NewSecurityContext(csecurity.SecurityContextConfig{
		ContainerID: id,
		AgentID:     id,
		Role:        "worker",
	})
	return container.NewContainer(container.ContainerConfig{
		ID: container.ContainerID(id),
		Spec: container.ContainerSpec{
			Name:      id,
			AgentType: agentType,
		},
		Scope:  scope,
		SecCtx: secCtx,
		Agent: &testModelSwappableAgent{
			id:        id,
			agentType: agentType,
			model:     "claude-opus-4-6",
		},
	})
}

func TestResolveModelSwapContainer_EnsuresArchitectActiveWhenMissing(t *testing.T) {
	reg := container.NewContainerRegistry()
	activator := &recordingModelSwapActivator{reg: reg}

	ctr, err := resolveModelSwapContainer(context.Background(), reg, activator, "architect")
	if err != nil {
		t.Fatalf("resolveModelSwapContainer() error = %v", err)
	}
	if ctr == nil {
		t.Fatal("expected architect container")
	}
	if len(activator.calls) != 1 || activator.calls[0] != "architect" {
		t.Fatalf("EnsureActive calls = %v, want [architect]", activator.calls)
	}
	if ctr.Spec().AgentType != "architect" {
		t.Fatalf("container agent type = %q, want architect", ctr.Spec().AgentType)
	}
}

func TestResolveModelSwapContainer_UsesActivatedArchitectOverStaleRegisteredContainer(t *testing.T) {
	reg := container.NewContainerRegistry()
	stale := newTestSwappableContainer("architect-stale", "architect")
	if err := reg.Register(stale); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	activator := &recordingModelSwapActivator{reg: reg}

	ctr, err := resolveModelSwapContainer(context.Background(), reg, activator, "architect")
	if err != nil {
		t.Fatalf("resolveModelSwapContainer() error = %v", err)
	}
	if ctr == nil {
		t.Fatal("expected architect container")
	}
	if len(activator.calls) != 1 || activator.calls[0] != "architect" {
		t.Fatalf("EnsureActive calls = %v, want [architect]", activator.calls)
	}
	if ctr.ID() == stale.ID() {
		t.Fatalf("resolved stale architect container %q instead of activated live container", ctr.ID())
	}
}

func TestResolveModelSwapContainer_DoesNotActivateNonArchitectWhenContainerExists(t *testing.T) {
	reg := container.NewContainerRegistry()
	if err := reg.Register(newTestSwappableContainer("guardian-1", "guardian")); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	activator := &recordingModelSwapActivator{reg: reg}

	ctr, err := resolveModelSwapContainer(context.Background(), reg, activator, "guardian")
	if err != nil {
		t.Fatalf("resolveModelSwapContainer() error = %v", err)
	}
	if ctr == nil {
		t.Fatal("expected guardian container")
	}
	if len(activator.calls) != 0 {
		t.Fatalf("EnsureActive calls = %v, want none", activator.calls)
	}
}

func TestBuildModelSwapper_ReturnsErrorWhenPriorityMissing(t *testing.T) {
	reg := container.NewContainerRegistry()
	if err := reg.Register(newTestSwappableContainer("custom-1", "custom")); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	swap := buildModelSwapper(reg, nil, nil, nil, nil, nil)
	err := swap(context.Background(), "custom", "claude-opus-4-6")
	if err == nil {
		t.Fatal("expected error for missing model swap priority")
	}
}
