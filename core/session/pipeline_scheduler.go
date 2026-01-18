package session

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

var (
	ErrSchedulerClosed     = errors.New("pipeline scheduler is closed")
	ErrPipelineNotFound    = errors.New("pipeline not found")
	ErrNoSlotsAvailable    = errors.New("no pipeline slots available")
	ErrSessionQuotaReached = errors.New("session pipeline quota reached")
)

const (
	SignalPipelineScheduled SignalType = "pipeline_scheduled"
	SignalPipelineCompleted SignalType = "pipeline_completed"
	SignalSlotsRebalanced   SignalType = "slots_rebalanced"
)

type PipelineSchedulerConfig struct {
	MaxConcurrent    int
	FairShare        *FairShareCalculator
	SignalDispatcher *CrossSessionSignalDispatcher
	Registry         *Registry
}

type GlobalPipelineScheduler struct {
	maxConcurrent int
	totalActive   int

	sessionActive map[string]int
	sessionQueued map[string]*concurrency.PipelinePriorityQueue

	fairShare        *FairShareCalculator
	signalDispatcher *CrossSessionSignalDispatcher
	registry         *Registry

	mu     sync.Mutex
	closed bool
}

type ScheduledPipelineInfo struct {
	PipelineID string
	SessionID  string
	Priority   concurrency.PipelinePriority
	SpawnTime  time.Time
	Status     string
}

type pipelineUpdate struct {
	sessionID string
	running   int
	queued    int
}

func NewGlobalPipelineScheduler(cfg PipelineSchedulerConfig) *GlobalPipelineScheduler {
	maxConcurrent := cfg.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = runtime.NumCPU()
	}

	return &GlobalPipelineScheduler{
		maxConcurrent:    maxConcurrent,
		sessionActive:    make(map[string]int),
		sessionQueued:    make(map[string]*concurrency.PipelinePriorityQueue),
		fairShare:        cfg.FairShare,
		signalDispatcher: cfg.SignalDispatcher,
		registry:         cfg.Registry,
	}
}

func (s *GlobalPipelineScheduler) Schedule(ctx context.Context, sessionID string, p *concurrency.SchedulablePipeline) (bool, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false, ErrSchedulerClosed
	}

	s.ensureSessionQueue(sessionID)
	scheduled := s.tryScheduleImmediate(sessionID, p)

	if !scheduled {
		s.queuePipeline(sessionID, p)
	}

	running := s.sessionActive[sessionID]
	queued := s.queueLength(sessionID)
	s.mu.Unlock()

	s.notifyScheduled(sessionID, p, scheduled)
	s.heartbeatSession(sessionID, running, queued)
	return scheduled, nil
}

func (s *GlobalPipelineScheduler) ensureSessionQueue(sessionID string) {
	if s.sessionQueued[sessionID] == nil {
		s.sessionQueued[sessionID] = concurrency.NewPipelinePriorityQueue()
	}
}

func (s *GlobalPipelineScheduler) tryScheduleImmediate(sessionID string, p *concurrency.SchedulablePipeline) bool {
	if p == nil {
		return false
	}
	if !s.hasGlobalCapacity() {
		return false
	}

	if !s.hasSessionQuota(sessionID) {
		return false
	}

	s.sessionActive[sessionID]++
	s.totalActive++
	return true
}

func (s *GlobalPipelineScheduler) hasGlobalCapacity() bool {
	return s.totalActive < s.maxConcurrent
}

func (s *GlobalPipelineScheduler) hasSessionQuota(sessionID string) bool {
	quota := s.getSessionQuota(sessionID)
	return s.sessionActive[sessionID] < quota
}

func (s *GlobalPipelineScheduler) getSessionQuota(sessionID string) int {
	if s.fairShare == nil {
		return s.maxConcurrent
	}

	totals := ResourceTotals{SubprocessSlots: s.maxConcurrent}
	allocations, err := s.fairShare.Calculate(totals)
	if err != nil {
		return 1
	}

	alloc, exists := allocations[sessionID]
	if !exists {
		return 1
	}
	return maxInt(1, alloc.SubprocessSlots)
}

func (s *GlobalPipelineScheduler) queuePipeline(sessionID string, p *concurrency.SchedulablePipeline) {
	s.sessionQueued[sessionID].Push(p)
}

func (s *GlobalPipelineScheduler) queueLength(sessionID string) int {
	queue := s.sessionQueued[sessionID]
	if queue == nil {
		return 0
	}
	return queue.Len()
}

func (s *GlobalPipelineScheduler) heartbeatSession(sessionID string, running, queued int) {
	if s.registry == nil {
		return
	}
	if running == 0 && queued == 0 {
		s.registry.Touch(sessionID)
		return
	}
	s.registry.Heartbeat(sessionID, running, queued)
}

func (s *GlobalPipelineScheduler) applyPipelineUpdates(updates []pipelineUpdate) {
	for _, update := range updates {
		s.heartbeatSession(update.sessionID, update.running, update.queued)
	}
}

func (s *GlobalPipelineScheduler) notifyScheduled(sessionID string, p *concurrency.SchedulablePipeline, immediate bool) {
	if s.signalDispatcher == nil {
		return
	}

	status := "queued"
	if immediate {
		status = "running"
	}

	payload, _ := json.Marshal(map[string]any{
		"pipeline_id": p.ID,
		"session_id":  sessionID,
		"status":      status,
	})

	s.signalDispatcher.SendSignal(CrossSessionSignal{
		Type:      SignalPipelineScheduled,
		ToSession: sessionID,
		Timestamp: time.Now(),
		Payload:   string(payload),
	})
}

func (s *GlobalPipelineScheduler) OnPipelineComplete(sessionID string, pipelineID string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSchedulerClosed
	}

	s.decrementActive(sessionID)
	updates := s.dispatchWaiting()
	running := s.sessionActive[sessionID]
	queued := s.queueLength(sessionID)
	s.mu.Unlock()

	s.notifyCompleted(sessionID, pipelineID)
	s.heartbeatSession(sessionID, running, queued)
	s.applyPipelineUpdates(updates)

	return nil
}

func (s *GlobalPipelineScheduler) decrementActive(sessionID string) {
	if s.sessionActive[sessionID] > 0 {
		s.sessionActive[sessionID]--
	}
	if s.totalActive > 0 {
		s.totalActive--
	}
}

func (s *GlobalPipelineScheduler) notifyCompleted(sessionID string, pipelineID string) {
	if s.signalDispatcher == nil {
		return
	}

	payload, _ := json.Marshal(map[string]string{
		"pipeline_id": pipelineID,
		"session_id":  sessionID,
	})

	s.signalDispatcher.SendSignal(CrossSessionSignal{
		Type:      SignalPipelineCompleted,
		ToSession: sessionID,
		Timestamp: time.Now(),
		Payload:   string(payload),
	})
}

func (s *GlobalPipelineScheduler) dispatchWaiting() []pipelineUpdate {
	var updates []pipelineUpdate
	for sessionID := range s.sessionQueued {
		update, dispatched := s.tryDispatchFromSession(sessionID)
		if dispatched {
			updates = append(updates, update)
			break
		}
	}
	return updates
}

func (s *GlobalPipelineScheduler) tryDispatchFromSession(sessionID string) (pipelineUpdate, bool) {
	if !s.canDispatchToSession(sessionID) {
		return pipelineUpdate{}, false
	}

	if !s.hasQueuedPipelines(sessionID) {
		return pipelineUpdate{}, false
	}

	s.sessionQueued[sessionID].Pop()
	s.sessionActive[sessionID]++
	s.totalActive++

	return pipelineUpdate{
		sessionID: sessionID,
		running:   s.sessionActive[sessionID],
		queued:    s.queueLength(sessionID),
	}, true
}

func (s *GlobalPipelineScheduler) canDispatchToSession(sessionID string) bool {
	return s.hasGlobalCapacity() && s.hasSessionQuota(sessionID)
}

func (s *GlobalPipelineScheduler) hasQueuedPipelines(sessionID string) bool {
	queue := s.sessionQueued[sessionID]
	return queue != nil && queue.Len() > 0
}

func (s *GlobalPipelineScheduler) GetSessionStats(sessionID string) *SessionPipelineStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	active := s.sessionActive[sessionID]
	queued := 0
	if queue := s.sessionQueued[sessionID]; queue != nil {
		queued = queue.Len()
	}

	return &SessionPipelineStats{
		SessionID:   sessionID,
		Active:      active,
		Queued:      queued,
		Quota:       s.getSessionQuota(sessionID),
		GlobalTotal: s.totalActive,
		GlobalMax:   s.maxConcurrent,
	}
}

type SessionPipelineStats struct {
	SessionID   string
	Active      int
	Queued      int
	Quota       int
	GlobalTotal int
	GlobalMax   int
}

func (s *GlobalPipelineScheduler) GetGlobalStats() *GlobalPipelineStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessionCount := len(s.sessionActive)
	totalQueued := 0

	for _, queue := range s.sessionQueued {
		if queue != nil {
			totalQueued += queue.Len()
		}
	}

	return &GlobalPipelineStats{
		TotalActive:   s.totalActive,
		MaxConcurrent: s.maxConcurrent,
		SessionCount:  sessionCount,
		TotalQueued:   totalQueued,
	}
}

type GlobalPipelineStats struct {
	TotalActive   int
	MaxConcurrent int
	SessionCount  int
	TotalQueued   int
}

func (s *GlobalPipelineScheduler) CancelPipeline(sessionID string, pipelineID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return false
	}

	queue := s.sessionQueued[sessionID]
	if queue == nil {
		return false
	}

	return queue.Remove(pipelineID)
}

func (s *GlobalPipelineScheduler) Rebalance() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	s.dispatchWaiting()
	s.notifyRebalanced()
}

func (s *GlobalPipelineScheduler) notifyRebalanced() {
	if s.signalDispatcher == nil {
		return
	}

	payload, _ := json.Marshal(map[string]int{
		"total_active": s.totalActive,
		"max":          s.maxConcurrent,
	})

	s.signalDispatcher.SendSignal(CrossSessionSignal{
		Type:      SignalSlotsRebalanced,
		Timestamp: time.Now(),
		Payload:   string(payload),
	})
}

type ActivePipelineInfo struct {
	PipelineID string
	SessionID  string
	Priority   concurrency.PipelinePriority
}

func (s *GlobalPipelineScheduler) GetActivePipelines() []ActivePipelineInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	var active []ActivePipelineInfo
	for sessionID, queue := range s.sessionQueued {
		items := queue.All()
		for _, p := range items {
			active = append(active, ActivePipelineInfo{
				PipelineID: p.ID,
				SessionID:  sessionID,
				Priority:   p.Priority,
			})
		}
	}

	sortActivePipelinesByPriorityAsc(active)
	return active
}

func sortActivePipelinesByPriorityAsc(pipelines []ActivePipelineInfo) {
	for i := 1; i < len(pipelines); i++ {
		for j := i; j > 0 && pipelines[j].Priority < pipelines[j-1].Priority; j-- {
			pipelines[j], pipelines[j-1] = pipelines[j-1], pipelines[j]
		}
	}
}

func (s *GlobalPipelineScheduler) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrSchedulerClosed
	}

	s.closed = true
	s.sessionActive = nil
	s.sessionQueued = nil

	return nil
}
