package architect

import (
	"time"

	"github.com/adalundhe/sylk/core/dag"
)

type ArchitectIntent string

const (
	IntentPlan          ArchitectIntent = "plan"
	IntentDesign        ArchitectIntent = "design"
	IntentGenerateTasks ArchitectIntent = "generate_tasks"
	IntentCreateDAG     ArchitectIntent = "create_dag"
	IntentRecall        ArchitectIntent = "recall"
	IntentCheck         ArchitectIntent = "check"
	IntentEstimate      ArchitectIntent = "estimate"
	IntentConsult       ArchitectIntent = "consult"
)

func (i ArchitectIntent) String() string {
	return string(i)
}

type ArchitectRequest struct {
	ID        string
	Intent    ArchitectIntent
	Query     string
	Params    map[string]any
	SessionID string
	Timestamp time.Time
}

type ArchitectResponse struct {
	ID        string
	RequestID string
	Success   bool
	Data      any
	Error     string
	Took      time.Duration
	Timestamp time.Time
}

type PlanStatus int

const (
	PlanStatusPending PlanStatus = iota
	PlanStatusAnalyzing
	PlanStatusConsulting
	PlanStatusDesigning
	PlanStatusGenerating
	PlanStatusOrchestrating
	PlanStatusReady
	PlanStatusExecuting
	PlanStatusCompleted
	PlanStatusFailed
)

func (s PlanStatus) String() string {
	names := map[PlanStatus]string{
		PlanStatusPending:       "pending",
		PlanStatusAnalyzing:     "analyzing",
		PlanStatusConsulting:    "consulting",
		PlanStatusDesigning:     "designing",
		PlanStatusGenerating:    "generating",
		PlanStatusOrchestrating: "orchestrating",
		PlanStatusReady:         "ready",
		PlanStatusExecuting:     "executing",
		PlanStatusCompleted:     "completed",
		PlanStatusFailed:        "failed",
	}
	if name, ok := names[s]; ok {
		return name
	}
	return "unknown"
}

type DesignPlan struct {
	ID               string
	Query            string
	Status           PlanStatus
	Error            string
	Requirements     *Requirements
	CodebasePatterns *CodebasePatterns
	Architecture     *SolutionArchitecture
	Tasks            []*AtomicTask
	Workflow         *WorkflowDAG
	Constraints      *PlanConstraints
	CreatedAt        time.Time
	CompletedAt      time.Time
}

type PlanConstraints struct {
	Scope            string
	MaxTasksPerAgent int
	AllowParallel    bool
	MaxConcurrency   int
	Timeout          time.Duration
	TargetAgents     []string
}

type Requirements struct {
	Query        string
	Goals        []string
	Constraints  []string
	Dependencies []string
	Scope        string
	Priority     string
	Metadata     map[string]any
}

type CodebasePatterns struct {
	Patterns           []PatternInfo
	RelevantFiles      []string
	ExistingComponents []string
	TestPatterns       []PatternInfo
	ErrorPatterns      []PatternInfo
}

type PatternInfo struct {
	Name        string
	Description string
	Example     string
	FilePath    string
	Category    string
	Confidence  float64
}

type SolutionArchitecture struct {
	Name        string
	Description string
	Components  []ComponentSpec
	Interfaces  []InterfaceSpec
	Patterns    []string
	Layers      []ArchitectureLayer
	Metadata    map[string]any
}

type ComponentSpec struct {
	Name         string
	Type         string
	Description  string
	Dependencies []string
	Interfaces   []string
	FilePath     string
	Metadata     map[string]any
}

type InterfaceSpec struct {
	Name        string
	From        string
	To          string
	Type        string
	Description string
	Methods     []MethodSpec
}

type MethodSpec struct {
	Name       string
	Parameters []string
	Returns    string
}

type ArchitectureLayer struct {
	Name       string
	Components []string
	Order      int
}

type TaskStatus int

const (
	TaskStatusPending TaskStatus = iota
	TaskStatusQueued
	TaskStatusRunning
	TaskStatusCompleted
	TaskStatusFailed
	TaskStatusBlocked
	TaskStatusSkipped
)

func (s TaskStatus) String() string {
	names := map[TaskStatus]string{
		TaskStatusPending:   "pending",
		TaskStatusQueued:    "queued",
		TaskStatusRunning:   "running",
		TaskStatusCompleted: "completed",
		TaskStatusFailed:    "failed",
		TaskStatusBlocked:   "blocked",
		TaskStatusSkipped:   "skipped",
	}
	if name, ok := names[s]; ok {
		return name
	}
	return "unknown"
}

type TaskComplexity int

const (
	ComplexityLow TaskComplexity = iota
	ComplexityMedium
	ComplexityHigh
	ComplexityCritical
)

func (c TaskComplexity) String() string {
	names := map[TaskComplexity]string{
		ComplexityLow:      "low",
		ComplexityMedium:   "medium",
		ComplexityHigh:     "high",
		ComplexityCritical: "critical",
	}
	if name, ok := names[c]; ok {
		return name
	}
	return "unknown"
}

type AtomicTask struct {
	ID              string
	Name            string
	Description     string
	AgentType       string
	SuccessCriteria []string
	Dependencies    []string
	EstimatedTokens int
	Complexity      TaskComplexity
	Status          TaskStatus
	Priority        int
	Inputs          map[string]any
	Outputs         map[string]any
	Context         map[string]any
	Result          *TaskResult
	CreatedAt       time.Time
	StartedAt       time.Time
	CompletedAt     time.Time
}

type TaskResult struct {
	Success bool
	Output  any
	Error   string
	Metrics TaskMetrics
}

type TaskMetrics struct {
	TokensUsed   int
	Duration     time.Duration
	RetryCount   int
	AgentID      string
	StartTime    time.Time
	EndTime      time.Time
	MemoryPeakMB int
}

type WorkflowDAG struct {
	DAG             *dag.DAG
	Tasks           []*AtomicTask
	TotalTasks      int
	CompletedTasks  int
	FailedTasks     int
	EstimatedTokens int
	ActualTokens    int
	CriticalPath    []string
	ExecutionLayers [][]string
	Status          WorkflowStatus
	CreatedAt       time.Time
	StartedAt       time.Time
	CompletedAt     time.Time
}

type WorkflowStatus int

const (
	WorkflowStatusPending WorkflowStatus = iota
	WorkflowStatusRunning
	WorkflowStatusCompleted
	WorkflowStatusFailed
	WorkflowStatusCancelled
)

func (s WorkflowStatus) String() string {
	names := map[WorkflowStatus]string{
		WorkflowStatusPending:   "pending",
		WorkflowStatusRunning:   "running",
		WorkflowStatusCompleted: "completed",
		WorkflowStatusFailed:    "failed",
		WorkflowStatusCancelled: "cancelled",
	}
	if name, ok := names[s]; ok {
		return name
	}
	return "unknown"
}

type ComplexityEstimate struct {
	Overall         TaskComplexity
	TokenEstimate   int
	DurationMinutes int
	RiskLevel       string
	Factors         []ComplexityFactor
}

type ComplexityFactor struct {
	Name        string
	Impact      string
	Description string
}

type ConsultRequest struct {
	Target string
	Query  string
	Scope  string
	Params map[string]any
}

type ConsultResponse struct {
	Target  string
	Success bool
	Data    any
	Error   string
	Took    time.Duration
}
