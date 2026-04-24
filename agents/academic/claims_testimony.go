package academic

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

// academicBoard resolves the claims board for the academic's session.
func (a *Academic) academicBoard() *claims.ClaimsBoard {
	return claims.DefaultSessionBoardRegistry().Lookup(a.config.SessionID)
}

// academicPostClaim posts a claim async via scope. Best-effort.
func (a *Academic) academicPostClaim(ctx context.Context, action claims.Action, claim claims.Claim) {
	board := a.academicBoard()
	if board == nil {
		return
	}
	if a.scope != nil {
		if err := a.scope.Go("academic_post_claim", 5*time.Second, func(gctx context.Context) error {
			return board.PostAction(gctx, action, []claims.Claim{claim})
		}); err != nil {
			slog.Error("academic_post_claim_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("academic post claim dispatch: " + err.Error())
		}
		return
	}
	if err := board.PostAction(ctx, action, []claims.Claim{claim}); err != nil {
		slog.Error("academic_post_claim_failed", "error", err.Error())
		board.RecordNotificationError("academic post claim: " + err.Error())
	}
}

// academicSubmitTestament submits a testament async via scope. Best-effort.
func (a *Academic) academicSubmitTestament(ctx context.Context, testament claims.Testament) {
	board := a.academicBoard()
	if board == nil {
		return
	}
	action := claims.Action{AgentID: "academic", Type: claims.ActionTypeTestament}
	if a.scope != nil {
		if err := a.scope.Go("academic_submit_testament", 5*time.Second, func(gctx context.Context) error {
			return board.SubmitTestaments(gctx, action, []claims.Testament{testament})
		}); err != nil {
			slog.Error("academic_submit_testament_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("academic testament dispatch: " + err.Error())
		}
		return
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Error("academic_submit_testament_failed", "error", err.Error())
		board.RecordNotificationError("academic testament: " + err.Error())
	}
}

// academicClaim builds a claim issued by the academic.
func academicClaim(title, description string, scope []claims.ClaimScopeEntry, actionType claims.ActionType, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  actionType,
		Relations: []claims.Relation{
			{Related: "academic", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "academic", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// academicConsultClaim builds a consultation claim against a peer.
func academicConsultClaim(title, description, target string, scope []claims.ClaimScopeEntry, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  claims.ActionTypeConsultation,
		Relations: []claims.Relation{
			{Related: "academic", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: target, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// academicTestament builds a testament issued by the academic.
func (a *Academic) academicTestament(summary, confidence string, artifacts []*claims.Artifact) claims.Testament {
	return claims.Testament{
		AgentID:    "academic",
		SessionID:  a.config.SessionID,
		Summary:    summary,
		Confidence: confidence,
		Relations: []claims.Relation{
			{Related: "academic", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
		Artifacts: artifacts,
	}
}

// academicTestamentWithSubject builds a testament with a subject relation.
func (a *Academic) academicTestamentWithSubject(summary, confidence, subject string, artifacts []*claims.Artifact) claims.Testament {
	return claims.Testament{
		AgentID:    "academic",
		SessionID:  a.config.SessionID,
		Summary:    summary,
		Confidence: confidence,
		Relations: []claims.Relation{
			{Related: "academic", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: subject, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Artifacts: artifacts,
	}
}

// academicArtifact builds a single artifact.
func (a *Academic) academicArtifact(kind, reference string) *claims.Artifact {
	return &claims.Artifact{
		AgentID:   "academic",
		SessionID: a.config.SessionID,
		Kind:      kind,
		Reference: reference,
	}
}

// academicJSONArtifact builds a JSON-serialized artifact.
func (a *Academic) academicJSONArtifact(kind string, value any) *claims.Artifact {
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	return a.academicArtifact(kind, string(ref))
}

// academicValidation builds a pending validation.
func academicValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	return &claims.Validation{
		Type: vtype, Required: required,
		Description: description, QualityBar: qualityBar,
		Status: claims.ValidationStatusPending,
	}
}

// academicStageClaim posts a research stage claim and returns a completion
// function to submit the stage testament.
func (a *Academic) academicStageClaim(ctx context.Context, stageTitle, stageDescription string) func(summary string, artifacts []*claims.Artifact, err error) {
	start := time.Now()
	a.academicPostClaim(ctx,
		claims.Action{AgentID: "academic", Type: claims.ActionTypeTask},
		academicClaim(stageTitle, stageDescription,
			[]claims.ClaimScopeEntry{{Kind: "research", Key: "stage"}},
			claims.ActionTypeTask, nil),
	)
	return func(summary string, artifacts []*claims.Artifact, stageErr error) {
		if stageErr != nil {
			artifacts = append(artifacts, a.academicArtifact("error", stageErr.Error()))
			summary = stageTitle + " failed: " + stageErr.Error()
		}
		artifacts = append(artifacts, a.academicArtifact("duration_ms",
			strings.TrimRight(time.Since(start).Truncate(time.Millisecond).String(), "0")))
		a.academicSubmitTestament(ctx, a.academicTestament(summary, "committed", artifacts))
	}
}

func (a *Academic) processClaimsEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil {
		return nil
	}
	p := a.getProvider()
	if p == nil {
		return fmt.Errorf("academic: LLM provider not configured")
	}

	acc := claims.NewTestamentAccumulator("academic", a.config.SessionID)
	defer acc.Flush(ctx, a.academicBoard(), nil)
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

	userMessage := shared.ComposeClaimsEntryPrompt(entry)
	board := a.academicBoard()
	userMessage = claims.PrependBoardPreamble(userMessage, board, "academic")
	a.prepareSkillsForInput(userMessage)
	surface := a.toolRuntime()

	systemPrompt := a.config.SystemPrompt
	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: userMessage}},
		Tools:        a.buildToolDefinitionsWithSurface(surface),
		Model:        a.config.Model,
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyLLMRuntimeProfile(ctx, req, "research")

	ledger := a.steering.Create(entry.Delta.DeltaKey(), a.id, a.config.SessionID, nil, nil)
	defer a.steering.Close(entry.Delta.DeltaKey(), ctx.Err() != nil)

	result, err := shared.ExecuteTurnLoop(ledger, req, func() (string, error) {
		return a.executeToolLoop(ctx, req, ledger, surface)
	})
	if err != nil {
		slog.Error("academic_claims_entry_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		acc.Record("error", err.Error())
		return err
	}
	acc.Record("result_length", fmt.Sprintf("%d", len(result)))
	acc.Note("Claims entry processed successfully")
	return nil
}

// truncateAcademic truncates a string for claim titles.
func truncateAcademic(s string, max int) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}
