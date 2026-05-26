// Package planapproval is the canonical data model for the plan acceptance
// dialog. The dialog replaces free-form-text classification for plan
// approval — when an architect-drafted plan reaches Ready, the architect
// publishes a Proposal carrying the plan text plus context; the UI renders
// an interactive Approve / Modify / Reject dialog; the user's click
// produces a Decision that routes back through Guardian and then to the
// architect's verdict handler.
//
// No inline modifications or rejection-reason text travels in the
// Decision payload. Modify and Reject verdicts trigger an architect
// follow-up turn ("what would you like changed?" / "what would you like
// to do instead?") whose answer is a normal user message — keeps the
// dialog single-purpose and gives the architect a turn boundary to think
// fresh on the user's response.
package planapproval

import (
	"time"
)

// Verdict is the user's decision on a presented plan. Exactly one of
// these three values rides on every Decision payload.
type Verdict string

const (
	// VerdictApprove launches the plan for execution. Architect's
	// actOnAccept path runs after Guardian approves the decision.
	VerdictApprove Verdict = "approve"

	// VerdictModify triggers an architect follow-up asking the user
	// what should be changed. The user's answer arrives as a normal
	// message in the next turn; architect revises the plan and
	// re-publishes a fresh Proposal.
	VerdictModify Verdict = "modify"

	// VerdictReject triggers an architect follow-up asking the user
	// what they would like to do instead. The user's answer becomes
	// the input to fresh planning (or a different agent entirely if
	// the user changes direction).
	VerdictReject Verdict = "reject"
)

// IsValid reports whether v is one of the three known verdicts.
func (v Verdict) IsValid() bool {
	switch v {
	case VerdictApprove, VerdictModify, VerdictReject:
		return true
	default:
		return false
	}
}

// Proposal is the architect-published payload that triggers the UI's
// plan acceptance dialog. The dialog renders the plan text, the
// freshness summary (when present — Phase 2 attaches drift signals and
// orchestrator state context), and three buttons.
//
// Identity:
//   - PlanID is the canonical plan identifier the decision routes back
//     to. Required.
//   - CorrelationID pairs the proposal with the decision so multiple
//     pending proposals don't collide. Required.
//   - SessionID scopes the proposal to a session for routing.
//
// Content:
//   - PlanName is the human-readable name shown in the dialog header.
//   - PlanSummary is a short one-paragraph synopsis (architect-derived).
//   - PlanArtifactID / PlanArtifactReplaceKey point at the canonical
//     user-reviewable claims artifact when available.
//   - PlanText is the full plan markdown fallback during migration.
//
// Context (Phase 2 fields, optional in Phase 1):
//   - FreshnessSummary describes what was re-checked when the plan was
//     re-presented (e.g. on resume after restart). Empty for fresh plans.
//   - DriftSignals enumerates any drift detected since the plan was
//     drafted (codebase changes, knowledge advisories, etc.) so the user
//     decides with full context.
//   - OrchestratorStateHint indicates whether the plan is already in
//     flight ("running") or stalled ("aborted", "none") when relevant.
type Proposal struct {
	PlanID        string `json:"plan_id"`
	CorrelationID string `json:"correlation_id"`
	SessionID     string `json:"session_id,omitempty"`

	PlanName    string `json:"plan_name,omitempty"`
	PlanSummary string `json:"plan_summary,omitempty"`
	PlanText    string `json:"plan_text"`

	PlanArtifactID         string `json:"plan_artifact_id,omitempty"`
	PlanArtifactReplaceKey string `json:"plan_artifact_replace_key,omitempty"`

	FreshnessSummary      string         `json:"freshness_summary,omitempty"`
	DriftSignals          []DriftSignal  `json:"drift_signals,omitempty"`
	OrchestratorStateHint string         `json:"orchestrator_state_hint,omitempty"`
	Metadata              map[string]any `json:"metadata,omitempty"`

	Timestamp time.Time `json:"timestamp"`
}

// DriftSignal is a single observed drift between when the plan was
// drafted and when it's being re-presented. Visible to the user in the
// dialog so the click is informed. Empty for fresh plans.
type DriftSignal struct {
	Kind        string `json:"kind"`        // "codebase" | "knowledge" | "consultation_age" | "pipeline" | ...
	Description string `json:"description"` // human-readable, displayed in dialog
	Severity    string `json:"severity"`    // "info" | "advisory" | "warning"
	SourcePath  string `json:"source_path,omitempty"`
}

// Decision is the user's clicked verdict on a Proposal. Routes back
// through Guardian (same pattern as tool_execution_control) and then to
// the architect's verdict handler, which dispatches to actOnAccept,
// actOnModify, or actOnReject based on the Verdict field.
//
// Notably absent from this struct: any free-form modification text or
// rejection reason. Those are handled by an architect follow-up turn
// after the verdict is processed, not inline in the dialog response.
type Decision struct {
	PlanID        string    `json:"plan_id"`
	CorrelationID string    `json:"correlation_id"`
	Verdict       Verdict   `json:"verdict"`
	Timestamp     time.Time `json:"timestamp"`
}
