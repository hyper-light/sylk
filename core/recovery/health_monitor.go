package recovery

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type AgentEnumerator interface {
	ActiveAgentIDs() []string
}

type HealthMonitor struct {
	healthScorer  *HealthScorer
	deadlockDet   *DeadlockDetector
	orchestrator  *RecoveryOrchestrator
	deadlockRecov *DeadlockRecovery
	agents        AgentEnumerator
	config        RecoveryConfig
	logger        *slog.Logger

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
}

func NewHealthMonitor(
	healthScorer *HealthScorer,
	deadlockDet *DeadlockDetector,
	orchestrator *RecoveryOrchestrator,
	deadlockRecov *DeadlockRecovery,
	agents AgentEnumerator,
	config RecoveryConfig,
	logger *slog.Logger,
) *HealthMonitor {
	return &HealthMonitor{
		healthScorer:  healthScorer,
		deadlockDet:   deadlockDet,
		orchestrator:  orchestrator,
		deadlockRecov: deadlockRecov,
		agents:        agents,
		config:        config,
		logger:        logger,
	}
}

func (h *HealthMonitor) Start(ctx context.Context) {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return
	}
	h.running = true
	monitorCtx, cancel := context.WithCancel(ctx)
	h.cancel = cancel
	h.mu.Unlock()

	go h.monitorLoop(monitorCtx)
}

func (h *HealthMonitor) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.running {
		return
	}
	h.running = false
	if h.cancel != nil {
		h.cancel()
	}
}

func (h *HealthMonitor) monitorLoop(ctx context.Context) {
	ticker := time.NewTicker(h.config.MonitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.checkAllAgents()
			h.checkDeadlocks()
		}
	}
}

func (h *HealthMonitor) checkAllAgents() {
	agentIDs := h.agents.ActiveAgentIDs()

	for _, agentID := range agentIDs {
		assessment := h.healthScorer.Assess(agentID)
		if assessment.Status >= StatusStuck {
			h.orchestrator.HandleStuckAgent(assessment)
		}
	}
}

func (h *HealthMonitor) checkDeadlocks() {
	results := h.deadlockDet.Check()

	for _, result := range results {
		if result.Detected {
			h.deadlockRecov.HandleDeadlock(result)
		}
	}
}

func (h *HealthMonitor) IsRunning() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.running
}

func (h *HealthMonitor) AssessAgent(agentID string) HealthAssessment {
	return h.healthScorer.Assess(agentID)
}
