package knowledgeruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
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
	doc, ok := claimsKnowledgeDocumentForRecord(record, board)
	if !ok {
		return nil
	}
	return m.writer.UpsertTextDocument(ctx, doc)
}

func claimsKnowledgeDocumentForRecord(record *claims.ClaimsOutboxRecord, board *claims.ClaimsBoard) (*TextDocumentIngestRequest, bool) {
	if record == nil || board == nil {
		return nil, false
	}
	switch record.EntityType {
	case "claim":
		if c, ok := board.CloneClaim(record.EntityID); ok {
			return claimKnowledgeDocument(record, c), true
		}
	case "testament":
		if t, ok := board.CloneTestament(record.EntityID); ok {
			if claims.IsProjectionDiagnosticTestament(t) {
				return nil, false
			}
			return testamentKnowledgeDocument(record, t), true
		}
	case "artifact":
		if a, ok := board.CloneArtifact(record.EntityID); ok && !a.Ephemeral {
			if a.Kind == claims.ArtifactKindProjectionError || a.Kind == claims.ArtifactKindProjectionReceipt {
				return nil, false
			}
			return artifactKnowledgeDocument(record, a), true
		}
	case "validation":
		if v, c, ok := board.CloneValidation(record.EntityID); ok {
			return validationKnowledgeDocument(record, v, c), true
		}
	}
	return nil, false
}

func claimKnowledgeDocument(record *claims.ClaimsOutboxRecord, c *claims.Claim) *TextDocumentIngestRequest {
	metadata := map[string]string{
		"claim_id":    c.ID,
		"entity_type": "claim",
		"status":      string(c.Status),
		"agent_id":    c.AgentID,
	}
	addRelationMetadata(metadata, c.Relations)
	addScopeMetadata(metadata, c.Scope)
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
	return claimsTextDocument(record, "claim", c.ID, c.AgentID, content, metadata)
}

func testamentKnowledgeDocument(record *claims.ClaimsOutboxRecord, t *claims.Testament) *TextDocumentIngestRequest {
	claimID := claims.ClaimIDFromRelations(t.Relations)
	metadata := map[string]string{
		"claim_id":     claimID,
		"testament_id": t.ID,
		"entity_type":  "testament",
		"agent_id":     t.AgentID,
	}
	addRelationMetadata(metadata, t.Relations)
	if details, ok := claims.ContinuityDetailsForTestament(t); ok {
		addContinuityMetadata(metadata, details)
	}
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
	return claimsTextDocument(record, "testament", t.ID, t.AgentID, content, metadata)
}

func artifactKnowledgeDocument(record *claims.ClaimsOutboxRecord, a *claims.Artifact) *TextDocumentIngestRequest {
	metadata := map[string]string{
		"artifact_id":   a.ID,
		"testament_id":  a.TestamentID,
		"entity_type":   "artifact",
		"artifact_kind": a.Kind,
		"agent_id":      a.AgentID,
	}
	addRelationMetadata(metadata, a.Relations)
	addArtifactMetadata(metadata, a.Metadata)
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
	return claimsTextDocument(record, "artifact", a.ID, a.AgentID, content, metadata)
}

func validationKnowledgeDocument(record *claims.ClaimsOutboxRecord, v *claims.Validation, c *claims.Claim) *TextDocumentIngestRequest {
	agentID := v.AgentID
	if agentID == "" && c != nil {
		agentID = c.AgentID
	}
	metadata := map[string]string{
		"claim_id":      v.ClaimID,
		"validation_id": v.ID,
		"entity_type":   "validation",
		"status":        string(v.Status),
		"agent_id":      agentID,
	}
	addRelationMetadata(metadata, v.Relations)
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
	return claimsTextDocument(record, "validation", v.ID, agentID, content, metadata)
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
	content = appendClaimsMetadataContent(content, metadata)
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

func appendClaimsMetadataContent(content string, metadata map[string]string) string {
	if len(metadata) == 0 {
		return content
	}
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	lines := []string{strings.TrimRight(content, "\n"), "", "## Metadata", ""}
	for _, key := range keys {
		value := strings.TrimSpace(metadata[key])
		if value == "" {
			continue
		}
		lines = append(lines, key+": "+value)
	}
	return linesToMarkdown(lines...)
}

func addRelationMetadata(metadata map[string]string, relations []claims.Relation) {
	if metadata == nil || len(relations) == 0 {
		return
	}
	var parts []string
	for _, rel := range relations {
		if strings.TrimSpace(rel.Related) == "" {
			continue
		}
		parts = append(parts, rel.RelatedType+":"+rel.Relationship+":"+rel.Related)
		key := "relation_" + sanitizeClaimsKnowledgeSegment(rel.RelatedType) + "_" + sanitizeClaimsKnowledgeSegment(rel.Relationship)
		if existing := strings.TrimSpace(metadata[key]); existing != "" {
			metadata[key] = existing + "," + rel.Related
		} else {
			metadata[key] = rel.Related
		}
	}
	if len(parts) > 0 {
		metadata["relations_index"] = strings.Join(parts, ",")
	}
}

func addScopeMetadata(metadata map[string]string, scope []claims.ClaimScopeEntry) {
	if metadata == nil || len(scope) == 0 {
		return
	}
	parts := make([]string, 0, len(scope))
	for _, entry := range scope {
		kind := strings.TrimSpace(entry.Kind)
		key := strings.TrimSpace(entry.Key)
		if kind == "" || key == "" {
			continue
		}
		parts = append(parts, kind+":"+key)
	}
	if len(parts) > 0 {
		metadata["scope_index"] = strings.Join(parts, ",")
	}
}

func addArtifactMetadata(metadata map[string]string, artifactMetadata map[string]any) {
	if metadata == nil || len(artifactMetadata) == 0 {
		return
	}
	for _, key := range []string{"topic", "agent_id", "board_id", "session_id", "claim_id", "testament_id", "continuity_topic", "continuity_agent_id"} {
		if value := metadataAnyString(artifactMetadata[key]); value != "" {
			metadata[key] = value
		}
	}
}

func addContinuityMetadata(metadata map[string]string, details *claims.ContinuityDetails) {
	if metadata == nil || details == nil {
		return
	}
	metadata["continuity"] = "true"
	metadata["continuity_topic"] = details.Topic
	metadata["topic"] = details.Topic
	metadata["continuity_agent_id"] = details.AgentID
	metadata["carry_forward_agent_id"] = details.AgentID
	metadata["through_sequence"] = strconv.FormatUint(details.ThroughSequence, 10)
	var sourceTestaments []string
	var sourceArtifacts []string
	for _, source := range details.Sources {
		if source.TestamentID != "" {
			sourceTestaments = append(sourceTestaments, source.TestamentID)
		}
		if source.ArtifactID != "" {
			sourceArtifacts = append(sourceArtifacts, source.ArtifactID)
		}
	}
	if len(sourceTestaments) > 0 {
		metadata["source_testament_ids"] = strings.Join(sourceTestaments, ",")
	}
	if len(sourceArtifacts) > 0 {
		metadata["source_artifact_ids"] = strings.Join(sourceArtifacts, ",")
	}
}

func metadataAnyString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	case json.Number:
		return strings.TrimSpace(v.String())
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
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
