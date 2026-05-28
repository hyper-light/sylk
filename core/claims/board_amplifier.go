package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/google/uuid"
)

// amplifierEmitTimeout bounds each tracked emission goroutine. Emissions
// are non-blocking on the hot path (activity sink enqueues; bus
// enqueues); this timeout guards against pathological stalls so
// scope.Shutdown can drain deterministically.
const amplifierEmitTimeout = 2 * time.Second

// BoardAmplifier dual-emits every claims board mutation as:
//
//	(1) a Fabric AgentActivity via activity.Append, and
//	(2) a structured Delta to a bus topic via DeltaPublisher.
//
// Both emissions are best-effort and non-blocking from the board
// mutation's perspective. Errors are logged via log/slog and recorded
// on the board's notificationErrors so agents see them in the next
// Projection() read as testament error artifacts (per the errors-as-
// artifacts principle). The board's state has already committed —
// emission failure never blocks or rolls back mutation.
//
// Thread safety: the amplifier itself is stateless after construction;
// all per-emission work happens on scope-tracked goroutines or inline.
// Fields are set only at construction via the With* options.
type BoardAmplifier struct {
	sessionID string
	taskID    string
	boardID   string

	deltaBus               DeltaPublisher
	scope                  ScopeProvider
	agentRefResolver       AgentRefResolver
	canonicalDirectEnabled bool

	// errorSink receives emission failures. When nil, errors are
	// dropped silently (tests). Wired by the board so agents see
	// them on Projection().
	errorSink func(message string)
}

// NewBoardAmplifier constructs an amplifier with a safe-default
// (Noop) DeltaPublisher. Callers wire a real bus via WithDeltaBus.
func NewBoardAmplifier(sessionID, taskID, boardID string) *BoardAmplifier {
	return &BoardAmplifier{
		sessionID:              sessionID,
		taskID:                 taskID,
		boardID:                boardID,
		deltaBus:               NoopDeltaBus{},
		canonicalDirectEnabled: true,
	}
}

// WithDeltaBus wires the bus publisher. Nil is normalized to the
// NoopDeltaBus so every amplifier is safe to call.
func (a *BoardAmplifier) WithDeltaBus(bus DeltaPublisher) *BoardAmplifier {
	if a == nil {
		return nil
	}
	a.deltaBus = publishOrNoop(bus)
	return a
}

// WithScope wires a goroutine scope. When set, emissions are
// dispatched asynchronously via scope.Go under amplifierEmitTimeout.
// When nil, emissions are synchronous (tests, standalone tools).
func (a *BoardAmplifier) WithScope(scope ScopeProvider) *BoardAmplifier {
	if a == nil {
		return nil
	}
	a.scope = scope
	return a
}

// WithAgentRefResolver wires optional canonical identity resolution for
// actor and delivery refs.
func (a *BoardAmplifier) WithAgentRefResolver(resolver AgentRefResolver) *BoardAmplifier {
	if a == nil {
		return nil
	}
	a.agentRefResolver = resolver
	return a
}

// WithCanonicalDirectEnabled controls direct canonical bus emission.
// Durable boards with the canonical projector disable direct canonical
// emission so the outbox owns replayable delivery.
func (a *BoardAmplifier) WithCanonicalDirectEnabled(enabled bool) *BoardAmplifier {
	if a == nil {
		return nil
	}
	a.canonicalDirectEnabled = enabled
	return a
}

// WithErrorSink wires a callback that receives emission-failure
// messages. Used by the board to forward into notificationErrors.
func (a *BoardAmplifier) WithErrorSink(sink func(message string)) *BoardAmplifier {
	if a == nil {
		return nil
	}
	a.errorSink = sink
	return a
}

// ────────────────────────────────────────────────────────────────────
// Fabric activity emissions (existing surface — preserved)
// ────────────────────────────────────────────────────────────────────

// EmitActionPosted emits a Fabric activity for a newly posted action.
func (a *BoardAmplifier) EmitActionPosted(ctx context.Context, action *Action) {
	if a == nil || action == nil {
		return
	}
	a.emit(ctx, activity.ActionActionPosted, action.AgentID, action.ID, map[string]any{
		"action_type": string(action.Type),
		"agent_id":    action.AgentID,
	})
}

// EmitClaimIssued emits a Fabric activity for a newly issued claim.
func (a *BoardAmplifier) EmitClaimIssued(ctx context.Context, claim *Claim) {
	if a == nil || claim == nil {
		return
	}
	a.emit(ctx, activity.ActionClaimIssued, claim.AgentID, claim.ID, map[string]any{
		"title":       claim.Title,
		"description": claim.Description,
		"action_type": string(claim.ActionType),
		"status":      string(claim.Status),
	})
}

// EmitClaimUpdated emits a Fabric activity for a claim progress update.
func (a *BoardAmplifier) EmitClaimUpdated(ctx context.Context, claim *Claim, agentID string) {
	if a == nil || claim == nil {
		return
	}
	a.emit(ctx, activity.ActionClaimUpdated, agentID, claim.ID, map[string]any{
		"title":  claim.Title,
		"status": string(claim.Status),
	})
}

// EmitTestamentSubmitted emits a Fabric activity for a submitted testament.
func (a *BoardAmplifier) EmitTestamentSubmitted(ctx context.Context, testament *Testament) {
	if a == nil || testament == nil {
		return
	}
	a.emit(ctx, activity.ActionTestamentSubmitted, testament.AgentID, testament.ID, map[string]any{
		"summary":    testament.Summary,
		"confidence": testament.Confidence,
	})
}

// EmitArtifactPublished emits a Fabric activity for each artifact.
func (a *BoardAmplifier) EmitArtifactPublished(ctx context.Context, artifact *Artifact) {
	if a == nil || artifact == nil {
		return
	}
	payload := map[string]any{
		"kind":      artifact.Kind,
		"reference": artifact.Reference,
		"ephemeral": artifact.Ephemeral,
	}
	addPresentationPayload(payload, artifact.Presentation)
	a.emit(ctx, activity.ActionClaimArtifactPublished, artifact.AgentID, artifact.ID, payload)
}

// EmitClaimValidated emits a Fabric activity for a validation evaluation.
func (a *BoardAmplifier) EmitClaimValidated(ctx context.Context, validation *Validation, agentID string) {
	if a == nil || validation == nil {
		return
	}
	a.emit(ctx, activity.ActionClaimValidated, agentID, validation.ID, map[string]any{
		"description": validation.Description,
		"status":      string(validation.Status),
		"type":        string(validation.Type),
	})
}

// EmitClaimAccepted emits a Fabric activity when a claim is accepted.
func (a *BoardAmplifier) EmitClaimAccepted(ctx context.Context, claim *Claim) {
	if a == nil || claim == nil {
		return
	}
	a.emit(ctx, activity.ActionClaimAccepted, claim.AgentID, claim.ID, map[string]any{
		"title": claim.Title,
	})
}

// EmitClaimRejected emits a Fabric activity when a claim is rejected.
func (a *BoardAmplifier) EmitClaimRejected(ctx context.Context, claim *Claim) {
	if a == nil || claim == nil {
		return
	}
	a.emit(ctx, activity.ActionClaimRejected, claim.AgentID, claim.ID, map[string]any{
		"title": claim.Title,
	})
}

// EmitCorrectiveIssued emits a Fabric activity for corrective claims.
func (a *BoardAmplifier) EmitCorrectiveIssued(ctx context.Context, action *Action) {
	if a == nil || action == nil {
		return
	}
	a.emit(ctx, activity.ActionCorrectiveIssued, action.AgentID, action.ID, map[string]any{
		"action_type": string(action.Type),
	})
}

// EmitBoardPhaseChanged emits a Fabric activity for phase transitions.
func (a *BoardAmplifier) EmitBoardPhaseChanged(ctx context.Context, phase BoardPhase, iteration int, agentID string) {
	if a == nil {
		return
	}
	a.emit(ctx, activity.ActionBoardPhaseChanged, agentID, a.boardID, map[string]any{
		"phase":     string(phase),
		"iteration": iteration,
	})
}

// EmitBoardComplete emits the terminal Fabric activity when the board completes.
func (a *BoardAmplifier) EmitBoardComplete(ctx context.Context, agentID string) {
	if a == nil {
		return
	}
	a.emit(ctx, activity.ActionBoardComplete, agentID, a.boardID, nil)
}

func (a *BoardAmplifier) emit(_ context.Context, kind activity.ActionKind, agentID, targetArtifact string, payload map[string]any) {
	var payloadJSON json.RawMessage
	if payload != nil {
		if data, err := json.Marshal(payload); err == nil {
			payloadJSON = data
		}
	}
	act := activity.AgentActivity{
		ID:         activity.ActivityID(generateActivityID()),
		SessionID:  activity.SessionID(a.sessionID),
		Timestamp:  time.Now().UTC(),
		Resolution: activity.ResolutionFor(kind),
		Actor: activity.Actor{
			AgentID:    agentID,
			AgentType:  claimsActivityActorType(agentID),
			PipelineID: a.boardID,
		},
		Action: kind,
		Subject: activity.Subject{
			TargetArtifact: targetArtifact,
			Coordinates: map[string]string{
				"task_id":  a.taskID,
				"board_id": a.boardID,
			},
		},
		Payload:     payloadJSON,
		State:       activity.StatePoint,
		SourceTable: "claims_board",
		SourceID:    targetArtifact,
	}
	activity.Append(context.Background(), act)
}

// ────────────────────────────────────────────────────────────────────
// Bus delta emissions
// ────────────────────────────────────────────────────────────────────

// PublishInboxDeltas emits claim.posted lifecycle deltas for each
// directed-agent relation on the claim. The retained method name is a
// compatibility call site; it no longer publishes legacy InboxDelta
// workflow inputs.
func (a *BoardAmplifier) PublishInboxDeltas(ctx context.Context, action *Action, claim *Claim) {
	if a == nil || action == nil || claim == nil {
		return
	}
	a.dispatchCanonical(ctx, a.buildClaimLifecycleDeltas(ctx, action, claim, ClaimLifecyclePosted, action.AgentID, time.Now().UTC()))
}

// PublishTestamentDelta emits testament.posted lifecycle deltas. The
// retained method name is a compatibility call site; it no longer
// publishes legacy TestamentDelta workflow inputs.
func (a *BoardAmplifier) PublishTestamentDelta(ctx context.Context, testament *Testament, claim *Claim) {
	if a == nil || testament == nil || claim == nil {
		return
	}
	if IsSystemInternalAction(claim.ActionType) {
		return
	}
	a.dispatchCanonical(ctx, a.buildTestamentLifecycleDeltas(ctx, testament, claim, TestamentLifecyclePosted, testament.AgentID, time.Now().UTC()))
}

// PublishCanonicalValidationEvaluated emits the canonical validation
// fact with full validation context.
func (a *BoardAmplifier) PublishCanonicalValidationEvaluated(ctx context.Context, claim *Claim, validation *Validation, change StatusChange, accepted bool, now time.Time) {
	if a == nil || claim == nil || validation == nil {
		return
	}
	if IsSystemInternalAction(claim.ActionType) {
		return
	}
	delta := a.buildCanonicalValidationEvaluated(ctx, claim, validation, change, accepted, now)
	a.dispatchCanonical(ctx, []canonicalDispatch{
		{
			topic: CanonicalValidationTopic(a.sessionID, validation.ID, DeltaActionValidationEvaluated),
			delta: delta,
		},
		{
			topic: CanonicalClaimTopic(a.sessionID, claim.ID, DeltaActionValidationEvaluated),
			delta: delta,
		},
	})
}

func (a *BoardAmplifier) PublishCanonicalClaimProgressed(ctx context.Context, claim *Claim, agentID, state, message string, transitionID int64, now time.Time) {
	if a == nil || claim == nil {
		return
	}
	if IsSystemInternalAction(claim.ActionType) {
		return
	}
	delta := NewCanonicalDelta(
		DeltaActionClaimProgressed,
		a.sessionID,
		a.boardID,
		claim.Sequence,
		now,
		a.resolveAgentRef(ctx, agentID, "legacy claim progress actor"),
		claimRefs(claim.ID),
		nil,
		map[string]any{
			"claim": map[string]any{
				"id":               claim.ID,
				"action":           string(claim.ActionType),
				"status":           string(claim.Status),
				"lifecycle_status": string(ClaimLifecycleProgressed),
			},
			"progress": map[string]any{
				"state":      strings.TrimSpace(state),
				"message":    strings.TrimSpace(message),
				"transition": transitionID,
			},
		},
	)
	a.dispatchCanonical(ctx, []canonicalDispatch{{
		topic: CanonicalClaimTopic(a.sessionID, claim.ID, DeltaActionClaimProgressed),
		delta: delta,
	}})
}

// PublishClaimStatusDelta maps the coarse compatibility status fact to
// the canonical claim lifecycle delta. The retained method name is a
// compatibility call site; it does not publish legacy ClaimStatusDelta
// workflow inputs.
func (a *BoardAmplifier) PublishClaimStatusDelta(ctx context.Context, delta ClaimStatusDelta) {
	if a == nil {
		return
	}
	if IsSystemInternalAction(delta.ActionKind) {
		return
	}
	lifecycle := claimLifecycleForCoarseStatus(delta.ToStatus)
	if lifecycle == "" {
		return
	}
	a.dispatchCanonical(ctx, []canonicalDispatch{{
		topic: CanonicalClaimTopic(a.sessionID, delta.ClaimID, mustClaimLifecycleDeltaAction(lifecycle)),
		delta: a.buildCanonicalClaimLifecycleFromStatusDelta(ctx, delta, lifecycle),
	}})
}

// PublishPhaseDelta emits a PhaseDelta on the phase topic.
func (a *BoardAmplifier) PublishPhaseDelta(ctx context.Context, delta PhaseDelta) {
	if a == nil {
		return
	}
	topic := PhaseTopic(a.sessionID, delta.ToPhase)
	a.dispatchSingle(ctx, topic, delta)
}

// PublishClaimContextDelta emits a ClaimContextDelta on the per-claim
// context topic. System-internal action types are filtered to keep
// system claims' narrative updates out of agent-waking subscription
// firehoses (consistent with the rest of the amplifier). UI's
// RoleObserver inbox subscribes via wildcard ClaimContextPattern.
func (a *BoardAmplifier) PublishClaimContextDelta(ctx context.Context, delta ClaimContextDelta) {
	if a == nil {
		return
	}
	if IsSystemInternalAction(delta.ActionKind) {
		return
	}
	a.dispatchCanonical(ctx, []canonicalDispatch{{
		topic: CanonicalClaimTopic(a.sessionID, delta.ClaimID, DeltaActionClaimProgressed),
		delta: NewCanonicalDelta(
			DeltaActionClaimProgressed,
			delta.SessionID,
			delta.BoardID,
			delta.Sequence,
			delta.EmittedAt,
			a.resolveAgentRef(ctx, delta.OwnerAgentID, "legacy claim progress actor"),
			append(claimRefs(delta.ClaimID), DeltaRef{Role: "progress", Type: "claim_progress", ID: fmt.Sprintf("%d", delta.TransitionID)}),
			nil,
			map[string]any{
				"claim": map[string]any{
					"id":               delta.ClaimID,
					"action":           string(delta.ActionKind),
					"status":           "",
					"lifecycle_status": string(ClaimLifecycleProgressed),
				},
				"progress": map[string]any{
					"state":      "context",
					"message":    delta.Context,
					"transition": delta.TransitionID,
				},
			},
		),
	}})
	topic := ClaimContextTopic(a.sessionID, delta.ClaimID)
	a.dispatchSingle(ctx, topic, delta)
}

// PublishTestamentContextDelta emits a TestamentContextDelta on the
// testament-anchor topic (AccumulatorID before flush, TestamentID
// after). UI's RoleObserver inbox subscribes via wildcard
// TestamentContextPattern.
func (a *BoardAmplifier) PublishTestamentContextDelta(ctx context.Context, delta TestamentContextDelta) {
	if a == nil {
		return
	}
	topic := TestamentContextTopic(a.sessionID, delta.TestamentID, delta.AccumulatorID)
	a.dispatchSingle(ctx, topic, delta)
}

// ────────────────────────────────────────────────────────────────────
// Delta builders
// ────────────────────────────────────────────────────────────────────

type canonicalDispatch struct {
	topic string
	delta CanonicalDelta
}

func (a *BoardAmplifier) buildClaimLifecycleDeltas(ctx context.Context, action *Action, claim *Claim, status ClaimLifecycleStatus, actorID string, occurredAt time.Time) []canonicalDispatch {
	if action != nil && IsSystemInternalAction(action.Type) {
		return nil
	}
	if claim != nil && IsSystemInternalAction(claim.ActionType) {
		return nil
	}
	deltaAction, ok := ClaimLifecycleDeltaAction(status)
	if !ok {
		return nil
	}
	issuerID := ""
	if action != nil {
		issuerID = action.AgentID
	}
	if issuerID == "" {
		issuerID = IssuerAgentID(claim.Relations)
	}
	actor := a.resolveAgentRef(ctx, firstNonEmpty(actorID, issuerID, claim.AgentID), "claim lifecycle actor")
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	refs := append(actionRefs(ClaimActionID(claim.Relations)), DeltaRef{Role: "claim", Type: RelatedTypeClaim, ID: claim.ID})
	if action != nil && action.ID != "" {
		refs = append(actionRefs(action.ID), DeltaRef{Role: "claim", Type: RelatedTypeClaim, ID: claim.ID})
	}
	if status != ClaimLifecyclePosted {
		delivery := a.claimLifecycleDelivery(ctx, claim, status, actorID)
		return []canonicalDispatch{{
			topic: CanonicalClaimTopic(a.sessionID, claim.ID, deltaAction),
			delta: a.buildCanonicalClaimLifecycle(ctx, claim, deltaAction, status, occurredAt, actor, refs, delivery),
		}}
	}
	var out []canonicalDispatch
	for _, r := range claim.Relations {
		if r.RelatedType != RelatedTypeAgent || !isDirectedAgentRelationship(r.Relationship) {
			continue
		}
		if r.Related == issuerID && HandoffFromClaimID(claim.Relations) == "" {
			continue
		}
		target := a.resolveAgentRef(ctx, r.Related, "legacy directed relation")
		delivery := &DeltaDelivery{
			To:           []AgentRef{target},
			Relationship: r.Relationship,
		}
		delta := a.buildCanonicalClaimLifecycle(ctx, claim, deltaAction, status, occurredAt, actor, refs, delivery)
		out = append(out, canonicalDispatch{
			topic: CanonicalAgentRefTopic(a.sessionID, target, deltaAction),
			delta: delta,
		})
	}
	return out
}

func (a *BoardAmplifier) claimLifecycleDelivery(ctx context.Context, claim *Claim, status ClaimLifecycleStatus, actorID string) *DeltaDelivery {
	if claim == nil {
		return nil
	}
	relationship := ""
	switch status {
	case ClaimLifecycleReceived, ClaimLifecycleReceiptFailed:
		relationship = RelationshipSubject
	case ClaimLifecycleTestamentAcknowledged, ClaimLifecycleTestamentAcknowledgementFailed:
		relationship = firstMatchingAgentRelationship(claim.Relations, actorID, RelationshipIssuer, RelationshipEvaluator)
		if relationship == "" {
			relationship = RelationshipIssuer
		}
	default:
		return nil
	}
	targetID := strings.TrimSpace(actorID)
	if targetID == "" {
		targetID = firstAgentIDForRelationship(claim.Relations, relationship)
	}
	if targetID == "" {
		return nil
	}
	return &DeltaDelivery{
		To:           []AgentRef{a.resolveAgentRef(ctx, targetID, "claim lifecycle receiver")},
		Relationship: relationship,
	}
}

func firstMatchingAgentRelationship(relations []Relation, agentID string, relationships ...string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ""
	}
	for _, relationship := range relationships {
		for _, relation := range relations {
			if relation.RelatedType == RelatedTypeAgent &&
				relation.Relationship == relationship &&
				strings.TrimSpace(relation.Related) == agentID {
				return relationship
			}
		}
	}
	return ""
}

func firstAgentIDForRelationship(relations []Relation, relationship string) string {
	relationship = strings.TrimSpace(relationship)
	if relationship == "" {
		return ""
	}
	for _, relation := range relations {
		if relation.RelatedType == RelatedTypeAgent && relation.Relationship == relationship {
			return strings.TrimSpace(relation.Related)
		}
	}
	return ""
}

func (a *BoardAmplifier) buildCanonicalClaimLifecycle(_ context.Context, claim *Claim, action DeltaAction, status ClaimLifecycleStatus, occurredAt time.Time, actor AgentRef, refs []DeltaRef, delivery *DeltaDelivery) CanonicalDelta {
	return NewCanonicalDelta(
		action,
		a.sessionID,
		a.boardID,
		claim.Sequence,
		occurredAt,
		actor,
		refs,
		delivery,
		map[string]any{
			"claim": map[string]any{
				"id":                  claim.ID,
				"action":              string(claim.ActionType),
				"title":               claim.Title,
				"description":         claim.Description,
				"status":              string(claim.Status),
				"lifecycle_status":    string(status),
				"scope":               claimScopeContext(claim.Scope),
				"validations":         validationContext(claim.Validations),
				"expected_tool_calls": expectedToolCallContext(claim.ExpectedToolCalls),
			},
		},
	)
}

func (a *BoardAmplifier) buildTestamentLifecycleDeltas(ctx context.Context, testament *Testament, claim *Claim, status TestamentLifecycleStatus, actorID string, occurredAt time.Time) []canonicalDispatch {
	action, ok := TestamentLifecycleDeltaAction(status)
	if !ok || testament == nil {
		return nil
	}
	delta := a.buildCanonicalTestamentLifecycle(ctx, testament, claim, action, status, actorID, occurredAt)
	if claim == nil || strings.TrimSpace(claim.ID) == "" {
		return []canonicalDispatch{{topic: CanonicalBoardTopic(a.sessionID, a.boardID, action), delta: delta}}
	}
	out := []canonicalDispatch{{topic: CanonicalClaimTopic(a.sessionID, claim.ID, action), delta: delta}}
	if status == TestamentLifecyclePosted || status == TestamentLifecycleReceived {
		issuer := a.resolveAgentRef(ctx, IssuerAgentID(claim.Relations), "claim issuer")
		out = append(out, canonicalDispatch{topic: CanonicalAgentRefTopic(a.sessionID, issuer, action), delta: delta})
	}
	return out
}

func (a *BoardAmplifier) buildCanonicalTestamentLifecycle(ctx context.Context, testament *Testament, claim *Claim, action DeltaAction, status TestamentLifecycleStatus, actorID string, occurredAt time.Time) CanonicalDelta {
	actor := a.resolveAgentRef(ctx, firstNonEmpty(actorID, testament.AgentID), "testament lifecycle actor")
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	refs := claimRefs("")
	if claim != nil {
		refs = claimRefs(claim.ID)
	}
	refs = append(refs, DeltaRef{Role: "testament", Type: RelatedTypeTestament, ID: testament.ID})
	for _, artifact := range testament.Artifacts {
		if artifact == nil || strings.TrimSpace(artifact.ID) == "" {
			continue
		}
		refs = append(refs, DeltaRef{Role: "artifact", Type: RelatedTypeArtifact, ID: artifact.ID})
	}
	testamentContext := compactTestamentDeltaContext(testament)
	testamentEntry := map[string]any{
		"id":               testament.ID,
		"verdict":          DeriveTestamentVerdict(testament.Artifacts),
		"confidence":       testament.Confidence,
		"context":          testamentContext.Value,
		"lifecycle_status": string(status),
		"artifacts":        artifactHeadersContext(testament.Artifacts),
	}
	if testamentContext.Truncated {
		testamentEntry["context_truncated"] = true
		if testamentContext.ArtifactID != "" {
			testamentEntry["context_artifact_id"] = testamentContext.ArtifactID
			testamentEntry["context_artifact_kind"] = testamentContext.ArtifactKind
		}
	}
	claimEntry := map[string]any{}
	delivery := (*DeltaDelivery)(nil)
	if claim != nil {
		claimEntry = map[string]any{
			"id":                  claim.ID,
			"action":              string(claim.ActionType),
			"status":              string(claim.Status),
			"lifecycle_status":    string(claim.LifecycleStatus),
			"validations":         validationContext(claim.Validations),
			"expected_tool_calls": expectedToolCallContext(claim.ExpectedToolCalls),
		}
		switch status {
		case TestamentLifecyclePosted:
			delivery = &DeltaDelivery{To: []AgentRef{a.resolveAgentRef(ctx, IssuerAgentID(claim.Relations), "claim issuer")}, Relationship: RelationshipIssuer}
		case TestamentLifecycleReceived:
			relationship := firstMatchingAgentRelationship(claim.Relations, actorID, RelationshipIssuer, RelationshipEvaluator)
			if relationship == "" {
				relationship = RelationshipIssuer
			}
			targetID := firstNonEmpty(actorID, firstAgentIDForRelationship(claim.Relations, relationship))
			if targetID != "" {
				delivery = &DeltaDelivery{To: []AgentRef{a.resolveAgentRef(ctx, targetID, "testament lifecycle receiver")}, Relationship: relationship}
			}
		}
	}
	return NewCanonicalDelta(
		action,
		a.sessionID,
		a.boardID,
		testament.Sequence,
		occurredAt,
		actor,
		refs,
		delivery,
		map[string]any{
			"claim":      claimEntry,
			"testament":  testamentEntry,
			"testaments": []map[string]any{testamentEntry},
		},
	)
}

func (a *BoardAmplifier) buildCanonicalValidationEvaluated(ctx context.Context, claim *Claim, validation *Validation, change StatusChange, accepted bool, now time.Time) CanonicalDelta {
	status := ValidationStatus(change.To)
	remaining := claim.PendingValidationCount()
	return NewCanonicalDelta(
		DeltaActionValidationEvaluated,
		a.sessionID,
		a.boardID,
		validation.Sequence,
		now,
		a.resolveAgentRef(ctx, change.AgentID, "legacy validation evaluator"),
		append(claimRefs(claim.ID), DeltaRef{Role: "validation", Type: RelatedTypeValidation, ID: validation.ID}),
		nil,
		map[string]any{
			"claim": map[string]any{
				"id":     claim.ID,
				"action": string(claim.ActionType),
				"status": string(claim.Status),
			},
			"validation": map[string]any{
				"id":                  validation.ID,
				"type":                string(validation.Type),
				"status":              string(status),
				"required":            validation.Required,
				"reason":              change.Reason,
				"remaining_required":  remaining,
				"claim_auto_accepted": accepted,
				"expected_tool_calls": expectedToolCallContext(validation.ExpectedToolCalls),
			},
		},
	)
}

func (a *BoardAmplifier) buildCanonicalClaimLifecycleFromStatusDelta(ctx context.Context, delta ClaimStatusDelta, lifecycle ClaimLifecycleStatus) CanonicalDelta {
	action := mustClaimLifecycleDeltaAction(lifecycle)
	return NewCanonicalDelta(
		action,
		delta.SessionID,
		delta.BoardID,
		delta.Sequence,
		delta.EmittedAt,
		a.resolveAgentRef(ctx, delta.AgentID, "legacy claim status actor"),
		claimRefs(delta.ClaimID),
		nil,
		map[string]any{
			"claim": map[string]any{
				"id":               delta.ClaimID,
				"action":           string(delta.ActionKind),
				"from_status":      string(delta.FromStatus),
				"to_status":        string(delta.ToStatus),
				"lifecycle_status": string(lifecycle),
				"reason":           delta.Reason,
			},
		},
	)
}

// ────────────────────────────────────────────────────────────────────
// Dispatch helpers
// ────────────────────────────────────────────────────────────────────

func (a *BoardAmplifier) dispatchSingle(ctx context.Context, topic string, delta Delta) {
	publisher := a.deltaBus
	emit := func(runCtx context.Context) error {
		if err := publisher.PublishDelta(runCtx, topic, delta); err != nil {
			a.reportEmitError("delta_publish_failed", topic, err)
		}
		return nil
	}
	a.runTracked(ctx, "claims_amplifier_delta", emit)
}

func (a *BoardAmplifier) dispatchCanonical(ctx context.Context, deltas []canonicalDispatch) {
	if a == nil || !a.canonicalDirectEnabled {
		return
	}
	if len(deltas) == 0 {
		return
	}
	deltas = a.canonicalDispatchesWithBoardTopic(deltas)
	publisher := a.deltaBus
	emit := func(runCtx context.Context) error {
		return a.publishCanonicalBatch(runCtx, publisher, deltas)
	}
	a.runTracked(ctx, "claims_amplifier_canonical_delta", emit)
}

func (a *BoardAmplifier) publishCanonicalBatch(ctx context.Context, publisher DeltaPublisher, deltas []canonicalDispatch) error {
	var firstErr error
	for _, d := range deltas {
		if err := publisher.PublishDelta(ctx, d.topic, d.delta); err != nil {
			a.reportEmitError("canonical_delta_publish_failed", d.topic, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (a *BoardAmplifier) canonicalDispatchesWithBoardTopic(deltas []canonicalDispatch) []canonicalDispatch {
	if a == nil || len(deltas) == 0 || strings.TrimSpace(a.boardID) == "" {
		return deltas
	}
	out := make([]canonicalDispatch, 0, len(deltas)*2)
	seen := make(map[string]struct{}, len(deltas)*2)
	add := func(d canonicalDispatch) {
		key := d.topic + "\x00" + d.delta.DeltaKey()
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, d)
	}
	for _, d := range deltas {
		add(d)
		boardTopic := CanonicalBoardTopic(a.sessionID, a.boardID, d.delta.Action)
		if boardTopic != d.topic {
			add(canonicalDispatch{topic: boardTopic, delta: d.delta})
		}
	}
	return out
}

// runTracked dispatches fn under scope as a tracked async goroutine.
// Scope is mandatory; a nil scope is a programming error at
// construction and the emission is dropped with a structured error
// so the failure is observable rather than silently inlined onto the
// caller's hot path. Per the substrate's "async by default, always
// tracked" invariant, there is no sync fallback.
func (a *BoardAmplifier) runTracked(_ context.Context, description string, fn func(context.Context) error) {
	if a.scope == nil {
		a.reportEmitError("amplifier_scope_unwired", description,
			fmt.Errorf("amplifier scope not configured; emission dropped"))
		return
	}
	if err := a.scope.Go(description, amplifierEmitTimeout, fn); err != nil {
		a.reportEmitError("scope_dispatch_failed", description, err)
	}
}

func (a *BoardAmplifier) reportEmitError(event, topic string, err error) {
	slog.Error(event,
		"topic", topic,
		"session_id", a.sessionID,
		"board_id", a.boardID,
		"err", err.Error(),
	)
	if a.errorSink != nil {
		a.errorSink(event + ": " + topic + ": " + err.Error())
	}
}

// ────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────

// isDirectedAgentRelationship reports whether r should trigger a
// receiver-specific claim.posted lifecycle delta. Subject and evaluator
// relations are directed; issuer is the actor (not a recipient) and
// receives no posted-work delta.
func isDirectedAgentRelationship(relationship string) bool {
	return relationship == RelationshipSubject || relationship == RelationshipEvaluator
}

func claimLifecycleForCoarseStatus(status ClaimStatus) ClaimLifecycleStatus {
	switch status {
	case ClaimStatusPending:
		return ClaimLifecyclePosted
	case ClaimStatusInProgress:
		return ClaimLifecycleProgressed
	case ClaimStatusTestified:
		return ClaimLifecycleTestamentGenerated
	case ClaimStatusAccepted:
		return ClaimLifecycleSatisfied
	case ClaimStatusRejected:
		return ClaimLifecycleValidationFailed
	default:
		return ""
	}
}

func mustClaimLifecycleDeltaAction(status ClaimLifecycleStatus) DeltaAction {
	action, ok := ClaimLifecycleDeltaAction(status)
	if !ok {
		return DeltaActionClaimProgressed
	}
	return action
}

func collectRelatedIDs(relations []Relation, relationship string) []string {
	var out []string
	for _, r := range relations {
		if r.Relationship == relationship && r.Related != "" {
			out = append(out, r.Related)
		}
	}
	return out
}

func (a *BoardAmplifier) resolveAgentRef(ctx context.Context, agentID, degradedReason string) AgentRef {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return DegradedAgentRef("unknown", firstNonEmpty(degradedReason, "missing agent reference"))
	}
	if a != nil && a.agentRefResolver != nil {
		if ref, ok := a.agentRefResolver.ResolveAgentRef(ctx, a.sessionID, agentID); ok {
			ref = ref.Normalized()
			if err := ref.Validate(); err == nil && ref.MatchesAgentID(agentID) {
				return ref
			}
			reason := "identity resolver returned invalid or mismatched ref"
			if degradedReason != "" {
				reason = degradedReason + ": " + reason
			}
			return DegradedAgentRef(agentID, reason)
		}
	}
	return DegradedAgentRef(agentID, degradedReason)
}

func copyScope(src []ClaimScopeEntry) []ClaimScopeEntry {
	if len(src) == 0 {
		return nil
	}
	out := make([]ClaimScopeEntry, len(src))
	copy(out, src)
	return out
}

func actionRefs(actionID string) []DeltaRef {
	actionID = strings.TrimSpace(actionID)
	if actionID == "" {
		return nil
	}
	return []DeltaRef{{Role: "action", Type: RelatedTypeAction, ID: actionID}}
}

func claimRefs(claimID string) []DeltaRef {
	claimID = strings.TrimSpace(claimID)
	if claimID == "" {
		return nil
	}
	return []DeltaRef{{Role: "claim", Type: RelatedTypeClaim, ID: claimID}}
}

func claimScopeContext(scope []ClaimScopeEntry) []map[string]string {
	if len(scope) == 0 {
		return nil
	}
	out := make([]map[string]string, 0, len(scope))
	for _, entry := range scope {
		kind := strings.TrimSpace(entry.Kind)
		key := strings.TrimSpace(entry.Key)
		if kind == "" && key == "" {
			continue
		}
		out = append(out, map[string]string{"kind": kind, "key": key})
	}
	return out
}

func validationContext(validations []*Validation) []map[string]any {
	if len(validations) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(validations))
	for _, validation := range validations {
		if validation == nil {
			continue
		}
		entry := map[string]any{
			"id":          validation.ID,
			"type":        string(validation.Type),
			"required":    validation.Required,
			"description": validation.Description,
			"quality_bar": validation.QualityBar,
			"status":      string(validation.Status),
		}
		if tools := expectedToolCallContext(validation.ExpectedToolCalls); len(tools) > 0 {
			entry["expected_tool_calls"] = tools
		}
		out = append(out, entry)
	}
	return out
}

func artifactHeadersContext(artifacts []*Artifact) []map[string]any {
	if len(artifacts) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		header := map[string]any{
			"id":        artifact.ID,
			"kind":      artifact.Kind,
			"ephemeral": artifact.Ephemeral,
		}
		if artifact.ContentHash != "" {
			header["content_hash"] = artifact.ContentHash
		}
		if artifact.Size > 0 {
			header["size"] = artifact.Size
		}
		if presentation := presentationHeaderContext(artifact.Presentation); len(presentation) > 0 {
			header["presentation"] = presentation
		}
		out = append(out, header)
	}
	return out
}

const canonicalTestamentContextMax = 4096

type compactTestamentContext struct {
	Value        string
	Truncated    bool
	ArtifactID   string
	ArtifactKind string
}

func compactTestamentDeltaContext(testament *Testament) compactTestamentContext {
	if testament == nil {
		return compactTestamentContext{}
	}
	value := firstNonEmpty(testament.Context, testament.Summary)
	out := compactTestamentContext{Value: value}
	if len(value) <= canonicalTestamentContextMax {
		return out
	}
	out.Value = value[:canonicalTestamentContextMax] + "…"
	out.Truncated = true
	if artifact := firstContextReferenceArtifact(testament.Artifacts); artifact != nil {
		out.ArtifactID = strings.TrimSpace(artifact.ID)
		out.ArtifactKind = strings.TrimSpace(artifact.Kind)
	}
	return out
}

func firstContextReferenceArtifact(artifacts []*Artifact) *Artifact {
	for _, artifact := range artifacts {
		if artifact != nil && strings.TrimSpace(artifact.ID) != "" && artifact.Kind == ArtifactKindResponseText {
			return artifact
		}
	}
	for _, artifact := range artifacts {
		if artifact != nil && strings.TrimSpace(artifact.ID) != "" {
			return artifact
		}
	}
	return nil
}

func presentationHeaderContext(p *Presentation) map[string]any {
	normalized := NormalizePresentation(p)
	if normalized == nil {
		return nil
	}
	out := map[string]any{}
	if len(normalized.Audiences) > 0 {
		audiences := make([]string, 0, len(normalized.Audiences))
		for _, audience := range normalized.Audiences {
			audiences = append(audiences, string(audience))
		}
		out["audiences"] = audiences
	}
	if len(normalized.Surfaces) > 0 {
		surfaces := make([]string, 0, len(normalized.Surfaces))
		for _, surface := range normalized.Surfaces {
			surfaces = append(surfaces, string(surface))
		}
		out["surfaces"] = surfaces
	}
	if normalized.Format != "" {
		out["format"] = string(normalized.Format)
	}
	if normalized.Title != "" {
		out["title"] = normalized.Title
	}
	if normalized.Placement != "" {
		out["placement"] = string(normalized.Placement)
	}
	if normalized.ReplaceKey != "" {
		out["replace_key"] = normalized.ReplaceKey
	}
	if normalized.Priority != 0 {
		out["priority"] = normalized.Priority
	}
	return out
}

// EvaluatorAgentID returns the agent on the first evaluator Relation,
// or empty. Complements IssuerAgentID / SubjectAgentID in types.go.
func EvaluatorAgentID(relations []Relation) string {
	r := FindRelation(relations, RelationshipEvaluator)
	if r == nil {
		return ""
	}
	return r.Related
}

func generateActivityID() string {
	return "act_" + uuid.NewString()[:12]
}
