package guardian

import (
	"time"
)

// =============================================================================
// Intent Classification
// =============================================================================

// GuardianIntent classifies the purpose of a user request to Guardian.
type GuardianIntent string

const (
	IntentConverse   GuardianIntent = "converse"   // General safety conversation
	IntentReport     GuardianIntent = "report"     // Status/health report
	IntentAssess     GuardianIntent = "assess"     // Security assessment
	IntentCheckpoint GuardianIntent = "checkpoint" // Trigger safety commit
)

func (i GuardianIntent) String() string { return string(i) }

// =============================================================================
// Severity + Confidence
// =============================================================================

// Severity classifies the urgency of a finding.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityLow
	SeverityMedium
	SeverityHigh
	SeverityCritical
)

func (s Severity) String() string {
	names := [...]string{"info", "low", "medium", "high", "critical"}
	if int(s) < len(names) {
		return names[s]
	}
	return "unknown"
}

// =============================================================================
// Findings
// =============================================================================

// Finding represents a single security or health finding from Guardian.
type Finding struct {
	ID         string         `json:"id"`
	Type       FindingType    `json:"type"`
	Severity   Severity       `json:"severity"`
	Confidence float64        `json:"confidence"` // 0.0–1.0
	Title      string         `json:"title"`
	Detail     string         `json:"detail"`
	FilePath   string         `json:"file_path,omitempty"`
	Line       int            `json:"line,omitempty"`
	Pattern    string         `json:"pattern,omitempty"`
	AgentID    string         `json:"agent_id,omitempty"`
	Timestamp  time.Time      `json:"timestamp"`
	Data       map[string]any `json:"data,omitempty"`
}

// FindingType categorises findings.
type FindingType string

const (
	FindingCredentialLeak  FindingType = "credential_leak"
	FindingInjection       FindingType = "injection"
	FindingSuspiciousDiff  FindingType = "suspicious_diff"
	FindingBranchViolation FindingType = "branch_violation"
	FindingHealthAnomaly   FindingType = "health_anomaly"
	FindingBudgetWarning   FindingType = "budget_warning"
	FindingBudgetExceeded  FindingType = "budget_exceeded"
	FindingAgentTimeout    FindingType = "agent_timeout"
	FindingLargeDeletion   FindingType = "large_deletion"
	FindingSensitiveFile   FindingType = "sensitive_file"
	FindingBinaryBlob      FindingType = "binary_blob"
	FindingPermissionChange FindingType = "permission_change"
)

// =============================================================================
// Git Mutation Proposals
// =============================================================================

// GitMutationProposal encapsulates a mutating git operation awaiting user approval.
type GitMutationProposal struct {
	ID            string         `json:"id"`
	CorrelationID string         `json:"correlation_id"`
	Op            string         `json:"op"`
	TargetBranch  string         `json:"target_branch,omitempty"`
	Params        map[string]any `json:"params,omitempty"`
	Reason        string         `json:"reason"`
	RiskLevel     Severity       `json:"risk_level"`
	Timestamp     time.Time      `json:"timestamp"`
}

// ApprovalResult carries the outcome of a user approval request.
type ApprovalResult struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
}

// =============================================================================
// Checkpoint
// =============================================================================

// CheckpointRecord describes a safety checkpoint commit.
type CheckpointRecord struct {
	Seq       int       `json:"seq"`
	CommitRef string    `json:"commit_ref"`
	Message   string    `json:"message"`
	FileCount int       `json:"file_count"`
	Timestamp time.Time `json:"timestamp"`
}

// =============================================================================
// Agent Health Snapshots
// =============================================================================

// AgentHealthSnapshot is a point-in-time health reading for a single agent.
type AgentHealthSnapshot struct {
	AgentID        string        `json:"agent_id"`
	AgentType      string        `json:"agent_type"`
	Responsive     bool          `json:"responsive"`
	CircuitOpen    bool          `json:"circuit_open"`
	RestartCount   int           `json:"restart_count"`
	LastHeartbeat  time.Time     `json:"last_heartbeat"`
	AvgResponseMs  float64       `json:"avg_response_ms"`
	ErrorCount     int           `json:"error_count"`
	LastError      string        `json:"last_error,omitempty"`
	TokensUsed     int64         `json:"tokens_used"`
	CostCentsUsed  int64         `json:"cost_cents_used"`
	Uptime         time.Duration `json:"uptime"`
}

// BudgetStatus summarises token/cost budget consumption.
type BudgetStatus struct {
	TokensUsed    int64   `json:"tokens_used"`
	TokenBudget   int64   `json:"token_budget"` // 0 = unlimited
	TokenPercent  float64 `json:"token_percent"`
	CostCentsUsed int64   `json:"cost_cents_used"`
	CostBudget    int64   `json:"cost_budget"` // 0 = unlimited
	CostPercent   float64 `json:"cost_percent"`
	Warning       bool    `json:"warning"`
	Exceeded      bool    `json:"exceeded"`
}

// =============================================================================
// Diff Review
// =============================================================================

// DiffReviewResult contains the composite outcome of a diff review.
type DiffReviewResult struct {
	Clean    bool      `json:"clean"`
	Findings []Finding `json:"findings,omitempty"`
	Stats    DiffStats `json:"stats"`
}

// DiffStats holds quantitative metrics about a diff.
type DiffStats struct {
	FilesChanged   int `json:"files_changed"`
	LinesAdded     int `json:"lines_added"`
	LinesDeleted   int `json:"lines_deleted"`
	BinaryFiles    int `json:"binary_files"`
	SensitiveFiles int `json:"sensitive_files"`
}

// =============================================================================
// Conversation
// =============================================================================

// ConversationResult holds the response from a Guardian conversation.
type ConversationResult struct {
	Response string         `json:"response"`
	Intent   GuardianIntent `json:"intent"`
}

// ResponseText implements the guide-layer text extraction interface.
func (r *ConversationResult) ResponseText() string {
	if r == nil {
		return ""
	}
	return r.Response
}

// =============================================================================
// Escalation
// =============================================================================

// shouldEscalate determines whether a finding warrants LLM escalation.
// Clear-cut cases (high confidence deterministic matches) skip the LLM.
func shouldEscalate(f *Finding) bool {
	if f == nil {
		return false
	}
	// Deterministic matches with high confidence never need LLM.
	if f.Confidence >= 0.9 {
		return false
	}
	// Low-confidence high-severity findings benefit from LLM analysis.
	if f.Severity >= SeverityHigh && f.Confidence < 0.7 {
		return true
	}
	// Medium-severity with very low confidence.
	if f.Severity >= SeverityMedium && f.Confidence < 0.4 {
		return true
	}
	return false
}
