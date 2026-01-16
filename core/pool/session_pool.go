package pool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

type SessionPool struct {
	mu sync.RWMutex

	name         string
	numWorkers   int
	maxQueueSize int

	sessionQueues map[string]*sessionQueue

	sessionOrder []string
	currentIndex int

	jobs chan *SessionJob

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	running atomic.Bool
	closed  atomic.Bool

	baseMaxJobsPerSession int
	baseMaxTotalJobs      int
	maxJobsPerSession     int
	maxTotalJobs          int
	maxJobsPerSessionMax  int
	maxTotalJobsMax       int
	dynamicLimits         bool
	adjustInterval        time.Duration

	maxAgents           map[string]int
	runningAgents       map[string]int
	runningAgentsBySess map[string]map[string]int
	runningJobs         int64

	totalSubmitted int64
	totalCompleted int64
	totalFailed    int64
	totalDropped   int64
}

type sessionQueue struct {
	mu        sync.Mutex
	sessionID string
	jobs      []*SessionJob
	submitted int64
	completed int64
	failed    int64
}

type SessionJob struct {
	ID        string
	SessionID string
	AgentType string
	Priority  Priority
	Execute   func(ctx context.Context) error
	OnError   func(error)
	CreatedAt time.Time
}

type SessionPoolConfig struct {
	Name                 string
	NumWorkers           int
	MaxQueueSize         int
	MaxJobsPerSession    int
	MaxTotalJobs         int
	MaxAgents            map[string]int
	EnableDynamicLimits  bool
	AdjustInterval       time.Duration
	MaxJobsPerSessionMax int
	MaxTotalJobsMax      int
}

func DefaultSessionPoolConfig() SessionPoolConfig {
	return SessionPoolConfig{
		Name:              "session-pool",
		NumWorkers:        8,
		MaxQueueSize:      1000,
		MaxJobsPerSession: 100,
		MaxTotalJobs:      500,
	}
}

func NewSessionPool(cfg SessionPoolConfig) *SessionPool {
	cfg = normalizeSessionPoolConfig(cfg)
	ctx, cancel := context.WithCancel(context.Background())

	return &SessionPool{
		name:                  cfg.Name,
		numWorkers:            cfg.NumWorkers,
		maxQueueSize:          cfg.MaxQueueSize,
		sessionQueues:         make(map[string]*sessionQueue),
		sessionOrder:          make([]string, 0),
		jobs:                  make(chan *SessionJob, cfg.MaxQueueSize),
		ctx:                   ctx,
		cancel:                cancel,
		baseMaxJobsPerSession: cfg.MaxJobsPerSession,
		baseMaxTotalJobs:      cfg.MaxTotalJobs,
		maxJobsPerSession:     cfg.MaxJobsPerSession,
		maxTotalJobs:          cfg.MaxTotalJobs,
		maxJobsPerSessionMax:  cfg.MaxJobsPerSessionMax,
		maxTotalJobsMax:       cfg.MaxTotalJobsMax,
		dynamicLimits:         cfg.EnableDynamicLimits,
		adjustInterval:        cfg.AdjustInterval,
		maxAgents:             cfg.MaxAgents,
		runningAgents:         make(map[string]int),
		runningAgentsBySess:   make(map[string]map[string]int),
	}
}

func normalizeSessionPoolConfig(cfg SessionPoolConfig) SessionPoolConfig {
	cfg = ensureSessionPoolWorkerLimits(cfg)
	cfg = ensureSessionPoolJobLimits(cfg)
	cfg = ensureSessionPoolTiming(cfg)
	return cfg
}

func ensureSessionPoolWorkerLimits(cfg SessionPoolConfig) SessionPoolConfig {
	if cfg.NumWorkers <= 0 {
		cfg.NumWorkers = 8
	}
	if cfg.MaxQueueSize <= 0 {
		cfg.MaxQueueSize = 1000
	}
	return cfg
}

func ensureSessionPoolJobLimits(cfg SessionPoolConfig) SessionPoolConfig {
	if cfg.MaxJobsPerSession <= 0 {
		cfg.MaxJobsPerSession = 100
	}
	if cfg.MaxTotalJobs <= 0 {
		cfg.MaxTotalJobs = 500
	}
	if cfg.MaxJobsPerSessionMax <= 0 {
		cfg.MaxJobsPerSessionMax = cfg.MaxJobsPerSession
	}
	if cfg.MaxTotalJobsMax <= 0 {
		cfg.MaxTotalJobsMax = cfg.MaxTotalJobs
	}
	return cfg
}

func ensureSessionPoolTiming(cfg SessionPoolConfig) SessionPoolConfig {
	if cfg.AdjustInterval <= 0 {
		cfg.AdjustInterval = 2 * time.Second
	}
	return cfg
}

func (p *SessionPool) Start() {
	if p.running.Swap(true) {
		return
	}

	for i := 0; i < p.numWorkers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	p.wg.Add(1)
	go p.scheduler()
	if p.dynamicLimits {
		p.wg.Add(1)
		go p.limitAdjuster()
	}
}

func (p *SessionPool) Stop() {
	if !p.running.Swap(false) {
		return
	}

	p.cancel()
	close(p.jobs)
	p.wg.Wait()
}

func (p *SessionPool) Close() error {
	if p.closed.Swap(true) {
		return ErrPoolClosed
	}

	p.Stop()
	return nil
}

func (p *SessionPool) Submit(job *SessionJob) bool {
	if !p.running.Load() || p.closed.Load() {
		return false
	}
	p.ensureJobTimestamp(job)
	if p.overTotalJobLimit() {
		return p.dropJob()
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.overAgentQueueLimit(job) {
		return p.dropJob()
	}

	sq := p.getOrCreateQueue(job.SessionID)
	if p.overSessionLimit(sq) {
		return p.dropJob()
	}

	sq.jobs = append(sq.jobs, job)
	sq.submitted++
	atomic.AddInt64(&p.totalSubmitted, 1)
	return true
}

func (p *SessionPool) SubmitBlocking(job *SessionJob) bool {
	if !p.running.Load() || p.closed.Load() {
		return false
	}
	p.ensureJobTimestamp(job)

	for {
		if p.Submit(job) {
			return true
		}
		if !p.waitForRetry() {
			return false
		}
	}
}

func (p *SessionPool) waitForRetry() bool {
	select {
	case <-time.After(10 * time.Millisecond):
		return true
	case <-p.ctx.Done():
		return false
	}
}

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

func (p *SessionPool) limitAdjuster() {
	defer p.wg.Done()

	ticker := time.NewTicker(p.adjustInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.adjustLimits()
		}
	}
}

func (p *SessionPool) adjustLimits() {
	p.mu.Lock()
	defer p.mu.Unlock()

	queued := p.countQueuedJobs()
	if queued > p.maxQueueSize/2 {
		p.raiseLimits()
		return
	}
	if queued == 0 {
		p.lowerLimits()
	}
}

func (p *SessionPool) countQueuedJobs() int {
	queued := 0
	for _, sq := range p.sessionQueues {
		sq.mu.Lock()
		queued += len(sq.jobs)
		sq.mu.Unlock()
	}
	return queued
}

func (p *SessionPool) raiseLimits() {
	if p.maxJobsPerSession < p.maxJobsPerSessionMax {
		p.maxJobsPerSession++
	}
	if p.maxTotalJobs < p.maxTotalJobsMax {
		p.maxTotalJobs += 5
	}
}

func (p *SessionPool) lowerLimits() {
	if p.maxJobsPerSession > p.baseMaxJobsPerSession {
		p.maxJobsPerSession--
	}
	if p.maxTotalJobs > p.baseMaxTotalJobs {
		p.maxTotalJobs -= 5
		if p.maxTotalJobs < p.baseMaxTotalJobs {
			p.maxTotalJobs = p.baseMaxTotalJobs
		}
	}
}

func (p *SessionPool) selectNextJob() *SessionJob {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.sessionOrder) == 0 {
		return nil
	}

	for i := 0; i < len(p.sessionOrder); i++ {
		idx := (p.currentIndex + i) % len(p.sessionOrder)
		sessionID := p.sessionOrder[idx]

		job := p.nextJobForSession(sessionID)
		if job == nil {
			continue
		}

		p.trackRunning(job)
		p.currentIndex = (idx + 1) % len(p.sessionOrder)
		return job
	}

	return nil
}

func (p *SessionPool) nextJobForSession(sessionID string) *SessionJob {
	sq, ok := p.sessionQueues[sessionID]
	if !ok {
		return nil
	}
	return p.popEligibleJob(sq)
}

func (p *SessionPool) worker(id int) {
	defer p.wg.Done()

	for job := range p.jobs {
		if job == nil {
			continue
		}
		p.handleJob(job)
		p.untrackRunning(job)
	}
}

func (p *SessionPool) handleJob(job *SessionJob) {
	err := p.executeJob(job)
	if err != nil {
		atomic.AddInt64(&p.totalFailed, 1)
		p.updateSessionStats(job.SessionID, false)
		if job.OnError != nil {
			job.OnError(err)
		}
		return
	}

	atomic.AddInt64(&p.totalCompleted, 1)
	p.updateSessionStats(job.SessionID, true)
}

func (p *SessionPool) ensureJobTimestamp(job *SessionJob) {
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now()
	}
}

func (p *SessionPool) overTotalJobLimit() bool {
	queued := p.totalQueuedJobs()
	running := int(atomic.LoadInt64(&p.runningJobs))
	return queued+running >= p.maxTotalJobs
}

func (p *SessionPool) dropJob() bool {
	atomic.AddInt64(&p.totalDropped, 1)
	return false
}

func (p *SessionPool) overAgentQueueLimit(job *SessionJob) bool {
	if p.unlimitedAgent(job.AgentType) {
		return false
	}

	queued := p.countQueuedAgents(job.AgentType)
	running := p.runningAgents[job.AgentType]
	limit := p.maxAgents[job.AgentType]

	sameSession := p.sessionRunning(job.AgentType, job.SessionID) > 0
	if sameSession {
		return queued+running >= limit
	}
	if !p.hasAvailableWorkers() {
		return queued+running >= limit
	}
	return false
}

func (p *SessionPool) hasAvailableWorkers() bool {
	return atomic.LoadInt64(&p.runningJobs) < int64(p.numWorkers)
}

func (p *SessionPool) unlimitedAgent(agentType string) bool {
	if agentType == "" || p.maxAgents == nil {
		return true
	}
	limit, ok := p.maxAgents[agentType]
	return !ok || limit <= 0
}

func (p *SessionPool) countQueuedAgents(agentType string) int {
	queued := 0
	for _, sq := range p.sessionQueues {
		sq.mu.Lock()
		for _, queuedJob := range sq.jobs {
			if queuedJob.AgentType == agentType {
				queued++
			}
		}
		sq.mu.Unlock()
	}
	return queued
}

func (p *SessionPool) sessionRunning(agentType, sessionID string) int {
	if sessions, ok := p.runningAgentsBySess[agentType]; ok {
		return sessions[sessionID]
	}
	return 0
}

func (p *SessionPool) getOrCreateQueue(sessionID string) *sessionQueue {
	sq, ok := p.sessionQueues[sessionID]
	if ok {
		return sq
	}

	sq = &sessionQueue{sessionID: sessionID, jobs: make([]*SessionJob, 0)}
	p.sessionQueues[sessionID] = sq
	p.sessionOrder = append(p.sessionOrder, sessionID)
	return sq
}

func (p *SessionPool) overSessionLimit(sq *sessionQueue) bool {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return len(sq.jobs) >= p.maxJobsPerSession
}

func (p *SessionPool) popEligibleJob(sq *sessionQueue) *SessionJob {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	if len(sq.jobs) == 0 {
		return nil
	}

	job := sq.jobs[0]
	if p.agentAtLimit(job.AgentType) {
		return nil
	}

	sq.jobs = sq.jobs[1:]
	return job
}

func (p *SessionPool) agentAtLimit(agentType string) bool {
	if p.unlimitedAgent(agentType) {
		return false
	}
	return p.runningAgents[agentType] >= p.maxAgents[agentType]
}

func (p *SessionPool) trackRunning(job *SessionJob) {
	atomic.AddInt64(&p.runningJobs, 1)
	if job.AgentType == "" || p.maxAgents == nil {
		return
	}

	p.runningAgents[job.AgentType]++
	sessions, ok := p.runningAgentsBySess[job.AgentType]
	if !ok {
		sessions = make(map[string]int)
		p.runningAgentsBySess[job.AgentType] = sessions
	}
	sessions[job.SessionID]++
}

func (p *SessionPool) untrackRunning(job *SessionJob) {
	atomic.AddInt64(&p.runningJobs, -1)
	if job.AgentType == "" || p.maxAgents == nil {
		return
	}

	p.mu.Lock()
	if p.runningAgents[job.AgentType] > 0 {
		p.runningAgents[job.AgentType]--
	}
	if sessions, ok := p.runningAgentsBySess[job.AgentType]; ok {
		if sessions[job.SessionID] > 0 {
			sessions[job.SessionID]--
		}
	}
	p.mu.Unlock()
}

func (p *SessionPool) executeJob(job *SessionJob) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = ErrJobPanicked
		}
	}()

	return job.Execute(p.ctx)
}

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

func (p *SessionPool) totalQueuedJobs() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.countQueuedJobs()
}

func (p *SessionPool) RemoveSession(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.sessionQueues, sessionID)

	for i, id := range p.sessionOrder {
		if id == sessionID {
			p.sessionOrder = append(p.sessionOrder[:i], p.sessionOrder[i+1:]...)
			break
		}
	}

	if p.currentIndex >= len(p.sessionOrder) && len(p.sessionOrder) > 0 {
		p.currentIndex = 0
	}
}

func (p *SessionPool) SessionCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.sessionQueues)
}

func (p *SessionPool) SessionIDs() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]string, len(p.sessionOrder))
	copy(result, p.sessionOrder)
	return result
}

func (p *SessionPool) Stats() SessionPoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := SessionPoolStats{
		Name:              p.name,
		NumWorkers:        p.numWorkers,
		MaxQueueSize:      p.maxQueueSize,
		Running:           p.running.Load(),
		TotalSubmitted:    atomic.LoadInt64(&p.totalSubmitted),
		TotalCompleted:    atomic.LoadInt64(&p.totalCompleted),
		TotalFailed:       atomic.LoadInt64(&p.totalFailed),
		TotalDropped:      atomic.LoadInt64(&p.totalDropped),
		SessionCount:      len(p.sessionQueues),
		SessionStats:      make(map[string]SessionStats),
		AgentStats:        make(map[string]AgentStats),
		MaxJobsPerSession: p.maxJobsPerSession,
		MaxTotalJobs:      p.maxTotalJobs,
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

	for agentType, limit := range p.maxAgents {
		queued := 0
		for _, sq := range p.sessionQueues {
			sq.mu.Lock()
			for _, job := range sq.jobs {
				if job.AgentType == agentType {
					queued++
				}
			}
			sq.mu.Unlock()
		}
		stats.AgentStats[agentType] = AgentStats{
			AgentType: agentType,
			Limit:     limit,
			Queued:    queued,
			Running:   p.runningAgents[agentType],
		}
	}

	return stats
}

type SessionPoolStats struct {
	Name              string                  `json:"name"`
	NumWorkers        int                     `json:"num_workers"`
	MaxQueueSize      int                     `json:"max_queue_size"`
	Running           bool                    `json:"running"`
	TotalSubmitted    int64                   `json:"total_submitted"`
	TotalCompleted    int64                   `json:"total_completed"`
	TotalFailed       int64                   `json:"total_failed"`
	TotalDropped      int64                   `json:"total_dropped"`
	TotalQueued       int                     `json:"total_queued"`
	SessionCount      int                     `json:"session_count"`
	SessionStats      map[string]SessionStats `json:"session_stats"`
	AgentStats        map[string]AgentStats   `json:"agent_stats"`
	MaxJobsPerSession int                     `json:"max_jobs_per_session"`
	MaxTotalJobs      int                     `json:"max_total_jobs"`
}

type AgentStats struct {
	AgentType string `json:"agent_type"`
	Limit     int    `json:"limit"`
	Queued    int    `json:"queued"`
	Running   int    `json:"running"`
}

type SessionStats struct {
	SessionID   string `json:"session_id"`
	QueueLength int    `json:"queue_length"`
	Submitted   int64  `json:"submitted"`
	Completed   int64  `json:"completed"`
	Failed      int64  `json:"failed"`
}

var (
	ErrJobPanicked = errors.New("job panicked")
)
