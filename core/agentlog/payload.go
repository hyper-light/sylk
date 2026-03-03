package agentlog

// LifecyclePayload records agent lifecycle transitions.
type LifecyclePayload struct {
	Phase   string `json:"phase"`
	Details string `json:"details,omitempty"`
}

// BusMessagePayload records bus message events.
type BusMessagePayload struct {
	MessageID     string `json:"msg_id"`
	CorrelationID string `json:"corr_id"`
	MessageType   string `json:"msg_type"`
	SourceAgent   string `json:"src,omitempty"`
	TargetAgent   string `json:"tgt,omitempty"`
	Topic         string `json:"topic"`
	DurationNs    int64  `json:"dur_ns,omitempty"`
	Error         string `json:"err,omitempty"`
}

// LLMPayload records LLM API call metrics.
type LLMPayload struct {
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	InputTokens  int    `json:"in_tok"`
	OutputTokens int    `json:"out_tok"`
	DurationNs   int64  `json:"dur_ns"`
	Error        string `json:"err,omitempty"`
}

// SkillPayload records skill invocation events.
type SkillPayload struct {
	Name       string `json:"name"`
	DurationNs int64  `json:"dur_ns"`
	Error      string `json:"err,omitempty"`
}

// ErrorPayload records agent-level errors.
type ErrorPayload struct {
	Error string `json:"error"`
	Stack string `json:"stack,omitempty"`
}

// RegistryPayload records agent registration events.
type RegistryPayload struct {
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
	Action    string `json:"action"` // "registered" | "unregistered"
}

// AuthPayload records credential change events.
type AuthPayload struct {
	Provider  string `json:"provider"`
	Method    string `json:"method"`
	Available bool   `json:"available"`
}

// ── Guide payloads ──────────────────────────────────────────────────────────

// RoutePayload records a routing classification decision.
type RoutePayload struct {
	Intent      string  `json:"intent"`
	Domain      string  `json:"domain"`
	TargetAgent string  `json:"target_agent"`
	Confidence  float64 `json:"confidence"`
	DurNs       int64   `json:"dur_ns"`
	Model       string  `json:"model,omitempty"`
}

// ForwardPayload records a request forwarding event.
type ForwardPayload struct {
	TargetAgent string `json:"target_agent"`
	InputLen    int    `json:"input_len"`
	Intent      string `json:"intent,omitempty"`
}

// CorrelationPayload records a response correlation.
type CorrelationPayload struct {
	SourceAgent string `json:"source_agent"`
	Success     bool   `json:"success"`
	DurNs       int64  `json:"dur_ns"`
}

// StreamPayload records a streaming lifecycle event.
type StreamPayload struct {
	Phase string `json:"phase"` // "started" | "completed"
}

// ── Orchestrator payloads ───────────────────────────────────────────────────

// DAGPayload records DAG lifecycle events.
type DAGPayload struct {
	DAGID   string `json:"dag_id"`
	NodeID  string `json:"node_id,omitempty"`
	Layer   int    `json:"layer,omitempty"`
	State   string `json:"state,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
	Err     string `json:"err,omitempty"`
}

// PipelinePayload records pipeline stage events.
type PipelinePayload struct {
	PipelineID string `json:"pipeline_id"`
	Stage      string `json:"stage,omitempty"`
	State      string `json:"state,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	Err        string `json:"err,omitempty"`
}

// ── Architect payloads ──────────────────────────────────────────────────────

// PlanPayload records plan lifecycle transitions.
type PlanPayload struct {
	PlanID string `json:"plan_id"`
	Status string `json:"status"`
	Epoch  uint64 `json:"epoch,omitempty"`
}

// PlanStepPayload records an individual plan step execution.
type PlanStepPayload struct {
	PlanID   string `json:"plan_id"`
	StepName string `json:"step_name"`
	Outcome  string `json:"outcome,omitempty"`
	DurNs    int64  `json:"dur_ns,omitempty"`
}

// ConsultPayload records a consultation bus exchange.
type ConsultPayload struct {
	PlanID  string `json:"plan_id,omitempty"`
	Target  string `json:"target"`
	Success bool   `json:"success"`
	DurNs   int64  `json:"dur_ns"`
}

// ProtocolPayload records a planning protocol lifecycle.
type ProtocolPayload struct {
	PlanID string `json:"plan_id,omitempty"`
	Phase  string `json:"phase"`
	DurNs  int64  `json:"dur_ns,omitempty"`
	Err    string `json:"err,omitempty"`
}

// ── Engineer payloads ───────────────────────────────────────────────────────

// ToolPayload records a tool invocation by the engineer.
type ToolPayload struct {
	ToolName    string `json:"tool_name"`
	ArgsSummary string `json:"args_summary,omitempty"`
	Success     bool   `json:"success"`
	DurNs       int64  `json:"dur_ns,omitempty"`
	Err         string `json:"err,omitempty"`
}

// DiscoveryPayload records a discovery phase event.
type DiscoveryPayload struct {
	Phase    string `json:"phase"` // "started" | "completed"
	Type     string `json:"type,omitempty"`
	DurNs    int64  `json:"dur_ns,omitempty"`
	Findings int    `json:"findings,omitempty"`
}

// GenerationPayload records a code generation phase event.
type GenerationPayload struct {
	Phase    string `json:"phase"` // "started" | "completed"
	DurNs    int64  `json:"dur_ns,omitempty"`
	ToolRuns int    `json:"tool_runs,omitempty"`
}

// ── Designer payloads ───────────────────────────────────────────────────────

// DesignPayload records a design lifecycle event.
type DesignPayload struct {
	Phase     string `json:"phase"`
	TaskID    string `json:"task_id,omitempty"`
	Component string `json:"component,omitempty"`
	DurNs     int64  `json:"dur_ns,omitempty"`
}

// ── Guardian payloads ───────────────────────────────────────────────────────

// DiffPayload records a diff review event.
type DiffPayload struct {
	DiffID   string `json:"diff_id,omitempty"`
	FilePath string `json:"file_path,omitempty"`
	Verdict  string `json:"verdict,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// ScanPayload records a content scan event.
type ScanPayload struct {
	ScanType string `json:"scan_type"`
	Detected bool   `json:"detected"`
	Severity string `json:"severity,omitempty"`
}

// CheckpointPayload records a checkpoint lifecycle event.
type CheckpointPayload struct {
	CheckpointID string `json:"checkpoint_id"`
	Action       string `json:"action"` // "created" | "rollback"
}

// HealthPayload records a health monitoring event.
type HealthPayload struct {
	AgentID string `json:"agent_id"`
	Status  string `json:"status"`
	Metric  string `json:"metric,omitempty"`
}

// ── Inspector payloads ──────────────────────────────────────────────────────

// AuditPayload records an audit lifecycle event.
type AuditPayload struct {
	AuditID  string `json:"audit_id,omitempty"`
	Phase    string `json:"phase"`
	Severity string `json:"severity,omitempty"`
	Finding  string `json:"finding,omitempty"`
	DurNs    int64  `json:"dur_ns,omitempty"`
}

// ValidationPayload records a validation lifecycle event.
type ValidationPayload struct {
	Phase      string `json:"phase"`
	TaskID     string `json:"task_id,omitempty"`
	Success    bool   `json:"success"`
	Correction string `json:"correction,omitempty"`
	DurNs      int64  `json:"dur_ns,omitempty"`
}

// ── Tester payloads ─────────────────────────────────────────────────────────

// TestPlanPayload records test plan creation.
type TestPlanPayload struct {
	PlanID    string `json:"plan_id"`
	TestCount int    `json:"test_count"`
}

// TestCasePayload records an individual test case event.
type TestCasePayload struct {
	SuiteID  string `json:"suite_id,omitempty"`
	CaseID   string `json:"case_id"`
	CaseName string `json:"case_name"`
	Phase    string `json:"phase"` // "started" | "passed" | "failed"
	Err      string `json:"err,omitempty"`
	DurNs    int64  `json:"dur_ns,omitempty"`
}

// TestSuitePayload records a test suite lifecycle event.
type TestSuitePayload struct {
	SuiteID string `json:"suite_id"`
	Phase   string `json:"phase"` // "started" | "completed"
	Passed  int    `json:"passed,omitempty"`
	Failed  int    `json:"failed,omitempty"`
	DurNs   int64  `json:"dur_ns,omitempty"`
}

// CoveragePayload records code coverage metrics.
type CoveragePayload struct {
	LineCoverage   float64 `json:"line_coverage"`
	BranchCoverage float64 `json:"branch_coverage"`
}

// MutationPayload records mutation testing events.
type MutationPayload struct {
	Phase   string  `json:"phase"` // "started" | "completed"
	Score   float64 `json:"score,omitempty"`
	Mutants int     `json:"mutants,omitempty"`
	Killed  int     `json:"killed,omitempty"`
	DurNs   int64   `json:"dur_ns,omitempty"`
}

// FlakyPayload records a flaky test detection.
type FlakyPayload struct {
	CaseID   string `json:"case_id"`
	CaseName string `json:"case_name"`
	Evidence string `json:"evidence,omitempty"`
}
