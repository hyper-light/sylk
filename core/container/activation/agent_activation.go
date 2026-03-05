package activation

import (
	"time"

	"github.com/adalundhe/sylk/core/handoff"
)

// daemonAgentTypes lists agent types that must remain TierHot at all times.
// Guide and orchestrator are session-critical coordinators that handle
// every user interaction and agent dispatch respectively.
var daemonAgentTypes = map[string]bool{
	"guide":        true,
	"orchestrator": true,
	"guardian":     true,
}

// categoryCostScale maps agent category to an idle-threshold scale factor.
// Knowledge agents are expensive to rebuild (large context windows), so
// their idle thresholds are doubled to keep them resident longer. Pipeline
// agents use baseline scale. Standalone agents use baseline scale.
var categoryCostScale = map[handoff.AgentCategory]int{
	handoff.CategoryKnowledge:  2,
	handoff.CategoryStandalone: 1,
	handoff.CategoryPipeline:   1,
}

// categoryDivisor maps agent category to an idle-threshold divisor.
// Used together with categoryCostScale to avoid fractional arithmetic:
// effective threshold = base * scale / divisor.
// Pipeline agents are cheap to recreate, so their thresholds are halved.
var categoryDivisor = map[handoff.AgentCategory]int{
	handoff.CategoryKnowledge:  1,
	handoff.CategoryStandalone: 1,
	handoff.CategoryPipeline:   2,
}

// AgentActivationPolicies generates per-agent-type activation policies
// from agent descriptors. Policy parameters are derived from each
// descriptor's category and usage patterns — no magic numbers.
func AgentActivationPolicies(descriptors []handoff.AgentDescriptor) []*ActivationPolicy {
	policies := make([]*ActivationPolicy, len(descriptors))
	for i, desc := range descriptors {
		policies[i] = policyForDescriptor(desc)
	}
	return policies
}

// preWarmAgentTypes lists agent types that should be activated at startup.
// Knowledge agents provide reduced-precision queries when cold-started;
// pre-warming avoids that latency on first use.
var preWarmAgentTypes = map[string]bool{
	"architect":   true,
	"librarian":   true,
	"archivalist": true,
	"academic":    true,
}

// policyForDescriptor builds an activation policy for a single agent descriptor.
// Daemon agents get DaemonPolicy; knowledge agents and the architect get
// PreWarmOnStartup; all others get DefaultPolicy with category-scaled idle
// thresholds.
func policyForDescriptor(desc handoff.AgentDescriptor) *ActivationPolicy {
	if daemonAgentTypes[desc.AgentType] {
		p := DaemonPolicy(desc.AgentType)
		p.PreWarmOnStartup = true
		return p
	}

	defaults := scaledDefaults(desc.Category)
	policy := DefaultPolicy(desc.AgentType, defaults)

	if preWarmAgentTypes[desc.AgentType] {
		policy.PreWarmOnStartup = true
	}

	return policy
}

// scaledDefaults returns PolicyDefaults with idle thresholds adjusted by the
// category's cost scale factor. Knowledge agents (scale 2, divisor 1) get 2×
// longer thresholds. Pipeline agents (scale 1, divisor 2) get thresholds halved.
func scaledDefaults(category handoff.AgentCategory) PolicyDefaults {
	base := DefaultPolicyDefaults()
	scale := categoryCostScale[category]
	divisor := categoryDivisor[category]

	return PolicyDefaults{
		IdleToWarm: base.IdleToWarm * time.Duration(scale) / time.Duration(divisor),
		IdleToCool: base.IdleToCool * time.Duration(scale) / time.Duration(divisor),
		IdleToCold: base.IdleToCold * time.Duration(scale) / time.Duration(divisor),
	}
}
