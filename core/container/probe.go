package container

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

// probeState tracks consecutive success/failure counts for a single probe.
type probeState struct {
	spec             ProbeSpec
	consecutiveOK    atomic.Int32
	consecutiveFail  atomic.Int32
	lastResult       atomic.Pointer[ProbeResult]
	healthy          atomic.Bool
	started          atomic.Bool // true once initial delay has elapsed
}

// ProbeRunner manages probe execution for a container.
// All goroutines are tracked via GoroutineScope.
type ProbeRunner struct {
	mu       sync.RWMutex
	probes   map[ProbeType]*probeState // bounded: at most 3 probe types
	scope    *concurrency.GoroutineScope
	onResult func(ProbeType, ProbeResult)
	stopped  atomic.Bool
	stopCh   chan struct{} // independent cancellation without scope shutdown
}

// NewProbeRunner creates a probe runner that executes probes using the
// provided GoroutineScope. The onResult callback is called after each
// probe execution with the result.
func NewProbeRunner(scope *concurrency.GoroutineScope, onResult func(ProbeType, ProbeResult)) *ProbeRunner {
	return &ProbeRunner{
		probes:   make(map[ProbeType]*probeState, probeTypeCount),
		scope:    scope,
		onResult: onResult,
		stopCh:   make(chan struct{}),
	}
}

// probeTypeCount is the number of distinct probe types.
const probeTypeCount = 3

// Start begins executing all configured probes. Each probe runs in its
// own tracked goroutine via GoroutineScope.Go().
func (pr *ProbeRunner) Start(specs []ProbeSpec) error {
	pr.mu.Lock()
	defer pr.mu.Unlock()

	for i := range specs {
		spec := specs[i]
		state := &probeState{spec: spec}
		pr.probes[spec.Type] = state
		if err := pr.startProbeLoop(spec, state); err != nil {
			return err
		}
	}
	return nil
}

func (pr *ProbeRunner) startProbeLoop(spec ProbeSpec, state *probeState) error {
	return pr.scope.Go(
		"probe-"+spec.Type.String(),
		probeWorkerLifetime(spec),
		func(ctx context.Context) error {
			pr.runProbeLoop(ctx, spec, state)
			return nil
		},
	)
}

// probeWorkerLifetime computes a reasonable lifetime for the probe worker.
// It must be long enough to run indefinitely while the container is alive.
// The container's stop will cancel the scope context, terminating the worker.
func probeWorkerLifetime(spec ProbeSpec) time.Duration {
	return spec.Period * time.Duration(maxProbeIterations)
}

// maxProbeIterations caps the probe worker lifetime derivation.
// The actual worker exits on context cancellation, not on iteration count.
const maxProbeIterations = 100_000

func (pr *ProbeRunner) runProbeLoop(ctx context.Context, spec ProbeSpec, state *probeState) {
	pr.waitInitialDelay(ctx, spec)

	// Execute immediately so health state is established before the first
	// ticker period elapses. Without this, there's a full-period gap where
	// started=true but healthy=false, causing transient "unhealthy" reports.
	pr.executeProbe(ctx, spec, state)
	state.started.Store(true)

	ticker := time.NewTicker(spec.Period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-pr.stopCh:
			return
		case <-ticker.C:
			pr.executeProbe(ctx, spec, state)
		}
	}
}

func (pr *ProbeRunner) waitInitialDelay(ctx context.Context, spec ProbeSpec) {
	if spec.InitialDelay <= 0 {
		return
	}
	timer := time.NewTimer(spec.InitialDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-pr.stopCh:
	case <-timer.C:
	}
}

func (pr *ProbeRunner) executeProbe(ctx context.Context, spec ProbeSpec, state *probeState) {
	probeCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	start := time.Now()
	result, err := spec.Handler.Execute(probeCtx)
	result.Latency = time.Since(start)
	result.Timestamp = start

	if err != nil {
		result.Success = false
		result.Message = err.Error()
	}

	pr.updateProbeState(spec, state, result)
}

func (pr *ProbeRunner) updateProbeState(spec ProbeSpec, state *probeState, result ProbeResult) {
	state.lastResult.Store(&result)

	if result.Success {
		state.consecutiveOK.Add(1)
		state.consecutiveFail.Store(0)
	} else {
		state.consecutiveFail.Add(1)
		state.consecutiveOK.Store(0)
	}

	state.healthy.Store(
		int(state.consecutiveOK.Load()) >= spec.SuccessThreshold,
	)

	if pr.onResult != nil {
		pr.onResult(spec.Type, result)
	}
}

// IsHealthy returns true if the probe of the given type has met its
// success threshold. Returns true if the probe type is not configured
// or if the probe has not yet started (still in initial delay).
func (pr *ProbeRunner) IsHealthy(probeType ProbeType) bool {
	pr.mu.RLock()
	state, ok := pr.probes[probeType]
	pr.mu.RUnlock()
	if !ok {
		return true // no probe configured means implicitly healthy
	}
	if !state.started.Load() {
		return true // probe in initial delay — assume healthy until first check
	}
	return state.healthy.Load()
}

// HasStarted returns true if the probe of the given type has completed
// its initial delay and begun execution.
func (pr *ProbeRunner) HasStarted(probeType ProbeType) bool {
	pr.mu.RLock()
	state, ok := pr.probes[probeType]
	pr.mu.RUnlock()
	if !ok {
		return true // no probe configured means implicitly started
	}
	return state.started.Load()
}

// LastResult returns the most recent result for a probe type, or nil
// if no result has been recorded yet.
func (pr *ProbeRunner) LastResult(probeType ProbeType) *ProbeResult {
	pr.mu.RLock()
	state, ok := pr.probes[probeType]
	pr.mu.RUnlock()
	if !ok {
		return nil
	}
	return state.lastResult.Load()
}

// IsReady returns true if the readiness probe is healthy (or unconfigured).
func (pr *ProbeRunner) IsReady() bool {
	return pr.IsHealthy(ProbeReadiness)
}

// IsAlive returns true if the liveness probe is healthy (or unconfigured).
func (pr *ProbeRunner) IsAlive() bool {
	return pr.IsHealthy(ProbeLiveness)
}

// ConsecutiveFailures returns the consecutive failure count for a probe type.
func (pr *ProbeRunner) ConsecutiveFailures(probeType ProbeType) int {
	pr.mu.RLock()
	state, ok := pr.probes[probeType]
	pr.mu.RUnlock()
	if !ok {
		return 0
	}
	return int(state.consecutiveFail.Load())
}

// Stop cancels all running probes without affecting the GoroutineScope.
// Probe goroutines will exit on the next select iteration. Safe for
// concurrent calls — only the first call closes stopCh.
func (pr *ProbeRunner) Stop() {
	if pr.stopped.CompareAndSwap(false, true) {
		close(pr.stopCh)
	}
}

// Reset prepares the probe runner for reuse after Stop.
// Must only be called after all probe goroutines have exited
// (i.e., after Stop has propagated). Clears all probe health
// state and allocates a fresh stopCh for the next Start cycle.
func (pr *ProbeRunner) Reset() {
	pr.stopped.Store(false)
	pr.stopCh = make(chan struct{})
	pr.mu.Lock()
	for _, state := range pr.probes {
		state.consecutiveOK.Store(0)
		state.consecutiveFail.Store(0)
		state.healthy.Store(false)
		state.started.Store(false)
		state.lastResult.Store(nil)
	}
	pr.mu.Unlock()
}

// IsStopped reports whether Stop has been called.
func (pr *ProbeRunner) IsStopped() bool {
	return pr.stopped.Load()
}
