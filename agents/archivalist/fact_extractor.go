package archivalist

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// FactExtractor extracts structured facts from entries
type FactExtractor struct {
	sessionID string

	// Patterns for extraction
	decisionPatterns []string
	failurePatterns  []string
	patternPatterns  []string
}

// NewFactExtractor creates a new fact extractor
func NewFactExtractor(sessionID string) *FactExtractor {
	return &FactExtractor{
		sessionID: sessionID,
		decisionPatterns: []string{
			`(?i)decided?\s+to\s+(.+)`,
			`(?i)chose\s+(.+)\s+(?:over|instead of)`,
			`(?i)will\s+(?:use|go with)\s+(.+)`,
			`(?i)selected\s+(.+)\s+because`,
		},
		failurePatterns: []string{
			`(?i)(?:failed|error|broke)\s+(?:when|while|because)\s+(.+)`,
			`(?i)didn'?t\s+work\s+because\s+(.+)`,
			`(?i)approach\s+(?:failed|didn't work):\s*(.+)`,
			`(?i)tried\s+(.+)\s+but\s+(?:it\s+)?failed`,
		},
		patternPatterns: []string{
			`(?i)pattern:\s*(.+)`,
			`(?i)(?:always|should|must)\s+(.+)\s+when`,
			`(?i)convention:\s*(.+)`,
		},
	}
}

// ExtractAll extracts all types of facts from entries
func (e *FactExtractor) ExtractAll(entries []*Entry) *ExtractedFacts {
	result := &ExtractedFacts{
		Decisions:   make([]*FactDecision, 0),
		Patterns:    make([]*FactPattern, 0),
		Failures:    make([]*FactFailure, 0),
		FileChanges: make([]*FactFileChange, 0),
	}

	for _, entry := range entries {
		switch entry.Category {
		case CategoryDecision:
			if decision := e.extractDecision(entry); decision != nil {
				result.Decisions = append(result.Decisions, decision)
			}
		case CategoryIssue:
			if failure := e.extractFailure(entry); failure != nil {
				result.Failures = append(result.Failures, failure)
			}
		case CategoryCodeStyle, CategoryCodebaseMap:
			if pattern := e.extractPattern(entry); pattern != nil {
				result.Patterns = append(result.Patterns, pattern)
			}
		default:
			// Try to extract facts from general entries based on content
			e.extractFromGeneral(entry, result)
		}
	}

	return result
}

// ExtractedFacts holds all extracted facts from a batch of entries
type ExtractedFacts struct {
	Decisions   []*FactDecision
	Patterns    []*FactPattern
	Failures    []*FactFailure
	FileChanges []*FactFileChange
}

// extractDecision extracts a decision fact from a decision entry
func (e *FactExtractor) extractDecision(entry *Entry) *FactDecision {
	choice := entry.Title
	if choice == "" {
		choice = firstSentence(entry.Content)
	}

	rationale := entry.Content
	if entry.Title != "" {
		rationale = entry.Content
	}

	// Extract alternatives from content if present
	alternatives := extractAlternatives(entry.Content)

	return &FactDecision{
		ID:             uuid.New().String(),
		Choice:         choice,
		Rationale:      rationale,
		Context:        extractContext(entry.Content),
		Alternatives:   alternatives,
		Confidence:     1.0,
		SourceEntryIDs: []string{entry.ID},
		SessionID:      e.sessionID,
		ExtractedAt:    time.Now(),
	}
}

// extractFailure extracts a failure fact from an issue entry
func (e *FactExtractor) extractFailure(entry *Entry) *FactFailure {
	approach := entry.Title
	if approach == "" {
		approach = firstSentence(entry.Content)
	}

	// Look for resolution in metadata
	resolution := ""
	if entry.Metadata != nil {
		if res, ok := entry.Metadata["resolution"].(string); ok {
			resolution = res
		}
	}

	return &FactFailure{
		ID:             uuid.New().String(),
		Approach:       approach,
		Reason:         entry.Content,
		Context:        extractContext(entry.Content),
		Resolution:     resolution,
		SourceEntryIDs: []string{entry.ID},
		SessionID:      e.sessionID,
		ExtractedAt:    time.Now(),
	}
}

// extractPattern extracts a pattern fact from a style/codebase entry
func (e *FactExtractor) extractPattern(entry *Entry) *FactPattern {
	name := entry.Title
	if name == "" {
		name = firstSentence(entry.Content)
	}

	category := "general"
	if entry.Metadata != nil {
		if cat, ok := entry.Metadata["category"].(string); ok {
			category = cat
		}
	}

	example := ""
	if entry.Metadata != nil {
		if ex, ok := entry.Metadata["example"].(string); ok {
			example = ex
		}
	}

	return &FactPattern{
		ID:             uuid.New().String(),
		Category:       category,
		Name:           name,
		Pattern:        entry.Content,
		Example:        example,
		UsageCount:     1,
		SourceEntryIDs: []string{entry.ID},
		SessionID:      e.sessionID,
		ExtractedAt:    time.Now(),
	}
}

// extractFromGeneral tries to extract facts from general entries
func (e *FactExtractor) extractFromGeneral(entry *Entry, result *ExtractedFacts) {
	content := entry.Content

	if e.tryExtractDecision(content, entry, result) {
		return
	}
	if e.tryExtractFailure(content, entry, result) {
		return
	}
	e.tryExtractPattern(content, entry, result)
}

func (e *FactExtractor) tryExtractDecision(content string, entry *Entry, result *ExtractedFacts) bool {
	return e.extractWithPatterns(content, e.decisionPatterns, func(match string) {
		result.Decisions = append(result.Decisions, &FactDecision{
			ID:             uuid.New().String(),
			Choice:         strings.TrimSpace(match),
			Rationale:      content,
			Confidence:     0.7,
			SourceEntryIDs: []string{entry.ID},
			SessionID:      e.sessionID,
			ExtractedAt:    time.Now(),
		})
	})
}

func (e *FactExtractor) tryExtractFailure(content string, entry *Entry, result *ExtractedFacts) bool {
	return e.extractWithPatterns(content, e.failurePatterns, func(match string) {
		result.Failures = append(result.Failures, &FactFailure{
			ID:             uuid.New().String(),
			Approach:       strings.TrimSpace(match),
			Reason:         content,
			SourceEntryIDs: []string{entry.ID},
			SessionID:      e.sessionID,
			ExtractedAt:    time.Now(),
		})
	})
}

func (e *FactExtractor) tryExtractPattern(content string, entry *Entry, result *ExtractedFacts) bool {
	return e.extractWithPatterns(content, e.patternPatterns, func(match string) {
		result.Patterns = append(result.Patterns, &FactPattern{
			ID:             uuid.New().String(),
			Category:       "general",
			Name:           strings.TrimSpace(match),
			Pattern:        content,
			UsageCount:     1,
			SourceEntryIDs: []string{entry.ID},
			SessionID:      e.sessionID,
			ExtractedAt:    time.Now(),
		})
	})
}

func (e *FactExtractor) extractWithPatterns(content string, patterns []string, onMatch func(string)) bool {
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(content); len(matches) > 1 {
			onMatch(matches[1])
			return true
		}
	}
	return false
}

// ExtractFileChanges extracts file change facts from entries with file metadata
func (e *FactExtractor) ExtractFileChanges(entries []*Entry) []*FactFileChange {
	var changes []*FactFileChange

	for _, entry := range entries {
		if entry.Metadata == nil {
			continue
		}

		path, hasPath := entry.Metadata["path"].(string)
		if !hasPath {
			continue
		}

		changeType := FileChangeTypeModified
		if ct, ok := entry.Metadata["change_type"].(string); ok {
			changeType = FileChangeType(ct)
		}

		var lineStart, lineEnd int
		if ls, ok := entry.Metadata["line_start"].(float64); ok {
			lineStart = int(ls)
		}
		if le, ok := entry.Metadata["line_end"].(float64); ok {
			lineEnd = int(le)
		}

		changes = append(changes, &FactFileChange{
			ID:             uuid.New().String(),
			Path:           path,
			ChangeType:     changeType,
			Description:    entry.Content,
			LineStart:      lineStart,
			LineEnd:        lineEnd,
			SourceEntryIDs: []string{entry.ID},
			SessionID:      e.sessionID,
			ExtractedAt:    time.Now(),
		})
	}

	return changes
}

// Helper functions

func firstSentence(text string) string {
	// Find the first period, question mark, or exclamation point
	for i, r := range text {
		if r == '.' || r == '?' || r == '!' {
			return strings.TrimSpace(text[:i+1])
		}
	}
	// If no sentence ending, take first 100 chars
	if len(text) > 100 {
		return strings.TrimSpace(text[:100]) + "..."
	}
	return strings.TrimSpace(text)
}

func extractAlternatives(content string) []string {
	var alternatives []string

	// Look for patterns like "instead of X", "over Y and Z", etc.
	patterns := []string{
		`(?i)(?:over|instead of)\s+([^,.]+(?:,\s*[^,.]+)*)`,
		`(?i)alternatives?:\s*([^.]+)`,
		`(?i)(?:also considered|other options):\s*([^.]+)`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(content); len(matches) > 1 {
			// Split by comma or "and"
			parts := regexp.MustCompile(`\s*(?:,|and)\s*`).Split(matches[1], -1)
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if part != "" {
					alternatives = append(alternatives, part)
				}
			}
		}
	}

	return alternatives
}

func extractContext(content string) string {
	// Look for context indicators
	patterns := []string{
		`(?i)(?:because|since|as)\s+([^.]+)`,
		`(?i)(?:for|to)\s+([^.]+)`,
		`(?i)(?:when|while)\s+([^.]+)`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(content); len(matches) > 1 {
			return strings.TrimSpace(matches[1])
		}
	}

	return ""
}

// MergeFacts combines facts from multiple extractions, deduplicating where possible
func MergeFacts(factSets ...*ExtractedFacts) *ExtractedFacts {
	result := newMergedFacts()
	tracker := newFactMergeTracker()

	for _, facts := range factSets {
		if facts == nil {
			continue
		}
		tracker.mergeDecisions(result, facts)
		tracker.mergePatterns(result, facts)
		tracker.mergeFailures(result, facts)
		tracker.mergeFileChanges(result, facts)
	}

	return result
}

type factMergeTracker struct {
	decisions   map[string]bool
	patterns    map[string]bool
	failures    map[string]bool
	fileChanges map[string]bool
}

func newMergedFacts() *ExtractedFacts {
	return &ExtractedFacts{
		Decisions:   make([]*FactDecision, 0),
		Patterns:    make([]*FactPattern, 0),
		Failures:    make([]*FactFailure, 0),
		FileChanges: make([]*FactFileChange, 0),
	}
}

func newFactMergeTracker() *factMergeTracker {
	return &factMergeTracker{
		decisions:   make(map[string]bool),
		patterns:    make(map[string]bool),
		failures:    make(map[string]bool),
		fileChanges: make(map[string]bool),
	}
}

func (t *factMergeTracker) mergeDecisions(result *ExtractedFacts, facts *ExtractedFacts) {
	for _, decision := range facts.Decisions {
		if t.decisions[decision.Choice] {
			continue
		}
		t.decisions[decision.Choice] = true
		result.Decisions = append(result.Decisions, decision)
	}
}

func (t *factMergeTracker) mergePatterns(result *ExtractedFacts, facts *ExtractedFacts) {
	for _, pattern := range facts.Patterns {
		key := pattern.Category + ":" + pattern.Name
		if t.patterns[key] {
			continue
		}
		t.patterns[key] = true
		result.Patterns = append(result.Patterns, pattern)
	}
}

func (t *factMergeTracker) mergeFailures(result *ExtractedFacts, facts *ExtractedFacts) {
	for _, failure := range facts.Failures {
		if t.failures[failure.Approach] {
			continue
		}
		t.failures[failure.Approach] = true
		result.Failures = append(result.Failures, failure)
	}
}

func (t *factMergeTracker) mergeFileChanges(result *ExtractedFacts, facts *ExtractedFacts) {
	for _, change := range facts.FileChanges {
		key := change.Path + ":" + string(change.ChangeType)
		if t.fileChanges[key] {
			continue
		}
		t.fileChanges[key] = true
		result.FileChanges = append(result.FileChanges, change)
	}
}
