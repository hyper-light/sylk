package concurrency

import (
	"context"
	"errors"
	"runtime"
	"sync"
)

var (
	ErrPipelineNotFound      = errors.New("pipeline not found")
	ErrCircularDependency    = errors.New("circular dependency detected")
	ErrSchedulerClosed       = errors.New("scheduler is closed")
	ErrPipelineAlreadyExists = errors.New("pipeline already exists in scheduler")
)

type SchedulerConfig struct {
	MaxConcurrent int
}

func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		MaxConcurrent: runtime.NumCPU(),
	}
}

type PipelineScheduler struct {
	config    SchedulerConfig
	active    map[string]*SchedulablePipeline
	ready     *PipelinePriorityQueue
	waiting   map[string]*SchedulablePipeline
	completed map[string]bool
	closed    bool
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
}

// NewPipelineScheduler creates a new scheduler with a background context.
// For context propagation, use NewPipelineSchedulerWithContext instead.
func NewPipelineScheduler(config SchedulerConfig) *PipelineScheduler {
	return NewPipelineSchedulerWithContext(context.Background(), config)
}

// NewPipelineSchedulerWithContext creates a new scheduler with the given parent context.
// All pipelines started by this scheduler will respect the parent context's cancellation.
func NewPipelineSchedulerWithContext(ctx context.Context, config SchedulerConfig) *PipelineScheduler {
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = runtime.NumCPU()
	}

	schedCtx, cancel := context.WithCancel(ctx)
	return &PipelineScheduler{
		config:    config,
		active:    make(map[string]*SchedulablePipeline),
		ready:     NewPipelinePriorityQueue(),
		waiting:   make(map[string]*SchedulablePipeline),
		completed: make(map[string]bool),
		ctx:       schedCtx,
		cancel:    cancel,
	}
}

func (s *PipelineScheduler) Schedule(p *SchedulablePipeline) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrSchedulerClosed
	}

	if s.pipelineExists(p.ID) {
		return ErrPipelineAlreadyExists
	}

	if err := s.detectCircularDependency(p); err != nil {
		return err
	}

	return s.enqueuePipeline(p)
}

func (s *PipelineScheduler) pipelineExists(id string) bool {
	if _, ok := s.active[id]; ok {
		return true
	}
	if _, ok := s.waiting[id]; ok {
		return true
	}
	return s.ready.Contains(id)
}

func (s *PipelineScheduler) detectCircularDependency(p *SchedulablePipeline) error {
	visited := make(map[string]bool)
	inStack := make(map[string]bool)
	return s.checkCycle(p.ID, p, visited, inStack)
}

func (s *PipelineScheduler) checkCycle(id string, p *SchedulablePipeline, visited, inStack map[string]bool) error {
	if inStack[id] {
		return ErrCircularDependency
	}
	if visited[id] {
		return nil
	}

	visited[id] = true
	inStack[id] = true
	defer func() { inStack[id] = false }()

	return s.checkDependencies(p.Dependencies, visited, inStack)
}

func (s *PipelineScheduler) checkDependencies(deps []string, visited, inStack map[string]bool) error {
	for _, depID := range deps {
		if err := s.checkDependencyCycle(depID, visited, inStack); err != nil {
			return err
		}
	}
	return nil
}

func (s *PipelineScheduler) checkDependencyCycle(depID string, visited, inStack map[string]bool) error {
	if inStack[depID] {
		return ErrCircularDependency
	}

	wp, ok := s.waiting[depID]
	if !ok {
		return nil
	}
	return s.checkCycle(depID, wp, visited, inStack)
}

func (s *PipelineScheduler) enqueuePipeline(p *SchedulablePipeline) error {
	if s.hasPendingDependencies(p) {
		s.waiting[p.ID] = p
		return nil
	}

	s.ready.Push(p)
	s.tryScheduleNext()
	return nil
}

func (s *PipelineScheduler) hasPendingDependencies(p *SchedulablePipeline) bool {
	for _, depID := range p.Dependencies {
		if !s.completed[depID] {
			return true
		}
	}
	return false
}

func (s *PipelineScheduler) tryScheduleNext() {
	for len(s.active) < s.config.MaxConcurrent {
		next := s.ready.Pop()
		if next == nil {
			return
		}
		s.startPipeline(next)
	}
}

func (s *PipelineScheduler) startPipeline(p *SchedulablePipeline) {
	s.active[p.ID] = p
	if p.Runner != nil {
		// Create child context for this pipeline that inherits from scheduler's context
		pipelineCtx, cancel := context.WithCancel(s.ctx)
		p.cancel = cancel
		go p.Runner.Start(pipelineCtx)
	}
}

func (s *PipelineScheduler) NotifyComplete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrSchedulerClosed
	}

	p, ok := s.active[id]
	if !ok {
		return ErrPipelineNotFound
	}

	delete(s.active, id)
	s.completed[id] = true
	s.onPipelineComplete(p)
	return nil
}

func (s *PipelineScheduler) onPipelineComplete(_ *SchedulablePipeline) {
	s.promoteWaitingPipelines()
	s.tryScheduleNext()
}

func (s *PipelineScheduler) promoteWaitingPipelines() {
	for id, wp := range s.waiting {
		if !s.hasPendingDependencies(wp) {
			delete(s.waiting, id)
			s.ready.Push(wp)
		}
	}
}

func (s *PipelineScheduler) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrSchedulerClosed
	}

	return s.cancelPipeline(id)
}

func (s *PipelineScheduler) cancelPipeline(id string) error {
	if s.cancelActive(id) || s.cancelReady(id) || s.cancelWaiting(id) {
		return nil
	}
	return ErrPipelineNotFound
}

func (s *PipelineScheduler) cancelActive(id string) bool {
	p, ok := s.active[id]
	if !ok {
		return false
	}
	// Cancel the pipeline's context first if available
	if p.cancel != nil {
		p.cancel()
	}
	if p.Runner != nil {
		p.Runner.Stop()
	}
	delete(s.active, id)
	s.tryScheduleNext()
	return true
}

func (s *PipelineScheduler) cancelReady(id string) bool {
	return s.ready.Remove(id)
}

func (s *PipelineScheduler) cancelWaiting(id string) bool {
	if _, ok := s.waiting[id]; ok {
		delete(s.waiting, id)
		return true
	}
	return false
}

func (s *PipelineScheduler) ActiveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.active)
}

func (s *PipelineScheduler) ReadyCount() int {
	return s.ready.Len()
}

func (s *PipelineScheduler) WaitingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.waiting)
}

func (s *PipelineScheduler) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true
	// Cancel the scheduler context, which propagates to all pipelines
	if s.cancel != nil {
		s.cancel()
	}
	s.cancelAllActive()
}

func (s *PipelineScheduler) cancelAllActive() {
	for id, p := range s.active {
		// Cancel the pipeline's context first if available
		if p.cancel != nil {
			p.cancel()
		}
		if p.Runner != nil {
			p.Runner.Stop()
		}
		delete(s.active, id)
	}
}

func (s *PipelineScheduler) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
