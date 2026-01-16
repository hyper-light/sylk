package pool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Session-Aware Pool
// =============================================================================

// SessionPool provides fair scheduling across multiple sessions
type SessionPool struct {
	mu sync.RWMutex

	// Configuration
	name         string
	numWorkers   int
	maxQueueSize int

	// Per-session queues
	sessionQueues map[string]*sessionQueue

	// Round-robin state
	sessionOrder []string
	currentIndex int

	// Job channel
	jobs chan *SessionJob

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// State
	running atomic.Bool
	closed  atomic.Bool

	// Global limits
	maxJobsPerSession int
	maxTotalJobs      int

	// Statistics
	totalSubmitted int64
	totalCompleted int64
	totalFailed    int64
	totalDropped   int64
}

// sessionQueue holds jobs for a single session
type sessionQueue struct {
	mu        sync.Mutex
	sessionID string
	jobs      []*SessionJob
	submitted int64
	completed int64
	failed    int64
}

// SessionJob represents a job with session affinity
type SessionJob struct {
	ID        string
	SessionID string
	Priority  Priority
	Execute   func(ctx context.Context) error
	OnError   func(error)
	CreatedAt time.Time
}

// SessionPoolConfig configures a session pool
type SessionPoolConfig struct {
	Name              string
	NumWorkers        int
	MaxQueueSize      int
	MaxJobsPerSession int
	MaxTotalJobs      int
}

// DefaultSessionPoolConfig returns sensible defaults
func DefaultSessionPoolConfig() SessionPoolConfig {
	return SessionPoolConfig{
		Name:              "session-pool",
		NumWorkers:        8,
		MaxQueueSize:      1000,
		MaxJobsPerSession: 100,
		MaxTotalJobs:      500,
	}
}

// NewSessionPool creates a new session-aware worker pool
func NewSessionPool(cfg SessionPoolConfig) *SessionPool {
	if cfg.NumWorkers <= 0 {
		cfg.NumWorkers = 8
	}
	if cfg.MaxQueueSize <= 0 {
		cfg.MaxQueueSize = 1000
	}
	if cfg.MaxJobsPerSession <= 0 {
		cfg.MaxJobsPerSession = 100
	}
	if cfg.MaxTotalJobs <= 0 {
		cfg.MaxTotalJobs = 500
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &SessionPool{
		name:              cfg.Name,
		numWorkers:        cfg.NumWorkers,
		maxQueueSize:      cfg.MaxQueueSize,
		sessionQueues:     make(map[string]*sessionQueue),
		sessionOrder:      make([]string, 0),
		jobs:              make(chan *SessionJob, cfg.MaxQueueSize),
		ctx:               ctx,
		cancel:            cancel,
		maxJobsPerSession: cfg.MaxJobsPerSession,
		maxTotalJobs:      cfg.MaxTotalJobs,
	}
}

// =============================================================================
// Lifecycle
// =============================================================================

// Start begins processing jobs
func (p *SessionPool) Start() {
	if p.running.Swap(true) {
		return
	}

	// Start workers
	for i := 0; i < p.numWorkers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	// Start fair scheduler
	p.wg.Add(1)
	go p.scheduler()
}

// Stop halts all workers
func (p *SessionPool) Stop() {
	if !p.running.Swap(false) {
		return
	}

	p.cancel()
	close(p.jobs)
	p.wg.Wait()
}

// Close closes the pool
func (p *SessionPool) Close() error {
	if p.closed.Swap(true) {
		return ErrPoolClosed
	}

	p.Stop()
	return nil
}

// =============================================================================
// Job Submission
// =============================================================================

// Submit adds a job to the session queue
func (p *SessionPool) Submit(job *SessionJob) bool {
	if !p.running.Load() || p.closed.Load() {
		return false
	}

	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now()
	}

	// Check global limits
	total := p.totalQueuedJobs()
	if total >= p.maxTotalJobs {
		atomic.AddInt64(&p.totalDropped, 1)
		return false
	}

	p.mu.Lock()

	// Get or create session queue
	sq, ok := p.sessionQueues[job.SessionID]
	if !ok {
		sq = &sessionQueue{
			sessionID: job.SessionID,
			jobs:      make([]*SessionJob, 0),
		}
		p.sessionQueues[job.SessionID] = sq
		p.sessionOrder = append(p.sessionOrder, job.SessionID)
	}

	// Check per-session limit
	sq.mu.Lock()
	if len(sq.jobs) >= p.maxJobsPerSession {
		sq.mu.Unlock()
		p.mu.Unlock()
		atomic.AddInt64(&p.totalDropped, 1)
		return false
	}

	sq.jobs = append(sq.jobs, job)
	sq.submitted++
	sq.mu.Unlock()

	p.mu.Unlock()

	atomic.AddInt64(&p.totalSubmitted, 1)
	return true
}

// SubmitBlocking blocks until space is available
func (p *SessionPool) SubmitBlocking(job *SessionJob) bool {
	if !p.running.Load() || p.closed.Load() {
		return false
	}

	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now()
	}

	for {
		// Try to submit
		if p.Submit(job) {
			return true
		}

		// Wait and retry
		select {
		case <-time.After(10 * time.Millisecond):
		case <-p.ctx.Done():
			return false
		}
	}
}

// =============================================================================
// Workers
// =============================================================================

// scheduler implements fair round-robin scheduling
func (p *SessionPool) scheduler() {
	defer p.wg.Done()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			job := p.selectNextJob()
			if job != nil {
				select {
				case p.jobs <- job:
				case <-p.ctx.Done():
					return
				}
			}
		}
	}
}

// selectNextJob selects the next job using round-robin across sessions
func (p *SessionPool) selectNextJob() *SessionJob {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.sessionOrder) == 0 {
		return nil
	}

	// Try each session starting from current index
	for i := 0; i < len(p.sessionOrder); i++ {
		idx := (p.currentIndex + i) % len(p.sessionOrder)
		sessionID := p.sessionOrder[idx]

		sq, ok := p.sessionQueues[sessionID]
		if !ok {
			continue
		}

		sq.mu.Lock()
		if len(sq.jobs) > 0 {
			// Pop first job
			job := sq.jobs[0]
			sq.jobs = sq.jobs[1:]
			sq.mu.Unlock()

			// Update round-robin index
			p.currentIndex = (idx + 1) % len(p.sessionOrder)

			return job
		}
		sq.mu.Unlock()
	}

	return nil
}

// worker processes jobs from the channel
func (p *SessionPool) worker(id int) {
	defer p.wg.Done()

	for job := range p.jobs {
		if job == nil {
			continue
		}

		err := p.executeJob(job)
		if err != nil {
			atomic.AddInt64(&p.totalFailed, 1)
			p.updateSessionStats(job.SessionID, false)
			if job.OnError != nil {
				job.OnError(err)
			}
		} else {
			atomic.AddInt64(&p.totalCompleted, 1)
			p.updateSessionStats(job.SessionID, true)
		}
	}
}

// executeJob executes a single job with panic recovery
func (p *SessionPool) executeJob(job *SessionJob) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = ErrJobPanicked
		}
	}()

	return job.Execute(p.ctx)
}

// updateSessionStats updates per-session statistics
func (p *SessionPool) updateSessionStats(sessionID string, success bool) {
	p.mu.RLock()
	sq, ok := p.sessionQueues[sessionID]
	p.mu.RUnlock()

	if !ok {
		return
	}

	sq.mu.Lock()
	if success {
		sq.completed++
	} else {
		sq.failed++
	}
	sq.mu.Unlock()
}

// totalQueuedJobs returns the total number of queued jobs
func (p *SessionPool) totalQueuedJobs() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	total := 0
	for _, sq := range p.sessionQueues {
		sq.mu.Lock()
		total += len(sq.jobs)
		sq.mu.Unlock()
	}
	return total
}

// =============================================================================
// Session Management
// =============================================================================

// RemoveSession removes a session and its queued jobs
func (p *SessionPool) RemoveSession(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.sessionQueues, sessionID)

	// Remove from order
	for i, id := range p.sessionOrder {
		if id == sessionID {
			p.sessionOrder = append(p.sessionOrder[:i], p.sessionOrder[i+1:]...)
			break
		}
	}

	// Adjust current index if needed
	if p.currentIndex >= len(p.sessionOrder) && len(p.sessionOrder) > 0 {
		p.currentIndex = 0
	}
}

// SessionCount returns the number of active sessions
func (p *SessionPool) SessionCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.sessionQueues)
}

// SessionIDs returns all session IDs
func (p *SessionPool) SessionIDs() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]string, len(p.sessionOrder))
	copy(result, p.sessionOrder)
	return result
}

// =============================================================================
// Statistics
// =============================================================================

// Stats returns pool statistics
func (p *SessionPool) Stats() SessionPoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := SessionPoolStats{
		Name:            p.name,
		NumWorkers:      p.numWorkers,
		MaxQueueSize:    p.maxQueueSize,
		Running:         p.running.Load(),
		TotalSubmitted:  atomic.LoadInt64(&p.totalSubmitted),
		TotalCompleted:  atomic.LoadInt64(&p.totalCompleted),
		TotalFailed:     atomic.LoadInt64(&p.totalFailed),
		TotalDropped:    atomic.LoadInt64(&p.totalDropped),
		SessionCount:    len(p.sessionQueues),
		SessionStats:    make(map[string]SessionStats),
	}

	for sessionID, sq := range p.sessionQueues {
		sq.mu.Lock()
		stats.SessionStats[sessionID] = SessionStats{
			SessionID:   sessionID,
			QueueLength: len(sq.jobs),
			Submitted:   sq.submitted,
			Completed:   sq.completed,
			Failed:      sq.failed,
		}
		stats.TotalQueued += len(sq.jobs)
		sq.mu.Unlock()
	}

	return stats
}

// SessionPoolStats contains pool statistics
type SessionPoolStats struct {
	Name           string                  `json:"name"`
	NumWorkers     int                     `json:"num_workers"`
	MaxQueueSize   int                     `json:"max_queue_size"`
	Running        bool                    `json:"running"`
	TotalSubmitted int64                   `json:"total_submitted"`
	TotalCompleted int64                   `json:"total_completed"`
	TotalFailed    int64                   `json:"total_failed"`
	TotalDropped   int64                   `json:"total_dropped"`
	TotalQueued    int                     `json:"total_queued"`
	SessionCount   int                     `json:"session_count"`
	SessionStats   map[string]SessionStats `json:"session_stats"`
}

// SessionStats contains per-session statistics
type SessionStats struct {
	SessionID   string `json:"session_id"`
	QueueLength int    `json:"queue_length"`
	Submitted   int64  `json:"submitted"`
	Completed   int64  `json:"completed"`
	Failed      int64  `json:"failed"`
}

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrJobPanicked indicates the job panicked during execution
	ErrJobPanicked = errors.New("job panicked")
)
