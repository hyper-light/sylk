package forest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultRuntimeShutdownTimeout = 30 * time.Second

// Runtime owns the lifecycle of Memory Forest background workers.
// Workers are named, tracked, panic-recovered, and observable. It is
// intentionally small: worker loops already own their internal lease,
// ticker, and queue semantics, while Runtime supplies the common
// goroutine boundary and shutdown accounting.
type Runtime struct {
	ctx    context.Context
	cancel context.CancelFunc
	logger *slog.Logger

	mu          sync.Mutex
	workers     map[string]*runtimeWorkerState
	queues      map[string]*runtimeQueueState
	tickers     map[string]*runtimeTickerState
	leases      map[string]*runtimeLeaseState
	started     bool
	closed      bool
	active      int
	drainDone   chan struct{}
	drainClosed bool
	timeoutAt   time.Time
	timeoutErr  string

	wg sync.WaitGroup
}

type runtimeWorkerState struct {
	name        string
	queueLimit  int
	status      RuntimeWorkerStatus
	startedAt   time.Time
	stoppedAt   time.Time
	lastSuccess time.Time
	lastError   string
	lastErrorAt time.Time
	panicStack  string
}

type runtimeQueueState struct {
	name       string
	capacity   int
	registered time.Time
	overflows  uint64
	lastReason string
	lastAt     time.Time
}

type runtimeTickerState struct {
	name       string
	interval   time.Duration
	registered time.Time
}

type runtimeLeaseState struct {
	name       string
	holder     string
	registered time.Time
}

// RuntimeWorkerStatus is the external status value exposed by RuntimeSnapshot.
type RuntimeWorkerStatus string

const (
	RuntimeWorkerRegistered RuntimeWorkerStatus = "registered"
	RuntimeWorkerRunning    RuntimeWorkerStatus = "running"
	RuntimeWorkerStopped    RuntimeWorkerStatus = "stopped"
	RuntimeWorkerErrored    RuntimeWorkerStatus = "errored"
	RuntimeWorkerPanicked   RuntimeWorkerStatus = "panicked"
)

// RuntimeWorkerSnapshot is a stable copy of one worker's state.
type RuntimeWorkerSnapshot struct {
	Name        string
	QueueLimit  int
	Status      RuntimeWorkerStatus
	StartedAt   time.Time
	StoppedAt   time.Time
	LastSuccess time.Time
	LastError   string
	LastErrorAt time.Time
}

type RuntimeQueueSnapshot struct {
	Name       string
	Capacity   int
	Registered time.Time
	Overflows  uint64
	LastReason string
	LastAt     time.Time
}

type RuntimeTickerSnapshot struct {
	Name       string
	Interval   time.Duration
	Registered time.Time
}

type RuntimeLeaseSnapshot struct {
	Name       string
	Holder     string
	Registered time.Time
}

// RuntimeSnapshot is an immutable view of all workers known to the runtime.
type RuntimeSnapshot struct {
	Started      bool
	Closed       bool
	TimeoutAt    time.Time
	TimeoutError string
	Workers      []RuntimeWorkerSnapshot
	Queues       []RuntimeQueueSnapshot
	Tickers      []RuntimeTickerSnapshot
	Leases       []RuntimeLeaseSnapshot
}

func newRuntime(parent context.Context, logger *slog.Logger) *Runtime {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &Runtime{
		ctx:       ctx,
		cancel:    cancel,
		logger:    normalizeLogger(logger),
		workers:   make(map[string]*runtimeWorkerState),
		queues:    make(map[string]*runtimeQueueState),
		tickers:   make(map[string]*runtimeTickerState),
		leases:    make(map[string]*runtimeLeaseState),
		drainDone: make(chan struct{}),
	}
}

// StartWorker registers and starts a named worker. The name is a durable
// observability key; duplicate names are rejected so one stuck worker cannot
// hide behind another.
func (r *Runtime) StartWorker(name string, queueLimit int, run func(context.Context) error) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("forest runtime worker name is required")
	}
	if queueLimit <= 0 {
		return fmt.Errorf("forest runtime worker %q queue limit must be positive", name)
	}
	if run == nil {
		return fmt.Errorf("forest runtime worker %q run func is required", name)
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return fmt.Errorf("forest runtime is closed")
	}
	if _, exists := r.workers[name]; exists {
		r.mu.Unlock()
		return fmt.Errorf("forest runtime worker %q already registered", name)
	}
	state := &runtimeWorkerState{
		name:       name,
		queueLimit: queueLimit,
		status:     RuntimeWorkerRegistered,
	}
	r.workers[name] = state
	r.started = true
	r.active++
	r.wg.Add(1)
	r.mu.Unlock()

	go r.runWorker(state, run)
	return nil
}

func (r *Runtime) runWorker(state *runtimeWorkerState, run func(context.Context) error) {
	defer r.wg.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			r.finishWorker(state.name, RuntimeWorkerPanicked, fmt.Sprintf("panic: %v", recovered), string(debug.Stack()))
			r.logger.Error("forest_runtime_worker_panicked",
				"worker", state.name,
				"panic", recovered,
			)
		}
	}()

	r.markWorkerRunning(state.name)
	err := run(r.ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		r.finishWorker(state.name, RuntimeWorkerErrored, err.Error(), "")
		r.logger.Error("forest_runtime_worker_failed", "worker", state.name, "err", err.Error())
		return
	}
	r.finishWorker(state.name, RuntimeWorkerStopped, "", "")
}

func (r *Runtime) markWorkerRunning(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if state := r.workers[name]; state != nil {
		now := time.Now().UTC()
		state.status = RuntimeWorkerRunning
		state.startedAt = now
		state.lastSuccess = now
	}
}

func (r *Runtime) finishWorker(name string, status RuntimeWorkerStatus, errText, stack string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if state := r.workers[name]; state != nil {
		now := time.Now().UTC()
		state.status = status
		state.stoppedAt = now
		if errText == "" {
			state.lastSuccess = now
		} else {
			state.lastError = truncateRuntimeError(errText)
			state.lastErrorAt = now
			state.panicStack = stack
		}
	}
	r.active--
	if r.active == 0 && r.closed {
		r.closeDrainLocked()
	}
}

func truncateRuntimeError(errText string) string {
	errText = strings.TrimSpace(errText)
	if len(errText) <= projectorErrTruncate {
		return errText
	}
	return errText[:projectorErrTruncate]
}

// Close cancels the runtime context and waits for workers to stop. The wait is
// bounded by ctx; callers may pass a shutdown budget from the host lifecycle.
func (r *Runtime) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		r.cancel()
		if r.active == 0 {
			r.closeDrainLocked()
		}
	}
	done := r.drainDone
	r.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		err := fmt.Errorf("forest runtime close: workers did not drain: %w", ctx.Err())
		r.recordCloseTimeout(err)
		return err
	}
}

func (r *Runtime) closeDrainLocked() {
	if r.drainClosed {
		return
	}
	close(r.drainDone)
	r.drainClosed = true
}

func (r *Runtime) recordCloseTimeout(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.timeoutAt = time.Now().UTC()
	r.timeoutErr = truncateRuntimeError(err.Error())
}

func (r *Runtime) RegisterQueue(name string, capacity int) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("forest runtime queue name is required")
	}
	if capacity <= 0 {
		return fmt.Errorf("forest runtime queue %q capacity must be positive", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.queues[name]; exists {
		return fmt.Errorf("forest runtime queue %q already registered", name)
	}
	r.queues[name] = &runtimeQueueState{name: name, capacity: capacity, registered: time.Now().UTC()}
	return nil
}

func (r *Runtime) RegisterTicker(name string, interval time.Duration) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("forest runtime ticker name is required")
	}
	if interval <= 0 {
		return fmt.Errorf("forest runtime ticker %q interval must be positive", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tickers[name]; exists {
		return fmt.Errorf("forest runtime ticker %q already registered", name)
	}
	r.tickers[name] = &runtimeTickerState{name: name, interval: interval, registered: time.Now().UTC()}
	return nil
}

func (r *Runtime) RegisterLease(name, holder string) error {
	name = strings.TrimSpace(name)
	holder = strings.TrimSpace(holder)
	if name == "" {
		return errors.New("forest runtime lease name is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.leases[name]; exists {
		return fmt.Errorf("forest runtime lease %q already registered", name)
	}
	r.leases[name] = &runtimeLeaseState{name: name, holder: holder, registered: time.Now().UTC()}
	return nil
}

func (r *Runtime) RecordQueueOverflow(name, reason string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.queues[name]
	if state == nil {
		return
	}
	state.overflows++
	state.lastReason = truncateRuntimeError(reason)
	state.lastAt = time.Now().UTC()
}

func (r *Runtime) Snapshot() RuntimeSnapshot {
	if r == nil {
		return RuntimeSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := RuntimeSnapshot{
		Started:      r.started,
		Closed:       r.closed,
		TimeoutAt:    r.timeoutAt,
		TimeoutError: r.timeoutErr,
		Workers:      make([]RuntimeWorkerSnapshot, 0, len(r.workers)),
		Queues:       make([]RuntimeQueueSnapshot, 0, len(r.queues)),
		Tickers:      make([]RuntimeTickerSnapshot, 0, len(r.tickers)),
		Leases:       make([]RuntimeLeaseSnapshot, 0, len(r.leases)),
	}
	for _, state := range r.workers {
		out.Workers = append(out.Workers, RuntimeWorkerSnapshot{
			Name:        state.name,
			QueueLimit:  state.queueLimit,
			Status:      state.status,
			StartedAt:   state.startedAt,
			StoppedAt:   state.stoppedAt,
			LastSuccess: state.lastSuccess,
			LastError:   state.lastError,
			LastErrorAt: state.lastErrorAt,
		})
	}
	for _, state := range r.queues {
		out.Queues = append(out.Queues, RuntimeQueueSnapshot{
			Name:       state.name,
			Capacity:   state.capacity,
			Registered: state.registered,
			Overflows:  state.overflows,
			LastReason: state.lastReason,
			LastAt:     state.lastAt,
		})
	}
	for _, state := range r.tickers {
		out.Tickers = append(out.Tickers, RuntimeTickerSnapshot{
			Name:       state.name,
			Interval:   state.interval,
			Registered: state.registered,
		})
	}
	for _, state := range r.leases {
		out.Leases = append(out.Leases, RuntimeLeaseSnapshot{
			Name:       state.name,
			Holder:     state.holder,
			Registered: state.registered,
		})
	}
	sort.Slice(out.Workers, func(i, j int) bool {
		return out.Workers[i].Name < out.Workers[j].Name
	})
	sort.Slice(out.Queues, func(i, j int) bool {
		return out.Queues[i].Name < out.Queues[j].Name
	})
	sort.Slice(out.Tickers, func(i, j int) bool {
		return out.Tickers[i].Name < out.Tickers[j].Name
	})
	sort.Slice(out.Leases, func(i, j int) bool {
		return out.Leases[i].Name < out.Leases[j].Name
	})
	return out
}
