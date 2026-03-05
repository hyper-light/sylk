package guardian

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/events"
)

// CostCalculator computes the cost in USD-cents for a given model and token
// counts. Implementations typically look up per-model pricing from a registry.
// Returns 0 when pricing is unavailable.
type CostCalculator func(model string, inputTokens, outputTokens int) int64

// HealthMonitor tracks agent health, budget consumption, and anomalies.
type HealthMonitor struct {
	interval     time.Duration
	agentTimeout time.Duration
	tokenBudget  int64
	costBudget   int64

	mu             sync.RWMutex
	agents         map[string]*agentHealth
	tokenUsed      int64
	costUsed       int64
	costCalculator CostCalculator

	running bool
	cancel  context.CancelFunc
	onEvent OnEventFunc
}

// SetOnEvent wires a callback for WAL event emission.
func (hm *HealthMonitor) SetOnEvent(fn OnEventFunc) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.onEvent = fn
}

// SetCostCalculator wires a function that converts (model, input, output)
// token counts into a cost in USD-cents. Called under the monitor lock so
// the implementation must not block.
func (hm *HealthMonitor) SetCostCalculator(fn CostCalculator) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.costCalculator = fn
}

type agentHealth struct {
	AgentID       string
	AgentType     string
	LastHeartbeat time.Time
	ErrorCount    int
	RestartCount  int
	CircuitOpen   bool
	LastError     string
	TokensUsed    int64
	Responsive    bool
}

// NewHealthMonitor creates a health monitor.
func NewHealthMonitor(interval, agentTimeout time.Duration, tokenBudget, costBudget int64) *HealthMonitor {
	return &HealthMonitor{
		interval:     interval,
		agentTimeout: agentTimeout,
		tokenBudget:  tokenBudget,
		costBudget:   costBudget,
		agents:       make(map[string]*agentHealth),
	}
}

// Start begins periodic health checks.
func (hm *HealthMonitor) Start(ctx context.Context) {
	hm.mu.Lock()
	if hm.running {
		hm.mu.Unlock()
		return
	}
	hm.running = true
	tickCtx, cancel := context.WithCancel(ctx)
	hm.cancel = cancel
	hm.mu.Unlock()

	go hm.tickerLoop(tickCtx)
}

// Stop halts health monitoring.
func (hm *HealthMonitor) Stop() {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	if !hm.running {
		return
	}
	hm.running = false
	if hm.cancel != nil {
		hm.cancel()
		hm.cancel = nil
	}
}

// RecordHeartbeat records a heartbeat from an agent.
func (hm *HealthMonitor) RecordHeartbeat(agentID, agentType string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	ah, ok := hm.agents[agentID]
	if !ok {
		ah = &agentHealth{AgentID: agentID, AgentType: agentType, Responsive: true}
		hm.agents[agentID] = ah
	}
	ah.LastHeartbeat = time.Now()
	ah.Responsive = true
}

// RecordError records an agent error.
func (hm *HealthMonitor) RecordError(agentID, errMsg string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	ah, ok := hm.agents[agentID]
	if !ok {
		ah = &agentHealth{AgentID: agentID, Responsive: true}
		hm.agents[agentID] = ah
	}
	ah.ErrorCount++
	ah.LastError = errMsg
}

// RecordRestart records an agent restart.
func (hm *HealthMonitor) RecordRestart(agentID string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	ah, ok := hm.agents[agentID]
	if !ok {
		ah = &agentHealth{AgentID: agentID, Responsive: true}
		hm.agents[agentID] = ah
	}
	ah.RestartCount++
}

// RecordTokenUsage accumulates token and cost usage from activity events.
// Token fields are additive; cost is derived via the CostCalculator when
// wired, using the model name and per-call input/output token counts.
func (hm *HealthMonitor) RecordTokenUsage(evt *events.ActivityEvent) {
	if evt == nil || evt.Data == nil {
		return
	}
	hm.mu.Lock()
	defer hm.mu.Unlock()

	var inputTok, outputTok int
	if v, ok := evt.Data["input_tokens"].(int); ok {
		inputTok = v
		hm.tokenUsed += int64(v)
	}
	if v, ok := evt.Data["output_tokens"].(int); ok {
		outputTok = v
		hm.tokenUsed += int64(v)
	}
	if v, ok := evt.Data["total_tokens"].(int); ok {
		hm.tokenUsed += int64(v)
	}

	// Accumulate cost when a calculator is wired and the event carries a model.
	if hm.costCalculator != nil {
		if model, ok := evt.Data["model"].(string); ok && model != "" {
			hm.costUsed += hm.costCalculator(model, inputTok, outputTok)
		}
	}
}

// AllSnapshots returns health snapshots for all known agents.
func (hm *HealthMonitor) AllSnapshots() []AgentHealthSnapshot {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	snapshots := make([]AgentHealthSnapshot, 0, len(hm.agents))
	for _, ah := range hm.agents {
		snapshots = append(snapshots, AgentHealthSnapshot{
			AgentID:      ah.AgentID,
			AgentType:    ah.AgentType,
			Responsive:   ah.Responsive,
			CircuitOpen:  ah.CircuitOpen,
			RestartCount: ah.RestartCount,
			LastHeartbeat: ah.LastHeartbeat,
			ErrorCount:   ah.ErrorCount,
			LastError:    ah.LastError,
			TokensUsed:   ah.TokensUsed,
		})
	}
	return snapshots
}

// AgentSnapshot returns the health snapshot for a specific agent.
func (hm *HealthMonitor) AgentSnapshot(agentID string) (AgentHealthSnapshot, bool) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	ah, ok := hm.agents[agentID]
	if !ok {
		return AgentHealthSnapshot{}, false
	}
	return AgentHealthSnapshot{
		AgentID:      ah.AgentID,
		AgentType:    ah.AgentType,
		Responsive:   ah.Responsive,
		CircuitOpen:  ah.CircuitOpen,
		RestartCount: ah.RestartCount,
		LastHeartbeat: ah.LastHeartbeat,
		ErrorCount:   ah.ErrorCount,
		LastError:    ah.LastError,
		TokensUsed:   ah.TokensUsed,
	}, true
}

// BudgetSnapshot returns the current budget consumption status.
func (hm *HealthMonitor) BudgetSnapshot() BudgetStatus {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	status := BudgetStatus{
		TokensUsed:     hm.tokenUsed,
		TokenBudget:    hm.tokenBudget,
		CostCentsUsed:  hm.costUsed,
		CostBudget:     hm.costBudget,
	}

	if hm.tokenBudget > 0 {
		status.TokenPercent = float64(hm.tokenUsed) / float64(hm.tokenBudget) * 100
		status.Warning = status.TokenPercent >= 80
		status.Exceeded = status.TokenPercent >= 100
	}
	if hm.costBudget > 0 {
		status.CostPercent = float64(hm.costUsed) / float64(hm.costBudget) * 100
		if status.CostPercent >= 80 {
			status.Warning = true
		}
		if status.CostPercent >= 100 {
			status.Exceeded = true
		}
	}
	return status
}

// DetectAnomalies checks for health anomalies across all agents.
func (hm *HealthMonitor) DetectAnomalies() []Finding {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	findings := make([]Finding, 0)
	now := time.Now()

	// Unresponsive agents: no heartbeat for 3x health check intervals.
	unresponsiveThreshold := hm.interval * 3
	for _, ah := range hm.agents {
		if !ah.LastHeartbeat.IsZero() && now.Sub(ah.LastHeartbeat) > unresponsiveThreshold {
			ah.Responsive = false
			findings = append(findings, Finding{
				Type:     FindingAgentTimeout,
				Severity: SeverityHigh,
				Title:    "Agent unresponsive: " + ah.AgentID,
				AgentID:  ah.AgentID,
				Timestamp: now,
			})
		}
	}

	// Budget warnings.
	if hm.tokenBudget > 0 {
		pct := float64(hm.tokenUsed) / float64(hm.tokenBudget) * 100
		if pct >= 100 {
			findings = append(findings, Finding{
				Type:     FindingBudgetExceeded,
				Severity: SeverityCritical,
				Title:    "Token budget exceeded",
				Timestamp: now,
				Data: map[string]any{
					"used":    hm.tokenUsed,
					"budget":  hm.tokenBudget,
					"percent": pct,
				},
			})
		} else if pct >= 80 {
			findings = append(findings, Finding{
				Type:     FindingBudgetWarning,
				Severity: SeverityMedium,
				Title:    "Token budget warning (>80%)",
				Timestamp: now,
				Data: map[string]any{
					"used":    hm.tokenUsed,
					"budget":  hm.tokenBudget,
					"percent": pct,
				},
			})
		}
	}

	return findings
}

func (hm *HealthMonitor) tickerLoop(ctx context.Context) {
	ticker := time.NewTicker(hm.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			anomalies := hm.DetectAnomalies()
			hm.mu.RLock()
			onEvt := hm.onEvent
			hm.mu.RUnlock()
			if onEvt != nil && len(anomalies) > 0 {
				onEvt(agentlog.EventHealthAlert, "warn", &agentlog.HealthPayload{
					Status: "anomaly",
					Metric: fmt.Sprintf("%d health anomalies detected", len(anomalies)),
				})
			}
		}
	}
}
