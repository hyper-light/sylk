package container

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

// mockAgent is defined in agent_probes_test.go as well, but this file
// tests the ProbeFactory integration path in DefaultRuntime.

func TestProbeFactory_WiresProbes(t *testing.T) {
	budget := concurrency.NewGoroutineBudget(new(atomic.Int32))
	reg := NewContainerRegistry()

	called := false
	factory := func(agent ContainerAgent, spec *ContainerSpec) []ProbeSpec {
		called = true
		if agent == nil {
			t.Fatal("expected non-nil agent")
		}
		return []ProbeSpec{
			LivenessProbeSpec(agent, spec.GracefulStop),
		}
	}

	rt := NewDefaultRuntime(DefaultRuntimeConfig{
		Budget:   budget,
		Registry: reg,
		CreateAgent: func(_ context.Context, agentType string) (ContainerAgent, error) {
			return &mockAgent{id: "test-1", agentType: agentType}, nil
		},
		ProbeFactory: factory,
	})

	spec := ContainerSpec{
		Name:         "test-agent",
		AgentType:    "test",
		GracefulStop: 30 * time.Second,
		Resources: ResourceSpec{
			GoroutineLimit: 4,
		},
	}

	c, err := rt.CreateContainer(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	if !called {
		t.Fatal("ProbeFactory was not called")
	}

	// Probes should be on the spec now.
	containerSpec := c.Spec()
	if len(containerSpec.Probes) != 1 {
		t.Fatalf("expected 1 probe, got %d", len(containerSpec.Probes))
	}
	if containerSpec.Probes[0].Type != ProbeLiveness {
		t.Fatalf("expected ProbeLiveness, got %v", containerSpec.Probes[0].Type)
	}
}

func TestProbeFactory_NilAgent_SkipsFactory(t *testing.T) {
	budget := concurrency.NewGoroutineBudget(new(atomic.Int32))
	reg := NewContainerRegistry()

	called := false
	factory := func(_ ContainerAgent, _ *ContainerSpec) []ProbeSpec {
		called = true
		return nil
	}

	rt := NewDefaultRuntime(DefaultRuntimeConfig{
		Budget:       budget,
		Registry:     reg,
		CreateAgent:  nil, // No agent creator — agent will be nil
		ProbeFactory: factory,
	})

	spec := ContainerSpec{
		Name:      "no-agent",
		AgentType: "none",
		Resources: ResourceSpec{
			GoroutineLimit: 2,
		},
	}

	_, err := rt.CreateContainer(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	if called {
		t.Fatal("ProbeFactory should not be called when agent is nil")
	}
}

func TestProbeFactory_NilFactory_NoProbes(t *testing.T) {
	budget := concurrency.NewGoroutineBudget(new(atomic.Int32))
	reg := NewContainerRegistry()

	rt := NewDefaultRuntime(DefaultRuntimeConfig{
		Budget:   budget,
		Registry: reg,
		CreateAgent: func(_ context.Context, agentType string) (ContainerAgent, error) {
			return &mockAgent{id: "test-1", agentType: agentType}, nil
		},
		ProbeFactory: nil, // No factory
	})

	spec := ContainerSpec{
		Name:      "no-probes",
		AgentType: "test",
		Resources: ResourceSpec{
			GoroutineLimit: 2,
		},
	}

	c, err := rt.CreateContainer(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	if len(c.Spec().Probes) != 0 {
		t.Fatalf("expected 0 probes, got %d", len(c.Spec().Probes))
	}
}
