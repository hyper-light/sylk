package shared

import (
	"context"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/google/uuid"
)

// PeerInteractionKind tags a started artifact emitted at the moment a
// consult/challenge/guardian-check/peer-interaction is dispatched.
// The bridge's cycle resolver pairs the eventual completion artifact
// (emitted when the child closes the issued claim) via Relation{completes}
// keyed off the started artifact's stable ID. UI_DESIGN.md §2.4 + §4.1.
type PeerInteractionKind string

const (
	PeerInteractionKindConsult        PeerInteractionKind = "consult_started"
	PeerInteractionKindChallenge      PeerInteractionKind = "challenge_started"
	PeerInteractionKindGuardianCheck  PeerInteractionKind = "guardian_check_started"
)

// EmitPeerInteractionStarted records a started artifact onto the
// caller's TestamentAccumulator (when one is on ctx) and returns the
// artifact ID. The bridge later pairs the completion artifact via
// Relation{completes} when the issued claim closes.
//
// The caller passes the just-posted claim's ID and the target
// (subject) agent identity so the artifact carries enough metadata
// for the bridge to attribute it without re-reading the claim.
//
// Returns empty string when no accumulator is on ctx — callers
// treat this as a no-op (the row simply will not appear in the chat
// tree, which is the correct behavior when the agent isn't in a
// claim-processing context).
func EmitPeerInteractionStarted(ctx context.Context, kind PeerInteractionKind, agentID, claimID, target, summary string) string {
	acc := claims.AccumulatorFromContext(ctx)
	if acc == nil {
		return ""
	}
	if strings.TrimSpace(claimID) == "" {
		return ""
	}
	startedID := uuid.NewString()
	acc.RecordArtifact(&claims.Artifact{
		ID:        startedID,
		AgentID:   strings.TrimSpace(agentID),
		Kind:      string(kind),
		Reference: strings.TrimSpace(target),
		Metadata: map[string]any{
			"claim_id":   strings.TrimSpace(claimID),
			"target":     strings.TrimSpace(target),
			"summary":    strings.TrimSpace(summary),
			"started_at": time.Now().UTC().Format(time.RFC3339Nano),
		},
		Ephemeral: true,
	})
	return startedID
}
