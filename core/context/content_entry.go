// Package context provides content storage and context management for multi-agent systems.
// CV.1: ContentEntry type definitions for UniversalContentStore.
package context

import (
	"encoding/json"
	"time"
)

// ContentType represents the type of content being stored.
type ContentType string

const (
	ContentTypeUserPrompt      ContentType = "user_prompt"
	ContentTypeAssistantReply  ContentType = "assistant_reply"
	ContentTypeToolCall        ContentType = "tool_call"
	ContentTypeToolResult      ContentType = "tool_result"
	ContentTypeCodeFile        ContentType = "code_file"
	ContentTypeResearchPaper   ContentType = "research_paper"
	ContentTypeWebFetch        ContentType = "web_fetch"
	ContentTypePlanWorkflow    ContentType = "plan_workflow"
	ContentTypeContextRef      ContentType = "context_reference"
	ContentTypeHandoffState    ContentType = "handoff_state"
	ContentTypeEvictionSummary ContentType = "eviction_summary"
)

// ContentEntry represents a single piece of content in the store.
type ContentEntry struct {
	ID           string            `json:"id"`
	SessionID    string            `json:"session_id"`
	AgentID      string            `json:"agent_id"`
	AgentType    string            `json:"agent_type"`
	ContentType  ContentType       `json:"content_type"`
	Content      string            `json:"content"`
	TokenCount   int               `json:"token_count"`
	Timestamp    time.Time         `json:"timestamp"`
	TurnNumber   int               `json:"turn_number"`
	Keywords     []string          `json:"keywords,omitempty"`
	Entities     []string          `json:"entities,omitempty"`
	ParentID     string            `json:"parent_id,omitempty"`
	RelatedFiles []string          `json:"related_files,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Embedding    []float32         `json:"embedding,omitempty"`
}

// MarshalJSON serializes the content entry to JSON.
func (e *ContentEntry) MarshalJSON() ([]byte, error) {
	type Alias ContentEntry
	return json.Marshal((*Alias)(e))
}

// UnmarshalJSON deserializes the content entry from JSON.
func (e *ContentEntry) UnmarshalJSON(data []byte) error {
	type Alias ContentEntry
	return json.Unmarshal(data, (*Alias)(e))
}

// Clone creates a deep copy of the content entry.
func (e *ContentEntry) Clone() *ContentEntry {
	if e == nil {
		return nil
	}

	clone := &ContentEntry{
		ID:          e.ID,
		SessionID:   e.SessionID,
		AgentID:     e.AgentID,
		AgentType:   e.AgentType,
		ContentType: e.ContentType,
		Content:     e.Content,
		TokenCount:  e.TokenCount,
		Timestamp:   e.Timestamp,
		TurnNumber:  e.TurnNumber,
		ParentID:    e.ParentID,
	}

	clone.Keywords = cloneStringSlice(e.Keywords)
	clone.Entities = cloneStringSlice(e.Entities)
	clone.RelatedFiles = cloneStringSlice(e.RelatedFiles)
	clone.Metadata = cloneStringMap(e.Metadata)
	clone.Embedding = cloneFloat32Slice(e.Embedding)

	return clone
}

func cloneStringSlice(s []string) []string {
	if s == nil {
		return nil
	}
	c := make([]string, len(s))
	copy(c, s)
	return c
}

func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func cloneFloat32Slice(s []float32) []float32 {
	if s == nil {
		return nil
	}
	c := make([]float32, len(s))
	copy(c, s)
	return c
}

// SearchFilters defines filters for content searches.
type SearchFilters struct {
	SessionID    string        `json:"session_id,omitempty"`
	AgentID      string        `json:"agent_id,omitempty"`
	AgentTypes   []string      `json:"agent_types,omitempty"`
	ContentTypes []ContentType `json:"content_types,omitempty"`
	TurnRange    [2]int        `json:"turn_range,omitempty"`
	TimeRange    [2]time.Time  `json:"time_range,omitempty"`
	Keywords     []string      `json:"keywords,omitempty"`
}

// HasSessionFilter returns true if a session filter is set.
func (f *SearchFilters) HasSessionFilter() bool {
	return f != nil && f.SessionID != ""
}

// HasAgentFilter returns true if an agent filter is set.
func (f *SearchFilters) HasAgentFilter() bool {
	return f != nil && f.AgentID != ""
}

// HasTurnRange returns true if a turn range filter is set.
func (f *SearchFilters) HasTurnRange() bool {
	return f != nil && (f.TurnRange[0] > 0 || f.TurnRange[1] > 0)
}

// HasTimeRange returns true if a time range filter is set.
func (f *SearchFilters) HasTimeRange() bool {
	return f != nil && (!f.TimeRange[0].IsZero() || !f.TimeRange[1].IsZero())
}
