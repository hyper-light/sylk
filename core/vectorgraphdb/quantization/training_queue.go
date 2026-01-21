package quantization

import (
	"container/heap"
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// InMemoryTrainingQueue implements TrainingQueue with in-memory storage.
// For SQLite persistence, wrap this with a persistent adapter.
type InMemoryTrainingQueue struct {
	config    TrainingQueueConfig
	jobs      map[int64]*TrainingJob
	pending   *jobHeap
	nextID    int64
	mu        sync.RWMutex
	shutdown  atomic.Bool
	completed int64
	failed    int64
	cancelled int64
}

// NewInMemoryTrainingQueue creates an in-memory training queue.
func NewInMemoryTrainingQueue(config TrainingQueueConfig) (*InMemoryTrainingQueue, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	q := &InMemoryTrainingQueue{
		config:  config,
		jobs:    make(map[int64]*TrainingJob),
		pending: &jobHeap{},
		nextID:  1,
	}
	heap.Init(q.pending)

	return q, nil
}

func (q *InMemoryTrainingQueue) Enqueue(ctx context.Context, job *TrainingJob) (int64, error) {
	if q.shutdown.Load() {
		return 0, ErrTrainingQueueShutdown
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	job.ID = atomic.AddInt64(&q.nextID, 1) - 1
	job.Status = JobStatusPending
	job.CreatedAt = time.Now()

	q.jobs[job.ID] = job
	heap.Push(q.pending, job)

	return job.ID, nil
}

func (q *InMemoryTrainingQueue) Dequeue(ctx context.Context) (*TrainingJob, error) {
	if q.shutdown.Load() {
		return nil, ErrTrainingQueueShutdown
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.pending.Len() == 0 {
		return nil, nil
	}

	job := heap.Pop(q.pending).(*TrainingJob)
	job.MarkRunning()

	return job, nil
}

func (q *InMemoryTrainingQueue) Complete(ctx context.Context, jobID int64, vectorCount int) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	job, ok := q.jobs[jobID]
	if !ok {
		return ErrTrainingQueueJobNotFound
	}
	if job.Status != JobStatusRunning {
		return ErrTrainingQueueInvalidTransition
	}

	job.MarkComplete(vectorCount)
	atomic.AddInt64(&q.completed, 1)

	return nil
}

func (q *InMemoryTrainingQueue) Fail(ctx context.Context, jobID int64, err error) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	job, ok := q.jobs[jobID]
	if !ok {
		return ErrTrainingQueueJobNotFound
	}
	if job.Status != JobStatusRunning {
		return ErrTrainingQueueInvalidTransition
	}

	job.MarkFailed(err)
	atomic.AddInt64(&q.failed, 1)

	return nil
}

func (q *InMemoryTrainingQueue) Cancel(ctx context.Context, jobID int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	job, ok := q.jobs[jobID]
	if !ok {
		return ErrTrainingQueueJobNotFound
	}
	if job.Status.IsTerminal() {
		return ErrTrainingQueueInvalidTransition
	}

	if job.Status == JobStatusPending {
		q.removeFromPending(jobID)
	}

	job.MarkCancelled()
	atomic.AddInt64(&q.cancelled, 1)

	return nil
}

func (q *InMemoryTrainingQueue) removeFromPending(jobID int64) {
	for i, j := range *q.pending {
		if j.ID == jobID {
			heap.Remove(q.pending, i)
			break
		}
	}
}

func (q *InMemoryTrainingQueue) GetJob(ctx context.Context, jobID int64) (*TrainingJob, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	job, ok := q.jobs[jobID]
	if !ok {
		return nil, ErrTrainingQueueJobNotFound
	}
	return job, nil
}

func (q *InMemoryTrainingQueue) GetPendingJobs(ctx context.Context) ([]*TrainingJob, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var result []*TrainingJob
	for _, job := range q.jobs {
		if job.Status == JobStatusPending {
			result = append(result, job)
		}
	}
	return result, nil
}

func (q *InMemoryTrainingQueue) GetRunningJobs(ctx context.Context) ([]*TrainingJob, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var result []*TrainingJob
	for _, job := range q.jobs {
		if job.Status == JobStatusRunning {
			result = append(result, job)
		}
	}
	return result, nil
}

func (q *InMemoryTrainingQueue) GetRecentJobs(ctx context.Context, since time.Duration) ([]*TrainingJob, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	cutoff := time.Now().Add(-since)
	var result []*TrainingJob
	for _, job := range q.jobs {
		if job.Status.IsTerminal() && job.CompletedAt.After(cutoff) {
			result = append(result, job)
		}
	}
	return result, nil
}

func (q *InMemoryTrainingQueue) GetStats(ctx context.Context) (TrainingQueueStats, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	stats := TrainingQueueStats{
		CompletedJobs: atomic.LoadInt64(&q.completed),
		FailedJobs:    atomic.LoadInt64(&q.failed),
		CancelledJobs: atomic.LoadInt64(&q.cancelled),
	}

	var totalWait, totalRun time.Duration
	var waitCount, runCount int
	var oldestPending time.Time

	for _, job := range q.jobs {
		switch job.Status {
		case JobStatusPending:
			stats.PendingJobs++
			if oldestPending.IsZero() || job.CreatedAt.Before(oldestPending) {
				oldestPending = job.CreatedAt
			}
		case JobStatusRunning:
			stats.RunningJobs++
		}

		if !job.StartedAt.IsZero() {
			totalWait += job.StartedAt.Sub(job.CreatedAt)
			waitCount++
		}
		if job.Duration > 0 {
			totalRun += job.Duration
			runCount++
		}
	}

	stats.OldestPendingJob = oldestPending
	if waitCount > 0 {
		stats.AverageWaitTime = totalWait / time.Duration(waitCount)
	}
	if runCount > 0 {
		stats.AverageRunTime = totalRun / time.Duration(runCount)
	}

	return stats, nil
}

func (q *InMemoryTrainingQueue) HasPendingForPartition(ctx context.Context, partitionID int) (bool, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	for _, job := range q.jobs {
		if job.PartitionID == partitionID && job.Status == JobStatusPending {
			return true, nil
		}
	}
	return false, nil
}

func (q *InMemoryTrainingQueue) Cleanup(ctx context.Context, retention time.Duration) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	cutoff := time.Now().Add(-retention)
	var removed int

	for id, job := range q.jobs {
		if job.Status.IsTerminal() && job.CompletedAt.Before(cutoff) {
			delete(q.jobs, id)
			removed++
		}
	}

	return removed, nil
}

// Shutdown marks the queue as shutting down.
func (q *InMemoryTrainingQueue) Shutdown() {
	q.shutdown.Store(true)
}

// jobHeap implements heap.Interface for priority-based job ordering.
type jobHeap []*TrainingJob

func (h jobHeap) Len() int { return len(h) }

func (h jobHeap) Less(i, j int) bool {
	if h[i].Priority != h[j].Priority {
		return h[i].Priority < h[j].Priority
	}
	return h[i].CreatedAt.Before(h[j].CreatedAt)
}

func (h jobHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *jobHeap) Push(x interface{}) {
	*h = append(*h, x.(*TrainingJob))
}

func (h *jobHeap) Pop() interface{} {
	old := *h
	n := len(old)
	job := old[n-1]
	*h = old[0 : n-1]
	return job
}

var _ TrainingQueue = (*InMemoryTrainingQueue)(nil)
