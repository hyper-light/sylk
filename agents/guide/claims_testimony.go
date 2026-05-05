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
	if g.scope != nil {
		if err := g.scope.Go("guide_post_claim", 5*time.Second, func(ctx context.Context) error {
			return board.PostAction(ctx, action, []claims.Claim{claim})
		}); err != nil {
			slog.Error("guide_post_claim_dispatch_failed", "session", sessionID, "error", err.Error())
			board.RecordNotificationError("guide post claim dispatch: " + err.Error())
		}
		return
	}
	if err := board.PostAction(context.Background(), action, []claims.Claim{claim}); err != nil {
		slog.Error("guide_post_claim_failed", "session", sessionID, "error", err.Error())
		board.RecordNotificationError("guide post claim: " + err.Error())
	}
}

type guideClassificationClaim struct {
	board        *claims.ClaimsBoard
	sessionID    string
	claimID      string
	validationID string
	started      time.Time
}

func shouldOpenGuideClassificationClaim(request *RouteRequest) bool {
	if request == nil {
		return false
	}
	if strings.TrimSpace(request.SourceAgentID) != sourceAgentTUI {
		return false
	}
	if request.FireAndForget {
		return false
	}
	if request.ExplicitTarget || strings.TrimSpace(request.TargetAgentID) != "" {
		return false
	}
	if metadataHasNonEmptyString(request.Metadata, "parent_claim_id") {
		return false
	}
	if metadataHasNonEmptyString(request.Metadata, "control_plane_kind") {
		return false
	}
	return true
}

func (g *Guide) beginGuideClassificationClaim(ctx context.Context, request *RouteRequest) *guideClassificationClaim {
	if g == nil || !shouldOpenGuideClassificationClaim(request) {
		return nil
	}
	board := g.sessionClaimsBoard(request.SessionID)
	if board == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	validationID := uuid.NewString()
	claim := claims.Claim{
		Title:       "Classify and route request",
		Description: truncateForGuide(request.Input, 240),
		ActionType:  claims.ActionTypePrompt,
		Context:     "Classifying and routing request",
		Scope: []claims.ClaimScopeEntry{
			{Kind: "session", Key: request.SessionID},
			{Kind: "correlation", Key: request.CorrelationID},
		},
		Relations: []claims.Relation{
			{Related: "guide", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
		},
		Tags: streamCorrelationTagsFor(request),
		Validations: []*claims.Validation{{
			ID:          validationID,
			Type:        claims.ValidationTypeInspection,
			Required:    true,
			Description: "Guide selected a target agent and recorded classification artifacts",
			QualityBar:  "intent, domain, target, confidence, and method are recorded",
		}},
	}
	action := claims.Action{AgentID: "guide", Type: claims.ActionTypePrompt}
	posted := []claims.Claim{claim}
	if err := board.PostAction(ctx, action, posted); err != nil {
		slog.Error("guide_classification_claim_post_failed",
			"session", request.SessionID,
			"error", err.Error(),
		)
		board.RecordNotificationError("guide classification claim post: " + err.Error())
		return nil
	}
	if len(posted) == 0 || strings.TrimSpace(posted[0].ID) == "" {
		return nil
	}
	claimID := strings.TrimSpace(posted[0].ID)
	if err := board.UpdateClaimProgress(ctx, claimID, claims.ClaimProgressUpdate{
		WorkSummary: "Classifying and routing request",
	}, "guide"); err != nil {
		slog.Warn("guide_classification_claim_progress_failed",
			"session", request.SessionID,
			"claim_id", claimID,
			"error", err.Error(),
		)
	}
	if err := board.SetClaimContext(ctx, claimID, "Classifying and routing request"); err != nil {
		slog.Warn("guide_classification_claim_context_failed",
			"session", request.SessionID,
			"claim_id", claimID,
			"error", err.Error(),
		)
	}
	return &guideClassificationClaim{
		board:        board,
		sessionID:    request.SessionID,
		claimID:      claimID,
		validationID: validationID,
		started:      time.Now(),
	}
}

func (g *Guide) completeGuideClassificationClaim(ctx context.Context, h *guideClassificationClaim, result *RouteResult, targetAgentID string, classifyErr error) {
	if h == nil || h.board == nil || strings.TrimSpace(h.claimID) == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if classifyErr != nil {
		_ = h.board.SetClaimContext(ctx, h.claimID, "Classification failed")
		testament := guideClassificationTestament(h, nil, "", classifyErr)
		if err := h.board.SubmitTestaments(ctx, claims.Action{AgentID: "guide", Type: claims.ActionTypeTestament}, []claims.Testament{testament}); err != nil {
			slog.Warn("guide_classification_failure_testament_failed",
				"session", h.sessionID,
				"claim_id", h.claimID,
				"error", err.Error(),
			)
		}
		if err := h.board.RejectClaim(ctx, h.claimID, claims.StatusChange{
			AgentID: "guide",
			Reason:  classifyErr.Error(),
		}, nil, nil); err != nil {
			slog.Warn("guide_classification_claim_reject_failed",
				"session", h.sessionID,
				"claim_id", h.claimID,
				"error", err.Error(),
			)
		}
		return
	}

	target := strings.TrimSpace(targetAgentID)
	if target == "" && result != nil {
		target = strings.TrimSpace(string(result.TargetAgent))
	}
	if target == "" {
		target = "unknown"
	}
	_ = h.board.SetClaimContext(ctx, h.claimID, "Routing to "+target)
	testament := guideClassificationTestament(h, result, target, nil)
	if err := h.board.SubmitTestaments(ctx, claims.Action{AgentID: "guide", Type: claims.ActionTypeTestament}, []claims.Testament{testament}); err != nil {
		slog.Warn("guide_classification_testament_failed",
			"session", h.sessionID,
			"claim_id", h.claimID,
			"error", err.Error(),
		)
		_ = h.board.RejectClaim(ctx, h.claimID, claims.StatusChange{
			AgentID: "guide",
			Reason:  "classification testament failed: " + err.Error(),
		}, nil, nil)
		return
	}
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
		_ = h.board.RejectClaim(ctx, h.claimID, claims.StatusChange{
			AgentID: "guide",
			Reason:  "classification validation failed: " + err.Error(),
		}, nil, nil)
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
		guideArtifact(h.sessionID, "duration_ms", fmt.Sprintf("%d", time.Since(h.started).Milliseconds())),
	}
	if classifyErr != nil {
		confidence = "error"
		summary = "Classification failed: " + classifyErr.Error()
		artifacts = append(artifacts, guideArtifact(h.sessionID, claims.ArtifactKindError, classifyErr.Error()))
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
			guideArtifact(h.sessionID, "intent", string(result.Intent)),
			guideArtifact(h.sessionID, "domain", string(result.Domain)),
			guideArtifact(h.sessionID, "target_agent", target),
			guideArtifact(h.sessionID, "confidence", formatConfidence(result.Confidence)),
			guideArtifact(h.sessionID, "method", result.ClassificationMethod),
		)
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
	if g.scope != nil {
		if err := g.scope.Go("guide_submit_testament", 5*time.Second, func(ctx context.Context) error {
			return board.SubmitTestaments(ctx, action, []claims.Testament{testament})
		}); err != nil {
			slog.Error("guide_submit_testament_dispatch_failed", "session", sessionID, "error", err.Error())
			board.RecordNotificationError("guide testament dispatch: " + err.Error())
		}
		return
	}
	if err := board.SubmitTestaments(context.Background(), action, []claims.Testament{testament}); err != nil {
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
	v := guideValidation(vtype, required, description, qualityBar)
	v.Status = claims.ValidationStatusPassed
	return v
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
		return claims.NoopDeltaBus{}.SubscribeDelta(pattern, handler)
	}
	sub, err := s.bus.SubscribeAsync(pattern, func(msg *Message) error {
		delta, extractErr := ExtractClaimsDelta(msg)
		if extractErr != nil || delta == nil {
			return nil
		}
		handler(delta)
		return nil
	})
	if err != nil {
		return nil, err
	}
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
