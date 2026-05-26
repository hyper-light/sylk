package claims

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/activity"
)

const (
	ProjectorFabric    = "fabric"
	ProjectorKnowledge = "knowledge"
)

type ClaimsProjector interface {
	Name() string
	Project(ctx context.Context, record *ClaimsOutboxRecord, board *ClaimsBoard) error
}

type FabricProjector struct{}

func NewFabricProjector() *FabricProjector {
	return &FabricProjector{}
}

func (p *FabricProjector) Name() string { return ProjectorFabric }

func (p *FabricProjector) Project(ctx context.Context, record *ClaimsOutboxRecord, board *ClaimsBoard) error {
	if record == nil || board == nil {
		return nil
	}
	act, ok := fabricActivityForRecord(record, board)
	if !ok {
		return nil
	}
	activity.Append(ctx, act)
	return nil
}

func fabricActivityForRecord(record *ClaimsOutboxRecord, board *ClaimsBoard) (activity.AgentActivity, bool) {
	kind := fabricActionKind(record.MutationKind)
	if kind == "" {
		return activity.AgentActivity{}, false
	}
	payload, actor, coords, target := board.fabricPayload(record)
	payloadJSON, _ := json.Marshal(payload)
	if target == "" {
		target = record.EntityID
	}
	coords["board_id"] = record.BoardID
	coords["session_id"] = record.SessionID
	coords["task_id"] = record.TaskID
	coords["sequence"] = strconv.FormatUint(record.Sequence, 10)
	coords["entity_type"] = record.EntityType
	coords["entity_id"] = record.EntityID
	coords["mutation_kind"] = record.MutationKind
	return activity.AgentActivity{
		ID:         stableClaimsActivityID(record),
		SessionID:  activity.SessionID(record.SessionID),
		Timestamp:  time.Now().UTC(),
		Resolution: activity.ResolutionFor(kind),
		Actor: activity.Actor{
			AgentID:    actor,
			PipelineID: record.BoardID,
		},
		Action: kind,
		Subject: activity.Subject{
			TargetArtifact: target,
			Coordinates:    coords,
		},
		Payload:     payloadJSON,
		State:       activity.StatePoint,
		SourceTable: "claims_board",
		SourceID:    record.EntityID,
	}, true
}

func stableClaimsActivityID(record *ClaimsOutboxRecord) activity.ActivityID {
	id := strings.Join([]string{
		"claims",
		sanitizeActivityIDPart(record.BoardID),
		strconv.FormatUint(record.Sequence, 10),
		sanitizeActivityIDPart(record.EntityType),
		sanitizeActivityIDPart(record.EntityID),
		sanitizeActivityIDPart(record.MutationKind),
	}, ":")
	return activity.ActivityID(id)
}

func sanitizeActivityIDPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer(":", "_", "\x1f", "_", " ", "_", "/", "_")
	return replacer.Replace(value)
}

func fabricActionKind(mutationKind string) activity.ActionKind {
	switch mutationKind {
	case walEventActionPosted:
		return activity.ActionActionPosted
	case "claim_issued":
		return activity.ActionClaimIssued
	case walEventClaimUpdated:
		return activity.ActionClaimUpdated
	case walEventTestamentSubmitted:
		return activity.ActionTestamentSubmitted
	case "artifact_published":
		return activity.ActionClaimArtifactPublished
	case walEventValidationEvaluated:
		return activity.ActionClaimValidated
	case walEventClaimAccepted:
		return activity.ActionClaimAccepted
	case walEventClaimRejected:
		return activity.ActionClaimRejected
	case walEventPhaseTransition:
		return activity.ActionBoardPhaseChanged
	case walEventBoardComplete:
		return activity.ActionBoardComplete
	default:
		return ""
	}
}

func (b *ClaimsBoard) fabricPayload(record *ClaimsOutboxRecord) (map[string]any, string, map[string]string, string) {
	payload := map[string]any{
		"entity_type":   record.EntityType,
		"entity_id":     record.EntityID,
		"mutation_kind": record.MutationKind,
		"sequence":      record.Sequence,
	}
	coords := make(map[string]string)
	actor := ""
	target := record.EntityID
	switch record.EntityType {
	case "action":
		if a, ok := b.CloneAction(record.EntityID); ok {
			actor = a.AgentID
			payload["action_type"] = string(a.Type)
			payload["status"] = string(a.Status)
			payload["agent_id"] = a.AgentID
			payload["relations"] = a.Relations
		}
	case "claim":
		if c, ok := b.CloneClaim(record.EntityID); ok {
			actor = c.AgentID
			payload["title"] = c.Title
			payload["description"] = c.Description
			payload["action_type"] = string(c.ActionType)
			payload["status"] = string(c.Status)
			payload["context"] = c.Context
			payload["relations"] = c.Relations
			coords["claim_id"] = c.ID
			if subj := SubjectAgentID(c.Relations); subj != "" {
				coords["subject_agent_id"] = subj
			}
			if issuer := IssuerAgentID(c.Relations); issuer != "" {
				coords["issuer_agent_id"] = issuer
			}
		}
	case "testament":
		if t, ok := b.CloneTestament(record.EntityID); ok {
			actor = t.AgentID
			payload["summary"] = t.Summary
			payload["confidence"] = t.Confidence
			payload["context"] = t.Context
			payload["relations"] = t.Relations
			coords["testament_id"] = t.ID
			if claimID := ClaimIDFromRelations(t.Relations); claimID != "" {
				coords["claim_id"] = claimID
			}
		}
	case "artifact":
		if a, ok := b.cloneArtifact(record.EntityID); ok {
			actor = a.AgentID
			payload["kind"] = a.Kind
			payload["reference"] = a.Reference
			payload["ephemeral"] = a.Ephemeral
			payload["metadata"] = a.Metadata
			coords["artifact_id"] = a.ID
			coords["testament_id"] = a.TestamentID
		}
	case "validation":
		if v, claim, ok := b.cloneValidation(record.EntityID); ok {
			actor = v.AgentID
			if actor == "" {
				actor = claim.AgentID
			}
			payload["description"] = v.Description
			payload["quality_bar"] = v.QualityBar
			payload["type"] = string(v.Type)
			payload["status"] = string(v.Status)
			coords["validation_id"] = v.ID
			coords["claim_id"] = v.ClaimID
		}
	case "board":
		actor = "claims-board"
		payload["phase"] = string(b.Phase())
	}
	return payload, actor, coords, target
}
