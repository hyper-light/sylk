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
func (o *Orchestrator) orchestratorBoard() *claims.ClaimsBoard {
	return claims.DefaultSessionBoardRegistry().Lookup(o.SessionID())
}

// orchestratorPostClaim posts a claim async via scope. Best-effort.
func (o *Orchestrator) orchestratorPostClaim(ctx context.Context, action claims.Action, claim claims.Claim) {
	board := o.orchestratorBoard()
	if board == nil {
		return
	}
	if o.scope != nil {
		if err := o.scope.Go("orchestrator_post_claim", 5*time.Second, func(gctx context.Context) error {
			return board.PostAction(gctx, action, []claims.Claim{claim})
		}); err != nil {
			slog.Error("orchestrator_post_claim_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("orchestrator post claim dispatch: " + err.Error())
		}
		return
	}
	if err := board.PostAction(ctx, action, []claims.Claim{claim}); err != nil {
		slog.Error("orchestrator_post_claim_failed", "error", err.Error())
		board.RecordNotificationError("orchestrator post claim: " + err.Error())
	}
}

// orchestratorSubmitTestament submits a testament async via scope. Best-effort.
func (o *Orchestrator) orchestratorSubmitTestament(ctx context.Context, testament claims.Testament) {
	board := o.orchestratorBoard()
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
func (o *Orchestrator) processClaimsEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil {
		return nil
	}
	if o.provider == nil {
		return fmt.Errorf("orchestrator: LLM provider not configured")
	}

	acc := claims.NewTestamentAccumulator("orchestrator", o.SessionID())
	defer acc.Flush(ctx, o.orchestratorBoard(), o.orchestratorScope())
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

	userMessage := shared.ComposeClaimsEntryPrompt(entry)
	board := o.orchestratorBoard()
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

	result, err := shared.ExecuteTurnLoop(ledger, req, func() (string, error) {
		return o.executeToolLoop(ctx, req, ledger)
	})
	if err != nil {
		slog.Error("orchestrator_claims_entry_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		acc.Record("error", err.Error())
		return err
	}
	acc.Record("result_length", fmt.Sprintf("%d", len(result)))
	acc.Note("Claims entry processed successfully")
	return nil
}

// truncateOrchestrator truncates a string for claim titles.
func truncateOrchestrator(s string, max int) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}
