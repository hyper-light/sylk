package architect

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
)

// PlanHeartbeatPayload is the wire type for plan.heartbeat messages.
type PlanHeartbeatPayload struct {
	PlanID string `json:"plan_id"`
	Epoch  uint64 `json:"epoch"`
}

// Lease multipliers — derived from LLM request timeout and ready plan max age.
const (
	executingLeaseMultiplier    = 3 // lease = 3x smallest work unit (LLM request timeout)
	terminalRetentionMultiplier = 2 // terminal plans retained for 2x readyPlanMaxAge
	reaperIntervalDivisor       = 3 // sweep every min(executing, ready) / 3
)

// PlanLeaseManager manages lease grants, renewals, and expiry checks for
// active plans. All timeouts are derived from two seed values — no magic numbers.
type PlanLeaseManager struct {
	executingLeaseTimeout time.Duration // LLMRequestTimeout * executingLeaseMultiplier
	readyLeaseTimeout     time.Duration // readyPlanMaxAge
	terminalRetention     time.Duration // readyPlanMaxAge * terminalRetentionMultiplier
	reaperInterval        time.Duration // min(executing, ready) / reaperIntervalDivisor
}

// NewPlanLeaseManager derives all timeouts from two seed durations.
func NewPlanLeaseManager(llmRequestTimeout, readyMaxAge time.Duration) *PlanLeaseManager {
	executing := llmRequestTimeout * executingLeaseMultiplier
	ready := readyMaxAge
	terminal := readyMaxAge * terminalRetentionMultiplier

	shorter := executing
	if ready < shorter {
		shorter = ready
	}
	reaper := shorter / reaperIntervalDivisor

	return &PlanLeaseManager{
		executingLeaseTimeout: executing,
		readyLeaseTimeout:     ready,
		terminalRetention:     terminal,
		reaperInterval:        reaper,
	}
}

// ReaperInterval returns the sweep interval for the plan reaper goroutine.
func (m *PlanLeaseManager) ReaperInterval() time.Duration {
	return m.reaperInterval
}

// GrantReadyLease stamps the plan with a lease that expires after readyLeaseTimeout.
func (m *PlanLeaseManager) GrantReadyLease(plan *DesignPlan) {
	if plan == nil {
		return
	}
	plan.LeaseExpiry = time.Now().Add(m.readyLeaseTimeout)
	plan.LeaseHolder = ""
}

// GrantExecutingLease stamps the plan with a lease that expires after
// executingLeaseTimeout, assigned to the given holder (e.g. "orchestrator").
func (m *PlanLeaseManager) GrantExecutingLease(plan *DesignPlan, holder string) {
	if plan == nil {
		return
	}
	plan.LeaseExpiry = time.Now().Add(m.executingLeaseTimeout)
	plan.LeaseHolder = holder
}

// RenewLease extends the lease by the executing lease timeout from now.
func (m *PlanLeaseManager) RenewLease(plan *DesignPlan) {
	if plan == nil {
		return
	}
	plan.LeaseExpiry = time.Now().Add(m.executingLeaseTimeout)
}

// IsLeaseExpired returns true when the plan's lease has passed.
func (m *PlanLeaseManager) IsLeaseExpired(plan *DesignPlan) bool {
	if plan == nil {
		return true
	}
	return time.Now().After(plan.LeaseExpiry)
}

// IsTerminalRetentionExpired returns true when a terminal (Completed/Failed)
// plan has been in that state longer than the retention window.
func (m *PlanLeaseManager) IsTerminalRetentionExpired(plan *DesignPlan) bool {
	if plan == nil {
		return true
	}
	return time.Since(plan.UpdatedAt) > m.terminalRetention
}

// handlePlanHeartbeat validates planID+epoch, then renews the lease.
// Stale-epoch heartbeats are silently ignored.
func (a *Architect) handlePlanHeartbeat(msg *guide.Message) error {
	if msg == nil {
		return nil
	}
	payload, ok := decodePlanHeartbeat(msg)
	if !ok {
		return nil
	}
	plan := a.planStore.Get(payload.PlanID)
	if plan == nil {
		return nil
	}
	if plan.SM().Epoch() != payload.Epoch {
		return nil
	}
	a.planStore.LeaseManager().RenewLease(plan)
	return nil
}

func decodePlanHeartbeat(msg *guide.Message) (PlanHeartbeatPayload, bool) {
	data, ok := msg.Payload.(string)
	if !ok {
		return PlanHeartbeatPayload{}, false
	}
	var payload PlanHeartbeatPayload
	if json.Unmarshal([]byte(data), &payload) != nil {
		return PlanHeartbeatPayload{}, false
	}
	if strings.TrimSpace(payload.PlanID) == "" {
		return PlanHeartbeatPayload{}, false
	}
	return payload, true
}

