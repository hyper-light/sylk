// Package shared provides types and utilities shared between the pipeline
// inspector and global inspector agent variants.
package shared

import (
	"fmt"
	"strings"
	"sync"
	"time"
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

// ValidSeverities returns all valid severity levels in descending order of importance.
func ValidSeverities() []Severity {
	return []Severity{Critical, High, Medium, Low, Info}
}

// HasCriticalFailure reports whether any issue in the slice has Critical severity.
func HasCriticalFailure(issues []ValidationIssue) bool {
	for _, issue := range issues {
		if issue.Severity == Critical {
			return true
		}
	}
	return false
}

// HasBlockingIssues reports whether any issue in the slice has Critical or High severity.
func HasBlockingIssues(issues []ValidationIssue) bool {
	for _, issue := range issues {
		if issue.Severity == Critical || issue.Severity == High {
			return true
		}
	}
	return false
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

// InspectorResult represents the final result of an inspection.
type InspectorResult struct {
	// TaskID identifies the task that was validated.
	TaskID string `json:"task_id"`

	// Mode indicates which inspection mode was used.
	Mode string `json:"mode"`

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

// InspectorState tracks the runtime state of an inspector agent instance.
type InspectorState struct {
	// ID uniquely identifies this inspector instance.
	ID string `json:"id"`

	// SessionID identifies the session this inspector belongs to.
	SessionID string `json:"session_id"`

	// Mode indicates the operational mode of this inspector.
	Mode string `json:"mode"`

	// CurrentTaskID identifies the task currently being validated.
	CurrentTaskID string `json:"current_task_id,omitempty"`

	// IssuesFound is the cumulative count of issues found.
	IssuesFound int `json:"issues_found"`

	// IssuesResolved is the cumulative count of issues resolved.
	IssuesResolved int `json:"issues_resolved"`

	// LoopsCompleted is the total number of validation loops executed.
	LoopsCompleted int `json:"loops_completed"`

	// ContextUsage tracks the fraction of context window consumed.
	ContextUsage float64 `json:"context_usage"`

	// StartedAt records when this inspector instance was created.
	StartedAt time.Time `json:"started_at"`

	// LastActiveAt records the most recent activity timestamp.
	LastActiveAt time.Time `json:"last_active_at"`
}

// ---------------------------------------------------------------------------
// Configuration types
// ---------------------------------------------------------------------------

// Named constants for pipeline inspector defaults.
const (
	pipelineDefaultModel            = "claude-opus-4-6"
	pipelineDefaultMaxToolRuns      = 12
	pipelineDefaultMaxTokens        = 16384
	pipelineDefaultTimeout          = 90 * time.Second
	pipelineDefaultMaxFeedbackLoops = 3
)

// PipelineInspectorConfig configures the pipeline inspector variant.
type PipelineInspectorConfig struct {
	// Model is the LLM model identifier used for analysis.
	Model string `json:"model"`

	// MaxToolRuns limits the number of tool invocations per session.
	MaxToolRuns int `json:"max_tool_runs"`

	// MaxTokens limits the response token budget.
	MaxTokens int `json:"max_tokens"`

	// DefaultTimeout is the per-operation timeout.
	DefaultTimeout time.Duration `json:"default_timeout"`

	// AgentID identifies the agent instance.
	AgentID string `json:"agent_id"`

	// SessionID identifies the session this agent belongs to.
	SessionID string `json:"session_id"`

	// MaxFeedbackLoops limits the number of validation feedback iterations.
	MaxFeedbackLoops int `json:"max_feedback_loops"`
}

// DefaultPipelineInspectorConfig returns sensible defaults for the pipeline inspector.
func DefaultPipelineInspectorConfig() PipelineInspectorConfig {
	return PipelineInspectorConfig{
		Model:            pipelineDefaultModel,
		MaxToolRuns:      pipelineDefaultMaxToolRuns,
		MaxTokens:        pipelineDefaultMaxTokens,
		DefaultTimeout:   pipelineDefaultTimeout,
		MaxFeedbackLoops: pipelineDefaultMaxFeedbackLoops,
	}
}

// Named constants for global inspector defaults.
const (
	globalDefaultModel        = "claude-opus-4-6"
	globalDefaultMaxToolRuns  = 16
	globalDefaultMaxTokens    = 16384
	globalDefaultTimeout      = 120 * time.Second
	globalDefaultAuditTimeout = 180 * time.Second
	globalDefaultMaxAge       = 30 * time.Minute
)

// GlobalInspectorConfig configures the global inspector variant.
type GlobalInspectorConfig struct {
	// Model is the LLM model identifier used for analysis.
	Model string `json:"model"`

	// MaxToolRuns limits the number of tool invocations per session.
	MaxToolRuns int `json:"max_tool_runs"`

	// MaxTokens limits the response token budget.
	MaxTokens int `json:"max_tokens"`

	// DefaultTimeout is the per-operation timeout.
	DefaultTimeout time.Duration `json:"default_timeout"`

	// AgentID identifies the agent instance.
	AgentID string `json:"agent_id"`

	// SessionID identifies the session this agent belongs to.
	SessionID string `json:"session_id"`

	// AuditTimeout is the timeout for full audit operations.
	AuditTimeout time.Duration `json:"audit_timeout"`

	// MaxAge is the maximum age of cached audit results before they are stale.
	MaxAge time.Duration `json:"max_age"`
}

// DefaultGlobalInspectorConfig returns sensible defaults for the global inspector.
func DefaultGlobalInspectorConfig() GlobalInspectorConfig {
	return GlobalInspectorConfig{
		Model:          globalDefaultModel,
		MaxToolRuns:    globalDefaultMaxToolRuns,
		MaxTokens:      globalDefaultMaxTokens,
		DefaultTimeout: globalDefaultTimeout,
		AuditTimeout:   globalDefaultAuditTimeout,
		MaxAge:         globalDefaultMaxAge,
	}
}

// ---------------------------------------------------------------------------
// Audit types
// ---------------------------------------------------------------------------

// AuditResult represents the outcome of a global inspector audit on a DAG layer.
type AuditResult struct {
	// DAGID identifies the execution DAG this audit belongs to.
	DAGID string `json:"dag_id"`

	// LayerIdx is the zero-based index of the DAG layer that was audited.
	LayerIdx int `json:"layer_idx"`

	// PlanID references the plan that drove execution.
	PlanID string `json:"plan_id"`

	// Passed indicates whether the layer audit passed overall.
	Passed bool `json:"passed"`

	// Issues lists all validation issues found during the audit.
	Issues []ValidationIssue `json:"issues"`

	// CriticalCount is the number of Critical severity issues.
	CriticalCount int `json:"critical_count"`

	// HighCount is the number of High severity issues.
	HighCount int `json:"high_count"`

	// CrossFileIssues lists issues spanning multiple files.
	CrossFileIssues []CrossFileIssue `json:"cross_file_issues"`

	// PlanAdherence scores how well the implementation follows the plan.
	PlanAdherence PlanAdherenceScore `json:"plan_adherence"`

	// Recommendations lists actionable follow-up suggestions.
	Recommendations []Recommendation `json:"recommendations"`

	// Duration is the wall-clock time spent on this audit.
	Duration time.Duration `json:"duration"`

	// StartedAt records when the audit began.
	StartedAt time.Time `json:"started_at"`

	// CompletedAt records when the audit finished.
	CompletedAt time.Time `json:"completed_at"`
}

// HasBlockingIssues reports whether this audit result contains any blocking
// issues (Critical or High severity).
func (r *AuditResult) HasBlockingIssues() bool {
	return r.CriticalCount > 0 || r.HighCount > 0
}

// CrossFileCategory classifies the type of cross-file issue detected.
type CrossFileCategory string

const (
	// CrossFileInterfaceMismatch indicates incompatible interface implementations
	// across file boundaries.
	CrossFileInterfaceMismatch CrossFileCategory = "interface_mismatch"

	// CrossFileImportCycle indicates a circular import dependency.
	CrossFileImportCycle CrossFileCategory = "import_cycle"

	// CrossFileTypeInconsistency indicates inconsistent type usage across files.
	CrossFileTypeInconsistency CrossFileCategory = "type_inconsistency"

	// CrossFileSharedStateRace indicates a potential data race on shared state
	// accessed from multiple files.
	CrossFileSharedStateRace CrossFileCategory = "shared_state_race"
)

// CrossFileIssue describes a validation issue that spans multiple files.
type CrossFileIssue struct {
	// Files lists the file paths involved in this cross-file issue.
	Files []string `json:"files"`

	// Description explains the cross-file problem.
	Description string `json:"description"`

	// Severity indicates the importance of this cross-file issue.
	Severity Severity `json:"severity"`

	// Category classifies the type of cross-file issue.
	Category CrossFileCategory `json:"category"`
}

// PlanAdherenceScore measures how faithfully the implementation follows the plan.
type PlanAdherenceScore struct {
	// Score is a value between 0.0 and 1.0 indicating overall plan adherence.
	Score float64 `json:"score"`

	// TasksCovered lists task IDs that were implemented as planned.
	TasksCovered []string `json:"tasks_covered"`

	// TasksMissing lists task IDs that were planned but not implemented.
	TasksMissing []string `json:"tasks_missing"`

	// Deviations describes how the implementation deviates from the plan.
	Deviations []string `json:"deviations"`
}

// RecommendationType classifies the kind of follow-up action recommended.
type RecommendationType string

const (
	// RecommendArchitectResearch suggests the architect should research a topic
	// before proceeding.
	RecommendArchitectResearch RecommendationType = "architect_research"

	// RecommendUserClarification suggests requesting clarification from the user.
	RecommendUserClarification RecommendationType = "user_clarification"

	// RecommendEngineerRework suggests the engineer should rework part of the
	// implementation.
	RecommendEngineerRework RecommendationType = "engineer_rework"

	// RecommendPlanRevision suggests the plan itself needs revision.
	RecommendPlanRevision RecommendationType = "plan_revision"
)

// Recommendation describes an actionable follow-up suggestion from the audit.
type Recommendation struct {
	// Type classifies the kind of recommendation.
	Type RecommendationType `json:"type"`

	// Description explains what should be done.
	Description string `json:"description"`

	// Priority indicates the urgency of this recommendation.
	Priority Severity `json:"priority"`

	// TargetAgent identifies the agent that should act on this recommendation.
	TargetAgent string `json:"target_agent"`
}

// ---------------------------------------------------------------------------
// Layer audit request/response types
// ---------------------------------------------------------------------------

// LayerAuditRequest is submitted to the global inspector to audit a completed
// DAG layer.
type LayerAuditRequest struct {
	// DAGID identifies the execution DAG.
	DAGID string `json:"dag_id"`

	// LayerIdx is the zero-based index of the completed layer.
	LayerIdx int `json:"layer_idx"`

	// PlanSnapshot is a textual snapshot of the plan at audit time.
	PlanSnapshot string `json:"plan_snapshot"`

	// NodeDiffs maps node IDs to their diff snapshots.
	NodeDiffs map[string]*NodeDiffSnapshot `json:"node_diffs"`

	// NodeResults maps node IDs to their execution results.
	NodeResults map[string]*NodeResultInfo `json:"node_results"`
}

// NodeResultInfo captures the execution result of a single DAG node.
type NodeResultInfo struct {
	// NodeID identifies the DAG node.
	NodeID string `json:"node_id"`

	// Success indicates whether the node completed successfully.
	Success bool `json:"success"`

	// Error contains the error message if Success is false.
	Error string `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// Audit diff capture types
// ---------------------------------------------------------------------------

// AuditKey uniquely identifies a layer audit by DAG ID and layer index.
type AuditKey struct {
	DAGID    string
	LayerIdx int
}

// LayerAuditDiffs holds the collected file diffs for a single DAG layer audit.
type LayerAuditDiffs struct {
	// Key identifies the DAG and layer this diff set belongs to.
	Key AuditKey `json:"key"`

	// NodeDiffs maps node IDs to their diff snapshots.
	NodeDiffs map[string]*NodeDiffSnapshot `json:"node_diffs"`

	// CapturedAt records when the diffs were captured.
	CapturedAt time.Time `json:"captured_at"`
}

// NodeDiffSnapshot captures the file modifications made by a single DAG node.
type NodeDiffSnapshot struct {
	// NodeID identifies the DAG node.
	NodeID string `json:"node_id"`

	// ModifiedFiles lists the file diffs produced by this node.
	ModifiedFiles []AuditFileDiff `json:"modified_files"`
}

// AuditFileDiff captures the before and after content of a modified file,
// with lazy diff computation via sync.Once.
type AuditFileDiff struct {
	// Path is the file path relative to the repository root.
	Path string `json:"path"`

	// Before is the file content before modification.
	Before []byte `json:"-"`

	// After is the file content after modification.
	After []byte `json:"-"`

	diff     string
	diffOnce sync.Once
}

// Diff returns a unified-like diff string comparing Before and After content.
// The result is computed lazily on first call and cached for subsequent calls.
func (d *AuditFileDiff) Diff() string {
	d.diffOnce.Do(func() {
		d.diff = computeLineDiff(d.Path, d.Before, d.After)
	})
	return d.diff
}

// computeLineDiff produces a unified-like diff between two byte slices,
// comparing line by line.
func computeLineDiff(path string, before, after []byte) string {
	beforeLines := splitLines(string(before))
	afterLines := splitLines(string(after))

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("--- a/%s\n", path))
	buf.WriteString(fmt.Sprintf("+++ b/%s\n", path))

	// Use a simple longest-common-subsequence approach to produce hunks.
	lcs := longestCommonSubsequence(beforeLines, afterLines)

	bi, ai, li := 0, 0, 0
	for bi < len(beforeLines) || ai < len(afterLines) {
		if li < len(lcs) && bi < len(beforeLines) && ai < len(afterLines) &&
			beforeLines[bi] == lcs[li] && afterLines[ai] == lcs[li] {
			buf.WriteString(fmt.Sprintf(" %s\n", beforeLines[bi]))
			bi++
			ai++
			li++
			continue
		}

		// Emit removals from before until we reach the next LCS line.
		for bi < len(beforeLines) && (li >= len(lcs) || beforeLines[bi] != lcs[li]) {
			buf.WriteString(fmt.Sprintf("-%s\n", beforeLines[bi]))
			bi++
		}

		// Emit additions from after until we reach the next LCS line.
		for ai < len(afterLines) && (li >= len(lcs) || afterLines[ai] != lcs[li]) {
			buf.WriteString(fmt.Sprintf("+%s\n", afterLines[ai]))
			ai++
		}
	}

	return buf.String()
}

// splitLines splits s into lines, preserving empty trailing lines only if
// the input ends with a newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	// Remove trailing empty string from Split if input ended with newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// longestCommonSubsequence computes the LCS of two string slices using
// dynamic programming.
func longestCommonSubsequence(a, b []string) []string {
	m, n := len(a), len(b)
	if m == 0 || n == 0 {
		return nil
	}

	// Build the DP table.
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	// Backtrack to recover the LCS.
	result := make([]string, 0, dp[m][n])
	i, j := m, n
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			result = append(result, a[i-1])
			i--
			j--
		} else if dp[i-1][j] >= dp[i][j-1] {
			i--
		} else {
			j--
		}
	}

	// Reverse the result since we built it backwards.
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}

	return result
}

// ---------------------------------------------------------------------------
// Quality grading
// ---------------------------------------------------------------------------

// Quality grade weight constants. These sum to 1.0.
const (
	weightCorrectness = 0.30
	weightRobustness  = 0.20
	weightPerformance = 0.15
	weightSecurity    = 0.20
	weightAdherence   = 0.15
)

// QualityGrade captures per-dimension quality scores for an audit.
type QualityGrade struct {
	// Correctness measures functional correctness (0.0 to 1.0).
	Correctness float64 `json:"correctness"`

	// Robustness measures error handling and edge case coverage (0.0 to 1.0).
	Robustness float64 `json:"robustness"`

	// Performance measures efficiency and resource usage (0.0 to 1.0).
	Performance float64 `json:"performance"`

	// Security measures protection against vulnerabilities (0.0 to 1.0).
	Security float64 `json:"security"`

	// Adherence measures conformance to the plan and coding standards (0.0 to 1.0).
	Adherence float64 `json:"adherence"`
}

// Overall returns the weighted average quality score.
// Weights: correctness=0.30, robustness=0.20, performance=0.15, security=0.20, adherence=0.15.
func (g QualityGrade) Overall() float64 {
	return g.Correctness*weightCorrectness +
		g.Robustness*weightRobustness +
		g.Performance*weightPerformance +
		g.Security*weightSecurity +
		g.Adherence*weightAdherence
}
