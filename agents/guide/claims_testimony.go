package guide

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/google/uuid"
)

// guideBoard resolves the claims board for the given session.
func guideBoard(sessionID string) *claims.ClaimsBoard {
	return claims.DefaultSessionBoardRegistry().Lookup(sessionID)
}

// guidePostClaimAsync posts a claim to the session board via the guide's
// goroutine scope. Best-effort — board unavailability does not fail the
// caller. Falls back to synchronous when scope is nil.
func (g *Guide) guidePostClaimAsync(sessionID string, action claims.Action, claim claims.Claim) {
	board := guideBoard(sessionID)
	if board == nil {
		return
	}
	post := func(ctx context.Context) error {
		generated, err := board.GenerateClaimAction(ctx, action, []claims.Claim{claim}, claims.GenerateClaimActionOptions{
			IdempotencyKey: firstNonEmptyString(action.IdempotencyKey, claim.IdempotencyKey, "guide:claim:"+sessionID+":"+claim.Title+":"+claim.Description),
			Reason:         "guide generated claim",
		})
		if err != nil {
			return err
		}
		if generated == nil || len(generated.Claims) == 0 {
			return fmt.Errorf("guide generated claim action returned no claims")
		}
		claimID := strings.TrimSpace(generated.Claims[0].ID)
		if claimID == "" {
			return fmt.Errorf("guide generated claim action returned empty claim id")
		}
		return postGuideGeneratedClaimIfNeeded(ctx, board, generated.Claims[0], "guide", "guide posted claim")
	}
	if g.scope != nil {
		if err := g.scope.Go("guide_post_claim", 5*time.Second, func(ctx context.Context) error {
			return post(ctx)
		}); err != nil {
			slog.Error("guide_post_claim_dispatch_failed", "session", sessionID, "error", err.Error())
			board.RecordNotificationError("guide post claim dispatch: " + err.Error())
		}
		return
	}
	if err := post(context.Background()); err != nil {
		slog.Error("guide_post_claim_failed", "session", sessionID, "error", err.Error())
		board.RecordNotificationError("guide post claim: " + err.Error())
	}
}

func postGuideGeneratedClaimIfNeeded(ctx context.Context, board *claims.ClaimsBoard, claim claims.Claim, actorID string, reason string) error {
	if board == nil {
		return fmt.Errorf("claims board is required")
	}
	claimID := strings.TrimSpace(claim.ID)
	if claimID == "" {
		return fmt.Errorf("generated claim id is required")
	}
	claims.RouteDebugLog().Info("guide_post_generated_claim_if_needed",
		"board_id", board.BoardID(),
		"claim_id", claimID,
		"actor_id", actorID,
		"reason", reason,
		"lifecycle_status", claim.LifecycleStatus,
	)
	switch claim.LifecycleStatus {
	case "", claims.ClaimLifecycleGenerated:
		return board.PostGeneratedClaim(ctx, claimID, actorID, claims.ClaimPostOptions{Reason: reason})
	case claims.ClaimLifecycleGenerationFailed, claims.ClaimLifecyclePostFailed:
		return fmt.Errorf("generated claim %q already failed lifecycle at %s", claimID, claim.LifecycleStatus)
	default:
		return nil
	}
}

func postGuideGeneratedTestamentIfNeeded(ctx context.Context, board *claims.ClaimsBoard, testament claims.Testament, actorID string, reason string) error {
	if board == nil {
		return fmt.Errorf("claims board is required")
	}
	testamentID := strings.TrimSpace(testament.ID)
	if testamentID == "" {
		return fmt.Errorf("generated testament id is required")
	}
	switch testament.LifecycleStatus {
	case "", claims.TestamentLifecycleGenerated:
		return board.PostGeneratedTestament(ctx, testamentID, actorID, claims.TestamentPostOptions{Reason: reason})
	default:
		return nil
	}
}

type guideClassificationClaim struct {
	board           *claims.ClaimsBoard
	sessionID       string
	claimID         string
	validationID    string
	input           string
	requestedTarget string
	explicitTarget  bool
	started         time.Time
}

const (
	guideClassificationClaimTag = "ui:guide_classification"
)

func shouldOpenGuideClassificationClaim(request *RouteRequest) bool {
	ok, _ := guideClassificationClaimDecision(request)
	return ok
}

func guideClassificationClaimDecision(request *RouteRequest) (bool, string) {
	if request == nil {
		return false, "nil_request"
	}
	if strings.TrimSpace(request.SourceAgentID) != sourceAgentTUI {
		return false, "non_tui_source"
	}
	if request.FireAndForget {
		return false, "fire_and_forget"
	}
	if metadataHasNonEmptyString(request.Metadata, "parent_claim_id") {
		return false, "parent_claim_id"
	}
	if metadataHasNonEmptyString(request.Metadata, "control_plane_kind") {
		return false, "control_plane_kind"
	}
	return true, "top_level_tui_classification"
}

func routeRequestSessionID(request *RouteRequest) string {
	if request == nil {
		return ""
	}
	return request.SessionID
}

func routeRequestCorrelationID(request *RouteRequest) string {
	if request == nil {
		return ""
	}
	return request.CorrelationID
}

func routeRequestSourceAgentID(request *RouteRequest) string {
	if request == nil {
		return ""
	}
	return request.SourceAgentID
}

func routeRequestTargetAgentID(request *RouteRequest) string {
	if request == nil {
		return ""
	}
	return request.TargetAgentID
}

func routeRequestExplicitTarget(request *RouteRequest) bool {
	return request != nil && request.ExplicitTarget
}

func routeRequestFireAndForget(request *RouteRequest) bool {
	return request != nil && request.FireAndForget
}

func routeRequestMetadata(request *RouteRequest) map[string]any {
	if request == nil {
		return nil
	}
	return request.Metadata
}

func (g *Guide) beginGuideClassificationClaim(ctx context.Context, request *RouteRequest) *guideClassificationClaim {
	ok, reason := guideClassificationClaimDecision(request)
	guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_decision",
		"allowed", ok,
		"reason", reason,
		"guide_nil", g == nil,
		"session_id", routeRequestSessionID(request),
		"correlation_id", routeRequestCorrelationID(request),
		"source_agent_id", routeRequestSourceAgentID(request),
		"target_agent_id", routeRequestTargetAgentID(request),
		"explicit_target", routeRequestExplicitTarget(request),
		"fire_and_forget", routeRequestFireAndForget(request),
		"has_parent_claim_id", metadataHasNonEmptyString(routeRequestMetadata(request), "parent_claim_id"),
		"has_control_plane_kind", metadataHasNonEmptyString(routeRequestMetadata(request), "control_plane_kind"),
	)
	if g == nil || !ok {
		return nil
	}
	board := g.sessionClaimsBoard(request.SessionID)
	if board == nil {
		guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_no_board",
			"session_id", request.SessionID,
			"correlation_id", request.CorrelationID,
		)
		return nil
	}
	guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_board",
		"session_id", request.SessionID,
		"correlation_id", request.CorrelationID,
		"board_id", board.BoardID(),
	)
	if ctx == nil {
		ctx = context.Background()
	}
	validationID := uuid.NewString()
	claim := claims.Claim{
		Title:       "Classifying request",
		Description: truncateForGuide(request.Input, 240),
		ActionType:  claims.ActionTypePrompt,
		Context:     "Classifying request",
		Scope: []claims.ClaimScopeEntry{
			{Kind: "session", Key: request.SessionID},
			{Kind: "correlation", Key: request.CorrelationID},
		},
		Relations: []claims.Relation{
			{Related: "guide", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "guide", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Tags: append(streamCorrelationTagsFor(request), guideClassificationClaimTag),
		Validations: []*claims.Validation{{
			ID:          validationID,
			Type:        claims.ValidationTypeInspection,
			Required:    true,
			Description: "Guide selected a target agent and recorded classification artifacts",
			QualityBar:  "intent, domain, target, confidence, and method are recorded",
		}},
	}
	action := claims.Action{AgentID: "guide", Type: claims.ActionTypePrompt}
	generated, err := board.GenerateClaimAction(ctx, action, []claims.Claim{claim}, claims.GenerateClaimActionOptions{
		IdempotencyKey: "guide:classification:" + request.SessionID + ":" + request.CorrelationID,
		Reason:         "guide generated classification claim",
	})
	if err != nil {
		slog.Error("guide_classification_claim_post_failed",
			"session", request.SessionID,
			"error", err.Error(),
		)
		guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_post_failed",
			"session_id", request.SessionID,
			"correlation_id", request.CorrelationID,
			"board_id", board.BoardID(),
			"error", err.Error(),
		)
		board.RecordNotificationError("guide classification claim post: " + err.Error())
		return nil
	}
	if generated == nil || len(generated.Claims) == 0 || strings.TrimSpace(generated.Claims[0].ID) == "" {
		guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_post_empty",
			"session_id", request.SessionID,
			"correlation_id", request.CorrelationID,
			"board_id", board.BoardID(),
		)
		return nil
	}
	claimID := strings.TrimSpace(generated.Claims[0].ID)
	if len(generated.Claims[0].Validations) > 0 && generated.Claims[0].Validations[0] != nil {
		validationID = strings.TrimSpace(generated.Claims[0].Validations[0].ID)
	}
	if err := postGuideGeneratedClaimIfNeeded(ctx, board, generated.Claims[0], "guide", "guide posted classification claim"); err != nil {
		slog.Error("guide_classification_claim_post_failed",
			"session", request.SessionID,
			"claim_id", claimID,
			"error", err.Error(),
		)
		board.RecordNotificationError("guide classification claim post: " + err.Error())
		return nil
	}
	claimStatus := generated.Claims[0].LifecycleStatus
	if claimStatus == "" || claimStatus == claims.ClaimLifecycleGenerated {
		claimStatus = claims.ClaimLifecyclePosted
	}
	if claimStatus == claims.ClaimLifecyclePosted {
		if err := board.AcknowledgeClaimReceipt(ctx, claimID, "guide"); err != nil {
			slog.Warn("guide_classification_claim_receipt_failed",
				"session", request.SessionID,
				"claim_id", claimID,
				"error", err.Error(),
			)
		} else {
			claimStatus = claims.ClaimLifecycleReceived
		}
	}
	if latest, ok := board.CloneClaim(claimID); ok {
		claimStatus = latest.LifecycleStatus
	}
	if !guideClassificationClaimCanProgress(claimStatus) {
		request.Metadata = mergeForwardMetadata(request.Metadata, map[string]any{
			"guide_classification_claim_id": claimID,
		})
		return &guideClassificationClaim{
			board:           board,
			sessionID:       request.SessionID,
			claimID:         claimID,
			validationID:    validationID,
			input:           request.Input,
			requestedTarget: strings.TrimSpace(request.TargetAgentID),
			explicitTarget:  request.ExplicitTarget,
			started:         time.Now(),
		}
	}
	request.Metadata = mergeForwardMetadata(request.Metadata, map[string]any{
		"guide_classification_claim_id": claimID,
	})
	guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_posted",
		"session_id", request.SessionID,
		"correlation_id", request.CorrelationID,
		"board_id", board.BoardID(),
		"claim_id", claimID,
		"validation_id", validationID,
	)
	if err := board.UpdateClaimProgress(ctx, claimID, claims.ClaimProgressUpdate{
		WorkSummary: "Classifying request",
	}, "guide"); err != nil {
		slog.Warn("guide_classification_claim_progress_failed",
			"session", request.SessionID,
			"claim_id", claimID,
			"error", err.Error(),
		)
		guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_progress_failed",
			"session_id", request.SessionID,
			"correlation_id", request.CorrelationID,
			"board_id", board.BoardID(),
			"claim_id", claimID,
			"error", err.Error(),
		)
	} else {
		guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_progress_ok",
			"session_id", request.SessionID,
			"correlation_id", request.CorrelationID,
			"board_id", board.BoardID(),
			"claim_id", claimID,
		)
	}
	if err := board.SetClaimContext(ctx, claimID, "Classifying request"); err != nil {
		slog.Warn("guide_classification_claim_context_failed",
			"session", request.SessionID,
			"claim_id", claimID,
			"error", err.Error(),
		)
		guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_context_failed",
			"session_id", request.SessionID,
			"correlation_id", request.CorrelationID,
			"board_id", board.BoardID(),
			"claim_id", claimID,
			"error", err.Error(),
		)
	} else {
		guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_context_ok",
			"session_id", request.SessionID,
			"correlation_id", request.CorrelationID,
			"board_id", board.BoardID(),
			"claim_id", claimID,
		)
	}
	return &guideClassificationClaim{
		board:           board,
		sessionID:       request.SessionID,
		claimID:         claimID,
		validationID:    validationID,
		input:           request.Input,
		requestedTarget: strings.TrimSpace(request.TargetAgentID),
		explicitTarget:  request.ExplicitTarget,
		started:         time.Now(),
	}
}

func guideClassificationClaimCanProgress(status claims.ClaimLifecycleStatus) bool {
	return status == claims.ClaimLifecycleReceived || status == claims.ClaimLifecycleProgressed
}

func (g *Guide) completeGuideClassificationClaim(ctx context.Context, h *guideClassificationClaim, result *RouteResult, targetAgentID string, classifyErr error) string {
	if h == nil || h.board == nil || strings.TrimSpace(h.claimID) == "" {
		guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_complete_skipped",
			"handle_nil", h == nil,
			"board_nil", h == nil || h.board == nil,
		)
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if classifyErr != nil {
		guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_complete_error",
			"session_id", h.sessionID,
			"board_id", h.board.BoardID(),
			"claim_id", h.claimID,
			"error", classifyErr.Error(),
			"elapsed_ms", time.Since(h.started).Milliseconds(),
		)
		_ = h.board.SetClaimContext(ctx, h.claimID, "Classification failed")
		testament := guideClassificationTestament(h, nil, "", classifyErr)
		testamentID, err := g.postGuideClassificationTestament(ctx, h, testament, "guide posted classification failure testament")
		if err != nil {
			slog.Warn("guide_classification_failure_testament_failed",
				"session", h.sessionID,
				"claim_id", h.claimID,
				"error", err.Error(),
			)
			guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_error_testament_failed",
				"session_id", h.sessionID,
				"board_id", h.board.BoardID(),
				"claim_id", h.claimID,
				"error", err.Error(),
			)
		}
		g.beginGuideClassificationTestamentValidation(ctx, h, testamentID)
		if err := h.board.EvaluateValidation(ctx, h.claimID, h.validationID, claims.StatusChange{
			AgentID: "guide",
			To:      string(claims.ValidationStatusErrored),
			Reason:  classifyErr.Error(),
		}); err != nil {
			slog.Warn("guide_classification_claim_validation_error_failed",
				"session", h.sessionID,
				"claim_id", h.claimID,
				"error", err.Error(),
			)
			guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_validation_error_failed",
				"session_id", h.sessionID,
				"board_id", h.board.BoardID(),
				"claim_id", h.claimID,
				"error", err.Error(),
			)
		} else {
			guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_validation_errored",
				"session_id", h.sessionID,
				"board_id", h.board.BoardID(),
				"claim_id", h.claimID,
			)
		}
		g.completeGuideClassificationTestamentValidation(ctx, h, testamentID, claims.TestamentLifecycleValidationErrored, classifyErr.Error())
		return testamentID
	}

	target := strings.TrimSpace(targetAgentID)
	if target == "" && result != nil {
		target = strings.TrimSpace(string(result.TargetAgent))
	}
	if target == "" {
		target = "unknown"
	}
	guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_complete_success",
		"session_id", h.sessionID,
		"board_id", h.board.BoardID(),
		"claim_id", h.claimID,
		"target", target,
		"intent", guideRouteIntent(result),
		"domain", guideRouteDomain(result),
		"elapsed_ms", time.Since(h.started).Milliseconds(),
	)
	_ = h.board.SetClaimContext(ctx, h.claimID, "Request forwarded")
	testament := guideClassificationTestament(h, result, target, nil)
	testamentID, err := g.postGuideClassificationTestament(ctx, h, testament, "guide posted classification testament")
	if err != nil {
		slog.Warn("guide_classification_testament_failed",
			"session", h.sessionID,
			"claim_id", h.claimID,
			"error", err.Error(),
		)
		guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_testament_failed",
			"session_id", h.sessionID,
			"board_id", h.board.BoardID(),
			"claim_id", h.claimID,
			"error", err.Error(),
		)
		_ = h.board.RecordClaimTestamentGenerationFailure(ctx, h.claimID, "guide", claims.LifecycleFailureOptions{
			Reason:       "classification testament failed: " + err.Error(),
			ArtifactKind: claims.ArtifactKindErrorDiagnostic,
		})
		return ""
	}
	g.beginGuideClassificationTestamentValidation(ctx, h, testamentID)
	if err := h.board.EvaluateValidation(ctx, h.claimID, h.validationID, claims.StatusChange{
		AgentID: "guide",
		To:      string(claims.ValidationStatusPassed),
		Reason:  "classification artifacts recorded",
	}); err != nil {
		slog.Warn("guide_classification_validation_failed",
			"session", h.sessionID,
			"claim_id", h.claimID,
			"validation_id", h.validationID,
			"error", err.Error(),
		)
		guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_validation_failed",
			"session_id", h.sessionID,
			"board_id", h.board.BoardID(),
			"claim_id", h.claimID,
			"validation_id", h.validationID,
			"error", err.Error(),
		)
		_ = h.board.RejectClaim(ctx, h.claimID, claims.StatusChange{
			AgentID: "guide",
			Reason:  "classification validation failed: " + err.Error(),
		}, nil, nil)
	} else {
		guideFileLog().Info("GUIDE_CLAIMS_DEBUG: classification_claim_validation_ok",
			"session_id", h.sessionID,
			"board_id", h.board.BoardID(),
			"claim_id", h.claimID,
			"validation_id", h.validationID,
		)
	}
	g.completeGuideClassificationTestamentValidation(ctx, h, testamentID, claims.TestamentLifecycleValidated, "classification artifacts validated")
	return testamentID
}

func guideRouteIntent(result *RouteResult) string {
	if result == nil {
		return ""
	}
	return string(result.Intent)
}

func guideRouteDomain(result *RouteResult) string {
	if result == nil {
		return ""
	}
	return string(result.Domain)
}

func (g *Guide) postGuideClassificationTestament(ctx context.Context, h *guideClassificationClaim, testament claims.Testament, reason string) (string, error) {
	if h == nil || h.board == nil {
		return "", fmt.Errorf("guide classification claim handle is not available")
	}
	generated, err := h.board.GenerateTestamentAction(ctx, claims.Action{AgentID: "guide", Type: claims.ActionTypeTestament}, []claims.Testament{testament}, claims.GenerateTestamentActionOptions{
		IdempotencyKey: "guide:classification:testament:" + h.claimID + ":" + reason,
		Reason:         reason,
	})
	if err != nil {
		return "", err
	}
	if generated == nil || len(generated.Testaments) == 0 {
		return "", fmt.Errorf("guide classification testament generation returned no testaments")
	}
	testamentID := strings.TrimSpace(generated.Testaments[0].ID)
	if testamentID == "" {
		return "", fmt.Errorf("guide classification testament generation returned empty testament id")
	}
	if err := postGuideGeneratedTestamentIfNeeded(ctx, h.board, generated.Testaments[0], "guide", reason); err != nil {
		return testamentID, err
	}
	return testamentID, nil
}

func (g *Guide) beginGuideClassificationTestamentValidation(ctx context.Context, h *guideClassificationClaim, testamentID string) {
	testamentID = strings.TrimSpace(testamentID)
	if h == nil || h.board == nil || testamentID == "" {
		return
	}
	if err := h.board.AcknowledgeTestamentReceipt(ctx, testamentID, "guide"); err != nil {
		slog.Warn("guide_classification_testament_receipt_failed", "session", h.sessionID, "testament_id", testamentID, "error", err.Error())
	}
	if err := h.board.BeginTestamentValidation(ctx, testamentID, "guide"); err != nil {
		slog.Warn("guide_classification_testament_validation_begin_failed", "session", h.sessionID, "testament_id", testamentID, "error", err.Error())
	}
}

func (g *Guide) completeGuideClassificationTestamentValidation(ctx context.Context, h *guideClassificationClaim, testamentID string, status claims.TestamentLifecycleStatus, reason string) {
	testamentID = strings.TrimSpace(testamentID)
	if h == nil || h.board == nil || testamentID == "" {
		return
	}
	if err := h.board.CompleteTestamentValidation(ctx, testamentID, "guide", status, reason); err != nil {
		slog.Warn("guide_classification_testament_validation_complete_failed", "session", h.sessionID, "testament_id", testamentID, "status", status, "error", err.Error())
	}
}

func guideClassificationTestament(h *guideClassificationClaim, result *RouteResult, target string, classifyErr error) claims.Testament {
	relations := []claims.Relation{
		{Related: "guide", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		{Related: h.claimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim},
	}
	confidence := "committed"
	summary := "Classified request: target=" + target
	artifacts := []*claims.Artifact{
		guideClassificationArtifact(h.sessionID, "duration_ms", fmt.Sprintf("%d", time.Since(h.started).Milliseconds())),
	}
	if classifyErr != nil {
		confidence = "error"
		summary = "Classification failed: " + classifyErr.Error()
		artifacts = append(artifacts, guideClassificationArtifact(h.sessionID, claims.ArtifactKindError, classifyErr.Error()))
		return claims.Testament{
			AgentID:    "guide",
			SessionID:  h.sessionID,
			Summary:    truncateForGuide(summary, 240),
			Confidence: confidence,
			Relations:  relations,
			Artifacts:  artifacts,
		}
	}
	if result != nil {
		summary = "Classified request: intent=" + string(result.Intent) + " target=" + target + " confidence=" + formatConfidence(result.Confidence)
		artifacts = append(artifacts,
			guideClassificationArtifact(h.sessionID, "intent", string(result.Intent)),
			guideClassificationArtifact(h.sessionID, "domain", string(result.Domain)),
			guideClassificationArtifact(h.sessionID, "target_agent", target),
			guideClassificationArtifact(h.sessionID, "confidence", formatConfidence(result.Confidence)),
			guideClassificationArtifact(h.sessionID, "method", result.ClassificationMethod),
		)
		if strings.TrimSpace(result.Reason) != "" {
			artifacts = append(artifacts, guideClassificationArtifact(h.sessionID, "route_reason", result.Reason))
		}
		if h != nil && h.explicitTarget {
			artifacts = append(artifacts, guideClassificationArtifact(h.sessionID, "direct_address", firstNonEmptyString(h.requestedTarget, target)))
			relations = append(relations, claims.Relation{
				Related: target, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipDirectAddressed,
			})
		}
	}
	if target != "" {
		relations = append(relations, claims.Relation{
			Related: target, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject,
		})
	}
	return claims.Testament{
		AgentID:    "guide",
		SessionID:  h.sessionID,
		Summary:    truncateForGuide(summary, 240),
		Confidence: confidence,
		Relations:  relations,
		Artifacts:  artifacts,
	}
}

// guideSubmitTestamentAsync submits a testament via scope. Best-effort.
func (g *Guide) guideSubmitTestamentAsync(sessionID string, testament claims.Testament) {
	board := guideBoard(sessionID)
	if board == nil {
		return
	}
	action := claims.Action{AgentID: "guide", Type: claims.ActionTypeTestament}
	submit := func(ctx context.Context) error {
		generated, err := board.GenerateTestamentAction(ctx, action, []claims.Testament{testament}, claims.GenerateTestamentActionOptions{
			IdempotencyKey:  firstNonEmptyString(testament.IdempotencyKey, "guide:testament:"+sessionID+":"+testament.Summary),
			AllowStandalone: claims.ClaimIDFromRelations(testament.Relations) == "",
			Reason:          "guide generated testament",
		})
		if err != nil {
			return err
		}
		if generated == nil || len(generated.Testaments) == 0 {
			return fmt.Errorf("guide generated testament action returned no testaments")
		}
		testamentID := strings.TrimSpace(generated.Testaments[0].ID)
		if testamentID == "" {
			return fmt.Errorf("guide generated testament action returned empty testament id")
		}
		return postGuideGeneratedTestamentIfNeeded(ctx, board, generated.Testaments[0], "guide", "guide posted testament")
	}
	if g.scope != nil {
		if err := g.scope.Go("guide_submit_testament", 5*time.Second, func(ctx context.Context) error {
			return submit(ctx)
		}); err != nil {
			slog.Error("guide_submit_testament_dispatch_failed", "session", sessionID, "error", err.Error())
			board.RecordNotificationError("guide testament dispatch: " + err.Error())
		}
		return
	}
	if err := submit(context.Background()); err != nil {
		slog.Error("guide_submit_testament_failed", "session", sessionID, "error", err.Error())
		board.RecordNotificationError("guide testament: " + err.Error())
	}
}

// guideCorrectiveClaim builds a corrective claim that supersedes a prior route.
func guideCorrectiveClaim(title, description, newTarget, sessionID, correlationID string) claims.Claim {
	return claims.Claim{
		Title:       title,
		Description: description,
		Scope: []claims.ClaimScopeEntry{
			{Kind: "session", Key: sessionID},
			{Kind: "correlation", Key: correlationID},
		},
		ActionType: claims.ActionTypeCorrective,
		Relations: []claims.Relation{
			{Related: "guide", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: newTarget, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
	}
}

// guideTestament builds a testament issued by the guide.
func guideTestament(sessionID, summary, confidence string, subject string, artifacts []*claims.Artifact) claims.Testament {
	rels := []claims.Relation{
		{Related: "guide", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
	}
	if subject != "" {
		rels = append(rels, claims.Relation{
			Related: subject, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject,
		})
	}
	return claims.Testament{
		AgentID:    "guide",
		SessionID:  sessionID,
		Summary:    summary,
		Confidence: confidence,
		Relations:  rels,
		Artifacts:  artifacts,
	}
}

// guideArtifact builds a single artifact.
func guideArtifact(sessionID, kind, reference string) *claims.Artifact {
	return &claims.Artifact{
		AgentID:   "guide",
		SessionID: sessionID,
		Kind:      kind,
		Reference: reference,
	}
}

func guideClassificationArtifact(sessionID, kind, reference string) *claims.Artifact {
	artifact := guideArtifact(sessionID, kind, reference)
	artifact.Metadata = map[string]any{
		"ui_activity":     "guide_classification",
		"ui_display_name": strings.TrimSpace(kind),
		"args_summary":    "artifact generated",
	}
	return artifact
}

// guideJSONArtifact builds a JSON-serialized artifact.
func guideJSONArtifact(sessionID, kind string, value any) *claims.Artifact {
	ref, err := json.Marshal(value)
	if err != nil {
		ref = []byte(`{"error":"` + strings.ReplaceAll(err.Error(), `"`, `\"`) + `"}`)
	}
	return guideArtifact(sessionID, kind, string(ref))
}

// guideValidation builds a pending validation.
func guideValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	return &claims.Validation{
		Type:        vtype,
		Required:    required,
		Description: description,
		QualityBar:  qualityBar,
		Status:      claims.ValidationStatusPending,
	}
}

// guidePassedValidation builds an already-passed validation.
func guidePassedValidation(vtype claims.ValidationType, required bool, description, qualityBar string) *claims.Validation {
	return &claims.Validation{
		Type:        vtype,
		Required:    required,
		Description: description,
		QualityBar:  qualityBar,
		Status:      claims.ValidationStatusPassed,
	}
}

// wireClaimsCallbacks sets up claims-posting callbacks on standalone
// subsystems (circuit breakers, health monitor, dead letter queue,
// pending cleanup) that don't have direct access to the Guide's scope.
// Called once after construction.
func (g *Guide) wireClaimsCallbacks() {
	// Circuit breaker state changes.
	if g.circuits != nil {
		g.circuits.SetOnStateChange(func(agentID string, from, to CircuitState) {
			g.guideSubmitTestamentAsync(g.sessionID, guideTestament(
				g.sessionID,
				fmt.Sprintf("Circuit breaker %s: %s → %s", agentID, from, to),
				"committed", agentID,
				[]*claims.Artifact{
					guideArtifact(g.sessionID, "agent_id", agentID),
					guideArtifact(g.sessionID, "from_state", from.String()),
					guideArtifact(g.sessionID, "to_state", to.String()),
				},
			))
		})
	}

	// Dead letter entries.
	if g.dlq != nil {
		g.dlq.SetOnEnqueue(func(letter *DeadLetter) {
			if letter == nil {
				return
			}
			g.guideSubmitTestamentAsync(g.sessionID, guideTestament(
				g.sessionID,
				"Dead letter: "+string(letter.Reason),
				"committed", "",
				[]*claims.Artifact{
					guideArtifact(g.sessionID, "reason", string(letter.Reason)),
					guideArtifact(g.sessionID, "source_agent", letter.SourceAgentID),
					guideArtifact(g.sessionID, "error", letter.Error),
				},
			))
		})
	}

	// Pending cleanup.
	if g.pendingCleanup != nil {
		g.pendingCleanup.SetOnCleanup(func(expiredCount int, correlationIDs []string) {
			if expiredCount == 0 {
				return
			}
			g.guideSubmitTestamentAsync(g.sessionID, guideTestament(
				g.sessionID,
				fmt.Sprintf("Pending cleanup: %d expired requests removed", expiredCount),
				"committed", "",
				[]*claims.Artifact{
					guideArtifact(g.sessionID, "expired_count", fmt.Sprintf("%d", expiredCount)),
					guideJSONArtifact(g.sessionID, "correlation_ids", correlationIDs),
				},
			))
		})
	}

	// Health degradation.
	if g.health != nil {
		g.health.SetOnDegraded(func(agentID string) {
			g.guideSubmitTestamentAsync(g.sessionID, guideTestament(
				g.sessionID,
				"Agent degraded: "+agentID,
				"committed", agentID,
				[]*claims.Artifact{
					guideArtifact(g.sessionID, "agent_id", agentID),
					guideArtifact(g.sessionID, "health_status", "degraded"),
				},
			))
		})
	}
}

// guideScope adapts *concurrency.GoroutineScope to claims.ScopeProvider.
func (g *Guide) guideScope() claims.ScopeProvider {
	if g.scope == nil {
		return nil
	}
	return &guideScopeClaimsAdapter{scope: g.scope}
}

type guideScopeClaimsAdapter struct {
	scope *concurrency.GoroutineScope
}

func (a *guideScopeClaimsAdapter) Go(desc string, timeout time.Duration, fn func(context.Context) error) error {
	return a.scope.Go(desc, timeout, concurrency.WorkFunc(fn))
}

// guideDeltaSubscriber wraps an EventBus as a claims.DeltaSubscriber.
// This is the guide-local equivalent of shared.EventBusDeltaSubscriber;
// the guide cannot import agents/shared (circular).
type guideDeltaSubscriber struct {
	bus EventBus
}

func (s *guideDeltaSubscriber) SubscribeDelta(pattern string, handler claims.DeltaHandler) (claims.DeltaSubscription, error) {
	if s.bus == nil {
		claims.RouteFlowDebugLog().Info("claims_route_flow_guide_delta_subscribe_noop",
			"pattern", pattern,
		)
		return claims.NoopDeltaBus{}.SubscribeDelta(pattern, handler)
	}
	sub, err := s.bus.SubscribeAsync(pattern, func(msg *Message) error {
		delta, extractErr := ExtractClaimsDelta(msg)
		if extractErr != nil {
			claims.RouteFlowDebugLog().Info("claims_route_flow_guide_delta_extract_failed",
				"pattern", pattern,
				"message_id", messageIDForDebug(msg),
				"message_type", messageTypeForDebug(msg),
				"error", extractErr.Error(),
			)
			return nil
		}
		if delta == nil {
			return nil
		}
		claims.TraceRouteFlowDelta("claims_route_flow_guide_delta_delivered", delta,
			"pattern", pattern,
			"message_id", messageIDForDebug(msg),
			"message_type", messageTypeForDebug(msg),
		)
		handler(delta)
		return nil
	})
	if err != nil {
		claims.RouteFlowDebugLog().Info("claims_route_flow_guide_delta_subscribe_failed",
			"pattern", pattern,
			"error", err.Error(),
		)
		return nil, err
	}
	claims.RouteFlowDebugLog().Info("claims_route_flow_guide_delta_subscribed",
		"pattern", pattern,
	)
	return &guideDeltaSub{sub: sub}, nil
}

type guideDeltaSub struct {
	sub Subscription
}

func (s *guideDeltaSub) Topic() string {
	if s.sub == nil {
		return ""
	}
	return s.sub.Topic()
}

func (s *guideDeltaSub) Unsubscribe() error {
	if s.sub == nil {
		return nil
	}
	return s.sub.Unsubscribe()
}

func (g *Guide) processClaimsEntry(ctx context.Context, entry *claims.GraphEntryPoint) error {
	if entry == nil {
		return nil
	}
	if guideShouldSuppressClaimsEntry(entry) {
		return nil
	}

	// Bind to the entry's parent claim so any artifacts the guide
	// emits during claim-driven processing nest under the issuer's
	// peer-interaction row in the chat tree (UI_DESIGN.md §4.7.3).
	// Inline rather than via shared.NewClaimsEntryAccumulator —
	// agents/guide cannot import agents/shared (circular).
	acc := claims.NewTestamentAccumulator("guide", g.sessionID)
	acc.WithBoard(guideBoard(g.sessionID))
	if entry != nil && entry.Node.Claim != nil {
		if id := strings.TrimSpace(entry.Node.Claim.ID); id != "" {
			acc.WithClaimID(id)
		}
	}
	defer acc.Flush(ctx, guideBoard(g.sessionID), g.guideScope())
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	acc.Note("Processing claims entry: " + entry.Delta.DeltaKind())

	userMessage := composeGuideClaimsEntryPrompt(entry)
	board := guideBoard(g.sessionID)
	userMessage = claims.PrependBoardPreamble(userMessage, board, "guide")

	routeReq := &RouteRequest{
		Input:         userMessage,
		SourceAgentID: "guide",
		SessionID:     g.sessionID,
	}

	fwd, err := g.Route(ctx, routeReq)
	if err != nil {
		slog.Error("guide_claims_entry_route_failed", "error", err.Error(), "delta_key", entry.Delta.DeltaKey())
		acc.Record("error", err.Error())
		return err
	}
	target := ""
	if fwd != nil {
		target = fwd.TargetAgentID
	}
	acc.Record("routed_to", target)
	acc.Note("Claims entry routed successfully")
	return nil
}

func guideShouldSuppressClaimsEntry(entry *claims.GraphEntryPoint) bool {
	if entry == nil || entry.Node.Claim == nil {
		return false
	}
	for _, tag := range entry.Node.Claim.Tags {
		if strings.TrimSpace(tag) == guideClassificationClaimTag {
			return true
		}
	}
	return false
}

// composeGuideClaimsEntryPrompt formats a GraphEntryPoint into a routing
// prompt. The guide package cannot import agents/shared (circular), so
// this is a local equivalent of shared.ComposeClaimsEntryPrompt.
func composeGuideClaimsEntryPrompt(entry *claims.GraphEntryPoint) string {
	if entry == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Claims Entry\n\n")
	b.WriteString("**Delta Kind:** " + entry.Delta.DeltaKind() + "\n")
	b.WriteString("**Delta Key:** " + entry.Delta.DeltaKey() + "\n\n")
	b.WriteString("Route this claims entry to the most appropriate agent for processing.\n")
	return b.String()
}

// truncateForGuide truncates a string for claim titles.
func truncateForGuide(s string, max int) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}

// formatConfidence formats a float64 confidence as a string.
func formatConfidence(c float64) string {
	return fmt.Sprintf("%.2f", c)
}
