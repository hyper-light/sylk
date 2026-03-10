package taskstate

import "time"

const Topic = "pipeline.task_state"

type Status string

const (
	StatusPending          Status = "pending"
	StatusDefiningCriteria Status = "defining_criteria"
	StatusCreatingTests    Status = "creating_tests"
	StatusExecuting        Status = "executing"
	StatusValidating       Status = "validating"
	StatusCompleted        Status = "completed"
	StatusFailed           Status = "failed"
	StatusCancelled        Status = "cancelled"
)

// Event is the canonical task-pipeline state update published by the
// orchestrator and consumed by the TUI pipeline bridge.
type Event struct {
	PipelineID string    `json:"pipeline_id"`
	TaskID     string    `json:"task_id"`
	TaskLabel  string    `json:"task_label,omitempty"`
	Status     Status    `json:"status"`
	WorkerType string    `json:"worker_type,omitempty"`
	LoopCount  int       `json:"loop_count,omitempty"`
	MaxLoops   int       `json:"max_loops,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}
