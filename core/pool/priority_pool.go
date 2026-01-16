package pool

import (
	"container/heap"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Priority Levels
// =============================================================================

// Priority represents job priority levels
type Priority int

const (
	// PriorityBackground is the lowest priority
	PriorityBackground Priority = iota
	// PriorityLow is low priority
	PriorityLow
	// PriorityNormal is normal priority
	PriorityNormal
	// PriorityHigh is high priority
	PriorityHigh
	// PriorityCritical is the highest priority
	PriorityCritical
)

// String returns the string representation of a priority
func (p Priority) String() string {
	switch p {
	case PriorityBackground:
		return "background"
	case PriorityLow:
		return "low"
	case PriorityNormal:
		return "normal"
	case PriorityHigh:
		return "high"
	case PriorityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// =============================================================================
// Priority Job
// =============================================================================

// PriorityJob represents a job with priority
type PriorityJob struct {
	ID        string
	Priority  Priority
	Execute   func(ctx context.Context) error
	OnError   func(error)
	SessionID string
	CreatedAt time.Time

	// Internal
	index int // Index in the heap
}

// =============================================================================
// Priority Queue (Heap)
// =============================================================================

// priorityQueue implements heap.Interface for priority-based ordering
type priorityQueue []*PriorityJob

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	// Higher priority comes first
	if pq[i].Priority != pq[j].Priority {
		return pq[i].Priority > pq[j].Priority
	}
	// Same priority: older jobs come first (age-based promotion)
	return pq[i].CreatedAt.Before(pq[j].CreatedAt)
}

func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *priorityQueue) Push(x any) {
	n := len(*pq)
	job := x.(*PriorityJob)
	job.index = n
	*pq = append(*pq, job)
}

func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	job := old[n-1]
	old[n-1] = nil  // avoid memory leak
	job.index = -1  // for safety
	*pq = old[0 : n-1]
	return job
}

// =============================================================================
// Priority Worker Pool
// =============================================================================

// PriorityPool is a worker pool with priority-based job selection
type PriorityPool struct {
	mu sync.Mutex

	// Configuration
	name        string
	numWorkers  int
	maxQueueSize int

	// Priority queue
	queue priorityQueue

	// Queue signal
	jobReady chan struct{}

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// State
	running atomic.Bool
	closed  atomic.Bool

	// Statistics
	jobsSubmitted int64
	jobsCompleted int64
	jobsFailed    int64
	jobsDropped   int64

	// Per-priority stats
	priorityStats [5]priorityLevelStats

	// Starvation prevention
	promotionInterval time.Duration
	lastPromotion     time.Time
}

type priorityLevelStats struct {
	submitted int64
	completed int64
	waitTime  int64 // Total wait time in nanoseconds
}

// PriorityPoolConfig configures a priority pool
type PriorityPoolConfig struct {
	Name              string
	NumWorkers        int
	MaxQueueSize      int
	PromotionInterval time.Duration // How often to promote aged jobs
}

// DefaultPriorityPoolConfig returns sensible defaults
func DefaultPriorityPoolConfig() PriorityPoolConfig {
	return PriorityPoolConfig{
		Name:              "priority-pool",
		NumWorkers:        4,
		MaxQueueSize:      1000,
		PromotionInterval: 5 * time.Second,
	}
}

// NewPriorityPool creates a new priority worker pool
func NewPriorityPool(cfg PriorityPoolConfig) *PriorityPool {
	if cfg.NumWorkers <= 0 {
		cfg.NumWorkers = 4
	}
	if cfg.MaxQueueSize <= 0 {
		cfg.MaxQueueSize = 1000
	}
	if cfg.PromotionInterval <= 0 {
		cfg.PromotionInterval = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	pool := &PriorityPool{
		name:              cfg.Name,
		numWorkers:        cfg.NumWorkers,
		maxQueueSize:      cfg.MaxQueueSize,
		queue:             make(priorityQueue, 0),
		jobReady:          make(chan struct{}, cfg.MaxQueueSize),
		ctx:               ctx,
		cancel:            cancel,
		promotionInterval: cfg.PromotionInterval,
		lastPromotion:     time.Now(),
	}

	heap.Init(&pool.queue)

	return pool
}

// =============================================================================
// Lifecycle
// =============================================================================

// Start begins processing jobs
func (p *PriorityPool) Start() {
	if p.running.Swap(true) {
		return
	}

	// Start workers
	for i := 0; i < p.numWorkers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	// Start promotion goroutine
	p.wg.Add(1)
	go p.promotionWorker()
}

// Stop halts all workers
func (p *PriorityPool) Stop() {
	if !p.running.Swap(false) {
		return
	}

	p.cancel()

	// Drain jobReady channel
	close(p.jobReady)

	p.wg.Wait()
}

// Close closes the pool
func (p *PriorityPool) Close() error {
	if p.closed.Swap(true) {
		return ErrPoolClosed
	}

	p.Stop()
	return nil
}

// =============================================================================
// Job Submission
// =============================================================================

// Submit adds a job to the queue
func (p *PriorityPool) Submit(job *PriorityJob) bool {
	if !p.running.Load() || p.closed.Load() {
		return false
	}

	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now()
	}

	p.mu.Lock()
	if len(p.queue) >= p.maxQueueSize {
		p.mu.Unlock()
		atomic.AddInt64(&p.jobsDropped, 1)
		return false
	}

	heap.Push(&p.queue, job)
	atomic.AddInt64(&p.jobsSubmitted, 1)
	atomic.AddInt64(&p.priorityStats[job.Priority].submitted, 1)
	p.mu.Unlock()

	// Signal that a job is ready
	select {
	case p.jobReady <- struct{}{}:
	default:
	}

	return true
}

// SubmitBlocking blocks until space is available
func (p *PriorityPool) SubmitBlocking(job *PriorityJob) bool {
	if !p.running.Load() || p.closed.Load() {
		return false
	}

	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now()
	}

	for {
		p.mu.Lock()
		if len(p.queue) < p.maxQueueSize {
			heap.Push(&p.queue, job)
			atomic.AddInt64(&p.jobsSubmitted, 1)
			atomic.AddInt64(&p.priorityStats[job.Priority].submitted, 1)
			p.mu.Unlock()

			select {
			case p.jobReady <- struct{}{}:
			default:
			}
			return true
		}
		p.mu.Unlock()

		// Wait and retry
		select {
		case <-time.After(10 * time.Millisecond):
		case <-p.ctx.Done():
			return false
		}
	}
}

// SubmitWithTimeout blocks for up to timeout
func (p *PriorityPool) SubmitWithTimeout(job *PriorityJob, timeout time.Duration) bool {
	if !p.running.Load() || p.closed.Load() {
		return false
	}

	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now()
	}

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		p.mu.Lock()
		if len(p.queue) < p.maxQueueSize {
			heap.Push(&p.queue, job)
			atomic.AddInt64(&p.jobsSubmitted, 1)
			atomic.AddInt64(&p.priorityStats[job.Priority].submitted, 1)
			p.mu.Unlock()

			select {
			case p.jobReady <- struct{}{}:
			default:
			}
			return true
		}
		p.mu.Unlock()

		// Wait and retry
		remaining := time.Until(deadline)
		waitTime := 10 * time.Millisecond
		if remaining < waitTime {
			waitTime = remaining
		}

		select {
		case <-time.After(waitTime):
		case <-p.ctx.Done():
			return false
		}
	}

	atomic.AddInt64(&p.jobsDropped, 1)
	return false
}

// =============================================================================
// Workers
// =============================================================================

// worker processes jobs from the queue
func (p *PriorityPool) worker(id int) {
	defer p.wg.Done()

	for {
		select {
		case <-p.ctx.Done():
			return
		case _, ok := <-p.jobReady:
			if !ok {
				return
			}

			job := p.popJob()
			if job == nil {
				continue
			}

			// Execute job
			waitTime := time.Since(job.CreatedAt)
			atomic.AddInt64(&p.priorityStats[job.Priority].waitTime, int64(waitTime))

			err := p.executeJob(job)
			if err != nil {
				atomic.AddInt64(&p.jobsFailed, 1)
				if job.OnError != nil {
					job.OnError(err)
				}
			} else {
				atomic.AddInt64(&p.jobsCompleted, 1)
				atomic.AddInt64(&p.priorityStats[job.Priority].completed, 1)
			}
		}
	}
}

// popJob removes and returns the highest priority job
func (p *PriorityPool) popJob() *PriorityJob {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.queue) == 0 {
		return nil
	}

	return heap.Pop(&p.queue).(*PriorityJob)
}

// executeJob executes a single job with panic recovery
func (p *PriorityPool) executeJob(job *PriorityJob) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("job panicked")
		}
	}()

	return job.Execute(p.ctx)
}

// promotionWorker periodically promotes aged low-priority jobs
func (p *PriorityPool) promotionWorker() {
	defer p.wg.Done()

	ticker := time.NewTicker(p.promotionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.promoteAgedJobs()
		}
	}
}

// promoteAgedJobs promotes jobs that have been waiting too long
func (p *PriorityPool) promoteAgedJobs() {
	p.mu.Lock()
	defer p.mu.Unlock()

	threshold := time.Now().Add(-p.promotionInterval * 2)

	for i, job := range p.queue {
		// Promote jobs that have been waiting too long
		if job.CreatedAt.Before(threshold) && job.Priority < PriorityCritical {
			p.queue[i].Priority++
		}
	}

	// Re-heapify after promotions
	heap.Init(&p.queue)
	p.lastPromotion = time.Now()
}

// =============================================================================
// Statistics
// =============================================================================

// Stats returns pool statistics
func (p *PriorityPool) Stats() PriorityPoolStats {
	p.mu.Lock()
	queueLen := len(p.queue)
	p.mu.Unlock()

	stats := PriorityPoolStats{
		Name:          p.name,
		NumWorkers:    p.numWorkers,
		MaxQueueSize:  p.maxQueueSize,
		QueueLength:   queueLen,
		Running:       p.running.Load(),
		JobsSubmitted: atomic.LoadInt64(&p.jobsSubmitted),
		JobsCompleted: atomic.LoadInt64(&p.jobsCompleted),
		JobsFailed:    atomic.LoadInt64(&p.jobsFailed),
		JobsDropped:   atomic.LoadInt64(&p.jobsDropped),
		PriorityStats: make(map[string]PriorityLevelStats),
	}

	for i, ps := range p.priorityStats {
		priority := Priority(i)
		stats.PriorityStats[priority.String()] = PriorityLevelStats{
			Submitted:   atomic.LoadInt64(&ps.submitted),
			Completed:   atomic.LoadInt64(&ps.completed),
			AvgWaitTime: time.Duration(atomic.LoadInt64(&ps.waitTime)),
		}
	}

	return stats
}

// PriorityPoolStats contains pool statistics
type PriorityPoolStats struct {
	Name          string                        `json:"name"`
	NumWorkers    int                           `json:"num_workers"`
	MaxQueueSize  int                           `json:"max_queue_size"`
	QueueLength   int                           `json:"queue_length"`
	Running       bool                          `json:"running"`
	JobsSubmitted int64                         `json:"jobs_submitted"`
	JobsCompleted int64                         `json:"jobs_completed"`
	JobsFailed    int64                         `json:"jobs_failed"`
	JobsDropped   int64                         `json:"jobs_dropped"`
	PriorityStats map[string]PriorityLevelStats `json:"priority_stats"`
}

// PriorityLevelStats contains per-priority statistics
type PriorityLevelStats struct {
	Submitted   int64         `json:"submitted"`
	Completed   int64         `json:"completed"`
	AvgWaitTime time.Duration `json:"avg_wait_time"`
}

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrPoolClosed indicates the pool is closed
	ErrPoolClosed = errors.New("pool is closed")

	// ErrPoolFull indicates the pool queue is full
	ErrPoolFull = errors.New("pool queue is full")
)
