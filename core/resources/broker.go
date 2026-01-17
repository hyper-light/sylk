package resources

import (
	"context"
	"fmt"
	"sync"
	"time"

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
}

type ResourceBrokerConfig struct {
	AcquisitionTimeout time.Duration
	DeadlockDetection  bool
	DetectionInterval  time.Duration
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
	return &ResourceBroker{
		memoryMonitor: memMon,
		filePool:      filePool,
		networkPool:   netPool,
		processPool:   procPool,
		diskQuota:     diskQuota,
		allocations:   make(map[string]*AllocationSet),
		waitGraph:     make(map[string][]string),
		signalBus:     signalBus,
		config:        config,
	}
}

func (b *ResourceBroker) AcquireBundle(
	ctx context.Context,
	pipelineID string,
	bundle ResourceBundle,
	priority int,
) (*AllocationSet, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, fmt.Errorf("broker is closed")
	}
	b.mu.Unlock()

	if b.memoryMonitor != nil && !b.memoryMonitor.CanAllocate(bundle.MemoryEstimate) {
		return nil, fmt.Errorf("insufficient memory")
	}

	if b.diskQuota != nil && !b.diskQuota.CanWrite(bundle.DiskEstimate) {
		return nil, fmt.Errorf("insufficient disk quota")
	}

	ctx, cancel := context.WithTimeout(ctx, b.config.AcquisitionTimeout)
	defer cancel()

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

	b.mu.Lock()
	b.allocations[pipelineID] = alloc
	b.mu.Unlock()

	return alloc, nil
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

func (b *ResourceBroker) ReleaseBundle(pipelineID string) error {
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

func (b *ResourceBroker) Stats() BrokerStats {
	b.mu.Lock()
	defer b.mu.Unlock()

	stats := BrokerStats{
		ActiveAllocations: len(b.allocations),
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
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}
	b.closed = true

	for pipelineID, alloc := range b.allocations {
		b.releaseHandles(b.filePool, alloc.FileHandles)
		b.releaseHandles(b.networkPool, alloc.NetworkConns)
		b.releaseHandles(b.processPool, alloc.Subprocesses)
		delete(b.allocations, pipelineID)
	}

	return nil
}

type BrokerStats struct {
	ActiveAllocations int       `json:"active_allocations"`
	FilePool          PoolStats `json:"file_pool"`
	NetworkPool       PoolStats `json:"network_pool"`
	ProcessPool       PoolStats `json:"process_pool"`
}
