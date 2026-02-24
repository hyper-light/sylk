package container

import (
	"github.com/adalundhe/sylk/core/handoff"
)

// agentSecurityProfile maps agent categories to their security properties.
// Standalone agents (guide, orchestrator) act as supervisors with full
// topic access and escalation rights. Knowledge agents are unrestricted
// workers. Pipeline agents are read-only workers.
type agentSecurityProfile struct {
	role          string
	pathRead      []string
	pathWrite     []string
	canEscalate   bool
	runAsReadOnly bool
}

// securityProfileByCategory derives security posture from agent category.
// Supervisors (Standalone) have the broadest permissions because they
// coordinate all other agents. Knowledge agents read/write freely.
// Pipeline agents are sandboxed to read-only filesystem access.
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
		pathRead:      []string{"/"},
		pathWrite:     nil,
		canEscalate:   false,
		runAsReadOnly: true,
	},
}

// inspectorReadOnlyOverride forces both inspector variants to read-only
// filesystem access. Inspectors must NEVER modify files — they observe and
// report only.
var inspectorReadOnlyOverride = agentSecurityProfile{
	role:          "worker",
	pathRead:      []string{"*"},
	pathWrite:     nil,
	canEscalate:   false,
	runAsReadOnly: true,
}

// agentTypeOverrides maps specific agent types to security profiles that
// deviate from their category default. The global inspector is registered
// as CategoryStandalone for bus permissions but must be filesystem read-only.
var agentTypeOverrides = map[string]agentSecurityProfile{
	"inspector":          inspectorReadOnlyOverride,
	"inspector-pipeline": inspectorReadOnlyOverride,
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
