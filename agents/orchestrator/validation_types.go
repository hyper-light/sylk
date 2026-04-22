package orchestrator

import (
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
)

type ValidationEpochStatus string

const (
	ValidationEpochStatusOpen              ValidationEpochStatus = "open"
	ValidationEpochStatusQuiescing         ValidationEpochStatus = "quiescing"
	ValidationEpochStatusInspecting        ValidationEpochStatus = "inspecting"
	ValidationEpochStatusTesting           ValidationEpochStatus = "testing"
	ValidationEpochStatusPassed            ValidationEpochStatus = "passed"
	ValidationEpochStatusFailedBlocking    ValidationEpochStatus = "failed_blocking"
	ValidationEpochStatusFailedRetryable   ValidationEpochStatus = "failed_retryable"
	ValidationEpochStatusAwaitingArchitect ValidationEpochStatus = "awaiting_architect"
	ValidationEpochStatusReadyToFlush      ValidationEpochStatus = "ready_to_flush"
	ValidationEpochStatusFlushed           ValidationEpochStatus = "flushed"
)

type ExecutionHoldStatus string

const (
	ExecutionHoldStatusActive   ExecutionHoldStatus = "active"
	ExecutionHoldStatusResolved ExecutionHoldStatus = "resolved"
	ExecutionHoldStatusAborted  ExecutionHoldStatus = "aborted"
	// ExecutionHoldStatusSuperseded is a terminal state applied when
	// a new plan takes over the session. Any active hold from a
	// prior plan_id transitions here; the remediation context of the
	// prior plan is, by definition, no longer the right context to
	// resolve it in. See SupersedePriorPlans.
	ExecutionHoldStatusSuperseded ExecutionHoldStatus = "superseded"
)

type RemediationCaseStatus string

const (
	RemediationCaseStatusOpen               RemediationCaseStatus = "open"
	RemediationCaseStatusArchitectAnalyzing RemediationCaseStatus = "architect_analyzing"
	RemediationCaseStatusArchitectRevising  RemediationCaseStatus = "architect_revising"
	RemediationCaseStatusAwaitingApply      RemediationCaseStatus = "awaiting_orchestrator_apply"
	RemediationCaseStatusApplied            RemediationCaseStatus = "applied"
	RemediationCaseStatusRejected           RemediationCaseStatus = "rejected"
	RemediationCaseStatusNeedsUserInput     RemediationCaseStatus = "needs_user_input"
)

type ValidationEpochRecord struct {
	EpochID          string
	SessionID        string
	Status           ValidationEpochStatus
	Reason           string
	WorkflowIDs      []string
	DAGIDs           []string
	TaskIDs          []string
	PlanID           string
	Summary          string
	ValidatorVerdict *agentshared.ValidationVerdictPayload
	CreatedAt        time.Time
	UpdatedAt        time.Time
	CompletedAt      *time.Time
}

type ExecutionHoldRecord struct {
	HoldID             string
	SessionID          string
	// PlanID scopes the hold to the workflow that opened it. See
	// schema.sql and docs/EXECUTION_HOLDS.md. A hold with plan_id=A
	// does not block DAGs submitted under plan_id=B.
	PlanID             string
	EpochID            string
	RemediationCaseID  string
	Status             ExecutionHoldStatus
	Reason             string
	Summary            string
	// BlocksDAGIDs constrains the blocking scope. Empty/nil means
	// "block every DAG inside this hold's PlanID" (the common
	// validation-hold case). A non-empty list constrains to the
	// named DAGs (per-DAG scoped holds). Stored as JSON in the
	// blocks_dag_ids column.
	BlocksDAGIDs       []string
	// ExemptDAGIDs lists DAGs that may run despite this hold — the
	// remediation DAG the architect launches to address the hold's
	// concern. Stored as JSON in the exempt_dag_ids column.
	ExemptDAGIDs       []string
	CreatedByAgentID   string
	CreatedByAgentType string
	ReleasedAt         *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type RemediationCaseRecord struct {
	CaseID      string
	SessionID   string
	EpochID     string
	HoldID      string
	PlanID      string
	Status      RemediationCaseStatus
	Summary     string
	Request     *agentshared.RemediationRequest
	Result      *agentshared.RemediationResult
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
}
