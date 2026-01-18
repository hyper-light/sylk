package concurrency

import (
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/recovery"
)

// SignalEmitterProvider abstracts the recovery.SignalEmitter for testing and decoupling.
type SignalEmitterProvider interface {
	EmitToolCompleted(agentID, sessionID, toolName, target string)
	EmitLLMResponse(agentID, sessionID string)
	EmitFileModified(agentID, sessionID, filePath string)
	EmitStateTransition(agentID, sessionID, fromState, toState string)
	EmitAgentRequest(fromAgentID, sessionID, toAgentID string)
}

// HealthScorerProvider abstracts the recovery.HealthScorer for testing and decoupling.
type HealthScorerProvider interface {
	Assess(agentID string) recovery.HealthAssessment
}

// HealthMonitorProvider abstracts the recovery.HealthMonitor for testing and decoupling.
type HealthMonitorProvider interface {
	AssessAgent(agentID string) recovery.HealthAssessment
	IsRunning() bool
}

// SupervisorHealthIntegration extends AgentSupervisor with health monitoring capabilities.
type SupervisorHealthIntegration struct {
	supervisor     *AgentSupervisor
	signalEmitter  SignalEmitterProvider
	healthScorer   HealthScorerProvider
	healthMonitor  HealthMonitorProvider
	mu             sync.RWMutex
	lastAssessment *recovery.HealthAssessment
}

// NewSupervisorHealthIntegration creates a new health integration for a supervisor.
func NewSupervisorHealthIntegration(supervisor *AgentSupervisor) *SupervisorHealthIntegration {
	return &SupervisorHealthIntegration{
		supervisor: supervisor,
	}
}

// SetSignalEmitter sets the signal emitter for health signal emission.
func (h *SupervisorHealthIntegration) SetSignalEmitter(emitter SignalEmitterProvider) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.signalEmitter = emitter
}

// SetHealthScorer sets the health scorer for health assessments.
func (h *SupervisorHealthIntegration) SetHealthScorer(scorer HealthScorerProvider) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.healthScorer = scorer
}

// SetHealthMonitor sets the health monitor for agent health monitoring.
func (h *SupervisorHealthIntegration) SetHealthMonitor(monitor HealthMonitorProvider) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.healthMonitor = monitor
}

// SignalEmitter returns the current signal emitter.
func (h *SupervisorHealthIntegration) SignalEmitter() SignalEmitterProvider {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.signalEmitter
}

// HealthScorer returns the current health scorer.
func (h *SupervisorHealthIntegration) HealthScorer() HealthScorerProvider {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.healthScorer
}

// HealthMonitor returns the current health monitor.
func (h *SupervisorHealthIntegration) HealthMonitor() HealthMonitorProvider {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.healthMonitor
}

// EmitOperationCompleted emits a signal when an operation completes.
func (h *SupervisorHealthIntegration) EmitOperationCompleted(op *Operation, sessionID string) {
	emitter := h.SignalEmitter()
	if emitter == nil {
		return
	}

	h.emitSignalForOperationType(emitter, op, sessionID)
}

// emitSignalForOperationType emits the appropriate signal based on operation type.
func (h *SupervisorHealthIntegration) emitSignalForOperationType(
	emitter SignalEmitterProvider,
	op *Operation,
	sessionID string,
) {
	switch op.Type {
	case OpTypeLLMCall:
		emitter.EmitLLMResponse(op.AgentID, sessionID)
	case OpTypeToolExecution:
		emitter.EmitToolCompleted(op.AgentID, sessionID, op.Description, "")
	case OpTypeFileIO:
		emitter.EmitFileModified(op.AgentID, sessionID, op.Description)
	case OpTypeNetworkIO:
		emitter.EmitToolCompleted(op.AgentID, sessionID, "network", op.Description)
	}
}

// EmitStateTransition emits a state transition signal.
func (h *SupervisorHealthIntegration) EmitStateTransition(
	sessionID string,
	fromState, toState SupervisorState,
) {
	emitter := h.SignalEmitter()
	if emitter == nil {
		return
	}

	emitter.EmitStateTransition(
		h.supervisor.agentID,
		sessionID,
		stateToString(fromState),
		stateToString(toState),
	)
}

// stateToString converts SupervisorState to string representation.
func stateToString(state SupervisorState) string {
	switch state {
	case SupervisorStateRunning:
		return "running"
	case SupervisorStatePausing:
		return "pausing"
	case SupervisorStatePaused:
		return "paused"
	case SupervisorStateStopping:
		return "stopping"
	case SupervisorStateStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// AssessHealth performs a health assessment of the supervised agent.
func (h *SupervisorHealthIntegration) AssessHealth() recovery.HealthAssessment {
	scorer := h.HealthScorer()
	if scorer == nil {
		return h.defaultHealthyAssessment()
	}

	assessment := scorer.Assess(h.supervisor.agentID)
	h.storeAssessment(&assessment)
	return assessment
}

// storeAssessment stores the latest assessment for later retrieval.
func (h *SupervisorHealthIntegration) storeAssessment(assessment *recovery.HealthAssessment) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastAssessment = assessment
}

// defaultHealthyAssessment returns a default healthy assessment when no scorer is available.
func (h *SupervisorHealthIntegration) defaultHealthyAssessment() recovery.HealthAssessment {
	return recovery.HealthAssessment{
		AgentID:      h.supervisor.agentID,
		OverallScore: 1.0,
		Status:       recovery.StatusHealthy,
		AssessedAt:   time.Now(),
	}
}

// LastAssessment returns the most recent health assessment.
func (h *SupervisorHealthIntegration) LastAssessment() *recovery.HealthAssessment {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.lastAssessment
}

// MonitorHealth uses the health monitor to assess the agent if available.
func (h *SupervisorHealthIntegration) MonitorHealth() recovery.HealthAssessment {
	monitor := h.HealthMonitor()
	if monitor == nil {
		return h.AssessHealth()
	}

	assessment := monitor.AssessAgent(h.supervisor.agentID)
	h.storeAssessment(&assessment)
	return assessment
}

// IsHealthy returns true if the agent's health status is healthy.
func (h *SupervisorHealthIntegration) IsHealthy() bool {
	assessment := h.AssessHealth()
	return assessment.Status == recovery.StatusHealthy
}

// IsStuck returns true if the agent's health status indicates it is stuck.
func (h *SupervisorHealthIntegration) IsStuck() bool {
	assessment := h.AssessHealth()
	return assessment.Status >= recovery.StatusStuck
}

// IsCritical returns true if the agent's health status is critical.
func (h *SupervisorHealthIntegration) IsCritical() bool {
	assessment := h.AssessHealth()
	return assessment.Status == recovery.StatusCritical
}

// EmitAgentRequest emits a signal when this agent requests another agent.
func (h *SupervisorHealthIntegration) EmitAgentRequest(sessionID, targetAgentID string) {
	emitter := h.SignalEmitter()
	if emitter == nil {
		return
	}

	emitter.EmitAgentRequest(h.supervisor.agentID, sessionID, targetAgentID)
}
