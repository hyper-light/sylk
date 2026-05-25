package architect

import (
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/dag"
	coreversioning "github.com/adalundhe/sylk/core/versioning"
)

// PlanStatusClarifying is outside the iota sequence (value 10) to avoid
// shifting existing values and breaking persisted plan JSON.
const PlanStatusClarifying PlanStatus = 10

// PlanStatusSuperseded is a terminal state meaning "replaced by newer user intent".
// Distinct from Failed — the plan was not broken, it was interrupted.
const PlanStatusSuperseded PlanStatus = 11

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
	Response      string
	Intent        ArchitectIntent
	HandoffTarget string                   // Non-empty when this result triggered a handoff (e.g. "orchestrator").
	Directive     *guide.ResponseDirective // Carried to StreamEventComplete.
}

// ResponseText implements the guide-layer text extraction interface so the
// conversation history stores the clean response string, not a JSON blob.
func (r *ConversationResult) ResponseText() string {
	if r == nil {
		return ""
	}
	return r.Response
}

// ResponseDirective implements the guide-layer directiveCarrier interface so
// the Guide can extract the directive from a RouteResponse as a fallback when
// the STREAM_COMPLETE event carrying the directive is lost.
func (r *ConversationResult) ResponseDirective() *guide.ResponseDirective {
	if r == nil {
		return nil
	}
	return r.Directive
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
		PlanStatusClarifying:    "clarifying",
		PlanStatusDesigning:     "designing",
		PlanStatusGenerating:    "generating",
		PlanStatusOrchestrating: "orchestrating",
		PlanStatusReady:         "ready",
		PlanStatusExecuting:     "executing",
		PlanStatusCompleted:     "completed",
		PlanStatusFailed:        "failed",
		PlanStatusSuperseded:    "superseded",
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
	ArtifactVersion        coreversioning.SemanticVersion
	Error                  string
	Requirements           *Requirements
	CodebasePatterns       *CodebasePatterns
	Architecture           *SolutionArchitecture
	Tasks                  []*AtomicTask
	Workflow               *WorkflowDAG
	Constraints            *PlanConstraints
	Consultations          map[string]*ConsultationEvidence
	EvidenceTrail          []*PlanEvidence
	Declarations           []*PreDelegationDeclaration
	PlanFile               string
	Todos                  []PlanTodo
	RiskSummary            []string
	ClarificationQuestions []string
	Assumptions            []string
	UserResponse           string
	RequestCorrelationID   string
	UpdatedAt              time.Time
	CreatedAt              time.Time
	CompletedAt            time.Time

	// Distributed lifecycle fields for epoch-based stale detection and lease management.
	Epoch       uint64               `json:"epoch"`
	LeaseExpiry time.Time            `json:"lease_expiry"`
	LeaseHolder string               `json:"lease_holder,omitempty"`
	PendingWork *PendingContinuation `json:"pending_work,omitempty"`

	// GuardianAttestation, when non-nil, is the Guardian-issued
	// preflight verdict for this plan revision. Populated during plan
	// finalization (after generateAtomicTasks + createWorkflowDAG)
	// and travels with PlanHandoff at dispatch. Invalidated on every
	// plan revision (Modify path) so the next presentation gets a
	// fresh verdict bound to the new task set's content hash.
	GuardianAttestation *agentshared.PlanPreflightAttestation `json:"guardian_attestation,omitempty"`

	// HandoffPayloadArtifactID is the ID of the plan_handoff_payload
	// artifact submitted at plan-finalize. The payload artifact carries
	// the full PlanHandoff JSON; the dispatch claim's validation
	// references this ID so the orchestrator can resolve the artifact
	// and run ingestPlan deterministically (no LLM tool loop, no
	// parallel bus message). Updated on every plan revision (Modify
	// path) so dispatch always references the current revision's
	// artifact.
	HandoffPayloadArtifactID string `json:"handoff_payload_artifact_id,omitempty"`

	// sm is not serialized; reconstructed on restore or creation.
	sm *PlanStateMachine `json:"-"`
}

type PendingContinuation struct {
	Kind          string    `json:"kind"`
	Status        string    `json:"status"`
	TargetAgentID string    `json:"target_agent_id,omitempty"`
	CorrelationID string    `json:"correlation_id,omitempty"`
	ToolName      string    `json:"tool_name,omitempty"`
	Message       string    `json:"message,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
}

// SM returns the plan's state machine, lazily initializing from the
// current Status if needed (e.g. after JSON deserialization).
func (p *DesignPlan) SM() *PlanStateMachine {
	if p.sm == nil {
		p.sm = NewPlanStateMachine(p.ID, p.Status)
	}
	return p.sm
}

// ReadyDirective returns a ResponseDirective when the plan is in Ready
// state, nil otherwise. This is a derived value — no stored field.
func (p *DesignPlan) ReadyDirective() *guide.ResponseDirective {
	if p.SM().State() != PlanStatusReady {
		return nil
	}
	return readyPlanDirective(p.ID, p.SM().Epoch())
}

// IsClarifying returns true when the plan is waiting for user clarification.
func (p *DesignPlan) IsClarifying() bool {
	return p.SM().State() == PlanStatusClarifying
}

// ResponseText implements the guide-layer text extraction interface so the
// conversation history stores the human-readable plan summary, not a JSON blob.
func (p *DesignPlan) ResponseText() string {
	if p == nil {
		return ""
	}
	return p.UserResponse
}

// ResponseDirective implements the guide-layer directiveCarrier interface so
// the Guide can extract the directive from a RouteResponse as a fallback when
// the STREAM_COMPLETE event carrying the directive is lost.
func (p *DesignPlan) ResponseDirective() *guide.ResponseDirective {
	if p == nil {
		return nil
	}
	return p.ReadyDirective()
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
	Slug            string
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
	Workspace           TaskWorkspaceSpec
	WorkerPackets       []WorkerPacket
	ExecutionContracts  []AgentExecutionContract

	// Claims: precise, atomic assertions with validations. The primary
	// work specification — replaces vague task descriptions with
	// structured claims that agents work against.
	Claims []TaskClaim

	// Co-tenancy fields for compound node dispatch.
	CoAgents          []string
	CollaborationMode dag.CollaborationMode
	MaxReviewRounds   int          // 0 = sequential default, >0 for adversarial
	AgentScopes       []AgentScope // Per-agent scoped specifications for compound tasks

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

// TaskClaim is the Architect's representation of a claim. Converted to
// the claims package's Claim type when the orchestrator populates the
// board. Each claim is precise and atomic — not "implement JWT
// middleware" but "implement HS256 JWK deserialization."
type TaskClaim struct {
	ID          string                `json:"id"`
	Title       string                `json:"title"`
	Description string                `json:"description"`
	Subject     string                `json:"subject"`  // agent type: "engineer", "designer", "tester-pipeline"
	Scope       []TaskClaimScope      `json:"scope,omitempty"`
	Validations []TaskClaimValidation `json:"validations"`
	DependsOn   []string              `json:"depends_on,omitempty"` // other claim IDs
	Priority    int                   `json:"priority,omitempty"`
	Tags        []string              `json:"tags,omitempty"`
}

// TaskClaimScope identifies one element of a claim's affected scope.
type TaskClaimScope struct {
	Kind string `json:"kind"` // "file", "symbol", "api", "test_surface", "component"
	Key  string `json:"key"`
}

// TaskClaimValidation is a single precise, atomic means of verifying a
// claim, paired with a quality bar statement.
type TaskClaimValidation struct {
	ID          string `json:"id,omitempty"`
	Description string `json:"description"` // precise, atomic validation method
	QualityBar  string `json:"quality_bar"` // standards/expectations statement
	Type        string `json:"type"`        // "test", "inspection", "integration", "contract", "design", "regression", "receipt"
}

// AgentScope defines a single agent's responsibilities within a compound task.
// The Architect's LLM produces these to tell each agent exactly what to do.
type AgentScope struct {
	AgentType           string                `json:"agent_type"`
	Role                string                `json:"role"` // "primary" | "co_agent"
	AcceptanceCriteria  []AcceptanceCriterion `json:"acceptance_criteria"`
	ImplementationGuide string                `json:"implementation_guide"`
	AffectedFiles       []TaskFileTarget      `json:"affected_files"`
	Guidelines          []string              `json:"guidelines"`
	TestRequirements    []string              `json:"test_requirements"`
}

// TaskWorkspaceSpec declares the sparse repository surface a task pipeline
// needs mounted into its in-memory VFS.
type TaskWorkspaceSpec struct {
	BaseVersion   string   `json:"base_version,omitempty"`
	ReadSet       []string `json:"read_set,omitempty"`
	WriteSet      []string `json:"write_set,omitempty"`
	TestSurface   []string `json:"test_surface,omitempty"`
	PrefetchPaths []string `json:"prefetch_paths,omitempty"`
}

// WorkerPacket is the per-agent execution contract the Architect emits for
// pipeline-local workers. It is more concrete than free-form guidance and is
// used by the inspector/tester/engineer/designer path directly.
type WorkerPacket struct {
	AgentType           string                `json:"agent_type"`
	Role                string                `json:"role"` // "primary" | "co_agent"
	Objective           string                `json:"objective,omitempty"`
	Responsibilities    []string              `json:"responsibilities,omitempty"`
	AcceptanceCriteria  []AcceptanceCriterion `json:"acceptance_criteria,omitempty"`
	ImplementationGuide string                `json:"implementation_guide,omitempty"`
	AffectedFiles       []TaskFileTarget      `json:"affected_files,omitempty"`
	ReadSet             []string              `json:"read_set,omitempty"`
	WriteSet            []string              `json:"write_set,omitempty"`
	Guidelines          []string              `json:"guidelines,omitempty"`
	TestRequirements    []string              `json:"test_requirements,omitempty"`
}

// AgentExecutionContract is the stage/worker contract the Architect emits for
// each runtime agent participating in the task pipeline. It declares the
// explicit intents and deliverables the downstream worker should satisfy.
type AgentExecutionContract struct {
	AgentType    string   `json:"agent_type"`
	Intents      []string `json:"intents,omitempty"`
	Deliverables []string `json:"deliverables,omitempty"`
}

// TaskExample provides a concrete code or pattern example for the task.
type TaskExample struct {
	Label       string // Short description of what the example shows
	Language    string // Code fence info string (e.g. "go", "sh", "json", "mermaid")
	Code        string // Code snippet or pattern; stored without surrounding backticks
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

type EvidenceKind string

const (
	EvidenceKindConsult EvidenceKind = "consult"
)

type PlanEvidence struct {
	ID               string
	Kind             EvidenceKind
	PlanID           string
	Target           string
	Query            string
	Scope            string
	Correlation      string
	Success          bool
	Data             any
	Summary          string
	Error            string
	RequestedAt      time.Time
	ReceivedAt       time.Time
	SourceTool       string
	SourceArtifactID string
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
	PlanName         string
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
// PlanHandoffPhase identifies which step of the two-phase orchestrator
// ingest a particular handoff payload represents.
//
// Empty (default) preserves the legacy single-phase flow: the
// orchestrator runs prepareExecution + submitExecution in one shot
// when an ingest_plan handler receives the handoff. This is what
// existing tests and any non-upgraded caller produces.
//
// "prepare" splits the work: the orchestrator does prepareExecution
// only and stores a prepared DAG. The plan does NOT run yet.
// Architect publishes this immediately after plan-finalization
// (after Guardian preflight), so the orchestrator's prep cost
// overlaps with the user's approval-dialog review time.
//
// "execute_prepared" looks up the prepared DAG for plan_id and
// transitions it to running via scheduler.Submit. Architect
// publishes this on approval. Tens-of-ms vs. the legacy path's
// hundreds-of-ms because all the prep work has already finished.
//
// "discard_prepared" drops a prepared DAG without ever submitting.
// Architect publishes this on user reject/modify so the orchestrator
// frees its prep state cheaply.
type PlanHandoffPhase string

const (
	PlanHandoffPhaseLegacy           PlanHandoffPhase = ""
	PlanHandoffPhasePrepare          PlanHandoffPhase = "prepare"
	PlanHandoffPhaseExecutePrepared  PlanHandoffPhase = "execute_prepared"
	PlanHandoffPhaseDiscardPrepared  PlanHandoffPhase = "discard_prepared"
)

type PlanHandoff struct {
	PlanID          string                `json:"plan_id"`
	SessionID       string                `json:"session_id"`
	Query           string                `json:"query"`
	Revision        int                   `json:"revision"`
	Phase           PlanHandoffPhase      `json:"phase,omitempty"`
	Tasks           []*HandoffTask        `json:"tasks"`
	ExecutionLayers [][]string            `json:"execution_layers"`
	CriticalPath    []string              `json:"critical_path"`
	Constraints     *PlanConstraints      `json:"constraints"`
	TotalTokens     int                   `json:"total_tokens"`
	RiskSummary     []string              `json:"risk_summary,omitempty"`
	Trigger         string                `json:"trigger"` // "user-approved" | "auto"
	PlanFile        string                `json:"plan_file,omitempty"`
	Timestamp       time.Time             `json:"timestamp"`
	Architecture    *SolutionArchitecture `json:"architecture,omitempty"`
	Requirements    *Requirements         `json:"requirements,omitempty"`
	Assumptions     []string              `json:"assumptions,omitempty"`

	// GuardianAttestation is Guardian's batched preflight verdict for
	// every task in this handoff. The architect requests it once
	// during plan finalization and folds findings into the approval
	// dialog — the user's approve click acts on a plan that Guardian
	// has already gated. The orchestrator verifies the attestation
	// at ingest (PlanContentHash recompute, ClassifierVersion check,
	// PerTask coverage) and does NOT re-run the classifier.
	//
	// Guardian remains the canonical gate authority; this field just
	// carries the gate's verdict alongside the plan it gated.
	GuardianAttestation *agentshared.PlanPreflightAttestation `json:"guardian_attestation,omitempty"`
}

// HandoffTask is the wire format for a single task in PlanHandoff.
// Mirrors AtomicTask but uses JSON tags for clean serialization and
// includes all rich specification fields.
type HandoffTask struct {
	ID                  string                   `json:"id"`
	Slug                string                   `json:"slug,omitempty"`
	Name                string                   `json:"name"`
	Description         string                   `json:"description"`
	AgentType           string                   `json:"agent_type"`
	Dependencies        []string                 `json:"dependencies"`
	EstimatedTokens     int                      `json:"estimated_tokens"`
	Complexity          string                   `json:"complexity"`
	Priority            int                      `json:"priority"`
	SuccessCriteria     []string                 `json:"success_criteria"`
	AcceptanceCriteria  []AcceptanceCriterion    `json:"acceptance_criteria,omitempty"`
	Guidelines          []string                 `json:"guidelines,omitempty"`
	ImplementationGuide string                   `json:"implementation_guide,omitempty"`
	Examples            []TaskExample            `json:"examples,omitempty"`
	AffectedFiles       []TaskFileTarget         `json:"affected_files,omitempty"`
	TestRequirements    []string                 `json:"test_requirements,omitempty"`
	RiskFactors         []string                 `json:"risk_factors,omitempty"`
	Workspace           TaskWorkspaceSpec        `json:"workspace,omitempty"`
	WorkerPackets       []WorkerPacket           `json:"worker_packets,omitempty"`
	ExecutionContracts  []AgentExecutionContract `json:"execution_contracts,omitempty"`
	CoAgents            []string                 `json:"co_agents,omitempty"`
	CollaborationMode   string                   `json:"collaboration_mode,omitempty"`
	MaxReviewRounds     int                      `json:"max_review_rounds,omitempty"`
	AgentScopes         []AgentScope             `json:"agent_scopes,omitempty"`

	// Claims: precise, atomic assertions with validations.
	Claims []TaskClaim `json:"claims,omitempty"`
}
