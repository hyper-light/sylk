package resources

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/signal"
)

type ResourceBroker struct {
	mu            sync.Mutex
	memoryMonitor *MemoryMonitor
	filePool      *ResourcePool
	networkPool   *ResourcePool
	processPool   *ResourcePool
	diskQuota     *DiskQuotaManager

	allocations map[string]*AllocationSet
	waitGraph   map[string][]string

	signalBus *signal.SignalBus
	config    ResourceBrokerConfig
	closed    bool
	stopCh    chan struct{}
	done      chan struct{}

	// scope is the optional GoroutineScope for tracking the deadlock detection goroutine.
	// W12.33: Ensures proper goroutine lifecycle management.
	scope *concurrency.GoroutineScope

	// usesScope indicates whether the deadlock detector is managed by GoroutineScope.
	// When true, Close() should not wait on done channel.
	usesScope bool
}

type ResourceBrokerConfig struct {
	AcquisitionTimeout time.Duration
	DeadlockDetection  bool
	DetectionInterval  time.Duration

	// Scope is the optional GoroutineScope for tracking the deadlock detection goroutine.
	// W12.33: When provided, the deadlock detector is tracked via GoroutineScope.
	Scope *concurrency.GoroutineScope
}

func DefaultBrokerConfig() ResourceBrokerConfig {
	return ResourceBrokerConfig{
		AcquisitionTimeout: 30 * time.Second,
		DeadlockDetection:  true,
		DetectionInterval:  5 * time.Second,
	}
}

type ResourceBundle struct {
	FileHandles    int
	NetworkConns   int
	Subprocesses   int
	MemoryEstimate int64
	DiskEstimate   int64
}

type AllocationSet struct {
	PipelineID   string
	FileHandles  []*ResourceHandle
	NetworkConns []*ResourceHandle
	Subprocesses []*ResourceHandle
	AcquiredAt   time.Time
}

func NewResourceBroker(
	memMon *MemoryMonitor,
	filePool *ResourcePool,
	netPool *ResourcePool,
	procPool *ResourcePool,
	diskQuota *DiskQuotaManager,
	signalBus *signal.SignalBus,
	config ResourceBrokerConfig,
) *ResourceBroker {
	b := &ResourceBroker{
		memoryMonitor: memMon,
		filePool:      filePool,
		networkPool:   netPool,
		processPool:   procPool,
		diskQuota:     diskQuota,
		allocations:   make(map[string]*AllocationSet),
		waitGraph:     make(map[string][]string),
		signalBus:     signalBus,
		config:        config,
		stopCh:        make(chan struct{}),
		done:          make(chan struct{}),
		scope:         config.Scope,
	}

	if config.DeadlockDetection {
		b.startDeadlockDetector()
	}

	return b
}

// startDeadlockDetector starts the deadlock detection goroutine.
// W12.33: Uses GoroutineScope when available for proper tracking.
func (b *ResourceBroker) startDeadlockDetector() {
	if b.scope != nil {
		// Use GoroutineScope for tracked goroutine lifecycle
		err := b.scope.Go("broker-deadlock-detector", b.config.DetectionInterval*10, b.deadlockDetectorWork)
		if err != nil {
			// Fallback to untracked if scope rejects (e.g., shutdown)
			go b.deadlockDetectionLoop()
		} else {
			b.usesScope = true
		}
	} else {
		// Fallback: untracked goroutine when no scope provided
		go b.deadlockDetectionLoop()
	}
}

// deadlockDetectorWork is the GoroutineScope-compatible work function.
// It runs the deadlock detection loop and respects context cancellation.
func (b *ResourceBroker) deadlockDetectorWork(ctx context.Context) error {
	ticker := time.NewTicker(b.config.DetectionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.stopCh:
			return nil
		case <-ticker.C:
			b.detectDeadlocks()
		}
	}
}

func (b *ResourceBroker) deadlockDetectionLoop() {
	defer close(b.done)

	ticker := time.NewTicker(b.config.DetectionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.stopCh:
			return
		case <-ticker.C:
			b.detectDeadlocks()
		}
	}
}

func (b *ResourceBroker) detectDeadlocks() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.waitGraph) == 0 {
		return
	}

	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	for node := range b.waitGraph {
		if b.hasCycle(node, visited, recStack) {
			b.alertDeadlock(node)
		}
	}
}

func (b *ResourceBroker) hasCycle(node string, visited, recStack map[string]bool) bool {
	if recStack[node] {
		return true
	}
	if visited[node] {
		return false
	}

	visited[node] = true
	recStack[node] = true

	for _, neighbor := range b.waitGraph[node] {
		if b.hasCycle(neighbor, visited, recStack) {
			return true
		}
	}

	recStack[node] = false
	return false
}

func (b *ResourceBroker) alertDeadlock(node string) {
	if b.signalBus == nil {
		return
	}

	msg := signal.NewSignalMessage(signal.QuotaWarning, node, false)
	msg.Reason = "potential deadlock detected"
	_ = b.signalBus.Broadcast(*msg)
}

func (b *ResourceBroker) AcquireBundle(
	ctx context.Context,
	pipelineID string,
	bundle ResourceBundle,
	priority int,
) (*AllocationSet, error) {
	if err := b.checkClosed(); err != nil {
		return nil, err
	}

	if err := b.checkResourceAvailability(bundle); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, b.config.AcquisitionTimeout)
	defer cancel()

	alloc, err := b.acquireAllResources(ctx, pipelineID, bundle, priority)
	if err != nil {
		return nil, err
	}

	b.trackAllocation(pipelineID, alloc)
	return alloc, nil
}

func (b *ResourceBroker) checkClosed() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return fmt.Errorf("broker is closed")
	}
	return nil
}

func (b *ResourceBroker) checkResourceAvailability(bundle ResourceBundle) error {
	if b.memoryMonitor != nil && !b.memoryMonitor.CanAllocate(bundle.MemoryEstimate) {
		return fmt.Errorf("insufficient memory")
	}

	if b.diskQuota != nil && !b.diskQuota.CanWrite(bundle.DiskEstimate) {
		return fmt.Errorf("insufficient disk quota")
	}

	return nil
}

func (b *ResourceBroker) acquireAllResources(
	ctx context.Context,
	pipelineID string,
	bundle ResourceBundle,
	priority int,
) (*AllocationSet, error) {
	alloc := &AllocationSet{
		PipelineID: pipelineID,
		AcquiredAt: time.Now(),
	}

	var err error

	alloc.FileHandles, err = b.acquireN(ctx, b.filePool, bundle.FileHandles, priority)
	if err != nil {
		return nil, fmt.Errorf("file handles: %w", err)
	}

	alloc.NetworkConns, err = b.acquireN(ctx, b.networkPool, bundle.NetworkConns, priority)
	if err != nil {
		b.releaseHandles(b.filePool, alloc.FileHandles)
		return nil, fmt.Errorf("network connections: %w", err)
	}

	alloc.Subprocesses, err = b.acquireN(ctx, b.processPool, bundle.Subprocesses, priority)
	if err != nil {
		b.releaseHandles(b.filePool, alloc.FileHandles)
		b.releaseHandles(b.networkPool, alloc.NetworkConns)
		return nil, fmt.Errorf("subprocesses: %w", err)
	}

	return alloc, nil
}

func (b *ResourceBroker) trackAllocation(pipelineID string, alloc *AllocationSet) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.allocations[pipelineID] = alloc
}

func (b *ResourceBroker) AcquireUser(ctx context.Context, bundle ResourceBundle) (*AllocationSet, error) {
	if err := b.checkClosed(); err != nil {
		return nil, err
	}

	alloc := &AllocationSet{
		PipelineID: "user",
		AcquiredAt: time.Now(),
	}

	var err error

	alloc.FileHandles, err = b.acquireUserN(ctx, b.filePool, bundle.FileHandles)
	if err != nil {
		return nil, fmt.Errorf("file handles: %w", err)
	}

	alloc.NetworkConns, err = b.acquireUserN(ctx, b.networkPool, bundle.NetworkConns)
	if err != nil {
		b.releaseHandles(b.filePool, alloc.FileHandles)
		return nil, fmt.Errorf("network connections: %w", err)
	}

	alloc.Subprocesses, err = b.acquireUserN(ctx, b.processPool, bundle.Subprocesses)
	if err != nil {
		b.releaseHandles(b.filePool, alloc.FileHandles)
		b.releaseHandles(b.networkPool, alloc.NetworkConns)
		return nil, fmt.Errorf("subprocesses: %w", err)
	}

	return alloc, nil
}

func (b *ResourceBroker) acquireUserN(ctx context.Context, pool *ResourcePool, count int) ([]*ResourceHandle, error) {
	if count <= 0 || pool == nil {
		return nil, nil
	}

	handles := make([]*ResourceHandle, 0, count)
	for i := 0; i < count; i++ {
		handle, err := pool.AcquireUser(ctx)
		if err != nil {
			for _, h := range handles {
				_ = pool.Release(h)
			}
			return nil, err
		}
		handles = append(handles, handle)
	}
	return handles, nil
}

func (b *ResourceBroker) acquireN(ctx context.Context, pool *ResourcePool, count int, priority int) ([]*ResourceHandle, error) {
	if count <= 0 || pool == nil {
		return nil, nil
	}

	handles := make([]*ResourceHandle, 0, count)
	for i := 0; i < count; i++ {
		handle, err := pool.AcquirePipeline(ctx, priority)
		if err != nil {
			for _, h := range handles {
				_ = pool.Release(h)
			}
			return nil, err
		}
		handles = append(handles, handle)
	}
	return handles, nil
}

func (b *ResourceBroker) releaseHandles(pool *ResourcePool, handles []*ResourceHandle) {
	if pool == nil {
		return
	}
	for _, h := range handles {
		_ = pool.Release(h)
	}
}

func (b *ResourceBroker) ReleaseBundle(alloc *AllocationSet) error {
	if alloc == nil {
		return nil
	}

	b.mu.Lock()
	delete(b.allocations, alloc.PipelineID)
	b.mu.Unlock()

	b.releaseHandles(b.filePool, alloc.FileHandles)
	b.releaseHandles(b.networkPool, alloc.NetworkConns)
	b.releaseHandles(b.processPool, alloc.Subprocesses)

	return nil
}

func (b *ResourceBroker) ReleaseBundleByID(pipelineID string) error {
	b.mu.Lock()
	alloc, ok := b.allocations[pipelineID]
	if !ok {
		b.mu.Unlock()
		return nil
	}
	delete(b.allocations, pipelineID)
	b.mu.Unlock()

	b.releaseHandles(b.filePool, alloc.FileHandles)
	b.releaseHandles(b.networkPool, alloc.NetworkConns)
	b.releaseHandles(b.processPool, alloc.Subprocesses)

	return nil
}

func (b *ResourceBroker) GetAllocation(pipelineID string) (*AllocationSet, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	alloc, ok := b.allocations[pipelineID]
	return alloc, ok
}

func (b *ResourceBroker) AddWaitEdge(waiter, holder string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.waitGraph[waiter] = append(b.waitGraph[waiter], holder)
}

func (b *ResourceBroker) RemoveWaitEdge(waiter string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.waitGraph, waiter)
}

func (b *ResourceBroker) Stats() BrokerStats {
	b.mu.Lock()
	defer b.mu.Unlock()

	stats := BrokerStats{
		ActiveAllocations: len(b.allocations),
		WaitGraphSize:     len(b.waitGraph),
	}

	if b.filePool != nil {
		stats.FilePool = b.filePool.Stats()
	}
	if b.networkPool != nil {
		stats.NetworkPool = b.networkPool.Stats()
	}
	if b.processPool != nil {
		stats.ProcessPool = b.processPool.Stats()
	}

	return stats
}

func (b *ResourceBroker) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	usesScope := b.usesScope

	for pipelineID, alloc := range b.allocations {
		b.releaseHandles(b.filePool, alloc.FileHandles)
		b.releaseHandles(b.networkPool, alloc.NetworkConns)
		b.releaseHandles(b.processPool, alloc.Subprocesses)
		delete(b.allocations, pipelineID)
	}
	b.mu.Unlock()

	close(b.stopCh)

	// W12.33: Only wait on done channel if not using GoroutineScope.
	// When using scope, the goroutine lifecycle is managed by the scope.
	if b.config.DeadlockDetection && !usesScope {
		<-b.done
	}

	return nil
}

type BrokerStats struct {
	ActiveAllocations int       `json:"active_allocations"`
	WaitGraphSize     int       `json:"wait_graph_size"`
	FilePool          PoolStats `json:"file_pool"`
	NetworkPool       PoolStats `json:"network_pool"`
	ProcessPool       PoolStats `json:"process_pool"`
}
