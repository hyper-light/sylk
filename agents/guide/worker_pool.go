package guide

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type Job struct {
	ID      string
	Execute func(ctx context.Context) error
	OnError func(error)
}

type WorkerPool struct {
	// Configuration
	name       string
	numWorkers int
	queueSize  int

	// Job queue
	jobs chan *Job

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// State
	running atomic.Bool
	mu      sync.Mutex

	// Stats
	jobsSubmitted int64
	jobsCompleted int64
	jobsFailed    int64
	jobsDropped   int64
}

// WorkerPoolConfig configures a worker pool
type WorkerPoolConfig struct {
	Name       string // Pool name for identification
	NumWorkers int    // Number of workers (default: runtime.NumCPU())
	QueueSize  int    // Job queue size (default: 1000)
}

// DefaultWorkerPoolConfig returns sensible defaults
func DefaultWorkerPoolConfig() WorkerPoolConfig {
	return WorkerPoolConfig{
		Name:       "default",
		NumWorkers: 4,
		QueueSize:  1000,
	}
}

// NewWorkerPool creates a new worker pool
func NewWorkerPool(cfg WorkerPoolConfig) *WorkerPool {
	if cfg.NumWorkers <= 0 {
		cfg.NumWorkers = 4
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1000
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &WorkerPool{
		name:       cfg.Name,
		numWorkers: cfg.NumWorkers,
		queueSize:  cfg.QueueSize,
		jobs:       make(chan *Job, cfg.QueueSize),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start begins processing jobs
func (p *WorkerPool) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running.Load() {
		return
	}

	p.running.Store(true)

	// Start workers
	for i := 0; i < p.numWorkers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
}

// Stop halts all workers and waits for completion
func (p *WorkerPool) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running.Load() {
		return
	}

	p.running.Store(false)
	p.cancel()
	close(p.jobs)
	p.wg.Wait()
}

// Submit adds a job to the queue.
// Returns false if the queue is full or pool is stopped.
func (p *WorkerPool) Submit(job *Job) bool {
	if !p.running.Load() {
		return false
	}

	select {
	case p.jobs <- job:
		atomic.AddInt64(&p.jobsSubmitted, 1)
		return true
	default:
		atomic.AddInt64(&p.jobsDropped, 1)
		return false
	}
}

// SubmitBlocking adds a job to the queue, blocking if full.
// Returns false if the pool is stopped.
func (p *WorkerPool) SubmitBlocking(job *Job) bool {
	if !p.running.Load() {
		return false
	}

	select {
	case <-p.ctx.Done():
		return false
	case p.jobs <- job:
		atomic.AddInt64(&p.jobsSubmitted, 1)
		return true
	}
}

// SubmitWithTimeout adds a job with a timeout for queue space.
func (p *WorkerPool) SubmitWithTimeout(job *Job, timeout time.Duration) bool {
	if !p.running.Load() {
		return false
	}

	select {
	case p.jobs <- job:
		atomic.AddInt64(&p.jobsSubmitted, 1)
		return true
	case <-time.After(timeout):
		atomic.AddInt64(&p.jobsDropped, 1)
		return false
	case <-p.ctx.Done():
		return false
	}
}

// worker processes jobs from the queue
func (p *WorkerPool) worker(_ int) {
	defer p.wg.Done()

	for job := range p.jobs {
		if job == nil {
			continue
		}

		// Execute job with pool context
		err := job.Execute(p.ctx)
		if err != nil {
			atomic.AddInt64(&p.jobsFailed, 1)
			if job.OnError != nil {
				job.OnError(err)
			}
		} else {
			atomic.AddInt64(&p.jobsCompleted, 1)
		}
	}
}

// Stats returns pool statistics
func (p *WorkerPool) Stats() WorkerPoolStats {
	return WorkerPoolStats{
		Name:          p.name,
		NumWorkers:    p.numWorkers,
		QueueSize:     p.queueSize,
		QueueLength:   len(p.jobs),
		Running:       p.running.Load(),
		JobsSubmitted: atomic.LoadInt64(&p.jobsSubmitted),
		JobsCompleted: atomic.LoadInt64(&p.jobsCompleted),
		JobsFailed:    atomic.LoadInt64(&p.jobsFailed),
		JobsDropped:   atomic.LoadInt64(&p.jobsDropped),
	}
}

// WorkerPoolStats contains pool statistics
type WorkerPoolStats struct {
	Name          string `json:"name"`
	NumWorkers    int    `json:"num_workers"`
	QueueSize     int    `json:"queue_size"`
	QueueLength   int    `json:"queue_length"`
	Running       bool   `json:"running"`
	JobsSubmitted int64  `json:"jobs_submitted"`
	JobsCompleted int64  `json:"jobs_completed"`
	JobsFailed    int64  `json:"jobs_failed"`
	JobsDropped   int64  `json:"jobs_dropped"`
}

// =============================================================================
// Async Classification Request
// =============================================================================

type AsyncClassificationRequest struct {
	Request       *RouteRequest
	ResultChan    chan *AsyncClassificationResult
	SubmittedAt   time.Time
	CorrelationID string
}

// AsyncClassificationResult contains the result of async classification
type AsyncClassificationResult struct {
	Request        *RouteRequest
	Result         *RouteResult
	TargetAgentID  string
	Error          error
	ProcessingTime time.Duration
}

type ClassificationWorkerPool struct {
	pool       *WorkerPool
	classifier *Classifier
	cache      *RouteCache
	parser     *Parser
}

type ClassificationWorkerPoolConfig struct {
	NumWorkers int // Default: 4
	QueueSize  int // Default: 500
	Classifier *Classifier
	Cache      *RouteCache
	Parser     *Parser
}

func NewClassificationWorkerPool(cfg ClassificationWorkerPoolConfig) *ClassificationWorkerPool {
	if cfg.NumWorkers <= 0 {
		cfg.NumWorkers = 4
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 500
	}

	pool := NewWorkerPool(WorkerPoolConfig{
		Name:       "classification",
		NumWorkers: cfg.NumWorkers,
		QueueSize:  cfg.QueueSize,
	})

	return &ClassificationWorkerPool{
		pool:       pool,
		classifier: cfg.Classifier,
		cache:      cfg.Cache,
		parser:     cfg.Parser,
	}
}

// Start begins classification processing
func (p *ClassificationWorkerPool) Start() {
	p.pool.Start()
}

// Stop halts classification processing
func (p *ClassificationWorkerPool) Stop() {
	p.pool.Stop()
}

// SubmitClassification queues a classification request
// Returns a channel to receive the result
func (p *ClassificationWorkerPool) SubmitClassification(req *RouteRequest) chan *AsyncClassificationResult {
	resultChan := make(chan *AsyncClassificationResult, 1)
	submittedAt := time.Now()

	job := &Job{
		ID: req.CorrelationID,
		Execute: func(ctx context.Context) error {
			return p.executeClassification(ctx, req, resultChan)
		},
		OnError: func(err error) {
			p.reportJobError(resultChan, req, submittedAt, err)
		},
	}

	if !p.pool.Submit(job) {
		p.reportQueueFull(resultChan, req)
	}

	return resultChan
}

func (p *ClassificationWorkerPool) executeClassification(ctx context.Context, req *RouteRequest, resultChan chan *AsyncClassificationResult) error {
	result := &AsyncClassificationResult{Request: req}
	start := time.Now()

	if cached := p.classifyFromCache(req); cached != nil {
		p.completeClassification(resultChan, result, cached, time.Since(start), nil)
		return nil
	}

	if dslResult := p.classifyFromDSL(req); dslResult != nil {
		p.completeClassification(resultChan, result, dslResult, time.Since(start), nil)
		return nil
	}

	classification, err := p.classifyFromLLM(ctx, req, start)
	p.completeClassification(resultChan, result, classification, time.Since(start), err)
	return err
}

func (p *ClassificationWorkerPool) classifyFromCache(req *RouteRequest) *RouteResult {
	if p.cache == nil {
		return nil
	}
	cached := p.cache.Get(req.Input)
	if cached == nil {
		return nil
	}
	return &RouteResult{
		TargetAgent:          TargetAgent(cached.TargetAgentID),
		Intent:               cached.Intent,
		Domain:               cached.Domain,
		Confidence:           cached.Confidence,
		ClassificationMethod: "cache",
	}
}

func (p *ClassificationWorkerPool) classifyFromDSL(req *RouteRequest) *RouteResult {
	if p.parser == nil || !p.parser.IsDSL(req.Input) {
		return nil
	}
	cmd, err := p.parser.Parse(req.Input)
	if err != nil {
		return nil
	}
	return cmd.ToRouteResult()
}

func (p *ClassificationWorkerPool) classifyFromLLM(ctx context.Context, req *RouteRequest, start time.Time) (*RouteResult, error) {
	if p.classifier == nil {
		return nil, nil
	}
	classResult, err := p.classifier.Classify(ctx, req.Input)
	if err != nil {
		return nil, err
	}
	result := classResult.ToRouteResult(time.Since(start))
	p.cacheClassification(req, result)
	return result, nil
}

func (p *ClassificationWorkerPool) cacheClassification(req *RouteRequest, result *RouteResult) {
	if p.cache != nil {
		p.cache.Set(req.Input, result)
	}
}

func (p *ClassificationWorkerPool) completeClassification(resultChan chan *AsyncClassificationResult, result *AsyncClassificationResult, routeResult *RouteResult, processingTime time.Duration, err error) {
	if routeResult != nil {
		result.Result = routeResult
		result.TargetAgentID = string(routeResult.TargetAgent)
	}
	result.Error = err
	result.ProcessingTime = processingTime
	resultChan <- result
	close(resultChan)
}

func (p *ClassificationWorkerPool) reportJobError(resultChan chan *AsyncClassificationResult, req *RouteRequest, submittedAt time.Time, err error) {
	result := &AsyncClassificationResult{
		Request:        req,
		Error:          err,
		ProcessingTime: time.Since(submittedAt),
	}
	select {
	case resultChan <- result:
	default:
	}
	close(resultChan)
}

func (p *ClassificationWorkerPool) reportQueueFull(resultChan chan *AsyncClassificationResult, req *RouteRequest) {
	result := &AsyncClassificationResult{
		Request:        req,
		Error:          ErrWorkerPoolFull,
		ProcessingTime: 0,
	}
	resultChan <- result
	close(resultChan)
}

// Stats returns pool statistics
func (p *ClassificationWorkerPool) Stats() WorkerPoolStats {
	return p.pool.Stats()
}

var ErrWorkerPoolFull = errorf("worker pool queue is full")

func errorf(format string, _ ...any) error {
	return &poolError{msg: format}
}

type poolError struct {
	msg string
}

func (e *poolError) Error() string {
	return e.msg
}
