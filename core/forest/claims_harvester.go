package forest

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/activity/activitystore"
	"github.com/google/uuid"
)

// ClaimsHarvester converts claims board Fabric activities into forest
// events. Accepted claims become Evidence branches (positive precedent),
// rejected claims become AntiPattern branches (negative precedent),
// testaments become Evidence branches, and board completions become
// Outcome branches.
type ClaimsHarvester struct {
	forest *MemoryForest
}

// NewClaimsHarvester creates a harvester bound to the given forest.
func NewClaimsHarvester(forest *MemoryForest) *ClaimsHarvester {
	return &ClaimsHarvester{forest: forest}
}

// Harvest processes a single forest candidate from the Fabric activity
// stream. Dispatches to the appropriate event builder based on the
// activity's ActionKind. Returns nil on success or if the activity is
// not a harvestable claims kind.
func (h *ClaimsHarvester) Harvest(ctx context.Context, candidate activitystore.ForestCandidate) error {
	if h.forest == nil {
		return nil
	}
	event := h.buildEvent(candidate.Activity)
	if event == nil {
		return nil
	}
	return h.forest.AppendEvent(ctx, event)
}

func (h *ClaimsHarvester) buildEvent(a activity.AgentActivity) *Event {
	switch a.Action {
	case activity.ActionClaimAccepted:
		return h.eventForAcceptedClaim(a)
	case activity.ActionClaimRejected:
		return h.eventForRejectedClaim(a)
	case activity.ActionTestamentSubmitted:
		return h.eventForTestament(a)
	case activity.ActionBoardComplete:
		return h.eventForBoardComplete(a)
	default:
		return nil
	}
}

func (h *ClaimsHarvester) eventForAcceptedClaim(a activity.AgentActivity) *Event {
	payload := parsePayload(a.Payload)
	title := stringFromPayload(payload, "title")
	return &Event{
		ID:             "evt_" + uuid.NewString(),
		SessionID:      string(a.SessionID),
		AgentID:        a.Actor.AgentID,
		AgentType:      a.Actor.AgentType,
		EventType:      EventTypeClaimAccepted,
		Family:         TreeFamilyEvidence,
		Scope:          ScopeEpisodic,
		RootID:         "claim_" + string(a.Subject.TargetArtifact),
		BranchID:       "claim_" + string(a.Subject.TargetArtifact),
		Confidence:     confidenceFromFabric(a.Confidence),
		Salience:       0.8,
		Timestamp:      a.Timestamp,
		Title:          title,
		Summary:        fmt.Sprintf("Accepted claim: %s", title),
		ProvenanceRefs: claimsProvenanceRefs(a),
		Payload:        payload,
	}
}

func (h *ClaimsHarvester) eventForRejectedClaim(a activity.AgentActivity) *Event {
	payload := parsePayload(a.Payload)
	title := stringFromPayload(payload, "title")
	return &Event{
		ID:             "evt_" + uuid.NewString(),
		SessionID:      string(a.SessionID),
		AgentID:        a.Actor.AgentID,
		AgentType:      a.Actor.AgentType,
		EventType:      EventTypeClaimRejected,
		Family:         TreeFamilyAntiPattern,
		Scope:          ScopeContradiction,
		RootID:         "claim_" + string(a.Subject.TargetArtifact),
		BranchID:       "claim_rejected_" + string(a.Subject.TargetArtifact),
		Confidence:     confidenceFromFabric(a.Confidence),
		Salience:       0.9,
		Timestamp:      a.Timestamp,
		Title:          title,
		Summary:        fmt.Sprintf("Rejected claim: %s", title),
		ProvenanceRefs: claimsProvenanceRefs(a),
		Payload:        payload,
	}
}

func (h *ClaimsHarvester) eventForTestament(a activity.AgentActivity) *Event {
	payload := parsePayload(a.Payload)
	summary := stringFromPayload(payload, "summary")
	return &Event{
		ID:             "evt_" + uuid.NewString(),
		SessionID:      string(a.SessionID),
		AgentID:        a.Actor.AgentID,
		AgentType:      a.Actor.AgentType,
		EventType:      EventTypeTestamentRecorded,
		Family:         TreeFamilyEvidence,
		Scope:          ScopeWorking,
		RootID:         "testament_" + string(a.Subject.TargetArtifact),
		BranchID:       "testament_" + string(a.Subject.TargetArtifact),
		Confidence:     confidenceFromFabric(a.Confidence),
		Salience:       0.6,
		Timestamp:      a.Timestamp,
		Title:          summary,
		Summary:        fmt.Sprintf("Testament: %s", summary),
		ProvenanceRefs: claimsProvenanceRefs(a),
		Payload:        payload,
	}
}

func (h *ClaimsHarvester) eventForBoardComplete(a activity.AgentActivity) *Event {
	return &Event{
		ID:             "evt_" + uuid.NewString(),
		SessionID:      string(a.SessionID),
		AgentID:        a.Actor.AgentID,
		AgentType:      a.Actor.AgentType,
		EventType:      EventTypeOutcomeRecorded,
		Family:         TreeFamilyOutcome,
		Scope:          ScopeEpisodic,
		RootID:         "board_" + string(a.Subject.TargetArtifact),
		BranchID:       "board_" + string(a.Subject.TargetArtifact),
		Confidence:     1.0,
		Salience:       0.7,
		Timestamp:      a.Timestamp,
		Title:          "Board completed",
		Summary:        "Claims board reached terminal completion",
		ProvenanceRefs: claimsProvenanceRefs(a),
	}
}

func parsePayload(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func stringFromPayload(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func claimsProvenanceRefs(a activity.AgentActivity) []string {
	refs := []string{string(a.ID)}
	if a.Subject.TargetArtifact != "" {
		refs = append(refs, a.Subject.TargetArtifact)
	}
	return refs
}

func confidenceFromFabric(c activity.Confidence) float64 {
	switch c {
	case activity.ConfidenceConsensus:
		return 0.95
	case activity.ConfidenceCommitted:
		return 0.80
	case activity.ConfidenceTentative:
		return 0.60
	case activity.ConfidenceHint:
		return 0.40
	default:
		return 0.50
	}
}

// Ensure ClaimsHarvester.Harvest satisfies the HarvestFunc signature.
var _ activitystore.HarvestFunc = (*ClaimsHarvester)(nil).Harvest
