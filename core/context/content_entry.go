// Package context provides content storage and context management for multi-agent systems.
// CV.1: ContentEntry type definitions for UniversalContentStore.
package context

import (
	"encoding/json"
	"strconv"
	"time"
)

// ContentType represents the type of content being stored.
type ContentType string

const (
	ContentTypeUserPrompt       ContentType = "user_prompt"
	ContentTypeAssistantReply   ContentType = "assistant_reply"
	ContentTypeToolCall         ContentType = "tool_call"
	ContentTypeToolResult       ContentType = "tool_result"
	ContentTypeCodeFile         ContentType = "code_file"
	ContentTypeResearchPaper    ContentType = "research_paper"
	ContentTypeWebFetch         ContentType = "web_fetch"
	ContentTypePlanWorkflow     ContentType = "plan_workflow"
	ContentTypeContextRef       ContentType = "context_reference"
	ContentTypeHandoffState     ContentType = "handoff_state"
	ContentTypeEvictionSummary  ContentType = "eviction_summary"
	ContentTypeIntentSnapshot   ContentType = "intent_snapshot"
	ContentTypeConstraint       ContentType = "constraint"
	ContentTypeSuccessCriterion ContentType = "success_criterion"
	ContentTypeDecision         ContentType = "decision"
	ContentTypeOutcome          ContentType = "outcome"
	ContentTypePreference       ContentType = "preference"
	ContentTypeEvidence         ContentType = "evidence"
	ContentTypeHypothesis       ContentType = "hypothesis"
	ContentTypeBranchSummary    ContentType = "branch_summary"
)

const (
	MetadataForestIntentID       = "forest.intent_id"
	MetadataForestBranchID       = "forest.branch_id"
	MetadataForestFacet          = "forest.facet"
	MetadataForestScope          = "forest.scope"
	MetadataForestConfidence     = "forest.confidence"
	MetadataForestSalience       = "forest.salience"
	MetadataForestOutcomeRef     = "forest.outcome_ref"
	MetadataForestProvenanceRefs = "forest.provenance_refs"
	MetadataForestSupersedes     = "forest.supersedes"
	MetadataForestContradicts    = "forest.contradicts"
	MetadataForestSynthetic      = "forest.synthetic"
)

// ContentEntry represents a single piece of content in the store.
type ContentEntry struct {
	ID             string            `json:"id"`
	SessionID      string            `json:"session_id"`
	AgentID        string            `json:"agent_id"`
	AgentType      string            `json:"agent_type"`
	ContentType    ContentType       `json:"content_type"`
	Content        string            `json:"content"`
	TokenCount     int               `json:"token_count"`
	Timestamp      time.Time         `json:"timestamp"`
	TurnNumber     int               `json:"turn_number"`
	Keywords       []string          `json:"keywords,omitempty"`
	Entities       []string          `json:"entities,omitempty"`
	ParentID       string            `json:"parent_id,omitempty"`
	RelatedFiles   []string          `json:"related_files,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Embedding      []float32         `json:"embedding,omitempty"`
	IntentID       string            `json:"intent_id,omitempty"`
	BranchID       string            `json:"branch_id,omitempty"`
	Facet          string            `json:"facet,omitempty"`
	Scope          string            `json:"scope,omitempty"`
	Confidence     float64           `json:"confidence,omitempty"`
	Salience       float64           `json:"salience,omitempty"`
	OutcomeRef     string            `json:"outcome_ref,omitempty"`
	ProvenanceRefs []string          `json:"provenance_refs,omitempty"`
	Supersedes     []string          `json:"supersedes,omitempty"`
	Contradicts    []string          `json:"contradicts,omitempty"`
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
		IntentID:    e.IntentID,
		BranchID:    e.BranchID,
		Facet:       e.Facet,
		Scope:       e.Scope,
		Confidence:  e.Confidence,
		Salience:    e.Salience,
		OutcomeRef:  e.OutcomeRef,
	}

	clone.Keywords = cloneStringSlice(e.Keywords)
	clone.Entities = cloneStringSlice(e.Entities)
	clone.RelatedFiles = cloneStringSlice(e.RelatedFiles)
	clone.Metadata = cloneStringMap(e.Metadata)
	clone.Embedding = cloneFloat32Slice(e.Embedding)
	clone.ProvenanceRefs = cloneStringSlice(e.ProvenanceRefs)
	clone.Supersedes = cloneStringSlice(e.Supersedes)
	clone.Contradicts = cloneStringSlice(e.Contradicts)

	return clone
}

func (e *ContentEntry) EffectiveMetadata() map[string]string {
	metadata := cloneStringMap(e.Metadata)
	if metadata == nil {
		metadata = make(map[string]string)
	}

	setMetadataValue(metadata, MetadataForestIntentID, e.IntentID)
	setMetadataValue(metadata, MetadataForestBranchID, e.BranchID)
	setMetadataValue(metadata, MetadataForestFacet, e.Facet)
	setMetadataValue(metadata, MetadataForestScope, e.Scope)
	setMetadataFloat(metadata, MetadataForestConfidence, e.Confidence)
	setMetadataFloat(metadata, MetadataForestSalience, e.Salience)
	setMetadataValue(metadata, MetadataForestOutcomeRef, e.OutcomeRef)
	setMetadataSlice(metadata, MetadataForestProvenanceRefs, e.ProvenanceRefs)
	setMetadataSlice(metadata, MetadataForestSupersedes, e.Supersedes)
	setMetadataSlice(metadata, MetadataForestContradicts, e.Contradicts)

	return metadata
}

func (e *ContentEntry) HydrateForestMetadata() {
	if e == nil || len(e.Metadata) == 0 {
		return
	}

	if e.IntentID == "" {
		e.IntentID = e.Metadata[MetadataForestIntentID]
	}
	if e.BranchID == "" {
		e.BranchID = e.Metadata[MetadataForestBranchID]
	}
	if e.Facet == "" {
		e.Facet = e.Metadata[MetadataForestFacet]
	}
	if e.Scope == "" {
		e.Scope = e.Metadata[MetadataForestScope]
	}
	if e.Confidence == 0 {
		e.Confidence = parseMetadataFloat(e.Metadata[MetadataForestConfidence])
	}
	if e.Salience == 0 {
		e.Salience = parseMetadataFloat(e.Metadata[MetadataForestSalience])
	}
	if e.OutcomeRef == "" {
		e.OutcomeRef = e.Metadata[MetadataForestOutcomeRef]
	}
	if len(e.ProvenanceRefs) == 0 {
		e.ProvenanceRefs = parseMetadataSlice(e.Metadata[MetadataForestProvenanceRefs])
	}
	if len(e.Supersedes) == 0 {
		e.Supersedes = parseMetadataSlice(e.Metadata[MetadataForestSupersedes])
	}
	if len(e.Contradicts) == 0 {
		e.Contradicts = parseMetadataSlice(e.Metadata[MetadataForestContradicts])
	}
}

func setMetadataValue(metadata map[string]string, key, value string) {
	if value == "" {
		return
	}
	metadata[key] = value
}

func setMetadataFloat(metadata map[string]string, key string, value float64) {
	if value == 0 {
		return
	}
	metadata[key] = strconv.FormatFloat(value, 'f', 4, 64)
}

func setMetadataSlice(metadata map[string]string, key string, values []string) {
	if len(values) == 0 {
		return
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return
	}
	metadata[key] = string(payload)
}

func parseMetadataFloat(raw string) float64 {
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0
	}
	return value
}

func parseMetadataSlice(raw string) []string {
	if raw == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
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
