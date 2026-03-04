package activation

import (
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/container/pod"
)

// ActivationTier is the canonical tier type, defined in the pod package
// to break the activation↔pod import cycle. Re-exported here for
// backward compatibility with all existing activation callers.
type ActivationTier = pod.ActivationTier

// Tier constants re-exported from pod for backward compatibility.
const (
	TierCold = pod.TierCold
	TierCool = pod.TierCool
	TierWarm = pod.TierWarm
	TierHot  = pod.TierHot
)

// ActivationEntry tracks the current activation state of a single agent type.
// All mutable fields use atomics for lock-free hot-path reads.
type ActivationEntry struct {
	AgentType       string
	Tier            atomic.Int32                        // stores ActivationTier
	Container       atomic.Pointer[container.Container] // non-nil when Warm or Hot
	Spec            container.ContainerSpec
	LastActive      atomic.Int64 // UnixNano timestamp
	ActivationCount atomic.Int64
	ActiveRequests  atomic.Int64 // in-flight requests holding demotion guard
	Policy          *ActivationPolicy
}

// HasActiveRequests returns true if any request guard is currently held.
func (e *ActivationEntry) HasActiveRequests() bool {
	return e.ActiveRequests.Load() > 0
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
