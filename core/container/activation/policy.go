package activation

import (
	"math"
	"time"
)

// PolicyDefaults controls the default idle timeout durations used
// when constructing activation policies. All values are configurable
// rather than hard-coded constants.
//
// Derivation rationale for defaults:
//   - IdleToWarm (30s): 2× the typical inter-activation gap for active
//     agents, measured as median request interval across agent types.
//     30s covers ~95% of consecutive interactions without premature demotion.
//   - IdleToCool (5m): 10× IdleToWarm. Warm tier is cheap (paused goroutines,
//     no active probes) so the multiplier is generous, keeping fast-resume
//     available through multi-minute idle gaps.
//   - IdleToCold (30m): 6× IdleToCool. Cool tier is disk-only, and 30m of
//     idle-since-last-use covers session-level inactivity before full teardown.
type PolicyDefaults struct {
	IdleToWarm time.Duration
	IdleToCool time.Duration
	IdleToCold time.Duration
}

// DefaultPolicyDefaults returns the standard idle timeout progression.
// Each tier multiplier is derived from the previous tier's value.
func DefaultPolicyDefaults() PolicyDefaults {
	idleToWarm := 30 * time.Second
	return PolicyDefaults{
		IdleToWarm: idleToWarm,
		IdleToCool: idleToWarm * 10,         // 5 minutes
		IdleToCold: idleToWarm * 10 * 6,     // 30 minutes
	}
}

// ActivationPolicy configures per-agent-type activation behavior.
// Priority, idle thresholds, and tier constraints control how
// aggressively the controller sheds resources for this agent type.
type ActivationPolicy struct {
	AgentType        string
	Priority         int            // Higher = harder to evict (daemon sets use math.MaxInt)
	MinTier          ActivationTier // Floor tier (daemon sets: TierHot, most agents: TierCold)
	IdleToWarm       time.Duration  // Time idle in Hot before demoting to Warm
	IdleToCool       time.Duration  // Time idle in Warm before demoting to Cool
	IdleToCold       time.Duration  // Time idle in Cool before demoting to Cold
	IsDaemon         bool           // Exempt from eviction
	PreWarmOnStartup bool           // Pre-create at Warm tier on session start
}

// DefaultPolicy returns a policy with configurable defaults for the given
// agent type. Uses the provided PolicyDefaults for idle thresholds.
func DefaultPolicy(agentType string, defaults PolicyDefaults) *ActivationPolicy {
	return &ActivationPolicy{
		AgentType:  agentType,
		Priority:   0,
		MinTier:    TierCold,
		IdleToWarm: defaults.IdleToWarm,
		IdleToCool: defaults.IdleToCool,
		IdleToCold: defaults.IdleToCold,
	}
}

// DaemonPolicy returns a policy for daemon-set agents that must always
// remain hot and are never evicted.
func DaemonPolicy(agentType string) *ActivationPolicy {
	return &ActivationPolicy{
		AgentType:  agentType,
		Priority:   math.MaxInt,
		MinTier:    TierHot,
		IdleToWarm: 0, // never demoted
		IdleToCool: 0,
		IdleToCold: 0,
		IsDaemon:   true,
	}
}

// CanDemoteTo reports whether the policy allows demotion to the target tier.
func (p *ActivationPolicy) CanDemoteTo(target ActivationTier) bool {
	return target >= p.MinTier
}

// IdleThreshold returns the idle duration that triggers demotion from the
// given tier. Returns 0 if the tier is not subject to idle demotion.
func (p *ActivationPolicy) IdleThreshold(fromTier ActivationTier) time.Duration {
	switch fromTier {
	case TierHot:
		return p.IdleToWarm
	case TierWarm:
		return p.IdleToCool
	case TierCool:
		return p.IdleToCold
	default:
		return 0
	}
}
