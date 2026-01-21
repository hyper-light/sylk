package variants

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type VFSFactory interface {
	CreateVariantVFS(ctx context.Context, basePipelineID string, variantID VariantID, isolation IsolationLevel) (any, error)
	CloneVFS(ctx context.Context, sourceVFS any, variantID VariantID) (any, error)
	CloseVFS(ctx context.Context, vfs any) error
}

type Controller interface {
	CreateVariant(ctx context.Context, cfg VariantConfig) (*Variant, error)
	StartVariant(ctx context.Context, id VariantID) error
	StopVariant(ctx context.Context, id VariantID) error
	SuspendVariant(ctx context.Context, id VariantID) error
	ResumeVariant(ctx context.Context, id VariantID) error
	CancelVariant(ctx context.Context, id VariantID) error
	CompleteVariant(ctx context.Context, id VariantID) error
	FailVariant(ctx context.Context, id VariantID, err error) error
	GetVariantVFS(id VariantID) (any, error)
	GetVariant(id VariantID) (*Variant, error)
	ListVariants(filter *VariantFilter) []*VariantInfo
	Stats() ControllerStats
	Close() error
}

type ControllerConfig struct {
	MaxConcurrentVariants int
	DefaultTimeout        time.Duration
	DefaultIsolation      IsolationLevel
	DefaultResourceLimits ResourceLimits
	CleanupInterval       time.Duration
	RetentionPeriod       time.Duration
}

func DefaultControllerConfig() ControllerConfig {
	return ControllerConfig{
		MaxConcurrentVariants: 10,
		DefaultTimeout:        30 * time.Minute,
		DefaultIsolation:      IsolationLevelCopyOnWrite,
		DefaultResourceLimits: DefaultResourceLimits(),
		CleanupInterval:       5 * time.Minute,
		RetentionPeriod:       24 * time.Hour,
	}
}

type VariantController struct {
	mu         sync.RWMutex
	config     ControllerConfig
	registry   Registry
	vfsFactory VFSFactory
	merger     Merger

	activeCount   atomic.Int32
	cleanupTicker *time.Ticker
	cleanupDone   chan struct{}
	closed        atomic.Bool
}

func NewController(cfg ControllerConfig, registry Registry, vfsFactory VFSFactory, merger Merger) *VariantController {
	if cfg.MaxConcurrentVariants == 0 {
		cfg = DefaultControllerConfig()
	}

	c := &VariantController{
		config:      cfg,
		registry:    registry,
		vfsFactory:  vfsFactory,
		merger:      merger,
		cleanupDone: make(chan struct{}),
	}

	if cfg.CleanupInterval > 0 {
		c.startCleanupRoutine()
	}

	return c
}

func (c *VariantController) startCleanupRoutine() {
	c.cleanupTicker = time.NewTicker(c.config.CleanupInterval)
	go c.cleanupLoop()
}

func (c *VariantController) cleanupLoop() {
	for {
		select {
		case <-c.cleanupTicker.C:
			c.performCleanup()
		case <-c.cleanupDone:
			return
		}
	}
}

func (c *VariantController) performCleanup() {
	if mr, ok := c.registry.(*MemoryRegistry); ok {
		mr.PurgeTerminal(c.config.RetentionPeriod)
	}
}

func (c *VariantController) CreateVariant(ctx context.Context, cfg VariantConfig) (*Variant, error) {
	if c.closed.Load() {
		return nil, ErrVariantClosed
	}

	if err := c.checkResourceAvailability(); err != nil {
		return nil, err
	}

	cfg = c.applyDefaults(cfg)

	variant, err := NewVariant(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create variant: %w", err)
	}

	vfs, err := c.createVariantVFS(ctx, cfg, variant.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to create variant VFS: %w", err)
	}
	variant.SetVFS(vfs)

	if err := c.registry.Register(ctx, variant); err != nil {
		c.cleanupVFS(ctx, vfs)
		return nil, fmt.Errorf("failed to register variant: %w", err)
	}

	return variant, nil
}

func (c *VariantController) checkResourceAvailability() error {
	if int(c.activeCount.Load()) >= c.config.MaxConcurrentVariants {
		return ErrVariantResourceLimit
	}
	return nil
}

func (c *VariantController) applyDefaults(cfg VariantConfig) VariantConfig {
	if cfg.Timeout == 0 {
		cfg.Timeout = c.config.DefaultTimeout
	}
	if cfg.IsolationLevel == IsolationLevelNone && c.config.DefaultIsolation != IsolationLevelNone {
		cfg.IsolationLevel = c.config.DefaultIsolation
	}
	if cfg.ResourceLimits == (ResourceLimits{}) {
		cfg.ResourceLimits = c.config.DefaultResourceLimits
	}
	return cfg
}

func (c *VariantController) createVariantVFS(ctx context.Context, cfg VariantConfig, id VariantID) (any, error) {
	if c.vfsFactory == nil {
		return nil, nil
	}
	return c.vfsFactory.CreateVariantVFS(ctx, cfg.BasePipelineID, id, cfg.IsolationLevel)
}

func (c *VariantController) cleanupVFS(ctx context.Context, vfs any) {
	if c.vfsFactory != nil && vfs != nil {
		c.vfsFactory.CloseVFS(ctx, vfs)
	}
}

func (c *VariantController) StartVariant(ctx context.Context, id VariantID) error {
	if c.closed.Load() {
		return ErrVariantClosed
	}

	variant, err := c.registry.Get(id)
	if err != nil {
		return err
	}

	if variant.State != VariantStateCreated {
		return ErrVariantInvalidState
	}

	if err := c.registry.UpdateState(id, VariantStateActive); err != nil {
		return err
	}

	c.activeCount.Add(1)

	if variant.Timeout > 0 {
		go c.watchTimeout(ctx, id, variant.Timeout)
	}

	return nil
}

func (c *VariantController) watchTimeout(ctx context.Context, id VariantID, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-timer.C:
		c.handleTimeout(ctx, id)
	case <-ctx.Done():
		return
	}
}

func (c *VariantController) handleTimeout(ctx context.Context, id VariantID) {
	variant, err := c.registry.Get(id)
	if err != nil {
		return
	}

	if !variant.State.IsTerminal() {
		c.FailVariant(ctx, id, fmt.Errorf("variant timeout exceeded"))
	}
}

func (c *VariantController) StopVariant(ctx context.Context, id VariantID) error {
	if c.closed.Load() {
		return ErrVariantClosed
	}

	variant, err := c.registry.Get(id)
	if err != nil {
		return err
	}

	if variant.State != VariantStateActive {
		return ErrVariantInvalidState
	}

	if err := c.registry.UpdateState(id, VariantStateComplete); err != nil {
		return err
	}

	c.activeCount.Add(-1)

	if variant.AutoMerge && c.merger != nil {
		go c.autoMerge(ctx, id)
	}

	return nil
}

func (c *VariantController) autoMerge(ctx context.Context, id VariantID) {
	_, err := c.merger.MergeToMain(ctx, id, MergeStrategyAuto)
	if err != nil {
		c.FailVariant(ctx, id, fmt.Errorf("auto-merge failed: %w", err))
	}
}

func (c *VariantController) SuspendVariant(ctx context.Context, id VariantID) error {
	if c.closed.Load() {
		return ErrVariantClosed
	}

	variant, err := c.registry.Get(id)
	if err != nil {
		return err
	}

	if variant.State != VariantStateActive {
		return ErrVariantInvalidState
	}

	if err := c.registry.UpdateState(id, VariantStateSuspended); err != nil {
		return err
	}

	c.activeCount.Add(-1)
	return nil
}

func (c *VariantController) ResumeVariant(ctx context.Context, id VariantID) error {
	if c.closed.Load() {
		return ErrVariantClosed
	}

	if err := c.checkResourceAvailability(); err != nil {
		return err
	}

	variant, err := c.registry.Get(id)
	if err != nil {
		return err
	}

	if variant.State != VariantStateSuspended {
		return ErrVariantInvalidState
	}

	if err := c.registry.UpdateState(id, VariantStateActive); err != nil {
		return err
	}

	c.activeCount.Add(1)
	return nil
}

func (c *VariantController) CancelVariant(ctx context.Context, id VariantID) error {
	if c.closed.Load() {
		return ErrVariantClosed
	}

	variant, err := c.registry.Get(id)
	if err != nil {
		return err
	}

	if variant.State.IsTerminal() {
		return ErrVariantInvalidState
	}

	wasActive := variant.State == VariantStateActive

	if err := c.registry.UpdateState(id, VariantStateCancelled); err != nil {
		return err
	}

	if wasActive {
		c.activeCount.Add(-1)
	}

	c.cleanupVariantResources(ctx, variant)
	return nil
}

func (c *VariantController) cleanupVariantResources(ctx context.Context, variant *Variant) {
	if vfs := variant.GetVFS(); vfs != nil {
		c.cleanupVFS(ctx, vfs)
	}
}

func (c *VariantController) CompleteVariant(ctx context.Context, id VariantID) error {
	if c.closed.Load() {
		return ErrVariantClosed
	}

	variant, err := c.registry.Get(id)
	if err != nil {
		return err
	}

	if variant.State != VariantStateActive {
		return ErrVariantInvalidState
	}

	if err := c.registry.UpdateState(id, VariantStateComplete); err != nil {
		return err
	}

	c.activeCount.Add(-1)

	if variant.AutoMerge && c.merger != nil {
		go c.autoMerge(ctx, id)
	}

	return nil
}

func (c *VariantController) FailVariant(ctx context.Context, id VariantID, variantErr error) error {
	if c.closed.Load() {
		return ErrVariantClosed
	}

	variant, err := c.registry.Get(id)
	if err != nil {
		return err
	}

	if variant.State.IsTerminal() {
		return nil
	}

	wasActive := variant.State == VariantStateActive

	if variant.Metrics != nil && variantErr != nil {
		variant.Metrics.RecordError(variantErr)
	}

	if err := c.registry.UpdateState(id, VariantStateFailed); err != nil {
		return err
	}

	if wasActive {
		c.activeCount.Add(-1)
	}

	c.cleanupVariantResources(ctx, variant)
	return nil
}

func (c *VariantController) GetVariantVFS(id VariantID) (any, error) {
	if c.closed.Load() {
		return nil, ErrVariantClosed
	}

	variant, err := c.registry.Get(id)
	if err != nil {
		return nil, err
	}

	return variant.GetVFS(), nil
}

func (c *VariantController) GetVariant(id VariantID) (*Variant, error) {
	if c.closed.Load() {
		return nil, ErrVariantClosed
	}

	return c.registry.Get(id)
}

func (c *VariantController) ListVariants(filter *VariantFilter) []*VariantInfo {
	if c.closed.Load() {
		return nil
	}

	return c.registry.List(filter)
}

func (c *VariantController) Stats() ControllerStats {
	regStats := c.registry.Stats()

	return ControllerStats{
		MaxConcurrent:     c.config.MaxConcurrentVariants,
		CurrentActive:     int(c.activeCount.Load()),
		TotalVariants:     regStats.TotalVariants,
		ActiveVariants:    regStats.ActiveVariants,
		SuspendedVariants: regStats.SuspendedVariants,
		CompletedVariants: regStats.CompletedVariants,
		FailedVariants:    regStats.FailedVariants,
		MergedVariants:    regStats.MergedVariants,
		CancelledVariants: regStats.CancelledVariants,
	}
}

func (c *VariantController) Close() error {
	if c.closed.Swap(true) {
		return nil
	}

	if c.cleanupTicker != nil {
		c.cleanupTicker.Stop()
		close(c.cleanupDone)
	}

	c.cancelAllActive()

	return c.registry.Close()
}

func (c *VariantController) cancelAllActive() {
	ctx := context.Background()
	activeVariants := c.registry.List(&VariantFilter{
		States: []VariantState{VariantStateActive, VariantStateSuspended},
	})

	for _, info := range activeVariants {
		c.CancelVariant(ctx, info.ID)
	}
}

type ControllerStats struct {
	MaxConcurrent     int
	CurrentActive     int
	TotalVariants     int
	ActiveVariants    int
	SuspendedVariants int
	CompletedVariants int
	FailedVariants    int
	MergedVariants    int
	CancelledVariants int
}

type ResourceAllocator struct {
	mu          sync.RWMutex
	totalMemory int64
	totalCPU    float64
	totalDisk   int64
	usedMemory  int64
	usedCPU     float64
	usedDisk    int64
	allocations map[VariantID]*ResourceAllocation
}

type ResourceAllocation struct {
	VariantID VariantID
	Memory    int64
	CPU       float64
	Disk      int64
	AllocAt   time.Time
}

func NewResourceAllocator(totalMemory, totalDisk int64, totalCPU float64) *ResourceAllocator {
	return &ResourceAllocator{
		totalMemory: totalMemory,
		totalCPU:    totalCPU,
		totalDisk:   totalDisk,
		allocations: make(map[VariantID]*ResourceAllocation),
	}
}

func (r *ResourceAllocator) Allocate(variantID VariantID, limits ResourceLimits) (*ResourceAllocation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.usedMemory+limits.MaxMemoryMB > r.totalMemory {
		return nil, ErrVariantResourceLimit
	}
	if r.usedCPU+limits.MaxCPUPercent > r.totalCPU {
		return nil, ErrVariantResourceLimit
	}
	if r.usedDisk+limits.MaxDiskMB > r.totalDisk {
		return nil, ErrVariantResourceLimit
	}

	allocation := &ResourceAllocation{
		VariantID: variantID,
		Memory:    limits.MaxMemoryMB,
		CPU:       limits.MaxCPUPercent,
		Disk:      limits.MaxDiskMB,
		AllocAt:   time.Now(),
	}

	r.usedMemory += limits.MaxMemoryMB
	r.usedCPU += limits.MaxCPUPercent
	r.usedDisk += limits.MaxDiskMB
	r.allocations[variantID] = allocation

	return allocation, nil
}

func (r *ResourceAllocator) Release(variantID VariantID) {
	r.mu.Lock()
	defer r.mu.Unlock()

	allocation, exists := r.allocations[variantID]
	if !exists {
		return
	}

	r.usedMemory -= allocation.Memory
	r.usedCPU -= allocation.CPU
	r.usedDisk -= allocation.Disk
	delete(r.allocations, variantID)
}

func (r *ResourceAllocator) GetUsage() (memory, disk int64, cpu float64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.usedMemory, r.usedDisk, r.usedCPU
}

func (r *ResourceAllocator) GetAvailable() (memory, disk int64, cpu float64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.totalMemory - r.usedMemory, r.totalDisk - r.usedDisk, r.totalCPU - r.usedCPU
}
