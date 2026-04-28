// Plan preflight attestation — Guardian-issued verdict for a whole
// plan, batched. Travels with PlanHandoff so the orchestrator can
// verify Guardian's gate decision at ingest without re-running the
// classifier per-task post-approval.
//
// Defined in agents/shared (rather than agents/guardian) so architect
// and orchestrator can consume the type without import cycles.
// Guardian remains the canonical issuer of the verdict — only the
// type definition lives here.
package shared

import "time"

// PlanPreflightAttestation is the Guardian-issued verdict for a
// whole-plan preflight evaluation. The orchestrator verifies fields
// at ingest:
//
//   - PlanContentHash must match the orchestrator's recompute over
//     the received PlanHandoff (proves the verdict was issued for
//     this exact plan revision).
//   - ClassifierVersion identifies the rule set the verdict was
//     produced under; mismatches signal a stale verdict that the
//     architect must re-issue.
//   - PerTask must cover every task in the handoff (proves the
//     verdict isn't partial).
//
// Authority: Guardian remains the gate. The orchestrator does not
// re-derive risk classification — it only verifies that Guardian
// issued a current verdict for this plan.
type PlanPreflightAttestation struct {
	PlanID              string                               `json:"plan_id"`
	PlanRevision        int                                  `json:"plan_revision"`
	PlanContentHash     string                               `json:"plan_content_hash"`
	ClassifierVersion   string                               `json:"classifier_version"`
	IssuedAt            time.Time                            `json:"issued_at"`
	IssuerAgentID       string                               `json:"issuer_agent_id"`
	PerTask             map[string]*PlanPreflightTaskVerdict `json:"per_task"`
	HasBlockingFindings bool                                 `json:"has_blocking_findings"`
	HasWarnings         bool                                 `json:"has_warnings"`
	RequiresApproval    bool                                 `json:"requires_approval"`
}

// PlanPreflightTaskVerdict is one task's verdict within an
// attestation. Same shape as Guardian's per-task classifier output.
type PlanPreflightTaskVerdict struct {
	TaskID           string   `json:"task_id"`
	AgentType        string   `json:"agent_type,omitempty"`
	Clean            bool     `json:"clean"`
	RequiresApproval bool     `json:"requires_approval"`
	Warnings         []string `json:"warnings,omitempty"`
	BlockingFindings []string `json:"blocking_findings,omitempty"`
	UserMessage      string   `json:"user_message,omitempty"`
}
