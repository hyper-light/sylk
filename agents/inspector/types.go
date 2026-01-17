// Package inspector provides types and functionality for the Inspector agent,
// which handles validation and quality assurance within the Sylk multi-agent system.
package inspector

import "time"

// InspectorMode defines the operational mode of the Inspector agent.
// The Inspector operates in two distinct modes depending on the pipeline phase.
type InspectorMode string

const (
	// PipelineInternal mode is used during TDD phases 1 and 4.
	// In Phase 1, Inspector defines success criteria and quality gates.
	// In Phase 4, Inspector validates implementation against those criteria.
	PipelineInternal InspectorMode = "pipeline_internal"

	// SessionWide mode performs comprehensive validation across the entire session,
	// checking for consistency, quality standards, and cross-cutting concerns.
	SessionWide InspectorMode = "session_wide"
)

// Severity represents the severity level of a validation issue.
type Severity string

const (
	// Critical issues block progress and must be fixed immediately.
	Critical Severity = "critical"

	// High severity issues should be addressed before completion.
	High Severity = "high"

	// Medium severity issues are important but not blocking.
	Medium Severity = "medium"

	// Low severity issues are minor and can be addressed later.
	Low Severity = "low"

	// Info represents informational messages, not actual issues.
	Info Severity = "info"
)

// InspectorCriteria defines the success criteria and quality gates for a task.
// Created during Phase 1 of the TDD pipeline and used for validation in Phase 4.
type InspectorCriteria struct {
	// TaskID uniquely identifies the task these criteria apply to.
	TaskID string `json:"task_id"`

	// SuccessCriteria lists the specific criteria that must be met.
	SuccessCriteria []SuccessCriterion `json:"success_criteria"`

	// QualityGates defines measurable thresholds that must be satisfied.
	QualityGates []QualityGate `json:"quality_gates"`

	// Constraints lists any restrictions or requirements for the implementation.
	Constraints []Constraint `json:"constraints"`

	// CreatedAt records when these criteria were defined.
	CreatedAt time.Time `json:"created_at"`
}

// SuccessCriterion represents a single success criterion for validation.
type SuccessCriterion struct {
	// ID uniquely identifies this criterion.
	ID string `json:"id"`

	// Description explains what this criterion checks.
	Description string `json:"description"`

	// Verifiable indicates whether this criterion can be automatically verified.
	Verifiable bool `json:"verifiable"`

	// VerificationMethod describes how to verify this criterion.
	// For automated criteria, this may reference a specific check or test.
	VerificationMethod string `json:"verification_method"`
}

// QualityGate represents a measurable quality threshold.
type QualityGate struct {
	// Name identifies this quality gate.
	Name string `json:"name"`

	// Threshold is the numeric value that must be met.
	Threshold float64 `json:"threshold"`

	// Metric specifies what is being measured (e.g., "coverage", "complexity").
	Metric string `json:"metric"`

	// Operator defines how to compare against the threshold.
	// Valid values: ">=", "<=", ">", "<", "==", "!=".
	Operator string `json:"operator"`
}

// Constraint represents a restriction or requirement for the implementation.
type Constraint struct {
	// Type categorizes the constraint (e.g., "performance", "security", "style").
	Type string `json:"type"`

	// Description explains the constraint in detail.
	Description string `json:"description"`

	// Required indicates whether this constraint is mandatory.
	Required bool `json:"required"`
}

// ValidationIssue represents a single issue found during validation.
type ValidationIssue struct {
	// ID uniquely identifies this issue.
	ID string `json:"id"`

	// Severity indicates the importance of this issue.
	Severity Severity `json:"severity"`

	// File is the path to the file containing the issue.
	File string `json:"file"`

	// Line is the line number where the issue was found.
	Line int `json:"line"`

	// Column is the column number where the issue starts.
	Column int `json:"column"`

	// Message describes the issue.
	Message string `json:"message"`

	// SuggestedFix provides a potential resolution for the issue.
	SuggestedFix string `json:"suggested_fix,omitempty"`

	// RuleID references the validation rule that triggered this issue.
	RuleID string `json:"rule_id,omitempty"`
}

// InspectorFeedback represents the feedback from a validation loop.
type InspectorFeedback struct {
	// Loop indicates which iteration of the feedback loop this is.
	Loop int `json:"loop"`

	// Timestamp records when this feedback was generated.
	Timestamp time.Time `json:"timestamp"`

	// Issues lists all validation issues found.
	Issues []ValidationIssue `json:"issues"`

	// Passed indicates whether validation succeeded.
	Passed bool `json:"passed"`

	// Corrections lists suggested corrections for found issues.
	Corrections []Correction `json:"corrections"`
}

// Correction represents a suggested fix for a validation issue.
type Correction struct {
	// IssueID references the ValidationIssue this correction addresses.
	IssueID string `json:"issue_id"`

	// Description explains the correction.
	Description string `json:"description"`

	// SuggestedFix provides the actual fix content.
	SuggestedFix string `json:"suggested_fix"`

	// File is the path to the file to be corrected.
	File string `json:"file"`

	// LineStart is the starting line for the correction.
	LineStart int `json:"line_start"`

	// LineEnd is the ending line for the correction.
	LineEnd int `json:"line_end"`
}

// InspectorResult represents the final result of an inspection.
type InspectorResult struct {
	// TaskID identifies the task that was validated.
	TaskID string `json:"task_id"`

	// Mode indicates which inspection mode was used.
	Mode InspectorMode `json:"mode"`

	// Passed indicates whether all criteria were satisfied.
	Passed bool `json:"passed"`

	// Issues lists all validation issues found.
	Issues []ValidationIssue `json:"issues"`

	// CriteriaMet lists the IDs of success criteria that were satisfied.
	CriteriaMet []string `json:"criteria_met"`

	// CriteriaFailed lists the IDs of success criteria that were not satisfied.
	CriteriaFailed []string `json:"criteria_failed"`

	// QualityGateResults maps gate names to their pass/fail status.
	QualityGateResults map[string]bool `json:"quality_gate_results"`

	// FeedbackHistory contains all feedback loops from this inspection.
	FeedbackHistory []InspectorFeedback `json:"feedback_history"`

	// StartedAt records when the inspection began.
	StartedAt time.Time `json:"started_at"`

	// CompletedAt records when the inspection finished.
	CompletedAt time.Time `json:"completed_at"`

	// LoopCount is the total number of feedback loops executed.
	LoopCount int `json:"loop_count"`
}

// OverrideRequest represents a user request to override a validation failure.
type OverrideRequest struct {
	// IssueID identifies the issue to override.
	IssueID string `json:"issue_id"`

	// Reason explains why the override is being requested.
	Reason string `json:"reason"`

	// RequestedBy identifies who requested the override.
	RequestedBy string `json:"requested_by"`

	// RequestedAt records when the override was requested.
	RequestedAt time.Time `json:"requested_at"`

	// Approved indicates whether the override has been approved.
	Approved bool `json:"approved"`

	// ApprovedBy identifies who approved the override.
	ApprovedBy string `json:"approved_by,omitempty"`

	// ApprovedAt records when the override was approved.
	ApprovedAt time.Time `json:"approved_at,omitempty"`
}

// MemoryThreshold defines limits for memory-based operations.
type MemoryThreshold struct {
	// MaxIssues is the maximum number of issues to retain in memory.
	MaxIssues int `json:"max_issues"`

	// MaxFeedbackLoops is the maximum number of feedback loops to retain.
	MaxFeedbackLoops int `json:"max_feedback_loops"`

	// MaxCorrectionSize is the maximum size in bytes for a single correction.
	MaxCorrectionSize int `json:"max_correction_size"`

	// TotalMemoryLimit is the total memory limit in bytes for inspection data.
	TotalMemoryLimit int64 `json:"total_memory_limit"`
}

// ValidSeverities returns all valid severity levels in order of importance.
func ValidSeverities() []Severity {
	return []Severity{Critical, High, Medium, Low, Info}
}

// ValidInspectorModes returns all valid inspector modes.
func ValidInspectorModes() []InspectorMode {
	return []InspectorMode{PipelineInternal, SessionWide}
}

// DefaultMemoryThreshold returns the default memory threshold configuration.
func DefaultMemoryThreshold() MemoryThreshold {
	return MemoryThreshold{
		MaxIssues:         1000,
		MaxFeedbackLoops:  10,
		MaxCorrectionSize: 1024 * 1024,
		TotalMemoryLimit:  100 * 1024 * 1024,
	}
}

type InspectorIntent string

const (
	IntentCheck    InspectorIntent = "check"
	IntentValidate InspectorIntent = "validate"
)

func ValidInspectorIntents() []InspectorIntent {
	return []InspectorIntent{IntentCheck, IntentValidate}
}

type AgentRoutingInfo struct {
	AgentID   string            `json:"agent_id"`
	AgentType string            `json:"agent_type"`
	Intents   []InspectorIntent `json:"intents"`
	Keywords  []string          `json:"keywords"`
	Priority  int               `json:"priority"`
	SessionID string            `json:"session_id"`
}

type InspectorRequest struct {
	ID          string             `json:"id"`
	Intent      InspectorIntent    `json:"intent"`
	TaskID      string             `json:"task_id,omitempty"`
	Files       []string           `json:"files,omitempty"`
	Criteria    *InspectorCriteria `json:"criteria,omitempty"`
	InspectorID string             `json:"inspector_id"`
	SessionID   string             `json:"session_id"`
	Timestamp   time.Time          `json:"timestamp"`
}

type InspectorResponse struct {
	ID        string           `json:"id"`
	RequestID string           `json:"request_id"`
	Success   bool             `json:"success"`
	Result    *InspectorResult `json:"result,omitempty"`
	Error     string           `json:"error,omitempty"`
	Timestamp time.Time        `json:"timestamp"`
}

type InspectorToolConfig struct {
	Name        string   `json:"name"`
	Command     string   `json:"command"`
	CheckOnly   string   `json:"check_only"`
	Languages   []string `json:"languages"`
	ConfigFiles []string `json:"config_files"`
	Severity    string   `json:"severity"`
}

type Finding struct {
	ID           string   `json:"id"`
	Severity     Severity `json:"severity"`
	File         string   `json:"file"`
	Line         int      `json:"line"`
	Column       int      `json:"column"`
	Message      string   `json:"message"`
	RuleID       string   `json:"rule_id,omitempty"`
	SuggestedFix string   `json:"suggested_fix,omitempty"`
}

type ResolvedIssue struct {
	IssueID    string    `json:"issue_id"`
	ResolvedBy string    `json:"resolved_by"`
	ResolvedAt time.Time `json:"resolved_at"`
	Resolution string    `json:"resolution"`
}

type UnresolvedIssue struct {
	IssueID      string   `json:"issue_id"`
	Severity     Severity `json:"severity"`
	Description  string   `json:"description"`
	AttemptCount int      `json:"attempt_count"`
}

type OverriddenIssue struct {
	IssueID      string    `json:"issue_id"`
	OverriddenBy string    `json:"overridden_by"`
	OverriddenAt time.Time `json:"overridden_at"`
	Reason       string    `json:"reason"`
}

type FixReference struct {
	IssueID      string `json:"issue_id"`
	Severity     string `json:"severity"`
	FilePath     string `json:"file_path"`
	LineNumber   int    `json:"line_number"`
	Description  string `json:"description"`
	SuggestedFix string `json:"suggested_fix"`
}

type InspectorCheckpointSummary struct {
	PipelineID        string            `json:"pipeline_id"`
	SessionID         string            `json:"session_id"`
	Timestamp         time.Time         `json:"timestamp"`
	ContextUsage      float64           `json:"context_usage"`
	CheckpointIndex   int               `json:"checkpoint_index"`
	TotalIssuesFound  int               `json:"total_issues_found"`
	ResolvedIssues    []ResolvedIssue   `json:"resolved_issues"`
	UnresolvedIssues  []UnresolvedIssue `json:"unresolved_issues"`
	OverriddenIssues  []OverriddenIssue `json:"overridden_issues"`
	CriticalFixes     []FixReference    `json:"critical_fixes"`
	HighPriorityFixes []FixReference    `json:"high_priority_fixes"`
	LoopsCompleted    int               `json:"loops_completed"`
	AverageLoopTime   time.Duration     `json:"average_loop_time"`
}

type ValidationRun struct {
	RunID        string            `json:"run_id"`
	StartedAt    time.Time         `json:"started_at"`
	CompletedAt  time.Time         `json:"completed_at"`
	FilesChecked []string          `json:"files_checked"`
	IssuesFound  []ValidationIssue `json:"issues_found"`
	Passed       bool              `json:"passed"`
}

type InspectorHandoffState struct {
	PipelineID         string          `json:"pipeline_id"`
	CurrentFindings    []Finding       `json:"current_findings"`
	ResolvedByEngineer []ResolvedIssue `json:"resolved_by_engineer"`
	PendingForEngineer []FixReference  `json:"pending_for_engineer"`
	ValidationHistory  []ValidationRun `json:"validation_history"`
}

type InspectorConfig struct {
	Model               string          `json:"model"`
	Mode                InspectorMode   `json:"mode"`
	CheckpointThreshold float64         `json:"checkpoint_threshold"`
	CompactionThreshold float64         `json:"compaction_threshold"`
	MaxValidationLoops  int             `json:"max_validation_loops"`
	ValidationTimeout   time.Duration   `json:"validation_timeout"`
	MemoryThreshold     MemoryThreshold `json:"memory_threshold"`
	EnabledTools        []string        `json:"enabled_tools"`
}

func DefaultInspectorConfig() InspectorConfig {
	return InspectorConfig{
		Model:               "codex-5.2",
		Mode:                PipelineInternal,
		CheckpointThreshold: 0.85,
		CompactionThreshold: 0.95,
		MaxValidationLoops:  3,
		ValidationTimeout:   5 * time.Second,
		MemoryThreshold:     DefaultMemoryThreshold(),
		EnabledTools:        []string{"run_linter", "run_formatter_check", "run_type_checker"},
	}
}

type InspectorState struct {
	ID             string        `json:"id"`
	SessionID      string        `json:"session_id"`
	Mode           InspectorMode `json:"mode"`
	CurrentTaskID  string        `json:"current_task_id,omitempty"`
	IssuesFound    int           `json:"issues_found"`
	IssuesResolved int           `json:"issues_resolved"`
	LoopsCompleted int           `json:"loops_completed"`
	ContextUsage   float64       `json:"context_usage"`
	StartedAt      time.Time     `json:"started_at"`
	LastActiveAt   time.Time     `json:"last_active_at"`
}
