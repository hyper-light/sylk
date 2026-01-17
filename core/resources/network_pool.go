package resources

import (
	"context"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/signal"
)

// NetworkPoolConfig holds network pool configuration.
type NetworkPoolConfig struct {
	GlobalLimit     int
	PerProviderMax  int
	UserReserved    float64
	PipelineTimeout time.Duration
	SignalBus       *signal.SignalBus
}

// DefaultNetworkPoolConfig returns sensible defaults.
func DefaultNetworkPoolConfig() NetworkPoolConfig {
	return NetworkPoolConfig{
		GlobalLimit:     50,
		PerProviderMax:  10,
		UserReserved:    0.20,
		PipelineTimeout: 30 * time.Second,
	}
}

// NetworkHandle represents an acquired network connection resource.
type NetworkHandle struct {
	handle     *ResourceHandle
	ProviderID string
	Endpoint   string
	CreatedAt  time.Time
	LastUsed   time.Time
	UseCount   int64
}

// NetworkPool manages network connection resources.
type NetworkPool struct {
	mu             sync.Mutex
	pool           *ResourcePool
	handles        map[string]*NetworkHandle
	providerCounts map[string]int
	perProviderMax int
	closed         bool
}

// NewNetworkPool creates a network connection pool.
func NewNetworkPool(cfg NetworkPoolConfig) *NetworkPool {
	cfg = normalizeNetworkConfig(cfg)

	poolCfg := ResourcePoolConfig{
		Total:               cfg.GlobalLimit,
		UserReservedPercent: cfg.UserReserved,
		PipelineTimeout:     cfg.PipelineTimeout,
		SignalBus:           cfg.SignalBus,
	}

	return &NetworkPool{
		pool:           NewResourcePool(ResourceTypeNetwork, poolCfg),
		handles:        make(map[string]*NetworkHandle),
		providerCounts: make(map[string]int),
		perProviderMax: cfg.PerProviderMax,
	}
}

func normalizeNetworkConfig(cfg NetworkPoolConfig) NetworkPoolConfig {
	cfg.GlobalLimit = normalizeIntDefault(cfg.GlobalLimit, 50)
	cfg.PerProviderMax = normalizeIntDefault(cfg.PerProviderMax, 10)
	cfg.UserReserved = normalizeNetPercent(cfg.UserReserved)
	cfg.PipelineTimeout = normalizeNetTimeout(cfg.PipelineTimeout)
	return cfg
}

func normalizeIntDefault(val, defaultVal int) int {
	if val <= 0 {
		return defaultVal
	}
	return val
}

func normalizeNetPercent(percent float64) float64 {
	if percent <= 0 || percent > 1.0 {
		return 0.20
	}
	return percent
}

func normalizeNetTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 30 * time.Second
	}
	return timeout
}

// AcquireUser acquires a network connection for user operations.
func (p *NetworkPool) AcquireUser(ctx context.Context, providerID string) (*NetworkHandle, error) {
	if err := p.checkProviderLimit(providerID); err != nil {
		return nil, err
	}

	handle, err := p.pool.AcquireUser(ctx)
	if err != nil {
		return nil, err
	}

	return p.trackHandle(handle, providerID, ""), nil
}

func (p *NetworkPool) checkProviderLimit(providerID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrPoolClosed
	}

	count := p.providerCounts[providerID]
	if count >= p.perProviderMax {
		return ErrProviderLimitReached
	}

	return nil
}

func (p *NetworkPool) trackHandle(handle *ResourceHandle, providerID, endpoint string) *NetworkHandle {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	nh := &NetworkHandle{
		handle:     handle,
		ProviderID: providerID,
		Endpoint:   endpoint,
		CreatedAt:  now,
		LastUsed:   now,
		UseCount:   1,
	}

	p.handles[handle.ID] = nh
	p.providerCounts[providerID]++
	return nh
}

// AcquireUserForEndpoint acquires a connection for a specific endpoint.
func (p *NetworkPool) AcquireUserForEndpoint(ctx context.Context, providerID, endpoint string) (*NetworkHandle, error) {
	if err := p.checkProviderLimit(providerID); err != nil {
		return nil, err
	}

	handle, err := p.pool.AcquireUser(ctx)
	if err != nil {
		return nil, err
	}

	return p.trackHandle(handle, providerID, endpoint), nil
}

// AcquirePipeline acquires a network connection for pipeline operations.
func (p *NetworkPool) AcquirePipeline(ctx context.Context, providerID string, priority int) (*NetworkHandle, error) {
	if err := p.checkProviderLimit(providerID); err != nil {
		return nil, err
	}

	handle, err := p.pool.AcquirePipeline(ctx, priority)
	if err != nil {
		return nil, err
	}

	return p.trackHandle(handle, providerID, ""), nil
}

// AcquirePipelineForEndpoint acquires a connection for a specific endpoint.
func (p *NetworkPool) AcquirePipelineForEndpoint(ctx context.Context, providerID, endpoint string, priority int) (*NetworkHandle, error) {
	if err := p.checkProviderLimit(providerID); err != nil {
		return nil, err
	}

	handle, err := p.pool.AcquirePipeline(ctx, priority)
	if err != nil {
		return nil, err
	}

	return p.trackHandle(handle, providerID, endpoint), nil
}

// Release returns a network connection to the pool.
func (p *NetworkPool) Release(nh *NetworkHandle) error {
	if nh == nil || nh.handle == nil {
		return ErrInvalidHandle
	}

	p.mu.Lock()
	p.untrackHandle(nh.handle.ID)
	p.mu.Unlock()

	return p.pool.Release(nh.handle)
}

func (p *NetworkPool) untrackHandle(handleID string) {
	tracked, exists := p.handles[handleID]
	if !exists {
		return
	}

	p.decrementProviderCount(tracked.ProviderID)
	delete(p.handles, handleID)
}

func (p *NetworkPool) decrementProviderCount(providerID string) {
	p.providerCounts[providerID]--
	if p.providerCounts[providerID] <= 0 {
		delete(p.providerCounts, providerID)
	}
}

// Touch updates the last used time for a handle.
func (p *NetworkPool) Touch(nh *NetworkHandle) {
	if nh == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if tracked, exists := p.handles[nh.handle.ID]; exists {
		tracked.LastUsed = time.Now()
		tracked.UseCount++
	}
}

// ProviderCount returns the number of connections for a provider.
func (p *NetworkPool) ProviderCount(providerID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.providerCounts[providerID]
}

// Stats returns current pool statistics.
func (p *NetworkPool) Stats() NetworkPoolStats {
	poolStats := p.pool.Stats()

	p.mu.Lock()
	providerStats := make(map[string]int, len(p.providerCounts))
	for k, v := range p.providerCounts {
		providerStats[k] = v
	}
	activeCount := len(p.handles)
	p.mu.Unlock()

	return NetworkPoolStats{
		PoolStats:      poolStats,
		PerProviderMax: p.perProviderMax,
		ActiveHandles:  activeCount,
		ProviderCounts: providerStats,
	}
}

// NetworkPoolStats contains network pool statistics.
type NetworkPoolStats struct {
	PoolStats
	PerProviderMax int            `json:"per_provider_max"`
	ActiveHandles  int            `json:"active_handles"`
	ProviderCounts map[string]int `json:"provider_counts"`
}

// Close shuts down the network pool.
func (p *NetworkPool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrPoolClosed
	}
	p.closed = true
	p.handles = make(map[string]*NetworkHandle)
	p.providerCounts = make(map[string]int)
	p.mu.Unlock()

	return p.pool.Close()
}

// ErrProviderLimitReached indicates per-provider limit is exceeded.
var ErrProviderLimitReached = errProviderLimitReached{}

type errProviderLimitReached struct{}

func (errProviderLimitReached) Error() string {
	return "provider connection limit reached"
}
