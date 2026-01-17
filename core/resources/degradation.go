package resources

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/signal"
)

type PipelinePriority int

const (
	PriorityLow PipelinePriority = iota
	PriorityMedium
	PriorityHigh
	PriorityCritical
)

type PipelineState int32

const (
	StateRunning PipelineState = iota
	StatePaused
	StateResuming
)

type DegradationLevel int32

const (
	LevelNormal DegradationLevel = iota
	LevelWarning
	LevelCritical
	LevelEmergency
)

type DegradationConfig struct {
	WarningThreshold   float64
	CriticalThreshold  float64
	EmergencyThreshold float64
	CheckInterval      time.Duration
	ResumeHysteresis   float64
}

func DefaultDegradationConfig() DegradationConfig {
	return DegradationConfig{
		WarningThreshold:   0.70,
		CriticalThreshold:  0.85,
		EmergencyThreshold: 0.95,
		CheckInterval:      1 * time.Second,
		ResumeHysteresis:   0.10,
	}
}

type PipelineEntry struct {
	ID         string
	Priority   PipelinePriority
	State      atomic.Int32
	PausedAt   time.Time
	ResumedAt  time.Time
	PauseCount int64
}

func (p *PipelineEntry) GetState() PipelineState {
	return PipelineState(p.State.Load())
}

func (p *PipelineEntry) SetState(state PipelineState) {
	p.State.Store(int32(state))
}

type DegradationController struct {
	mu            sync.RWMutex
	config        DegradationConfig
	memoryMonitor *MemoryMonitor
	signalBus     *signal.SignalBus
	pipelines     map[string]*PipelineEntry
	level         atomic.Int32
	stopCh        chan struct{}
	done          chan struct{}
}

func NewDegradationController(
	config DegradationConfig,
	memMon *MemoryMonitor,
	bus *signal.SignalBus,
) *DegradationController {
	dc := &DegradationController{
		config:        config,
		memoryMonitor: memMon,
		signalBus:     bus,
		pipelines:     make(map[string]*PipelineEntry),
		stopCh:        make(chan struct{}),
		done:          make(chan struct{}),
	}

	if memMon != nil {
		go dc.monitorLoop()
	}

	return dc
}

func (dc *DegradationController) RegisterPipeline(id string, priority PipelinePriority) {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	dc.pipelines[id] = &PipelineEntry{
		ID:       id,
		Priority: priority,
	}
}

func (dc *DegradationController) UnregisterPipeline(id string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	delete(dc.pipelines, id)
}

func (dc *DegradationController) GetLevel() DegradationLevel {
	return DegradationLevel(dc.level.Load())
}

func (dc *DegradationController) GetPipelineState(id string) (PipelineState, bool) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	entry, ok := dc.pipelines[id]
	if !ok {
		return StateRunning, false
	}
	return entry.GetState(), true
}

func (dc *DegradationController) monitorLoop() {
	defer close(dc.done)

	ticker := time.NewTicker(dc.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-dc.stopCh:
			return
		case <-ticker.C:
			dc.evaluatePressure()
		}
	}
}

func (dc *DegradationController) evaluatePressure() {
	if dc.memoryMonitor == nil {
		return
	}

	usage := dc.memoryMonitor.GlobalUsagePercent()
	newLevel := dc.calculateLevel(usage)
	oldLevel := DegradationLevel(dc.level.Swap(int32(newLevel)))

	if newLevel > oldLevel {
		dc.handleEscalation(newLevel)
	} else if newLevel < oldLevel {
		dc.handleDeescalation(newLevel, usage)
	}
}

func (dc *DegradationController) calculateLevel(usage float64) DegradationLevel {
	switch {
	case usage >= dc.config.EmergencyThreshold:
		return LevelEmergency
	case usage >= dc.config.CriticalThreshold:
		return LevelCritical
	case usage >= dc.config.WarningThreshold:
		return LevelWarning
	default:
		return LevelNormal
	}
}

func (dc *DegradationController) handleEscalation(level DegradationLevel) {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	threshold := dc.levelToPriority(level)
	for _, entry := range dc.pipelines {
		if entry.Priority < threshold && entry.GetState() == StateRunning {
			dc.pausePipeline(entry)
		}
	}
}

func (dc *DegradationController) handleDeescalation(level DegradationLevel, usage float64) {
	resumeThreshold := dc.levelToResumeThreshold(level)
	if usage > resumeThreshold {
		return
	}

	dc.mu.Lock()
	defer dc.mu.Unlock()

	threshold := dc.levelToPriority(level)
	for _, entry := range dc.pipelines {
		if entry.Priority >= threshold && entry.GetState() == StatePaused {
			dc.resumePipeline(entry)
		}
	}
}

func (dc *DegradationController) levelToPriority(level DegradationLevel) PipelinePriority {
	switch level {
	case LevelEmergency:
		return PriorityCritical
	case LevelCritical:
		return PriorityHigh
	case LevelWarning:
		return PriorityMedium
	default:
		return PriorityLow
	}
}

func (dc *DegradationController) levelToResumeThreshold(level DegradationLevel) float64 {
	switch level {
	case LevelCritical:
		return dc.config.CriticalThreshold - dc.config.ResumeHysteresis
	case LevelWarning:
		return dc.config.WarningThreshold - dc.config.ResumeHysteresis
	default:
		return dc.config.WarningThreshold - dc.config.ResumeHysteresis
	}
}

func (dc *DegradationController) pausePipeline(entry *PipelineEntry) {
	entry.SetState(StatePaused)
	entry.PausedAt = time.Now()
	entry.PauseCount++

	dc.sendPauseSignal(entry.ID)
}

func (dc *DegradationController) resumePipeline(entry *PipelineEntry) {
	entry.SetState(StateResuming)
	entry.ResumedAt = time.Now()

	dc.sendResumeSignal(entry.ID)
	entry.SetState(StateRunning)
}

func (dc *DegradationController) sendPauseSignal(pipelineID string) {
	if dc.signalBus == nil {
		return
	}

	msg := signal.NewSignalMessage(signal.PausePipeline, pipelineID, false)
	msg.Reason = "resource pressure"
	_ = dc.signalBus.Broadcast(*msg)
}

func (dc *DegradationController) sendResumeSignal(pipelineID string) {
	if dc.signalBus == nil {
		return
	}

	msg := signal.NewSignalMessage(signal.ResumePipeline, pipelineID, false)
	msg.Reason = "resource pressure relieved"
	_ = dc.signalBus.Broadcast(*msg)
}

func (dc *DegradationController) PausePipeline(id string) bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	entry, ok := dc.pipelines[id]
	if !ok {
		return false
	}

	if entry.GetState() != StateRunning {
		return false
	}

	dc.pausePipeline(entry)
	return true
}

func (dc *DegradationController) ResumePipeline(id string) bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	entry, ok := dc.pipelines[id]
	if !ok {
		return false
	}

	if entry.GetState() != StatePaused {
		return false
	}

	dc.resumePipeline(entry)
	return true
}

func (dc *DegradationController) Stats() DegradationStats {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	stats := DegradationStats{
		Level:          dc.GetLevel(),
		TotalPipelines: len(dc.pipelines),
		Pipelines:      make([]PipelineStats, 0, len(dc.pipelines)),
	}

	for _, entry := range dc.pipelines {
		ps := PipelineStats{
			ID:         entry.ID,
			Priority:   entry.Priority,
			State:      entry.GetState(),
			PauseCount: entry.PauseCount,
		}
		if entry.GetState() == StatePaused {
			stats.PausedCount++
		} else {
			stats.RunningCount++
		}
		stats.Pipelines = append(stats.Pipelines, ps)
	}

	return stats
}

type DegradationStats struct {
	Level          DegradationLevel `json:"level"`
	TotalPipelines int              `json:"total_pipelines"`
	RunningCount   int              `json:"running_count"`
	PausedCount    int              `json:"paused_count"`
	Pipelines      []PipelineStats  `json:"pipelines"`
}

type PipelineStats struct {
	ID         string           `json:"id"`
	Priority   PipelinePriority `json:"priority"`
	State      PipelineState    `json:"state"`
	PauseCount int64            `json:"pause_count"`
}

func (dc *DegradationController) Close() error {
	close(dc.stopCh)
	if dc.memoryMonitor != nil {
		<-dc.done
	}
	return nil
}
