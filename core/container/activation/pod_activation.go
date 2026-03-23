package activation

import (
	"math"
	"time"

	"github.com/adalundhe/sylk/core/container/pod"
	"github.com/adalundhe/sylk/core/handoff"
)

// pipelinePodID is the well-known pod ID for the pipeline pod entry
// in the activation controller. Dynamic DAG-scoped pods are registered
// separately; this is the static "pipeline" activation slot.
const pipelinePodID = "pipeline"

// PipelineAgentTypes are the four agent types every pipeline requires.
var PipelineAgentTypes = []string{"engineer", "designer", "inspector-pipeline", "tester-pipeline"}

// PodActivationPolicies generates pod-level activation policies from
// agent descriptors. Pipeline agents are grouped into a single policy
// with one pod ID; standalone and daemon agents get individual policies.
//
// Returns:
//   - policies: for the ActivationController
//   - podPolicies: for AgentPod runtime construction (richer metadata)
//   - agentToPod: mapping for ControllerPodActivator.RegisterMapping
func PodActivationPolicies(descriptors []handoff.AgentDescriptor) (
	policies []*ActivationPolicy,
	podPolicies []*pod.PodPolicy,
	agentToPod map[string]string,
	err error,
) {
	agentToPod = make(map[string]string, len(descriptors))
	pipelineMembers := make([]string, 0, 4)
	seenPipeline := false

	for _, desc := range descriptors {
		if isPipelineAgent(desc.AgentType) {
			pipelineMembers = append(pipelineMembers, desc.AgentType)
			agentToPod[desc.AgentType] = pipelinePodID
			seenPipeline = true
			continue
		}

		podID := desc.AgentType // singleton/daemon pods use agent type as ID
		agentToPod[desc.AgentType] = podID

		pp, policyErr := singletonPodPolicy(desc)
		if policyErr != nil {
			return nil, nil, nil, policyErr
		}
		podPolicies = append(podPolicies, pp)
		policies = append(policies, toActivationPolicy(pp))
	}

	if seenPipeline {
		pp, policyErr := pipelinePodPolicy(pipelineMembers)
		if policyErr != nil {
			return nil, nil, nil, policyErr
		}
		podPolicies = append(podPolicies, pp)
		policies = append(policies, toActivationPolicy(pp))
	}

	return policies, podPolicies, agentToPod, nil
}

// toActivationPolicy converts a PodPolicy to an ActivationPolicy.
// Lives here (not on PodPolicy) to avoid a pod→activation import cycle.
func toActivationPolicy(pp *pod.PodPolicy) *ActivationPolicy {
	if pp.IsDaemon {
		return DaemonPolicy(string(pp.PodID))
	}
	return &ActivationPolicy{
		AgentType:        string(pp.PodID),
		Priority:         pp.Priority,
		MinTier:          pp.MinTier,
		IdleToWarm:       pp.IdleToWarm,
		IdleToCool:       pp.IdleToCool,
		IdleToCold:       pp.IdleToCold,
		IsDaemon:         pp.IsDaemon,
		PreWarmOnStartup: pp.PreWarmOnStartup,
	}
}

// singletonPodPolicy generates a PodPolicy for a single-agent pod.
func singletonPodPolicy(desc handoff.AgentDescriptor) (*pod.PodPolicy, error) {
	if isDaemonAgentType(desc.AgentType) {
		return &pod.PodPolicy{
			PodID:            desc.AgentType,
			PodType:          pod.PodTypeDaemon,
			MemberTypes:      []string{desc.AgentType},
			Priority:         math.MaxInt,
			MinTier:          TierHot,
			IsDaemon:         true,
			PreWarmOnStartup: true,
		}, nil
	}

	defaults, err := scaledDefaults(desc.Category)
	if err != nil {
		return nil, err
	}
	pp := &pod.PodPolicy{
		PodID:       desc.AgentType,
		PodType:     pod.PodTypeSingleton,
		MemberTypes: []string{desc.AgentType},
		MinTier:     TierCold,
		IdleToWarm:  defaults.IdleToWarm,
		IdleToCool:  defaults.IdleToCool,
		IdleToCold:  defaults.IdleToCold,
	}

	if shouldPreWarmAgentType(desc.AgentType) {
		pp.PreWarmOnStartup = true
	}

	return pp, nil
}

// pipelinePodPolicy generates a PodPolicy for the pipeline pod.
// Pipeline agents use halved idle thresholds (cheap to recreate).
func pipelinePodPolicy(memberTypes []string) (*pod.PodPolicy, error) {
	defaults, err := scaledDefaults(handoff.CategoryPipeline)
	if err != nil {
		return nil, err
	}
	return &pod.PodPolicy{
		PodID:       pipelinePodID,
		PodType:     pod.PodTypePipeline,
		MemberTypes: memberTypes,
		MinTier:     TierCold,
		IdleToWarm:  defaults.IdleToWarm,
		IdleToCool:  defaults.IdleToCool,
		IdleToCold:  defaults.IdleToCold,
	}, nil
}

// isPipelineAgent returns true if the agent type belongs in the pipeline pod.
func isPipelineAgent(agentType string) bool {
	for _, pt := range PipelineAgentTypes {
		if agentType == pt {
			return true
		}
	}
	return false
}

// RegisterPodMappings configures a ControllerPodActivator with the
// agentType→podID mappings from PodActivationPolicies.
func RegisterPodMappings(activator *ControllerPodActivator, agentToPod map[string]string) {
	for agentType, podID := range agentToPod {
		activator.RegisterMapping(agentType, podID)
	}
}

// DefaultPodPolicies is a convenience that combines PodActivationPolicies
// with a timer-based derivation. It derives idle thresholds from defaults.
func DefaultPodPolicies() ([]*ActivationPolicy, []*pod.PodPolicy, map[string]string) {
	base := DefaultPolicyDefaults()
	_ = base
	// This function requires agent descriptors. The descriptors are obtained
	// from the agent registry at session startup. This placeholder exists
	// for documentation — callers should use PodActivationPolicies directly.
	return nil, nil, nil
}

// PodPolicyScanPeriod returns the idle scan period derived from the shortest
// non-daemon idle threshold across all pod policies.
func PodPolicyScanPeriod(podPolicies []*pod.PodPolicy) time.Duration {
	var shortest time.Duration
	for _, pp := range podPolicies {
		if pp.IsDaemon {
			continue
		}
		for _, d := range []time.Duration{pp.IdleToWarm, pp.IdleToCool, pp.IdleToCold} {
			if d > 0 && (shortest == 0 || d < shortest) {
				shortest = d
			}
		}
	}
	if shortest <= 0 {
		return 5 * time.Second
	}
	derived := shortest / 6
	if derived < time.Second {
		return time.Second
	}
	return derived
}
