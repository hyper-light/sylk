package guardian

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
)

// guardianBoard resolves the claims board for the guardian's active session.
// Returns nil when no session is active (pre-request, tests).
func (g *Guardian) guardianBoard() *claims.ClaimsBoard {
	return claims.DefaultSessionBoardRegistry().Lookup(g.activeSessionID)
}

// guardianPostClaim posts an action with a single claim to the session board.
// Best-effort: board unavailability does not fail the caller. Returns the
// posted claim ID (empty if board unavailable or post failed).
func (g *Guardian) guardianPostClaim(ctx context.Context, action claims.Action, claim claims.Claim) string {
	board := g.guardianBoard()
	if board == nil {
		return ""
	}
	if err := board.PostAction(ctx, action, []claims.Claim{claim}); err != nil {
		board.RecordNotificationError("guardian post claim: " + err.Error())
		return ""
	}
	return claim.ID
}

// guardianSubmitTestament submits a testament action to the session board.
// Best-effort: board unavailability does not fail the caller.
func (g *Guardian) guardianSubmitTestament(ctx context.Context, action claims.Action, testament claims.Testament) {
	board := g.guardianBoard()
	if board == nil {
		return
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		board.RecordNotificationError("guardian submit testament: " + err.Error())
	}
}

// guardianClaimAction builds an Action for the guardian issuing claims.
func guardianClaimAction(actionType claims.ActionType) claims.Action {
	return claims.Action{
		AgentID: "guardian",
		Type:    actionType,
	}
}

// guardianTestamentAction builds an Action for the guardian submitting testaments.
func guardianTestamentAction() claims.Action {
	return claims.Action{
		AgentID: "guardian",
		Type:    claims.ActionTypeTestament,
	}
}

// guardianClaim builds a claim issued by the guardian against itself or a peer.
func guardianClaim(title, description string, subject string, scope []claims.ClaimScopeEntry, actionType claims.ActionType, validations []*claims.Validation) claims.Claim {
	rels := []claims.Relation{
		{Related: "guardian", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
	}
	if subject != "" {
		rels = append(rels, claims.Relation{
			Related: subject, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject,
		})
	}
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  actionType,
		Relations:   rels,
		Validations: validations,
	}
}

// guardianExternalClaim builds a claim issued by an external agent against the guardian.
func guardianExternalClaim(title, description string, issuer string, scope []claims.ClaimScopeEntry, actionType claims.ActionType, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  actionType,
		Relations: []claims.Relation{
			{Related: issuer, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "guardian", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// guardianTestament builds a testament issued by the guardian.
func guardianTestament(sessionID, summary, confidence string, subject string, artifacts []*claims.Artifact) claims.Testament {
	rels := []claims.Relation{
		{Related: "guardian", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
	}
	if subject != "" {
		rels = append(rels, claims.Relation{
			Related: subject, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject,
		})
	}
	return claims.Testament{
		AgentID:    "guardian",
		SessionID:  sessionID,
		Summary:    summary,
		Confidence: confidence,
		Relations:  rels,
		Artifacts:  artifacts,
	}
}

// guardianArtifact builds a single artifact for the guardian.
func guardianArtifact(sessionID, kind, reference string) *claims.Artifact {
	return &claims.Artifact{
		AgentID:   "guardian",
		SessionID: sessionID,
		Kind:      kind,
		Reference: reference,
	}
}

// guardianJSONArtifact builds an artifact with a JSON-serialized reference.
// Falls back to the error string if marshaling fails.
func guardianJSONArtifact(sessionID, kind string, value any) *claims.Artifact {
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	return guardianArtifact(sessionID, kind, string(ref))
}

// guardianValidation builds a single validation entry.
func guardianValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	return &claims.Validation{
		Type:        vtype,
		Required:    required,
		Description: description,
		QualityBar:  qualityBar,
		Status:      claims.ValidationStatusPending,
	}
}

// guardianPassedValidation builds a validation that has already been evaluated as passed.
func guardianPassedValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	v := guardianValidation(vtype, required, description, qualityBar)
	v.Status = claims.ValidationStatusPassed
	return v
}

// guardianFailedValidation builds a validation that has already been evaluated as failed.
func guardianFailedValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	v := guardianValidation(vtype, required, description, qualityBar)
	v.Status = claims.ValidationStatusFailed
	return v
}

func (g *Guardian) processClaimsEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil {
		return nil
	}
	p := g.getProvider()
	if p == nil {
		return fmt.Errorf("guardian: LLM provider not configured")
	}

	acc := claims.NewTestamentAccumulator("guardian", g.activeSessionID)
	defer acc.Flush(ctx, g.guardianBoard(), nil)
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

	userMessage := shared.ComposeClaimsEntryPrompt(entry)
	board := g.guardianBoard()
	userMessage = claims.PrependBoardPreamble(userMessage, board, "guardian")
	g.prepareSkillsForInput(userMessage, IntentAssess)

	systemPrompt := g.buildSystemPrompt(IntentAssess)
	req := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: userMessage}},
		Tools:        g.buildToolDefinitions(),
		MaxTokens:    DefaultMaxOutputTokens,
	}
	g.applyConversationRuntimeProfile(req, IntentAssess, g.activeSessionID)

	ledger := g.steering.Create(entry.Delta.DeltaKey(), g.id, g.activeSessionID, nil, nil)
	defer g.steering.Close(entry.Delta.DeltaKey(), ctx.Err() != nil)

	result, err := shared.ExecuteTurnLoop(ledger, req, func() (string, error) {
		content, _, loopErr := g.executeToolLoop(ctx, req, "claims_entry", nil, ledger)
		return content, loopErr
	})
	if err != nil {
		slog.Error("guardian_claims_entry_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		acc.Record("error", err.Error())
		return err
	}
	acc.Record("result_length", fmt.Sprintf("%d", len(result)))
	acc.Note("Claims entry processed successfully")
	return nil
}

// truncateCommandForClaim truncates a command string for claim titles.
func truncateCommandForClaim(cmd string, max int) string {
	trimmed := strings.TrimSpace(cmd)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}
