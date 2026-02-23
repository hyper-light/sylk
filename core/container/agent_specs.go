package container

import (
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/handoff"
)

// goroutineLimitByCategory derives hard goroutine limits from agent category.
// Knowledge agents manage large context windows requiring more concurrent
// operations. Standalone agents need moderate concurrency. Pipeline agents
// are sequentially driven with minimal concurrent work.
var goroutineLimitByCategory = map[handoff.AgentCategory]int64{
	handoff.CategoryKnowledge:  32,
	handoff.CategoryStandalone: 16,
	handoff.CategoryPipeline:   8,
}

// restartPolicyByCategory derives restart behavior from agent category.
// Knowledge and standalone agents restart on failure to preserve session
// continuity. Pipeline agents never restart because the pipeline coordinator
// manages their lifecycle externally.
var restartPolicyByCategory = map[handoff.AgentCategory]RestartPolicy{
	handoff.CategoryKnowledge:  RestartOnFailure,
	handoff.CategoryStandalone: RestartOnFailure,
	handoff.CategoryPipeline:   RestartNever,
}

// gracefulStopMin is the minimum grace period for container shutdown.
const gracefulStopMin = 5 * time.Second

// gracefulStopMax is the maximum grace period for container shutdown.
const gracefulStopMax = 60 * time.Second

// gracefulStopTokenDivisor converts context window tokens to seconds.
// A 200K window yields 20s, a 1M window yields 100s (before clamping).
const gracefulStopTokenDivisor = 10_000

// AgentSpecRegistry converts handoff.AgentDescriptor metadata into
// ContainerSpec values suitable for the container runtime.
type AgentSpecRegistry struct {
	descriptors *handoff.DescriptorRegistry
}

// NewAgentSpecRegistry creates a registry backed by the given descriptor source.
func NewAgentSpecRegistry(reg *handoff.DescriptorRegistry) *AgentSpecRegistry {
	return &AgentSpecRegistry{descriptors: reg}
}

// Descriptors returns the underlying descriptor registry.
func (r *AgentSpecRegistry) Descriptors() *handoff.DescriptorRegistry {
	return r.descriptors
}

// SpecForAgent builds a ContainerSpec from the registered descriptor for agentType.
// Returns an error if agentType is not found in the descriptor registry.
func (r *AgentSpecRegistry) SpecForAgent(agentType string) (ContainerSpec, error) {
	desc, ok := r.descriptors.Get(agentType)
	if !ok {
		return ContainerSpec{}, fmt.Errorf("agent_specs: unknown agent type %q", agentType)
	}

	goroutineLimit := goroutineLimitByCategory[desc.Category]

	return ContainerSpec{
		Name:          agentType,
		AgentType:     desc.AgentType,
		Role:          RoleMain,
		RestartPolicy: restartPolicyByCategory[desc.Category],
		GracefulStop:  gracefulStopDuration(desc.ContextWindow),
		Descriptor: AgentDescriptorRef{
			AgentType:     desc.AgentType,
			ModelID:       desc.ModelID,
			ContextWindow: desc.ContextWindow,
			Category:      int(desc.Category),
		},
		Resources: ResourceSpec{
			ContextWindowLimit:   desc.ContextWindow,
			ContextWindowRequest: desc.ContextWindow * 3 / 4,
			GoroutineLimit:       goroutineLimit,
			GoroutineRequest:     goroutineLimit / 2,
		},
		Labels: map[string]string{
			"agent_type": agentType,
			"model":      desc.ModelID,
			"category":   desc.Category.String(),
		},
	}, nil
}

// gracefulStopDuration derives the grace period from context window size.
// Larger context windows need more time for graceful serialization during
// shutdown. The formula produces:
//
//	200K tokens -> 20s, clamped to min 5s
//	1M tokens   -> 100s, clamped to max 60s
func gracefulStopDuration(contextWindow int) time.Duration {
	seconds := contextWindow / gracefulStopTokenDivisor
	clamped := min(max(seconds, int(gracefulStopMin/time.Second)), int(gracefulStopMax/time.Second))
	return time.Duration(clamped) * time.Second
}
