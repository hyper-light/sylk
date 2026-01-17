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
	SignalDispatcher *SignalDispatcher
}

type GlobalPipelineScheduler struct {
	maxConcurrent int
	totalActive   int

	sessionActive map[string]int
	sessionQueued map[string]*concurrency.PipelinePriorityQueue

	fairShare        *FairShareCalculator
	signalDispatcher *SignalDispatcher

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
	}
}

func (s *GlobalPipelineScheduler) Schedule(ctx context.Context, sessionID string, p *concurrency.SchedulablePipeline) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return false, ErrSchedulerClosed
	}

	s.ensureSessionQueue(sessionID)
	scheduled := s.tryScheduleImmediate(sessionID, p)

	if !scheduled {
		s.queuePipeline(sessionID, p)
	}

	s.notifyScheduled(sessionID, p, scheduled)
	return scheduled, nil
}

func (s *GlobalPipelineScheduler) ensureSessionQueue(sessionID string) {
	if s.sessionQueued[sessionID] == nil {
		s.sessionQueued[sessionID] = concurrency.NewPipelinePriorityQueue()
	}
}

func (s *GlobalPipelineScheduler) tryScheduleImmediate(sessionID string, p *concurrency.SchedulablePipeline) bool {
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

func (s *GlobalPipelineScheduler) notifyScheduled(sessionID string, p *concurrency.SchedulablePipeline, immediate bool) {
	if s.signalDispatcher == nil {
		return
	}

	status := "queued"
	if immediate {
		status = "running"
	}

	payload, _ := json.Marshal(map[string]interface{}{
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
	defer s.mu.Unlock()

	if s.closed {
		return ErrSchedulerClosed
	}

	s.decrementActive(sessionID)
	s.notifyCompleted(sessionID, pipelineID)
	s.dispatchWaiting()

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

func (s *GlobalPipelineScheduler) dispatchWaiting() {
	for sessionID := range s.sessionQueued {
		if s.tryDispatchFromSession(sessionID) {
			return
		}
	}
}

func (s *GlobalPipelineScheduler) tryDispatchFromSession(sessionID string) bool {
	if !s.canDispatchToSession(sessionID) {
		return false
	}

	if !s.hasQueuedPipelines(sessionID) {
		return false
	}

	s.sessionQueued[sessionID].Pop()
	s.sessionActive[sessionID]++
	s.totalActive++
	return true
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
