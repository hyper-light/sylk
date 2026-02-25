package tdd

import (
	"context"
	"errors"
	"fmt"
	"time"

	inspShared "github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/agents/tester"
)

// PipelineStatus represents the current phase of a TDD pipeline.
type PipelineStatus string

const (
	StatusPending          PipelineStatus = "pending"
	StatusDefiningCriteria PipelineStatus = "defining_criteria"
	StatusCreatingTests    PipelineStatus = "creating_tests"
	StatusExecuting        PipelineStatus = "executing"
	StatusValidating       PipelineStatus = "validating"
	StatusCompleted        PipelineStatus = "completed"
	StatusFailed           PipelineStatus = "failed"
	StatusCancelled        PipelineStatus = "cancelled"
)

// statusTransitions defines the valid state machine transitions.
// Non-terminal states can always transition to Failed or Cancelled.
var statusTransitions = map[PipelineStatus][]PipelineStatus{
	StatusPending:          {StatusDefiningCriteria, StatusFailed, StatusCancelled},
	StatusDefiningCriteria: {StatusCreatingTests, StatusFailed, StatusCancelled},
	StatusCreatingTests:    {StatusExecuting, StatusFailed, StatusCancelled},
	StatusExecuting:        {StatusValidating, StatusFailed, StatusCancelled},
	StatusValidating:       {StatusCompleted, StatusDefiningCriteria, StatusFailed, StatusCancelled},
}

var (
	ErrInvalidTransition  = errors.New("invalid status transition")
	ErrMaxLoopsExhausted  = errors.New("maximum TDD loops exhausted")
	ErrPipelineNotFound   = errors.New("pipeline not found")
	ErrPipelineClosed     = errors.New("pipeline manager closed")
	ErrCapacityExhausted  = errors.New("maximum active pipelines reached")
	ErrPipelineTerminated = errors.New("pipeline already in terminal state")
)

// ValidateTransition checks whether a status transition is allowed.
func ValidateTransition(from, to PipelineStatus) error {
	allowed, ok := statusTransitions[from]
	if !ok {
		return fmt.Errorf("%w: no transitions from %s", ErrInvalidTransition, from)
	}
	for _, s := range allowed {
		if s == to {
			return nil
		}
	}
	return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
}

// IsTerminalStatus returns true for Completed, Failed, and Cancelled.
func IsTerminalStatus(s PipelineStatus) bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

// WorkerType identifies the type of worker agent in the pipeline.
type WorkerType string

const (
	WorkerEngineer WorkerType = "engineer"
	WorkerDesigner WorkerType = "designer"
)

// PipelineConfig holds the configuration for creating a TDD pipeline.
type PipelineConfig struct {
	TaskID          string
	SessionID       string
	DAGNodeID       string
	WorkerType      WorkerType
	MaxLoops        int
	PhaseTimeout    time.Duration
	PipelineTimeout time.Duration
	Priority        int
	InitialCriteria *inspShared.InspectorCriteria
	TaskPrompt      string            // Full task prompt from buildNodePrompt
	CoWorkerTypes   []WorkerType      // Co-tenant worker types (sequential order)
	AgentPrompts    map[WorkerType]string // Per-agent scoped prompts (compound only)
}

// Pipeline represents the state of a single TDD pipeline execution.
type Pipeline struct {
	ID        string
	TaskID    string
	SessionID string
	DAGNodeID string

	WorkerType WorkerType
	Status     PipelineStatus
	LoopCount  int
	MaxLoops   int

	// Phase results — populated as the pipeline progresses.
	InspectorCriteria *inspShared.InspectorCriteria
	TesterTests       *tester.TestSuiteResult
	WorkerOutput      *WorkerResult
	InspectorResult   *inspShared.InspectorResult
	TesterResult      *tester.TesterResponse

	// Compound pipeline fields.
	TaskPrompt      string
	CoWorkerTypes   []WorkerType
	AgentPrompts    map[WorkerType]string
	CoWorkerOutputs []*WorkerResult

	// Timestamps.
	CreatedAt   time.Time
	StartedAt   time.Time
	CompletedAt time.Time

	LastError string
}

// IsCompound returns true if this pipeline has co-tenant workers.
func (p *Pipeline) IsCompound() bool {
	return len(p.CoWorkerTypes) > 0
}

// PipelineResult is the final output of a completed TDD pipeline.
type PipelineResult struct {
	PipelineID string
	TaskID     string
	Status     PipelineStatus
	LoopCount  int

	InspectorCriteria *inspShared.InspectorCriteria
	TesterTests       *tester.TestSuiteResult
	WorkerOutput      *WorkerResult
	InspectorResult   *inspShared.InspectorResult
	TesterResult      *tester.TesterResponse
	CoWorkerOutputs   []*WorkerResult

	Duration time.Duration
	Error    string
}

// PipelineEvent is emitted on every status change.
type PipelineEvent struct {
	PipelineID string
	TaskID     string
	SessionID  string
	OldStatus  PipelineStatus
	NewStatus  PipelineStatus
	WorkerType WorkerType
	LoopCount  int
	MaxLoops   int
	Timestamp  time.Time
	Error      string
}

// WorkerAgent is the interface that engineer and designer adapters implement.
type WorkerAgent interface {
	Execute(ctx context.Context, criteria *inspShared.InspectorCriteria, inspectorFeedback *InspectorFeedback, testerFeedback *TesterFeedback) (*WorkerResult, error)
	Close() error
}
