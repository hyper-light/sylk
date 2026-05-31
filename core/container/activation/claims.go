package activation

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/container/pod"
	"github.com/google/uuid"
)

const activationControllerParticipantID = "sys:activation_controller"
const activationClaimsAgent = activationControllerParticipantID

// ────────────────────────────────────────────────────────────────────
// Activation claims (async, best-effort, session board)
// ────────────────────────────────────────────────────────────────────

// postActivationSuccess posts an activation claim and testament to the
// session board through the activation-controller service participant.
// Async via scope.Go — never blocks the activation path.
func (ac *ActivationController) postActivationSuccess(agentType string, c *container.Container, duration time.Duration) {
	board := ac.loadBoard()
	if board == nil {
		activationFileLog().Info("DEBUG: activation_claim_success_no_board", "agent_type", agentType, "duration_ms", duration.Milliseconds())
		return
	}
	record := ac.activationRecord(agentType, c, nil, duration)
	activationFileLog().Info("DEBUG: activation_claim_success_schedule",
		"agent_type", agentType,
		"session_id", activationBoardSessionID(board),
		"operation", record.Operation,
		"tier", record.Tier,
		"ready", record.Ready,
		"duration_ms", duration.Milliseconds())
	ac.runAsync("activation_claim_"+agentType, func(ctx context.Context) error {
		return invokeActivationServiceClaim(ctx, board, claims.ActivationControllerToolActivate, record)
	})
}

// postActivationError posts an error testament for a failed activation.
// Async via scope.Go — never blocks the caller.
func (ac *ActivationController) postActivationError(agentType string, err error, duration time.Duration) {
	board := ac.loadBoard()
	if board == nil {
		activationFileLog().Info("DEBUG: activation_claim_error_no_board", "agent_type", agentType, "duration_ms", duration.Milliseconds(), "error", err)
		return
	}
	record := ac.activationRecord(agentType, nil, err, duration)
	activationFileLog().Info("DEBUG: activation_claim_error_schedule",
		"agent_type", agentType,
		"session_id", activationBoardSessionID(board),
		"operation", record.Operation,
		"tier", record.Tier,
		"ready", record.Ready,
		"duration_ms", duration.Milliseconds(),
		"error", err)
	ac.runAsync("activation_error_"+agentType, func(ctx context.Context) error {
		return invokeActivationServiceClaim(ctx, board, claims.ActivationControllerToolActivate, record)
	})
}

func (ac *ActivationController) postActivationTransition(agentType string, previous, target, final ActivationTier, transitionErr error, duration time.Duration) {
	board := ac.loadBoard()
	if board == nil {
		return
	}
	record := claims.ActivationRecordArtifactData{
		ParticipantID:   strings.TrimSpace(agentType),
		ParticipantType: "agent",
		Operation:       "tier_transition",
		PreviousTier:    pod.TierString(previous),
		TargetTier:      pod.TierString(target),
		Tier:            pod.TierString(final),
		Ready:           final >= TierWarm,
		Duration:        duration,
	}
	if record.Ready {
		record.ReplicaCount = 1
	}
	if transitionErr != nil {
		record.FailureReason = transitionErr.Error()
		record.Ready = false
		record.ReplicaCount = 0
	}
	activationFileLog().Info("DEBUG: activation_claim_transition_schedule",
		"agent_type", agentType,
		"session_id", activationBoardSessionID(board),
		"previous_tier", record.PreviousTier,
		"target_tier", record.TargetTier,
		"final_tier", record.Tier,
		"ready", record.Ready,
		"duration_ms", duration.Milliseconds(),
		"error", transitionErr)
	ac.runAsync("activation_transition_"+agentType, func(ctx context.Context) error {
		return invokeActivationServiceClaim(ctx, board, claims.ActivationControllerToolDeactivate, record)
	})
}

func (ac *ActivationController) postActivationQuery(agentType string, tier ActivationTier) {
	board := ac.loadBoard()
	if board == nil {
		return
	}
	record := claims.ActivationRecordArtifactData{
		ParticipantID:   strings.TrimSpace(agentType),
		ParticipantType: "agent",
		Operation:       "query_tier",
		Tier:            pod.TierString(tier),
		TargetTier:      pod.TierString(tier),
		Ready:           tier >= TierWarm,
	}
	if record.Ready {
		record.ReplicaCount = 1
	}
	activationFileLog().Info("DEBUG: activation_claim_query_schedule",
		"agent_type", agentType,
		"session_id", activationBoardSessionID(board),
		"tier", record.Tier,
		"ready", record.Ready)
	ac.runAsync("activation_query_"+agentType, func(ctx context.Context) error {
		return invokeActivationServiceClaim(ctx, board, claims.ActivationControllerToolQueryTier, record)
	})
}

func (ac *ActivationController) activationRecord(agentType string, c *container.Container, activationErr error, duration time.Duration) claims.ActivationRecordArtifactData {
	tier := ac.currentTierForRecord(agentType)
	record := claims.ActivationRecordArtifactData{
		ParticipantID:   strings.TrimSpace(agentType),
		ParticipantType: "agent",
		Operation:       "activate",
		Tier:            pod.TierString(tier),
		TargetTier:      pod.TierString(TierHot),
		Duration:        duration,
	}
	if c != nil {
		record.ReplicaCount = 1
		record.Ready = true
	}
	if activationErr != nil {
		record.FailureReason = activationErr.Error()
		record.Ready = false
		record.ReplicaCount = 0
	}
	if activationErr == nil && c == nil {
		record.FailureReason = "activated container missing"
	}
	return record
}

func (ac *ActivationController) currentTierForRecord(agentType string) ActivationTier {
	if ac == nil {
		return TierCold
	}
	entry, err := ac.getEntry(agentType)
	if err != nil {
		return TierCold
	}
	return entry.LoadTier()
}

func invokeActivationServiceClaim(ctx context.Context, board *claims.ClaimsBoard, tool string, record claims.ActivationRecordArtifactData) error {
	participant, err := activationControllerParticipant(board)
	if err != nil {
		return err
	}
	start := time.Now()
	activationFileLog().Info("DEBUG: activation_service_claim_start",
		"session_id", activationBoardSessionID(board),
		"tool", tool,
		"participant_id", record.ParticipantID,
		"operation", record.Operation,
		"action_type", claims.ActionTypeActivation)
	result, err := claims.InvokeServiceClaim(ctx, claims.ServiceInvocationOptions{
		Board:        board,
		Handler:      claims.NewActivationControllerService(claims.ActivationControllerServiceConfig{MaxRecords: activationControllerRecordCapacity()}),
		Participant:  participant,
		IssuerID:     activationClaimsAgent,
		SubjectID:    activationControllerParticipantID,
		Title:        fmt.Sprintf("Activation controller %s %s", record.Operation, record.ParticipantID),
		Description:  fmt.Sprintf("Run activation controller operation %s for agent type %s.", record.Operation, record.ParticipantID),
		ActionType:   claims.ActionTypeActivation,
		ExpectedCall: claims.ExpectedToolCall{Tool: tool, Arguments: activationServiceArguments(record)},
		Reason:       "activation controller service claim generated",
	})
	activationFileLog().Info("DEBUG: activation_service_claim_done",
		"session_id", activationBoardSessionID(board),
		"tool", tool,
		"participant_id", record.ParticipantID,
		"operation", record.Operation,
		"claim_id", result.ClaimID,
		"testament_id", result.TestamentID,
		"elapsed_ms", time.Since(start).Milliseconds(),
		"error", err)
	return err
}

func activationControllerParticipant(board *claims.ClaimsBoard) (claims.ParticipantRegistration, error) {
	return claims.NewParticipantRegistration(
		claims.ParticipantCategoryService,
		activationControllerParticipantID,
		map[string]string{"session_id": activationBoardSessionID(board)},
		activationControllerRecordCapacity(),
		len(activationControllerTools()),
		activationControllerTimeout(),
		claims.HandlerDeterminismSideEffect,
		[]claims.ActionType{claims.ActionTypeActivation},
	)
}

func activationServiceArguments(record claims.ActivationRecordArtifactData) map[string]any {
	return map[string]any{
		"participant_id":   record.ParticipantID,
		"participant_type": record.ParticipantType,
		"operation":        record.Operation,
		"tier":             record.Tier,
		"previous_tier":    record.PreviousTier,
		"target_tier":      record.TargetTier,
		"replica_count":    record.ReplicaCount,
		"ready":            record.Ready,
		"failure_reason":   record.FailureReason,
		"duration":         record.Duration,
	}
}

func activationControllerTools() []string {
	return []string{claims.ActivationControllerToolActivate, claims.ActivationControllerToolDeactivate, claims.ActivationControllerToolQueryTier}
}

func activationControllerRecordClasses() []string {
	return []string{"ready", "failed", "interrupted"}
}

func activationControllerRecordCapacity() int {
	return len(activationControllerTools()) * len(activationControllerRecordClasses())
}

func activationControllerTimeout() time.Duration {
	return time.Duration(activationControllerRecordCapacity()) * time.Second
}

func activationBoardSessionID(board *claims.ClaimsBoard) string {
	if board == nil {
		return ""
	}
	return board.SessionID()
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
		start := time.Now()
		activationFileLog().Info("DEBUG: activation_async_sync_start", "desc", desc)
		if err := fn(context.Background()); err != nil {
			activationFileLog().Info("DEBUG: activation_async_sync_error", "desc", desc, "elapsed_ms", time.Since(start).Milliseconds(), "error", err)
			slog.Warn("activation_claims_sync_error", "desc", desc, "error", err.Error())
			return
		}
		activationFileLog().Info("DEBUG: activation_async_sync_done", "desc", desc, "elapsed_ms", time.Since(start).Milliseconds())
		return
	}
	if err := ac.scope.Go(desc, activationControllerTimeout(), func(ctx context.Context) error {
		start := time.Now()
		activationFileLog().Info("DEBUG: activation_async_start", "desc", desc)
		err := fn(ctx)
		activationFileLog().Info("DEBUG: activation_async_done", "desc", desc, "elapsed_ms", time.Since(start).Milliseconds(), "error", err)
		return err
	}); err != nil {
		activationFileLog().Info("DEBUG: activation_async_dispatch_failed", "desc", desc, "error", err)
		slog.Warn("activation_claims_dispatch_failed", "desc", desc, "error", err.Error())
	}
}
