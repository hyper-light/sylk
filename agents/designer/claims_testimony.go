package designer

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

// designerBoard returns the claims board (may be nil).
func (d *Designer) designerBoard() *claims.ClaimsBoard {
	return d.claimsBoard
}

// designerScope adapts *concurrency.GoroutineScope to claims.ScopeProvider.
func (d *Designer) designerScope() claims.ScopeProvider {
	if d.scope == nil {
		return nil
	}
	return &designerScopeAdapter{scope: d.scope}
}

type designerScopeAdapter struct {
	scope *concurrency.GoroutineScope
}

func (a *designerScopeAdapter) Go(desc string, timeout time.Duration, fn func(context.Context) error) error {
	return a.scope.Go(desc, timeout, concurrency.WorkFunc(fn))
}

// designerPostClaim posts a claim async via scope. Best-effort.
// Registers a just-in-time response Expectation on the designer's
// inbox after a successful PostAction (CLAIMS.md §5).
func (d *Designer) designerPostClaim(ctx context.Context, action claims.Action, claim claims.Claim) {
	board := d.designerBoard()
	if board == nil {
		return
	}
	posted := []claims.Claim{claim}
	if d.scope != nil {
		if err := d.scope.Go("designer_post_claim", 5*time.Second, func(gctx context.Context) error {
			if err := board.PostAction(gctx, action, posted); err != nil {
				return err
			}
			claims.RegisterPostActionExpectations(d.claimsInbox, action, posted)
			return nil
		}); err != nil {
			slog.Error("designer_post_claim_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("designer post claim dispatch: " + err.Error())
		}
		return
	}
	if err := board.PostAction(ctx, action, posted); err != nil {
		slog.Error("designer_post_claim_failed", "error", err.Error())
		board.RecordNotificationError("designer post claim: " + err.Error())
		return
	}
	claims.RegisterPostActionExpectations(d.claimsInbox, action, posted)
}

// designerSubmitTestament submits a testament async via scope. Best-effort.
func (d *Designer) designerSubmitTestament(ctx context.Context, testament claims.Testament) {
	board := d.designerBoard()
	if board == nil {
		return
	}
	action := claims.Action{AgentID: "designer", Type: claims.ActionTypeTestament}
	if d.scope != nil {
		if err := d.scope.Go("designer_submit_testament", 5*time.Second, func(gctx context.Context) error {
			return board.SubmitTestaments(gctx, action, []claims.Testament{testament})
		}); err != nil {
			slog.Error("designer_submit_testament_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("designer testament dispatch: " + err.Error())
		}
		return
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Error("designer_submit_testament_failed", "error", err.Error())
		board.RecordNotificationError("designer testament: " + err.Error())
	}
}

// designerClaim builds a claim issued by the designer against itself.
func designerClaim(title, description string, scope []claims.ClaimScopeEntry, actionType claims.ActionType, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  actionType,
		Relations: []claims.Relation{
			{Related: "designer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "designer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// designerConsultClaim builds a consultation claim against a peer.
func designerConsultClaim(title, description, target string, scope []claims.ClaimScopeEntry, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  claims.ActionTypeConsultation,
		Relations: []claims.Relation{
			{Related: "designer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: target, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// designerTestament builds a testament issued by the designer.
func (d *Designer) designerTestament(summary, confidence string, artifacts []*claims.Artifact) claims.Testament {
	return claims.Testament{
		AgentID: "designer", SessionID: d.config.SessionID,
		Summary: summary, Confidence: confidence,
		Relations: []claims.Relation{
			{Related: "designer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
		Artifacts: artifacts,
	}
}

// designerArtifact builds a single artifact.
func (d *Designer) designerArtifact(kind, reference string) *claims.Artifact {
	return &claims.Artifact{
		AgentID: "designer", SessionID: d.config.SessionID,
		Kind: kind, Reference: reference,
	}
}

// designerJSONArtifact builds a JSON-serialized artifact.
func (d *Designer) designerJSONArtifact(kind string, value any) *claims.Artifact {
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	return d.designerArtifact(kind, string(ref))
}

// designerValidation builds a pending validation.
func designerValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	return &claims.Validation{
		Type: vtype, Required: required,
		Description: description, QualityBar: qualityBar,
		Status: claims.ValidationStatusPending,
	}
}

func (d *Designer) processClaimsEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil {
		return nil
	}
	claimID := ""
	claimTitle := ""
	if entry.Node.Claim != nil {
		claimID = entry.Node.Claim.ID
		claimTitle = entry.Node.Claim.Title
	}
	slog.Info("designer_process_claims_entry_begin",
		"designer_id", d.id,
		"session_id", d.config.SessionID,
		"delta_kind", entry.Delta.DeltaKind(),
		"delta_key", entry.Delta.DeltaKey(),
		"claim_id", claimID,
		"claim_title", claimTitle,
		"board_present", d.designerBoard() != nil,
	)
	p := d.getProvider()
	if p == nil {
		slog.Error("designer_process_claims_entry_no_provider", "designer_id", d.id)
		return fmt.Errorf("designer: LLM provider not configured")
	}

	acc := shared.NewClaimsEntryAccumulator("designer", d.config.SessionID, entry)
	acc.WithBoard(d.designerBoard())
	defer acc.Flush(ctx, d.designerBoard(), d.designerScope())
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

	if entry.Node.Claim != nil {
		shared.RecordAgentState(ctx, d.designerBoard(), entry.Node.Claim.ID,
			"Acknowledging request", shared.AgentStateReasoning, nil)
	}

	userMessage := shared.ComposeClaimsEntryPrompt(entry)
	userMessage = claims.PrependBoardPreamble(userMessage, d.claimsBoard, "designer")
	d.prepareSkillsForInput(userMessage)
	surface := d.toolRuntime()

	systemPrompt := d.systemPromptForContract(nil)
	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: userMessage}},
		Tools:        d.buildToolDefinitionsWithSurface(surface),
		Model:        d.config.DesignerConfig.Model,
		MaxTokens:    d.config.DesignerConfig.MaxTokens,
	}
	d.applyDesignRuntimeProfile(req)

	ledger := d.steering.Create(entry.Delta.DeltaKey(), d.id, d.config.SessionID, nil, nil)
	defer d.steering.Close(entry.Delta.DeltaKey(), ctx.Err() != nil)

	ctx = shared.WithContinuationStore(ctx, d.continuationStore)
	ctx = shared.WithTurnContext(ctx, &shared.TurnContext{
		Request:       req,
		CorrelationID: designerLedgerCorrelation(ledger, d.config.SessionID),
		AgentID:       d.id,
		SessionID:     d.config.SessionID,
	})

	result, err := shared.ExecuteTurnLoop(ctx, ledger, req, func() (string, error) {
		return d.executeToolLoopWithSurface(ctx, req, ledger, surface)
	})
	if err != nil {
		if shared.IsConsultYielded(err) {
			slog.Info("designer_claims_entry_yielded", "delta_key", entry.Delta.DeltaKey())
			return nil
		}
		slog.Error("designer_claims_entry_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		acc.Record("error", err.Error())
		return err
	}
	acc.Record("result_length", fmt.Sprintf("%d", len(result)))
	acc.Note("Claims entry processed successfully")
	return nil
}

// truncateDesigner truncates a string for claim titles.
func truncateDesigner(s string, max int) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}
