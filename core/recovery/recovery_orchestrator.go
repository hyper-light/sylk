package recovery

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type AgentTerminator interface {
	TerminateAgent(agentID string, reason string) error
}

type ResourceReleaser interface {
	ForceReleaseByAgent(agentID string) []string
}

type RecoveryOrchestrator struct {
	healthScorer  *HealthScorer
	deadlockDet   *DeadlockDetector
	resourceMgr   ResourceReleaser
	terminator    AgentTerminator
	notifier      RecoveryNotifier
	logger        *slog.Logger
	recoveryState sync.Map
	config        RecoveryConfig
}

func NewRecoveryOrchestrator(
	healthScorer *HealthScorer,
	deadlockDet *DeadlockDetector,
	resourceMgr ResourceReleaser,
	terminator AgentTerminator,
	notifier RecoveryNotifier,
	logger *slog.Logger,
	config RecoveryConfig,
) *RecoveryOrchestrator {
	return &RecoveryOrchestrator{
		healthScorer: healthScorer,
		deadlockDet:  deadlockDet,
		resourceMgr:  resourceMgr,
		terminator:   terminator,
		notifier:     notifier,
		logger:       logger,
		config:       config,
	}
}

func (r *RecoveryOrchestrator) HandleStuckAgent(assessment HealthAssessment) {
	state := r.getOrCreateState(assessment.AgentID, assessment.SessionID)
	state.Lock()
	defer state.Unlock()

	if assessment.Status == StatusHealthy {
		r.handleRecoveredAgent(state)
		return
	}

	if assessment.StuckSince == nil {
		return
	}

	stuckDuration := time.Since(*assessment.StuckSince)
	r.processStuckDuration(state, assessment, stuckDuration)
}

func (r *RecoveryOrchestrator) handleRecoveredAgent(state *RecoveryState) {
	if len(state.ResourcesReleased) > 0 {
		r.notifier.NotifyReacquireResources(state.AgentID, state.ResourcesReleased)
		r.logger.Info("agent recovered, notified to re-acquire resources",
			"agent", state.AgentID,
			"resources", state.ResourcesReleased,
		)
	}
	r.clearState(state.AgentID)
}

func (r *RecoveryOrchestrator) processStuckDuration(state *RecoveryState, assessment HealthAssessment, duration time.Duration) {
	switch {
	case duration < r.config.SoftInterventionDelay:
		return
	case duration < r.config.UserEscalationDelay:
		r.trySoftIntervention(state, assessment)
	case duration < r.config.ForceKillDelay:
		r.handleUserEscalationPhase(state, assessment)
	default:
		r.handleForceKillPhase(state, assessment)
	}
}

func (r *RecoveryOrchestrator) handleUserEscalationPhase(state *RecoveryState, assessment HealthAssessment) {
	if !state.UserEscalated {
		r.escalateToUser(state, assessment)
		return
	}
	if state.UserResponse != nil {
		r.handleUserResponse(state, assessment)
	}
}

func (r *RecoveryOrchestrator) handleForceKillPhase(state *RecoveryState, assessment HealthAssessment) {
	if state.UserResponse == nil || state.UserResponse.Action != UserActionWait {
		r.forceKill(state, assessment)
	}
}

func (r *RecoveryOrchestrator) trySoftIntervention(state *RecoveryState, assessment HealthAssessment) {
	if state.SoftAttempts >= r.config.MaxSoftAttempts {
		return
	}

	prompt := r.buildBreakoutPrompt(assessment)
	if err := r.notifier.InjectBreakoutPrompt(state.AgentID, prompt); err != nil {
		r.logger.Warn("soft intervention failed", "agent", state.AgentID, "error", err)
		return
	}

	state.MarkSoftIntervention()
	r.logger.Info("soft intervention sent",
		"agent", state.AgentID,
		"attempt", state.SoftAttempts,
		"max_attempts", r.config.MaxSoftAttempts,
	)
}

func (r *RecoveryOrchestrator) buildBreakoutPrompt(assessment HealthAssessment) string {
	if assessment.RepetitionConcern {
		return "[SYSTEM] You appear to be repeating similar operations without making progress. " +
			"Please analyze what's blocking you and try a different approach, or explain " +
			"why these repeated operations are necessary."
	}
	return "[SYSTEM] You haven't made observable progress recently. Please provide a status " +
		"update on your current task, explain any blockers, or try an alternative approach."
}

func (r *RecoveryOrchestrator) escalateToUser(state *RecoveryState, assessment HealthAssessment) {
	if err := r.notifier.EscalateToUser(state.SessionID, state.AgentID, assessment); err != nil {
		r.logger.Warn("user escalation failed", "agent", state.AgentID, "error", err)
		return
	}

	state.MarkUserEscalated()
	r.logger.Info("escalated to user", "agent", state.AgentID)
}

func (r *RecoveryOrchestrator) handleUserResponse(state *RecoveryState, assessment HealthAssessment) {
	switch state.UserResponse.Action {
	case UserActionWait:
		r.logger.Info("user chose to wait", "agent", state.AgentID)
	case UserActionKill:
		r.forceKill(state, assessment)
	case UserActionInspect:
		r.logger.Info("user inspecting agent", "agent", state.AgentID)
	}
}

func (r *RecoveryOrchestrator) forceKill(state *RecoveryState, assessment HealthAssessment) {
	state.MarkForceKill()

	released := r.resourceMgr.ForceReleaseByAgent(state.AgentID)
	state.AddReleasedResources(released)

	reason := r.buildForceKillReason(assessment)
	r.notifier.NotifyForceKill(state.SessionID, state.AgentID, reason)

	if err := r.terminator.TerminateAgent(state.AgentID, reason); err != nil {
		r.logger.Error("failed to terminate agent", "agent", state.AgentID, "error", err)
	}

	r.logger.Warn("force killed stuck agent",
		"agent", state.AgentID,
		"released_resources", len(released),
	)
}

func (r *RecoveryOrchestrator) buildForceKillReason(assessment HealthAssessment) string {
	if assessment.StuckSince == nil {
		return "Agent stuck without progress"
	}
	return fmt.Sprintf("Agent stuck for %s without progress",
		time.Since(*assessment.StuckSince).Round(time.Second))
}

func (r *RecoveryOrchestrator) getOrCreateState(agentID, sessionID string) *RecoveryState {
	if existing, ok := r.recoveryState.Load(agentID); ok {
		return existing.(*RecoveryState)
	}

	state := NewRecoveryState(agentID, sessionID)
	actual, _ := r.recoveryState.LoadOrStore(agentID, state)
	return actual.(*RecoveryState)
}

func (r *RecoveryOrchestrator) clearState(agentID string) {
	r.recoveryState.Delete(agentID)
}

func (r *RecoveryOrchestrator) SetUserResponse(agentID string, response UserRecoveryResponse) {
	if state, ok := r.recoveryState.Load(agentID); ok {
		s := state.(*RecoveryState)
		s.Lock()
		s.SetUserResponse(&response)
		s.Unlock()
		r.notifier.OnUserResponse(agentID, response)
	}
}

func (r *RecoveryOrchestrator) GetState(agentID string) (*RecoveryState, bool) {
	if state, ok := r.recoveryState.Load(agentID); ok {
		return state.(*RecoveryState), true
	}
	return nil, false
}
