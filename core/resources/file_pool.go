package resources

import (
	"context"
	"sync"
	"syscall"
	"time"

	"github.com/adalundhe/sylk/core/signal"
)

// FileHandlePoolConfig holds file pool configuration.
type FileHandlePoolConfig struct {
	UsagePercent    float64
	UserReserved    float64
	PipelineTimeout time.Duration
	SignalBus       *signal.SignalBus
}

// DefaultFileHandlePoolConfig returns sensible defaults.
func DefaultFileHandlePoolConfig() FileHandlePoolConfig {
	return FileHandlePoolConfig{
		UsagePercent:    0.50,
		UserReserved:    0.20,
		PipelineTimeout: 30 * time.Second,
	}
}

// FileHandle represents an acquired file handle resource.
type FileHandle struct {
	handle   *ResourceHandle
	Path     string
	OpenedAt time.Time
	LastUsed time.Time
	UseCount int64
}

// FileHandlePool manages file descriptor resources.
type FileHandlePool struct {
	mu       sync.Mutex
	pool     *ResourcePool
	handles  map[string]*FileHandle
	maxLimit int
	closed   bool
}

// NewFileHandlePool creates a file handle pool based on OS limits.
func NewFileHandlePool(cfg FileHandlePoolConfig) (*FileHandlePool, error) {
	cfg = normalizeFileConfig(cfg)

	maxLimit := calculateFileLimit(cfg.UsagePercent)

	poolCfg := ResourcePoolConfig{
		Total:               maxLimit,
		UserReservedPercent: cfg.UserReserved,
		PipelineTimeout:     cfg.PipelineTimeout,
		SignalBus:           cfg.SignalBus,
	}

	return &FileHandlePool{
		pool:     NewResourcePool(ResourceTypeFile, poolCfg),
		handles:  make(map[string]*FileHandle),
		maxLimit: maxLimit,
	}, nil
}

func normalizeFileConfig(cfg FileHandlePoolConfig) FileHandlePoolConfig {
	cfg.UsagePercent = normalizeUsagePercent(cfg.UsagePercent, 0.50)
	cfg.UserReserved = normalizeUsagePercent(cfg.UserReserved, 0.20)
	cfg.PipelineTimeout = normalizeFileTimeout(cfg.PipelineTimeout)
	return cfg
}

func normalizeUsagePercent(percent, defaultVal float64) float64 {
	if percent <= 0 || percent > 1.0 {
		return defaultVal
	}
	return percent
}

func normalizeFileTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 30 * time.Second
	}
	return timeout
}

func calculateFileLimit(usagePercent float64) int {
	var rlimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rlimit); err != nil {
		return 256
	}

	limit := int(float64(rlimit.Cur) * usagePercent)
	if limit < 10 {
		return 10
	}
	return limit
}

// AcquireUser acquires a file handle for user operations.
func (p *FileHandlePool) AcquireUser(ctx context.Context) (*FileHandle, error) {
	handle, err := p.pool.AcquireUser(ctx)
	if err != nil {
		return nil, err
	}

	return p.trackHandle(handle, ""), nil
}

// AcquireUserForPath acquires a file handle for a specific path.
func (p *FileHandlePool) AcquireUserForPath(ctx context.Context, path string) (*FileHandle, error) {
	handle, err := p.pool.AcquireUser(ctx)
	if err != nil {
		return nil, err
	}

	return p.trackHandle(handle, path), nil
}

func (p *FileHandlePool) trackHandle(handle *ResourceHandle, path string) *FileHandle {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	fh := &FileHandle{
		handle:   handle,
		Path:     path,
		OpenedAt: now,
		LastUsed: now,
		UseCount: 1,
	}

	p.handles[handle.ID] = fh
	return fh
}

// AcquirePipeline acquires a file handle for pipeline operations.
func (p *FileHandlePool) AcquirePipeline(ctx context.Context, priority int) (*FileHandle, error) {
	handle, err := p.pool.AcquirePipeline(ctx, priority)
	if err != nil {
		return nil, err
	}

	return p.trackHandle(handle, ""), nil
}

// AcquirePipelineForPath acquires a file handle for a specific path.
func (p *FileHandlePool) AcquirePipelineForPath(ctx context.Context, priority int, path string) (*FileHandle, error) {
	handle, err := p.pool.AcquirePipeline(ctx, priority)
	if err != nil {
		return nil, err
	}

	return p.trackHandle(handle, path), nil
}

// Release returns a file handle to the pool.
func (p *FileHandlePool) Release(fh *FileHandle) error {
	if fh == nil || fh.handle == nil {
		return ErrInvalidHandle
	}

	p.mu.Lock()
	delete(p.handles, fh.handle.ID)
	p.mu.Unlock()

	return p.pool.Release(fh.handle)
}

// Touch updates the last used time for a handle.
func (p *FileHandlePool) Touch(fh *FileHandle) {
	if fh == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if tracked, exists := p.handles[fh.handle.ID]; exists {
		tracked.LastUsed = time.Now()
		tracked.UseCount++
	}
}

// LeakedHandles returns handles that haven't been used recently.
func (p *FileHandlePool) LeakedHandles(threshold time.Duration) []*FileHandle {
	p.mu.Lock()
	defer p.mu.Unlock()

	cutoff := time.Now().Add(-threshold)
	var leaked []*FileHandle

	for _, fh := range p.handles {
		if fh.LastUsed.Before(cutoff) {
			leaked = append(leaked, fh)
		}
	}

	return leaked
}

// Stats returns current pool statistics.
func (p *FileHandlePool) Stats() FilePoolStats {
	poolStats := p.pool.Stats()

	p.mu.Lock()
	activeCount := len(p.handles)
	p.mu.Unlock()

	return FilePoolStats{
		PoolStats:     poolStats,
		MaxLimit:      p.maxLimit,
		ActiveHandles: activeCount,
	}
}

// FilePoolStats contains file pool statistics.
type FilePoolStats struct {
	PoolStats
	MaxLimit      int `json:"max_limit"`
	ActiveHandles int `json:"active_handles"`
}

// Close shuts down the file pool.
func (p *FileHandlePool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrPoolClosed
	}
	p.closed = true
	p.handles = make(map[string]*FileHandle)
	p.mu.Unlock()

	return p.pool.Close()
}
