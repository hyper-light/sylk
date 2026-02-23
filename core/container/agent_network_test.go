package container

import (
	"testing"

	"github.com/adalundhe/sylk/core/handoff"
)

func TestBuildNetworkPolicies_NonEmpty(t *testing.T) {
	reg := handoff.NewDescriptorRegistry()
	policies := BuildNetworkPolicies(reg.All())

	if len(policies) == 0 {
		t.Fatal("expected non-empty policies")
	}
}

func TestBuildNetworkPolicies_GuideHubIngress(t *testing.T) {
	reg := handoff.NewDescriptorRegistry()
	policies := BuildNetworkPolicies(reg.All())

	var found bool
	for _, p := range policies {
		if p.Name == "guide-hub-ingress" {
			found = true
			if len(p.Ingress) == 0 {
				t.Fatal("guide-hub-ingress should have ingress rules")
			}
			if p.Ingress[0].AllowedTopics[0] != "guide.requests" {
				t.Fatalf("expected topic guide.requests, got %q",
					p.Ingress[0].AllowedTopics[0])
			}
		}
	}
	if !found {
		t.Fatal("missing guide-hub-ingress policy")
	}
}

func TestBuildNetworkPolicies_RegistryBroadcast(t *testing.T) {
	reg := handoff.NewDescriptorRegistry()
	policies := BuildNetworkPolicies(reg.All())

	var found bool
	for _, p := range policies {
		if p.Name == "registry-broadcast" {
			found = true
			if len(p.Ingress) == 0 || len(p.Egress) == 0 {
				t.Fatal("registry-broadcast should have both ingress and egress")
			}
		}
	}
	if !found {
		t.Fatal("missing registry-broadcast policy")
	}
}

func TestBuildNetworkPolicies_PerAgentPolicies(t *testing.T) {
	reg := handoff.NewDescriptorRegistry()
	policies := BuildNetworkPolicies(reg.All())

	// Every agent type should have an ingress and egress policy.
	for _, desc := range reg.All() {
		ingressName := "per-agent-ingress-" + desc.AgentType
		egressName := "per-agent-egress-" + desc.AgentType

		var foundIngress, foundEgress bool
		for _, p := range policies {
			if p.Name == ingressName {
				foundIngress = true
			}
			if p.Name == egressName {
				foundEgress = true
			}
		}
		if !foundIngress {
			t.Fatalf("missing %s policy", ingressName)
		}
		if !foundEgress {
			t.Fatalf("missing %s policy", egressName)
		}
	}
}

func TestBuildNetworkPolicies_OrchestratorPipeline(t *testing.T) {
	reg := handoff.NewDescriptorRegistry()
	policies := BuildNetworkPolicies(reg.All())

	var found bool
	for _, p := range policies {
		if p.Name == "orchestrator-pipeline-egress" {
			found = true
			if len(p.Egress) == 0 {
				t.Fatal("orchestrator-pipeline-egress should have egress rules")
			}
			// Verify pipeline agent types are included.
			peerTypes := p.Egress[0].To[0].AgentTypes
			if len(peerTypes) == 0 {
				t.Fatal("expected pipeline agent types in orchestrator egress")
			}
		}
	}
	if !found {
		t.Fatal("missing orchestrator-pipeline-egress policy")
	}
}

func TestBuildNetworkPolicies_ArchitectConsultation(t *testing.T) {
	reg := handoff.NewDescriptorRegistry()
	policies := BuildNetworkPolicies(reg.All())

	var found bool
	for _, p := range policies {
		if p.Name == "architect-consultation-egress" {
			found = true
			if len(p.Egress) == 0 {
				t.Fatal("architect-consultation-egress should have egress rules")
			}
			peerTypes := p.Egress[0].To[0].AgentTypes
			if len(peerTypes) == 0 {
				t.Fatal("expected knowledge agent types in architect egress")
			}
		}
	}
	if !found {
		t.Fatal("missing architect-consultation-egress policy")
	}
}

func TestCollectAgentTypes(t *testing.T) {
	descriptors := []handoff.AgentDescriptor{
		{AgentType: "guide"},
		{AgentType: "architect"},
	}
	types := collectAgentTypes(descriptors)
	if len(types) != 2 {
		t.Fatalf("expected 2 types, got %d", len(types))
	}
}

func TestCollectByCategory(t *testing.T) {
	reg := handoff.NewDescriptorRegistry()
	pipeline := collectByCategory(reg.All(), handoff.CategoryPipeline)
	if len(pipeline) == 0 {
		t.Fatal("expected at least one pipeline agent")
	}
	knowledge := collectByCategory(reg.All(), handoff.CategoryKnowledge)
	if len(knowledge) == 0 {
		t.Fatal("expected at least one knowledge agent")
	}
}
