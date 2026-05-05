package activation

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/container/pod"
	"github.com/google/uuid"
)

const activationClaimsAgent = "system:activation"

// ────────────────────────────────────────────────────────────────────
// Activation claims (async, best-effort, session board)
// ────────────────────────────────────────────────────────────────────

// postActivationSuccess posts an activation claim and testament to the
// session board. Async via scope.Go — never blocks the activation
// path. The scope-provided ctx is threaded into board.PostAction and
// board.SubmitTestaments so a SignalShutdown immediately aborts these
// workers instead of leaving them blocked on board IO with a
// never-cancellable context.Background.
func (ac *ActivationController) postActivationSuccess(agentType string, c *container.Container) {
	board := ac.loadBoard()
	if board == nil {
		return
	}
	ac.runAsync("activation_claim_"+agentType, func(ctx context.Context) error {
		claimID := postActivationClaim(ctx, board, agentType)
		if claimID == "" {
			return nil
		}
		submitActivationTestament(ctx, board, agentType, claimID, c)
		return nil
	})
}

// postActivationError posts an error testament for a failed activation.
// Async via scope.Go — never blocks the caller.
func (ac *ActivationController) postActivationError(agentType string, err error) {
	board := ac.loadBoard()
	if board == nil {
		return
	}
	ac.runAsync("activation_error_"+agentType, func(ctx context.Context) error {
		claimID := postActivationClaim(ctx, board, agentType)
		if claimID == "" {
			return nil
		}
		submitActivationErrorTestament(ctx, board, agentType, claimID, err)
		return nil
	})
}

func postActivationClaim(ctx context.Context, board *claims.ClaimsBoard, agentType string) string {
	if board == nil {
		return ""
	}
	action := claims.Action{
		AgentID: activationClaimsAgent,
		Type:    claims.ActionTypeActivation,
	}
	claim := claims.Claim{
		Title:       fmt.Sprintf("Activate %s", agentType),
		Description: fmt.Sprintf("Activate agent type %s for the current session", agentType),
		ActionType:  claims.ActionTypeActivation,
		Relations: []claims.Relation{
			{Related: activationClaimsAgent, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: agentType, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: []*claims.Validation{{
			Type:        claims.ValidationTypeReceipt,
			Required:    true,
			Description: fmt.Sprintf("Agent %s activation acknowledged", agentType),
			Status:      claims.ValidationStatusPending,
		}},
	}
	posted := []claims.Claim{claim}
	if err := board.PostAction(ctx, action, posted); err != nil {
		slog.Warn("activation_claim_post_failed", "agent_type", agentType, "error", err.Error())
		return ""
	}
	if len(posted) > 0 {
		return posted[0].ID
	}
	return ""
}

func submitActivationTestament(ctx context.Context, board *claims.ClaimsBoard, agentType, claimID string, c *container.Container) {
	if board == nil || claimID == "" {
		return
	}
	agentID := ""
	ready := false
	if c != nil {
		agentID = string(c.ID())
		ready = c.IsReady()
	}
	action := claims.Action{AgentID: activationClaimsAgent, Type: claims.ActionTypeTestament}
	testament := claims.Testament{
		AgentID:    activationClaimsAgent,
		Summary:    fmt.Sprintf("Agent %s activated (ready=%t)", agentType, ready),
		Confidence: "high",
		Artifacts: []*claims.Artifact{
			{Kind: claims.ArtifactKindAgentID, Reference: agentID, AgentID: activationClaimsAgent},
			{Kind: claims.ArtifactKindReadiness, Reference: fmt.Sprintf("%t", ready), AgentID: activationClaimsAgent},
		},
		Relations: lifecycleClaimRelation(claimID),
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Warn("activation_testament_failed", "agent_type", agentType, "error", err.Error())
	}
}

func submitActivationErrorTestament(ctx context.Context, board *claims.ClaimsBoard, agentType, claimID string, activationErr error) {
	if board == nil || claimID == "" {
		return
	}
	action := claims.Action{AgentID: activationClaimsAgent, Type: claims.ActionTypeTestament}
	testament := claims.Testament{
		AgentID:    activationClaimsAgent,
		Summary:    fmt.Sprintf("Agent %s activation failed: %s", agentType, activationErr.Error()),
		Confidence: "high",
		Artifacts: []*claims.Artifact{
			{Kind: claims.ArtifactKindError, Reference: activationErr.Error(), AgentID: activationClaimsAgent},
		},
		Relations: lifecycleClaimRelation(claimID),
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Warn("activation_error_testament_failed", "agent_type", agentType, "error", err.Error())
	}
}

// ────────────────────────────────────────────────────────────────────
// Shutdown claims (synchronous, ephemeral board)
// ────────────────────────────────────────────────────────────────────

func newShutdownBoard() *claims.ClaimsBoard {
	return claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID: "shutdown-" + uuid.NewString()[:8],
		TaskID:  "shutdown",
	})
}

func postShutdownClaim(ctx context.Context, board *claims.ClaimsBoard, agentType string) string {
	if board == nil {
		return ""
	}
	action := claims.Action{
		AgentID: activationClaimsAgent,
		Type:    claims.ActionTypeShutdown,
	}
	claim := claims.Claim{
		Title:       fmt.Sprintf("Shutdown %s", agentType),
		Description: fmt.Sprintf("Persist state and terminate agent %s", agentType),
		ActionType:  claims.ActionTypeShutdown,
		Relations: []claims.Relation{
			{Related: activationClaimsAgent, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: agentType, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: []*claims.Validation{{
			Type:        claims.ValidationTypeReceipt,
			Required:    true,
			Description: fmt.Sprintf("Agent %s shutdown acknowledged", agentType),
			Status:      claims.ValidationStatusPending,
		}},
	}
	posted := []claims.Claim{claim}
	if err := board.PostAction(ctx, action, posted); err != nil {
		slog.Warn("shutdown_claim_post_failed", "agent_type", agentType, "error", err.Error())
		return ""
	}
	if len(posted) > 0 {
		return posted[0].ID
	}
	return ""
}

func submitShutdownTestament(ctx context.Context, board *claims.ClaimsBoard, agentType, claimID string, tier ActivationTier) {
	if board == nil || claimID == "" {
		return
	}
	action := claims.Action{AgentID: activationClaimsAgent, Type: claims.ActionTypeTestament}
	testament := claims.Testament{
		AgentID:    activationClaimsAgent,
		Summary:    fmt.Sprintf("Agent %s shutdown complete (was tier %s)", agentType, pod.TierString(tier)),
		Confidence: "high",
		Artifacts: []*claims.Artifact{
			{Kind: claims.ArtifactKindShutdownAck, Reference: agentType, AgentID: activationClaimsAgent,
				Metadata: map[string]any{"agent_type": agentType, "final_tier": pod.TierString(tier)}},
		},
		Relations: lifecycleClaimRelation(claimID),
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Warn("shutdown_testament_failed", "agent_type", agentType, "error", err.Error())
	}
}

func submitShutdownError(ctx context.Context, board *claims.ClaimsBoard, agentType, claimID string, shutdownErr error) {
	if board == nil || claimID == "" {
		return
	}
	action := claims.Action{AgentID: activationClaimsAgent, Type: claims.ActionTypeTestament}
	testament := claims.Testament{
		AgentID:    activationClaimsAgent,
		Summary:    fmt.Sprintf("Agent %s shutdown failed: %s", agentType, shutdownErr.Error()),
		Confidence: "high",
		Artifacts: []*claims.Artifact{
			{Kind: claims.ArtifactKindError, Reference: shutdownErr.Error(), AgentID: activationClaimsAgent},
			{Kind: claims.ArtifactKindShutdownAck, Reference: agentType, AgentID: activationClaimsAgent},
		},
		Relations: lifecycleClaimRelation(claimID),
	}
	if err := board.SubmitTestaments(ctx, action, []claims.Testament{testament}); err != nil {
		slog.Warn("shutdown_error_testament_failed", "agent_type", agentType, "error", err.Error())
	}
}

func acceptShutdownClaims(ctx context.Context, board *claims.ClaimsBoard) {
	if board == nil {
		return
	}
	proj := board.Projection()
	if proj == nil {
		return
	}
	for _, c := range proj.Claims {
		for _, v := range c.Validations {
			if v == nil || v.Status != claims.ValidationStatusPending {
				continue
			}
			_ = board.EvaluateValidation(ctx, c.ID, v.ID, claims.StatusChange{
				To:      string(claims.ValidationStatusPassed),
				Reason:  "shutdown testament received",
				AgentID: activationClaimsAgent,
				Changed: time.Now(),
			})
		}
	}
}

// ────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────

// lifecycleClaimRelation builds the Relation slice linking a testament to its claim.
func lifecycleClaimRelation(claimID string) []claims.Relation {
	if claimID == "" {
		return nil
	}
	return []claims.Relation{{
		Related:      claimID,
		RelatedType:  claims.RelatedTypeClaim,
		Relationship: claims.RelationshipClaim,
	}}
}

// loadBoard returns the current session board, or nil.
func (ac *ActivationController) loadBoard() *claims.ClaimsBoard {
	if ac.boardProvider == nil {
		return nil
	}
	return ac.boardProvider()
}

// runAsync dispatches fn via scope.Go. Falls back to synchronous on nil scope.
func (ac *ActivationController) runAsync(desc string, fn func(context.Context) error) {
	if ac.scope == nil {
		if err := fn(context.Background()); err != nil {
			slog.Warn("activation_claims_sync_error", "desc", desc, "error", err.Error())
		}
		return
	}
	if err := ac.scope.Go(desc, 5*time.Second, fn); err != nil {
		slog.Warn("activation_claims_dispatch_failed", "desc", desc, "error", err.Error())
	}
}
