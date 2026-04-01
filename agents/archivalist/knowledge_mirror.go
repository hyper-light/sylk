package archivalist

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/knowledgeruntime"
	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
)

func (a *Archivalist) mirrorStoredEntryToKnowledge(ctx context.Context, entry *Entry, result SubmissionResult) error {
	if a == nil || a.knowledgeWriter == nil || entry == nil || !result.Success {
		return nil
	}
	if !entryShouldMirrorToKnowledge(entry) {
		return nil
	}

	storedID := strings.TrimSpace(result.ID)
	if storedID == "" {
		storedID = strings.TrimSpace(entry.ID)
	}
	if storedID == "" {
		return fmt.Errorf("scribe knowledge mirror: stored entry id is required")
	}

	req := &knowledgeruntime.TextDocumentIngestRequest{
		DocumentID: "archivalist_entry_" + storedID,
		Path:       archivalistKnowledgePath(entry, storedID),
		Content:    archivalistKnowledgeContent(entry, storedID),
		DocType:    search.DocTypeMarkdown,
		Language:   "markdown",
		Domain:     sylkdir.DomainDoc,
		Metadata: map[string]string{
			"entry_id":             storedID,
			"entry_category":       string(entry.Category),
			"origin_source_type":   firstMetadataString(entry.Metadata, originSourceTypeKey, "source_type"),
			"origin_agent_type":    firstMetadataString(entry.Metadata, originAgentTypeKey, "origin_agent", "parent_agent"),
			"origin_agent_id":      firstMetadataString(entry.Metadata, originAgentIDKey),
			"ingest_agent_id":      firstMetadataString(entry.Metadata, ingestAgentIDKey),
			"ingest_agent_name":    firstMetadataString(entry.Metadata, ingestAgentNameKey),
			"cross_agent_ingested": fmt.Sprintf("%t", isCrossAgentEntry(entry)),
		},
	}
	return a.knowledgeWriter.UpsertTextDocument(ctx, req)
}

func entryShouldMirrorToKnowledge(entry *Entry) bool {
	if entry == nil {
		return false
	}
	sourceType := strings.ToLower(strings.TrimSpace(firstMetadataString(entry.Metadata, originSourceTypeKey, "source_type")))
	if sourceType == "scribe" {
		return true
	}
	agentType := strings.ToLower(strings.TrimSpace(firstMetadataString(entry.Metadata, originAgentTypeKey, "origin_agent", "parent_agent")))
	return strings.HasPrefix(agentType, "scribe-")
}

func archivalistKnowledgePath(entry *Entry, storedID string) string {
	sourceType := firstNonEmptyKnowledgeString(
		firstMetadataString(entry.Metadata, originSourceTypeKey, "source_type"),
		"entry",
	)
	agentType := firstNonEmptyKnowledgeString(
		firstMetadataString(entry.Metadata, originAgentTypeKey, "origin_agent", "parent_agent"),
		"unknown",
	)
	return path.Join("archivalist", sanitizeKnowledgePathSegment(sourceType), sanitizeKnowledgePathSegment(agentType), storedID+".md")
}

func archivalistKnowledgeContent(entry *Entry, storedID string) string {
	if entry == nil {
		return ""
	}
	lines := []string{
		"# Archivalist Entry",
		"",
		"entry_id: " + storedID,
		"category: " + string(entry.Category),
		"source: " + string(entry.Source),
	}
	if entry.SessionID != "" {
		lines = append(lines, "session_id: "+entry.SessionID)
	}
	if !entry.CreatedAt.IsZero() {
		lines = append(lines, "created_at: "+entry.CreatedAt.UTC().Format(time.RFC3339Nano))
	}
	if title := strings.TrimSpace(entry.Title); title != "" {
		lines = append(lines, "title: "+title)
	}
	if len(entry.Metadata) > 0 {
		if meta, err := json.Marshal(entry.Metadata); err == nil && len(meta) > 0 {
			lines = append(lines, "metadata: "+string(meta))
		}
	}
	lines = append(lines, "", strings.TrimSpace(entry.Content))
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func sanitizeKnowledgePathSegment(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "unknown"
	}
	value = strings.ReplaceAll(value, "\\", "/")
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func firstMetadataString(metadata map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := metadataString(metadata, key); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonEmptyKnowledgeString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
