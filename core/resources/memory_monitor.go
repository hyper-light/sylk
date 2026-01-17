package resources

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Types and Constants
// =============================================================================

// ComponentName represents a memory-tracked component.
type ComponentName string

const (
	ComponentQueryCache   ComponentName = "query_cache"
	ComponentStaging      ComponentName = "staging"
	ComponentAgentContext ComponentName = "agent_context"
	ComponentWAL          ComponentName = "wal"
)

// MemoryPressureLevel indicates the severity of memory pressure.
type MemoryPressureLevel int

const (
	MemoryPressureNone      MemoryPressureLevel = iota
	MemoryPressureLow                           // 70% component
	MemoryPressureHigh                          // 90% component
	MemoryPressureCritical                      // 80% global
	MemoryPressureEmergency                     // 95% global
)

// Default budgets in bytes.
const (
	DefaultQueryCacheBudget   int64 = 500 * 1024 * 1024  // 500MB
	DefaultStagingBudget      int64 = 1024 * 1024 * 1024 // 1GB
	DefaultAgentContextBudget int64 = 500 * 1024 * 1024  // 500MB
	DefaultWALBudget          int64 = 200 * 1024 * 1024  // 200MB
)

// Default thresholds.
const (
	DefaultGlobalCeilingPercent     float64 = 0.80
	DefaultComponentLRUThreshold    float64 = 0.70
	DefaultComponentWarnThreshold   float64 = 0.90
	DefaultGlobalPauseThreshold     float64 = 0.80
	DefaultGlobalEmergencyThreshold float64 = 0.95
	DefaultMonitorInterval                  = 1 * time.Second
)

// =============================================================================
// Signal Bus Interface (Stub)
// =============================================================================

// MemorySignal represents memory-related signals.
type MemorySignal string

const (
	SignalComponentLRU        MemorySignal = "memory.component.lru"
	SignalComponentAggressive MemorySignal = "memory.component.aggressive"
	SignalGlobalPause         MemorySignal = "memory.global.pause"
	SignalGlobalEmergency     MemorySignal = "memory.global.emergency"
	SignalGlobalResume        MemorySignal = "memory.global.resume"
)

// SignalPublisher publishes memory signals.
type SignalPublisher interface {
	PublishMemorySignal(signal MemorySignal, payload MemorySignalPayload) error
}

// MemorySignalPayload contains signal context.
type MemorySignalPayload struct {
	Component     ComponentName
	CurrentBytes  int64
	BudgetBytes   int64
	GlobalBytes   int64
	GlobalCeiling int64
	Level         MemoryPressureLevel
	Timestamp     time.Time
}

// NoOpSignalPublisher is a stub implementation.
type NoOpSignalPublisher struct{}

// PublishMemorySignal does nothing (stub).
func (n *NoOpSignalPublisher) PublishMemorySignal(_ MemorySignal, _ MemorySignalPayload) error {
	return nil
}

// =============================================================================
// Component Memory
// =============================================================================

// ComponentMemory tracks memory usage for a single component.
type ComponentMemory struct {
	mu          sync.RWMutex
	name        ComponentName
	budget      int64
	current     int64
	peak        int64
	lastUpdated time.Time
}

// NewComponentMemory creates a new component memory tracker.
func NewComponentMemory(name ComponentName, budget int64) *ComponentMemory {
	return &ComponentMemory{
		name:        name,
		budget:      budget,
		lastUpdated: time.Now(),
	}
}

// Name returns the component name.
func (c *ComponentMemory) Name() ComponentName {
	return c.name
}

// Budget returns the budget in bytes.
func (c *ComponentMemory) Budget() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.budget
}

// SetBudget updates the budget.
func (c *ComponentMemory) SetBudget(budget int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.budget = budget
}

// Current returns current usage in bytes.
func (c *ComponentMemory) Current() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

// Peak returns peak usage in bytes.
func (c *ComponentMemory) Peak() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.peak
}

// Add adds bytes to the component's usage.
func (c *ComponentMemory) Add(bytes int64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current += bytes
	c.lastUpdated = time.Now()
	if c.current > c.peak {
		c.peak = c.current
	}
	return c.current
}

// Remove removes bytes from the component's usage.
func (c *ComponentMemory) Remove(bytes int64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current -= bytes
	if c.current < 0 {
		c.current = 0
	}
	c.lastUpdated = time.Now()
	return c.current
}

// UsagePercent returns usage as a percentage of budget.
func (c *ComponentMemory) UsagePercent() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.budget <= 0 {
		return 0
	}
	return float64(c.current) / float64(c.budget)
}

// Stats returns a snapshot of component memory stats.
func (c *ComponentMemory) Stats() ComponentMemoryStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return ComponentMemoryStats{
		Name:         c.name,
		Budget:       c.budget,
		Current:      c.current,
		Peak:         c.peak,
		UsagePercent: float64(c.current) / float64(c.budget),
		LastUpdated:  c.lastUpdated,
	}
}

// ComponentMemoryStats is a snapshot of component memory.
type ComponentMemoryStats struct {
	Name         ComponentName `json:"name"`
	Budget       int64         `json:"budget"`
	Current      int64         `json:"current"`
	Peak         int64         `json:"peak"`
	UsagePercent float64       `json:"usage_percent"`
	LastUpdated  time.Time     `json:"last_updated"`
}

// =============================================================================
// Memory Monitor Configuration
// =============================================================================

// MemoryMonitorConfig configures the memory monitor.
type MemoryMonitorConfig struct {
	// Per-component budgets
	QueryCacheBudget   int64
	StagingBudget      int64
	AgentContextBudget int64
	WALBudget          int64

	// Global settings
	GlobalCeilingPercent float64

	// Thresholds
	ComponentLRUThreshold    float64
	ComponentWarnThreshold   float64
	GlobalPauseThreshold     float64
	GlobalEmergencyThreshold float64

	// Monitoring
	MonitorInterval time.Duration

	// Signal publisher
	SignalPublisher SignalPublisher
}

// DefaultMemoryMonitorConfig returns default configuration.
func DefaultMemoryMonitorConfig() MemoryMonitorConfig {
	return MemoryMonitorConfig{
		QueryCacheBudget:         DefaultQueryCacheBudget,
		StagingBudget:            DefaultStagingBudget,
		AgentContextBudget:       DefaultAgentContextBudget,
		WALBudget:                DefaultWALBudget,
		GlobalCeilingPercent:     DefaultGlobalCeilingPercent,
		ComponentLRUThreshold:    DefaultComponentLRUThreshold,
		ComponentWarnThreshold:   DefaultComponentWarnThreshold,
		GlobalPauseThreshold:     DefaultGlobalPauseThreshold,
		GlobalEmergencyThreshold: DefaultGlobalEmergencyThreshold,
		MonitorInterval:          DefaultMonitorInterval,
		SignalPublisher:          &NoOpSignalPublisher{},
	}
}

// =============================================================================
// Memory Monitor
// =============================================================================

// MemoryMonitor tracks memory across all components.
type MemoryMonitor struct {
	mu     sync.RWMutex
	config MemoryMonitorConfig

	components    map[ComponentName]*ComponentMemory
	globalCeiling int64
	systemMemory  int64

	// State tracking
	pipelinesPaused atomic.Bool
	allPaused       atomic.Bool
	pressureLevel   atomic.Int32

	// Lifecycle
	stopCh chan struct{}
	done   chan struct{}
}

// NewMemoryMonitor creates a new memory monitor.
func NewMemoryMonitor(config MemoryMonitorConfig) *MemoryMonitor {
	if config.SignalPublisher == nil {
		config.SignalPublisher = &NoOpSignalPublisher{}
	}

	m := &MemoryMonitor{
		config:     config,
		components: make(map[ComponentName]*ComponentMemory),
		stopCh:     make(chan struct{}),
		done:       make(chan struct{}),
	}

	m.initComponents()
	m.updateSystemMemory()

	go m.monitorLoop()

	return m
}

// initComponents initializes all component trackers.
func (m *MemoryMonitor) initComponents() {
	m.components[ComponentQueryCache] = NewComponentMemory(ComponentQueryCache, m.config.QueryCacheBudget)
	m.components[ComponentStaging] = NewComponentMemory(ComponentStaging, m.config.StagingBudget)
	m.components[ComponentAgentContext] = NewComponentMemory(ComponentAgentContext, m.config.AgentContextBudget)
	m.components[ComponentWAL] = NewComponentMemory(ComponentWAL, m.config.WALBudget)
}

// updateSystemMemory queries system memory and updates ceiling.
func (m *MemoryMonitor) updateSystemMemory() {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.systemMemory = int64(memStats.Sys)
	m.globalCeiling = int64(float64(m.systemMemory) * m.config.GlobalCeilingPercent)
}

// =============================================================================
// Component Access
// =============================================================================

// GetComponent returns a component's memory tracker.
func (m *MemoryMonitor) GetComponent(name ComponentName) *ComponentMemory {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.components[name]
}

// AddUsage adds memory usage to a component.
func (m *MemoryMonitor) AddUsage(name ComponentName, bytes int64) int64 {
	comp := m.GetComponent(name)
	if comp == nil {
		return 0
	}
	return comp.Add(bytes)
}

// RemoveUsage removes memory usage from a component.
func (m *MemoryMonitor) RemoveUsage(name ComponentName, bytes int64) int64 {
	comp := m.GetComponent(name)
	if comp == nil {
		return 0
	}
	return comp.Remove(bytes)
}

// SetComponentBudget updates a component's budget.
func (m *MemoryMonitor) SetComponentBudget(name ComponentName, budget int64) {
	comp := m.GetComponent(name)
	if comp != nil {
		comp.SetBudget(budget)
	}
}

// =============================================================================
// Global State
// =============================================================================

// GlobalUsage returns total memory usage across all components.
func (m *MemoryMonitor) GlobalUsage() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var total int64
	for _, comp := range m.components {
		total += comp.Current()
	}
	return total
}

// GlobalCeiling returns the global memory ceiling.
func (m *MemoryMonitor) GlobalCeiling() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.globalCeiling
}

// GlobalUsagePercent returns global usage as percentage of ceiling.
func (m *MemoryMonitor) GlobalUsagePercent() float64 {
	m.mu.RLock()
	ceiling := m.globalCeiling
	m.mu.RUnlock()

	if ceiling <= 0 {
		return 0
	}
	return float64(m.GlobalUsage()) / float64(ceiling)
}

// PipelinesPaused returns whether pipelines are paused.
func (m *MemoryMonitor) PipelinesPaused() bool {
	return m.pipelinesPaused.Load()
}

// AllPaused returns whether all operations are paused.
func (m *MemoryMonitor) AllPaused() bool {
	return m.allPaused.Load()
}

// PressureLevel returns current memory pressure level.
func (m *MemoryMonitor) PressureLevel() MemoryPressureLevel {
	return MemoryPressureLevel(m.pressureLevel.Load())
}

// =============================================================================
// Statistics
// =============================================================================

// Stats returns a snapshot of all memory statistics.
func (m *MemoryMonitor) Stats() MemoryMonitorStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := MemoryMonitorStats{
		GlobalUsage:     m.GlobalUsage(),
		GlobalCeiling:   m.globalCeiling,
		SystemMemory:    m.systemMemory,
		PipelinesPaused: m.pipelinesPaused.Load(),
		AllPaused:       m.allPaused.Load(),
		PressureLevel:   MemoryPressureLevel(m.pressureLevel.Load()),
		Components:      make(map[ComponentName]ComponentMemoryStats),
	}

	for name, comp := range m.components {
		stats.Components[name] = comp.Stats()
	}

	return stats
}

// MemoryMonitorStats is a snapshot of monitor state.
type MemoryMonitorStats struct {
	GlobalUsage     int64                                  `json:"global_usage"`
	GlobalCeiling   int64                                  `json:"global_ceiling"`
	SystemMemory    int64                                  `json:"system_memory"`
	PipelinesPaused bool                                   `json:"pipelines_paused"`
	AllPaused       bool                                   `json:"all_paused"`
	PressureLevel   MemoryPressureLevel                    `json:"pressure_level"`
	Components      map[ComponentName]ComponentMemoryStats `json:"components"`
}

// =============================================================================
// Monitoring Loop
// =============================================================================

// monitorLoop runs the background monitoring goroutine.
func (m *MemoryMonitor) monitorLoop() {
	defer close(m.done)

	ticker := time.NewTicker(m.config.MonitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.checkMemoryPressure()
		}
	}
}

// checkMemoryPressure evaluates memory state and triggers actions.
func (m *MemoryMonitor) checkMemoryPressure() {
	m.updateSystemMemory()
	m.checkGlobalPressure()
	m.checkComponentPressure()
}

// checkGlobalPressure checks and handles global memory pressure.
func (m *MemoryMonitor) checkGlobalPressure() {
	globalPercent := m.GlobalUsagePercent()
	globalUsage := m.GlobalUsage()

	m.mu.RLock()
	ceiling := m.globalCeiling
	m.mu.RUnlock()

	payload := m.createPayload("", globalUsage, 0, ceiling)

	if globalPercent >= m.config.GlobalEmergencyThreshold {
		m.handleEmergencyPressure(payload)
		return
	}

	if globalPercent >= m.config.GlobalPauseThreshold {
		m.handleGlobalPausePressure(payload)
		return
	}

	m.handleGlobalResume(payload)
}

// handleEmergencyPressure handles 95%+ global memory.
func (m *MemoryMonitor) handleEmergencyPressure(payload MemorySignalPayload) {
	payload.Level = MemoryPressureEmergency
	m.pressureLevel.Store(int32(MemoryPressureEmergency))
	m.pipelinesPaused.Store(true)
	m.allPaused.Store(true)
	_ = m.config.SignalPublisher.PublishMemorySignal(SignalGlobalEmergency, payload)
}

// handleGlobalPausePressure handles 80%+ global memory.
func (m *MemoryMonitor) handleGlobalPausePressure(payload MemorySignalPayload) {
	payload.Level = MemoryPressureCritical
	m.pressureLevel.Store(int32(MemoryPressureCritical))
	m.pipelinesPaused.Store(true)
	m.allPaused.Store(false)
	_ = m.config.SignalPublisher.PublishMemorySignal(SignalGlobalPause, payload)
}

// handleGlobalResume handles return to normal global memory.
func (m *MemoryMonitor) handleGlobalResume(payload MemorySignalPayload) {
	wasPaused := m.pipelinesPaused.Load() || m.allPaused.Load()
	m.pipelinesPaused.Store(false)
	m.allPaused.Store(false)

	if wasPaused {
		payload.Level = MemoryPressureNone
		_ = m.config.SignalPublisher.PublishMemorySignal(SignalGlobalResume, payload)
	}
}

// checkComponentPressure checks all components for pressure.
func (m *MemoryMonitor) checkComponentPressure() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, comp := range m.components {
		m.checkSingleComponentPressure(comp)
	}
}

// checkSingleComponentPressure checks one component.
func (m *MemoryMonitor) checkSingleComponentPressure(comp *ComponentMemory) {
	percent := comp.UsagePercent()
	stats := comp.Stats()

	payload := m.createPayload(comp.Name(), stats.Current, stats.Budget, m.globalCeiling)

	if percent >= m.config.ComponentWarnThreshold {
		m.handleComponentAggressivePressure(payload)
		return
	}

	if percent >= m.config.ComponentLRUThreshold {
		m.handleComponentLRUPressure(payload)
	}
}

// handleComponentAggressivePressure handles 90%+ component memory.
func (m *MemoryMonitor) handleComponentAggressivePressure(payload MemorySignalPayload) {
	payload.Level = MemoryPressureHigh
	_ = m.config.SignalPublisher.PublishMemorySignal(SignalComponentAggressive, payload)
}

// handleComponentLRUPressure handles 70%+ component memory.
func (m *MemoryMonitor) handleComponentLRUPressure(payload MemorySignalPayload) {
	payload.Level = MemoryPressureLow
	_ = m.config.SignalPublisher.PublishMemorySignal(SignalComponentLRU, payload)
}

// createPayload creates a signal payload.
func (m *MemoryMonitor) createPayload(comp ComponentName, current, budget, ceiling int64) MemorySignalPayload {
	return MemorySignalPayload{
		Component:     comp,
		CurrentBytes:  current,
		BudgetBytes:   budget,
		GlobalBytes:   m.GlobalUsage(),
		GlobalCeiling: ceiling,
		Timestamp:     time.Now(),
	}
}

// =============================================================================
// Lifecycle
// =============================================================================

// Close stops the memory monitor.
func (m *MemoryMonitor) Close() error {
	close(m.stopCh)
	<-m.done
	return nil
}
