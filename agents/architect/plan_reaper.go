package architect

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PlanReaper periodically sweeps active plans, evicting terminal plans past
// retention and transitioning expired-lease plans to Failed. The goroutine
// is tracked via context cancellation — no untracked goroutines.
type PlanReaper struct {
	architect    *Architect
	leaseManager *PlanLeaseManager
	logger       *slog.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}
}

// NewPlanReaper creates a reaper bound to the architect's plan store.
func NewPlanReaper(a *Architect, lm *PlanLeaseManager, logger *slog.Logger) *PlanReaper {
	ctx, cancel := context.WithCancel(context.Background())
	return &PlanReaper{
		architect:    a,
		leaseManager: lm,
		logger:       logger,
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
	}
}

// Start launches the sweep goroutine. Tracked via ctx; waitable via Stop.
func (r *PlanReaper) Start() {
	go r.loop()
}

// Stop cancels the context and waits for the goroutine to exit.
func (r *PlanReaper) Stop() {
	r.cancel()
	<-r.done
}

func (r *PlanReaper) loop() {
	defer close(r.done)
	ticker := time.NewTicker(r.leaseManager.ReaperInterval())
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.sweep()
		}
	}
}

func (r *PlanReaper) sweep() {
	plans := r.architect.snapshotActivePlans()
	for _, plan := range plans {
		action := r.classifyAction(plan)
		r.executeAction(action, plan)
	}
}

// reaperAction classifies what the reaper should do with a plan.
type reaperAction int

const (
	reaperActionNone          reaperAction = iota
	reaperActionEvictTerminal              // Remove terminal plan past retention
	reaperActionExpireReady                // Transition expired Ready → Failed
	reaperActionExpireExecuting            // Transition expired Executing → Failed
)

func (r *PlanReaper) classifyAction(plan *DesignPlan) reaperAction {
	state := plan.SM().State()
	switch {
	case isTerminalPlanStatus(state) && r.leaseManager.IsTerminalRetentionExpired(plan):
		return reaperActionEvictTerminal
	case state == PlanStatusReady && r.leaseManager.IsLeaseExpired(plan):
		return reaperActionExpireReady
	case state == PlanStatusExecuting && r.leaseManager.IsLeaseExpired(plan):
		return reaperActionExpireExecuting
	default:
		return reaperActionNone
	}
}

func (r *PlanReaper) executeAction(action reaperAction, plan *DesignPlan) {
	switch action {
	case reaperActionEvictTerminal:
		r.evictPlan(plan)
	case reaperActionExpireReady:
		r.expirePlan(plan, "lease expired: ready plan reaped")
	case reaperActionExpireExecuting:
		r.expirePlan(plan, "lease expired: executing plan reaped (crash recovery)")
	case reaperActionNone:
		// no-op
	}
}

func (r *PlanReaper) evictPlan(plan *DesignPlan) {
	r.architect.removeActivePlan(plan.ID)
	r.removeDiskFile(plan.ID, plan.SessionID)
	r.logger.Info("reaper: evicted terminal plan",
		"plan_id", plan.ID,
		"status", plan.SM().State().String())
}

func (r *PlanReaper) expirePlan(plan *DesignPlan, reason string) {
	plan.Error = reason
	if err := plan.SM().TransitionToFailed(plan, fmt.Errorf("%s", reason)); err != nil {
		r.logger.Warn("reaper: transition to Failed failed",
			"plan_id", plan.ID, "error", err)
		return
	}
	plan.Status = plan.SM().State()
	plan.Epoch = plan.SM().Epoch()
	plan.UpdatedAt = time.Now()
	_ = r.architect.persistPlanState(plan)
	r.logger.Info("reaper: expired plan",
		"plan_id", plan.ID, "reason", reason)
}

func (r *PlanReaper) removeDiskFile(planID, sessionID string) {
	dir := r.architect.planStoreDir(sessionID)
	path := filepath.Join(dir, strings.TrimSpace(planID)+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		r.logger.Warn("reaper: failed to remove disk file",
			"path", path, "error", err)
	}
}

func isTerminalPlanStatus(s PlanStatus) bool {
	return s == PlanStatusCompleted || s == PlanStatusFailed
}

// snapshotActivePlans returns a shallow copy of the active plans slice for
// safe iteration outside the lock.
func (a *Architect) snapshotActivePlans() []*DesignPlan {
	a.activePlansMu.RLock()
	defer a.activePlansMu.RUnlock()
	plans := make([]*DesignPlan, 0, len(a.activePlans))
	for _, p := range a.activePlans {
		plans = append(plans, p)
	}
	return plans
}

// removeActivePlan deletes a plan from the in-memory active set.
func (a *Architect) removeActivePlan(planID string) {
	a.activePlansMu.Lock()
	defer a.activePlansMu.Unlock()
	delete(a.activePlans, planID)
}

