package quantization

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// =============================================================================
// Training Queue Types
// =============================================================================
//
// Background training queue enables non-blocking LOPQ codebook training.
// Jobs are persisted to SQLite for crash recovery.

// =============================================================================
// JobStatus
// =============================================================================

// JobStatus represents the state of a training job.
type JobStatus int

const (
	// JobStatusPending indicates job is queued but not started.
	JobStatusPending JobStatus = iota

	// JobStatusRunning indicates job is currently executing.
	JobStatusRunning

	// JobStatusComplete indicates job finished successfully.
	JobStatusComplete

	// JobStatusFailed indicates job failed with an error.
	JobStatusFailed

	// JobStatusCancelled indicates job was cancelled before completion.
	JobStatusCancelled
)

// String returns a string representation.
func (s JobStatus) String() string {
	switch s {
	case JobStatusPending:
		return "pending"
	case JobStatusRunning:
		return "running"
	case JobStatusComplete:
		return "complete"
	case JobStatusFailed:
		return "failed"
	case JobStatusCancelled:
		return "cancelled"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

// IsTerminal returns true if this is a final state (no more transitions).
func (s JobStatus) IsTerminal() bool {
	return s == JobStatusComplete || s == JobStatusFailed || s == JobStatusCancelled
}

// ParseJobStatus converts a string to JobStatus.
func ParseJobStatus(s string) JobStatus {
	switch s {
	case "pending":
		return JobStatusPending
	case "running":
		return JobStatusRunning
	case "complete":
		return JobStatusComplete
	case "failed":
		return JobStatusFailed
	case "cancelled":
		return JobStatusCancelled
	default:
		return JobStatusPending
	}
}

// =============================================================================
// JobPriority
// =============================================================================

// JobPriority controls training job ordering.
// Lower values = higher priority.
type JobPriority int

const (
	// PriorityUrgent is for immediate training needs (e.g., user-triggered).
	PriorityUrgent JobPriority = 0

	// PriorityHigh is for partitions with many vectors needing encoding.
	PriorityHigh JobPriority = 10

	// PriorityNormal is the default priority for routine training.
	PriorityNormal JobPriority = 50

	// PriorityLow is for background maintenance training.
	PriorityLow JobPriority = 100

	// PriorityIdle is for training during system idle time only.
	PriorityIdle JobPriority = 1000
)

// String returns a string representation.
func (p JobPriority) String() string {
	switch p {
	case PriorityUrgent:
		return "urgent"
	case PriorityHigh:
		return "high"
	case PriorityNormal:
		return "normal"
	case PriorityLow:
		return "low"
	case PriorityIdle:
		return "idle"
	default:
		return fmt.Sprintf("priority(%d)", p)
	}
}

// =============================================================================
// TrainingJobType
// =============================================================================

// TrainingJobType identifies what kind of training to perform.
type TrainingJobType int

const (
	// TrainingJobLOPQLocal trains a local codebook for an LOPQ partition.
	TrainingJobLOPQLocal TrainingJobType = iota

	// TrainingJobLOPQCoarse retrains the coarse quantizer.
	TrainingJobLOPQCoarse

	// TrainingJobCentroidSplit splits an overloaded centroid.
	TrainingJobCentroidSplit

	// TrainingJobReencode re-encodes vectors after codebook change.
	TrainingJobReencode
)

// String returns a string representation.
func (t TrainingJobType) String() string {
	switch t {
	case TrainingJobLOPQLocal:
		return "lopq_local"
	case TrainingJobLOPQCoarse:
		return "lopq_coarse"
	case TrainingJobCentroidSplit:
		return "centroid_split"
	case TrainingJobReencode:
		return "reencode"
	default:
		return fmt.Sprintf("unknown(%d)", t)
	}
}

// =============================================================================
// TrainingJob
// =============================================================================

// TrainingJob represents a background training task.
type TrainingJob struct {
	// ID is the unique job identifier.
	ID int64

	// Type identifies what kind of training to perform.
	Type TrainingJobType

	// PartitionID is the target partition (for LOPQ local training).
	// -1 if not applicable.
	PartitionID int

	// Priority controls job ordering (lower = higher priority).
	Priority JobPriority

	// Status is the current job state.
	Status JobStatus

	// CreatedAt is when the job was queued.
	CreatedAt time.Time

	// StartedAt is when the job began executing.
	// Zero if not started yet.
	StartedAt time.Time

	// CompletedAt is when the job finished (success or failure).
	// Zero if not completed yet.
	CompletedAt time.Time

	// Error contains the error message if Status == JobStatusFailed.
	Error string

	// VectorCount is the number of vectors used for training.
	// Populated after completion.
	VectorCount int

	// Duration is how long the training took.
	// Populated after completion.
	Duration time.Duration
}

// NewTrainingJob creates a new pending training job.
func NewTrainingJob(jobType TrainingJobType, partitionID int, priority JobPriority) *TrainingJob {
	return &TrainingJob{
		Type:        jobType,
		PartitionID: partitionID,
		Priority:    priority,
		Status:      JobStatusPending,
		CreatedAt:   time.Now(),
	}
}

// MarkRunning transitions the job to running state.
func (j *TrainingJob) MarkRunning() {
	j.Status = JobStatusRunning
	j.StartedAt = time.Now()
}

// MarkComplete transitions the job to complete state.
func (j *TrainingJob) MarkComplete(vectorCount int) {
	j.Status = JobStatusComplete
	j.CompletedAt = time.Now()
	j.VectorCount = vectorCount
	if !j.StartedAt.IsZero() {
		j.Duration = j.CompletedAt.Sub(j.StartedAt)
	}
}

// MarkFailed transitions the job to failed state.
func (j *TrainingJob) MarkFailed(err error) {
	j.Status = JobStatusFailed
	j.CompletedAt = time.Now()
	if err != nil {
		j.Error = err.Error()
	}
	if !j.StartedAt.IsZero() {
		j.Duration = j.CompletedAt.Sub(j.StartedAt)
	}
}

// MarkCancelled transitions the job to cancelled state.
func (j *TrainingJob) MarkCancelled() {
	j.Status = JobStatusCancelled
	j.CompletedAt = time.Now()
	if !j.StartedAt.IsZero() {
		j.Duration = j.CompletedAt.Sub(j.StartedAt)
	}
}

// WaitTime returns how long the job has been waiting (pending or running).
func (j *TrainingJob) WaitTime() time.Duration {
	if j.Status.IsTerminal() {
		return j.StartedAt.Sub(j.CreatedAt)
	}
	if !j.StartedAt.IsZero() {
		return time.Since(j.StartedAt)
	}
	return time.Since(j.CreatedAt)
}

// String returns a string representation for debugging.
func (j *TrainingJob) String() string {
	if j == nil {
		return "TrainingJob(nil)"
	}
	return fmt.Sprintf("TrainingJob{ID:%d, Type:%s, Part:%d, Status:%s, Pri:%s}",
		j.ID, j.Type, j.PartitionID, j.Status, j.Priority)
}

// =============================================================================
// TrainingQueue Interface
// =============================================================================

// TrainingQueue manages background training jobs.
// Implementations are safe for concurrent use.
type TrainingQueue interface {
	// Enqueue adds a new training job to the queue.
	// Returns the assigned job ID.
	Enqueue(ctx context.Context, job *TrainingJob) (int64, error)

	// Dequeue retrieves the highest-priority pending job and marks it running.
	// Returns nil if the queue is empty.
	Dequeue(ctx context.Context) (*TrainingJob, error)

	// Complete marks a job as successfully completed.
	Complete(ctx context.Context, jobID int64, vectorCount int) error

	// Fail marks a job as failed with an error.
	Fail(ctx context.Context, jobID int64, err error) error

	// Cancel cancels a pending or running job.
	Cancel(ctx context.Context, jobID int64) error

	// GetJob retrieves a job by ID.
	GetJob(ctx context.Context, jobID int64) (*TrainingJob, error)

	// GetPendingJobs returns all pending jobs ordered by priority.
	GetPendingJobs(ctx context.Context) ([]*TrainingJob, error)

	// GetRunningJobs returns all currently running jobs.
	GetRunningJobs(ctx context.Context) ([]*TrainingJob, error)

	// GetRecentJobs returns recently completed jobs (within duration).
	GetRecentJobs(ctx context.Context, since time.Duration) ([]*TrainingJob, error)

	// GetStats returns queue statistics.
	GetStats(ctx context.Context) (TrainingQueueStats, error)

	// HasPendingForPartition checks if partition already has pending job.
	HasPendingForPartition(ctx context.Context, partitionID int) (bool, error)

	// Cleanup removes completed jobs older than retention period.
	Cleanup(ctx context.Context, retention time.Duration) (int, error)
}

// =============================================================================
// TrainingQueueStats
// =============================================================================

// TrainingQueueStats contains statistics about the training queue.
type TrainingQueueStats struct {
	// PendingJobs is the number of jobs waiting to run.
	PendingJobs int

	// RunningJobs is the number of currently executing jobs.
	RunningJobs int

	// CompletedJobs is the total number of successfully completed jobs.
	CompletedJobs int64

	// FailedJobs is the total number of failed jobs.
	FailedJobs int64

	// CancelledJobs is the total number of cancelled jobs.
	CancelledJobs int64

	// AverageWaitTime is the average time jobs wait in queue.
	AverageWaitTime time.Duration

	// AverageRunTime is the average training duration.
	AverageRunTime time.Duration

	// OldestPendingJob is the creation time of the oldest pending job.
	// Zero if no pending jobs.
	OldestPendingJob time.Time
}

// TotalJobs returns the total number of jobs across all states.
func (s TrainingQueueStats) TotalJobs() int64 {
	return int64(s.PendingJobs) + int64(s.RunningJobs) + s.CompletedJobs + s.FailedJobs + s.CancelledJobs
}

// SuccessRate returns the fraction of completed jobs that succeeded.
func (s TrainingQueueStats) SuccessRate() float64 {
	total := s.CompletedJobs + s.FailedJobs
	if total == 0 {
		return 0
	}
	return float64(s.CompletedJobs) / float64(total)
}

// String returns a string representation for debugging.
func (s TrainingQueueStats) String() string {
	return fmt.Sprintf("TrainingQueueStats{Pending:%d, Running:%d, Done:%d, Failed:%d, AvgWait:%s}",
		s.PendingJobs, s.RunningJobs, s.CompletedJobs, s.FailedJobs, s.AverageWaitTime)
}

// =============================================================================
// TrainingQueueConfig
// =============================================================================

// TrainingQueueConfig configures the training queue.
type TrainingQueueConfig struct {
	// MaxConcurrent is the maximum number of concurrent training jobs.
	// Default: 1 (sequential training to avoid CPU contention).
	MaxConcurrent int

	// RetentionPeriod is how long to keep completed jobs before cleanup.
	// Default: 24 hours.
	RetentionPeriod time.Duration

	// StaleJobTimeout is how long a running job can be stale before requeue.
	// Default: 10 minutes.
	StaleJobTimeout time.Duration

	// PollInterval is how often to check for new jobs.
	// Default: 1 second.
	PollInterval time.Duration
}

// DefaultTrainingQueueConfig returns a TrainingQueueConfig with default values.
func DefaultTrainingQueueConfig() TrainingQueueConfig {
	return TrainingQueueConfig{
		MaxConcurrent:   1,
		RetentionPeriod: 24 * time.Hour,
		StaleJobTimeout: 10 * time.Minute,
		PollInterval:    time.Second,
	}
}

// Validate checks that the configuration is valid.
func (c TrainingQueueConfig) Validate() error {
	if c.MaxConcurrent <= 0 {
		return ErrTrainingQueueInvalidConcurrency
	}
	if c.RetentionPeriod < time.Minute {
		return ErrTrainingQueueInvalidRetention
	}
	if c.StaleJobTimeout < time.Minute {
		return ErrTrainingQueueInvalidTimeout
	}
	if c.PollInterval < time.Millisecond*100 {
		return ErrTrainingQueueInvalidPollInterval
	}
	return nil
}

// String returns a string representation.
func (c TrainingQueueConfig) String() string {
	return fmt.Sprintf("TrainingQueueConfig{MaxConcurrent:%d, Retention:%s, Stale:%s}",
		c.MaxConcurrent, c.RetentionPeriod, c.StaleJobTimeout)
}

// =============================================================================
// Training Queue Errors
// =============================================================================

var (
	// ErrTrainingQueueJobNotFound indicates job ID doesn't exist.
	ErrTrainingQueueJobNotFound = errors.New("training_queue: job not found")

	// ErrTrainingQueueInvalidTransition indicates invalid status transition.
	ErrTrainingQueueInvalidTransition = errors.New("training_queue: invalid status transition")

	// ErrTrainingQueueFull indicates queue has reached capacity.
	ErrTrainingQueueFull = errors.New("training_queue: queue is full")

	// ErrTrainingQueueShutdown indicates queue is shutting down.
	ErrTrainingQueueShutdown = errors.New("training_queue: shutting down")

	// ErrTrainingQueueInvalidConcurrency indicates max concurrent is invalid.
	ErrTrainingQueueInvalidConcurrency = errors.New("training_queue: max concurrent must be positive")

	// ErrTrainingQueueInvalidRetention indicates retention period is too short.
	ErrTrainingQueueInvalidRetention = errors.New("training_queue: retention period must be at least 1 minute")

	// ErrTrainingQueueInvalidTimeout indicates stale timeout is too short.
	ErrTrainingQueueInvalidTimeout = errors.New("training_queue: stale timeout must be at least 1 minute")

	// ErrTrainingQueueInvalidPollInterval indicates poll interval is too short.
	ErrTrainingQueueInvalidPollInterval = errors.New("training_queue: poll interval must be at least 100ms")
)
