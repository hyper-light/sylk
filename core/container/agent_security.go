package container

import (
	"github.com/adalundhe/sylk/core/handoff"
)

// agentSecurityProfile maps agent categories to their security properties.
// Standalone agents (guide, orchestrator) act as supervisors with full
// topic access and escalation rights. Knowledge agents are unrestricted
// workers. Pipeline agents are VFS-scoped workers and must be allowed to
// mutate their mounted draft workspace.
type agentSecurityProfile struct {
	role           string
	pathRead       []string
	pathWrite      []string
	networkEgress  []string // Allowed egress domain patterns
	networkIngress []string // Allowed ingress content types
	canEscalate    bool
	runAsReadOnly  bool
}

// securityProfileByCategory derives security posture from agent category.
// Supervisors (Standalone) have the broadest permissions because they
// coordinate all other agents. Knowledge agents read/write freely.
// Pipeline agents read/write through VFS-backed pod volumes rather than
// direct disk mutation, so the runtime must not force them read-only.
var securityProfileByCategory = map[handoff.AgentCategory]agentSecurityProfile{
	handoff.CategoryStandalone: {
		role:          "supervisor",
		pathRead:      []string{"*"},
		pathWrite:     []string{"*"},
		canEscalate:   true,
		runAsReadOnly: false,
	},
	handoff.CategoryKnowledge: {
		role:          "worker",
		pathRead:      []string{"*"},
		pathWrite:     []string{"*"},
		canEscalate:   false,
		runAsReadOnly: false,
	},
	handoff.CategoryPipeline: {
		role:          "worker",
		pathRead:      []string{"*"},
		pathWrite:     []string{"*"},
		canEscalate:   false,
		runAsReadOnly: false,
	},
}

// academicNetworkProfile grants the Academic agent outbound network access
// for external research. Network egress requires user consent at fetch time
// (enforced by core/fetch); this capability merely permits the agent to
// attempt egress. All fetched content must pass Guardian quarantine inspection
// before ingestion.
var academicNetworkProfile = agentSecurityProfile{
	role:      "worker",
	pathRead:  []string{"*"},
	pathWrite: []string{"*"},
	networkEgress: []string{"*"}, // All domains; per-request consent enforced by fetch policy
	networkIngress: []string{
		"text/html",
		"text/plain",
		"application/pdf",
		"application/json",
		"text/markdown",
	},
	canEscalate:   false,
	runAsReadOnly: false,
}

// agentTypeOverrides maps specific agent types to security profiles that
// deviate from their category default. The academic agent is granted network
// egress for external research.
var agentTypeOverrides = map[string]agentSecurityProfile{
	"academic": academicNetworkProfile,
}

// BuildSecuritySpec constructs a SecurityContextSpec for an agent based on
// its descriptor category and topic naming convention. Agent-type overrides
// take precedence over category defaults. Publish/subscribe topics follow
// the Guide bus topic convention: agents publish to guide.requests and
// their own response/error topics, and subscribe to their own request
// topic and the registry broadcast.
func BuildSecuritySpec(desc handoff.AgentDescriptor) SecurityContextSpec {
	profile, ok := agentTypeOverrides[desc.AgentType]
	if !ok {
		profile = securityProfileByCategory[desc.Category]
	}

	publishTopics := agentPublishTopics(desc.AgentType)
	subscribeTopics := agentSubscribeTopics(desc.AgentType)

	// Inspector variants additionally publish to audit.results and subscribe
	// to request topics from the orchestrator.
	if desc.AgentType == "inspector" || desc.AgentType == "inspector-pipeline" {
		publishTopics = append(publishTopics, "audit.results")
	}

	return SecurityContextSpec{
		Role: profile.role,
		Capabilities: CapabilitySpec{
			PublishTopics:   publishTopics,
			SubscribeTopics: subscribeTopics,
			PathRead:        profile.pathRead,
			PathWrite:       profile.pathWrite,
			NetworkEgress:   profile.networkEgress,
			NetworkIngress:  profile.networkIngress,
			CanEscalate:     profile.canEscalate,
		},
		RunAsReadOnly: profile.runAsReadOnly,
	}
}

// agentPublishTopics returns the topics an agent is allowed to publish to.
// All agents can publish to guide.requests (for routing), their own
// response topic (for replies), and their own error topic (for failures).
func agentPublishTopics(agentType string) []string {
	return []string{
		"guide.requests",
		"response." + agentType + ".*",
		"error." + agentType + ".*",
	}
}

// agentSubscribeTopics returns the topics an agent is allowed to subscribe to.
// All agents receive on their own request topic and the global registry.
func agentSubscribeTopics(agentType string) []string {
	return []string{
		"request." + agentType + ".*",
		"agents.registry",
	}
}
