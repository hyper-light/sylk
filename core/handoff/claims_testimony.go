package handoff

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/adalundhe/sylk/core/claims"
)

// BoardProvider resolves the claims board for the active session.
// Set on the bridge via SetBoardProvider.
type BoardProvider func() *claims.ClaimsBoard

// handoffPostClaim posts a handoff claim to the session board.
// Best-effort — board unavailability does not fail the handoff.
func handoffPostClaim(bp BoardProvider, ctx context.Context, action claims.Action, claim claims.Claim) {
	if bp == nil {
		return
	}
	board := bp()
	if board == nil {
		return
	}
	if err := board.PostAction(ctx, action, []claims.Claim{claim}); err != nil {
		slog.Error("handoff_post_claim_failed", "error", err.Error())
		board.RecordNotificationError("handoff post claim: " + err.Error())
	}
}

// handoffSubmitTestament submits a handoff testament to the session board.
func handoffSubmitTestament(bp BoardProvider, ctx context.Context, testament claims.Testament) {
	if bp == nil {
		return
	}
	board := bp()
	if board == nil {
		return
	}
	action := claims.Action{
		AgentID: "handoff_bridge",
		Type:    claims.ActionTypeTestament,
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Error("handoff_submit_testament_failed", "error", err.Error())
		board.RecordNotificationError("handoff testament: " + err.Error())
	}
}

// handoffClaim builds a claim from the handoff bridge. Tagged with
// ActionTypeHandoff so downstream consumers — left-panel filter, audit
// readers, Forest outcome classifier — can distinguish a handoff from
// ordinary task work. Mistagging this as ActionTypeTask hides cycle
// ownership transfers behind plain task semantics.
func handoffClaim(title, description, agentID string, scope []claims.ClaimScopeEntry, validations []*claims.Validation) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope:       scope,
		ActionType:  claims.ActionTypeHandoff,
		Relations: []claims.Relation{
			{Related: "handoff_bridge", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: agentID, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}
}

// handoffTestament builds a testament from the handoff bridge.
func handoffTestament(sessionID, agentID, summary, confidence string, artifacts []*claims.Artifact) claims.Testament {
	return claims.Testament{
		AgentID:    agentID,
		SessionID:  sessionID,
		Summary:    summary,
		Confidence: confidence,
		Relations: []claims.Relation{
			{Related: agentID, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
		Artifacts: artifacts,
	}
}

// handoffArtifact builds a handoff artifact.
func handoffArtifact(sessionID, agentID, kind, reference string) *claims.Artifact {
	return &claims.Artifact{
		AgentID:   agentID,
		SessionID: sessionID,
		Kind:      kind,
		Reference: reference,
	}
}

// handoffJSONArtifact builds a JSON-serialized artifact.
func handoffJSONArtifact(sessionID, agentID, kind string, value any) *claims.Artifact {
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	return handoffArtifact(sessionID, agentID, kind, string(ref))
}

// handoffValidation builds a pending validation.
func handoffValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	return &claims.Validation{
		Type:        vtype,
		Required:    required,
		Description: description,
		QualityBar:  qualityBar,
		Status:      claims.ValidationStatusPending,
	}
}

// handoffFailedValidation builds an already-failed validation.
func handoffFailedValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	v := handoffValidation(vtype, required, description, qualityBar)
	v.Status = claims.ValidationStatusFailed
	return v
}
