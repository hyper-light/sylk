package dag

import (
	"errors"
	"time"
)

// =============================================================================
// Node State
// =============================================================================

// NodeState represents the execution state of a DAG node
type NodeState int

const (
	// NodeStatePending indicates the node is waiting for dependencies
	NodeStatePending NodeState = iota
	// NodeStateQueued indicates the node is queued for execution
	NodeStateQueued
	// NodeStateRunning indicates the node is currently executing
	NodeStateRunning
	// NodeStateSucceeded indicates the node completed successfully
	NodeStateSucceeded
	// NodeStateFailed indicates the node failed
	NodeStateFailed
	// NodeStateBlocked indicates the node is blocked by failed dependencies
	NodeStateBlocked
	// NodeStateSkipped indicates the node was skipped
	NodeStateSkipped
	// NodeStateCancelled indicates the node was cancelled
	NodeStateCancelled
)

// String returns the string representation of a node state
func (s NodeState) String() string {
	switch s {
	case NodeStatePending:
		return "pending"
	case NodeStateQueued:
		return "queued"
	case NodeStateRunning:
		return "running"
	case NodeStateSucceeded:
		return "succeeded"
	case NodeStateFailed:
		return "failed"
	case NodeStateBlocked:
		return "blocked"
	case NodeStateSkipped:
		return "skipped"
	case NodeStateCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// IsTerminal returns true if this is a terminal state
func (s NodeState) IsTerminal() bool {
	return s == NodeStateSucceeded || s == NodeStateFailed ||
		s == NodeStateBlocked || s == NodeStateSkipped || s == NodeStateCancelled
}

// IsSuccess returns true if this is a success state
func (s NodeState) IsSuccess() bool {
	return s == NodeStateSucceeded
}

// =============================================================================
// Execution Policy
// =============================================================================

// FailurePolicy defines how to handle node failures
type FailurePolicy int

const (
	// FailurePolicyFailFast stops execution on first failure
	FailurePolicyFailFast FailurePolicy = iota
	// FailurePolicyContinue continues execution, marking dependents as blocked
	FailurePolicyContinue
)

// String returns the string representation of a failure policy
func (p FailurePolicy) String() string {
	switch p {
	case FailurePolicyFailFast:
		return "fail_fast"
	case FailurePolicyContinue:
		return "continue"
	default:
		return "unknown"
	}
}

// ExecutionPolicy configures DAG execution behavior
type ExecutionPolicy struct {
	// FailurePolicy controls behavior on node failure
	FailurePolicy FailurePolicy

	// MaxConcurrency limits parallel node execution (0 = unlimited)
	MaxConcurrency int

	// DefaultTimeout is the default timeout per node
	DefaultTimeout time.Duration

	// DefaultRetries is the default retry count per node
	DefaultRetries int

	// RetryBackoff is the backoff duration between retries
	RetryBackoff time.Duration
}

// DefaultExecutionPolicy returns sensible defaults
func DefaultExecutionPolicy() ExecutionPolicy {
	return ExecutionPolicy{
		FailurePolicy:  FailurePolicyFailFast,
		MaxConcurrency: 10,
		DefaultTimeout: 5 * time.Minute,
		DefaultRetries: 0,
		RetryBackoff:   time.Second,
	}
}

// =============================================================================
// Node Configuration
// =============================================================================

// NodeConfig configures a single DAG node
type NodeConfig struct {
	// ID is the unique node identifier
	ID string

	// AgentType is the agent that will execute this node
	AgentType string

	// Prompt is the task prompt for the agent
	Prompt string

	// Context provides additional context for the node
	Context map[string]any

	// Dependencies are IDs of nodes this node depends on
	Dependencies []string

	// Timeout overrides the default timeout
	Timeout time.Duration

	// Retries overrides the default retry count
	Retries int

	// Priority affects execution order within a layer (higher = first)
	Priority int

	// Metadata holds arbitrary node metadata
	Metadata map[string]any
}

// =============================================================================
// Results
// =============================================================================

// NodeResult contains the result of a node execution
type NodeResult struct {
	// NodeID is the ID of the node
	NodeID string

	// State is the final state of the node
	State NodeState

	// Output is the output from the agent
	Output any

	// Error is the error if the node failed
	Error error

	// StartTime is when execution started
	StartTime time.Time

	// EndTime is when execution ended
	EndTime time.Time

	// Duration is the execution duration
	Duration time.Duration

	// Retries is the number of retries performed
	Retries int

	// Metadata holds additional result metadata
	Metadata map[string]any
}

// IsSuccess returns true if the node succeeded
func (r *NodeResult) IsSuccess() bool {
	return r.State == NodeStateSucceeded
}

// DAGResult contains the result of a DAG execution
type DAGResult struct {
	// ID is the DAG ID
	ID string

	// State is the overall execution state
	State DAGState

	// NodeResults maps node IDs to their results
	NodeResults map[string]*NodeResult

	// StartTime is when execution started
	StartTime time.Time

	// EndTime is when execution ended
	EndTime time.Time

	// Duration is the total execution duration
	Duration time.Duration

	// NodesSucceeded is the count of succeeded nodes
	NodesSucceeded int

	// NodesFailed is the count of failed nodes
	NodesFailed int

	// NodesSkipped is the count of skipped nodes
	NodesSkipped int

	// Error is the overall error if DAG failed
	Error error
}

// IsSuccess returns true if the DAG succeeded
func (r *DAGResult) IsSuccess() bool {
	return r.State == DAGStateSucceeded
}

// =============================================================================
// DAG State
// =============================================================================

// DAGState represents the execution state of a DAG
type DAGState int

const (
	// DAGStatePending indicates the DAG is waiting to start
	DAGStatePending DAGState = iota
	// DAGStateRunning indicates the DAG is currently executing
	DAGStateRunning
	// DAGStateSucceeded indicates the DAG completed successfully
	DAGStateSucceeded
	// DAGStateFailed indicates the DAG failed
	DAGStateFailed
	// DAGStateCancelled indicates the DAG was cancelled
	DAGStateCancelled
)

// String returns the string representation of a DAG state
func (s DAGState) String() string {
	switch s {
	case DAGStatePending:
		return "pending"
	case DAGStateRunning:
		return "running"
	case DAGStateSucceeded:
		return "succeeded"
	case DAGStateFailed:
		return "failed"
	case DAGStateCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// IsTerminal returns true if this is a terminal state
func (s DAGState) IsTerminal() bool {
	return s == DAGStateSucceeded || s == DAGStateFailed || s == DAGStateCancelled
}

// =============================================================================
// Status
// =============================================================================

// DAGStatus contains the current status of a DAG execution
type DAGStatus struct {
	// ID is the DAG ID
	ID string

	// State is the current state
	State DAGState

	// CurrentLayer is the currently executing layer index
	CurrentLayer int

	// TotalLayers is the total number of layers
	TotalLayers int

	// Progress is the completion percentage (0-1)
	Progress float64

	// NodesTotal is the total number of nodes
	NodesTotal int

	// NodesCompleted is the count of completed nodes
	NodesCompleted int

	// NodesRunning is the count of currently running nodes
	NodesRunning int

	// NodeStates maps node IDs to their current states
	NodeStates map[string]NodeState

	// StartTime is when execution started
	StartTime time.Time

	// EstimatedCompletion is the estimated completion time
	EstimatedCompletion time.Time
}

// =============================================================================
// Events
// =============================================================================

// EventType represents the type of DAG event
type EventType int

const (
	EventDAGStarted EventType = iota
	EventDAGCompleted
	EventDAGFailed
	EventDAGCancelled
	EventLayerStarted
	EventLayerCompleted
	EventNodeQueued
	EventNodeStarted
	EventNodeCompleted
	EventNodeFailed
	EventNodeRetrying
	EventNodeSkipped
	EventNodeCancelled
)

// String returns the string representation of an event type
func (e EventType) String() string {
	switch e {
	case EventDAGStarted:
		return "dag_started"
	case EventDAGCompleted:
		return "dag_completed"
	case EventDAGFailed:
		return "dag_failed"
	case EventDAGCancelled:
		return "dag_cancelled"
	case EventLayerStarted:
		return "layer_started"
	case EventLayerCompleted:
		return "layer_completed"
	case EventNodeQueued:
		return "node_queued"
	case EventNodeStarted:
		return "node_started"
	case EventNodeCompleted:
		return "node_completed"
	case EventNodeFailed:
		return "node_failed"
	case EventNodeRetrying:
		return "node_retrying"
	case EventNodeSkipped:
		return "node_skipped"
	case EventNodeCancelled:
		return "node_cancelled"
	default:
		return "unknown"
	}
}

// Event represents a DAG execution event
type Event struct {
	Type      EventType
	DAGID     string
	NodeID    string
	Layer     int
	Timestamp time.Time
	Data      map[string]any
}

// EventHandler is a callback for DAG events
type EventHandler func(event *Event)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrDAGNotFound indicates the DAG was not found
	ErrDAGNotFound = errors.New("DAG not found")

	// ErrDAGAlreadyRunning indicates the DAG is already running
	ErrDAGAlreadyRunning = errors.New("DAG is already running")

	// ErrDAGNotRunning indicates the DAG is not running
	ErrDAGNotRunning = errors.New("DAG is not running")

	// ErrDAGCancelled indicates the DAG was cancelled
	ErrDAGCancelled = errors.New("DAG was cancelled")

	// ErrNodeNotFound indicates the node was not found
	ErrNodeNotFound = errors.New("node not found")

	// ErrInvalidDAG indicates the DAG is invalid
	ErrInvalidDAG = errors.New("invalid DAG")

	// ErrCyclicDependency indicates a cyclic dependency was detected
	ErrCyclicDependency = errors.New("cyclic dependency detected")

	// ErrMissingDependency indicates a dependency was not found
	ErrMissingDependency = errors.New("missing dependency")

	// ErrEmptyDAG indicates the DAG has no nodes
	ErrEmptyDAG = errors.New("DAG has no nodes")

	// ErrDuplicateNode indicates a duplicate node ID
	ErrDuplicateNode = errors.New("duplicate node ID")

	// ErrNodeTimeout indicates a node execution timed out
	ErrNodeTimeout = errors.New("node execution timed out")

	// ErrExecutorClosed indicates the executor is closed
	ErrExecutorClosed = errors.New("executor is closed")
)
