package pod

import (
	"time"

	"github.com/adalundhe/sylk/core/container"
)

// PodPolicy configures a managed pod's activation behavior, membership,
// and volume requirements.
type PodPolicy struct {
	PodID            PodID
	PodType          PodType
	MemberTypes      []string              // agent types in this pod
	Priority         int                   // higher = harder to evict
	MinTier          ActivationTier
	IdleToWarm       time.Duration
	IdleToCool       time.Duration
	IdleToCold       time.Duration
	IsDaemon         bool
	PreWarmOnStartup bool
	VolumeSpecs      []container.VolumeSpec
}

// ContainsMember returns true if agentType is in the pod's member list.
func (p *PodPolicy) ContainsMember(agentType string) bool {
	for _, m := range p.MemberTypes {
		if m == agentType {
			return true
		}
	}
	return false
}

// MemberCount returns the number of agent types in the pod.
func (p *PodPolicy) MemberCount() int {
	return len(p.MemberTypes)
}
