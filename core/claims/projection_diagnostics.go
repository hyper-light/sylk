package claims

import (
	"context"
	"fmt"
	"strings"
)

const projectionDiagnosticsAgentID = "claims-board"

func projectionDiagnosticKey(record ClaimsOutboxRecord, projector string) string {
	return strings.Join([]string{
		record.BoardID,
		fmt.Sprint(record.Sequence),
		record.EntityType,
		record.EntityID,
		projector,
	}, "\x1f")
}

func projectionDiagnosticMessage(record ClaimsOutboxRecord, projector string, err error) string {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return fmt.Sprintf("projection_error projector=%s board=%s sequence=%d entity=%s/%s: %s",
		projector, record.BoardID, record.Sequence, record.EntityType, record.EntityID, msg)
}

func (b *ClaimsBoard) submitProjectionDiagnostic(ctx context.Context, record ClaimsOutboxRecord, projector, artifactKind, reference, errorMessage string) {
	if b == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	diagnosticType := "projection_receipt"
	if artifactKind == ArtifactKindProjectionError {
		diagnosticType = "projection_error"
	}
	relations := []Relation{{
		Related:      record.EntityID,
		RelatedType:  relatedTypeForOutboxEntity(record.EntityType),
		Relationship: RelationshipCausedBy,
	}}
	metadata := map[string]any{
		"diagnostic_type": diagnosticType,
		"projector":       projector,
		"board_id":        record.BoardID,
		"session_id":      record.SessionID,
		"task_id":         record.TaskID,
		"sequence":        record.Sequence,
		"entity_type":     record.EntityType,
		"entity_id":       record.EntityID,
		"mutation_kind":   record.MutationKind,
	}
	if errorMessage != "" {
		metadata["error"] = errorMessage
	}
	testament := Testament{
		AgentID:    projectionDiagnosticsAgentID,
		Summary:    reference,
		Confidence: "committed",
		Relations:  relations,
		Artifacts: []*Artifact{{
			Kind:      artifactKind,
			Reference: reference,
			Metadata:  metadata,
			Relations: relations,
		}},
	}
	if err := b.SubmitTestaments(ctx, Action{AgentID: projectionDiagnosticsAgentID, Type: ActionTypeTestament}, []Testament{testament}); err != nil {
		b.RecordNotificationError("projection diagnostic testament: " + err.Error())
	}
}

func relatedTypeForOutboxEntity(entityType string) string {
	switch strings.TrimSpace(entityType) {
	case "action":
		return RelatedTypeAction
	case "claim":
		return RelatedTypeClaim
	case "testament":
		return RelatedTypeTestament
	case "artifact":
		return RelatedTypeArtifact
	case "validation":
		return RelatedTypeValidation
	default:
		return strings.TrimSpace(entityType)
	}
}

func removeNotificationError(messages []string, target string) []string {
	if target == "" || len(messages) == 0 {
		return messages
	}
	out := messages[:0]
	for _, msg := range messages {
		if msg == target {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func IsProjectionDiagnosticTestament(t *Testament) bool {
	if t == nil || t.AgentID != projectionDiagnosticsAgentID {
		return false
	}
	if len(t.Artifacts) == 0 {
		return false
	}
	seen := false
	for _, artifact := range t.Artifacts {
		if artifact == nil {
			continue
		}
		seen = true
		if artifact.Kind != ArtifactKindProjectionError && artifact.Kind != ArtifactKindProjectionReceipt {
			return false
		}
	}
	return seen
}
