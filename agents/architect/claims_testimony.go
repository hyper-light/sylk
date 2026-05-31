package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/providers"
)

// architectBoard resolves the claims board for the architect's session.
func (a *Architect) architectBoard() *claims.ClaimsBoard {
	return claims.DefaultSessionBoardRegistry().Lookup(a.config.SessionID)
}

// architectScope adapts *concurrency.GoroutineScope to claims.ScopeProvider
// so the testament accumulator's Flush can dispatch async via the
// architect's tracked scope. Without this adapter, Flush gets passed
// a nil scope and emits accumulator_flush_scope_unwired errors,
// dropping every per-claim testament the architect would have
// recorded.
func (a *Architect) architectScope() claims.ScopeProvider {
	if a.scope == nil {
		return nil
	}
	return &architectScopeAdapter{scope: a.scope}
}

type architectScopeAdapter struct {
	scope *concurrency.GoroutineScope
}

func (s *architectScopeAdapter) Go(desc string, timeout time.Duration, fn func(context.Context) error) error {
	return s.scope.Go(desc, timeout, concurrency.WorkFunc(fn))
}

// architectPostClaim posts an action with a claim to the session board.
// Async via scope when available. Best-effort.
//
// Stamps a caused_by relation when the context carries an enclosing
// claim ID via claims.ParentClaimIDFromContext (UI_DESIGN.md §4.5).
// The architect dispatching from inside an executing plan claim is
// the canonical case; top-level architect actions (no parent) get no
// relation and become cycle roots.
//
// Just-in-time response routing (CLAIMS.md §5): after a successful
// PostAction, register a response Expectation on the architect's
// inbox via claims.RegisterPostActionExpectations. The peer's
// testament wakes the architect via expectation match (event-driven,
// targeted), not via a standing subscription firehose.
func (a *Architect) architectPostClaim(ctx context.Context, action claims.Action, claim claims.Claim) {
	board := a.architectBoard()
	if board == nil {
		return
	}
	claim.Relations = shared.AttachCausedByFromContext(ctx, claim.Relations)
	posted := []claims.Claim{claim}
	if a.scope != nil {
		if err := a.scope.Go("architect_post_claim", 5*time.Second, func(gctx context.Context) error {
			if err := board.PostAction(gctx, action, posted); err != nil {
				return err
			}
			claims.RegisterPostActionExpectations(a.claimsInbox, action, posted)
			return nil
		}); err != nil {
			slog.Error("architect_post_claim_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("architect post claim dispatch: " + err.Error())
		}
		return
	}
	if err := board.PostAction(ctx, action, posted); err != nil {
		slog.Error("architect_post_claim_failed", "error", err.Error())
		board.RecordNotificationError("architect post claim: " + err.Error())
		return
	}
	claims.RegisterPostActionExpectations(a.claimsInbox, action, posted)
}

// architectSubmitTestament submits a testament to the session board.
// Async via scope when available. Best-effort.
func (a *Architect) architectSubmitTestament(ctx context.Context, testament claims.Testament) {
	board := a.architectBoard()
	if board == nil {
		return
	}
	action := claims.Action{AgentID: "architect", Type: claims.ActionTypeTestament}
	if a.scope != nil {
		if err := a.scope.Go("architect_submit_testament", 5*time.Second, func(gctx context.Context) error {
			return board.SubmitTestaments(gctx, action, []claims.Testament{testament})
		}); err != nil {
			slog.Error("architect_submit_testament_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("architect testament dispatch: " + err.Error())
		}
		return
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Error("architect_submit_testament_failed", "error", err.Error())
		board.RecordNotificationError("architect testament: " + err.Error())
	}
}

// architectSubmitTestamentSync submits a testament and reports the board
// error to callers that must not claim visibility before evidence is durable.
func (a *Architect) architectSubmitTestamentSync(ctx context.Context, testament claims.Testament) error {
	if ctx == nil {
		ctx = context.Background()
	}
	board := a.architectBoard()
	if board == nil {
		return fmt.Errorf("architect claims board unavailable")
	}
	action := claims.Action{AgentID: "architect", Type: claims.ActionTypeTestament}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Error("architect_submit_testament_failed", "error", err.Error())
		board.RecordNotificationError("architect testament: " + err.Error())
		return err
	}
	return nil
}

// architectClaimAction builds a claim action for the architect.
func architectClaimAction(actionType claims.ActionType) claims.Action {
	return claims.Action{AgentID: "architect", Type: actionType}
}

// architectSelfClaim builds an audit-shaped claim where the architect
// is BOTH issuer and subject — used to record the architect's own
// work (tool loops, planning stages) on the board for traceability.
// The amplifier filters self-claims out of the inbox-delivery path
// (BoardAmplifier.buildInboxDeltas) so posting one does not wake the
// architect's own request handler. Use architectClaimWithSubject for
// directed work targeting a peer (handoff dispatch, consult,
// challenge); confusing the two is the bug fixed at plan_execution.go
// where a handoff record was incorrectly self-targeted, leaving its
// "Orchestrator acknowledges plan receipt" validation unsatisfiable.
func architectSelfClaim(title, description string, scope []claims.ClaimScopeEntry, actionType claims.ActionType, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  actionType,
		Relations: []claims.Relation{
			{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// architectClaimWithSubject builds a claim issued by the architect against another agent.
func architectClaimWithSubject(title, description, subject string, scope []claims.ClaimScopeEntry, actionType claims.ActionType, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  actionType,
		Relations: []claims.Relation{
			{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: subject, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// architectTestament builds a testament issued by the architect.
func (a *Architect) architectTestament(summary, confidence string, artifacts []*claims.Artifact) claims.Testament {
	return claims.Testament{
		AgentID:    "architect",
		SessionID:  a.config.SessionID,
		Summary:    summary,
		Confidence: confidence,
		Relations: []claims.Relation{
			{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
		Artifacts: artifacts,
	}
}

// architectTestamentWithSubject builds a testament with a subject relation.
func (a *Architect) architectTestamentWithSubject(summary, confidence, subject string, artifacts []*claims.Artifact) claims.Testament {
	return claims.Testament{
		AgentID:    "architect",
		SessionID:  a.config.SessionID,
		Summary:    summary,
		Confidence: confidence,
		Relations: []claims.Relation{
			{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: subject, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Artifacts: artifacts,
	}
}

// architectArtifact builds an artifact for the architect.
func (a *Architect) architectArtifact(kind, reference string) *claims.Artifact {
	return &claims.Artifact{
		AgentID:   "architect",
		SessionID: a.config.SessionID,
		Kind:      kind,
		Reference: reference,
	}
}

// architectJSONArtifact builds a JSON-serialized artifact.
func (a *Architect) architectJSONArtifact(kind string, value any) *claims.Artifact {
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	return a.architectArtifact(kind, string(ref))
}

// architectValidation builds a pending validation.
func architectValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	return &claims.Validation{
		Type: vtype, Required: required,
		Description: description, QualityBar: qualityBar,
		Status: claims.ValidationStatusPending,
	}
}

// architectPassedValidation builds a validation already evaluated as passed.
func architectPassedValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	return &claims.Validation{
		Type: vtype, Required: required,
		Description: description, QualityBar: qualityBar,
		Status: claims.ValidationStatusPassed,
	}
}

// architectStageClaim posts a planning stage claim and returns a function
// to submit the stage testament when the stage completes.
func (a *Architect) architectStageClaim(ctx context.Context, planID, stageTitle, stageDescription string) func(summary string, artifacts []*claims.Artifact, err error) {
	start := time.Now()
	a.architectPostClaim(ctx,
		architectClaimAction(claims.ActionTypeTask),
		architectSelfClaim(stageTitle, stageDescription,
			[]claims.ClaimScopeEntry{{Kind: "plan", Key: planID}},
			claims.ActionTypeTask, nil),
	)

	return func(summary string, artifacts []*claims.Artifact, stageErr error) {
		if stageErr != nil {
			artifacts = append(artifacts, a.architectArtifact("error", stageErr.Error()))
			summary = stageTitle + " failed: " + stageErr.Error()
		}
		elapsed := time.Since(start).Truncate(time.Millisecond)
		artifacts = append(artifacts, a.architectArtifact("duration_ms", fmt.Sprintf("%d", elapsed.Milliseconds())))
		a.architectSubmitTestament(ctx, a.architectTestament(summary, "committed", artifacts))
	}
}

func (a *Architect) processClaimsEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil {
		return nil
	}
	claims.RouteFlowDebugLog().Info("architect_route_flow_process_claims_entry_start",
		append(a.claimsEntryRouteFlowArgs(entry), claims.DeltaDebugArgs(entry.Delta)...)...,
	)
	claims.RouteDebugLog().Info("architect_process_claims_entry_start",
		append([]any{
			"agent_id", a.id,
			"session_id", a.config.SessionID,
			"node_has_claim", entry.Node.Claim != nil,
			"node_has_testament", entry.Node.Testament != nil,
			"node_has_validation", entry.Node.Validation != nil,
		}, claims.DeltaDebugArgs(entry.Delta)...)...,
	)
	planner := a.ensurePlanner(ctx)
	if planner == nil {
		claims.RouteFlowDebugLog().Info("architect_route_flow_process_claims_entry_failed",
			append(a.claimsEntryRouteFlowArgs(entry),
				append([]any{"reason", "planner_not_configured"}, claims.DeltaDebugArgs(entry.Delta)...)...)...,
		)
		claims.RouteDebugLog().Info("architect_process_claims_entry_failed",
			append([]any{
				"agent_id", a.id,
				"session_id", a.config.SessionID,
				"reason", "planner_not_configured",
			}, claims.DeltaDebugArgs(entry.Delta)...)...,
		)
		return fmt.Errorf("architect: LLM planner not configured")
	}

	acc := shared.NewClaimsEntryAccumulator("architect", a.config.SessionID, entry)
	acc.WithBoard(a.architectBoard())
	defer acc.Flush(ctx, a.architectBoard(), a.architectScope())
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

	// Push: narrate entry into the claim. Surfaces an immediate
	// "Acknowledging request" status on the architect's row in the
	// UI before the LLM tool loop produces any output.
	if entry.Node.Claim != nil {
		shared.RecordAgentState(ctx, a.architectBoard(), entry.Node.Claim.ID,
			"Acknowledging request", shared.AgentStateReasoning, nil)
	}

	userMessage := shared.ComposeClaimsEntryPrompt(entry)
	board := a.architectBoard()
	userMessage = claims.PrependBoardPreamble(userMessage, board, "architect")

	systemPrompt := DefaultSystemPrompt
	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: userMessage}},
		Tools:        a.buildToolDefinitions(),
		Model:        a.config.Model,
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyProtocolRuntimeProfile(req, a.config.SessionID)

	ledger := a.steering.Create(entry.Delta.DeltaKey(), a.id, a.config.SessionID, nil, nil)
	defer a.steering.Close(entry.Delta.DeltaKey(), ctx.Err() != nil)

	ctx = shared.WithContinuationStore(ctx, a.continuationStore)
	ctx = shared.WithTurnContext(ctx, &shared.TurnContext{
		Request:       req,
		CorrelationID: ledgerCorrelationOrDefault(ledger, a.config.SessionID),
		AgentID:       a.id,
		SessionID:     a.config.SessionID,
	})

	claims.RouteFlowDebugLog().Info("architect_route_flow_llm_turn_start",
		append(a.claimsEntryRouteFlowArgs(entry), claims.DeltaDebugArgs(entry.Delta)...)...,
	)
	result, err := shared.ExecuteTurnLoop(ctx, ledger, req, func() (string, error) {
		return a.executeToolLoop(ctx, req, "claims_entry", nil, ledger)
	})
	if err != nil {
		if shared.IsConsultYielded(err) {
			slog.Info("architect_claims_entry_yielded", "delta_key", entry.Delta.DeltaKey())
			claims.RouteFlowDebugLog().Info("architect_route_flow_process_claims_entry_yielded",
				append(a.claimsEntryRouteFlowArgs(entry), claims.DeltaDebugArgs(entry.Delta)...)...,
			)
			claims.RouteDebugLog().Info("architect_process_claims_entry_yielded",
				append([]any{
					"agent_id", a.id,
					"session_id", a.config.SessionID,
				}, claims.DeltaDebugArgs(entry.Delta)...)...,
			)
			return nil
		}
		slog.Error("architect_claims_entry_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		claims.RouteFlowDebugLog().Info("architect_route_flow_process_claims_entry_failed",
			append(a.claimsEntryRouteFlowArgs(entry),
				append([]any{"error", err.Error()}, claims.DeltaDebugArgs(entry.Delta)...)...)...,
		)
		claims.RouteDebugLog().Info("architect_process_claims_entry_failed",
			append([]any{
				"agent_id", a.id,
				"session_id", a.config.SessionID,
				"error", err.Error(),
			}, claims.DeltaDebugArgs(entry.Delta)...)...,
		)
		acc.Record("error", err.Error())
		return err
	}
	acc.Record("result_length", fmt.Sprintf("%d", len(result)))
	acc.Note("Claims entry processed successfully")
	claims.RouteDebugLog().Info("architect_process_claims_entry_done",
		append([]any{
			"agent_id", a.id,
			"session_id", a.config.SessionID,
			"result_length", len(result),
		}, claims.DeltaDebugArgs(entry.Delta)...)...,
	)
	claims.RouteFlowDebugLog().Info("architect_route_flow_process_claims_entry_done",
		append(a.claimsEntryRouteFlowArgs(entry),
			append([]any{"result_length", len(result)}, claims.DeltaDebugArgs(entry.Delta)...)...)...,
	)
	return nil
}

func (a *Architect) claimsEntryRouteFlowArgs(entry *claims.GraphEntryPoint) []any {
	args := []any{
		"agent_id", a.id,
		"session_id", a.config.SessionID,
	}
	if entry == nil || entry.Node.Claim == nil {
		return append(args,
			"node_has_claim", false,
			"node_has_testament", entry != nil && entry.Node.Testament != nil,
			"node_has_validation", entry != nil && entry.Node.Validation != nil,
		)
	}
	claim := entry.Node.Claim
	return append(args,
		"node_has_claim", true,
		"node_has_testament", entry.Node.Testament != nil,
		"node_has_validation", entry.Node.Validation != nil,
		"claim_id", strings.TrimSpace(claim.ID),
		"claim_title", strings.TrimSpace(claim.Title),
		"claim_action", string(claim.ActionType),
		"claim_status", string(claim.Status),
		"claim_lifecycle", string(claim.LifecycleStatus),
	)
}

// truncateArchitectString truncates a string for claim titles.
func truncateArchitectString(s string, max int) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}
