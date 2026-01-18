package recovery

import (
	"log/slog"
	"math"
	"time"
)

type DeadlockRecovery struct {
	orchestrator *RecoveryOrchestrator
	collector    *ProgressCollector
	resourceMgr  ResourceReleaser
	notifier     RecoveryNotifier
	logger       *slog.Logger
}

func NewDeadlockRecovery(
	orchestrator *RecoveryOrchestrator,
	collector *ProgressCollector,
	resourceMgr ResourceReleaser,
	notifier RecoveryNotifier,
	logger *slog.Logger,
) *DeadlockRecovery {
	return &DeadlockRecovery{
		orchestrator: orchestrator,
		collector:    collector,
		resourceMgr:  resourceMgr,
		notifier:     notifier,
		logger:       logger,
	}
}

func (d *DeadlockRecovery) HandleDeadlock(result DeadlockResult) {
	if !result.Detected {
		return
	}

	switch result.Type {
	case DeadlockDeadHolder:
		d.handleDeadHolderDeadlock(result)
	case DeadlockCircular:
		d.handleCircularDeadlock(result)
	}
}

func (d *DeadlockRecovery) handleDeadHolderDeadlock(result DeadlockResult) {
	d.logger.Info("deadlock detected: agent waiting on dead holder",
		"dead_agent", result.DeadHolder,
		"waiting_agents", result.WaitingAgents,
		"resource_type", result.ResourceType,
		"resource_id", result.ResourceID,
	)

	released := d.resourceMgr.ForceReleaseByAgent(result.DeadHolder)
	d.recordReleasedResources(result.DeadHolder, released)
	d.forceKillDeadAgent(result)

	d.logger.Info("released dead agent resources",
		"dead_agent", result.DeadHolder,
		"released", released,
	)
}

func (d *DeadlockRecovery) recordReleasedResources(agentID string, released []string) {
	if state, ok := d.orchestrator.GetState(agentID); ok {
		state.Lock()
		state.AddReleasedResources(released)
		state.Unlock()
	}
}

func (d *DeadlockRecovery) forceKillDeadAgent(result DeadlockResult) {
	stuckTime := time.Now().Add(-time.Hour)
	assessment := HealthAssessment{
		AgentID:    result.DeadHolder,
		Status:     StatusCritical,
		StuckSince: &stuckTime,
	}
	d.orchestrator.HandleStuckAgent(assessment)
}

func (d *DeadlockRecovery) handleCircularDeadlock(result DeadlockResult) {
	victim := d.selectDeadlockVictim(result.Cycle)

	d.logger.Warn("circular deadlock detected: breaking cycle",
		"cycle", result.Cycle,
		"victim", victim,
	)

	released := d.resourceMgr.ForceReleaseByAgent(victim)
	d.recordReleasedResources(victim, released)
	d.escalateCircularDeadlock(victim)
}

func (d *DeadlockRecovery) selectDeadlockVictim(cycle []string) string {
	if len(cycle) == 0 {
		return ""
	}

	var victim string
	var minProgress int64 = math.MaxInt64

	for _, agentID := range cycle {
		progress := d.collector.SignalCount(agentID)
		if progress < minProgress {
			minProgress = progress
			victim = agentID
		}
	}
	return victim
}

func (d *DeadlockRecovery) escalateCircularDeadlock(victim string) {
	assessment := HealthAssessment{
		AgentID: victim,
		Status:  StatusDeadlocked,
	}

	if err := d.notifier.EscalateToUser("", victim, assessment); err != nil {
		d.logger.Warn("failed to escalate circular deadlock", "agent", victim, "error", err)
	}
}
