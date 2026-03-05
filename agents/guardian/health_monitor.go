package guardian

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/providers"
)

// CostCalculator computes the cost in USD-cents for a given model and full
// token usage breakdown. Implementations can account for cache read/write
// pricing differentials. Returns 0 when pricing is unavailable.
type CostCalculator func(model string, usage *providers.Usage) int64

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

	// Per-agent and per-model usage breakdown.
	agentUsage map[string]*usageEntry
	modelUsage map[string]*usageEntry

	running bool
	cancel  context.CancelFunc
	onEvent OnEventFunc
}

// usageEntry tracks token and cost usage for a single agent or model.
type usageEntry struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	ReasoningTokens  int64 `json:"reasoning_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	CostCents        int64 `json:"cost_cents"`
	RequestCount     int64 `json:"request_count"`
}

// SetOnEvent wires a callback for WAL event emission.
func (hm *HealthMonitor) SetOnEvent(fn OnEventFunc) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.onEvent = fn
}

// SetCostCalculator wires a function that converts a model and full token
// usage breakdown into a cost in USD-cents. Called under the monitor lock
// so the implementation must not block.
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
		agentUsage:   make(map[string]*usageEntry),
		modelUsage:   make(map[string]*usageEntry),
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
// Expects EventTypeLLMResponse events with input_tokens, output_tokens, and
// model fields. Cost is derived via the CostCalculator when wired.
func (hm *HealthMonitor) RecordTokenUsage(evt *events.ActivityEvent) {
	if evt == nil || evt.Data == nil {
		return
	}
	hm.mu.Lock()
	defer hm.mu.Unlock()

	usage := extractUsageFromEvent(evt)
	hm.tokenUsed += int64(usage.InputTokens + usage.OutputTokens)

	var costDelta int64
	model, _ := evt.Data["model"].(string)
	if hm.costCalculator != nil && model != "" {
		costDelta = hm.costCalculator(model, &usage)
		hm.costUsed += costDelta
	}

	// Per-agent breakdown.
	if agentID, ok := evt.Data["agent_id"].(string); ok && agentID != "" {
		au := hm.agentUsage[agentID]
		if au == nil {
			au = &usageEntry{}
			hm.agentUsage[agentID] = au
		}
		accumulateUsageEntry(au, &usage, costDelta)
	}

	// Per-model breakdown.
	if model != "" {
		mu := hm.modelUsage[model]
		if mu == nil {
			mu = &usageEntry{}
			hm.modelUsage[model] = mu
		}
		accumulateUsageEntry(mu, &usage, costDelta)
	}
}

// extractUsageFromEvent builds a providers.Usage from activity event data.
func extractUsageFromEvent(evt *events.ActivityEvent) providers.Usage {
	var u providers.Usage
	if v, ok := evt.Data["input_tokens"].(int); ok {
		u.InputTokens = v
	}
	if v, ok := evt.Data["output_tokens"].(int); ok {
		u.OutputTokens = v
	}
	if v, ok := evt.Data["reasoning_tokens"].(int); ok {
		u.ReasoningTokens = v
	}
	if v, ok := evt.Data["cache_read_tokens"].(int); ok {
		u.CacheReadTokens = v
	}
	if v, ok := evt.Data["cache_write_tokens"].(int); ok {
		u.CacheWriteTokens = v
	}
	return u
}

// accumulateUsageEntry adds usage deltas to a usage entry.
func accumulateUsageEntry(entry *usageEntry, usage *providers.Usage, costDelta int64) {
	entry.InputTokens += int64(usage.InputTokens)
	entry.OutputTokens += int64(usage.OutputTokens)
	entry.ReasoningTokens += int64(usage.ReasoningTokens)
	entry.CacheReadTokens += int64(usage.CacheReadTokens)
	entry.CacheWriteTokens += int64(usage.CacheWriteTokens)
	entry.CostCents += costDelta
	entry.RequestCount++
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
		TokensUsed:       hm.tokenUsed,
		TokenBudget:      hm.tokenBudget,
		CostCentsUsed:    hm.costUsed,
		CostBudget:       hm.costBudget,
		BudgetConfigured: hm.tokenBudget > 0 || hm.costBudget > 0,
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

// UsageBreakdown returns per-agent and per-model token/cost breakdowns.
func (hm *HealthMonitor) UsageBreakdown() (agentBreakdown, modelBreakdown map[string]*usageEntry) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	agentBreakdown = make(map[string]*usageEntry, len(hm.agentUsage))
	for k, v := range hm.agentUsage {
		cp := *v
		agentBreakdown[k] = &cp
	}
	modelBreakdown = make(map[string]*usageEntry, len(hm.modelUsage))
	for k, v := range hm.modelUsage {
		cp := *v
		modelBreakdown[k] = &cp
	}
	return agentBreakdown, modelBreakdown
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
