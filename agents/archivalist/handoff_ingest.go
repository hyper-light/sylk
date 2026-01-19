// Package archivalist provides helper functions for handoff state ingestion.
// PH.6: Coordinates handoff ingestion into both Bleve and VectorDB storage.
package archivalist

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// HandoffRecord represents an archived handoff state record.
type HandoffRecord struct {
	ID             string          `json:"id"`
	AgentType      string          `json:"agent_type"`
	SessionID      string          `json:"session_id"`
	PipelineID     string          `json:"pipeline_id,omitempty"`
	HandoffIndex   int             `json:"handoff_index"`
	ContextUsage   float64         `json:"context_usage_percent"`
	HandoffReason  string          `json:"handoff_reason"`
	State          json.RawMessage `json:"state"`
	StateSummary   string          `json:"state_summary"`
	Timestamp      time.Time       `json:"timestamp"`
	OriginalPrompt string          `json:"original_prompt,omitempty"`
	Accomplished   []string        `json:"accomplished,omitempty"`
	Remaining      []string        `json:"remaining,omitempty"`
}

// HandoffIngestConfig configures handoff ingestion behavior.
type HandoffIngestConfig struct {
	MaxSummaryLength int           // Maximum length of state summary
	IndexTimeout     time.Duration // Timeout for index operations
	EnableBleve      bool          // Whether to index in Bleve
	EnableVector     bool          // Whether to index in VectorDB
}

// DefaultHandoffIngestConfig returns sensible defaults.
func DefaultHandoffIngestConfig() HandoffIngestConfig {
	return HandoffIngestConfig{
		MaxSummaryLength: 2000,
		IndexTimeout:     5 * time.Second,
		EnableBleve:      true,
		EnableVector:     true,
	}
}

// HandoffIngester handles ingestion of handoff records into archivalist storage.
type HandoffIngester struct {
	archivalist ArchivalistService
	config      HandoffIngestConfig
}

// NewHandoffIngester creates a new ingester with the given archivalist service.
func NewHandoffIngester(svc ArchivalistService, config HandoffIngestConfig) *HandoffIngester {
	if config.MaxSummaryLength == 0 {
		config = DefaultHandoffIngestConfig()
	}

	return &HandoffIngester{
		archivalist: svc,
		config:      config,
	}
}

// IngestHandoff stores a handoff record in the archivalist.
func (h *HandoffIngester) IngestHandoff(ctx context.Context, record *HandoffRecord) error {
	if record == nil {
		return fmt.Errorf("record cannot be nil")
	}

	entry := h.buildEntry(record)
	result := h.archivalist.StoreEntry(ctx, entry)
	if !result.Success {
		return fmt.Errorf("failed to store handoff: %v", result.Error)
	}

	return nil
}

// buildEntry converts a HandoffRecord to an archivalist Entry.
func (h *HandoffIngester) buildEntry(record *HandoffRecord) *Entry {
	category := BuildHandoffCategory(record.AgentType)

	metadata := map[string]any{
		"handoff_index":         record.HandoffIndex,
		"context_usage_percent": record.ContextUsage,
		"handoff_reason":        record.HandoffReason,
		"pipeline_id":           record.PipelineID,
	}

	content := h.buildContent(record)

	return &Entry{
		ID:             record.ID,
		Category:       category,
		Title:          BuildHandoffTitle(record.AgentType, record.HandoffIndex),
		Content:        content,
		Source:         SourceModelArchivalist,
		SessionID:      record.SessionID,
		CreatedAt:      record.Timestamp,
		UpdatedAt:      record.Timestamp,
		TokensEstimate: EstimateTokens(content),
		Metadata:       metadata,
	}
}

// buildContent creates the searchable content from a handoff record.
func (h *HandoffIngester) buildContent(record *HandoffRecord) string {
	content := record.StateSummary
	if record.OriginalPrompt != "" {
		content += "\n\nOriginal Prompt: " + record.OriginalPrompt
	}

	if len(record.Accomplished) > 0 {
		content += "\n\nAccomplished:"
		for _, a := range record.Accomplished {
			content += "\n- " + a
		}
	}

	if len(record.Remaining) > 0 {
		content += "\n\nRemaining:"
		for _, r := range record.Remaining {
			content += "\n- " + r
		}
	}

	return h.truncateContent(content)
}

// truncateContent ensures content doesn't exceed max length.
func (h *HandoffIngester) truncateContent(content string) string {
	if len(content) <= h.config.MaxSummaryLength {
		return content
	}
	return content[:h.config.MaxSummaryLength-3] + "..."
}

// QueryHandoffsByAgent retrieves handoffs for a specific agent type.
func (h *HandoffIngester) QueryHandoffsByAgent(
	ctx context.Context,
	agentType string,
	limit int,
) ([]*HandoffRecord, error) {
	category := BuildHandoffCategory(agentType)
	entries := h.archivalist.QueryByCategory(ctx, category, limit)
	return h.entriesToRecords(entries), nil
}

// QueryHandoffsBySession retrieves all handoffs for a session.
func (h *HandoffIngester) QueryHandoffsBySession(
	ctx context.Context,
	sessionID string,
) ([]*HandoffRecord, error) {
	query := ArchiveQuery{
		SessionIDs: []string{sessionID},
		Categories: handoffCategories(),
	}

	entries, err := h.archivalist.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}

	return h.entriesToRecords(entries), nil
}

// QueryHandoffsByPattern searches for handoffs matching a text pattern.
func (h *HandoffIngester) QueryHandoffsByPattern(
	ctx context.Context,
	pattern string,
	limit int,
) ([]*HandoffRecord, error) {
	entries, err := h.archivalist.SearchText(ctx, pattern, false, limit)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Filter to only handoff categories
	filtered := filterHandoffEntries(entries)
	return h.entriesToRecords(filtered), nil
}

// entriesToRecords converts archivalist entries to handoff records.
func (h *HandoffIngester) entriesToRecords(entries []*Entry) []*HandoffRecord {
	records := make([]*HandoffRecord, 0, len(entries))
	for _, e := range entries {
		record := h.entryToRecord(e)
		if record != nil {
			records = append(records, record)
		}
	}
	return records
}

// entryToRecord converts a single entry to a handoff record.
func (h *HandoffIngester) entryToRecord(e *Entry) *HandoffRecord {
	if e == nil {
		return nil
	}

	record := &HandoffRecord{
		ID:           e.ID,
		AgentType:    extractAgentType(e.Category),
		SessionID:    e.SessionID,
		StateSummary: e.Content,
		Timestamp:    e.CreatedAt,
	}

	h.extractMetadata(e.Metadata, record)
	return record
}

// extractMetadata populates record fields from entry metadata.
func (h *HandoffIngester) extractMetadata(metadata map[string]any, record *HandoffRecord) {
	if metadata == nil {
		return
	}

	if v, ok := metadata["handoff_index"].(int); ok {
		record.HandoffIndex = v
	}
	if v, ok := metadata["handoff_index"].(float64); ok {
		record.HandoffIndex = int(v)
	}
	if v, ok := metadata["context_usage_percent"].(float64); ok {
		record.ContextUsage = v
	}
	if v, ok := metadata["handoff_reason"].(string); ok {
		record.HandoffReason = v
	}
	if v, ok := metadata["pipeline_id"].(string); ok {
		record.PipelineID = v
	}
}

// BuildHandoffCategory creates the category string for handoff entries.
func BuildHandoffCategory(agentType string) Category {
	return Category(fmt.Sprintf("agent_handoff_%s", agentType))
}

// BuildHandoffTitle creates a descriptive title for a handoff entry.
func BuildHandoffTitle(agentType string, index int) string {
	return fmt.Sprintf("%s handoff #%d", agentType, index)
}

// extractAgentType extracts the agent type from a handoff category.
func extractAgentType(category Category) string {
	const prefix = "agent_handoff_"
	s := string(category)
	if len(s) > len(prefix) {
		return s[len(prefix):]
	}
	return string(category)
}

// handoffCategories returns all known handoff categories.
func handoffCategories() []Category {
	agentTypes := []string{
		"engineer", "designer", "inspector", "tester",
		"orchestrator", "guide", "architect",
		"librarian", "archivalist", "academic",
	}

	categories := make([]Category, len(agentTypes))
	for i, t := range agentTypes {
		categories[i] = BuildHandoffCategory(t)
	}
	return categories
}

// filterHandoffEntries filters entries to only those with handoff categories.
func filterHandoffEntries(entries []*Entry) []*Entry {
	handoffCats := make(map[Category]bool)
	for _, c := range handoffCategories() {
		handoffCats[c] = true
	}

	filtered := make([]*Entry, 0, len(entries))
	for _, e := range entries {
		if handoffCats[e.Category] {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// IsHandoffCategory checks if a category is a handoff category.
func IsHandoffCategory(category Category) bool {
	const prefix = "agent_handoff_"
	return len(string(category)) > len(prefix) &&
		string(category)[:len(prefix)] == prefix
}

// HandoffSubmission represents a handoff state submission request.
type HandoffSubmission struct {
	AgentID        string          `json:"agent_id"`
	AgentType      string          `json:"agent_type"`
	SessionID      string          `json:"session_id"`
	PipelineID     string          `json:"pipeline_id,omitempty"`
	HandoffIndex   int             `json:"handoff_index"`
	ContextUsage   float64         `json:"context_usage_percent"`
	TriggerReason  string          `json:"trigger_reason"`
	State          json.RawMessage `json:"state"`
	Summary        string          `json:"summary"`
	OriginalPrompt string          `json:"original_prompt,omitempty"`
	Accomplished   []string        `json:"accomplished,omitempty"`
	Remaining      []string        `json:"remaining,omitempty"`
}

// ToRecord converts a submission to a HandoffRecord.
func (s *HandoffSubmission) ToRecord() *HandoffRecord {
	return &HandoffRecord{
		ID:             generateHandoffID(s),
		AgentType:      s.AgentType,
		SessionID:      s.SessionID,
		PipelineID:     s.PipelineID,
		HandoffIndex:   s.HandoffIndex,
		ContextUsage:   s.ContextUsage,
		HandoffReason:  s.TriggerReason,
		State:          s.State,
		StateSummary:   s.Summary,
		Timestamp:      time.Now(),
		OriginalPrompt: s.OriginalPrompt,
		Accomplished:   s.Accomplished,
		Remaining:      s.Remaining,
	}
}

// generateHandoffID creates a unique ID for a handoff record.
func generateHandoffID(s *HandoffSubmission) string {
	return fmt.Sprintf("handoff_%s_%s_%d_%d",
		s.AgentType,
		s.SessionID,
		s.HandoffIndex,
		time.Now().UnixNano(),
	)
}
