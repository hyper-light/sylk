package resources

import (
	"sort"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/signal"
	"github.com/google/uuid"
)

type ActivePipelineProvider interface {
	GetActiveSortedByPriority() []PipelineInfo
}

type PipelineInfo struct {
	PipelineID string
	SessionID  string
	Priority   concurrency.PipelinePriority
}

type PausedPipeline struct {
	ID        string
	SessionID string
	Priority  concurrency.PipelinePriority
	PausedAt  time.Time
}

type PipelineSuspender struct {
	provider ActivePipelineProvider
	bus      *signal.SignalBus

	mu     sync.RWMutex
	paused map[string]*PausedPipeline
}

func NewPipelineSuspender(provider ActivePipelineProvider, bus *signal.SignalBus) *PipelineSuspender {
	return &PipelineSuspender{
		provider: provider,
		bus:      bus,
		paused:   make(map[string]*PausedPipeline),
	}
}

func (ps *PipelineSuspender) PauseLowestPriority() *PausedPipeline {
	active := ps.provider.GetActiveSortedByPriority()
	if len(active) == 0 {
		return nil
	}

	ps.mu.Lock()
	target := ps.findLowestPriorityLocked(active)
	if target == nil {
		ps.mu.Unlock()
		return nil
	}

	paused := ps.pausePipelineLocked(target)
	ps.mu.Unlock()

	ps.sendPauseSignal(paused.ID)
	return paused
}

func (ps *PipelineSuspender) findLowestPriorityLocked(active []PipelineInfo) *PipelineInfo {
	for i := range active {
		if _, exists := ps.paused[active[i].PipelineID]; !exists {
			return &active[i]
		}
	}
	return nil
}

func (ps *PipelineSuspender) pausePipelineLocked(info *PipelineInfo) *PausedPipeline {
	paused := &PausedPipeline{
		ID:        info.PipelineID,
		SessionID: info.SessionID,
		Priority:  info.Priority,
		PausedAt:  time.Now(),
	}
	ps.paused[info.PipelineID] = paused
	return paused
}

func (ps *PipelineSuspender) sendPauseSignal(pipelineID string) {
	msg := signal.SignalMessage{
		ID:          uuid.New().String(),
		Signal:      signal.PausePipeline,
		TargetID:    pipelineID,
		RequiresAck: true,
		SentAt:      time.Now(),
	}
	_ = ps.bus.Broadcast(msg)
}

func (ps *PipelineSuspender) ResumeAll() {
	ps.mu.Lock()
	toResume := make([]*PausedPipeline, 0, len(ps.paused))
	for _, p := range ps.paused {
		toResume = append(toResume, p)
	}
	ps.paused = make(map[string]*PausedPipeline)
	ps.mu.Unlock()

	for _, p := range toResume {
		ps.sendResumeSignal(p.ID)
	}
}

func (ps *PipelineSuspender) ResumeHighestPriority() *PausedPipeline {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if len(ps.paused) == 0 {
		return nil
	}

	highest := findHighestPriority(ps.paused)
	delete(ps.paused, highest.ID)

	ps.sendResumeSignal(highest.ID)
	return highest
}

func findHighestPriority(paused map[string]*PausedPipeline) *PausedPipeline {
	sorted := make([]*PausedPipeline, 0, len(paused))
	for _, p := range paused {
		sorted = append(sorted, p)
	}

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority > sorted[j].Priority
	})

	return sorted[0]
}

func (ps *PipelineSuspender) sendResumeSignal(pipelineID string) {
	msg := signal.SignalMessage{
		ID:          uuid.New().String(),
		Signal:      signal.ResumePipeline,
		TargetID:    pipelineID,
		RequiresAck: false,
		SentAt:      time.Now(),
	}
	_ = ps.bus.Broadcast(msg)
}

func (ps *PipelineSuspender) PausedCount() int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return len(ps.paused)
}

func (ps *PipelineSuspender) IsPaused(pipelineID string) bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.isPausedInternal(pipelineID)
}

func (ps *PipelineSuspender) isPausedInternal(pipelineID string) bool {
	_, exists := ps.paused[pipelineID]
	return exists
}
