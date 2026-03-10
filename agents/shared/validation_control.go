package shared

import "time"

const (
	ControlPlaneKindValidationVerdict  = "validation_verdict"
	ControlPlaneKindRemediationRequest = "remediation_request"
)

type ValidationVerdictKind string

const (
	ValidationVerdictPass                      ValidationVerdictKind = "pass"
	ValidationVerdictPassWithFindings          ValidationVerdictKind = "pass_with_findings"
	ValidationVerdictRetryableFailure          ValidationVerdictKind = "retryable_failure"
	ValidationVerdictBlockingFailure           ValidationVerdictKind = "blocking_failure"
	ValidationVerdictNeedsArchitectRemediation ValidationVerdictKind = "needs_architect_remediation"
	ValidationVerdictNeedsUserDecision         ValidationVerdictKind = "needs_user_decision"
)

type ValidationSeverity string

const (
	ValidationSeverityInfo     ValidationSeverity = "info"
	ValidationSeverityWarning  ValidationSeverity = "warning"
	ValidationSeverityHigh     ValidationSeverity = "high"
	ValidationSeverityCritical ValidationSeverity = "critical"
)

type ValidationFinding struct {
	ID             string             `json:"id,omitempty"`
	Kind           string             `json:"kind,omitempty"`
	Severity       ValidationSeverity `json:"severity,omitempty"`
	Scope          string             `json:"scope,omitempty"`
	File           string             `json:"file,omitempty"`
	Line           int                `json:"line,omitempty"`
	Summary        string             `json:"summary"`
	Detail         string             `json:"detail,omitempty"`
	Recommendation string             `json:"recommendation,omitempty"`
}

type ValidationVerdictPayload struct {
	Kind               ValidationVerdictKind `json:"kind"`
	Severity           ValidationSeverity    `json:"severity"`
	ValidatorAgentID   string                `json:"validator_agent_id,omitempty"`
	ValidatorType      string                `json:"validator_type,omitempty"`
	SessionID          string                `json:"session_id,omitempty"`
	EpochID            string                `json:"epoch_id,omitempty"`
	WorkflowIDs        []string              `json:"workflow_ids,omitempty"`
	DAGIDs             []string              `json:"dag_ids,omitempty"`
	PlanID             string                `json:"plan_id,omitempty"`
	AffectedTasks      []string              `json:"affected_tasks,omitempty"`
	ShouldPause        bool                  `json:"should_pause,omitempty"`
	Summary            string                `json:"summary"`
	Details            string                `json:"details,omitempty"`
	Findings           []ValidationFinding   `json:"findings,omitempty"`
	RecommendedActions []string              `json:"recommended_actions,omitempty"`
	SuggestedFixes     []string              `json:"suggested_fixes,omitempty"`
	CreatedAt          time.Time             `json:"created_at,omitempty"`
}

type RemediationRequest struct {
	CaseID             string              `json:"case_id"`
	HoldID             string              `json:"hold_id"`
	EpochID            string              `json:"epoch_id,omitempty"`
	SessionID          string              `json:"session_id"`
	PlanID             string              `json:"plan_id,omitempty"`
	WorkflowIDs        []string            `json:"workflow_ids,omitempty"`
	DAGIDs             []string            `json:"dag_ids,omitempty"`
	AffectedTasks      []string            `json:"affected_tasks,omitempty"`
	Summary            string              `json:"summary"`
	Details            string              `json:"details,omitempty"`
	Findings           []ValidationFinding `json:"findings,omitempty"`
	RecommendedActions []string            `json:"recommended_actions,omitempty"`
	SuggestedFixes     []string            `json:"suggested_fixes,omitempty"`
	CreatedAt          time.Time           `json:"created_at,omitempty"`
}

type RemediationResolution string

const (
	RemediationResolutionNoop            RemediationResolution = "noop"
	RemediationResolutionFixWorkflow     RemediationResolution = "fix_workflow"
	RemediationResolutionCancelAndReplan RemediationResolution = "cancel_and_replan"
	RemediationResolutionNeedsUserInput  RemediationResolution = "needs_user_input"
	RemediationResolutionUnrecoverable   RemediationResolution = "unrecoverable"
)

type RemediationResult struct {
	CaseID             string                `json:"case_id"`
	SessionID          string                `json:"session_id"`
	PlanID             string                `json:"plan_id,omitempty"`
	Resolution         RemediationResolution `json:"resolution"`
	Summary            string                `json:"summary"`
	UserMessage        string                `json:"user_message,omitempty"`
	FixWorkflowDAGJSON string                `json:"fix_workflow_dag_json,omitempty"`
	FixTaskCount       int                   `json:"fix_task_count,omitempty"`
	NeedsUserInput     bool                  `json:"needs_user_input,omitempty"`
	Corrections        []ValidationFinding   `json:"corrections,omitempty"`
	CreatedAt          time.Time             `json:"created_at,omitempty"`
}
