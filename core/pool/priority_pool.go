package pool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

type Priority int

const (
	PriorityBackground Priority = iota
	PriorityLow
	PriorityNormal
	PriorityHigh
	PriorityCritical
)

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

type PriorityJob struct {
	ID        string
	Priority  Priority
	Execute   func(ctx context.Context) error
	OnError   func(error)
	SessionID string
	CreatedAt time.Time
}

type PriorityPool struct {
	mu sync.Mutex

	name         string
	numWorkers   int
	maxQueueSize int

	lanes    [5][]*PriorityJob
	queueLen int

	jobReady chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	running atomic.Bool
	closed  atomic.Bool

	jobsSubmitted int64
	jobsCompleted int64
	jobsFailed    int64
	jobsDropped   int64

	priorityStats [5]priorityLevelStats

	waitBuckets map[string]int64
	runBuckets  map[string]int64

	promotionInterval time.Duration
	lastPromotion     time.Time
}

type priorityLevelStats struct {
	submitted int64
	completed int64
	waitTime  int64
}

type PriorityPoolConfig struct {
	Name              string
	NumWorkers        int
	MaxQueueSize      int
	PromotionInterval time.Duration
}

func DefaultPriorityPoolConfig() PriorityPoolConfig {
	return PriorityPoolConfig{
		Name:              "priority-pool",
		NumWorkers:        4,
		MaxQueueSize:      1000,
		PromotionInterval: 5 * time.Second,
	}
}

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
		jobReady:          make(chan struct{}, cfg.MaxQueueSize),
		ctx:               ctx,
		cancel:            cancel,
		promotionInterval: cfg.PromotionInterval,
		lastPromotion:     time.Now(),
		waitBuckets:       make(map[string]int64),
		runBuckets:        make(map[string]int64),
	}

	return pool
}

func (p *PriorityPool) Start() {
	if p.running.Swap(true) {
		return
	}

	for i := 0; i < p.numWorkers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	p.wg.Add(1)
	go p.promotionWorker()
}

func (p *PriorityPool) Stop() {
	if !p.running.Swap(false) {
		return
	}

	p.cancel()
	close(p.jobReady)
	p.wg.Wait()
}

func (p *PriorityPool) Close() error {
	if p.closed.Swap(true) {
		return ErrPoolClosed
	}

	p.Stop()
	return nil
}

func (p *PriorityPool) Submit(job *PriorityJob) bool {
	if !p.canAcceptJob(job) {
		return false
	}

	accepted := p.submitIfAvailable(job)
	if !accepted {
		atomic.AddInt64(&p.jobsDropped, 1)
	}
	return accepted
}

func (p *PriorityPool) SubmitBlocking(job *PriorityJob) bool {
	if !p.canAcceptJob(job) {
		return false
	}

	for {
		if p.submitIfAvailable(job) {
			return true
		}

		if p.waitForCapacity(10 * time.Millisecond) {
			continue
		}
		return false
	}
}

func (p *PriorityPool) SubmitWithTimeout(job *PriorityJob, timeout time.Duration) bool {
	if !p.canAcceptJob(job) {
		return false
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p.submitIfAvailable(job) {
			return true
		}

		remaining := time.Until(deadline)
		if p.waitForCapacity(p.waitDuration(remaining)) {
			continue
		}
		return false
	}

	atomic.AddInt64(&p.jobsDropped, 1)
	return false
}

func (p *PriorityPool) canAcceptJob(job *PriorityJob) bool {
	if !p.running.Load() || p.closed.Load() {
		return false
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now()
	}
	return true
}

func (p *PriorityPool) submitIfAvailable(job *PriorityJob) bool {
	p.mu.Lock()
	if p.queueLen >= p.maxQueueSize {
		p.mu.Unlock()
		return false
	}

	lane := p.normalizeLane(job.Priority)
	p.lanes[lane] = append(p.lanes[lane], job)
	p.queueLen++
	atomic.AddInt64(&p.jobsSubmitted, 1)
	atomic.AddInt64(&p.priorityStats[lane].submitted, 1)
	p.mu.Unlock()

	p.signalJobReady()
	return true
}

func (p *PriorityPool) normalizeLane(priority Priority) int {
	lane := int(priority)
	if lane < 0 {
		return 0
	}
	if lane > int(PriorityCritical) {
		return int(PriorityCritical)
	}
	return lane
}

func (p *PriorityPool) signalJobReady() {
	select {
	case p.jobReady <- struct{}{}:
	default:
	}
}

func (p *PriorityPool) waitForCapacity(waitTime time.Duration) bool {
	select {
	case <-time.After(waitTime):
		return true
	case <-p.ctx.Done():
		return false
	}
}

func (p *PriorityPool) waitDuration(remaining time.Duration) time.Duration {
	waitTime := 10 * time.Millisecond
	if remaining < waitTime {
		return remaining
	}
	return waitTime
}

func (p *PriorityPool) worker(id int) {
	defer p.wg.Done()

	for {
		if !p.waitForJobReady() {
			return
		}

		job := p.popJob()
		if job == nil {
			continue
		}

		p.trackWaitTime(job)
		err, runDuration := p.runJob(job)
		p.recordRunBucket(runDuration)
		p.handleJobResult(job, err)
	}
}

func (p *PriorityPool) waitForJobReady() bool {
	select {
	case <-p.ctx.Done():
		return false
	case _, ok := <-p.jobReady:
		return ok
	}
}

func (p *PriorityPool) trackWaitTime(job *PriorityJob) {
	waitTime := time.Since(job.CreatedAt)
	atomic.AddInt64(&p.priorityStats[job.Priority].waitTime, int64(waitTime))
	p.recordWaitBucket(waitTime)
}

func (p *PriorityPool) runJob(job *PriorityJob) (error, time.Duration) {
	start := time.Now()
	err := p.executeJob(job)
	return err, time.Since(start)
}

func (p *PriorityPool) handleJobResult(job *PriorityJob, err error) {
	if err != nil {
		atomic.AddInt64(&p.jobsFailed, 1)
		if job.OnError != nil {
			job.OnError(err)
		}
		return
	}

	atomic.AddInt64(&p.jobsCompleted, 1)
	atomic.AddInt64(&p.priorityStats[job.Priority].completed, 1)
}

func (p *PriorityPool) popJob() *PriorityJob {
	p.mu.Lock()
	defer p.mu.Unlock()

	for priority := PriorityCritical; priority >= PriorityBackground; priority-- {
		lane := int(priority)
		if len(p.lanes[lane]) == 0 {
			continue
		}
		job := p.lanes[lane][0]
		p.lanes[lane] = p.lanes[lane][1:]
		p.queueLen--
		return job
	}

	return nil
}

func (p *PriorityPool) executeJob(job *PriorityJob) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("job panicked")
		}
	}()

	return job.Execute(p.ctx)
}

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

func (p *PriorityPool) promoteAgedJobs() {
	p.mu.Lock()
	defer p.mu.Unlock()

	threshold := time.Now().Add(-p.promotionInterval * 2)

	for prio := PriorityBackground; prio < PriorityCritical; prio++ {
		lane := int(prio)
		if len(p.lanes[lane]) == 0 {
			continue
		}

		keep := p.lanes[lane][:0]
		for _, job := range p.lanes[lane] {
			if job.CreatedAt.Before(threshold) {
				p.lanes[lane+1] = append(p.lanes[lane+1], job)
			} else {
				keep = append(keep, job)
			}
		}
		p.lanes[lane] = keep
	}

	p.lastPromotion = time.Now()
}

func (p *PriorityPool) recordWaitBucket(d time.Duration) {
	bucket := latencyBucket(d)
	p.mu.Lock()
	p.waitBuckets[bucket]++
	p.mu.Unlock()
}

func (p *PriorityPool) recordRunBucket(d time.Duration) {
	bucket := latencyBucket(d)
	p.mu.Lock()
	p.runBuckets[bucket]++
	p.mu.Unlock()
}

func latencyBucket(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	if d <= 10*time.Millisecond {
		return "<=10ms"
	}
	if d <= 50*time.Millisecond {
		return "<=50ms"
	}
	if d <= 100*time.Millisecond {
		return "<=100ms"
	}
	if d <= 250*time.Millisecond {
		return "<=250ms"
	}
	if d <= time.Second {
		return "<=1s"
	}
	return ">1s"
}

func (p *PriorityPool) Stats() PriorityPoolStats {
	p.mu.Lock()
	queueLen := p.queueLen
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
		WaitBuckets:   make(map[string]int64),
		RunBuckets:    make(map[string]int64),
	}

	for i, ps := range p.priorityStats {
		priority := Priority(i)
		stats.PriorityStats[priority.String()] = PriorityLevelStats{
			Submitted:   atomic.LoadInt64(&ps.submitted),
			Completed:   atomic.LoadInt64(&ps.completed),
			AvgWaitTime: time.Duration(atomic.LoadInt64(&ps.waitTime)),
		}
	}

	for key, value := range p.waitBuckets {
		stats.WaitBuckets[key] = value
	}
	for key, value := range p.runBuckets {
		stats.RunBuckets[key] = value
	}

	return stats
}

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
	WaitBuckets   map[string]int64              `json:"wait_buckets"`
	RunBuckets    map[string]int64              `json:"run_buckets"`
}

type PriorityLevelStats struct {
	Submitted   int64         `json:"submitted"`
	Completed   int64         `json:"completed"`
	AvgWaitTime time.Duration `json:"avg_wait_time"`
}

var (
	ErrPoolClosed = errors.New("pool is closed")
	ErrPoolFull   = errors.New("pool queue is full")
)
