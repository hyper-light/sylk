package activation

import (
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/container"
)

// ActivationTier represents the resource/latency tradeoff for an agent.
//
//	Cold: No state exists, full creation needed.
//	Cool: State serialized on disk, moderate resume.
//	Warm: Container paused in memory, fast resume.
//	Hot:  Container running and ready.
type ActivationTier int32

const (
	TierCold ActivationTier = iota
	TierCool
	TierWarm
	TierHot
)

var tierNames = map[ActivationTier]string{
	TierCold: "cold",
	TierCool: "cool",
	TierWarm: "warm",
	TierHot:  "hot",
}

func (t ActivationTier) String() string {
	if name, ok := tierNames[t]; ok {
		return name
	}
	return "unknown"
}

// ActivationEntry tracks the current activation state of a single agent type.
// All mutable fields use atomics for lock-free hot-path reads.
type ActivationEntry struct {
	AgentType       string
	Tier            atomic.Int32                        // stores ActivationTier
	Container       atomic.Pointer[container.Container] // non-nil when Warm or Hot
	Spec            container.ContainerSpec
	LastActive      atomic.Int64 // UnixNano timestamp
	ActivationCount atomic.Int64
	Policy          *ActivationPolicy
}

// LoadTier returns the current ActivationTier.
func (e *ActivationEntry) LoadTier() ActivationTier {
	return ActivationTier(e.Tier.Load())
}

// StoreTier sets the ActivationTier atomically.
func (e *ActivationEntry) StoreTier(t ActivationTier) {
	e.Tier.Store(int32(t))
}

// TouchActivity updates the last-active timestamp to now.
func (e *ActivationEntry) TouchActivity() {
	e.LastActive.Store(time.Now().UnixNano())
}

// IdleDuration returns how long the entry has been idle.
func (e *ActivationEntry) IdleDuration() time.Duration {
	last := e.LastActive.Load()
	if last == 0 {
		return 0
	}
	return time.Since(time.Unix(0, last))
}
