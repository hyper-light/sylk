package container

import (
	"context"
	"testing"
	"time"
)

type panicAgent struct{}

func (p *panicAgent) AgentID() string                   { panic("corrupted") }
func (p *panicAgent) AgentType() string                 { return "test" }
func (p *panicAgent) Terminate(_ context.Context) error { return nil }

func TestAgentLivenessProbe_Healthy(t *testing.T) {
	agent := &mockAgent{id: "test-1", agentType: "test"}
	probe := NewAgentLivenessProbe(agent)

	result, err := probe.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatal("expected success for healthy agent")
	}
}

func TestAgentLivenessProbe_PanicRecovery(t *testing.T) {
	probe := NewAgentLivenessProbe(&panicAgent{})

	result, err := probe.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure for panicking agent")
	}
	if result.Message == "" {
		t.Fatal("expected non-empty failure message")
	}
}

func TestBusReadinessProbe_Ready(t *testing.T) {
	probe := NewBusReadinessProbe("agent-1", func(id string) bool {
		return id == "agent-1"
	})

	result, err := probe.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatal("expected success for ready agent")
	}
}

func TestBusReadinessProbe_NotReady(t *testing.T) {
	probe := NewBusReadinessProbe("agent-1", func(_ string) bool {
		return false
	})

	result, err := probe.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure for unready agent")
	}
}

func TestLivenessProbeSpec_DerivedTiming(t *testing.T) {
	agent := &mockAgent{id: "test-1", agentType: "test"}
	graceful := 30 * time.Second

	spec := livenessProbeSpec(agent, graceful)
	if spec.Type != ProbeLiveness {
		t.Fatalf("expected ProbeLiveness, got %v", spec.Type)
	}
	if spec.Period != 5*time.Second {
		t.Fatalf("expected period 5s, got %v", spec.Period)
	}
	if spec.InitialDelay != 10*time.Second {
		t.Fatalf("expected initial delay 10s, got %v", spec.InitialDelay)
	}
	if spec.FailureThreshold != 3 {
		t.Fatalf("expected failure threshold 3, got %d", spec.FailureThreshold)
	}
}

func TestReadinessProbeSpec_FixedTiming(t *testing.T) {
	spec := readinessProbeSpec("agent-1", func(_ string) bool { return true })
	if spec.Type != ProbeReadiness {
		t.Fatalf("expected ProbeReadiness, got %v", spec.Type)
	}
	if spec.Period != busCloseTimeout {
		t.Fatalf("expected period %v, got %v", busCloseTimeout, spec.Period)
	}
	if spec.InitialDelay != 2*time.Second {
		t.Fatalf("expected initial delay 2s, got %v", spec.InitialDelay)
	}
}
