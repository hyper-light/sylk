package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/google/uuid"
)

// HealthMonitor monitors agent and task health (6.132)
type HealthMonitor struct {
	orchestrator *Orchestrator
	config       HealthConfig

	agents map[string]*AgentHealthMetrics
	alerts map[string]*HealthAlert

	onResult func(*HealthCheckResult)

	errorWindow   []errorWindowEntry
	errorWindowMu sync.RWMutex

	scope     *concurrency.GoroutineScope
	sessionID string

	stopCh  chan struct{}
	stopped int32
	mu      sync.RWMutex
}

type errorWindowEntry struct {
	agentID   string
	taskID    string
	timestamp time.Time
	error     string
}

func NewHealthMonitor(o *Orchestrator, cfg HealthConfig) *HealthMonitor {
	return &HealthMonitor{
		orchestrator: o,
		config:       cfg,
		agents:       make(map[string]*AgentHealthMetrics),
		alerts:       make(map[string]*HealthAlert),
		errorWindow:  make([]errorWindowEntry, 0, 100),
		stopCh:       make(chan struct{}),
	}
}

// SetResultCallback sets the function invoked after each monitoring cycle.
// The callback fires outside m.mu to prevent deadlocks.
func (m *HealthMonitor) SetResultCallback(fn func(*HealthCheckResult)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onResult = fn
}

func (m *HealthMonitor) Start(ctx context.Context) {
	if m.scope != nil {
		_ = m.scope.Go("health-monitor", 0, func(gctx context.Context) error {
			m.monitorLoop(gctx)
			return nil
		})
		return
	}
	go m.monitorLoop(ctx)
}

func (m *HealthMonitor) Stop() {
	if atomic.CompareAndSwapInt32(&m.stopped, 0, 1) {
		close(m.stopCh)
	}
}

func (m *HealthMonitor) monitorLoop(ctx context.Context) {
	ticker := time.NewTicker(m.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.checkHealthStatus()
		}
	}
}

func (m *HealthMonitor) checkHealthStatus() {
	result := m.runChecks()

	// Invoke callback outside m.mu to prevent deadlock.
	// Lock ordering: o.mu → m.mu is safe; callback must never acquire m.mu.
	if m.onResult != nil {
		m.onResult(result)
	}

	m.postHealthClaims(result)
}

// postHealthClaims posts consultation claims for degraded/unhealthy/critical agents
// and submits an overall health summary testament.
func (m *HealthMonitor) postHealthClaims(result *HealthCheckResult) {
	if result == nil || m.sessionID == "" {
		return
	}
	board := claims.DefaultSessionBoardRegistry().Lookup(m.sessionID)
	if board == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Post consultation claims for agents that are degraded/unhealthy/critical.
	for _, ar := range result.AgentResults {
		if ar.Level == HealthLevelHealthy || ar.Level == HealthLevelUnknown {
			continue
		}
		consultClaim := orchestratorConsultClaim(
			fmt.Sprintf("Health alert: %s is %s", ar.AgentID, ar.Level),
			fmt.Sprintf("Agent %s health degraded: level=%s, missed_heartbeats=%d, error_rate=%.2f, active_alerts=%d",
				ar.AgentID, ar.Level, ar.MissedHeartbeats, ar.ErrorRate, ar.ActiveAlertCount),
			ar.AgentID,
			[]claims.ClaimScopeEntry{{Kind: "agent", Key: ar.AgentID}},
			[]*claims.Validation{
				orchestratorValidation(claims.ValidationTypeReceipt, false, "Agent returns to healthy status", "agent.health == healthy"),
			},
		)
		action := claims.Action{AgentID: "orchestrator", Type: claims.ActionTypeConsultation}
		if err := board.PostAction(ctx, action, []claims.Claim{consultClaim}); err != nil {
			slog.Error("health_monitor_claim_post_failed", "agent_id", ar.AgentID, "error", err.Error())
			board.RecordNotificationError("health monitor claim: " + err.Error())
		}
	}

	// Submit overall health summary testament.
	summary := result.Summary
	confidence := "committed"
	if summary.UnhealthyAgents > 0 {
		confidence = "low"
	} else if summary.DegradedAgents > 0 {
		confidence = "medium"
	}
	testament := claims.Testament{
		AgentID:   "orchestrator",
		SessionID: m.sessionID,
		Summary: fmt.Sprintf("Health check: %s (agents=%d healthy=%d degraded=%d unhealthy=%d alerts=%d)",
			summary.OverallStatus, summary.AgentCount, summary.HealthyAgents, summary.DegradedAgents, summary.UnhealthyAgents, len(summary.ActiveAlerts)),
		Confidence: confidence,
		Relations: []claims.Relation{
			{Related: "orchestrator", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
	}
	testamentAction := claims.Action{AgentID: "orchestrator", Type: claims.ActionTypeTestament}
	if err := board.SubmitTestaments(ctx, testamentAction, []claims.Testament{testament}); err != nil {
		slog.Error("health_monitor_testament_failed", "error", err.Error())
		board.RecordNotificationError("health monitor testament: " + err.Error())
	}
}

// runChecks performs all health checks under m.mu and produces a HealthCheckResult.
func (m *HealthMonitor) runChecks() *HealthCheckResult {
	taskTimeouts := m.snapshotTaskTimeouts()

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()

	// Snapshot state before checks for transition detection.
	priorAlertIDs := m.snapshotAlertIDsLocked()
	priorLevels := m.snapshotAgentLevelsLocked()

	for agentID, metrics := range m.agents {
		m.checkHeartbeatTimeout(agentID, metrics, now)
		m.checkTaskTimeout(agentID, metrics, now, taskTimeouts)
		m.checkErrorRate(agentID, metrics, now)
		m.checkTransientStorm(agentID, metrics, now)
		m.updateHealthLevel(metrics)
	}

	return m.buildCheckResultLocked(now, priorAlertIDs, priorLevels)
}

type healthTaskTimeoutSnapshot struct {
	ID             string
	AssignedAgent  string
	StageAgent     string
	Status         TaskStatus
	PipelineStage  string
	StartedAt      *time.Time
	StageStartedAt *time.Time
}

func (m *HealthMonitor) snapshotTaskTimeouts() []healthTaskTimeoutSnapshot {
	if m == nil || m.orchestrator == nil || m.orchestrator.state == nil {
		return nil
	}

	m.orchestrator.mu.RLock()
	defer m.orchestrator.mu.RUnlock()

	snapshots := make([]healthTaskTimeoutSnapshot, 0, len(m.orchestrator.state.Tasks))
	for _, task := range m.orchestrator.state.Tasks {
		if task == nil {
			continue
		}
		snapshot := healthTaskTimeoutSnapshot{
			ID:            task.ID,
			AssignedAgent: task.AssignedAgentID,
			StageAgent:    task.StageAgentID,
			Status:        task.Status,
			PipelineStage: task.PipelineStage,
		}
		if task.StartedAt != nil {
			startedAt := *task.StartedAt
			snapshot.StartedAt = &startedAt
		}
		if task.StageStartedAt != nil {
			stageStartedAt := *task.StageStartedAt
			snapshot.StageStartedAt = &stageStartedAt
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

// snapshotAlertIDsLocked captures the set of alert IDs before checks run.
// Caller must hold m.mu.
func (m *HealthMonitor) snapshotAlertIDsLocked() map[string]struct{} {
	snapshot := make(map[string]struct{}, len(m.alerts))
	for id := range m.alerts {
		snapshot[id] = struct{}{}
	}
	return snapshot
}

// snapshotAgentLevelsLocked captures per-agent HealthLevel before checks run.
// Caller must hold m.mu.
func (m *HealthMonitor) snapshotAgentLevelsLocked() map[string]HealthLevel {
	snapshot := make(map[string]HealthLevel, len(m.agents))
	for id, metrics := range m.agents {
		snapshot[id] = metrics.Level
	}
	return snapshot
}

// buildCheckResultLocked builds a HealthCheckResult from current state + pre-check snapshots.
// Caller must hold m.mu.
func (m *HealthMonitor) buildCheckResultLocked(
	checkedAt time.Time,
	priorAlertIDs map[string]struct{},
	priorLevels map[string]HealthLevel,
) *HealthCheckResult {
	result := &HealthCheckResult{
		CheckedAt:    checkedAt,
		AgentResults: make([]AgentCheckResult, 0, len(m.agents)),
		Summary:      m.buildSummaryLocked(),
	}

	if m.orchestrator != nil {
		result.SessionID = m.orchestrator.config.SessionID
	}

	for agentID, metrics := range m.agents {
		activeAlertCount := 0
		for _, alert := range metrics.ActiveAlerts {
			if alert.ResolvedAt == nil {
				activeAlertCount++
			}
		}

		result.AgentResults = append(result.AgentResults, AgentCheckResult{
			AgentID:          agentID,
			Level:            metrics.Level,
			PreviousLevel:    priorLevels[agentID],
			MissedHeartbeats: metrics.MissedHeartbeats,
			ActiveTasks:      metrics.ActiveTasks,
			ErrorRate:        metrics.ErrorRate,
			ActiveAlertCount: activeAlertCount,
		})
	}

	// Identify new alerts (created during this check cycle).
	for id, alert := range m.alerts {
		if _, existed := priorAlertIDs[id]; !existed {
			result.NewAlerts = append(result.NewAlerts, *alert)
		}
	}

	return result
}

// buildSummaryLocked builds a HealthSummary. Caller must hold m.mu.
func (m *HealthMonitor) buildSummaryLocked() HealthSummary {
	summary := HealthSummary{
		OverallStatus: HealthLevelHealthy,
		AgentCount:    len(m.agents),
	}

	for _, metrics := range m.agents {
		switch metrics.Level {
		case HealthLevelHealthy:
			summary.HealthyAgents++
		case HealthLevelDegraded:
			summary.DegradedAgents++
		case HealthLevelUnhealthy, HealthLevelCritical:
			summary.UnhealthyAgents++
		}

		for _, alert := range metrics.ActiveAlerts {
			if alert.ResolvedAt == nil {
				summary.ActiveAlerts = append(summary.ActiveAlerts, alert)
			}
		}
	}

	if summary.UnhealthyAgents > 0 {
		summary.OverallStatus = HealthLevelUnhealthy
	} else if summary.DegradedAgents > 0 {
		summary.OverallStatus = HealthLevelDegraded
	}

	return summary
}

func (m *HealthMonitor) checkHeartbeatTimeout(agentID string, metrics *AgentHealthMetrics, now time.Time) {
	if metrics.LastHeartbeat.IsZero() {
		return
	}

	elapsed := now.Sub(metrics.LastHeartbeat)
	if elapsed > m.config.HeartbeatTimeout {
		metrics.MissedHeartbeats++
		m.createAlert(agentID, AlertTypeHeartbeatLost, HealthLevelDegraded,
			"Agent missed heartbeat")
	}
}

func (m *HealthMonitor) checkTaskTimeout(agentID string, metrics *AgentHealthMetrics, now time.Time, tasks []healthTaskTimeoutSnapshot) {
	for _, task := range tasks {
		if task.AssignedAgent != agentID && task.StageAgent != agentID {
			continue
		}
		if task.Status.IsTerminal() {
			continue
		}

		// Stage-aware timeout: use stage start time if available.
		startTime := task.StartedAt
		if task.StageStartedAt != nil {
			startTime = task.StageStartedAt
		}
		if startTime == nil {
			continue
		}

		// Check queued tasks (no ACK received yet).
		if task.Status == TaskStatusQueued {
			elapsed := now.Sub(*startTime)
			if elapsed > m.config.TaskTimeout {
				m.createAlert(agentID, AlertTypeTimeout, HealthLevelUnhealthy,
					"Task stuck in queue (no ACK): "+task.ID)
				metrics.TimedOutTasks++
			}
			continue
		}

		// Running tasks
		elapsed := now.Sub(*startTime)
		if elapsed > m.config.TaskTimeout {
			stageInfo := ""
			if task.PipelineStage != "" {
				stageInfo = " (stage: " + task.PipelineStage + ")"
			}
			m.createAlert(agentID, AlertTypeTimeout, HealthLevelUnhealthy,
				"Task timed out: "+task.ID+stageInfo)
			metrics.TimedOutTasks++
		}
	}
}

// RecordStageTransition records the transition of a task from one pipeline
// stage to the next. Decrements ActiveTasks on the completing agent and
// increments on the next.
func (m *HealthMonitor) RecordStageTransition(fromAgentID, toAgentID, taskID, stage string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if fromMetrics, ok := m.agents[fromAgentID]; ok {
		if fromMetrics.ActiveTasks > 0 {
			fromMetrics.ActiveTasks--
		}
		fromMetrics.CompletedTasks++
		fromMetrics.LastSeen = time.Now()
	}

	if toMetrics, ok := m.agents[toAgentID]; ok {
		toMetrics.ActiveTasks++
		toMetrics.TotalRequests++
		toMetrics.LastSeen = time.Now()
	}
}

func (m *HealthMonitor) checkErrorRate(agentID string, metrics *AgentHealthMetrics, now time.Time) {
	if metrics.TotalRequests == 0 {
		return
	}

	metrics.ErrorRate = float64(metrics.TotalErrors) / float64(metrics.TotalRequests)

	if metrics.ErrorRate >= m.config.ErrorRateThreshold {
		m.createAlert(agentID, AlertTypeHighErrorRate, HealthLevelUnhealthy,
			"High error rate detected")
	}
}

func (m *HealthMonitor) checkTransientStorm(agentID string, metrics *AgentHealthMetrics, now time.Time) {
	m.errorWindowMu.Lock()
	defer m.errorWindowMu.Unlock()

	cutoff := now.Add(-m.config.StormWindow)
	count := 0

	filtered := make([]errorWindowEntry, 0, len(m.errorWindow))
	for _, entry := range m.errorWindow {
		if entry.timestamp.After(cutoff) {
			filtered = append(filtered, entry)
			if entry.agentID == agentID {
				count++
			}
		}
	}
	m.errorWindow = filtered

	if count >= m.config.StormThreshold {
		m.createAlert(agentID, AlertTypeTransientStorm, HealthLevelCritical,
			"Transient failure storm detected")
	}
}

func (m *HealthMonitor) updateHealthLevel(metrics *AgentHealthMetrics) {
	hasActive := false
	highestLevel := HealthLevelHealthy

	for _, alert := range m.alerts {
		if alert.AgentID != metrics.AgentID {
			continue
		}
		if alert.ResolvedAt != nil {
			continue
		}
		hasActive = true

		switch alert.Level {
		case HealthLevelCritical:
			highestLevel = HealthLevelCritical
		case HealthLevelUnhealthy:
			if highestLevel != HealthLevelCritical {
				highestLevel = HealthLevelUnhealthy
			}
		case HealthLevelDegraded:
			if highestLevel == HealthLevelHealthy {
				highestLevel = HealthLevelDegraded
			}
		}
	}

	if hasActive {
		metrics.Level = highestLevel
	} else {
		metrics.Level = HealthLevelHealthy
	}
}

func (m *HealthMonitor) createAlert(agentID string, alertType AlertType, level HealthLevel, message string) {
	alertID := uuid.New().String()[:8]
	alert := &HealthAlert{
		ID:          alertID,
		AgentID:     agentID,
		Type:        alertType,
		Level:       level,
		Message:     message,
		TriggeredAt: time.Now(),
	}

	m.alerts[alertID] = alert

	if metrics, ok := m.agents[agentID]; ok {
		metrics.ActiveAlerts = append(metrics.ActiveAlerts, *alert)
	}
}

func (m *HealthMonitor) RegisterAgent(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.agents[agentID]; ok {
		return
	}

	m.agents[agentID] = &AgentHealthMetrics{
		AgentID:   agentID,
		Level:     HealthLevelUnknown,
		FirstSeen: time.Now(),
		LastSeen:  time.Now(),
	}
}

func (m *HealthMonitor) UnregisterAgent(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.agents, agentID)
}

func (m *HealthMonitor) RecordHeartbeat(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics, ok := m.agents[agentID]
	if !ok {
		m.agents[agentID] = &AgentHealthMetrics{
			AgentID:       agentID,
			Level:         HealthLevelHealthy,
			LastHeartbeat: time.Now(),
			FirstSeen:     time.Now(),
			LastSeen:      time.Now(),
		}
		return
	}

	metrics.LastHeartbeat = time.Now()
	metrics.LastSeen = time.Now()
	metrics.MissedHeartbeats = 0
}

func (m *HealthMonitor) RecordTaskStart(agentID, taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics, ok := m.agents[agentID]
	if !ok {
		return
	}

	metrics.ActiveTasks++
	metrics.TotalRequests++
	metrics.LastSeen = time.Now()
}

func (m *HealthMonitor) RecordTaskComplete(agentID, taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics, ok := m.agents[agentID]
	if !ok {
		return
	}

	if metrics.ActiveTasks > 0 {
		metrics.ActiveTasks--
	}
	metrics.CompletedTasks++
	metrics.LastSeen = time.Now()
}

func (m *HealthMonitor) RecordTaskFailed(agentID, taskID, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics, ok := m.agents[agentID]
	if !ok {
		return
	}

	if metrics.ActiveTasks > 0 {
		metrics.ActiveTasks--
	}
	metrics.FailedTasks++
	metrics.TotalErrors++
	metrics.LastSeen = time.Now()

	metrics.RecentErrors = append(metrics.RecentErrors, ErrorRecord{
		Timestamp: time.Now(),
		TaskID:    taskID,
		Error:     errMsg,
	})
	if len(metrics.RecentErrors) > 20 {
		metrics.RecentErrors = metrics.RecentErrors[1:]
	}

	m.errorWindowMu.Lock()
	m.errorWindow = append(m.errorWindow, errorWindowEntry{
		agentID:   agentID,
		taskID:    taskID,
		timestamp: time.Now(),
		error:     errMsg,
	})
	m.errorWindowMu.Unlock()
}

func (m *HealthMonitor) RecordResponseTime(agentID string, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics, ok := m.agents[agentID]
	if !ok {
		return
	}

	ms := float64(duration.Milliseconds())

	if ms > metrics.MaxResponseTimeMs {
		metrics.MaxResponseTimeMs = ms
	}

	if metrics.AvgResponseTimeMs == 0 {
		metrics.AvgResponseTimeMs = ms
	} else {
		metrics.AvgResponseTimeMs = (metrics.AvgResponseTimeMs*0.9 + ms*0.1)
	}
}

func (m *HealthMonitor) GetAgentHealth(agentID string) *AgentHealthMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.agents[agentID]
}

func (m *HealthMonitor) GetSummary() HealthSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.buildSummaryLocked()
}

func (m *HealthMonitor) ResolveAlert(alertID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	alert, ok := m.alerts[alertID]
	if !ok {
		return
	}

	now := time.Now()
	alert.ResolvedAt = &now
}
