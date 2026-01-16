package dag

import (
	"context"
	"sync"
	"sync/atomic"
)

// =============================================================================
// Scheduler
// =============================================================================

// Scheduler manages multiple DAG executions
type Scheduler struct {
	mu sync.RWMutex

	// Active executions
	executions map[string]*Execution

	// Configuration
	maxConcurrentDAGs int

	// Semaphore for limiting concurrent DAGs
	dagSem chan struct{}

	// Default policy
	defaultPolicy ExecutionPolicy

	// Event handlers
	handlersMu sync.RWMutex
	handlers   []EventHandler

	// Closed
	closed atomic.Bool
}

// Execution represents an active DAG execution
type Execution struct {
	DAG      *DAG
	Executor *Executor
	Status   *DAGStatus
}

// SchedulerConfig configures the scheduler
type SchedulerConfig struct {
	// MaxConcurrentDAGs limits simultaneous DAG executions
	MaxConcurrentDAGs int

	// DefaultPolicy is the default execution policy
	DefaultPolicy ExecutionPolicy
}

// DefaultSchedulerConfig returns sensible defaults
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		MaxConcurrentDAGs: 10,
		DefaultPolicy:     DefaultExecutionPolicy(),
	}
}

// NewScheduler creates a new DAG scheduler
func NewScheduler(cfg SchedulerConfig) *Scheduler {
	if cfg.MaxConcurrentDAGs <= 0 {
		cfg.MaxConcurrentDAGs = 10
	}

	return &Scheduler{
		executions:        make(map[string]*Execution),
		maxConcurrentDAGs: cfg.MaxConcurrentDAGs,
		dagSem:            make(chan struct{}, cfg.MaxConcurrentDAGs),
		defaultPolicy:     cfg.DefaultPolicy,
		handlers:          make([]EventHandler, 0),
	}
}

// =============================================================================
// Execution Management
// =============================================================================

// Submit submits a DAG for execution
func (s *Scheduler) Submit(ctx context.Context, dag *DAG, dispatcher NodeDispatcher) (string, error) {
	if s.closed.Load() {
		return "", ErrExecutorClosed
	}

	// Acquire semaphore
	select {
	case s.dagSem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}

	// Validate DAG
	if !dag.IsValidated() {
		if err := ValidateDAG(dag); err != nil {
			<-s.dagSem
			return "", err
		}
	}

	// Create executor with DAG's policy or default
	policy := dag.Policy()
	if policy.MaxConcurrency == 0 {
		policy = s.defaultPolicy
	}

	executor := NewExecutor(policy)

	// Forward events
	executor.Subscribe(func(event *Event) {
		s.emitEvent(event)
	})

	// Register execution
	s.mu.Lock()
	s.executions[dag.ID()] = &Execution{
		DAG:      dag,
		Executor: executor,
	}
	s.mu.Unlock()

	// Execute in background
	go func() {
		defer func() {
			<-s.dagSem
		}()

		result, err := executor.Execute(ctx, dag, dispatcher)

		// Update execution status
		s.mu.Lock()
		if exec, ok := s.executions[dag.ID()]; ok {
			if result != nil {
				exec.Status = &DAGStatus{
					ID:             dag.ID(),
					State:          result.State,
					Progress:       1.0,
					NodesTotal:     dag.NodeCount(),
					NodesCompleted: result.NodesSucceeded + result.NodesFailed + result.NodesSkipped,
					StartTime:      result.StartTime,
				}
			}
		}
		s.mu.Unlock()

		// Emit result
		if err != nil {
			s.emitEvent(&Event{
				Type:      EventDAGFailed,
				DAGID:     dag.ID(),
				Timestamp: result.EndTime,
				Data: map[string]any{
					"error": err.Error(),
				},
			})
		}
	}()

	return dag.ID(), nil
}

// SubmitAndWait submits a DAG and waits for completion
func (s *Scheduler) SubmitAndWait(ctx context.Context, dag *DAG, dispatcher NodeDispatcher) (*DAGResult, error) {
	if s.closed.Load() {
		return nil, ErrExecutorClosed
	}

	// Acquire semaphore
	select {
	case s.dagSem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() {
		<-s.dagSem
	}()

	// Validate DAG
	if !dag.IsValidated() {
		if err := ValidateDAG(dag); err != nil {
			return nil, err
		}
	}

	// Create executor
	policy := dag.Policy()
	if policy.MaxConcurrency == 0 {
		policy = s.defaultPolicy
	}

	executor := NewExecutor(policy)

	// Forward events
	executor.Subscribe(func(event *Event) {
		s.emitEvent(event)
	})

	// Register execution
	s.mu.Lock()
	s.executions[dag.ID()] = &Execution{
		DAG:      dag,
		Executor: executor,
	}
	s.mu.Unlock()

	// Execute synchronously
	result, err := executor.Execute(ctx, dag, dispatcher)

	// Cleanup
	s.mu.Lock()
	delete(s.executions, dag.ID())
	s.mu.Unlock()

	return result, err
}

// Cancel cancels a DAG execution
func (s *Scheduler) Cancel(dagID string) error {
	s.mu.RLock()
	exec, ok := s.executions[dagID]
	s.mu.RUnlock()

	if !ok {
		return ErrDAGNotFound
	}

	return exec.Executor.Cancel()
}

// Status returns the status of a DAG execution
func (s *Scheduler) Status(dagID string) (*DAGStatus, error) {
	s.mu.RLock()
	exec, ok := s.executions[dagID]
	s.mu.RUnlock()

	if !ok {
		return nil, ErrDAGNotFound
	}

	return exec.Executor.Status(), nil
}

// List returns all active executions
func (s *Scheduler) List() []*DAGStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*DAGStatus, 0, len(s.executions))
	for _, exec := range s.executions {
		if status := exec.Executor.Status(); status != nil {
			result = append(result, status)
		}
	}
	return result
}

// =============================================================================
// Event Handling
// =============================================================================

// Subscribe registers an event handler
func (s *Scheduler) Subscribe(handler EventHandler) func() {
	s.handlersMu.Lock()
	s.handlers = append(s.handlers, handler)
	index := len(s.handlers) - 1
	s.handlersMu.Unlock()

	return func() {
		s.handlersMu.Lock()
		defer s.handlersMu.Unlock()
		if index < len(s.handlers) {
			s.handlers[index] = nil
		}
	}
}

// emitEvent emits an event to all handlers
func (s *Scheduler) emitEvent(event *Event) {
	s.handlersMu.RLock()
	handlers := make([]EventHandler, len(s.handlers))
	copy(handlers, s.handlers)
	s.handlersMu.RUnlock()

	for _, handler := range handlers {
		if handler != nil {
			go handler(event)
		}
	}
}

// =============================================================================
// Lifecycle
// =============================================================================

// Close closes the scheduler
func (s *Scheduler) Close() error {
	if s.closed.Swap(true) {
		return ErrExecutorClosed
	}

	// Cancel all running executions
	s.mu.Lock()
	for _, exec := range s.executions {
		exec.Executor.Cancel()
	}
	s.executions = make(map[string]*Execution)
	s.mu.Unlock()

	return nil
}

// Stats returns scheduler statistics
func (s *Scheduler) Stats() SchedulerStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := SchedulerStats{
		ActiveExecutions:  len(s.executions),
		MaxConcurrentDAGs: s.maxConcurrentDAGs,
	}

	for _, exec := range s.executions {
		status := exec.Executor.Status()
		if status != nil {
			switch status.State {
			case DAGStateRunning:
				stats.RunningDAGs++
			case DAGStateSucceeded:
				stats.CompletedDAGs++
			case DAGStateFailed:
				stats.FailedDAGs++
			}
		}
	}

	return stats
}

// SchedulerStats contains scheduler statistics
type SchedulerStats struct {
	ActiveExecutions  int `json:"active_executions"`
	MaxConcurrentDAGs int `json:"max_concurrent_dags"`
	RunningDAGs       int `json:"running_dags"`
	CompletedDAGs     int `json:"completed_dags"`
	FailedDAGs        int `json:"failed_dags"`
}
