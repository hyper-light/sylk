// Package context provides CV.3 ContextReference System for the Context Virtualization System.
// Context references are compact markers that replace evicted content while preserving
// retrieval capabilities.
package context

import (
	"fmt"
	"strings"
	"time"
)

// ReferenceType represents the type of context that was evicted.
type ReferenceType string

const (
	RefTypeConversation   ReferenceType = "conversation"    // User/agent exchanges
	RefTypeResearch       ReferenceType = "research"        // Academic findings
	RefTypeCodeAnalysis   ReferenceType = "code_analysis"   // Librarian discoveries
	RefTypeToolResults    ReferenceType = "tool_results"    // Tool call results
	RefTypePlanDiscussion ReferenceType = "plan_discussion" // Architect planning
)

// ValidReferenceTypes returns all valid reference types.
func ValidReferenceTypes() []ReferenceType {
	return []ReferenceType{
		RefTypeConversation,
		RefTypeResearch,
		RefTypeCodeAnalysis,
		RefTypeToolResults,
		RefTypePlanDiscussion,
	}
}

// IsValid returns true if the reference type is recognized.
func (rt ReferenceType) IsValid() bool {
	for _, valid := range ValidReferenceTypes() {
		if rt == valid {
			return true
		}
	}
	return false
}

// String returns the string representation.
func (rt ReferenceType) String() string {
	return string(rt)
}

// ContextReference represents a compact marker for evicted content.
// It preserves metadata for retrieval while removing the actual content
// from the active context window.
type ContextReference struct {
	ID          string        `json:"id"`
	Type        ReferenceType `json:"type"`
	SessionID   string        `json:"session_id"`
	AgentID     string        `json:"agent_id"`
	ContentIDs  []string      `json:"content_ids"`  // What was evicted
	Summary     string        `json:"summary"`      // One-line description
	TokensSaved int           `json:"tokens_saved"` // How much we saved
	TurnRange   [2]int        `json:"turn_range"`   // Turns [from, to]
	Timestamp   time.Time     `json:"timestamp"`
	Topics      []string      `json:"topics"`      // Main topics covered
	Entities    []string      `json:"entities"`    // Key entities mentioned
	QueryHints  []string      `json:"query_hints"` // Good retrieval queries
}

// Render renders the reference as a compact marker for inclusion in context.
func (r *ContextReference) Render() string {
	topicsStr := r.formatTopics()
	return fmt.Sprintf(
		"[CTX-REF:%s | %d turns (%d tokens) @ %s | Topics: %s | retrieve_context(ref_id=\"%s\")]",
		r.Type,
		r.turnCount(),
		r.TokensSaved,
		r.Timestamp.Format("15:04"),
		topicsStr,
		r.ID,
	)
}

func (r *ContextReference) formatTopics() string {
	maxTopics := 3
	if len(r.Topics) < maxTopics {
		maxTopics = len(r.Topics)
	}
	if maxTopics == 0 {
		return "none"
	}
	return strings.Join(r.Topics[:maxTopics], ", ")
}

func (r *ContextReference) turnCount() int {
	return r.TurnRange[1] - r.TurnRange[0] + 1
}

// RenderCompact renders a shorter version of the reference.
func (r *ContextReference) RenderCompact() string {
	return fmt.Sprintf("[REF:%s|%dt|%dtok]", r.Type, r.turnCount(), r.TokensSaved)
}

// IsEmpty returns true if the reference has no content IDs.
func (r *ContextReference) IsEmpty() bool {
	return len(r.ContentIDs) == 0
}

// ContentIDSet returns the content IDs as a set for efficient lookup.
func (r *ContextReference) ContentIDSet() map[string]bool {
	set := make(map[string]bool, len(r.ContentIDs))
	for _, id := range r.ContentIDs {
		set[id] = true
	}
	return set
}

// HasTopic returns true if the reference covers the given topic.
func (r *ContextReference) HasTopic(topic string) bool {
	lower := strings.ToLower(topic)
	for _, t := range r.Topics {
		if strings.Contains(strings.ToLower(t), lower) {
			return true
		}
	}
	return false
}

// HasEntity returns true if the reference mentions the given entity.
func (r *ContextReference) HasEntity(entity string) bool {
	lower := strings.ToLower(entity)
	for _, e := range r.Entities {
		if strings.Contains(strings.ToLower(e), lower) {
			return true
		}
	}
	return false
}

// MatchesQuery returns a relevance score (0-1) for the given query.
func (r *ContextReference) MatchesQuery(query string) float64 {
	lower := strings.ToLower(query)
	words := strings.Fields(lower)

	var matches float64
	for _, word := range words {
		if r.matchesWord(word) {
			matches++
		}
	}

	if len(words) == 0 {
		return 0
	}
	return matches / float64(len(words))
}

func (r *ContextReference) matchesWord(word string) bool {
	if containsWord(r.Topics, word) {
		return true
	}
	if containsWord(r.Entities, word) {
		return true
	}
	if containsWord(r.QueryHints, word) {
		return true
	}
	return strings.Contains(strings.ToLower(r.Summary), word)
}

func containsWord(slice []string, word string) bool {
	for _, s := range slice {
		if strings.Contains(strings.ToLower(s), word) {
			return true
		}
	}
	return false
}

// Clone creates a deep copy of the reference.
func (r *ContextReference) Clone() *ContextReference {
	if r == nil {
		return nil
	}

	return &ContextReference{
		ID:          r.ID,
		Type:        r.Type,
		SessionID:   r.SessionID,
		AgentID:     r.AgentID,
		ContentIDs:  cloneStringSlice(r.ContentIDs),
		Summary:     r.Summary,
		TokensSaved: r.TokensSaved,
		TurnRange:   r.TurnRange,
		Timestamp:   r.Timestamp,
		Topics:      cloneStringSlice(r.Topics),
		Entities:    cloneStringSlice(r.Entities),
		QueryHints:  cloneStringSlice(r.QueryHints),
	}
}

// Validate checks that the reference has valid required fields.
func (r *ContextReference) Validate() error {
	if err := r.validateID(); err != nil {
		return err
	}
	if err := r.validateType(); err != nil {
		return err
	}
	if err := r.validateContentIDs(); err != nil {
		return err
	}
	return r.validateSummary()
}

func (r *ContextReference) validateID() error {
	if r.ID == "" {
		return fmt.Errorf("reference ID cannot be empty")
	}
	return nil
}

func (r *ContextReference) validateType() error {
	if !r.Type.IsValid() {
		return fmt.Errorf("invalid reference type: %s", r.Type)
	}
	return nil
}

func (r *ContextReference) validateContentIDs() error {
	if len(r.ContentIDs) == 0 {
		return fmt.Errorf("reference must have at least one content ID")
	}
	return nil
}

func (r *ContextReference) validateSummary() error {
	if r.Summary == "" {
		return fmt.Errorf("reference summary cannot be empty")
	}
	return nil
}

// ToContentEntry converts the reference to a pseudo ContentEntry for rendering.
func (r *ContextReference) ToContentEntry() *ContentEntry {
	return &ContentEntry{
		ID:          r.ID,
		SessionID:   r.SessionID,
		AgentID:     r.AgentID,
		ContentType: ContentTypeContextRef,
		Content:     r.Render(),
		TokenCount:  len(r.Render()) / 4, // Rough estimate
		Timestamp:   r.Timestamp,
		TurnNumber:  r.TurnRange[0],
		Keywords:    r.Topics,
		Entities:    r.Entities,
	}
}
