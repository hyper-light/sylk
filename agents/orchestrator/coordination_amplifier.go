package orchestrator

import (
	"context"
	"encoding/json"
	"time"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/pipeline/coordination"
)

// Activity Fabric coordination amplifier (Tier 2).
//
// The coordination service stays sovereign over its own claim,
// artifact, review, and remediation tables. After every successful
// commit, the amplifier emits a typed projection to the fabric. The
// fabric activity carries SourceTable + SourceID for back-reference;
// it does not duplicate the canonical row.
//
// Failure to emit must never block the primary store — emission is
// post-commit and uses the discard sink as a fallback when the fabric
// is disabled.

// emitClaimAcquired publishes a claim_acquired activity (Medium
// resolution) when ClaimScope/renewClaimLease succeeds.
func emitClaimAcquired(ctx context.Context, claim *coordination.Claim, actor coordination.Actor) {
	if claim == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"scope_kind":       string(claim.ScopeKind),
		"scope_key":        claim.ScopeKey,
		"mode":             string(claim.Mode),
		"task_name":        claim.TaskName,
		"lease_expires_at": claim.LeaseExpiresAt,
		"idempotency_key":  claim.IdempotencyKey,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(claim.TaskID),
		Timestamp:  claim.UpdatedAt,
		Resolution: activity.ResolutionFor(activity.ActionClaimAcquired),
		Action:     activity.ActionClaimAcquired,
		Actor: activity.Actor{
			AgentID:   actor.AgentID,
			AgentType: actor.AgentType,
		},
		Subject: activity.Subject{
			PathPrefix:     claim.ScopeKey,
			TargetArtifact: claim.ID,
		},
		Payload:     payload,
		State:       activity.StateInFlight,
		SourceTable: "coordination_claims",
		SourceID:    claim.ID,
	}
	activity.Append(ctx, act)
}

// emitClaimReleased publishes a claim_released activity that resolves
// the corresponding claim_acquired by Resolves linkage. A best-effort
// match — if the inflight claim_acquired isn't located, the released
// activity stands alone (still durable).
func emitClaimReleased(ctx context.Context, claim *coordination.Claim, actor coordination.Actor) {
	if claim == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"scope_kind":  string(claim.ScopeKind),
		"scope_key":   claim.ScopeKey,
		"resolved_at": claim.UpdatedAt,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(claim.TaskID),
		Timestamp:  claim.UpdatedAt,
		Resolution: activity.ResolutionFor(activity.ActionClaimReleased),
		Action:     activity.ActionClaimReleased,
		Actor: activity.Actor{
			AgentID:   actor.AgentID,
			AgentType: actor.AgentType,
		},
		Subject: activity.Subject{
			PathPrefix:     claim.ScopeKey,
			TargetArtifact: claim.ID,
		},
		Payload:     payload,
		State:       activity.StateResolved,
		SourceTable: "coordination_claims",
		SourceID:    claim.ID,
	}
	activity.Append(ctx, act)
}

// emitArtifactPublished publishes an artifact_published activity for a
// just-persisted coordination artifact.
func emitArtifactPublished(ctx context.Context, art *coordination.Artifact, actor coordination.Actor) {
	if art == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"kind":           art.Kind,
		"status":         string(art.Status),
		"summary":        art.Summary,
		"scope_kind":     string(art.ScopeKind),
		"scope_key":      art.ScopeKey,
		"version":        art.Version,
		"supersedes":     art.SupersedesArtifactID,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(art.TaskID),
		Timestamp:  art.UpdatedAt,
		Resolution: activity.ResolutionFor(activity.ActionArtifactPublished),
		Action:     activity.ActionArtifactPublished,
		Actor: activity.Actor{
			AgentID:   actor.AgentID,
			AgentType: actor.AgentType,
		},
		Subject: activity.Subject{
			PathPrefix:     art.ScopeKey,
			TargetArtifact: art.ID,
		},
		Payload:     payload,
		State:       activity.StatePoint,
		SourceTable: "coordination_artifacts",
		SourceID:    art.ID,
	}
	activity.Append(ctx, act)
}

// emitReviewRequested publishes a review_requested activity for a
// just-persisted coordination review.
func emitReviewRequested(ctx context.Context, rev *coordination.Review, actor coordination.Actor) {
	if rev == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"reviewer_type": string(rev.ReviewerType),
		"artifact_id":   rev.ArtifactID,
		"status":        string(rev.Status),
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(rev.TaskID),
		Timestamp:  rev.UpdatedAt,
		Resolution: activity.ResolutionFor(activity.ActionReviewRequested),
		Action:     activity.ActionReviewRequested,
		Actor: activity.Actor{
			AgentID:   actor.AgentID,
			AgentType: actor.AgentType,
		},
		Subject: activity.Subject{
			TargetArtifact: rev.ArtifactID,
			TargetAgent:    string(rev.ReviewerType),
		},
		Payload:     payload,
		State:       activity.StateInFlight,
		SourceTable: "coordination_reviews",
		SourceID:    rev.ID,
	}
	activity.Append(ctx, act)
}

// emitReviewCompleted publishes a review_completed activity that
// resolves the original review_requested.
func emitReviewCompleted(ctx context.Context, taskID, reviewID string, actor coordination.Actor, status string, summary string) {
	payload, _ := json.Marshal(map[string]any{
		"review_id":          reviewID,
		"status":             status,
		"resolution_summary": summary,
	})
	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(taskID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionReviewCompleted),
		Action:     activity.ActionReviewCompleted,
		Actor: activity.Actor{
			AgentID:   actor.AgentID,
			AgentType: actor.AgentType,
		},
		Subject: activity.Subject{
			TargetArtifact: reviewID,
		},
		Payload:     payload,
		State:       activity.StateResolved,
		SourceTable: "coordination_reviews",
		SourceID:    reviewID,
	}
	activity.Append(ctx, act)
}
