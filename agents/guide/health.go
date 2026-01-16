package guide

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Health Check System
// =============================================================================
//
// The health system monitors agent liveness through periodic heartbeats.
// It enables:
// - Detection of unresponsive agents
// - Automatic circuit breaker triggering for dead agents
// - Agent status queries for routing decisions
// - Proactive recovery when agents come back online

// HealthStatus represents the health of an agent
type HealthStatus int32

const (
	HealthStatusUnknown   HealthStatus = iota // No heartbeat received yet
	HealthStatusHealthy                       // Recent heartbeat received
	HealthStatusDegraded                      // Slow responses or partial failures
	HealthStatusUnhealthy                     // Missed heartbeats
	HealthStatusDead                          // Confirmed dead, needs restart
)

func (s HealthStatus) String() string {
	switch s {
	case HealthStatusUnknown:
		return "unknown"
	case HealthStatusHealthy:
		return "healthy"
	case HealthStatusDegraded:
		return "degraded"
	case HealthStatusUnhealthy:
		return "unhealthy"
	case HealthStatusDead:
		return "dead"
	default:
		return "unknown"
	}
}

// AgentHealth tracks the health of a single agent
type AgentHealth struct {
	AgentID string `json:"agent_id"`

	// Status (atomic for lock-free reads)
	status int32

	// Heartbeat tracking
	mu                sync.RWMutex
	lastHeartbeat     time.Time
	lastResponse      time.Time
	missedHeartbeats  int
	avgResponseTimeNs int64

	// Response time tracking (rolling window)
	responseTimes    []int64
	responseTimeIdx  int
	responseTimeCap  int

	// Configuration
	heartbeatInterval time.Duration
	unhealthyAfter    int // Missed heartbeats before unhealthy
	deadAfter         int // Missed heartbeats before dead
}

// AgentHealthConfig configures health tracking for an agent
type AgentHealthConfig struct {
	HeartbeatInterval time.Duration // Default: 10s
	UnhealthyAfter    int           // Default: 3 missed
	DeadAfter         int           // Default: 6 missed
	ResponseTimeCap   int           // Default: 100 samples
}

// DefaultAgentHealthConfig returns sensible defaults
func DefaultAgentHealthConfig() AgentHealthConfig {
	return AgentHealthConfig{
		HeartbeatInterval: 10 * time.Second,
		UnhealthyAfter:    3,
		DeadAfter:         6,
		ResponseTimeCap:   100,
	}
}

// NewAgentHealth creates a new agent health tracker
func NewAgentHealth(agentID string, cfg AgentHealthConfig) *AgentHealth {
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 10 * time.Second
	}
	if cfg.UnhealthyAfter <= 0 {
		cfg.UnhealthyAfter = 3
	}
	if cfg.DeadAfter <= 0 {
		cfg.DeadAfter = 6
	}
	if cfg.ResponseTimeCap <= 0 {
		cfg.ResponseTimeCap = 100
	}

	return &AgentHealth{
		AgentID:           agentID,
		status:            int32(HealthStatusUnknown),
		heartbeatInterval: cfg.HeartbeatInterval,
		unhealthyAfter:    cfg.UnhealthyAfter,
		deadAfter:         cfg.DeadAfter,
		responseTimes:     make([]int64, cfg.ResponseTimeCap),
		responseTimeCap:   cfg.ResponseTimeCap,
	}
}

// Status returns the current health status
func (h *AgentHealth) Status() HealthStatus {
	return HealthStatus(atomic.LoadInt32(&h.status))
}

// IsHealthy returns true if the agent is healthy or degraded
func (h *AgentHealth) IsHealthy() bool {
	status := h.Status()
	return status == HealthStatusHealthy || status == HealthStatusDegraded
}

// RecordHeartbeat records a received heartbeat
func (h *AgentHealth) RecordHeartbeat() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.lastHeartbeat = time.Now()
	h.missedHeartbeats = 0
	atomic.StoreInt32(&h.status, int32(HealthStatusHealthy))
}

// RecordResponse records a successful response with timing
func (h *AgentHealth) RecordResponse(responseTime time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.lastResponse = time.Now()

	// Add to rolling window
	h.responseTimes[h.responseTimeIdx] = responseTime.Nanoseconds()
	h.responseTimeIdx = (h.responseTimeIdx + 1) % h.responseTimeCap

	// Update average
	h.recalculateAvgResponseTime()
}

// RecordMissedHeartbeat records a missed heartbeat
func (h *AgentHealth) RecordMissedHeartbeat() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.missedHeartbeats++

	if h.missedHeartbeats >= h.deadAfter {
		atomic.StoreInt32(&h.status, int32(HealthStatusDead))
	} else if h.missedHeartbeats >= h.unhealthyAfter {
		atomic.StoreInt32(&h.status, int32(HealthStatusUnhealthy))
	}
}

// SetDegraded marks the agent as degraded
func (h *AgentHealth) SetDegraded() {
	h.mu.Lock()
	if h.Status() == HealthStatusHealthy {
		atomic.StoreInt32(&h.status, int32(HealthStatusDegraded))
	}
	h.mu.Unlock()
}

// recalculateAvgResponseTime recalculates average (must hold lock)
func (h *AgentHealth) recalculateAvgResponseTime() {
	var sum int64
	var count int

	for _, rt := range h.responseTimes {
		if rt > 0 {
			sum += rt
			count++
		}
	}

	if count > 0 {
		h.avgResponseTimeNs = sum / int64(count)
	}
}

// Info returns health information
func (h *AgentHealth) Info() AgentHealthInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return AgentHealthInfo{
		AgentID:           h.AgentID,
		Status:            h.Status(),
		LastHeartbeat:     h.lastHeartbeat,
		LastResponse:      h.lastResponse,
		MissedHeartbeats:  h.missedHeartbeats,
		AvgResponseTimeMs: float64(h.avgResponseTimeNs) / 1e6,
	}
}

// AgentHealthInfo contains health information
type AgentHealthInfo struct {
	AgentID           string       `json:"agent_id"`
	Status            HealthStatus `json:"status"`
	LastHeartbeat     time.Time    `json:"last_heartbeat"`
	LastResponse      time.Time    `json:"last_response"`
	MissedHeartbeats  int          `json:"missed_heartbeats"`
	AvgResponseTimeMs float64      `json:"avg_response_time_ms"`
}

// =============================================================================
// Health Monitor
// =============================================================================

// HealthMonitor monitors the health of all agents
type HealthMonitor struct {
	agents   *ShardedMap[string, *AgentHealth]
	bus      EventBus
	config   AgentHealthConfig
	circuits *CircuitBreakerRegistry

	// Control
	stopCh  chan struct{}
	stopped bool
	mu      sync.Mutex

	// Callbacks
	onUnhealthy func(agentID string, status HealthStatus)
	onRecovered func(agentID string)
}

// HealthMonitorConfig configures the health monitor
type HealthMonitorConfig struct {
	AgentConfig AgentHealthConfig
	Circuits    *CircuitBreakerRegistry
	OnUnhealthy func(agentID string, status HealthStatus)
	OnRecovered func(agentID string)
}

// NewHealthMonitor creates a new health monitor
func NewHealthMonitor(bus EventBus, cfg HealthMonitorConfig) *HealthMonitor {
	return &HealthMonitor{
		agents:      NewStringMap[*AgentHealth](DefaultShardCount),
		bus:         bus,
		config:      cfg.AgentConfig,
		circuits:    cfg.Circuits,
		stopCh:      make(chan struct{}),
		onUnhealthy: cfg.OnUnhealthy,
		onRecovered: cfg.OnRecovered,
	}
}

// Start begins health monitoring
func (m *HealthMonitor) Start(ctx context.Context) {
	// Subscribe to heartbeat topic
	m.bus.SubscribeAsync("agents.heartbeat", m.handleHeartbeat)

	// Start periodic check
	go m.checkLoop(ctx)
}

// Stop halts health monitoring
func (m *HealthMonitor) Stop() {
	m.mu.Lock()
	if !m.stopped {
		m.stopped = true
		close(m.stopCh)
	}
	m.mu.Unlock()
}

// Register registers an agent for health monitoring
func (m *HealthMonitor) Register(agentID string) {
	health := NewAgentHealth(agentID, m.config)
	m.agents.Set(agentID, health)
}

// Unregister removes an agent from health monitoring
func (m *HealthMonitor) Unregister(agentID string) {
	m.agents.Delete(agentID)
}

// RecordResponse records a response from an agent
func (m *HealthMonitor) RecordResponse(agentID string, responseTime time.Duration) {
	if health, ok := m.agents.Get(agentID); ok {
		health.RecordResponse(responseTime)
	}
}

// GetStatus returns the health status of an agent
func (m *HealthMonitor) GetStatus(agentID string) HealthStatus {
	if health, ok := m.agents.Get(agentID); ok {
		return health.Status()
	}
	return HealthStatusUnknown
}

// IsHealthy returns true if an agent is healthy
func (m *HealthMonitor) IsHealthy(agentID string) bool {
	if health, ok := m.agents.Get(agentID); ok {
		return health.IsHealthy()
	}
	return false
}

// handleHeartbeat processes heartbeat messages
func (m *HealthMonitor) handleHeartbeat(msg *Message) error {
	agentID := msg.SourceAgentID
	if agentID == "" {
		return nil
	}

	health, ok := m.agents.Get(agentID)
	if !ok {
		// Auto-register unknown agents
		health = NewAgentHealth(agentID, m.config)
		m.agents.Set(agentID, health)
	}

	wasUnhealthy := !health.IsHealthy()
	health.RecordHeartbeat()

	// Check for recovery
	if wasUnhealthy && health.IsHealthy() {
		if m.circuits != nil {
			m.circuits.Get(agentID).Reset()
		}
		if m.onRecovered != nil {
			go m.onRecovered(agentID)
		}
	}

	return nil
}

// checkLoop periodically checks agent health
func (m *HealthMonitor) checkLoop(ctx context.Context) {
	ticker := time.NewTicker(m.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.checkAgents()
		}
	}
}

// checkAgents checks all agents for missed heartbeats
func (m *HealthMonitor) checkAgents() {
	now := time.Now()

	m.agents.Range(func(agentID string, health *AgentHealth) bool {
		health.mu.RLock()
		lastHeartbeat := health.lastHeartbeat
		wasHealthy := health.IsHealthy()
		health.mu.RUnlock()

		// Check if heartbeat is overdue
		if !lastHeartbeat.IsZero() && now.Sub(lastHeartbeat) > m.config.HeartbeatInterval {
			health.RecordMissedHeartbeat()

			// Trigger circuit breaker if now unhealthy
			if wasHealthy && !health.IsHealthy() {
				if m.circuits != nil {
					m.circuits.Get(agentID).ForceOpen()
				}
				if m.onUnhealthy != nil {
					go m.onUnhealthy(agentID, health.Status())
				}
			}
		}

		return true
	})
}

// Stats returns health statistics for all agents
func (m *HealthMonitor) Stats() HealthMonitorStats {
	stats := HealthMonitorStats{
		Agents:   make(map[string]AgentHealthInfo),
		ByStatus: make(map[HealthStatus]int),
	}

	m.agents.Range(func(agentID string, health *AgentHealth) bool {
		info := health.Info()
		stats.Agents[agentID] = info
		stats.ByStatus[info.Status]++
		stats.Total++
		if health.IsHealthy() {
			stats.Healthy++
		}
		return true
	})

	return stats
}

// HealthMonitorStats contains health monitor statistics
type HealthMonitorStats struct {
	Total    int                         `json:"total"`
	Healthy  int                         `json:"healthy"`
	Agents   map[string]AgentHealthInfo  `json:"agents"`
	ByStatus map[HealthStatus]int        `json:"by_status"`
}

// =============================================================================
// Heartbeat Sender
// =============================================================================

// HeartbeatSender sends periodic heartbeats for an agent
type HeartbeatSender struct {
	agentID  string
	bus      EventBus
	interval time.Duration
	stopCh   chan struct{}
	stopped  bool
	mu       sync.Mutex
}

// NewHeartbeatSender creates a new heartbeat sender
func NewHeartbeatSender(agentID string, bus EventBus, interval time.Duration) *HeartbeatSender {
	if interval <= 0 {
		interval = 10 * time.Second
	}

	return &HeartbeatSender{
		agentID:  agentID,
		bus:      bus,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start begins sending heartbeats
func (s *HeartbeatSender) Start() {
	go s.sendLoop()
}

// Stop halts heartbeat sending
func (s *HeartbeatSender) Stop() {
	s.mu.Lock()
	if !s.stopped {
		s.stopped = true
		close(s.stopCh)
	}
	s.mu.Unlock()
}

// sendLoop periodically sends heartbeats
func (s *HeartbeatSender) sendLoop() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Send initial heartbeat
	s.sendHeartbeat()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.sendHeartbeat()
		}
	}
}

// sendHeartbeat sends a single heartbeat
func (s *HeartbeatSender) sendHeartbeat() {
	msg := &Message{
		ID:            generateMessageID(),
		Type:          MessageTypeHeartbeat,
		SourceAgentID: s.agentID,
		Timestamp:     time.Now(),
		Payload: &HeartbeatPayload{
			AgentID:   s.agentID,
			Timestamp: time.Now().UnixNano(),
		},
	}

	s.bus.Publish("agents.heartbeat", msg)
}

// HeartbeatPayload is the payload for heartbeat messages
type HeartbeatPayload struct {
	AgentID   string `json:"agent_id"`
	Timestamp int64  `json:"timestamp"`
	Load      int    `json:"load,omitempty"`      // Optional: current load
	Capacity  int    `json:"capacity,omitempty"`  // Optional: max capacity
}
