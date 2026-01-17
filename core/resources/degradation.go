package resources

import (
	"sort"
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

type PipelineCheckpointer interface {
	Checkpoint(pipelineID string) error
}

type PipelineResourceReleaser interface {
	ReleaseBundleByID(pipelineID string) error
}

type UserNotifier interface {
	NotifyPaused(pipelineID string, reason string)
	NotifyResumed(pipelineID string)
}

type DegradationConfig struct {
	WarningThreshold   float64
	CriticalThreshold  float64
	EmergencyThreshold float64
	CheckInterval      time.Duration
	ResumeHysteresis   float64
	ResumeInterval     time.Duration
}

func DefaultDegradationConfig() DegradationConfig {
	return DegradationConfig{
		WarningThreshold:   0.70,
		CriticalThreshold:  0.85,
		EmergencyThreshold: 0.95,
		CheckInterval:      1 * time.Second,
		ResumeHysteresis:   0.10,
		ResumeInterval:     100 * time.Millisecond,
	}
}

type PipelineEntry struct {
	ID                string
	Priority          PipelinePriority
	State             atomic.Int32
	IsUserInteractive bool
	CreatedAt         time.Time
	PausedAt          time.Time
	ResumedAt         time.Time
	PauseCount        int64
}

func (p *PipelineEntry) GetState() PipelineState {
	return PipelineState(p.State.Load())
}

func (p *PipelineEntry) SetState(state PipelineState) {
	p.State.Store(int32(state))
}

type DegradationController struct {
	mu               sync.RWMutex
	config           DegradationConfig
	memoryMonitor    *MemoryMonitor
	signalBus        *signal.SignalBus
	checkpointer     PipelineCheckpointer
	resourceReleaser PipelineResourceReleaser
	userNotifier     UserNotifier
	pipelines        map[string]*PipelineEntry
	level            atomic.Int32
	stopCh           chan struct{}
	done             chan struct{}
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

func (dc *DegradationController) SetCheckpointer(cp PipelineCheckpointer) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.checkpointer = cp
}

func (dc *DegradationController) SetResourceReleaser(rr PipelineResourceReleaser) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.resourceReleaser = rr
}

func (dc *DegradationController) SetUserNotifier(un UserNotifier) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.userNotifier = un
}

func (dc *DegradationController) RegisterPipeline(id string, priority PipelinePriority) {
	dc.RegisterPipelineWithOptions(id, priority, false)
}
func (dc *DegradationController) RegisterPipelineWithOptions(
	id string,
	priority PipelinePriority,
	isUserInteractive bool,
) {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	dc.pipelines[id] = &PipelineEntry{
		ID:                id,
		Priority:          priority,
		IsUserInteractive: isUserInteractive,
		CreatedAt:         time.Now(),
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

	candidates := dc.selectPauseablePipelines(level)
	for _, entry := range candidates {
		dc.pausePipelineInternal(entry, "resource pressure")
	}
}

func (dc *DegradationController) selectPauseablePipelines(level DegradationLevel) []*PipelineEntry {
	threshold := dc.levelToPriority(level)
	candidates := make([]*PipelineEntry, 0)

	for _, entry := range dc.pipelines {
		if dc.shouldPause(entry, threshold) {
			candidates = append(candidates, entry)
		}
	}

	sortPipelinesForPause(candidates)
	return candidates
}

func (dc *DegradationController) shouldPause(entry *PipelineEntry, threshold PipelinePriority) bool {
	if entry.IsUserInteractive {
		return false
	}
	if entry.GetState() != StateRunning {
		return false
	}
	return entry.Priority < threshold
}

func sortPipelinesForPause(entries []*PipelineEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Priority != entries[j].Priority {
			return entries[i].Priority < entries[j].Priority
		}
		return entries[i].CreatedAt.After(entries[j].CreatedAt)
	})
}

func (dc *DegradationController) handleDeescalation(level DegradationLevel, usage float64) {
	resumeThreshold := dc.levelToResumeThreshold(level)
	if usage > resumeThreshold {
		return
	}

	dc.mu.Lock()
	defer dc.mu.Unlock()

	candidates := dc.selectResumablePipelines(level)
	dc.resumeGradually(candidates)
}

func (dc *DegradationController) selectResumablePipelines(level DegradationLevel) []*PipelineEntry {
	threshold := dc.levelToPriority(level)
	candidates := make([]*PipelineEntry, 0)

	for _, entry := range dc.pipelines {
		if dc.shouldResume(entry, threshold) {
			candidates = append(candidates, entry)
		}
	}

	sortPipelinesForResume(candidates)
	return candidates
}

func (dc *DegradationController) shouldResume(entry *PipelineEntry, threshold PipelinePriority) bool {
	if entry.GetState() != StatePaused {
		return false
	}
	return entry.Priority >= threshold
}

func sortPipelinesForResume(entries []*PipelineEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Priority != entries[j].Priority {
			return entries[i].Priority > entries[j].Priority
		}
		return entries[i].PausedAt.Before(entries[j].PausedAt)
	})
}

func (dc *DegradationController) resumeGradually(candidates []*PipelineEntry) {
	for i, entry := range candidates {
		dc.resumePipelineInternal(entry)
		if i < len(candidates)-1 && dc.config.ResumeInterval > 0 {
			dc.mu.Unlock()
			time.Sleep(dc.config.ResumeInterval)
			dc.mu.Lock()
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

func (dc *DegradationController) pausePipelineInternal(entry *PipelineEntry, reason string) {
	entry.SetState(StatePaused)
	entry.PausedAt = time.Now()
	entry.PauseCount++

	dc.sendPauseSignal(entry.ID)
	dc.checkpointPipeline(entry.ID)
	dc.releasePipelineResources(entry.ID)
	dc.notifyUserPaused(entry.ID, reason)
}

func (dc *DegradationController) resumePipelineInternal(entry *PipelineEntry) {
	entry.SetState(StateResuming)
	entry.ResumedAt = time.Now()

	dc.sendResumeSignal(entry.ID)
	dc.notifyUserResumed(entry.ID)
	entry.SetState(StateRunning)
}

func (dc *DegradationController) checkpointPipeline(pipelineID string) {
	if dc.checkpointer == nil {
		return
	}
	_ = dc.checkpointer.Checkpoint(pipelineID)
}

func (dc *DegradationController) releasePipelineResources(pipelineID string) {
	if dc.resourceReleaser == nil {
		return
	}
	_ = dc.resourceReleaser.ReleaseBundleByID(pipelineID)
}

func (dc *DegradationController) notifyUserPaused(pipelineID, reason string) {
	if dc.userNotifier == nil {
		return
	}
	dc.userNotifier.NotifyPaused(pipelineID, reason)
}

func (dc *DegradationController) notifyUserResumed(pipelineID string) {
	if dc.userNotifier == nil {
		return
	}
	dc.userNotifier.NotifyResumed(pipelineID)
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

	if entry.IsUserInteractive {
		return false
	}

	if entry.GetState() != StateRunning {
		return false
	}

	dc.pausePipelineInternal(entry, "manual pause")
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

	dc.resumePipelineInternal(entry)
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
			ID:                entry.ID,
			Priority:          entry.Priority,
			State:             entry.GetState(),
			IsUserInteractive: entry.IsUserInteractive,
			PauseCount:        entry.PauseCount,
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
	ID                string           `json:"id"`
	Priority          PipelinePriority `json:"priority"`
	State             PipelineState    `json:"state"`
	IsUserInteractive bool             `json:"is_user_interactive"`
	PauseCount        int64            `json:"pause_count"`
}

func (dc *DegradationController) Close() error {
	close(dc.stopCh)
	if dc.memoryMonitor != nil {
		<-dc.done
	}
	return nil
}
