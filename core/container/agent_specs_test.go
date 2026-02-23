package container

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/handoff"
)

func TestAgentSpecRegistry_SpecForAgent(t *testing.T) {
	reg := handoff.NewDescriptorRegistry()
	specReg := NewAgentSpecRegistry(reg)

	spec, err := specReg.SpecForAgent("guide")
	if err != nil {
		t.Fatalf("SpecForAgent(guide) failed: %v", err)
	}
	if spec.AgentType != "guide" {
		t.Fatalf("expected AgentType guide, got %q", spec.AgentType)
	}
	if spec.Role != RoleMain {
		t.Fatalf("expected RoleMain, got %v", spec.Role)
	}
}

func TestAgentSpecRegistry_ResourcesFromDescriptor(t *testing.T) {
	reg := handoff.NewDescriptorRegistry()
	specReg := NewAgentSpecRegistry(reg)

	desc, ok := reg.Get("guide")
	if !ok {
		t.Fatal("guide descriptor not found")
	}

	spec, err := specReg.SpecForAgent("guide")
	if err != nil {
		t.Fatalf("SpecForAgent failed: %v", err)
	}

	if spec.Resources.ContextWindowLimit != desc.ContextWindow {
		t.Fatalf("ContextWindowLimit %d != descriptor ContextWindow %d",
			spec.Resources.ContextWindowLimit, desc.ContextWindow)
	}

	expectedRequest := desc.ContextWindow * 3 / 4
	if spec.Resources.ContextWindowRequest != expectedRequest {
		t.Fatalf("ContextWindowRequest %d != expected %d",
			spec.Resources.ContextWindowRequest, expectedRequest)
	}
}

func TestAgentSpecRegistry_GoroutineLimitsByCategory(t *testing.T) {
	reg := handoff.NewDescriptorRegistry()
	specReg := NewAgentSpecRegistry(reg)

	tests := []struct {
		agentType string
		category  handoff.AgentCategory
		limit     int64
	}{
		{"librarian", handoff.CategoryKnowledge, 32},
		{"guide", handoff.CategoryStandalone, 16},
		{"engineer", handoff.CategoryPipeline, 8},
	}

	for _, tt := range tests {
		spec, err := specReg.SpecForAgent(tt.agentType)
		if err != nil {
			t.Fatalf("SpecForAgent(%s) failed: %v", tt.agentType, err)
		}
		if spec.Resources.GoroutineLimit != tt.limit {
			t.Fatalf("%s: GoroutineLimit %d != expected %d",
				tt.agentType, spec.Resources.GoroutineLimit, tt.limit)
		}
		if spec.Resources.GoroutineRequest != tt.limit/2 {
			t.Fatalf("%s: GoroutineRequest %d != expected %d",
				tt.agentType, spec.Resources.GoroutineRequest, tt.limit/2)
		}
	}
}

func TestAgentSpecRegistry_RestartPolicyByCategory(t *testing.T) {
	reg := handoff.NewDescriptorRegistry()
	specReg := NewAgentSpecRegistry(reg)

	tests := []struct {
		agentType string
		policy    RestartPolicy
	}{
		{"librarian", RestartOnFailure},
		{"guide", RestartOnFailure},
		{"engineer", RestartNever},
	}

	for _, tt := range tests {
		spec, err := specReg.SpecForAgent(tt.agentType)
		if err != nil {
			t.Fatalf("SpecForAgent(%s) failed: %v", tt.agentType, err)
		}
		if spec.RestartPolicy != tt.policy {
			t.Fatalf("%s: RestartPolicy %v != expected %v",
				tt.agentType, spec.RestartPolicy, tt.policy)
		}
	}
}

func TestAgentSpecRegistry_GracefulStopClamped(t *testing.T) {
	reg := handoff.NewDescriptorRegistry()
	specReg := NewAgentSpecRegistry(reg)

	for _, desc := range reg.All() {
		spec, err := specReg.SpecForAgent(desc.AgentType)
		if err != nil {
			t.Fatalf("SpecForAgent(%s) failed: %v", desc.AgentType, err)
		}
		if spec.GracefulStop < gracefulStopMin || spec.GracefulStop > gracefulStopMax {
			t.Fatalf("%s: GracefulStop %v out of range [%v, %v]",
				desc.AgentType, spec.GracefulStop, gracefulStopMin, gracefulStopMax)
		}
	}
}

func TestAgentSpecRegistry_Labels(t *testing.T) {
	reg := handoff.NewDescriptorRegistry()
	specReg := NewAgentSpecRegistry(reg)

	spec, err := specReg.SpecForAgent("architect")
	if err != nil {
		t.Fatalf("SpecForAgent(architect) failed: %v", err)
	}
	if spec.Labels["agent_type"] != "architect" {
		t.Fatalf("expected label agent_type=architect, got %q", spec.Labels["agent_type"])
	}
	if spec.Labels["model"] == "" {
		t.Fatal("expected non-empty model label")
	}
	if spec.Labels["category"] == "" {
		t.Fatal("expected non-empty category label")
	}
}

func TestAgentSpecRegistry_UnknownAgent(t *testing.T) {
	reg := handoff.NewDescriptorRegistry()
	specReg := NewAgentSpecRegistry(reg)

	_, err := specReg.SpecForAgent("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown agent type")
	}
}

func TestGracefulStopDuration(t *testing.T) {
	tests := []struct {
		contextWindow int
		expected      time.Duration
	}{
		{10_000, gracefulStopMin},   // 1s → clamped to 5s
		{200_000, 20 * time.Second}, // 20s
		{600_000, gracefulStopMax},   // 60s → clamped to 60s
		{2_000_000, gracefulStopMax}, // 200s → clamped to 60s
	}

	for _, tt := range tests {
		got := gracefulStopDuration(tt.contextWindow)
		if got != tt.expected {
			t.Fatalf("gracefulStopDuration(%d) = %v, want %v",
				tt.contextWindow, got, tt.expected)
		}
	}
}

func TestAgentSpecRegistry_AllDescriptorsProduceValidSpecs(t *testing.T) {
	reg := handoff.NewDescriptorRegistry()
	specReg := NewAgentSpecRegistry(reg)

	for _, desc := range reg.All() {
		spec, err := specReg.SpecForAgent(desc.AgentType)
		if err != nil {
			t.Fatalf("SpecForAgent(%s) failed: %v", desc.AgentType, err)
		}
		if err := ValidateContainerSpec(&spec); err != nil {
			t.Fatalf("ValidateContainerSpec(%s) failed: %v", desc.AgentType, err)
		}
	}
}
