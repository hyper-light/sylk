package knowledgeruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
)

type ClaimsKnowledgeWriter interface {
	UpsertTextDocument(ctx context.Context, req *TextDocumentIngestRequest) error
}

type ClaimsKnowledgeMirror struct {
	writer ClaimsKnowledgeWriter
}

func NewClaimsKnowledgeMirror(writer ClaimsKnowledgeWriter) *ClaimsKnowledgeMirror {
	return &ClaimsKnowledgeMirror{writer: writer}
}

func (m *ClaimsKnowledgeMirror) Name() string { return claims.ProjectorKnowledge }

func (m *ClaimsKnowledgeMirror) Project(ctx context.Context, record *claims.ClaimsOutboxRecord, board *claims.ClaimsBoard) error {
	if m == nil || m.writer == nil || record == nil || board == nil {
		return nil
	}
	doc, ok := claimsKnowledgeDocumentForRecord(record, board.Projection())
	if !ok {
		return nil
	}
	return m.writer.UpsertTextDocument(ctx, doc)
}

func claimsKnowledgeDocumentForRecord(record *claims.ClaimsOutboxRecord, proj *claims.ClaimsBoardProjection) (*TextDocumentIngestRequest, bool) {
	if record == nil || proj == nil {
		return nil, false
	}
	switch record.EntityType {
	case "claim":
		for i := range proj.Claims {
			if proj.Claims[i].ID == record.EntityID {
				return claimKnowledgeDocument(record, &proj.Claims[i]), true
			}
		}
	case "testament":
		for i := range proj.Testaments {
			if proj.Testaments[i].ID == record.EntityID {
				return testamentKnowledgeDocument(record, &proj.Testaments[i]), true
			}
		}
	case "artifact":
		for i := range proj.Testaments {
			for _, a := range proj.Testaments[i].Artifacts {
				if a != nil && a.ID == record.EntityID && !a.Ephemeral {
					return artifactKnowledgeDocument(record, a), true
				}
			}
		}
	case "validation":
		for i := range proj.Claims {
			for _, v := range proj.Claims[i].Validations {
				if v != nil && v.ID == record.EntityID {
					return validationKnowledgeDocument(record, v, &proj.Claims[i]), true
				}
			}
		}
	}
	return nil, false
}

func claimKnowledgeDocument(record *claims.ClaimsOutboxRecord, c *claims.Claim) *TextDocumentIngestRequest {
	content := linesToMarkdown(
		"# Claim "+c.ID,
		"",
		"entity_type: claim",
		"entity_id: "+c.ID,
		"agent_id: "+c.AgentID,
		"session_id: "+c.SessionID,
		"board_id: "+record.BoardID,
		"task_id: "+c.TaskID,
		"sequence: "+strconv.FormatUint(c.Sequence, 10),
		"status: "+string(c.Status),
		"action_type: "+string(c.ActionType),
		"relations: "+jsonOneLine(c.Relations),
		"",
		"## Title",
		"",
		c.Title,
		"",
		"## Description",
		"",
		c.Description,
		"",
		"## Validations",
		"",
		jsonOneLine(c.Validations),
	)
	return claimsTextDocument(record, "claim", c.ID, c.AgentID, content, map[string]string{
		"claim_id":    c.ID,
		"entity_type": "claim",
		"status":      string(c.Status),
		"agent_id":    c.AgentID,
	})
}

func testamentKnowledgeDocument(record *claims.ClaimsOutboxRecord, t *claims.Testament) *TextDocumentIngestRequest {
	claimID := claims.ClaimIDFromRelations(t.Relations)
	content := linesToMarkdown(
		"# Testament "+t.ID,
		"",
		"entity_type: testament",
		"entity_id: "+t.ID,
		"claim_id: "+claimID,
		"agent_id: "+t.AgentID,
		"session_id: "+t.SessionID,
		"board_id: "+record.BoardID,
		"task_id: "+t.TaskID,
		"sequence: "+strconv.FormatUint(t.Sequence, 10),
		"confidence: "+t.Confidence,
		"relations: "+jsonOneLine(t.Relations),
		"",
		"## Summary",
		"",
		t.Summary,
		"",
		"## Context",
		"",
		t.Context,
		"",
		"## Artifacts",
		"",
		jsonOneLine(t.Artifacts),
	)
	return claimsTextDocument(record, "testament", t.ID, t.AgentID, content, map[string]string{
		"claim_id":     claimID,
		"testament_id": t.ID,
		"entity_type":  "testament",
		"agent_id":     t.AgentID,
	})
}

func artifactKnowledgeDocument(record *claims.ClaimsOutboxRecord, a *claims.Artifact) *TextDocumentIngestRequest {
	content := linesToMarkdown(
		"# Artifact "+a.ID,
		"",
		"entity_type: artifact",
		"entity_id: "+a.ID,
		"testament_id: "+a.TestamentID,
		"agent_id: "+a.AgentID,
		"session_id: "+a.SessionID,
		"board_id: "+record.BoardID,
		"task_id: "+a.TaskID,
		"sequence: "+strconv.FormatUint(a.Sequence, 10),
		"kind: "+a.Kind,
		"content_hash: "+a.ContentHash,
		"ephemeral: "+strconv.FormatBool(a.Ephemeral),
		"relations: "+jsonOneLine(a.Relations),
		"metadata: "+jsonOneLine(a.Metadata),
		"",
		"## Reference",
		"",
		a.Reference,
	)
	return claimsTextDocument(record, "artifact", a.ID, a.AgentID, content, map[string]string{
		"artifact_id":   a.ID,
		"testament_id":  a.TestamentID,
		"entity_type":   "artifact",
		"artifact_kind": a.Kind,
		"agent_id":      a.AgentID,
	})
}

func validationKnowledgeDocument(record *claims.ClaimsOutboxRecord, v *claims.Validation, c *claims.Claim) *TextDocumentIngestRequest {
	agentID := v.AgentID
	if agentID == "" && c != nil {
		agentID = c.AgentID
	}
	content := linesToMarkdown(
		"# Validation "+v.ID,
		"",
		"entity_type: validation",
		"entity_id: "+v.ID,
		"claim_id: "+v.ClaimID,
		"agent_id: "+agentID,
		"session_id: "+v.SessionID,
		"board_id: "+record.BoardID,
		"task_id: "+v.TaskID,
		"sequence: "+strconv.FormatUint(record.Sequence, 10),
		"type: "+string(v.Type),
		"status: "+string(v.Status),
		"required: "+strconv.FormatBool(v.Required),
		"relations: "+jsonOneLine(v.Relations),
		"",
		"## Description",
		"",
		v.Description,
		"",
		"## Quality Bar",
		"",
		v.QualityBar,
	)
	return claimsTextDocument(record, "validation", v.ID, agentID, content, map[string]string{
		"claim_id":      v.ClaimID,
		"validation_id": v.ID,
		"entity_type":   "validation",
		"status":        string(v.Status),
		"agent_id":      agentID,
	})
}

func claimsTextDocument(record *claims.ClaimsOutboxRecord, entityType, entityID, agentID, content string, metadata map[string]string) *TextDocumentIngestRequest {
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata["source"] = "claims_board"
	metadata["board_id"] = record.BoardID
	metadata["session_id"] = record.SessionID
	metadata["task_id"] = record.TaskID
	metadata["sequence"] = strconv.FormatUint(record.Sequence, 10)
	metadata["entity_id"] = entityID
	metadata["mutation_kind"] = record.MutationKind
	if agentID != "" {
		metadata["agent_id"] = agentID
	}
	docID := "claims_" + sanitizeClaimsKnowledgeSegment(record.BoardID) + "_" + sanitizeClaimsKnowledgeSegment(entityType) + "_" + sanitizeClaimsKnowledgeSegment(entityID)
	docPath := path.Join("claims", sanitizeClaimsKnowledgeSegment(record.SessionID), sanitizeClaimsKnowledgeSegment(record.BoardID), sanitizeClaimsKnowledgeSegment(entityType), sanitizeClaimsKnowledgeSegment(entityID)+".md")
	return &TextDocumentIngestRequest{
		DocumentID: docID,
		Path:       docPath,
		Content:    content,
		DocType:    search.DocTypeMarkdown,
		Language:   "markdown",
		Domain:     sylkdir.DomainDoc,
		Metadata:   metadata,
	}
}

func linesToMarkdown(lines ...string) string {
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func jsonOneLine(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}

func sanitizeClaimsKnowledgeSegment(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}
