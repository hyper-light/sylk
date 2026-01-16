package archivalist

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFactExtractor_ExtractDecision(t *testing.T) {
	extractor := NewFactExtractor("test-session")

	entry := &Entry{
		ID:       "entry-1",
		Category: CategoryDecision,
		Title:    "Use PostgreSQL over MySQL",
		Content:  "PostgreSQL has better JSON support and JSONB type for our use case",
		Source:   SourceModelClaudeOpus45,
	}

	facts := extractor.ExtractAll([]*Entry{entry})

	require.Len(t, facts.Decisions, 1)
	decision := facts.Decisions[0]

	assert.Equal(t, "Use PostgreSQL over MySQL", decision.Choice)
	assert.Contains(t, decision.Rationale, "PostgreSQL")
	assert.Equal(t, "test-session", decision.SessionID)
	assert.Equal(t, []string{"entry-1"}, decision.SourceEntryIDs)
	assert.Equal(t, 1.0, decision.Confidence)
}

func TestFactExtractor_ExtractFailure(t *testing.T) {
	extractor := NewFactExtractor("test-session")

	entry := &Entry{
		ID:       "entry-1",
		Category: CategoryIssue,
		Title:    "Recursive file walking",
		Content:  "Recursive file walking without depth limit caused memory exhaustion",
		Source:   SourceModelClaudeOpus45,
		Metadata: map[string]any{
			"resolution": "Added max depth of 10",
		},
	}

	facts := extractor.ExtractAll([]*Entry{entry})

	require.Len(t, facts.Failures, 1)
	failure := facts.Failures[0]

	assert.Equal(t, "Recursive file walking", failure.Approach)
	assert.Contains(t, failure.Reason, "memory exhaustion")
	assert.Equal(t, "Added max depth of 10", failure.Resolution)
	assert.Equal(t, "test-session", failure.SessionID)
}

func TestFactExtractor_ExtractPattern(t *testing.T) {
	extractor := NewFactExtractor("test-session")

	entry := &Entry{
		ID:       "entry-1",
		Category: CategoryCodeStyle,
		Title:    "Error handling pattern",
		Content:  "Always wrap errors with context using fmt.Errorf with %w",
		Source:   SourceModelClaudeOpus45,
		Metadata: map[string]any{
			"category": "error-handling",
			"example":  `fmt.Errorf("failed to process: %w", err)`,
		},
	}

	facts := extractor.ExtractAll([]*Entry{entry})

	require.Len(t, facts.Patterns, 1)
	pattern := facts.Patterns[0]

	assert.Equal(t, "error-handling", pattern.Category)
	assert.Equal(t, "Error handling pattern", pattern.Name)
	assert.Contains(t, pattern.Pattern, "fmt.Errorf")
	assert.Contains(t, pattern.Example, "%w")
}

func TestFactExtractor_ExtractFromGeneral_Decision(t *testing.T) {
	extractor := NewFactExtractor("test-session")

	entry := &Entry{
		ID:       "entry-1",
		Category: CategoryGeneral,
		Content:  "We decided to use gRPC instead of REST for the internal API",
		Source:   SourceModelClaudeOpus45,
	}

	facts := extractor.ExtractAll([]*Entry{entry})

	require.Len(t, facts.Decisions, 1)
	assert.Contains(t, facts.Decisions[0].Choice, "gRPC")
	assert.Equal(t, 0.7, facts.Decisions[0].Confidence) // Lower confidence for pattern match
}

func TestFactExtractor_ExtractFromGeneral_Failure(t *testing.T) {
	extractor := NewFactExtractor("test-session")

	entry := &Entry{
		ID:       "entry-1",
		Category: CategoryGeneral,
		Content:  "The approach failed because the API rate limits were exceeded",
		Source:   SourceModelClaudeOpus45,
	}

	facts := extractor.ExtractAll([]*Entry{entry})

	require.Len(t, facts.Failures, 1)
	assert.Contains(t, facts.Failures[0].Approach, "API rate limits")
}

func TestFactExtractor_ExtractFileChanges(t *testing.T) {
	extractor := NewFactExtractor("test-session")

	entries := []*Entry{
		{
			ID:       "entry-1",
			Category: CategoryGeneral,
			Content:  "Added error handling to the process function",
			Source:   SourceModelClaudeOpus45,
			Metadata: map[string]any{
				"path":        "/src/process.go",
				"change_type": "modified",
				"line_start":  float64(100),
				"line_end":    float64(120),
			},
		},
		{
			ID:       "entry-2",
			Category: CategoryGeneral,
			Content:  "Created new config file",
			Source:   SourceModelClaudeOpus45,
			Metadata: map[string]any{
				"path":        "/config/settings.yaml",
				"change_type": "created",
			},
		},
	}

	changes := extractor.ExtractFileChanges(entries)

	require.Len(t, changes, 2)

	assert.Equal(t, "/src/process.go", changes[0].Path)
	assert.Equal(t, FileChangeTypeModified, changes[0].ChangeType)
	assert.Equal(t, 100, changes[0].LineStart)
	assert.Equal(t, 120, changes[0].LineEnd)

	assert.Equal(t, "/config/settings.yaml", changes[1].Path)
	assert.Equal(t, FileChangeTypeCreated, changes[1].ChangeType)
}

func TestMergeFacts_Deduplication(t *testing.T) {
	facts1 := &ExtractedFacts{
		Decisions: []*FactDecision{
			{ID: "1", Choice: "Use PostgreSQL"},
		},
		Patterns: []*FactPattern{
			{ID: "1", Category: "error", Name: "wrap errors"},
		},
	}

	facts2 := &ExtractedFacts{
		Decisions: []*FactDecision{
			{ID: "2", Choice: "Use PostgreSQL"}, // Duplicate by choice
			{ID: "3", Choice: "Use Redis"},
		},
		Patterns: []*FactPattern{
			{ID: "2", Category: "error", Name: "wrap errors"}, // Duplicate by category:name
			{ID: "3", Category: "naming", Name: "use camelCase"},
		},
	}

	merged := MergeFacts(facts1, facts2)

	assert.Len(t, merged.Decisions, 2) // PostgreSQL + Redis
	assert.Len(t, merged.Patterns, 2)  // wrap errors + camelCase
}

func TestExtractAlternatives(t *testing.T) {
	tests := []struct {
		content  string
		expected []string
	}{
		{
			content:  "Chose PostgreSQL over MySQL and SQLite",
			expected: []string{"MySQL", "SQLite"},
		},
		{
			content:  "Selected gRPC instead of REST",
			expected: []string{"REST"},
		},
		{
			content:  "Alternatives: Redis, Memcached, in-memory cache",
			expected: []string{"Redis", "Memcached", "in-memory cache"},
		},
	}

	for _, tt := range tests {
		alternatives := extractAlternatives(tt.content)
		assert.Equal(t, tt.expected, alternatives, "content: %s", tt.content)
	}
}

func TestFirstSentence(t *testing.T) {
	tests := []struct {
		text     string
		expected string
	}{
		{
			text:     "This is the first sentence. This is the second.",
			expected: "This is the first sentence.",
		},
		{
			text:     "Is this a question? Yes it is.",
			expected: "Is this a question?",
		},
		{
			text:     "Short text",
			expected: "Short text",
		},
	}

	for _, tt := range tests {
		result := firstSentence(tt.text)
		assert.Equal(t, tt.expected, result)
	}
}

func TestFactExtractor_AllCategories(t *testing.T) {
	extractor := NewFactExtractor("test-session")

	entries := []*Entry{
		{ID: "1", Category: CategoryDecision, Title: "Decision", Content: "Choose A", CreatedAt: time.Now()},
		{ID: "2", Category: CategoryIssue, Title: "Issue", Content: "Problem B", CreatedAt: time.Now()},
		{ID: "3", Category: CategoryCodeStyle, Title: "Style", Content: "Pattern C", CreatedAt: time.Now()},
		{ID: "4", Category: CategoryCodebaseMap, Title: "Map", Content: "Structure D", CreatedAt: time.Now()},
		{ID: "5", Category: CategoryGeneral, Content: "We decided to use approach E", CreatedAt: time.Now()},
	}

	facts := extractor.ExtractAll(entries)

	// Should have extracted from all relevant categories
	assert.GreaterOrEqual(t, len(facts.Decisions), 1)
	assert.GreaterOrEqual(t, len(facts.Failures), 1)
	assert.GreaterOrEqual(t, len(facts.Patterns), 2) // CodeStyle and CodebaseMap
}
