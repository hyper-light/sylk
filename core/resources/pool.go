package resources

import (
	"container/heap"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/signal"
	"github.com/google/uuid"
)

var (
	ErrPoolClosed     = errors.New("resource pool is closed")
	ErrAcquireTimeout = errors.New("acquire timeout exceeded")
	ErrInvalidHandle  = errors.New("invalid resource handle")
)

// ResourceType identifies the kind of resource.
type ResourceType string

const (
	ResourceTypeFile    ResourceType = "file"
	ResourceTypeNetwork ResourceType = "network"
	ResourceTypeProcess ResourceType = "process"
)

// ResourceHandle represents an acquired resource.
type ResourceHandle struct {
	ID         string
	Type       ResourceType
	Priority   int
	IsUser     bool
	AcquiredAt time.Time
	Data       any
}

// ResourcePoolConfig holds pool configuration.
type ResourcePoolConfig struct {
	Total               int
	UserReservedPercent float64
	PipelineTimeout     time.Duration
	SignalBus           *signal.SignalBus
}

// DefaultResourcePoolConfig returns sensible defaults.
func DefaultResourcePoolConfig(total int) ResourcePoolConfig {
	return ResourcePoolConfig{
		Total:               total,
		UserReservedPercent: 0.20,
		PipelineTimeout:     30 * time.Second,
	}
}

// waitRequest represents a queued pipeline request.
type waitRequest struct {
	priority int
	index    int
	ready    chan *ResourceHandle
	ctx      context.Context
}

// waitQueue implements heap.Interface for priority queue.
type waitQueue []*waitRequest

func (wq waitQueue) Len() int { return len(wq) }

func (wq waitQueue) Less(i, j int) bool {
	return wq[i].priority > wq[j].priority
}

func (wq waitQueue) Swap(i, j int) {
	wq[i], wq[j] = wq[j], wq[i]
	wq[i].index = i
	wq[j].index = j
}

func (wq *waitQueue) Push(x any) {
	n := len(*wq)
	item := x.(*waitRequest)
	item.index = n
	*wq = append(*wq, item)
}

func (wq *waitQueue) Pop() any {
	old := *wq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*wq = old[0 : n-1]
	return item
}

// ResourcePool manages a pool of generic resources.
type ResourcePool struct {
	mu sync.Mutex

	resourceType ResourceType
	total        int
	userReserved int
	available    int
	timeout      time.Duration

	handles   map[string]*ResourceHandle
	waitQueue waitQueue

	signalBus *signal.SignalBus
	closed    bool
}

// NewResourcePool creates a new resource pool.
func NewResourcePool(resourceType ResourceType, cfg ResourcePoolConfig) *ResourcePool {
	cfg = normalizeConfig(cfg)

	userReserved := int(float64(cfg.Total) * cfg.UserReservedPercent)
	if userReserved < 1 && cfg.Total > 0 {
		userReserved = 1
	}

	pool := &ResourcePool{
		resourceType: resourceType,
		total:        cfg.Total,
		userReserved: userReserved,
		available:    cfg.Total,
		timeout:      cfg.PipelineTimeout,
		handles:      make(map[string]*ResourceHandle),
		waitQueue:    make(waitQueue, 0),
		signalBus:    cfg.SignalBus,
	}

	heap.Init(&pool.waitQueue)
	return pool
}

func normalizeConfig(cfg ResourcePoolConfig) ResourcePoolConfig {
	cfg.Total = normalizeTotal(cfg.Total)
	cfg.UserReservedPercent = normalizePercent(cfg.UserReservedPercent)
	cfg.PipelineTimeout = normalizeTimeout(cfg.PipelineTimeout)
	return cfg
}

func normalizeTotal(total int) int {
	if total <= 0 {
		return 1
	}
	return total
}

func normalizePercent(percent float64) float64 {
	if percent <= 0 {
		return 0.20
	}
	if percent > 1.0 {
		return 1.0
	}
	return percent
}

func normalizeTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 30 * time.Second
	}
	return timeout
}

// AcquireUser acquires a resource for user operations.
// This never fails - it uses reserved capacity or preempts pipelines.
func (p *ResourcePool) AcquireUser(ctx context.Context) (*ResourceHandle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, ErrPoolClosed
	}

	if p.available > 0 {
		return p.createHandle(true, 0), nil
	}

	return p.preemptForUser()
}

func (p *ResourcePool) createHandle(isUser bool, priority int) *ResourceHandle {
	handle := &ResourceHandle{
		ID:         uuid.New().String(),
		Type:       p.resourceType,
		Priority:   priority,
		IsUser:     isUser,
		AcquiredAt: time.Now(),
	}
	p.available--
	p.handles[handle.ID] = handle
	return handle
}

func (p *ResourcePool) preemptForUser() (*ResourceHandle, error) {
	victim := p.findLowestPriorityPipeline()
	if victim == nil {
		return p.createHandle(true, 0), nil
	}

	p.sendPreemptSignal(victim)
	delete(p.handles, victim.ID)
	return p.createHandle(true, 0), nil
}

func (p *ResourcePool) findLowestPriorityPipeline() *ResourceHandle {
	var lowest *ResourceHandle
	for _, h := range p.handles {
		lowest = selectLowerPriority(lowest, h)
	}
	return lowest
}

func selectLowerPriority(current, candidate *ResourceHandle) *ResourceHandle {
	if candidate.IsUser {
		return current
	}
	if current == nil || candidate.Priority < current.Priority {
		return candidate
	}
	return current
}

func (p *ResourcePool) sendPreemptSignal(handle *ResourceHandle) {
	if p.signalBus == nil {
		return
	}

	msg := signal.NewSignalMessage(signal.PausePipeline, handle.ID, false)
	msg.Reason = "resource preemption for user"
	_ = p.signalBus.Broadcast(*msg)
}

// AcquirePipeline acquires a resource for pipeline operations.
// This may queue and timeout if no resources are available.
func (p *ResourcePool) AcquirePipeline(ctx context.Context, priority int) (*ResourceHandle, error) {
	p.mu.Lock()

	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}

	pipelineAvailable := p.available - p.userReserved
	if pipelineAvailable > 0 {
		handle := p.createHandle(false, priority)
		p.mu.Unlock()
		return handle, nil
	}

	req := p.enqueueRequest(ctx, priority)
	p.mu.Unlock()

	return p.waitForResource(ctx, req)
}

func (p *ResourcePool) enqueueRequest(ctx context.Context, priority int) *waitRequest {
	req := &waitRequest{
		priority: priority,
		ready:    make(chan *ResourceHandle, 1),
		ctx:      ctx,
	}
	heap.Push(&p.waitQueue, req)
	return req
}

func (p *ResourcePool) waitForResource(ctx context.Context, req *waitRequest) (*ResourceHandle, error) {
	timer := time.NewTimer(p.timeout)
	defer timer.Stop()

	select {
	case handle := <-req.ready:
		return handle, nil
	case <-timer.C:
		p.removeWaitRequest(req)
		return nil, ErrAcquireTimeout
	case <-ctx.Done():
		p.removeWaitRequest(req)
		return nil, ctx.Err()
	}
}

func (p *ResourcePool) removeWaitRequest(req *waitRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, r := range p.waitQueue {
		if r == req {
			heap.Remove(&p.waitQueue, i)
			return
		}
	}
}

// Release returns a resource to the pool.
func (p *ResourcePool) Release(handle *ResourceHandle) error {
	if handle == nil {
		return ErrInvalidHandle
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.handles[handle.ID]; !exists {
		return ErrInvalidHandle
	}

	delete(p.handles, handle.ID)
	p.available++

	p.notifyWaitingRequest()
	return nil
}

func (p *ResourcePool) notifyWaitingRequest() {
	if p.waitQueue.Len() == 0 {
		return
	}

	pipelineAvailable := p.available - p.userReserved
	if pipelineAvailable <= 0 {
		return
	}

	req := heap.Pop(&p.waitQueue).(*waitRequest)
	handle := p.createHandle(false, req.priority)

	select {
	case req.ready <- handle:
	default:
		p.available++
		delete(p.handles, handle.ID)
	}
}

// Stats returns current pool statistics.
func (p *ResourcePool) Stats() PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	return PoolStats{
		Type:         p.resourceType,
		Total:        p.total,
		UserReserved: p.userReserved,
		Available:    p.available,
		InUse:        p.total - p.available,
		WaitQueue:    p.waitQueue.Len(),
	}
}

// PoolStats contains pool statistics.
type PoolStats struct {
	Type         ResourceType `json:"type"`
	Total        int          `json:"total"`
	UserReserved int          `json:"user_reserved"`
	Available    int          `json:"available"`
	InUse        int          `json:"in_use"`
	WaitQueue    int          `json:"wait_queue"`
}

// Close shuts down the pool.
func (p *ResourcePool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return ErrPoolClosed
	}

	p.closed = true

	for p.waitQueue.Len() > 0 {
		req := heap.Pop(&p.waitQueue).(*waitRequest)
		close(req.ready)
	}

	return nil
}
