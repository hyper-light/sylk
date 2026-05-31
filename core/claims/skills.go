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
		Description("Query the claims board. Use this at claim intake to inspect delivered claims, expected tool calls, linked testaments, artifacts, and pending validations before acting. Use op to select what you need.\n\n"+
			"Ops:\n"+
			"- summary: Board status counts (phase, accepted/total, pending, etc.) — fastest\n"+
			"- my_claims: Claims where you are the subject (your directed work)\n"+
			"- my_evaluations: Testified claims where you are issuer/evaluator (awaiting your validation)\n"+
			"- claim: Single claim by ID with its validations\n"+
			"- testament: Single testament by ID with its artifacts\n"+
			"- artifact: Single artifact by ID with reference, metadata, and presentation\n"+
			"- scope: Claims overlapping a scope entry (kind + key)\n"+
			"- claims_by_lifecycle: Claims with exact lifecycle_status\n"+
			"- testaments_by_lifecycle: Testaments with exact lifecycle_status\n"+
			"- my_claims_by_lifecycle: Subject claims for agent_id with exact lifecycle_status\n"+
			"- pending_validations: Pending validations on a specific claim\n"+
			"- testaments: Testaments linked to a specific claim").
		Domain("claims").
		Keywords("claims", "board", "query", "status", "my_claims", "scope", "validations").
		Priority(98).
		EnumParam("op", "Query operation", []string{"summary", "my_claims", "my_evaluations", "claim", "testament", "artifact", "scope", "claims_by_lifecycle", "testaments_by_lifecycle", "my_claims_by_lifecycle", "pending_validations", "testaments"}, true).
		StringParam("claim_id", "Claim ID (for claim, pending_validations, testaments ops)", false).
		StringParam("testament_id", "Testament ID (for testament op)", false).
		StringParam("artifact_id", "Artifact ID (for artifact op)", false).
		StringParam("agent_id", "Agent ID override (for my_claims, my_evaluations). Defaults to calling agent.", false).
		StringParam("lifecycle_status", "Exact lifecycle status for lifecycle filter ops", false).
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
				ArtifactID  string `json:"artifact_id"`
				AgentID     string `json:"agent_id"`
				Lifecycle   string `json:"lifecycle_status"`
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
			return dispatchBoardQuery(board, params.Op, agentID, params.ClaimID, params.TestamentID, params.ArtifactID, params.Lifecycle, params.Kind, params.Key)
		}).
		Build()
}

func dispatchBoardQuery(board *ClaimsBoard, op, agentID, claimID, testamentID, artifactID, lifecycleStatus, scopeKind, scopeKey string) (any, error) {
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
	case "artifact":
		a, ok := board.CloneArtifact(strings.TrimSpace(artifactID))
		if !ok {
			return nil, fmt.Errorf("artifact %q not found", artifactID)
		}
		return a, nil
	case "scope":
		ids := board.ClaimIDsWithScope(strings.TrimSpace(scopeKind), strings.TrimSpace(scopeKey))
		result := make([]*Claim, 0, len(ids))
		for _, id := range ids {
			if c, ok := board.CloneClaim(id); ok {
				result = append(result, c)
			}
		}
		return result, nil
	case "claims_by_lifecycle":
		status := ClaimLifecycleStatus(strings.TrimSpace(lifecycleStatus))
		if !status.Valid() {
			return nil, fmt.Errorf("unknown claim lifecycle status %q", lifecycleStatus)
		}
		return board.ClaimsByLifecycleStatus(status), nil
	case "testaments_by_lifecycle":
		status := TestamentLifecycleStatus(strings.TrimSpace(lifecycleStatus))
		if !status.Valid() {
			return nil, fmt.Errorf("unknown testament lifecycle status %q", lifecycleStatus)
		}
		return board.TestamentsByLifecycleStatus(status), nil
	case "my_claims_by_lifecycle":
		status := ClaimLifecycleStatus(strings.TrimSpace(lifecycleStatus))
		if !status.Valid() {
			return nil, fmt.Errorf("unknown claim lifecycle status %q", lifecycleStatus)
		}
		return board.ClaimsForAgentByLifecycleStatus(agentID, RelationshipSubject, status), nil
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
		Description("Issue an action (set of claims) against one or more target agents. Each claim names its target via the `subject` field — this is the agent who must respond with a testament. Use `expected_tool_calls` on the claim for work the subject should attempt, and on validations for tools the evaluator should run. Covers task, challenge, consultation, corrective, archival, prompt, and handoff actions. Returns the committed action_id and claim_ids. The issuer's inbox automatically watches for testament responses from the subject agents.").
		Domain("claims").
		Keywords("action", "claim", "issue", "challenge", "consult").
		Priority(97).
		StringParam("action_type", "Type: task, challenge, consultation, corrective, archival, prompt, handoff", true).
		StringParam("claims_json", `JSON array of claim objects. Each claim: {"title": str, "description": str, "subject": "<agent_id>", "scope": [{"kind": "file"|"scope"|..., "key": str}], "expected_tool_calls": [{"tool": str, "arguments": {...}, "purpose": str, "required": bool, "produces_artifacts": [str]}], "validations": [{"description": str, "quality_bar": str, "type": "test"|"inspection"|"integration"|"contract"|"design"|"regression"|"receipt", "expected_tool_calls": [{"tool": str, "arguments": {...}, "purpose": str, "required": bool}]}]}. validations MUST be an array of objects — never a string. Example: [{"title":"Inspect repo","description":"Check current structure","subject":"librarian","scope":[{"kind":"workspace","key":"root"}],"expected_tool_calls":[{"tool":"workspace_read","arguments":{"op":"glob","path":"."},"purpose":"list top-level files","required":true,"produces_artifacts":["workspace_observation"]}],"validations":[{"description":"response received","quality_bar":"testament links workspace observation","type":"receipt"}]}]`, true).
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

			if strings.TrimSpace(params.ActionType) == "" {
				return nil, fmt.Errorf(
					"tool %q: required parameter %q is missing or empty. Expected: one of task|challenge|consultation|corrective|archival|prompt",
					"post_action", "action_type",
				)
			}
			const claimsShapeHint = `non-empty JSON array of claim objects, e.g. [{"title":"...","description":"...","subject":"<agent_id>","scope":[{"kind":"file","key":"path"}],"expected_tool_calls":[{"tool":"workspace_read","arguments":{"op":"glob","path":"."},"purpose":"inspect workspace","required":true}],"validations":[{"description":"...","quality_bar":"...","type":"test","expected_tool_calls":[{"tool":"run_tests","arguments":{"target":"./..."},"required":true}]}]}]`
			if err := requireRawJSONParam("post_action", "claims_json", params.ClaimsJSON, claimsShapeHint); err != nil {
				return nil, err
			}

			claimsRaw := unwrapJSONArray(params.ClaimsJSON)
			if err := diagnoseJSONTruncation("post_action", "claims_json", claimsRaw); err != nil {
				return nil, err
			}
			var claimInputs []claimInput
			if err := json.Unmarshal(claimsRaw, &claimInputs); err != nil {
				return nil, fmt.Errorf("invalid claims_json: %w. Expected: %s", err, claimsShapeHint)
			}
			if len(claimInputs) == 0 {
				return nil, fmt.Errorf(
					"tool %q: %q parsed to zero claims. Expected: %s",
					"post_action", "claims_json", claimsShapeHint,
				)
			}

			actionType := ActionType(strings.TrimSpace(params.ActionType))
			postedClaims := make([]Claim, 0, len(claimInputs))
			for _, ci := range claimInputs {
				c := Claim{
					Title:             ci.Title,
					Description:       ci.Description,
					Scope:             ci.Scope,
					ExpectedToolCalls: ci.ExpectedToolCalls,
					Relations: []Relation{
						{Related: ci.Subject, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
					},
				}
				for _, vi := range ci.Validations {
					c.Validations = append(c.Validations, &Validation{
						Description:       vi.Description,
						QualityBar:        vi.QualityBar,
						Type:              ValidationType(vi.Type),
						Required:          true,
						ExpectedToolCalls: vi.ExpectedToolCalls,
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
	return ExpectedDeltaForActionType(t)
}

func expectedPriorityForActionType(t ActionType) WorkUnitPriority {
	return ExpectedPriorityForActionType(t)
}

// ExpectedDeltaForActionType returns the delta kind the issuer of an
// action of the given ActionType should expect as a peer's response.
// Exported so dispatchers that bypass the post_action skill can
// register their own expectations via RegisterPostActionExpectations.
func ExpectedDeltaForActionType(t ActionType) string {
	switch t {
	case ActionTypeChallenge,
		ActionTypeConsultation,
		ActionTypeTask,
		ActionTypeCorrective,
		ActionTypeHandoff:
		return DeltaKindTestament
	}
	return DeltaKindTestament
}

// ExpectedPriorityForActionType returns the priority the issuer
// should assign to the response expectation.
func ExpectedPriorityForActionType(t ActionType) WorkUnitPriority {
	switch t {
	case ActionTypeChallenge:
		return PriorityChallenge
	case ActionTypeCorrective:
		return PriorityRemediation
	case ActionTypeConsultation:
		return PriorityResponse
	case ActionTypeTask, ActionTypeHandoff:
		return PriorityResponse
	}
	return PriorityResponse
}

// RegisterPostActionExpectations is the canonical helper every
// dispatcher uses after a successful PostAction to register
// just-in-time response expectations on the issuing agent's inbox.
// One call per directed claim — self-targeted claims (issuer ==
// subject) and unaddressed claims are skipped because no peer
// response is awaited.
//
// This is the event-driven dual of the standing-subscription path:
// the issuer waits ONLY for the specific testament its own claim
// produced, not the firehose. UI_DESIGN.md / CLAIMS.md §5 — every
// directed claim's return path SHOULD flow through Expect, not
// through standing subscriptions.
//
// Safe to call with nil inbox or empty claims (no-op). System-
// internal action types are also skipped because the amplifier
// never publishes their TestamentDelta anyway.
func RegisterPostActionExpectations(inbox *ClaimsInbox, action Action, postedClaims []Claim) {
	if inbox == nil || len(postedClaims) == 0 {
		return
	}
	for i := range postedClaims {
		c := &postedClaims[i]
		if IsSystemInternalAction(c.ActionType) {
			continue
		}
		subject := SubjectAgentID(c.Relations)
		issuer := IssuerAgentID(c.Relations)
		if subject == "" || subject == issuer {
			// Self-targeted or unaddressed — no peer response will
			// fire a TestamentDelta against this claim.
			continue
		}
		inbox.Expect(&Expectation{
			ClaimID:       c.ID,
			ExpectedDelta: ExpectedDeltaForActionType(c.ActionType),
			ActionID:      action.ID,
			IssuedAt:      c.Created,
			Priority:      ExpectedPriorityForActionType(c.ActionType),
		})
	}
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
		Description("Submit testaments responding to specific claims. Each testament's `claim_id` field names the claim being answered — this is how you respond to work directed at you. Carry artifacts as proof of completion, refusal, impossibility, blocker, or error details. When work fails or an expected tool cannot run, submit a testament with error/error_trace/error_diagnostic artifacts — never silently drop errors. Error artifacts are structured evidence the issuer evaluates, not system failures.").
		Domain("claims").
		Keywords("testament", "submit", "proof", "artifacts", "done", "error").
		Priority(96).
		StringParam("testaments_json", `JSON array of testament objects. Each testament: {"claim_id": "<id>", "summary": str, "confidence": "low"|"medium"|"high", "artifacts": [{"kind": "code_reference"|"test_output"|"diff"|"error"|"error_trace"|"error_diagnostic", "reference": str, "metadata": {...}, "ephemeral": bool}]}. artifacts MUST be an array of objects — never a string. Example: [{"claim_id":"clm_123","summary":"Tests pass","confidence":"high","artifacts":[{"kind":"test_output","reference":"go test ./... — 42 passed"}]}]`, true).
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

			const testamentsShapeHint = `non-empty JSON array of testament objects, e.g. [{"claim_id":"clm_...","summary":"...","confidence":"high","artifacts":[{"kind":"test_output","reference":"go test ./... — 42 passed"}]}]`
			if err := requireRawJSONParam("submit_testaments", "testaments_json", params.TestamentsJSON, testamentsShapeHint); err != nil {
				return nil, err
			}

			testamentsRaw := unwrapJSONArray(params.TestamentsJSON)
			if err := diagnoseJSONTruncation("submit_testaments", "testaments_json", testamentsRaw); err != nil {
				return nil, err
			}
			var testamentInputs []testamentInput
			if err := json.Unmarshal(testamentsRaw, &testamentInputs); err != nil {
				return nil, fmt.Errorf("invalid testaments_json: %w. Expected: %s", err, testamentsShapeHint)
			}
			if len(testamentInputs) == 0 {
				return nil, fmt.Errorf(
					"tool %q: %q parsed to zero testaments. Expected: %s",
					"submit_testaments", "testaments_json", testamentsShapeHint,
				)
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
		Description("Record incremental progress on a claim you are processing. Use this for non-terminal state changes such as received, inspecting context, running expected tools, waiting on a subclaim, or blocked. If work encounters an error, report it here — then submit a testament with error artifacts when done.").
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
		Description("Evaluate a testament's artifacts against a claim validation's quality bar. Use passed for satisfied evidence, incomplete for missing required evidence, failed for present evidence that does not meet the bar, errored for validator infrastructure errors, and skipped only for explicit waivers.").
		Domain("claims").
		Keywords("validate", "evaluate", "quality", "bar", "pass", "fail", "incomplete", "errored").
		Priority(100).
		StringParam("claim_id", "ID of the claim owning the validation", true).
		StringParam("validation_id", "ID of the validation to evaluate", true).
		StringParam("status", "Verdict: passed, incomplete, failed, errored, or skipped", true).
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
			if !status.IsTerminal() {
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
		StringParam("replacements_json", `JSON array of replacement claim objects. Same shape as claims_json: each has {"title", "description", "subject", "scope": [{"kind","key"}], "validations": [{"description","quality_bar","type"}]}. validations MUST be an array of objects.`, true).
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

			if strings.TrimSpace(params.ClaimID) == "" {
				return nil, fmt.Errorf(
					"tool %q: required parameter %q is missing or empty. Expected: ID of the claim being rejected",
					"post_remediation_claims", "claim_id",
				)
			}
			if strings.TrimSpace(params.Reason) == "" {
				return nil, fmt.Errorf(
					"tool %q: required parameter %q is missing or empty. Expected: explanation for why the claim is being rejected",
					"post_remediation_claims", "reason",
				)
			}
			const replacementsShapeHint = `non-empty JSON array of replacement claim objects, e.g. [{"title":"...","description":"...","subject":"<agent_id>","scope":[{"kind":"file","key":"path"}],"expected_tool_calls":[{"tool":"workspace_read","arguments":{"op":"read","path":"src/app.go"},"required":true}],"validations":[{"description":"...","quality_bar":"...","type":"test","expected_tool_calls":[{"tool":"run_tests","arguments":{"target":"./..."},"required":true}]}]}]`
			if err := requireRawJSONParam("post_remediation_claims", "replacements_json", params.ReplacementsJSON, replacementsShapeHint); err != nil {
				return nil, err
			}

			replacementsRaw := unwrapJSONArray(params.ReplacementsJSON)
			if err := diagnoseJSONTruncation("post_remediation_claims", "replacements_json", replacementsRaw); err != nil {
				return nil, err
			}
			var claimInputs []claimInput
			if err := json.Unmarshal(replacementsRaw, &claimInputs); err != nil {
				return nil, fmt.Errorf("invalid replacements_json: %w. Expected: %s", err, replacementsShapeHint)
			}
			if len(claimInputs) == 0 {
				return nil, fmt.Errorf(
					"tool %q: %q parsed to zero replacement claims. Expected: %s",
					"post_remediation_claims", "replacements_json", replacementsShapeHint,
				)
			}

			var claims []Claim
			for _, ci := range claimInputs {
				c := Claim{
					Title:             ci.Title,
					Description:       ci.Description,
					Scope:             ci.Scope,
					ExpectedToolCalls: ci.ExpectedToolCalls,
					Relations: []Relation{
						{Related: ci.Subject, RelatedType: RelatedTypeAgent, Relationship: RelationshipSubject},
					},
				}
				for _, vi := range ci.Validations {
					c.Validations = append(c.Validations, &Validation{
						Description:       vi.Description,
						QualityBar:        vi.QualityBar,
						Type:              ValidationType(vi.Type),
						Required:          true,
						ExpectedToolCalls: vi.ExpectedToolCalls,
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
	Title             string              `json:"title"`
	Description       string              `json:"description"`
	Subject           string              `json:"subject"`
	Scope             flexibleScope       `json:"scope,omitempty"`
	ExpectedToolCalls []ExpectedToolCall  `json:"expected_tool_calls,omitempty"`
	Validations       flexibleValidations `json:"validations,omitempty"`
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
	Description       string             `json:"description"`
	QualityBar        string             `json:"quality_bar"`
	Type              string             `json:"type"`
	ExpectedToolCalls []ExpectedToolCall `json:"expected_tool_calls,omitempty"`
}

// flexibleValidations handles LLM variability in how validations are provided.
// LLMs frequently emit a string, single object, or string array instead of the
// declared []validationInput shape — this is the single most common source of
// post_action JSON errors. Coerce to the canonical shape:
//   - "ensure tests pass" → [{Description: "ensure tests pass"}]
//   - {description, quality_bar, type} → [{...}]
//   - ["a", "b"] → [{Description: "a"}, {Description: "b"}]
//   - [{...}, {...}] → used directly
type flexibleValidations []validationInput

func (v *flexibleValidations) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}

	// Canonical shape: array of objects.
	var entries []validationInput
	if err := json.Unmarshal(data, &entries); err == nil {
		*v = entries
		return nil
	}

	// Single object: {description, quality_bar, type}.
	var single validationInput
	if err := json.Unmarshal(data, &single); err == nil && (single.Description != "" || single.QualityBar != "" || single.Type != "") {
		*v = flexibleValidations{single}
		return nil
	}

	// Array of strings: ["criterion a", "criterion b"].
	var stringSlice []string
	if err := json.Unmarshal(data, &stringSlice); err == nil {
		out := make(flexibleValidations, 0, len(stringSlice))
		for _, s := range stringSlice {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				out = append(out, validationInput{Description: trimmed})
			}
		}
		*v = out
		return nil
	}

	// Single string: "ensure tests pass".
	var description string
	if err := json.Unmarshal(data, &description); err == nil {
		if trimmed := strings.TrimSpace(description); trimmed != "" {
			*v = flexibleValidations{{Description: trimmed}}
		}
		return nil
	}

	return fmt.Errorf("validations must be a string, string array, [{description, quality_bar, type}] array, or single object")
}

type testamentInput struct {
	ClaimID    string            `json:"claim_id"`
	Summary    string            `json:"summary"`
	Confidence string            `json:"confidence,omitempty"`
	Artifacts  flexibleArtifacts `json:"artifacts,omitempty"`
}

type artifactInput struct {
	Kind      string         `json:"kind"`
	Reference string         `json:"reference"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Ephemeral bool           `json:"ephemeral,omitempty"`
}

// flexibleArtifacts handles LLM variability in how testament artifacts are
// provided. Same coercion contract as flexibleValidations:
//   - "src/foo.go:42" → [{Reference: "src/foo.go:42"}]
//   - {kind, reference, ...} → [{...}]
//   - ["ref1", "ref2"] → [{Reference: "ref1"}, {Reference: "ref2"}]
//   - [{...}, {...}] → used directly
type flexibleArtifacts []artifactInput

func (a *flexibleArtifacts) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}

	var entries []artifactInput
	if err := json.Unmarshal(data, &entries); err == nil {
		*a = entries
		return nil
	}

	var single artifactInput
	if err := json.Unmarshal(data, &single); err == nil && (single.Kind != "" || single.Reference != "" || len(single.Metadata) > 0) {
		*a = flexibleArtifacts{single}
		return nil
	}

	var stringSlice []string
	if err := json.Unmarshal(data, &stringSlice); err == nil {
		out := make(flexibleArtifacts, 0, len(stringSlice))
		for _, s := range stringSlice {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				out = append(out, artifactInput{Reference: trimmed})
			}
		}
		*a = out
		return nil
	}

	var reference string
	if err := json.Unmarshal(data, &reference); err == nil {
		if trimmed := strings.TrimSpace(reference); trimmed != "" {
			*a = flexibleArtifacts{{Reference: trimmed}}
		}
		return nil
	}

	return fmt.Errorf("artifacts must be a string, string array, [{kind, reference, ...}] array, or single object")
}

// requireRawJSONParam validates that a required JSON-typed tool parameter is
// present and non-empty. The schema's required-flag is descriptive only — the
// runtime does not enforce it — so an LLM that omits the field reaches the
// handler with `raw` set to nil bytes, which then fails json.Unmarshal with
// the opaque "unexpected end of JSON input". This helper short-circuits with
// an actionable error that echoes the canonical shape inline so the agent can
// self-correct on retry without another round trip.
func requireRawJSONParam(toolName, paramName string, raw json.RawMessage, shapeHint string) error {
	switch string(bytes.TrimSpace(raw)) {
	case "", "null", `""`, "[]", "{}":
		return fmt.Errorf(
			"tool %q: required parameter %q is missing or empty. Expected: %s",
			toolName, paramName, shapeHint,
		)
	}
	return nil
}

// diagnoseJSONTruncation returns a specific, actionable error when raw is
// detected as truncated mid-structure. Counts brackets and braces while
// respecting string literals (so `[` inside `"foo[bar"` doesn't mis-count).
// When the structure is well-balanced returns nil and json.Unmarshal handles
// the rest. When unbalanced, returns an error naming the missing closers so
// the LLM's next turn can correct without guessing.
//
// The motivating failure: LLMs streaming long structured tool args
// occasionally truncate before emitting trailing closers, producing payloads
// like `[{"title":"...","validations":[{...}]}` (missing the outer `]`). A
// generic "unexpected end of JSON input" gives the LLM no clue what to fix;
// this diagnostic does.
func diagnoseJSONTruncation(toolName, paramName string, raw json.RawMessage) error {
	bracketDepth := 0
	braceDepth := 0
	inString := false
	escaped := false
	for _, b := range raw {
		if escaped {
			escaped = false
			continue
		}
		if b == '\\' && inString {
			escaped = true
			continue
		}
		if b == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch b {
		case '[':
			bracketDepth++
		case ']':
			bracketDepth--
		case '{':
			braceDepth++
		case '}':
			braceDepth--
		}
	}
	if inString {
		return fmt.Errorf(
			"tool %q: %q appears truncated mid-string. Re-emit the full JSON, ensuring all string literals are closed.",
			toolName, paramName,
		)
	}
	if bracketDepth != 0 || braceDepth != 0 {
		return fmt.Errorf(
			"tool %q: %q appears truncated. Detected %d unclosed `[` and %d unclosed `{`. Re-emit the full JSON array, ensuring every opening bracket and brace has a matching closer.",
			toolName, paramName, bracketDepth, braceDepth,
		)
	}
	return nil
}

// unwrapJSONArray normalizes LLM-produced JSON for tool parameters that expect
// an array. It handles three shape mistakes seen in the wild:
//   - double-encoded JSON: `"[{...}]"` (JSON string of JSON) → `[{...}]`
//   - single object instead of array: `{...}` → `[{...}]`
//   - canonical array: `[{...}]` → unchanged
//
// Anything else passes through unchanged so the caller's strict unmarshal
// produces a meaningful error message.
func unwrapJSONArray(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw
	}
	// Unwrap double-encoded JSON string → inner JSON.
	if trimmed[0] == '"' {
		var inner string
		if err := json.Unmarshal(trimmed, &inner); err == nil {
			trimmed = bytes.TrimSpace([]byte(inner))
		}
	}
	if len(trimmed) == 0 {
		return raw
	}
	// Promote single object → single-element array.
	if trimmed[0] == '{' {
		wrapped := make([]byte, 0, len(trimmed)+2)
		wrapped = append(wrapped, '[')
		wrapped = append(wrapped, trimmed...)
		wrapped = append(wrapped, ']')
		return wrapped
	}
	return trimmed
}
