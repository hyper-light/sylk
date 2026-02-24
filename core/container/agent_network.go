package container

import (
	"github.com/adalundhe/sylk/core/container/network"
	"github.com/adalundhe/sylk/core/handoff"
)

// BuildNetworkPolicies generates default-deny network policies from agent
// descriptors. Only explicitly declared flows are permitted.
func BuildNetworkPolicies(descriptors []handoff.AgentDescriptor) []*network.NetworkPolicy {
	allTypes := collectAgentTypes(descriptors)
	pipelineTypes := collectByCategory(descriptors, handoff.CategoryPipeline)
	knowledgeTypes := collectByCategory(descriptors, handoff.CategoryKnowledge)

	policies := make([]*network.NetworkPolicy, 0, len(descriptors)*2+5)
	policies = append(policies, guideHubIngress(allTypes))
	policies = append(policies, perAgentPolicies(allTypes)...)
	policies = append(policies, registryBroadcast())
	policies = append(policies, orchestratorPipelineEgress(pipelineTypes))
	policies = append(policies, architectConsultationEgress(knowledgeTypes))
	policies = append(policies, auditResultsPublish())
	policies = append(policies, auditResultsSubscribe())
	return policies
}

// guideHubIngress creates the policy allowing all agent types to send
// messages to the guide on the guide.requests topic.
func guideHubIngress(allTypes []string) *network.NetworkPolicy {
	return &network.NetworkPolicy{
		Name:        "guide-hub-ingress",
		PodSelector: map[string]string{"agent-type": "guide"},
		Ingress: []network.IngressRule{{
			From:          []network.PeerSelector{{AgentTypes: allTypes}},
			AllowedTopics: []string{"guide.requests"},
		}},
	}
}

// perAgentPolicies generates an ingress and egress policy for each agent type.
func perAgentPolicies(allTypes []string) []*network.NetworkPolicy {
	policies := make([]*network.NetworkPolicy, 0, len(allTypes)*2)
	for _, agentType := range allTypes {
		policies = append(policies, perAgentIngress(agentType))
		policies = append(policies, perAgentEgress(agentType))
	}
	return policies
}

// perAgentIngress creates a policy allowing the guide to send requests
// to a specific agent type on its request topic wildcard.
func perAgentIngress(agentType string) *network.NetworkPolicy {
	return &network.NetworkPolicy{
		Name:        "per-agent-ingress-" + agentType,
		PodSelector: map[string]string{"agent-type": agentType},
		Ingress: []network.IngressRule{{
			From:          []network.PeerSelector{{AgentTypes: []string{"guide"}}},
			AllowedTopics: []string{"request." + agentType + ".*"},
		}},
	}
}

// perAgentEgress creates a policy allowing an agent type to send to
// guide.requests and its own response topic wildcard.
func perAgentEgress(agentType string) *network.NetworkPolicy {
	return &network.NetworkPolicy{
		Name:        "per-agent-egress-" + agentType,
		PodSelector: map[string]string{"agent-type": agentType},
		Egress: []network.EgressRule{{
			To:            []network.PeerSelector{{AgentTypes: []string{"guide"}}},
			AllowedTopics: []string{"guide.requests", "response." + agentType + ".*"},
		}},
	}
}

// registryBroadcast creates the policy allowing all agents to publish
// and subscribe to the agents.registry topic.
func registryBroadcast() *network.NetworkPolicy {
	return &network.NetworkPolicy{
		Name:        "registry-broadcast",
		PodSelector: map[string]string{},
		Ingress: []network.IngressRule{{
			From:          []network.PeerSelector{{}},
			AllowedTopics: []string{"agents.registry"},
		}},
		Egress: []network.EgressRule{{
			To:            []network.PeerSelector{{}},
			AllowedTopics: []string{"agents.registry"},
		}},
	}
}

// orchestratorPipelineEgress creates the policy allowing the orchestrator
// to send requests to pipeline agent types.
func orchestratorPipelineEgress(pipelineTypes []string) *network.NetworkPolicy {
	return &network.NetworkPolicy{
		Name:        "orchestrator-pipeline-egress",
		PodSelector: map[string]string{"agent-type": "orchestrator"},
		Egress: []network.EgressRule{{
			To:            []network.PeerSelector{{AgentTypes: pipelineTypes}},
			AllowedTopics: []string{"request.*.*"},
		}},
	}
}

// architectConsultationEgress creates the policy allowing the architect
// to send requests to knowledge agent types.
func architectConsultationEgress(knowledgeTypes []string) *network.NetworkPolicy {
	return &network.NetworkPolicy{
		Name:        "architect-consultation-egress",
		PodSelector: map[string]string{"agent-type": "architect"},
		Egress: []network.EgressRule{{
			To:            []network.PeerSelector{{AgentTypes: knowledgeTypes}},
			AllowedTopics: []string{"request.*.*"},
		}},
	}
}

// auditResultsPublish creates the policy allowing inspector variants to publish
// audit findings to the audit.results topic.
func auditResultsPublish() *network.NetworkPolicy {
	return &network.NetworkPolicy{
		Name:        "audit-results-publish",
		PodSelector: map[string]string{},
		Egress: []network.EgressRule{{
			To:            []network.PeerSelector{{AgentTypes: []string{"inspector", "inspector-pipeline"}}},
			AllowedTopics: []string{"audit.results"},
		}},
	}
}

// auditResultsSubscribe creates the policy allowing the orchestrator and
// architect to subscribe to audit findings on the audit.results topic.
func auditResultsSubscribe() *network.NetworkPolicy {
	return &network.NetworkPolicy{
		Name:        "audit-results-subscribe",
		PodSelector: map[string]string{},
		Ingress: []network.IngressRule{{
			From:          []network.PeerSelector{{AgentTypes: []string{"orchestrator", "architect"}}},
			AllowedTopics: []string{"audit.results"},
		}},
	}
}

// collectAgentTypes extracts unique agent type strings from descriptors.
func collectAgentTypes(descriptors []handoff.AgentDescriptor) []string {
	types := make([]string, 0, len(descriptors))
	for _, d := range descriptors {
		types = append(types, d.AgentType)
	}
	return types
}

// collectByCategory extracts agent type strings matching the given category.
func collectByCategory(descriptors []handoff.AgentDescriptor, cat handoff.AgentCategory) []string {
	types := make([]string, 0, len(descriptors))
	for _, d := range descriptors {
		if d.Category == cat {
			types = append(types, d.AgentType)
		}
	}
	return types
}
