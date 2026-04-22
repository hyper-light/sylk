package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// BoardProvider returns the active ClaimsBoard for the current context.
type BoardProvider func() *ClaimsBoard

// ── Shared skills (all agents) ──────────────────────────────────────

func QueryClaimsBoardSkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("query_claims_board").
		Description("Read the current claims board state: all claims, testaments, validations, artifacts, phase, iteration, and computed summaries.").
		Domain("claims").
		Keywords("claims", "board", "status", "progress").
		Priority(98).
		Handler(func(ctx context.Context, _ json.RawMessage) (any, error) {
			board := bp()
			if board == nil {
				return nil, fmt.Errorf("claims board not available")
			}
			return board.Projection(), nil
		}).
		Build()
}

func PostActionSkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("post_action").
		Description("Issue an action (set of claims) against one or more subjects. Each claim carries its validations. Covers task, challenge, consultation, corrective, and archival actions.").
		Domain("claims").
		Keywords("action", "claim", "issue", "challenge", "consult").
		Priority(97).
		StringParam("action_type", "Type: task, challenge, consultation, corrective, archival, prompt", true).
		StringParam("claims_json", "JSON array of claims with title, description, subject, scope, and validations", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			board := bp()
			if board == nil {
				return nil, fmt.Errorf("claims board not available")
			}
			var params struct {
				ActionType string          `json:"action_type"`
				ClaimsJSON json.RawMessage `json:"claims_json"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			var claimInputs []claimInput
			if err := json.Unmarshal(params.ClaimsJSON, &claimInputs); err != nil {
				return nil, fmt.Errorf("invalid claims_json: %w", err)
			}

			var claims []Claim
			for _, ci := range claimInputs {
				c := Claim{
					Title:       ci.Title,
					Description: ci.Description,
					Scope:       ci.Scope,
					Relations: []Relation{
						{Related: ci.Subject, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
					},
				}
				// Validations live ON the claim.
				for _, vi := range ci.Validations {
					c.Validations = append(c.Validations, &Validation{
						Description: vi.Description,
						QualityBar:  vi.QualityBar,
						Type:        ValidationType(vi.Type),
						Required:    true,
					})
				}
				claims = append(claims, c)
			}

			action := Action{Type: ActionType(strings.TrimSpace(params.ActionType))}
			return nil, board.PostAction(ctx, action, claims)
		}).
		Build()
}

func InspectClaimConflictsSkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("inspect_claim_conflicts").
		Description("Surface overlapping claims, competing testaments, and mutual dependency blocks.").
		Domain("claims").
		Keywords("conflicts", "overlap", "competing", "blocked").
		Priority(94).
		Handler(func(ctx context.Context, _ json.RawMessage) (any, error) {
			board := bp()
			if board == nil {
				return nil, fmt.Errorf("claims board not available")
			}
			p := board.Projection()
			var conflicts []map[string]any

			for i := range p.Claims {
				for j := i + 1; j < len(p.Claims); j++ {
					ci, cj := &p.Claims[i], &p.Claims[j]
					if ci.Status.IsTerminal() || cj.Status.IsTerminal() {
						continue
					}
					if ScopeOverlaps(ci.Scope, cj.Scope) {
						conflicts = append(conflicts, map[string]any{
							"type": "scope_overlap", "claim_a": ci.ID, "title_a": ci.Title,
							"claim_b": cj.ID, "title_b": cj.Title,
						})
					}
				}
			}

			return map[string]any{"conflicts": conflicts, "count": len(conflicts)}, nil
		}).
		Build()
}

// ── Subject skills (implementers) ───────────────────────────────────

func SubmitTestamentsSkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("submit_testaments").
		Description("Submit testaments with artifacts proving claims are satisfied or recording failures. Each testament references a claim and carries artifacts as proof. When work FAILS, submit a testament with kind='error' artifacts containing the error details — never silently drop errors. A testament with error artifacts is not a system failure; it is a structured report the issuer evaluates.").
		Domain("claims").
		Keywords("testament", "submit", "proof", "artifacts", "done", "error").
		Priority(96).
		StringParam("testaments_json", "JSON array of testaments. Each has claim_id, summary, confidence, and artifacts. Artifact kinds include 'code_reference', 'test_output', 'diff', 'error' (for failures), 'error_trace' (stack traces), 'error_diagnostic' (environmental failures).", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			board := bp()
			if board == nil {
				return nil, fmt.Errorf("claims board not available")
			}
			var params struct {
				TestamentsJSON json.RawMessage `json:"testaments_json"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			var testamentInputs []testamentInput
			if err := json.Unmarshal(params.TestamentsJSON, &testamentInputs); err != nil {
				return nil, fmt.Errorf("invalid testaments_json: %w", err)
			}

			var testaments []Testament
			for _, ti := range testamentInputs {
				t := Testament{
					Summary:    ti.Summary,
					Confidence: ti.Confidence,
					Relations: []Relation{
						{Related: ti.ClaimID, RelatedType: RelatedTypeClaim, Relationship: RelationshipClaim},
					},
				}
				// Artifacts live ON the testament.
				for _, ai := range ti.Artifacts {
					t.Artifacts = append(t.Artifacts, &Artifact{
						Kind:      ai.Kind,
						Reference: ai.Reference,
						Ephemeral: ai.Ephemeral,
						Metadata:  ai.Metadata,
					})
				}
				testaments = append(testaments, t)
			}

			return nil, board.SubmitTestaments(ctx, Action{Type: ActionTypeTask}, testaments)
		}).
		Build()
}

func UpdateClaimProgressSkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("update_claim_progress").
		Description("Record incremental progress on a claim. If work encounters an error, report it here — then submit a testament with error artifacts when done.").
		Domain("claims").
		Keywords("progress", "update", "evidence", "work").
		Priority(95).
		StringParam("claim_id", "ID of the claim to update", true).
		StringParam("work_summary", "Description of work done", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			board := bp()
			if board == nil {
				return nil, fmt.Errorf("claims board not available")
			}
			var params struct {
				ClaimID     string `json:"claim_id"`
				WorkSummary string `json:"work_summary"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return nil, board.UpdateClaimProgress(ctx, params.ClaimID, ClaimProgressUpdate{WorkSummary: params.WorkSummary}, "")
		}).
		Build()
}

// ── Issuer skills (validators) ──────────────────────────────────────

func EvaluateValidationSkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("evaluate_validation").
		Description("Evaluate a testament's artifacts against a claim validation's quality bar. Records pass/fail with reason. When the testament contains error artifacts, evaluate whether the error is recoverable — if so, post remediation claims; if not, fail the validation with the error details.").
		Domain("claims").
		Keywords("validate", "evaluate", "quality", "bar", "pass", "fail").
		Priority(100).
		StringParam("claim_id", "ID of the claim owning the validation", true).
		StringParam("validation_id", "ID of the validation to evaluate", true).
		StringParam("status", "Verdict: passed, failed, or skipped", true).
		StringParam("reason", "Explanation", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			board := bp()
			if board == nil {
				return nil, fmt.Errorf("claims board not available")
			}
			var params struct {
				ClaimID      string `json:"claim_id"`
				ValidationID string `json:"validation_id"`
				Status       string `json:"status"`
				Reason       string `json:"reason"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			status := ValidationStatus(strings.TrimSpace(params.Status))
			if status != ValidationStatusPassed && status != ValidationStatusFailed && status != ValidationStatusSkipped {
				return nil, fmt.Errorf("invalid status %q", params.Status)
			}
			return nil, board.EvaluateValidation(ctx, params.ClaimID, params.ValidationID, StatusChange{
				To: string(status), Reason: strings.TrimSpace(params.Reason),
			})
		}).
		Build()
}

func PostRemediationClaimsSkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("post_remediation_claims").
		Description("Reject a claim and post replacement claims. Each replacement carries its own validations.").
		Domain("claims").
		Keywords("remediation", "reject", "corrective", "replacement").
		Priority(99).
		StringParam("claim_id", "ID of the claim to reject", true).
		StringParam("reason", "Why the claim is being rejected", true).
		StringParam("replacements_json", "JSON array of replacement claims", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			board := bp()
			if board == nil {
				return nil, fmt.Errorf("claims board not available")
			}
			var params struct {
				ClaimID          string          `json:"claim_id"`
				Reason           string          `json:"reason"`
				ReplacementsJSON json.RawMessage `json:"replacements_json"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			var claimInputs []claimInput
			if err := json.Unmarshal(params.ReplacementsJSON, &claimInputs); err != nil {
				return nil, fmt.Errorf("invalid replacements_json: %w", err)
			}

			var claims []Claim
			for _, ci := range claimInputs {
				c := Claim{
					Title:       ci.Title,
					Description: ci.Description,
					Scope:       ci.Scope,
					Relations: []Relation{
						{Related: ci.Subject, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
					},
				}
				for _, vi := range ci.Validations {
					c.Validations = append(c.Validations, &Validation{
						Description: vi.Description,
						QualityBar:  vi.QualityBar,
						Type:        ValidationType(vi.Type),
						Required:    true,
					})
				}
				claims = append(claims, c)
			}

			return nil, board.RejectClaim(ctx, params.ClaimID,
				StatusChange{Reason: strings.TrimSpace(params.Reason)},
				&Action{Type: ActionTypeCorrective}, claims)
		}).
		Build()
}

// ── Input types ─────────────────────────────────────────────────────

type claimInput struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Subject     string            `json:"subject"`
	Scope       []ClaimScopeEntry `json:"scope,omitempty"`
	Validations []validationInput `json:"validations,omitempty"`
}

type validationInput struct {
	Description string `json:"description"`
	QualityBar  string `json:"quality_bar"`
	Type        string `json:"type"`
}

type testamentInput struct {
	ClaimID    string          `json:"claim_id"`
	Summary    string          `json:"summary"`
	Confidence string          `json:"confidence,omitempty"`
	Artifacts  []artifactInput `json:"artifacts,omitempty"`
}

type artifactInput struct {
	Kind      string         `json:"kind"`
	Reference string         `json:"reference"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Ephemeral bool           `json:"ephemeral,omitempty"`
}
