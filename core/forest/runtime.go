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

	mu      sync.Mutex
	workers map[string]*runtimeWorkerState
	started bool
	closed  bool

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

// RuntimeSnapshot is an immutable view of all workers known to the runtime.
type RuntimeSnapshot struct {
	Started bool
	Closed  bool
	Workers []RuntimeWorkerSnapshot
}

func newRuntime(parent context.Context, logger *slog.Logger) *Runtime {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &Runtime{
		ctx:     ctx,
		cancel:  cancel,
		logger:  normalizeLogger(logger),
		workers: make(map[string]*runtimeWorkerState),
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
	if queueLimit < 0 {
		return fmt.Errorf("forest runtime worker %q queue limit cannot be negative", name)
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
	}
	r.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("forest runtime close: workers did not drain: %w", ctx.Err())
	}
}

func (r *Runtime) Snapshot() RuntimeSnapshot {
	if r == nil {
		return RuntimeSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := RuntimeSnapshot{
		Started: r.started,
		Closed:  r.closed,
		Workers: make([]RuntimeWorkerSnapshot, 0, len(r.workers)),
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
	sort.Slice(out.Workers, func(i, j int) bool {
		return out.Workers[i].Name < out.Workers[j].Name
	})
	return out
}
