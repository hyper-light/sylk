package concurrency

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var ErrScopeShutdown = errors.New("goroutine scope is shutting down")

const defaultMaxLifetime = 5 * time.Minute

type WorkFunc func(ctx context.Context) error

type GoroutineScope struct {
	agentID   string
	parentCtx context.Context
	cancel    context.CancelFunc

	mu      sync.Mutex
	workers map[uint64]*worker
	nextID  uint64

	wg     sync.WaitGroup
	budget *GoroutineBudget

	maxLifetime time.Duration

	shutdownMu       sync.Mutex
	shutdownStarted  bool
	shutdownComplete chan struct{}
}

type worker struct {
	id          uint64
	ctx         context.Context
	cancel      context.CancelFunc
	startedAt   time.Time
	deadline    time.Time
	description string
	done        chan struct{}
	err         atomic.Pointer[error]
}

type GoroutineLeakError struct {
	AgentID     string
	LeakedCount int
	Workers     []string
	StackDump   string
}

func (e *GoroutineLeakError) Error() string {
	return fmt.Sprintf("CRITICAL: %d goroutines leaked in agent %s: %v",
		e.LeakedCount, e.AgentID, e.Workers)
}

func NewGoroutineScope(parentCtx context.Context, agentID string, budget *GoroutineBudget) *GoroutineScope {
	ctx, cancel := context.WithCancel(parentCtx)
	return &GoroutineScope{
		agentID:          agentID,
		parentCtx:        ctx,
		cancel:           cancel,
		workers:          make(map[uint64]*worker),
		budget:           budget,
		maxLifetime:      defaultMaxLifetime,
		shutdownComplete: make(chan struct{}),
	}
}

func (s *GoroutineScope) Go(description string, timeout time.Duration, fn WorkFunc) error {
	if err := s.checkShutdown(); err != nil {
		return err
	}

	timeout = s.normalizeTimeout(timeout)

	if err := s.budget.Acquire(s.agentID); err != nil {
		return err
	}

	w := s.createWorker(description, timeout)
	s.registerWorker(w)

	go s.runWorker(w, fn)
	return nil
}

func (s *GoroutineScope) checkShutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdownStarted {
		return ErrScopeShutdown
	}
	return nil
}

func (s *GoroutineScope) normalizeTimeout(timeout time.Duration) time.Duration {
	if timeout > s.maxLifetime || timeout == 0 {
		return s.maxLifetime
	}
	return timeout
}

func (s *GoroutineScope) createWorker(description string, timeout time.Duration) *worker {
	workerCtx, workerCancel := context.WithTimeout(s.parentCtx, timeout)
	return &worker{
		ctx:         workerCtx,
		cancel:      workerCancel,
		startedAt:   time.Now(),
		deadline:    time.Now().Add(timeout),
		description: description,
		done:        make(chan struct{}),
	}
}

func (s *GoroutineScope) registerWorker(w *worker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.id = s.nextID
	s.nextID++
	s.workers[w.id] = w
	s.wg.Add(1)
}

func (s *GoroutineScope) runWorker(w *worker, fn WorkFunc) {
	defer s.cleanupWorker(w)
	s.executeWork(w, fn)
}

func (s *GoroutineScope) executeWork(w *worker, fn WorkFunc) {
	defer s.recoverPanic(w)
	err := fn(w.ctx)
	if err != nil {
		w.err.Store(&err)
	}
}

func (s *GoroutineScope) recoverPanic(w *worker) {
	if r := recover(); r != nil {
		err := fmt.Errorf("panic: %v\n%s", r, captureStack(3))
		w.err.Store(&err)
	}
}

func (s *GoroutineScope) cleanupWorker(w *worker) {
	w.cancel()
	close(w.done)

	s.mu.Lock()
	delete(s.workers, w.id)
	s.mu.Unlock()

	s.budget.Release(s.agentID)
	s.wg.Done()
}

func (s *GoroutineScope) Shutdown(gracePeriod, hardDeadline time.Duration) error {
	if !s.beginShutdown() {
		<-s.shutdownComplete
		return nil
	}
	defer close(s.shutdownComplete)

	s.cancel()
	return s.waitForShutdown(gracePeriod, hardDeadline)
}

func (s *GoroutineScope) beginShutdown() bool {
	s.shutdownMu.Lock()
	defer s.shutdownMu.Unlock()
	if s.shutdownStarted {
		return false
	}
	s.shutdownStarted = true
	return true
}

func (s *GoroutineScope) waitForShutdown(gracePeriod, hardDeadline time.Duration) error {
	graceDone := s.startWaitGroup()

	select {
	case <-graceDone:
		return nil
	case <-time.After(gracePeriod):
		return s.forceShutdown(graceDone, hardDeadline-gracePeriod)
	}
}

func (s *GoroutineScope) startWaitGroup() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	return done
}

func (s *GoroutineScope) forceShutdown(graceDone <-chan struct{}, remaining time.Duration) error {
	s.cancelAllWorkers()

	if s.workerCount() == 0 {
		return nil
	}

	select {
	case <-graceDone:
		return nil
	case <-time.After(remaining):
		return s.buildLeakError()
	}
}

func (s *GoroutineScope) cancelAllWorkers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.workers {
		w.cancel()
	}
}

func (s *GoroutineScope) workerCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.workers)
}

func (s *GoroutineScope) buildLeakError() *GoroutineLeakError {
	s.mu.Lock()
	leaked := make([]string, 0, len(s.workers))
	for _, w := range s.workers {
		leaked = append(leaked, fmt.Sprintf(
			"worker[%d] desc=%q started=%v deadline=%v",
			w.id, w.description, w.startedAt, w.deadline,
		))
	}
	s.mu.Unlock()

	return &GoroutineLeakError{
		AgentID:     s.agentID,
		LeakedCount: len(leaked),
		Workers:     leaked,
		StackDump:   captureAllStacks(),
	}
}

func (s *GoroutineScope) WorkerCount() int {
	return s.workerCount()
}

func (s *GoroutineScope) AgentID() string {
	return s.agentID
}

func (s *GoroutineScope) SetMaxLifetime(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxLifetime = d
}

func captureStack(skip int) string {
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	return string(buf[:n])
}

func captureAllStacks() string {
	buf := make([]byte, 65536)
	n := runtime.Stack(buf, true)
	return string(buf[:n])
}
