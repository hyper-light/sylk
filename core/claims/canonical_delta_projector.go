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
	switch record.MutationKind {
	case "claim_issued":
		claim, ok := board.CloneClaim(record.EntityID)
		if !ok || IsSystemInternalAction(claim.ActionType) {
			return nil
		}
		action := actionForClaimRecord(board, claim)
		return a.buildClaimDirectedDeltas(ctx, action, claim, occurredAt)
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
		return []canonicalDispatch{{
			topic: CanonicalClaimTopic(a.sessionID, claim.ID, DeltaActionTestamentSubmitted),
			delta: a.buildCanonicalTestamentSubmitted(ctx, testament, claim, occurredAt),
		}}
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
		change := latestClaimChange(claim)
		if change.To == "" {
			return nil
		}
		statusDelta := ClaimStatusDelta{
			SessionID:      record.SessionID,
			BoardID:        record.BoardID,
			ClaimID:        claim.ID,
			Sequence:       claim.Sequence,
			EmittedAt:      occurredAt,
			ActionKind:     claim.ActionType,
			FromStatus:     ClaimStatus(change.From),
			ToStatus:       ClaimStatus(change.To),
			Reason:         change.Reason,
			AgentID:        change.AgentID,
			SubjectAgentID: SubjectAgentID(claim.Relations),
			IssuerAgentID:  IssuerAgentID(claim.Relations),
		}
		return []canonicalDispatch{{
			topic: CanonicalClaimTopic(a.sessionID, claim.ID, DeltaActionClaimTransitioned),
			delta: a.buildCanonicalClaimTransitioned(ctx, statusDelta),
		}}
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
				"id":     claim.ID,
				"action": string(claim.ActionType),
				"status": string(claim.Status),
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

func latestValidationChange(validation *Validation) StatusChange {
	if validation == nil || len(validation.StatusHistory) == 0 {
		return StatusChange{}
	}
	return validation.StatusHistory[len(validation.StatusHistory)-1]
}
