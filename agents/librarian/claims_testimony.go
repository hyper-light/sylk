package librarian

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

// librarianBoard resolves the claims board for the librarian's session.
func (l *Librarian) librarianBoard() *claims.ClaimsBoard {
	return claims.DefaultSessionBoardRegistry().Lookup(l.config.SessionID)
}

// librarianPostClaim posts a claim async via scope. Best-effort.
// Registers a just-in-time response Expectation on the librarian's
// inbox after a successful PostAction (CLAIMS.md §5).
func (l *Librarian) librarianPostClaim(ctx context.Context, action claims.Action, claim claims.Claim) {
	board := l.librarianBoard()
	if board == nil {
		return
	}
	posted := []claims.Claim{claim}
	if l.scope != nil {
		if err := l.scope.Go("librarian_post_claim", 5*time.Second, func(gctx context.Context) error {
			if err := board.PostAction(gctx, action, posted); err != nil {
				return err
			}
			claims.RegisterPostActionExpectations(l.claimsInbox, action, posted)
			return nil
		}); err != nil {
			slog.Error("librarian_post_claim_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("librarian post claim dispatch: " + err.Error())
		}
		return
	}
	if err := board.PostAction(ctx, action, posted); err != nil {
		slog.Error("librarian_post_claim_failed", "error", err.Error())
		board.RecordNotificationError("librarian post claim: " + err.Error())
		return
	}
	claims.RegisterPostActionExpectations(l.claimsInbox, action, posted)
}

// librarianSubmitTestament submits a testament async via scope. Best-effort.
func (l *Librarian) librarianSubmitTestament(ctx context.Context, testament claims.Testament) {
	board := l.librarianBoard()
	if board == nil {
		return
	}
	action := claims.Action{AgentID: "librarian", Type: claims.ActionTypeTestament}
	if l.scope != nil {
		if err := l.scope.Go("librarian_submit_testament", 5*time.Second, func(gctx context.Context) error {
			return board.SubmitTestaments(gctx, action, []claims.Testament{testament})
		}); err != nil {
			slog.Error("librarian_submit_testament_dispatch_failed", "error", err.Error())
			board.RecordNotificationError("librarian testament dispatch: " + err.Error())
		}
		return
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Error("librarian_submit_testament_failed", "error", err.Error())
		board.RecordNotificationError("librarian testament: " + err.Error())
	}
}

// librarianClaim builds a claim issued by the librarian.
func librarianClaim(title, description string, scope []claims.ClaimScopeEntry, actionType claims.ActionType, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title: title, Description: description, Scope: scope,
		ActionType: actionType,
		Relations: []claims.Relation{
			{Related: "librarian", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "librarian", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// librarianConsultClaim builds a consultation claim against a peer.
func librarianConsultClaim(title, description, target string, scope []claims.ClaimScopeEntry, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title: title, Description: description, Scope: scope,
		ActionType: claims.ActionTypeConsultation,
		Relations: []claims.Relation{
			{Related: "librarian", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: target, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// librarianTestament builds a testament issued by the librarian.
func (l *Librarian) librarianTestament(summary, confidence string, artifacts []*claims.Artifact) claims.Testament {
	return claims.Testament{
		AgentID: "librarian", SessionID: l.config.SessionID,
		Summary: summary, Confidence: confidence,
		Relations: []claims.Relation{
			{Related: "librarian", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
		Artifacts: artifacts,
	}
}

// librarianTestamentWithSubject builds a testament with a subject relation.
func (l *Librarian) librarianTestamentWithSubject(summary, confidence, subject string, artifacts []*claims.Artifact) claims.Testament {
	return claims.Testament{
		AgentID: "librarian", SessionID: l.config.SessionID,
		Summary: summary, Confidence: confidence,
		Relations: []claims.Relation{
			{Related: "librarian", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: subject, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Artifacts: artifacts,
	}
}

// librarianArtifact builds a single artifact.
func (l *Librarian) librarianArtifact(kind, reference string) *claims.Artifact {
	return &claims.Artifact{
		AgentID: "librarian", SessionID: l.config.SessionID,
		Kind: kind, Reference: reference,
	}
}

// librarianJSONArtifact builds a JSON-serialized artifact.
func (l *Librarian) librarianJSONArtifact(kind string, value any) *claims.Artifact {
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	return l.librarianArtifact(kind, string(ref))
}

// librarianValidation builds a pending validation.
func librarianValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	return &claims.Validation{
		Type: vtype, Required: required,
		Description: description, QualityBar: qualityBar,
		Status: claims.ValidationStatusPending,
	}
}

// librarianScope adapts *concurrency.GoroutineScope to claims.ScopeProvider.
func (l *Librarian) librarianScope() claims.ScopeProvider {
	if l.scope == nil {
		return nil
	}
	return &librarianScopeAdapter{scope: l.scope}
}

type librarianScopeAdapter struct {
	scope *concurrency.GoroutineScope
}

func (a *librarianScopeAdapter) Go(desc string, timeout time.Duration, fn func(context.Context) error) error {
	return a.scope.Go(desc, timeout, concurrency.WorkFunc(fn))
}

func (l *Librarian) processClaimsEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil {
		return nil
	}
	p := l.getProvider()
	if p == nil {
		return fmt.Errorf("librarian: LLM provider not configured")
	}

	acc := shared.NewClaimsEntryAccumulator("librarian", l.config.SessionID, entry)
	acc.WithBoard(l.librarianBoard())
	defer acc.Flush(ctx, l.librarianBoard(), l.librarianScope())
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

	if entry.Node.Claim != nil {
		shared.RecordAgentState(ctx, l.librarianBoard(), entry.Node.Claim.ID,
			"Acknowledging request", shared.AgentStateReasoning, nil)
	}

	userMessage := shared.ComposeClaimsEntryPrompt(entry)
	board := l.librarianBoard()
	userMessage = claims.PrependBoardPreamble(userMessage, board, "librarian")
	l.prepareSkillsForInput(userMessage)

	systemPrompt := l.config.SystemPrompt
	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: userMessage}},
		Tools:        l.buildToolDefinitions(),
		Model:        l.CurrentModel(),
		MaxTokens:    l.config.MaxTokens,
	}
	l.applyConversationRuntimeProfile(req, l.config.SessionID)

	ledger := l.steering.Create(entry.Delta.DeltaKey(), l.id, l.config.SessionID, nil, nil)
	defer l.steering.Close(entry.Delta.DeltaKey(), ctx.Err() != nil)

	result, err := shared.ExecuteTurnLoop(ctx, ledger, req, func() (string, error) {
		return l.executeToolLoop(ctx, req, ledger)
	})
	if err != nil {
		slog.Error("librarian_claims_entry_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		acc.Record("error", err.Error())
		return err
	}
	acc.Record("result_length", fmt.Sprintf("%d", len(result)))
	acc.Note("Claims entry processed successfully")
	return nil
}

// truncateLibrarian truncates a string for claim titles.
func truncateLibrarian(s string, max int) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}
