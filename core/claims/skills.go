package claims

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// BoardProvider returns the active ClaimsBoard for the current context.
// Returns an error with a diagnostic message when the board is unavailable
// (e.g., no active session). The error propagates to the LLM as a tool
// failure so the agent and user see the specific reason.
type BoardProvider func() (*ClaimsBoard, error)

// InboxProvider returns the active ClaimsInbox for the current agent.
// Used by PostActionSkill to auto-register expectations after commit.
type InboxProvider func() *ClaimsInbox

// ── Shared skills (all agents) ──────────────────────────────────────

// QueryClaimsBoardSkill is DEPRECATED — use QueryBoardSkill instead.
// Kept for backward compatibility; returns full projection (expensive).
func QueryClaimsBoardSkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("query_claims_board").
		Description("[DEPRECATED: use query_board instead] Read the full claims board state.").
		Domain("claims").
		Keywords("claims", "board", "status", "progress").
		Priority(50). // deprioritized so agents prefer query_board
		Handler(func(ctx context.Context, _ json.RawMessage) (any, error) {
			board, err := bp()
			if err != nil {
				return nil, fmt.Errorf("claims board: %w", err)
			}
			if board == nil {
				return nil, fmt.Errorf("claims board not available (no error returned)")
			}
			return board.Projection(), nil
		}).
		Build()
}

// QueryBoardSkill creates the composable query_board LLM-callable skill.
// Each op returns ONLY the requested slice — no full board copy.
func QueryBoardSkill(bp BoardProvider, defaultAgentID string) *skills.Skill {
	return skills.NewSkill("query_board").
		Description("Query the claims board. Use op to select what you need.\n\n"+
			"Ops:\n"+
			"- summary: Board status counts (phase, accepted/total, pending, etc.) — fastest\n"+
			"- my_claims: Claims where you are the subject (your directed work)\n"+
			"- my_evaluations: Testified claims where you are issuer/evaluator (awaiting your validation)\n"+
			"- claim: Single claim by ID with its validations\n"+
			"- testament: Single testament by ID with its artifacts\n"+
			"- scope: Claims overlapping a scope entry (kind + key)\n"+
			"- pending_validations: Pending validations on a specific claim\n"+
			"- testaments: Testaments linked to a specific claim").
		Domain("claims").
		Keywords("claims", "board", "query", "status", "my_claims", "scope", "validations").
		Priority(98).
		EnumParam("op", "Query operation", []string{"summary", "my_claims", "my_evaluations", "claim", "testament", "scope", "pending_validations", "testaments"}, true).
		StringParam("claim_id", "Claim ID (for claim, pending_validations, testaments ops)", false).
		StringParam("testament_id", "Testament ID (for testament op)", false).
		StringParam("agent_id", "Agent ID override (for my_claims, my_evaluations). Defaults to calling agent.", false).
		StringParam("kind", "Scope kind (for scope op)", false).
		StringParam("key", "Scope key (for scope op)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			board, err := bp()
			if err != nil {
				return nil, fmt.Errorf("claims board: %w", err)
			}
			if board == nil {
				return nil, fmt.Errorf("claims board not available (no error returned)")
			}
			var params struct {
				Op          string `json:"op"`
				ClaimID     string `json:"claim_id"`
				TestamentID string `json:"testament_id"`
				AgentID     string `json:"agent_id"`
				Kind        string `json:"kind"`
				Key         string `json:"key"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			agentID := strings.TrimSpace(params.AgentID)
			if agentID == "" {
				agentID = defaultAgentID
			}
			return dispatchBoardQuery(board, params.Op, agentID, params.ClaimID, params.TestamentID, params.Kind, params.Key)
		}).
		Build()
}

func dispatchBoardQuery(board *ClaimsBoard, op, agentID, claimID, testamentID, scopeKind, scopeKey string) (any, error) {
	switch strings.TrimSpace(op) {
	case "summary":
		return board.Summary(), nil
	case "my_claims":
		return board.ClaimsForAgent(agentID, RelationshipSubject), nil
	case "my_evaluations":
		return board.ClaimsForAgentByStatus(agentID, RelationshipIssuer, ClaimStatusTestified), nil
	case "claim":
		c, ok := board.CloneClaim(strings.TrimSpace(claimID))
		if !ok {
			return nil, fmt.Errorf("claim %q not found", claimID)
		}
		return c, nil
	case "testament":
		t, ok := board.CloneTestament(strings.TrimSpace(testamentID))
		if !ok {
			return nil, fmt.Errorf("testament %q not found", testamentID)
		}
		return t, nil
	case "scope":
		ids := board.ClaimIDsWithScope(strings.TrimSpace(scopeKind), strings.TrimSpace(scopeKey))
		result := make([]*Claim, 0, len(ids))
		for _, id := range ids {
			if c, ok := board.CloneClaim(id); ok {
				result = append(result, c)
			}
		}
		return result, nil
	case "pending_validations":
		return board.PendingValidationsForClaim(strings.TrimSpace(claimID)), nil
	case "testaments":
		nodes := Traverse(board, strings.TrimSpace(claimID), RelationshipTestament, 1)
		var testaments []*Testament
		for _, n := range nodes {
			if n.Testament != nil {
				testaments = append(testaments, n.Testament)
			}
		}
		return testaments, nil
	default:
		return nil, fmt.Errorf("unknown query_board op: %q", op)
	}
}

// PostActionSkill creates the post_action LLM-callable skill. The
// optional InboxProvider enables automatic expectation registration:
// after PostAction commits, every directed claim gets an expectation
// registered on the inbox so the issuer's OnResolved fires when the
// subject responds with a testament.
func PostActionSkill(bp BoardProvider, ip ...InboxProvider) *skills.Skill {
	var inboxFn InboxProvider
	if len(ip) > 0 {
		inboxFn = ip[0]
	}
	return skills.NewSkill("post_action").
		Description("Issue an action (set of claims) against one or more target agents. Each claim names its target via the `subject` field — this is the agent who must respond with a testament. Covers task, challenge, consultation, corrective, and archival actions. Returns the committed action_id and claim_ids. The issuer's inbox automatically watches for testament responses from the subject agents.").
		Domain("claims").
		Keywords("action", "claim", "issue", "challenge", "consult").
		Priority(97).
		StringParam("action_type", "Type: task, challenge, consultation, corrective, archival, prompt", true).
		StringParam("claims_json", "JSON array of claims with title, description, subject, scope, and validations", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			board, err := bp()
			if err != nil {
				return nil, fmt.Errorf("claims board: %w", err)
			}
			if board == nil {
				return nil, fmt.Errorf("claims board not available (no error returned)")
			}
			var params struct {
				ActionType string          `json:"action_type"`
				ClaimsJSON json.RawMessage `json:"claims_json"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			claimsRaw := unwrapStringEncodedJSON(params.ClaimsJSON)
			var claimInputs []claimInput
			if err := json.Unmarshal(claimsRaw, &claimInputs); err != nil {
				return nil, fmt.Errorf("invalid claims_json: %w", err)
			}

			actionType := ActionType(strings.TrimSpace(params.ActionType))
			postedClaims := make([]Claim, 0, len(claimInputs))
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
				postedClaims = append(postedClaims, c)
			}

			action := Action{Type: actionType}
			if err := board.PostAction(ctx, action, postedClaims); err != nil {
				return nil, err
			}

			// Auto-register expectations: for each directed claim, the
			// issuer expects a TestamentDelta when the subject responds.
			claimIDs := make([]string, 0, len(postedClaims))
			for idx := range postedClaims {
				claimIDs = append(claimIDs, postedClaims[idx].ID)
			}
			if inboxFn != nil {
				if inbox := inboxFn(); inbox != nil {
					expectedDelta := expectedDeltaForActionType(actionType)
					priority := expectedPriorityForActionType(actionType)
					for idx := range postedClaims {
						inbox.Expect(&Expectation{
							ClaimID:       postedClaims[idx].ID,
							ExpectedDelta: expectedDelta,
							ActionID:      action.ID,
							IssuedAt:      postedClaims[idx].Created,
							Priority:      priority,
						})
					}
				}
			}

			return map[string]any{
				"action_id": action.ID,
				"claim_ids": claimIDs,
				"count":     len(claimIDs),
			}, nil
		}).
		Build()
}

// expectedDeltaForActionType returns the delta kind the issuer should
// expect in response to an action of the given type.
func expectedDeltaForActionType(t ActionType) string {
	switch t {
	case ActionTypeChallenge:
		return DeltaKindTestament
	case ActionTypeConsultation:
		return DeltaKindTestament
	case ActionTypeTask:
		return DeltaKindTestament
	case ActionTypeCorrective:
		return DeltaKindTestament
	}
	return DeltaKindTestament
}

// expectedPriorityForActionType returns the priority the issuer
// should assign to the response expectation.
func expectedPriorityForActionType(t ActionType) WorkUnitPriority {
	switch t {
	case ActionTypeChallenge:
		return PriorityChallenge
	case ActionTypeCorrective:
		return PriorityRemediation
	case ActionTypeConsultation:
		return PriorityResponse
	case ActionTypeTask:
		return PriorityResponse
	}
	return PriorityResponse
}

func InspectClaimConflictsSkill(bp BoardProvider) *skills.Skill {
	return skills.NewSkill("inspect_claim_conflicts").
		Description("Surface overlapping claims, competing testaments, and mutual dependency blocks.").
		Domain("claims").
		Keywords("conflicts", "overlap", "competing", "blocked").
		Priority(94).
		Handler(func(ctx context.Context, _ json.RawMessage) (any, error) {
			board, err := bp()
			if err != nil {
				return nil, fmt.Errorf("claims board: %w", err)
			}
			if board == nil {
				return nil, fmt.Errorf("claims board not available (no error returned)")
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
		Description("Submit testaments responding to specific claims. Each testament's `claim_id` field names the claim being answered — this is how you respond to work directed at you. Carry artifacts as proof of completion or error details. When work FAILS, submit a testament with kind='error' artifacts — never silently drop errors. Error artifacts are structured reports the issuer evaluates, not system failures.").
		Domain("claims").
		Keywords("testament", "submit", "proof", "artifacts", "done", "error").
		Priority(96).
		StringParam("testaments_json", "JSON array of testaments. Each has claim_id, summary, confidence, and artifacts. Artifact kinds include 'code_reference', 'test_output', 'diff', 'error' (for failures), 'error_trace' (stack traces), 'error_diagnostic' (environmental failures).", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			board, err := bp()
			if err != nil {
				return nil, fmt.Errorf("claims board: %w", err)
			}
			if board == nil {
				return nil, fmt.Errorf("claims board not available (no error returned)")
			}
			var params struct {
				TestamentsJSON json.RawMessage `json:"testaments_json"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			testamentsRaw := unwrapStringEncodedJSON(params.TestamentsJSON)
			var testamentInputs []testamentInput
			if err := json.Unmarshal(testamentsRaw, &testamentInputs); err != nil {
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
			board, err := bp()
			if err != nil {
				return nil, fmt.Errorf("claims board: %w", err)
			}
			if board == nil {
				return nil, fmt.Errorf("claims board not available (no error returned)")
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
			board, err := bp()
			if err != nil {
				return nil, fmt.Errorf("claims board: %w", err)
			}
			if board == nil {
				return nil, fmt.Errorf("claims board not available (no error returned)")
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
			board, err := bp()
			if err != nil {
				return nil, fmt.Errorf("claims board: %w", err)
			}
			if board == nil {
				return nil, fmt.Errorf("claims board not available (no error returned)")
			}
			var params struct {
				ClaimID          string          `json:"claim_id"`
				Reason           string          `json:"reason"`
				ReplacementsJSON json.RawMessage `json:"replacements_json"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			replacementsRaw := unwrapStringEncodedJSON(params.ReplacementsJSON)
			var claimInputs []claimInput
			if err := json.Unmarshal(replacementsRaw, &claimInputs); err != nil {
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
	Scope       flexibleScope     `json:"scope,omitempty"`
	Validations []validationInput `json:"validations,omitempty"`
}

// flexibleScope handles LLM variability in how scope is provided:
//   - string: "hello-cli" → [{Kind: "scope", Key: "hello-cli"}]
//   - []string: ["hello-cli", "auth"] → [{Kind: "scope", Key: "hello-cli"}, ...]
//   - []ClaimScopeEntry: [{kind: "file", key: "src/main.go"}] → used directly
type flexibleScope []ClaimScopeEntry

func (s *flexibleScope) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}

	// Try proper []ClaimScopeEntry first.
	var entries []ClaimScopeEntry
	if err := json.Unmarshal(data, &entries); err == nil {
		*s = entries
		return nil
	}

	// Try single string: "hello-cli"
	var single string
	if err := json.Unmarshal(data, &single); err == nil && single != "" {
		*s = []ClaimScopeEntry{{Kind: "scope", Key: single}}
		return nil
	}

	// Try []string: ["hello-cli", "auth"]
	var strings []string
	if err := json.Unmarshal(data, &strings); err == nil {
		result := make([]ClaimScopeEntry, 0, len(strings))
		for _, v := range strings {
			if v != "" {
				result = append(result, ClaimScopeEntry{Kind: "scope", Key: v})
			}
		}
		*s = result
		return nil
	}

	return fmt.Errorf("scope must be a string, string array, or [{kind, key}] array")
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

// unwrapStringEncodedJSON handles the common LLM behavior of double-encoding
// JSON parameters: sending `"[{...}]"` (a JSON string containing JSON) instead
// of `[{...}]` (raw JSON). When the input is a JSON string, it unquotes it
// and returns the inner JSON. When the input is already raw JSON (array or
// object), it returns it unchanged.
func unwrapStringEncodedJSON(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw
	}
	// If it starts with a quote, it's a JSON string — unquote to get the inner JSON.
	if trimmed[0] == '"' {
		var inner string
		if err := json.Unmarshal(trimmed, &inner); err == nil {
			return json.RawMessage(inner)
		}
	}
	return raw
}
