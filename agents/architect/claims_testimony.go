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
	"github.com/adalundhe/sylk/core/providers"
)

// architectBoard resolves the claims board for the architect's session.
func (a *Architect) architectBoard() *claims.ClaimsBoard {
	return claims.DefaultSessionBoardRegistry().Lookup(a.config.SessionID)
}

// architectPostClaim posts an action with a claim to the session board.
// Async via scope when available. Best-effort.
func (a *Architect) architectPostClaim(ctx context.Context, action claims.Action, claim claims.Claim) {
	board := a.architectBoard()
	if board == nil {
		return
	}
	if a.scope != nil {
		if err := a.scope.Go("architect_post_claim", 5*time.Second, func(gctx context.Context) error {
			return board.PostAction(gctx, action, []claims.Claim{claim})
		}); err != nil {
			slog.Error("architect_post_claim_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("architect post claim dispatch: " + err.Error())
		}
		return
	}
	if err := board.PostAction(ctx, action, []claims.Claim{claim}); err != nil {
		slog.Error("architect_post_claim_failed", "error", err.Error())
		board.RecordNotificationError("architect post claim: " + err.Error())
	}
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

// architectClaimAction builds a claim action for the architect.
func architectClaimAction(actionType claims.ActionType) claims.Action {
	return claims.Action{AgentID: "architect", Type: actionType}
}

// architectClaim builds a claim issued by the architect against itself.
func architectClaim(title, description string, scope []claims.ClaimScopeEntry, actionType claims.ActionType, validations []*claims.Validation) claims.Claim {
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
	v := architectValidation(vtype, required, description, qualityBar)
	v.Status = claims.ValidationStatusPassed
	return v
}

// architectStageClaim posts a planning stage claim and returns a function
// to submit the stage testament when the stage completes.
func (a *Architect) architectStageClaim(ctx context.Context, planID, stageTitle, stageDescription string) func(summary string, artifacts []*claims.Artifact, err error) {
	start := time.Now()
	a.architectPostClaim(ctx,
		architectClaimAction(claims.ActionTypeTask),
		architectClaim(stageTitle, stageDescription,
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
	planner := a.ensurePlanner(ctx)
	if planner == nil {
		return fmt.Errorf("architect: LLM planner not configured")
	}

	acc := claims.NewTestamentAccumulator("architect", a.config.SessionID)
	defer acc.Flush(ctx, a.architectBoard(), nil)
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

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

	result, err := shared.ExecuteTurnLoop(ledger, req, func() (string, error) {
		return a.executeToolLoop(ctx, req, "claims_entry", nil, ledger)
	})
	if err != nil {
		slog.Error("architect_claims_entry_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		acc.Record("error", err.Error())
		return err
	}
	acc.Record("result_length", fmt.Sprintf("%d", len(result)))
	acc.Note("Claims entry processed successfully")
	return nil
}

// truncateArchitectString truncates a string for claim titles.
func truncateArchitectString(s string, max int) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}
