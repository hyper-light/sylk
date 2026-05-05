package orchestrator

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

// orchestratorScope returns a ScopeProvider for async claims dispatch.
func (o *Orchestrator) orchestratorScope() claims.ScopeProvider {
	if o.scope == nil {
		return nil
	}
	return &orchestratorScopeAdapter{scope: o.scope}
}

type orchestratorScopeAdapter struct {
	scope *concurrency.GoroutineScope
}

func (a *orchestratorScopeAdapter) Go(desc string, timeout time.Duration, fn func(context.Context) error) error {
	return a.scope.Go(desc, timeout, concurrency.WorkFunc(fn))
}

// orchestratorBoard resolves the claims board for the session.
func (o *Orchestrator) orchestratorBoard() (*claims.ClaimsBoard, error) {
	sid := o.SessionID()
	if sid == "" {
		return nil, fmt.Errorf("orchestrator: no session ID configured")
	}
	board := claims.DefaultSessionBoardRegistry().Lookup(sid)
	if board == nil {
		return nil, fmt.Errorf("orchestrator: session %q has no claims board registered", sid)
	}
	return board, nil
}

// orchestratorBoardOrNil returns the board or nil (for best-effort callers).
func (o *Orchestrator) orchestratorBoardOrNil() *claims.ClaimsBoard {
	board, _ := o.orchestratorBoard()
	return board
}

// orchestratorPostClaim posts a claim async via scope. Best-effort.
//
// Stamps a caused_by relation when the context carries an enclosing
// claim ID via claims.ParentClaimIDFromContext (UI_DESIGN.md §4.5),
// so child dispatches nest correctly under the orchestrator's
// currently-executing claim in the bridge's cycle resolver.
func (o *Orchestrator) orchestratorPostClaim(ctx context.Context, action claims.Action, claim claims.Claim) {
	board := o.orchestratorBoardOrNil()
	if board == nil {
		return
	}
	claim.Relations = shared.AttachCausedByFromContext(ctx, claim.Relations)
	posted := []claims.Claim{claim}
	if o.scope != nil {
		if err := o.scope.Go("orchestrator_post_claim", 5*time.Second, func(gctx context.Context) error {
			if err := board.PostAction(gctx, action, posted); err != nil {
				return err
			}
			claims.RegisterPostActionExpectations(o.claimsInbox, action, posted)
			return nil
		}); err != nil {
			slog.Error("orchestrator_post_claim_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("orchestrator post claim dispatch: " + err.Error())
		}
		return
	}
	if err := board.PostAction(ctx, action, posted); err != nil {
		slog.Error("orchestrator_post_claim_failed", "error", err.Error())
		board.RecordNotificationError("orchestrator post claim: " + err.Error())
		return
	}
	claims.RegisterPostActionExpectations(o.claimsInbox, action, posted)
}

// orchestratorSubmitTestament submits a testament async via scope. Best-effort.
func (o *Orchestrator) orchestratorSubmitTestament(ctx context.Context, testament claims.Testament) {
	board := o.orchestratorBoardOrNil()
	if board == nil {
		return
	}
	action := claims.Action{AgentID: "orchestrator", Type: claims.ActionTypeTestament}
	if o.scope != nil {
		if err := o.scope.Go("orchestrator_submit_testament", 5*time.Second, func(gctx context.Context) error {
			return board.SubmitTestaments(gctx, action, []claims.Testament{testament})
		}); err != nil {
			slog.Error("orchestrator_submit_testament_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("orchestrator testament dispatch: " + err.Error())
		}
		return
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Error("orchestrator_submit_testament_failed", "error", err.Error())
		board.RecordNotificationError("orchestrator testament: " + err.Error())
	}
}

// orchestratorTestament builds a testament issued by the orchestrator.
func (o *Orchestrator) orchestratorTestament(summary, confidence string, artifacts []*claims.Artifact) claims.Testament {
	return claims.Testament{
		AgentID: "orchestrator", SessionID: o.SessionID(),
		Summary: summary, Confidence: confidence,
		Relations: []claims.Relation{
			{Related: "orchestrator", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
		Artifacts: artifacts,
	}
}

// orchestratorArtifact builds a single artifact.
func (o *Orchestrator) orchestratorArtifact(kind, reference string) *claims.Artifact {
	return &claims.Artifact{
		AgentID: "orchestrator", SessionID: o.SessionID(),
		Kind: kind, Reference: reference,
	}
}

// orchestratorJSONArtifact builds a JSON-serialized artifact.
func (o *Orchestrator) orchestratorJSONArtifact(kind string, value any) *claims.Artifact {
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	return o.orchestratorArtifact(kind, string(ref))
}

// orchestratorConsultClaim builds a consultation claim against a peer.
func orchestratorConsultClaim(title, description, target string, scope []claims.ClaimScopeEntry, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  claims.ActionTypeConsultation,
		Relations: []claims.Relation{
			{Related: "orchestrator", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: target, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// orchestratorTaskClaim builds a task claim against a peer.
func orchestratorTaskClaim(title, description, target string, scope []claims.ClaimScopeEntry, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  claims.ActionTypeTask,
		Relations: []claims.Relation{
			{Related: "orchestrator", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: target, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// orchestratorCorrectiveClaim builds a corrective claim against a peer.
func orchestratorCorrectiveClaim(title, description, target string, scope []claims.ClaimScopeEntry, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  claims.ActionTypeCorrective,
		Relations: []claims.Relation{
			{Related: "orchestrator", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: target, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// orchestratorValidation builds a pending validation.
func orchestratorValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	return &claims.Validation{
		Type: vtype, Required: required,
		Description: description, QualityBar: qualityBar,
		Status: claims.ValidationStatusPending,
	}
}

// processClaimsEntry handles an event-driven claims delta. Called by
// the ClaimsInbox OnResolved callback under scope.Go.
//
// Handoff fast path: when the entry is the architect's plan-dispatch
// claim (ActionType=Handoff, subject=orchestrator, with a depends_on
// → plan_handoff_payload artifact relation), skip the LLM tool loop
// entirely and run ingestPlan deterministically against the
// artifact's content. The architect's plan dispatch is structured
// data (PlanHandoff JSON), not a deliberative request — running an
// LLM tool loop on a generic claims-entry prompt produces spurious
// tool calls that race the bus path's ingest. This is the
// "claim-driven, single-transport, deterministic dispatch" path
// (architect's planning skill, plan-finalize → testament+artifact;
// user-accept → handoff claim → here).
func (o *Orchestrator) processClaimsEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil {
		return nil
	}
	if planJSON, ok := o.tryDeterministicHandoffIngest(entry); ok {
		ingestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if _, err := o.ingestPlan(ingestCtx, planJSON); err != nil {
			slog.Error("orchestrator_handoff_claim_ingest_failed",
				"error", err.Error(),
				"delta_key", entry.Delta.DeltaKey())
			return err
		}
		return nil
	}
	if o.provider == nil {
		return fmt.Errorf("orchestrator: LLM provider not configured")
	}

	acc := shared.NewClaimsEntryAccumulator("orchestrator", o.SessionID(), entry)
	acc.WithBoard(o.orchestratorBoardOrNil())
	defer acc.Flush(ctx, o.orchestratorBoardOrNil(), o.orchestratorScope())
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

	if entry.Node.Claim != nil {
		shared.RecordAgentState(ctx, o.orchestratorBoardOrNil(), entry.Node.Claim.ID,
			"Acknowledging request", shared.AgentStateReasoning, nil)
	}

	userMessage := shared.ComposeClaimsEntryPrompt(entry)
	board := o.orchestratorBoardOrNil()
	userMessage = claims.PrependBoardPreamble(userMessage, board, "orchestrator")
	o.prepareSkillsForInput(userMessage)

	req := &providers.Request{
		SystemPrompt: DefaultSystemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: userMessage}},
		Tools:        o.buildToolDefinitions(),
		Model:        o.config.Model,
		MaxTokens:    o.config.MaxOutputTokens,
	}
	o.applyBackgroundRuntimeProfile(req)

	ledger := o.steering.Create(entry.Delta.DeltaKey(), "orchestrator", o.SessionID(), nil, nil)
	defer o.steering.Close(entry.Delta.DeltaKey(), ctx.Err() != nil)

	ctx = shared.WithContinuationStore(ctx, o.continuationStore)
	ctx = shared.WithTurnContext(ctx, &shared.TurnContext{
		Request:       req,
		CorrelationID: orchestratorLedgerCorrelation(ledger, o.SessionID()),
		AgentID:       o.config.AgentID,
		SessionID:     o.SessionID(),
	})

	result, err := shared.ExecuteTurnLoop(ctx, ledger, req, func() (string, error) {
		return o.executeToolLoop(ctx, req, ledger)
	})
	if err != nil {
		if shared.IsConsultYielded(err) {
			slog.Info("orchestrator_claims_entry_yielded", "delta_key", entry.Delta.DeltaKey())
			return nil
		}
		slog.Error("orchestrator_claims_entry_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		acc.Record("error", err.Error())
		return err
	}
	acc.Record("result_length", fmt.Sprintf("%d", len(result)))
	acc.Note("Claims entry processed successfully")
	return nil
}

// tryDeterministicHandoffIngest inspects an inbound claims entry and,
// if it is the architect's plan-dispatch handoff claim (ActionType=
// Handoff, subject=orchestrator, depends_on→plan_handoff_payload
// artifact), resolves the artifact off the board and returns its
// PlanHandoff JSON. Returns ok=false for any entry that does not
// match the deterministic-dispatch shape; the caller falls through to
// the legacy LLM tool loop.
//
// The artifact is looked up by walking the board's projection
// testaments. The architect submits the testament+artifact at
// plan-finalize (publishPreparedHandoff), well before the user-accept
// dispatch posts the claim, so the artifact is on the board when the
// orchestrator's intake fires.
func (o *Orchestrator) tryDeterministicHandoffIngest(entry *claims.GraphEntryPoint) (string, bool) {
	if entry == nil || entry.Node.Claim == nil {
		return "", false
	}
	c := entry.Node.Claim
	if c.ActionType != claims.ActionTypeHandoff {
		return "", false
	}
	if !o.claimSubjectIsOrchestrator(c) {
		return "", false
	}
	artifactID := dependsOnArtifactID(c.Relations)
	if artifactID == "" {
		return "", false
	}
	board := o.orchestratorBoardOrNil()
	if board == nil {
		return "", false
	}
	proj := board.Projection()
	if proj == nil {
		return "", false
	}
	for i := range proj.Testaments {
		t := &proj.Testaments[i]
		for _, a := range t.Artifacts {
			if a == nil || a.ID != artifactID {
				continue
			}
			if a.Kind != claims.ArtifactKindPlanHandoffPayload {
				continue
			}
			payload := strings.TrimSpace(a.Reference)
			if payload == "" {
				return "", false
			}
			return payload, true
		}
	}
	return "", false
}

// claimSubjectIsOrchestrator reports whether a claim's subject
// resolves to this orchestrator instance. Tolerates the architect
// using either the orchestrator's slug ("orchestrator") or its
// runtime UUID — both must work because dispatch sites write the slug
// directly while the inbox subscription is keyed off the UUID.
func (o *Orchestrator) claimSubjectIsOrchestrator(c *claims.Claim) bool {
	if c == nil {
		return false
	}
	subject := strings.TrimSpace(claims.SubjectAgentID(c.Relations))
	if subject == "" {
		return false
	}
	if subject == "orchestrator" {
		return true
	}
	return subject == o.config.AgentID
}

// dependsOnArtifactID returns the Related ID of the first depends_on
// Relation whose RelatedType is artifact, or "" if none.
func dependsOnArtifactID(relations []claims.Relation) string {
	for _, r := range relations {
		if r.Relationship == claims.RelationshipDependsOn && r.RelatedType == claims.RelatedTypeArtifact {
			return strings.TrimSpace(r.Related)
		}
	}
	return ""
}

// truncateOrchestrator truncates a string for claim titles.
func truncateOrchestrator(s string, max int) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}
