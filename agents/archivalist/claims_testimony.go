package archivalist

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

// SetScope injects the goroutine scope for async claims dispatch.
func (a *Archivalist) SetScope(scope *concurrency.GoroutineScope) {
	a.scope = scope
}

// archivalistBoard resolves the claims board for the archivalist's session.
func (a *Archivalist) archivalistBoard() *claims.ClaimsBoard {
	return claims.DefaultSessionBoardRegistry().Lookup(a.defaultSessionID)
}

// archivalistScope adapts *concurrency.GoroutineScope to claims.ScopeProvider.
func (a *Archivalist) archivalistScope() claims.ScopeProvider {
	if a.scope == nil {
		return nil
	}
	return &archivalistScopeAdapter{scope: a.scope}
}

type archivalistScopeAdapter struct {
	scope *concurrency.GoroutineScope
}

func (ad *archivalistScopeAdapter) Go(desc string, timeout time.Duration, fn func(context.Context) error) error {
	return ad.scope.Go(desc, timeout, concurrency.WorkFunc(fn))
}

// archivalistSubmitTestament submits a testament async via scope. Best-effort.
func (a *Archivalist) archivalistSubmitTestament(ctx context.Context, testament claims.Testament) {
	board := a.archivalistBoard()
	if board == nil {
		return
	}
	action := claims.Action{AgentID: "archivalist", Type: claims.ActionTypeTestament}
	if a.scope != nil {
		if err := a.scope.Go("archivalist_submit_testament", 5*time.Second, func(gctx context.Context) error {
			return board.SubmitTestaments(gctx, action, []claims.Testament{testament})
		}); err != nil {
			slog.Error("archivalist_submit_testament_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("archivalist testament dispatch: " + err.Error())
		}
		return
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Error("archivalist_submit_testament_failed", "error", err.Error())
		board.RecordNotificationError("archivalist testament: " + err.Error())
	}
}

// archivalistTestament builds a testament issued by the archivalist.
func (a *Archivalist) archivalistTestament(summary, confidence string, artifacts []*claims.Artifact) claims.Testament {
	return claims.Testament{
		AgentID: "archivalist", SessionID: a.defaultSessionID,
		Summary: summary, Confidence: confidence,
		Relations: []claims.Relation{
			{Related: "archivalist", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
		Artifacts: artifacts,
	}
}

// archivalistArtifact builds a single artifact.
func (a *Archivalist) archivalistArtifact(kind, reference string) *claims.Artifact {
	return &claims.Artifact{
		AgentID: "archivalist", SessionID: a.defaultSessionID,
		Kind: kind, Reference: reference,
	}
}

// archivalistJSONArtifact builds a JSON-serialized artifact.
func (a *Archivalist) archivalistJSONArtifact(kind string, value any) *claims.Artifact {
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	return a.archivalistArtifact(kind, string(ref))
}

// archivalistPostClaim posts a claim async via scope. Best-effort.
func (a *Archivalist) archivalistPostClaim(ctx context.Context, action claims.Action, claim claims.Claim) {
	board := a.archivalistBoard()
	if board == nil {
		return
	}
	if a.scope != nil {
		if err := a.scope.Go("archivalist_post_claim", 5*time.Second, func(gctx context.Context) error {
			return board.PostAction(gctx, action, []claims.Claim{claim})
		}); err != nil {
			slog.Error("archivalist_post_claim_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("archivalist post claim dispatch: " + err.Error())
		}
		return
	}
	if err := board.PostAction(ctx, action, []claims.Claim{claim}); err != nil {
		slog.Error("archivalist_post_claim_failed", "error", err.Error())
		board.RecordNotificationError("archivalist post claim: " + err.Error())
	}
}

// archivalistConsultClaim builds a consultation claim against a peer.
func archivalistConsultClaim(title, description, target string, scope []claims.ClaimScopeEntry, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  claims.ActionTypeConsultation,
		Relations: []claims.Relation{
			{Related: "archivalist", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: target, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// archivalistValidation builds a pending validation.
func archivalistValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	return &claims.Validation{
		Type: vtype, Required: required,
		Description: description, QualityBar: qualityBar,
		Status: claims.ValidationStatusPending,
	}
}

func (a *Archivalist) processClaimsEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil {
		return nil
	}
	p := a.getProvider()
	if p == nil {
		return fmt.Errorf("archivalist: LLM provider not configured")
	}

	acc := claims.NewTestamentAccumulator("archivalist", a.defaultSessionID)
	defer acc.Flush(ctx, a.archivalistBoard(), a.archivalistScope())
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

	userMessage := shared.ComposeClaimsEntryPrompt(entry)
	board := a.archivalistBoard()
	userMessage = claims.PrependBoardPreamble(userMessage, board, "archivalist")
	a.prepareSkillsForInput(userMessage)

	systemPrompt := a.config.SystemPrompt
	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: userMessage}},
		Tools:        a.buildToolDefinitions(),
		Model:        a.CurrentModel(),
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyConversationRuntimeProfile(req)

	ledger := a.steering.Create(entry.Delta.DeltaKey(), a.id, a.defaultSessionID, nil, nil)
	defer a.steering.Close(entry.Delta.DeltaKey(), ctx.Err() != nil)

	result, err := shared.ExecuteTurnLoop(ledger, req, func() (string, error) {
		return a.executeToolLoop(ctx, req, ledger)
	})
	if err != nil {
		slog.Error("archivalist_claims_entry_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		acc.Record("error", err.Error())
		return err
	}
	acc.Record("result_length", fmt.Sprintf("%d", len(result)))
	acc.Note("Claims entry processed successfully")
	return nil
}

// truncateArchivalist truncates a string for claim titles.
func truncateArchivalist(s string, max int) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}
