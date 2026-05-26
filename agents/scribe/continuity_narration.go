package scribe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/claims"
)

const (
	scribeArtifactContinuityNarration = "continuity_narration"
	scribeArtifactArchivalistReceipt  = "archivalist_receipt"
)

func (s *Scribe) maybeNarrateContinuityActivity(ctx context.Context, a activity.AgentActivity) bool {
	if s == nil || a.Action != activity.ActionTestamentSubmitted {
		return false
	}
	testamentID := strings.TrimSpace(a.Subject.Coordinates["testament_id"])
	if testamentID == "" {
		testamentID = strings.TrimSpace(a.SourceID)
	}
	if testamentID == "" {
		return false
	}
	board := s.scribeBoard()
	if board == nil {
		return false
	}
	t, ok := board.CloneTestament(testamentID)
	if !ok || t == nil {
		return false
	}
	details, ok := claims.ContinuityDetailsForTestament(t)
	if !ok {
		return false
	}
	if strings.HasPrefix(details.AgentID, "scribe-") || !strings.EqualFold(details.AgentID, s.parentAgentType) {
		return false
	}
	key := "continuity:" + testamentID
	if _, loaded := s.continuityNarrated.LoadOrStore(key, struct{}{}); loaded {
		return true
	}
	s.submitContinuityNarration(ctx, board, t, details)
	return true
}

func (s *Scribe) submitContinuityNarration(ctx context.Context, board *claims.ClaimsBoard, source *claims.Testament, details *claims.ContinuityDetails) {
	if board == nil || source == nil || details == nil {
		return
	}
	entryID := fmt.Sprintf("scribe_%s_continuity_%s_%d", s.parentAgentType, safeScribeIDFragment(source.ID), time.Now().UnixNano())
	payload := scribeContinuityPayload(details, source.ID)
	payloadJSON, _ := json.Marshal(payload)
	metadata := scribeContinuityMetadata(details, source.ID, entryID)
	feed := shared.ScribeFeed{
		UserRequest:         "Continuity testament submitted for " + details.Topic,
		AgentResponse:       string(payloadJSON),
		ParentCorrelationID: "continuity:" + source.ID,
		Timestamp:           time.Now(),
	}

	storeErr := s.storeCommentaryInArchivalistWithEntryID(ctx, string(payloadJSON), feed, entryID, metadata)
	relations := scribeContinuityRelations(source, details)
	artifacts := []*claims.Artifact{{
		AgentID:   s.scribeAgentID(),
		SessionID: s.sessionID,
		Kind:      scribeArtifactContinuityNarration,
		Reference: string(payloadJSON),
		Metadata:  metadata,
		Relations: relations,
	}}
	if storeErr != nil {
		artifacts = append(artifacts, &claims.Artifact{
			AgentID:   s.scribeAgentID(),
			SessionID: s.sessionID,
			Kind:      claims.ArtifactKindErrorDiagnostic,
			Reference: storeErr.Error(),
			Metadata: map[string]any{
				"operation":            "store_continuity_narration_archivalist",
				"continuity_testament": source.ID,
				"continuity_topic":     details.Topic,
				"continuity_agent_id":  details.AgentID,
				"source_board_id":      details.BoardID,
				"source_session_id":    details.SessionID,
				"archivalist_entry_id": entryID,
				"error":                storeErr.Error(),
			},
			Relations: relations,
		})
	} else {
		artifacts = append(artifacts, &claims.Artifact{
			AgentID:   s.scribeAgentID(),
			SessionID: s.sessionID,
			Kind:      scribeArtifactArchivalistReceipt,
			Reference: entryID,
			Metadata:  metadata,
			Relations: relations,
		})
	}
	testament := claims.Testament{
		AgentID:    s.scribeAgentID(),
		SessionID:  s.sessionID,
		Summary:    fmt.Sprintf("Narrated continuity for %s through board sequence %d.", details.Topic, details.ThroughSequence),
		Confidence: "committed",
		Relations:  relations,
		Artifacts:  artifacts,
	}
	if err := board.SubmitTestaments(ctx, claims.Action{AgentID: s.scribeAgentID(), Type: claims.ActionTypeTestament}, []claims.Testament{testament}); err != nil {
		board.RecordNotificationError("scribe continuity narration testament: " + err.Error())
	}
}

func scribeContinuityPayload(details *claims.ContinuityDetails, sourceTestamentID string) map[string]any {
	return map[string]any{
		"summary":                 fmt.Sprintf("%s carried %d durable source(s) through board sequence %d.", details.AgentID, len(details.Sources), details.ThroughSequence),
		"continuity_topic":        details.Topic,
		"continuity_agent_id":     details.AgentID,
		"continuity_testament_id": sourceTestamentID,
		"continuity_claim_id":     details.ClaimID,
		"source_session_id":       details.SessionID,
		"source_board_id":         details.BoardID,
		"from_sequence":           details.FromSequence,
		"through_sequence":        details.ThroughSequence,
		"working_context":         details.WorkingContext,
		"evidence_digest":         details.EvidenceDigest,
		"sources":                 details.Sources,
	}
}

func scribeContinuityMetadata(details *claims.ContinuityDetails, sourceTestamentID, entryID string) map[string]any {
	sourceTestaments := make([]string, 0, len(details.Sources))
	sourceArtifacts := make([]string, 0, len(details.Sources))
	for _, source := range details.Sources {
		if source.TestamentID != "" {
			sourceTestaments = appendUniqueString(sourceTestaments, source.TestamentID)
		}
		if source.ArtifactID != "" {
			sourceArtifacts = appendUniqueString(sourceArtifacts, source.ArtifactID)
		}
	}
	return map[string]any{
		"source_type":               "scribe",
		"narration_type":            "continuity",
		"continuity_topic":          details.Topic,
		"continuity_agent_id":       details.AgentID,
		"continuity_claim_id":       details.ClaimID,
		"continuity_testament_id":   sourceTestamentID,
		"source_session_id":         details.SessionID,
		"source_board_id":           details.BoardID,
		"from_sequence":             details.FromSequence,
		"through_sequence":          details.ThroughSequence,
		"source_testament_ids":      sourceTestaments,
		"source_artifact_ids":       sourceArtifacts,
		"archivalist_entry_id":      entryID,
		"joinable_source_entity_id": sourceTestamentID,
	}
}

func scribeContinuityRelations(source *claims.Testament, details *claims.ContinuityDetails) []claims.Relation {
	relations := []claims.Relation{
		{Related: source.ID, RelatedType: claims.RelatedTypeTestament, Relationship: claims.RelationshipDerivedFrom},
	}
	if details.ClaimID != "" {
		relations = append(relations, claims.Relation{Related: details.ClaimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipCausedBy})
	}
	for _, sourceRef := range details.Sources {
		if sourceRef.TestamentID != "" {
			relations = append(relations, claims.Relation{Related: sourceRef.TestamentID, RelatedType: claims.RelatedTypeTestament, Relationship: claims.RelationshipDerivedFrom})
		}
		if sourceRef.ArtifactID != "" {
			relations = append(relations, claims.Relation{Related: sourceRef.ArtifactID, RelatedType: claims.RelatedTypeArtifact, Relationship: claims.RelationshipDerivedFrom})
		}
	}
	return dedupeScribeRelations(relations)
}

func appendUniqueString(values []string, next string) []string {
	next = strings.TrimSpace(next)
	if next == "" {
		return values
	}
	for _, existing := range values {
		if existing == next {
			return values
		}
	}
	return append(values, next)
}

func dedupeScribeRelations(in []claims.Relation) []claims.Relation {
	out := make([]claims.Relation, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, rel := range in {
		key := rel.RelatedType + "\x00" + rel.Relationship + "\x00" + rel.Related
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, rel)
	}
	return out
}

func safeScribeIDFragment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 48 {
			break
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "unknown"
	}
	return out
}
