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
	EpochID            string
	RemediationCaseID  string
	Status             ExecutionHoldStatus
	Reason             string
	Summary            string
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
