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

	// Try to match decision patterns
	for _, pattern := range e.decisionPatterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(content); len(matches) > 1 {
			result.Decisions = append(result.Decisions, &FactDecision{
				ID:             uuid.New().String(),
				Choice:         strings.TrimSpace(matches[1]),
				Rationale:      content,
				Confidence:     0.7, // Lower confidence for pattern-matched extraction
				SourceEntryIDs: []string{entry.ID},
				SessionID:      e.sessionID,
				ExtractedAt:    time.Now(),
			})
			return
		}
	}

	// Try to match failure patterns
	for _, pattern := range e.failurePatterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(content); len(matches) > 1 {
			result.Failures = append(result.Failures, &FactFailure{
				ID:             uuid.New().String(),
				Approach:       strings.TrimSpace(matches[1]),
				Reason:         content,
				SourceEntryIDs: []string{entry.ID},
				SessionID:      e.sessionID,
				ExtractedAt:    time.Now(),
			})
			return
		}
	}

	// Try to match pattern patterns
	for _, pattern := range e.patternPatterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(content); len(matches) > 1 {
			result.Patterns = append(result.Patterns, &FactPattern{
				ID:             uuid.New().String(),
				Category:       "general",
				Name:           strings.TrimSpace(matches[1]),
				Pattern:        content,
				UsageCount:     1,
				SourceEntryIDs: []string{entry.ID},
				SessionID:      e.sessionID,
				ExtractedAt:    time.Now(),
			})
			return
		}
	}
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
	result := &ExtractedFacts{
		Decisions:   make([]*FactDecision, 0),
		Patterns:    make([]*FactPattern, 0),
		Failures:    make([]*FactFailure, 0),
		FileChanges: make([]*FactFileChange, 0),
	}

	seenDecisions := make(map[string]bool)
	seenPatterns := make(map[string]bool)
	seenFailures := make(map[string]bool)
	seenFileChanges := make(map[string]bool)

	for _, facts := range factSets {
		if facts == nil {
			continue
		}

		for _, d := range facts.Decisions {
			key := d.Choice
			if !seenDecisions[key] {
				seenDecisions[key] = true
				result.Decisions = append(result.Decisions, d)
			}
		}

		for _, p := range facts.Patterns {
			key := p.Category + ":" + p.Name
			if !seenPatterns[key] {
				seenPatterns[key] = true
				result.Patterns = append(result.Patterns, p)
			}
		}

		for _, f := range facts.Failures {
			key := f.Approach
			if !seenFailures[key] {
				seenFailures[key] = true
				result.Failures = append(result.Failures, f)
			}
		}

		for _, fc := range facts.FileChanges {
			key := fc.Path + ":" + string(fc.ChangeType)
			if !seenFileChanges[key] {
				seenFileChanges[key] = true
				result.FileChanges = append(result.FileChanges, fc)
			}
		}
	}

	return result
}
