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

// HealthAssessmentSink is the abstract delivery surface for per-tick
// health assessments leaving the recovery package. REC-02: mirrors
// the RecoveryEventSink and ProgressEventSink shapes so wiring code
// can attach a ChannelBus-backed sink without pulling agents/events
// imports into core/recovery.
//
// The sink is called synchronously from the monitor loop on every
// scored agent — implementations MUST return quickly or spawn their
// own goroutines, otherwise a slow sink stalls the monitor and
// escalation latency blows up with it.
type HealthAssessmentSink interface {
	PublishHealthAssessment(assessment HealthAssessment)
}

// DeadlockResultSink is the parallel sink for REC-03 — it fires every
// time the DeadlockDetector returns a result, not only when
// DeadlockRecovery actually initiates a recovery cycle. UI consumers
// want to see detection immediately; filtering to "only when recovery
// fires" would hide transient detections that resolve on their own,
// which are themselves useful diagnostic signal.
//
// Only results with Detected=true are published; non-detections are
// noise. Implementations MUST return quickly for the same reason
// HealthAssessmentSink does.
type DeadlockResultSink interface {
	PublishDeadlockResult(result DeadlockResult)
}

type HealthMonitor struct {
	healthScorer  *HealthScorer
	deadlockDet   *DeadlockDetector
	orchestrator  *RecoveryOrchestrator
	deadlockRecov *DeadlockRecovery
	agents        AgentEnumerator
	config        RecoveryConfig
	logger        *slog.Logger

	mu           sync.Mutex
	running      bool
	cancel       context.CancelFunc
	healthSink   HealthAssessmentSink
	deadlockSink DeadlockResultSink
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

// SetHealthSink installs the bus-forwarder sink that receives every
// scored assessment on every tick. Passing nil clears the sink —
// teardown paths call this so the monitor loop keeps running but no
// longer publishes while the bus is shutting down.
//
// Safe to call at any point in the monitor's lifecycle; the sink
// pointer is read-guarded inside checkAllAgents.
func (h *HealthMonitor) SetHealthSink(sink HealthAssessmentSink) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.healthSink = sink
}

func (h *HealthMonitor) checkAllAgents() {
	agentIDs := h.agents.ActiveAgentIDs()

	h.mu.Lock()
	sink := h.healthSink
	h.mu.Unlock()

	for _, agentID := range agentIDs {
		assessment := h.healthScorer.Assess(agentID)
		// REC-02: publish every assessment to the bus, not just the
		// ones that trigger escalation. The UI needs to show the full
		// healthy→warning→stuck curve, and the stuck-only subset
		// means the first visible point is already mid-crisis.
		if sink != nil {
			sink.PublishHealthAssessment(assessment)
		}
		if assessment.Status >= StatusStuck {
			h.orchestrator.HandleStuckAgent(assessment)
		}
	}
}

// SetDeadlockSink installs the REC-03 deadlock publish sink. Mirrors
// SetHealthSink: nil clears, subsequent detections fire to the new
// sink. Safe to call at any lifecycle phase.
func (h *HealthMonitor) SetDeadlockSink(sink DeadlockResultSink) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.deadlockSink = sink
}

func (h *HealthMonitor) checkDeadlocks() {
	results := h.deadlockDet.Check()

	h.mu.Lock()
	sink := h.deadlockSink
	h.mu.Unlock()

	for _, result := range results {
		if !result.Detected {
			continue
		}
		// REC-03: publish before recovery. Observers see the
		// detection even when DeadlockRecovery chooses not to act on
		// it (transient, or victim-selection races).
		if sink != nil {
			sink.PublishDeadlockResult(result)
		}
		h.deadlockRecov.HandleDeadlock(result)
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
