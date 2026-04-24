package scribe

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

// scribeBoard resolves the claims board for the scribe's session.
func (s *Scribe) scribeBoard() *claims.ClaimsBoard {
	return claims.DefaultSessionBoardRegistry().Lookup(s.sessionID)
}

// scribeAgentID returns the scribe's claims identity.
func (s *Scribe) scribeAgentID() string {
	return "scribe-" + s.parentAgentType
}

// scribePostClaim posts an action with a claim to the session board.
// Async via scope when available. Best-effort — board unavailability
// does not fail the caller.
func (s *Scribe) scribePostClaim(ctx context.Context, action claims.Action, claim claims.Claim) {
	board := s.scribeBoard()
	if board == nil {
		return
	}
	agentID := s.scribeAgentID()
	if s.scope != nil {
		if err := s.scope.Go("scribe_post_claim", 5*time.Second, func(gctx context.Context) error {
			return board.PostAction(gctx, action, []claims.Claim{claim})
		}); err != nil {
			slog.Error("scribe_post_claim_dispatch_failed", "agent", agentID, "error", err.Error())
			board.RecordNotificationError("scribe post claim dispatch: " + err.Error())
		}
		return
	}
	if err := board.PostAction(ctx, action, []claims.Claim{claim}); err != nil {
		slog.Error("scribe_post_claim_failed", "agent", agentID, "error", err.Error())
		board.RecordNotificationError("scribe post claim: " + err.Error())
	}
}

// scribeSubmitTestament submits a testament to the session board.
// Async via scope when available.
func (s *Scribe) scribeSubmitTestament(ctx context.Context, testament claims.Testament) {
	board := s.scribeBoard()
	if board == nil {
		return
	}
	action := claims.Action{
		AgentID: s.scribeAgentID(),
		Type:    claims.ActionTypeTestament,
	}
	agentID := s.scribeAgentID()
	if s.scope != nil {
		if err := s.scope.Go("scribe_submit_testament", 5*time.Second, func(gctx context.Context) error {
			return board.SubmitTestaments(gctx, action, []claims.Testament{testament})
		}); err != nil {
			slog.Error("scribe_submit_testament_dispatch_failed", "agent", agentID, "error", err.Error())
			board.RecordNotificationError("scribe testament dispatch: " + err.Error())
		}
		return
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Error("scribe_submit_testament_failed", "agent", agentID, "error", err.Error())
		board.RecordNotificationError("scribe testament: " + err.Error())
	}
}

// scribeClaimAction builds an Action for the scribe posting claims.
func (s *Scribe) scribeClaimAction(actionType claims.ActionType) claims.Action {
	return claims.Action{
		AgentID: s.scribeAgentID(),
		Type:    actionType,
	}
}

// scribeClaim builds a claim issued by the scribe.
func (s *Scribe) scribeClaim(title, description string, scope []claims.ClaimScopeEntry, actionType claims.ActionType, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  actionType,
		Relations: []claims.Relation{
			{Related: s.scribeAgentID(), RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: s.parentAgentType, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// scribeTestament builds a testament issued by the scribe.
func (s *Scribe) scribeTestament(summary, confidence string, artifacts []*claims.Artifact) claims.Testament {
	return claims.Testament{
		AgentID:    s.scribeAgentID(),
		SessionID:  s.sessionID,
		Summary:    summary,
		Confidence: confidence,
		Relations: []claims.Relation{
			{Related: s.scribeAgentID(), RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: s.parentAgentType, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Artifacts: artifacts,
	}
}

// scribeArtifact builds a single artifact for the scribe.
func (s *Scribe) scribeArtifact(kind, reference string) *claims.Artifact {
	return &claims.Artifact{
		AgentID:   s.scribeAgentID(),
		SessionID: s.sessionID,
		Kind:      kind,
		Reference: reference,
	}
}

// scribeJSONArtifact builds an artifact with JSON-serialized reference.
func (s *Scribe) scribeJSONArtifact(kind string, value any) *claims.Artifact {
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	return s.scribeArtifact(kind, string(ref))
}

func (s *Scribe) processClaimsEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil {
		return nil
	}
	if s.provider == nil {
		return fmt.Errorf("scribe: LLM provider not configured")
	}

	acc := claims.NewTestamentAccumulator(s.scribeAgentID(), s.sessionID)
	defer acc.Flush(ctx, s.scribeBoard(), nil)
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

	userMessage := shared.ComposeClaimsEntryPrompt(entry)
	board := s.scribeBoard()
	userMessage = claims.PrependBoardPreamble(userMessage, board, s.scribeAgentID())

	history := []providers.Message{
		{Role: providers.RoleUser, Content: userMessage},
	}
	feed := shared.ScribeFeed{
		UserRequest:   userMessage,
		AgentResponse: "",
	}

	result, err := s.generateCommentary(ctx, history, feed)
	if err != nil {
		slog.Error("scribe_claims_entry_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		acc.Record("error", err.Error())
		return err
	}
	acc.Record("result_length", fmt.Sprintf("%d", len(result)))
	acc.Note("Claims entry processed successfully")
	return nil
}

// scribeValidation builds a pending validation.
func scribeValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	return &claims.Validation{
		Type:        vtype,
		Required:    required,
		Description: description,
		QualityBar:  qualityBar,
		Status:      claims.ValidationStatusPending,
	}
}
