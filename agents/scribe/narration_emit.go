package scribe

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/activity"
)

// emitNarrationActivity publishes the typed Activity Fabric projection
// of a scribe commentary alongside the canonical archivalist entry.
// Per SCRIBE_FABRIC.md §9, the activity carries:
//
//   - Resolution: Coarse (durable, indexed, embedded, harvest-eligible)
//   - Subject.Domain: parent agent type
//   - Subject.PathPrefix: scope inferred from the most recent batch
//     activity, when the scribe ran for a fabric batch
//   - Subject.Coordinates: replica_generation, parent_correlation_id
//   - Payload: the raw commentary JSON (same content as the archivalist
//     entry)
//   - SourceTable + SourceID: back-reference to the canonical
//     archivalist entry so consumers can join from fabric to document
//
// The function is best-effort: failures here must not block the
// archivalist publish (which has already succeeded by this point).
// Used by storeCommentaryInArchivalist immediately after a successful
// route-request publish.
func (s *Scribe) emitNarrationActivity(ctx context.Context, commentary, archivalEntryID, parentCorrelationID string) {
	if strings.TrimSpace(commentary) == "" {
		return
	}
	subject := activity.Subject{
		Domain: s.parentAgentType,
		Coordinates: map[string]string{
			"replica_generation":    strconv.Itoa(s.replicaGeneration),
			"parent_correlation_id": parentCorrelationID,
			"scribe_id":             s.id,
		},
	}
	// Carry the commentary directly as payload. If the commentary
	// isn't valid JSON for some reason (unlikely — it came from the
	// scribe's structured output), wrap it so the activity row stays
	// well-formed.
	var payload json.RawMessage
	if json.Valid([]byte(commentary)) {
		payload = json.RawMessage(commentary)
	} else {
		wrapped, _ := json.Marshal(map[string]string{"raw": commentary})
		payload = json.RawMessage(wrapped)
	}

	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(s.sessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionNarrationEmitted),
		Action:     activity.ActionNarrationEmitted,
		Actor: activity.Actor{
			AgentID:   s.id,
			AgentType: "scribe-" + s.parentAgentType,
		},
		Subject:     subject,
		Payload:     payload,
		State:       activity.StatePoint,
		SourceTable: "archivalist_entries",
		SourceID:    archivalEntryID,
	}
	activity.Append(ctx, act)
}

// emitPrecedentActivity publishes a separate ActionPrecedentEmitted
// activity when the scribe LLM flagged a batch as precedent-worthy.
// The activity carries:
//
//   - Caused: the most recent narration_emitted activity ID (the
//     narration that flagged the precedent) — populated when callers
//     pass it; otherwise nil.
//   - Payload: the precedent rationale (precedent_why) plus a
//     compact summary of what the batch did so consumers (Memory
//     Forest harvesters, knowledge agents) can index it without
//     re-fetching the full narration.
//
// ForestSubscriber's electCandidate already accepts
// ActionPrecedentEmitted; this is the natural emission point for it.
// Per SCRIBE_FABRIC.md §10.
func (s *Scribe) emitPrecedentActivity(ctx context.Context, commentary map[string]any, feed shared.ScribeFeed) {
	if commentary == nil {
		return
	}
	why := strings.TrimSpace(scribeStringFromAny(commentary["precedent_why"]))
	summary := strings.TrimSpace(scribeStringFromAny(commentary["summary"]))

	payload, _ := json.Marshal(map[string]any{
		"why":                why,
		"summary":            summary,
		"parent_agent":       s.parentAgentType,
		"replica_generation": s.replicaGeneration,
		"correlation_id":     strings.TrimSpace(feed.ParentCorrelationID),
	})

	subject := activity.Subject{
		Domain: s.parentAgentType,
		Coordinates: map[string]string{
			"replica_generation":    strconv.Itoa(s.replicaGeneration),
			"parent_correlation_id": strings.TrimSpace(feed.ParentCorrelationID),
			"scribe_id":             s.id,
		},
	}

	act := activity.AgentActivity{
		ID:         activity.NewActivityID(),
		SessionID:  activity.SessionID(s.sessionID),
		Timestamp:  time.Now(),
		Resolution: activity.ResolutionFor(activity.ActionPrecedentEmitted),
		Action:     activity.ActionPrecedentEmitted,
		Actor: activity.Actor{
			AgentID:   s.id,
			AgentType: "scribe-" + s.parentAgentType,
		},
		Subject: subject,
		Payload: payload,
		State:   activity.StatePoint,
	}
	activity.Append(ctx, act)
}
