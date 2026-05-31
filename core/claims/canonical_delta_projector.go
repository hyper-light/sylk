package claims

import (
	"context"
	"strings"
	"time"
)

// CanonicalDeltaProjector republishes canonical bus deltas from durable
// outbox records. It makes Guide-bus delivery recoverable after a
// process dies between board commit and immediate publication.
type CanonicalDeltaProjector struct {
	publisher DeltaPublisher
	resolver  AgentRefResolver
}

func NewCanonicalDeltaProjector(publisher DeltaPublisher, resolver AgentRefResolver) *CanonicalDeltaProjector {
	return &CanonicalDeltaProjector{publisher: publishOrNoop(publisher), resolver: resolver}
}

func (p *CanonicalDeltaProjector) Name() string { return ProjectorCanonicalDelta }

func (p *CanonicalDeltaProjector) Project(ctx context.Context, record *ClaimsOutboxRecord, board *ClaimsBoard) error {
	if p == nil || record == nil || board == nil {
		return nil
	}
	amp := NewBoardAmplifier(record.SessionID, record.TaskID, record.BoardID).
		WithDeltaBus(p.publisher).
		WithAgentRefResolver(firstNonNilAgentRefResolver(p.resolver, board.agentRefResolver)).
		WithMetricsSink(board.metrics).
		WithCanonicalDirectEnabled(true)
	dispatches := amp.canonicalDispatchesForOutboxRecord(ctx, record, board)
	if len(dispatches) == 0 {
		return nil
	}
	dispatches = amp.canonicalDispatchesWithBoardTopic(dispatches)
	return amp.publishCanonicalBatch(ctx, p.publisher, dispatches)
}

func firstNonNilAgentRefResolver(values ...AgentRefResolver) AgentRefResolver {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func (a *BoardAmplifier) canonicalDispatchesForOutboxRecord(ctx context.Context, record *ClaimsOutboxRecord, board *ClaimsBoard) []canonicalDispatch {
	if a == nil || record == nil || board == nil {
		return nil
	}
	occurredAt := firstNonZeroTime(record.CreatedAt, time.Now().UTC())
	if status, ok := DeltaActionClaimLifecycleStatus(DeltaAction(record.MutationKind)); ok {
		claim, ok := board.CloneClaim(record.EntityID)
		if !ok || IsSystemInternalAction(claim.ActionType) {
			return nil
		}
		action := actionForClaimRecord(board, claim)
		actorID := firstNonEmpty(latestClaimLifecycleChange(claim).AgentID, IssuerAgentID(claim.Relations), claim.AgentID)
		return a.buildClaimLifecycleDeltas(ctx, action, claim, status, actorID, occurredAt)
	}
	if status, ok := DeltaActionTestamentLifecycleStatus(DeltaAction(record.MutationKind)); ok {
		testament, ok := board.CloneTestament(record.EntityID)
		if !ok {
			return nil
		}
		claim, _ := board.CloneClaim(ClaimIDFromRelations(testament.Relations))
		if claim != nil && IsSystemInternalAction(claim.ActionType) {
			return nil
		}
		actorID := firstNonEmpty(latestTestamentLifecycleChange(testament).AgentID, testament.AgentID)
		dispatches := a.buildTestamentLifecycleDeltas(ctx, testament, claim, status, actorID, occurredAt)
		if status == TestamentLifecyclePosted && claim != nil && claim.LifecycleStatus != "" {
			dispatches = append(dispatches, a.buildClaimLifecycleDeltas(ctx, actionForClaimRecord(board, claim), claim, claim.LifecycleStatus, actorID, occurredAt)...)
		}
		return dispatches
	}
	if status, ok := DeltaActionArtifactLifecycleStatus(DeltaAction(record.MutationKind)); ok {
		artifact, testament, claim, ok := board.cloneArtifactWithParents(record.EntityID)
		if !ok {
			return nil
		}
		if claim != nil && IsSystemInternalAction(claim.ActionType) {
			return nil
		}
		actorID := firstNonEmpty(artifactStatusChangeFor(artifact, status).AgentID, artifact.ParticipantID, artifact.AgentID)
		return a.buildArtifactLifecycleDeltas(ctx, artifact, testament, claim, status, actorID, occurredAt)
	}
	if status, ok := DeltaActionValidationLifecycleStatus(DeltaAction(record.MutationKind)); ok {
		validation, claim, ok := board.CloneValidation(record.EntityID)
		if !ok || IsSystemInternalAction(claim.ActionType) {
			return nil
		}
		artifact, _ := board.cloneValidationTargetArtifact(claim.ID, validation)
		actorID := firstNonEmpty(validationStatusChangeFor(validation, status).AgentID, validation.ParticipantID, validation.AgentID, validation.ValidatorID)
		return a.buildValidationLifecycleDeltas(ctx, claim, validation, artifact, status, actorID, occurredAt)
	}
	switch record.MutationKind {
	case "claim_issued":
		claim, ok := board.CloneClaim(record.EntityID)
		if !ok || IsSystemInternalAction(claim.ActionType) {
			return nil
		}
		action := actionForClaimRecord(board, claim)
		return a.buildClaimLifecycleDeltas(ctx, action, claim, ClaimLifecyclePosted, firstNonEmpty(IssuerAgentID(claim.Relations), claim.AgentID), occurredAt)
	case walEventClaimUpdated:
		claim, ok := board.CloneClaim(record.EntityID)
		if !ok || IsSystemInternalAction(claim.ActionType) {
			return nil
		}
		return []canonicalDispatch{{
			topic: CanonicalClaimTopic(a.sessionID, claim.ID, DeltaActionClaimProgressed),
			delta: a.buildCanonicalClaimProgressed(ctx, claim, occurredAt),
		}}
	case walEventTestamentSubmitted:
		testament, ok := board.CloneTestament(record.EntityID)
		if !ok {
			return nil
		}
		claimID := ClaimIDFromRelations(testament.Relations)
		if claimID == "" {
			return nil
		}
		claim, ok := board.CloneClaim(claimID)
		if !ok || IsSystemInternalAction(claim.ActionType) {
			return nil
		}
		return a.buildTestamentLifecycleDeltas(ctx, testament, claim, TestamentLifecyclePosted, testament.AgentID, occurredAt)
	case "artifact_published":
		artifact, testament, claim, ok := board.cloneArtifactWithParents(record.EntityID)
		if !ok {
			return nil
		}
		if claim != nil && IsSystemInternalAction(claim.ActionType) {
			return nil
		}
		return a.buildArtifactLifecycleDeltas(ctx, artifact, testament, claim, ArtifactStatusAttached, artifact.AgentID, occurredAt)
	case walEventValidationEvaluated:
		validation, claim, ok := board.CloneValidation(record.EntityID)
		if !ok || IsSystemInternalAction(claim.ActionType) {
			return nil
		}
		change := latestValidationChange(validation)
		accepted := claim.Status == ClaimStatusAccepted && claim.AllValidationsPassed()
		return []canonicalDispatch{
			{
				topic: CanonicalValidationTopic(a.sessionID, validation.ID, DeltaActionValidationEvaluated),
				delta: a.buildCanonicalValidationEvaluated(ctx, claim, validation, change, accepted, occurredAt),
			},
			{
				topic: CanonicalClaimTopic(a.sessionID, claim.ID, DeltaActionValidationEvaluated),
				delta: a.buildCanonicalValidationEvaluated(ctx, claim, validation, change, accepted, occurredAt),
			},
		}
	case walEventClaimAccepted, walEventClaimRejected:
		claim, ok := board.CloneClaim(record.EntityID)
		if !ok || IsSystemInternalAction(claim.ActionType) {
			return nil
		}
		lifecycle := claim.LifecycleStatus
		if lifecycle == "" {
			return nil
		}
		return a.buildClaimLifecycleDeltas(ctx, actionForClaimRecord(board, claim), claim, lifecycle, firstNonEmpty(latestClaimLifecycleChange(claim).AgentID, claim.AgentID), occurredAt)
	default:
		return nil
	}
}

func actionForClaimRecord(board *ClaimsBoard, claim *Claim) *Action {
	if claim == nil {
		return nil
	}
	actionID := ClaimActionID(claim.Relations)
	if actionID != "" {
		if action, ok := board.CloneAction(actionID); ok {
			return action
		}
	}
	return &Action{
		ID:      actionID,
		AgentID: firstNonEmpty(IssuerAgentID(claim.Relations), claim.AgentID),
		Type:    claim.ActionType,
	}
}

func (a *BoardAmplifier) buildCanonicalClaimProgressed(ctx context.Context, claim *Claim, occurredAt time.Time) CanonicalDelta {
	change := latestClaimChange(claim)
	agentID := firstNonEmpty(change.AgentID, IssuerAgentID(claim.Relations), claim.AgentID)
	message := firstNonEmpty(strings.TrimSpace(claim.Context), strings.TrimSpace(change.Reason), "work progressed")
	return NewCanonicalDelta(
		DeltaActionClaimProgressed,
		a.sessionID,
		a.boardID,
		claim.Sequence,
		occurredAt,
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
				"state":      string(claim.Status),
				"message":    message,
				"transition": claim.ContextTransition,
			},
		},
	)
}

func latestClaimChange(claim *Claim) StatusChange {
	if claim == nil || len(claim.StatusHistory) == 0 {
		return StatusChange{}
	}
	return claim.StatusHistory[len(claim.StatusHistory)-1]
}

func latestClaimLifecycleChange(claim *Claim) StatusChange {
	if claim == nil || len(claim.LifecycleHistory) == 0 {
		return StatusChange{}
	}
	return claim.LifecycleHistory[len(claim.LifecycleHistory)-1]
}

func latestTestamentLifecycleChange(testament *Testament) StatusChange {
	if testament == nil || len(testament.LifecycleHistory) == 0 {
		return StatusChange{}
	}
	return testament.LifecycleHistory[len(testament.LifecycleHistory)-1]
}

func latestValidationChange(validation *Validation) StatusChange {
	if validation == nil || len(validation.StatusHistory) == 0 {
		return StatusChange{}
	}
	return validation.StatusHistory[len(validation.StatusHistory)-1]
}
