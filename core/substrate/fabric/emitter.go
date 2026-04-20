// Package fabric provides the substrate's interface to Sylk's Activity
// Fabric.
//
// Substrate operations (provisioning, pinning, attaching, supply-chain
// violations, eviction pressure) emit fabric activities so other
// agents see substrate evolution in their ambient context envelopes.
// The substrate itself does not depend on any specific fabric
// implementation; it talks to the Emitter interface, and the wider
// system installs a concrete Emitter that publishes to the project's
// canonical activity store (per docs/FABRIC.md).
//
// Phase 9 wires the production Emitter that bridges into core/activity.
// Until then, substrate code uses NoOpEmitter (which discards activities)
// or CapturingEmitter (which captures them in memory for tests).
package fabric

import (
	"sync"
	"time"
)

// Activity is one substrate event suitable for emission to the wider
// Sylk activity fabric.
type Activity struct {
	// Kind names what happened.
	Kind ActivityKind

	// Scope is the activity's path-prefix scope, e.g.
	// "tooling/python/pytest" or "pipeline/pipe-7". Used by the
	// activity fabric's lenses to route the event to interested
	// agents' ambient context envelopes.
	Scope string

	// Timestamp is when the event happened. Set by Emit if zero.
	Timestamp time.Time

	// Payload carries event-specific fields. Convention: a map[string]any
	// with stable keys so consumers can pattern-match without type
	// assertions on private types.
	Payload any
}

// ActivityKind enumerates substrate-emitted activity kinds.
//
// The string values are stable wire names; consumers (lens code,
// agent prompts, observability dashboards) reference them by string,
// so renames break compatibility.
type ActivityKind string

const (
	// ToolPinned: first-time pin of (ecosystem, name, version) in the session.
	ToolPinned ActivityKind = "tool_pinned"

	// ToolUpgraded: version pin updated for a coordinate.
	ToolUpgraded ActivityKind = "tool_upgraded"

	// ToolWitnessAdded: existing pin gained a new witness pipeline.
	ToolWitnessAdded ActivityKind = "tool_witness_added"

	// ToolAttached: manifest attached to a pipeline view.
	ToolAttached ActivityKind = "tool_attached"

	// ToolDetached: manifest detached from a pipeline view (or pin removed
	// because last witness expired).
	ToolDetached ActivityKind = "tool_detached"

	// ProvisioningStarted: recipe runtime began work on a recipe.
	ProvisioningStarted ActivityKind = "provisioning_started"

	// ProvisioningCompleted: recipe runtime registered a manifest.
	ProvisioningCompleted ActivityKind = "provisioning_completed"

	// ProvisioningFailed: recipe runtime failed.
	ProvisioningFailed ActivityKind = "provisioning_failed"

	// BuildStarted: sandbox build began.
	BuildStarted ActivityKind = "build_started"

	// BuildCompleted: sandbox build succeeded.
	BuildCompleted ActivityKind = "build_completed"

	// BuildFailed: sandbox build failed.
	BuildFailed ActivityKind = "build_failed"

	// SupplyChainViolation: hash mismatch / attestation failure.
	SupplyChainViolation ActivityKind = "supply_chain_violation"

	// DiskFallbackUsed: substrate fell back to host disk for a tool.
	DiskFallbackUsed ActivityKind = "disk_fallback_used"

	// SubstrateEvictionPressure: tier cap exceeded.
	SubstrateEvictionPressure ActivityKind = "substrate_eviction_pressure"
)

// Emitter publishes substrate activities to the wider fabric.
type Emitter interface {
	Emit(activity Activity)
}

// NoOpEmitter discards activities. Suitable as the default when no
// real fabric is wired (tests, early-bring-up environments).
type NoOpEmitter struct{}

// Emit discards the activity.
func (NoOpEmitter) Emit(_ Activity) {}

// CapturingEmitter records emitted activities in memory. Intended for
// tests that need to assert which activities were emitted in what order.
//
// Safe for concurrent use.
type CapturingEmitter struct {
	mu         sync.Mutex
	activities []Activity
}

// NewCapturingEmitter returns a fresh CapturingEmitter.
func NewCapturingEmitter() *CapturingEmitter {
	return &CapturingEmitter{}
}

// Emit captures the activity, stamping Timestamp if zero.
func (c *CapturingEmitter) Emit(activity Activity) {
	if activity.Timestamp.IsZero() {
		activity.Timestamp = time.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.activities = append(c.activities, activity)
}

// Activities returns a snapshot of captured activities in arrival order.
func (c *CapturingEmitter) Activities() []Activity {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Activity, len(c.activities))
	copy(out, c.activities)
	return out
}

// Reset clears the captured activities. Useful between assertions.
func (c *CapturingEmitter) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.activities = nil
}

// Len returns the number of captured activities.
func (c *CapturingEmitter) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.activities)
}

// CountByKind returns how many activities of the given kind have been
// captured.
func (c *CapturingEmitter) CountByKind(kind ActivityKind) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for i := range c.activities {
		if c.activities[i].Kind == kind {
			n++
		}
	}
	return n
}
