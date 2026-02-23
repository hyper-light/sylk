package architect

import (
	"time"

	"github.com/adalundhe/sylk/agents/guide"
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
	IntentHelp          ArchitectIntent = "help"
	IntentEstimate      ArchitectIntent = "estimate"
	IntentConsult       ArchitectIntent = "consult"
	IntentChat          ArchitectIntent = "chat"
	IntentExecute       ArchitectIntent = "execute"
	IntentConverse      ArchitectIntent = IntentChat // Backward-compatible alias.
)

// ConversationResult holds the response from a conversational (non-planning) interaction.
type ConversationResult struct {
	Response string
	Intent   ArchitectIntent
}

func (i ArchitectIntent) String() string {
	return string(i)
}

type ArchitectRequest struct {
	ID                  string
	Intent              ArchitectIntent
	Query               string
	Params              map[string]any
	SessionID           string
	Timestamp           time.Time
	ConversationHistory []guide.ConversationTurn
}

type ArchitectResponse struct {
	ID           string
	RequestID    string
	Success      bool
	Data         any
	UserResponse string
	Error        string
	Took         time.Duration
	Timestamp    time.Time
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
	ID                     string
	SessionID              string
	Query                  string
	Status                 PlanStatus
	Revision               int
	Error                  string
	Requirements           *Requirements
	CodebasePatterns       *CodebasePatterns
	Architecture           *SolutionArchitecture
	Tasks                  []*AtomicTask
	Workflow               *WorkflowDAG
	Constraints            *PlanConstraints
	Consultations          map[string]*ConsultationEvidence
	Declarations           []*PreDelegationDeclaration
	PlanFile               string
	Todos                  []PlanTodo
	RiskSummary            []string
	ClarificationNeeded    bool
	ClarificationQuestions []string
	Assumptions            []string
	UserResponse           string
	UpdatedAt              time.Time
	CreatedAt              time.Time
	CompletedAt            time.Time
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

	// Rich specification fields for Jira-like task items.
	AcceptanceCriteria  []AcceptanceCriterion
	Guidelines          []string
	ImplementationGuide string
	Examples            []TaskExample
	AffectedFiles       []TaskFileTarget
	TestRequirements    []string
	RiskFactors         []string

	CreatedAt   time.Time
	StartedAt   time.Time
	CompletedAt time.Time
}

// AcceptanceCriterion defines a single verifiable acceptance condition.
// Uses Given/When/Then structure for unambiguous testability.
type AcceptanceCriterion struct {
	Given    string // Precondition state
	When     string // Action or trigger
	Then     string // Expected outcome
	Priority string // "must" | "should" | "could"
}

// TaskExample provides a concrete code or pattern example for the task.
type TaskExample struct {
	Label       string // Short description of what the example shows
	Code        string // Code snippet or pattern
	Explanation string // Why this example is relevant
}

// TaskFileTarget identifies a file that the task must create or modify.
type TaskFileTarget struct {
	Path      string // Relative file path (forward slashes)
	Operation string // "create" | "modify" | "delete"
	Reason    string // Why this file is affected
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

type ConsultationEvidence struct {
	Target      string
	Query       string
	Scope       string
	Correlation string
	Success     bool
	Data        any
	Error       string
	RequestedAt time.Time
	ReceivedAt  time.Time
}

type PreDelegationDeclaration struct {
	ID                 string
	PlanID             string
	TaskID             string
	TargetAgent        string
	Reasoning          string
	RequiredSkills     []string
	ExpectedOutcome    string
	FailureCriteria    string
	UserClarification  bool
	ChallengesRaised   []string
	ConsultationChecks map[string]*ConsultationEvidence
	CreatedAt          time.Time
}

type PlanTodo struct {
	Content    string
	Status     string
	ActiveForm string
}

type PlanModeState struct {
	SessionID        string
	PlanID           string
	Enabled          bool
	AwaitingApproval bool
	PlanFile         string
	AllowedPrompts   []string
	Todos            []PlanTodo
	UpdatedAt        time.Time
}

type ResearchProposal struct {
	ResearchSlug string
	PaperPath    string
	Version      int
	Summary      string
	SessionID    string
	ProjectHash  string
}

// PlanHandoff is the structured payload sent from the architect to the
// orchestrator when a plan is approved for execution. Contains everything
// the orchestrator needs to build a DAG, create task records, and begin
// execution without reading external files.
type PlanHandoff struct {
	PlanID          string                `json:"plan_id"`
	SessionID       string                `json:"session_id"`
	Query           string                `json:"query"`
	Revision        int                   `json:"revision"`
	Tasks           []*HandoffTask        `json:"tasks"`
	ExecutionLayers [][]string            `json:"execution_layers"`
	CriticalPath    []string              `json:"critical_path"`
	Constraints     *PlanConstraints      `json:"constraints"`
	TotalTokens     int                   `json:"total_tokens"`
	RiskSummary     []string              `json:"risk_summary,omitempty"`
	Trigger         string                `json:"trigger"` // "user-approved" | "auto"
	Timestamp       time.Time             `json:"timestamp"`
}

// HandoffTask is the wire format for a single task in PlanHandoff.
// Mirrors AtomicTask but uses JSON tags for clean serialization and
// includes all rich specification fields.
type HandoffTask struct {
	ID                  string                `json:"id"`
	Name                string                `json:"name"`
	Description         string                `json:"description"`
	AgentType           string                `json:"agent_type"`
	Dependencies        []string              `json:"dependencies"`
	EstimatedTokens     int                   `json:"estimated_tokens"`
	Complexity          string                `json:"complexity"`
	Priority            int                   `json:"priority"`
	SuccessCriteria     []string              `json:"success_criteria"`
	AcceptanceCriteria  []AcceptanceCriterion `json:"acceptance_criteria,omitempty"`
	Guidelines          []string              `json:"guidelines,omitempty"`
	ImplementationGuide string                `json:"implementation_guide,omitempty"`
	Examples            []TaskExample         `json:"examples,omitempty"`
	AffectedFiles       []TaskFileTarget      `json:"affected_files,omitempty"`
	TestRequirements    []string              `json:"test_requirements,omitempty"`
	RiskFactors         []string              `json:"risk_factors,omitempty"`
}
