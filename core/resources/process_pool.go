package resources

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/signal"
)

// ProcessPoolConfig holds process pool configuration.
type ProcessPoolConfig struct {
	CPUMultiplier   int
	UserReserved    float64
	PipelineTimeout time.Duration
	SignalBus       *signal.SignalBus
}

// DefaultProcessPoolConfig returns sensible defaults.
func DefaultProcessPoolConfig() ProcessPoolConfig {
	return ProcessPoolConfig{
		CPUMultiplier:   2,
		UserReserved:    0.20,
		PipelineTimeout: 30 * time.Second,
	}
}

// ProcessHandle represents an acquired subprocess slot.
type ProcessHandle struct {
	handle    *ResourceHandle
	PID       int
	Command   string
	StartedAt time.Time
}

// ProcessPool manages subprocess resources.
type ProcessPool struct {
	mu       sync.Mutex
	pool     *ResourcePool
	handles  map[string]*ProcessHandle
	pids     map[int]string
	maxProcs int
	closed   bool
}

// NewProcessPool creates a subprocess pool based on CPU cores.
func NewProcessPool(cfg ProcessPoolConfig) *ProcessPool {
	cfg = normalizeProcessConfig(cfg)

	maxProcs := runtime.NumCPU() * cfg.CPUMultiplier
	if maxProcs < 1 {
		maxProcs = 1
	}

	poolCfg := ResourcePoolConfig{
		Total:               maxProcs,
		UserReservedPercent: cfg.UserReserved,
		PipelineTimeout:     cfg.PipelineTimeout,
		SignalBus:           cfg.SignalBus,
	}

	return &ProcessPool{
		pool:     NewResourcePool(ResourceTypeProcess, poolCfg),
		handles:  make(map[string]*ProcessHandle),
		pids:     make(map[int]string),
		maxProcs: maxProcs,
	}
}

func normalizeProcessConfig(cfg ProcessPoolConfig) ProcessPoolConfig {
	cfg.CPUMultiplier = normalizeProcMultiplier(cfg.CPUMultiplier)
	cfg.UserReserved = normalizeProcPercent(cfg.UserReserved)
	cfg.PipelineTimeout = normalizeProcTimeout(cfg.PipelineTimeout)
	return cfg
}

func normalizeProcMultiplier(mult int) int {
	if mult <= 0 {
		return 2
	}
	return mult
}

func normalizeProcPercent(percent float64) float64 {
	if percent <= 0 || percent > 1.0 {
		return 0.20
	}
	return percent
}

func normalizeProcTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 30 * time.Second
	}
	return timeout
}

// AcquireUser acquires a process slot for user operations.
func (p *ProcessPool) AcquireUser(ctx context.Context) (*ProcessHandle, error) {
	handle, err := p.pool.AcquireUser(ctx)
	if err != nil {
		return nil, err
	}

	return p.trackHandle(handle, 0, ""), nil
}

// AcquireUserForCommand acquires a process slot for a specific command.
func (p *ProcessPool) AcquireUserForCommand(ctx context.Context, command string) (*ProcessHandle, error) {
	handle, err := p.pool.AcquireUser(ctx)
	if err != nil {
		return nil, err
	}

	return p.trackHandle(handle, 0, command), nil
}

func (p *ProcessPool) trackHandle(handle *ResourceHandle, pid int, command string) *ProcessHandle {
	p.mu.Lock()
	defer p.mu.Unlock()

	ph := &ProcessHandle{
		handle:    handle,
		PID:       pid,
		Command:   command,
		StartedAt: time.Now(),
	}

	p.handles[handle.ID] = ph
	if pid > 0 {
		p.pids[pid] = handle.ID
	}
	return ph
}

// AcquirePipeline acquires a process slot for pipeline operations.
func (p *ProcessPool) AcquirePipeline(ctx context.Context, priority int) (*ProcessHandle, error) {
	handle, err := p.pool.AcquirePipeline(ctx, priority)
	if err != nil {
		return nil, err
	}

	return p.trackHandle(handle, 0, ""), nil
}

// AcquirePipelineForCommand acquires a process slot for a specific command.
func (p *ProcessPool) AcquirePipelineForCommand(ctx context.Context, priority int, command string) (*ProcessHandle, error) {
	handle, err := p.pool.AcquirePipeline(ctx, priority)
	if err != nil {
		return nil, err
	}

	return p.trackHandle(handle, 0, command), nil
}

// SetPID associates a process ID with a handle for cleanup tracking.
func (p *ProcessPool) SetPID(ph *ProcessHandle, pid int) {
	if ph == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	tracked, exists := p.handles[ph.handle.ID]
	if !exists {
		return
	}

	p.updateTrackedPID(tracked, ph.handle.ID, pid)
}

func (p *ProcessPool) updateTrackedPID(tracked *ProcessHandle, handleID string, pid int) {
	if tracked.PID > 0 {
		delete(p.pids, tracked.PID)
	}
	tracked.PID = pid
	if pid > 0 {
		p.pids[pid] = handleID
	}
}

// Release returns a process slot to the pool.
func (p *ProcessPool) Release(ph *ProcessHandle) error {
	if ph == nil || ph.handle == nil {
		return ErrInvalidHandle
	}

	p.mu.Lock()
	p.untrackProcessHandle(ph.handle.ID)
	p.mu.Unlock()

	return p.pool.Release(ph.handle)
}

func (p *ProcessPool) untrackProcessHandle(handleID string) {
	tracked, exists := p.handles[handleID]
	if !exists {
		return
	}

	if tracked.PID > 0 {
		delete(p.pids, tracked.PID)
	}
	delete(p.handles, handleID)
}

// ActivePIDs returns all tracked process IDs.
func (p *ProcessPool) ActivePIDs() []int {
	p.mu.Lock()
	defer p.mu.Unlock()

	pids := make([]int, 0, len(p.pids))
	for pid := range p.pids {
		pids = append(pids, pid)
	}
	return pids
}

// HandleByPID finds a handle by its process ID.
func (p *ProcessPool) HandleByPID(pid int) *ProcessHandle {
	p.mu.Lock()
	defer p.mu.Unlock()

	handleID, exists := p.pids[pid]
	if !exists {
		return nil
	}

	return p.handles[handleID]
}

// Stats returns current pool statistics.
func (p *ProcessPool) Stats() ProcessPoolStats {
	poolStats := p.pool.Stats()

	p.mu.Lock()
	activeCount := len(p.handles)
	pidCount := len(p.pids)
	p.mu.Unlock()

	return ProcessPoolStats{
		PoolStats:     poolStats,
		MaxProcs:      p.maxProcs,
		CPUCores:      runtime.NumCPU(),
		ActiveHandles: activeCount,
		TrackedPIDs:   pidCount,
	}
}

// ProcessPoolStats contains process pool statistics.
type ProcessPoolStats struct {
	PoolStats
	MaxProcs      int `json:"max_procs"`
	CPUCores      int `json:"cpu_cores"`
	ActiveHandles int `json:"active_handles"`
	TrackedPIDs   int `json:"tracked_pids"`
}

// Close shuts down the process pool.
func (p *ProcessPool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrPoolClosed
	}
	p.closed = true
	p.handles = make(map[string]*ProcessHandle)
	p.pids = make(map[int]string)
	p.mu.Unlock()

	return p.pool.Close()
}
