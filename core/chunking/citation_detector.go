package chunking

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// =============================================================================
// CK.4.3 Citation Detection for Feedback
// =============================================================================

// DetectedCitation represents a citation found in a response that maps back to a source chunk.
type DetectedCitation struct {
	// ChunkID is the ID of the chunk that was cited.
	ChunkID string `json:"chunk_id"`

	// MatchedText is the text that was matched as a citation.
	MatchedText string `json:"matched_text"`

	// Pattern is the name of the pattern that matched.
	Pattern string `json:"pattern"`

	// StartPosition is the byte offset where the citation starts in the response.
	StartPosition int `json:"start_position"`

	// EndPosition is the byte offset where the citation ends in the response.
	EndPosition int `json:"end_position"`

	// Confidence is a score indicating how confident we are this is a valid citation.
	// Values range from 0 to 1, with higher values indicating more confidence.
	Confidence float64 `json:"confidence"`

	// IsExplicit indicates if this was an explicit citation (e.g., [1], [source])
	// vs. an implicit citation (content overlap).
	IsExplicit bool `json:"is_explicit"`
}

// CitationPattern defines a pattern for detecting citations in text.
type CitationPattern struct {
	// Name identifies this pattern.
	Name string `json:"name"`

	// Regex is the compiled regular expression for matching.
	Regex *regexp.Regexp `json:"-"`

	// Pattern is the string form of the regex (for serialization).
	Pattern string `json:"pattern"`

	// Confidence is the base confidence score for matches from this pattern.
	Confidence float64 `json:"confidence"`

	// IsExplicit indicates if this pattern matches explicit citations.
	IsExplicit bool `json:"is_explicit"`

	// ChunkIDGroup is the capture group index that contains the chunk ID.
	// Use 0 for patterns that don't capture the ID directly.
	ChunkIDGroup int `json:"chunk_id_group"`
}

// DefaultCitationPatterns returns a set of common citation patterns.
func DefaultCitationPatterns() []CitationPattern {
	return []CitationPattern{
		// Bracketed number citations: [1], [2], etc.
		{
			Name:         "bracketed_number",
			Pattern:      `\[(\d+)\]`,
			Confidence:   0.9,
			IsExplicit:   true,
			ChunkIDGroup: 1,
		},
		// Parenthetical number citations: (1), (2), etc.
		{
			Name:         "parenthetical_number",
			Pattern:      `\((\d+)\)`,
			Confidence:   0.7,
			IsExplicit:   true,
			ChunkIDGroup: 1,
		},
		// Source reference: [source], [ref], etc.
		{
			Name:         "source_keyword",
			Pattern:      `\[(source|ref|reference|src)\]`,
			Confidence:   0.85,
			IsExplicit:   true,
			ChunkIDGroup: 0,
		},
		// According to source: "according to ...", "from the source ...", etc.
		{
			Name:         "according_to",
			Pattern:      `(?i)(according to|from|per|based on)\s+(the\s+)?(source|document|text|passage|excerpt)`,
			Confidence:   0.6,
			IsExplicit:   false,
			ChunkIDGroup: 0,
		},
		// Quote markers: "..." or '...'
		{
			Name:         "quoted_text",
			Pattern:      `["']([^"']{10,100})["']`,
			Confidence:   0.5,
			IsExplicit:   false,
			ChunkIDGroup: 1,
		},
		// Superscript-style citations: ^1, ^2, etc.
		{
			Name:         "superscript_number",
			Pattern:      `\^(\d+)`,
			Confidence:   0.85,
			IsExplicit:   true,
			ChunkIDGroup: 1,
		},
		// Chunk ID reference: chunk_xyz, chunk-123, etc.
		{
			Name:         "chunk_id_reference",
			Pattern:      `(?i)chunk[_-]([a-z0-9_-]+)`,
			Confidence:   0.95,
			IsExplicit:   true,
			ChunkIDGroup: 1,
		},
		// Footnote style: [^1], [^note], etc.
		{
			Name:         "footnote",
			Pattern:      `\[\^(\w+)\]`,
			Confidence:   0.9,
			IsExplicit:   true,
			ChunkIDGroup: 1,
		},
	}
}

// CitationDetector detects citations in responses and maps them to source chunks.
type CitationDetector struct {
	mu sync.RWMutex

	// patterns holds the citation patterns to use for detection.
	patterns []CitationPattern

	// chunkContent maps chunk IDs to their content for overlap detection.
	chunkContent map[string]string

	// chunkIndex maps chunk indices (1, 2, 3...) to chunk IDs for numeric citations.
	chunkIndex map[int]string

	// minOverlapLength is the minimum overlap length for implicit citations.
	minOverlapLength int

	// minOverlapConfidence is the confidence for overlap-based detection.
	minOverlapConfidence float64
}

// CitationDetectorConfig holds configuration for CitationDetector.
type CitationDetectorConfig struct {
	// Patterns are the citation patterns to use (optional, defaults to DefaultCitationPatterns).
	Patterns []CitationPattern

	// MinOverlapLength is the minimum character overlap to detect implicit citations (default: 20).
	MinOverlapLength int

	// MinOverlapConfidence is the confidence for overlap-based detection (default: 0.4).
	MinOverlapConfidence float64
}

// NewCitationDetector creates a new CitationDetector with the given configuration.
func NewCitationDetector(config CitationDetectorConfig) (*CitationDetector, error) {
	patterns := config.Patterns
	if len(patterns) == 0 {
		patterns = DefaultCitationPatterns()
	}

	// Compile regex patterns
	compiledPatterns := make([]CitationPattern, 0, len(patterns))
	for _, p := range patterns {
		compiled, err := regexp.Compile(p.Pattern)
		if err != nil {
			return nil, fmt.Errorf("failed to compile pattern %q: %w", p.Name, err)
		}
		p.Regex = compiled
		compiledPatterns = append(compiledPatterns, p)
	}

	minOverlapLength := config.MinOverlapLength
	if minOverlapLength <= 0 {
		minOverlapLength = 20
	}

	minOverlapConfidence := config.MinOverlapConfidence
	if minOverlapConfidence <= 0 {
		minOverlapConfidence = 0.4
	}

	return &CitationDetector{
		patterns:             compiledPatterns,
		chunkContent:         make(map[string]string),
		chunkIndex:           make(map[int]string),
		minOverlapLength:     minOverlapLength,
		minOverlapConfidence: minOverlapConfidence,
	}, nil
}

// RegisterChunks registers chunks for citation detection.
// The chunks are indexed in order (1, 2, 3...) for numeric citation matching.
func (cd *CitationDetector) RegisterChunks(chunks []Chunk) {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	for i, chunk := range chunks {
		cd.chunkContent[chunk.ID] = chunk.Content
		cd.chunkIndex[i+1] = chunk.ID // 1-indexed for human-readable citations
	}
}

// RegisterChunk registers a single chunk with an explicit index.
func (cd *CitationDetector) RegisterChunk(chunk Chunk, index int) {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	cd.chunkContent[chunk.ID] = chunk.Content
	if index > 0 {
		cd.chunkIndex[index] = chunk.ID
	}
}

// RegisterChunkByID registers chunk content directly by ID.
func (cd *CitationDetector) RegisterChunkByID(chunkID string, content string, index int) {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	cd.chunkContent[chunkID] = content
	if index > 0 {
		cd.chunkIndex[index] = chunkID
	}
}

// DetectCitations detects citations in a response and maps them to source chunks.
// It returns all detected citations, both explicit and implicit.
func (cd *CitationDetector) DetectCitations(response string, chunkIDs []string) []DetectedCitation {
	cd.mu.RLock()
	defer cd.mu.RUnlock()

	var citations []DetectedCitation

	// Create a set of valid chunk IDs for quick lookup
	validChunks := make(map[string]bool, len(chunkIDs))
	for _, id := range chunkIDs {
		validChunks[id] = true
	}

	// Detect explicit citations using patterns
	for _, pattern := range cd.patterns {
		matches := pattern.Regex.FindAllStringSubmatchIndex(response, -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}

			citation := cd.processCitationMatch(response, match, pattern, validChunks)
			if citation != nil {
				citations = append(citations, *citation)
			}
		}
	}

	// Detect implicit citations through content overlap
	overlapCitations := cd.detectOverlapCitations(response, chunkIDs, validChunks)
	citations = append(citations, overlapCitations...)

	// Deduplicate and sort by position
	citations = cd.deduplicateCitations(citations)
	sort.Slice(citations, func(i, j int) bool {
		return citations[i].StartPosition < citations[j].StartPosition
	})

	return citations
}

// processCitationMatch processes a regex match and returns a DetectedCitation if valid.
func (cd *CitationDetector) processCitationMatch(response string, match []int, pattern CitationPattern, validChunks map[string]bool) *DetectedCitation {
	startPos := match[0]
	endPos := match[1]
	matchedText := response[startPos:endPos]

	var chunkID string

	// Try to extract chunk ID from the match
	if pattern.ChunkIDGroup > 0 && len(match) > pattern.ChunkIDGroup*2+1 {
		groupStart := match[pattern.ChunkIDGroup*2]
		groupEnd := match[pattern.ChunkIDGroup*2+1]
		if groupStart >= 0 && groupEnd >= 0 {
			extractedID := response[groupStart:groupEnd]

			// Check if it's a numeric reference
			if num := parseNumericReference(extractedID); num > 0 {
				if id, exists := cd.chunkIndex[num]; exists {
					chunkID = id
				}
			} else {
				// Try direct ID match
				if validChunks[extractedID] {
					chunkID = extractedID
				} else {
					// Try finding a chunk ID that contains this text
					for id := range validChunks {
						if strings.Contains(strings.ToLower(id), strings.ToLower(extractedID)) {
							chunkID = id
							break
						}
					}
				}
			}
		}
	}

	// For quote patterns, try to find matching chunk by content
	if chunkID == "" && pattern.Name == "quoted_text" && len(match) > 2 {
		groupStart := match[2]
		groupEnd := match[3]
		if groupStart >= 0 && groupEnd >= 0 {
			quotedText := response[groupStart:groupEnd]
			chunkID = cd.findChunkByOverlap(quotedText, validChunks)
		}
	}

	// If we still don't have a chunk ID and there's only one chunk, use it
	if chunkID == "" && len(validChunks) == 1 {
		for id := range validChunks {
			chunkID = id
			break
		}
	}

	if chunkID == "" {
		return nil
	}

	return &DetectedCitation{
		ChunkID:       chunkID,
		MatchedText:   matchedText,
		Pattern:       pattern.Name,
		StartPosition: startPos,
		EndPosition:   endPos,
		Confidence:    pattern.Confidence,
		IsExplicit:    pattern.IsExplicit,
	}
}

// parseNumericReference attempts to parse a string as a numeric reference.
func parseNumericReference(s string) int {
	var num int
	_, err := fmt.Sscanf(s, "%d", &num)
	if err != nil {
		return 0
	}
	return num
}

// findChunkByOverlap finds a chunk ID whose content overlaps with the given text.
func (cd *CitationDetector) findChunkByOverlap(text string, validChunks map[string]bool) string {
	if len(text) < cd.minOverlapLength {
		return ""
	}

	normalizedText := strings.ToLower(strings.TrimSpace(text))

	var bestMatch string
	var bestOverlap int

	for id := range validChunks {
		content, exists := cd.chunkContent[id]
		if !exists {
			continue
		}

		normalizedContent := strings.ToLower(content)
		overlap := longestCommonSubstringLength(normalizedText, normalizedContent)

		if overlap >= cd.minOverlapLength && overlap > bestOverlap {
			bestOverlap = overlap
			bestMatch = id
		}
	}

	return bestMatch
}

// detectOverlapCitations detects implicit citations through content overlap.
func (cd *CitationDetector) detectOverlapCitations(response string, chunkIDs []string, validChunks map[string]bool) []DetectedCitation {
	var citations []DetectedCitation
	normalizedResponse := strings.ToLower(response)

	for _, chunkID := range chunkIDs {
		content, exists := cd.chunkContent[chunkID]
		if !exists {
			continue
		}

		// Find significant overlapping phrases
		overlaps := findSignificantOverlaps(normalizedResponse, strings.ToLower(content), cd.minOverlapLength)

		for _, overlap := range overlaps {
			// Find position in original response
			pos := strings.Index(normalizedResponse, overlap)
			if pos < 0 {
				continue
			}

			citations = append(citations, DetectedCitation{
				ChunkID:       chunkID,
				MatchedText:   response[pos : pos+len(overlap)],
				Pattern:       "content_overlap",
				StartPosition: pos,
				EndPosition:   pos + len(overlap),
				Confidence:    cd.calculateOverlapConfidence(len(overlap), len(content)),
				IsExplicit:    false,
			})
		}
	}

	return citations
}

// calculateOverlapConfidence calculates confidence based on overlap length relative to chunk size.
func (cd *CitationDetector) calculateOverlapConfidence(overlapLength, chunkLength int) float64 {
	if chunkLength == 0 {
		return cd.minOverlapConfidence
	}

	// Base confidence increases with overlap length
	ratio := float64(overlapLength) / float64(chunkLength)

	// Scale confidence between minOverlapConfidence and 0.8
	confidence := cd.minOverlapConfidence + (0.8-cd.minOverlapConfidence)*ratio

	// Cap at 0.8 for implicit citations
	if confidence > 0.8 {
		confidence = 0.8
	}

	return confidence
}

// deduplicateCitations removes duplicate citations for the same chunk.
// Keeps the citation with highest confidence.
func (cd *CitationDetector) deduplicateCitations(citations []DetectedCitation) []DetectedCitation {
	if len(citations) == 0 {
		return citations
	}

	// Group by chunk ID and keep highest confidence
	byChunk := make(map[string]DetectedCitation)
	for _, c := range citations {
		existing, exists := byChunk[c.ChunkID]
		if !exists || c.Confidence > existing.Confidence {
			byChunk[c.ChunkID] = c
		}
	}

	result := make([]DetectedCitation, 0, len(byChunk))
	for _, c := range byChunk {
		result = append(result, c)
	}

	return result
}

// AddPattern adds a custom citation pattern to the detector.
func (cd *CitationDetector) AddPattern(pattern CitationPattern) error {
	compiled, err := regexp.Compile(pattern.Pattern)
	if err != nil {
		return fmt.Errorf("failed to compile pattern %q: %w", pattern.Name, err)
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	pattern.Regex = compiled
	cd.patterns = append(cd.patterns, pattern)
	return nil
}

// RemovePattern removes a pattern by name.
func (cd *CitationDetector) RemovePattern(name string) bool {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	for i, p := range cd.patterns {
		if p.Name == name {
			cd.patterns = append(cd.patterns[:i], cd.patterns[i+1:]...)
			return true
		}
	}
	return false
}

// GetPatterns returns a copy of the current patterns.
func (cd *CitationDetector) GetPatterns() []CitationPattern {
	cd.mu.RLock()
	defer cd.mu.RUnlock()

	result := make([]CitationPattern, len(cd.patterns))
	copy(result, cd.patterns)
	return result
}

// ClearChunks removes all registered chunks.
func (cd *CitationDetector) ClearChunks() {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	cd.chunkContent = make(map[string]string)
	cd.chunkIndex = make(map[int]string)
}

// =============================================================================
// String Overlap Utilities
// =============================================================================

// longestCommonSubstringLength finds the length of the longest common substring
// using dynamic programming. Time complexity: O(n*m), Space complexity: O(min(n,m)).
func longestCommonSubstringLength(s1, s2 string) int {
	if len(s1) == 0 || len(s2) == 0 {
		return 0
	}

	// Ensure s2 is the shorter string for space efficiency
	if len(s2) > len(s1) {
		s1, s2 = s2, s1
	}

	// Use only 2 rows for space efficiency: O(min(n,m)) space
	prev := make([]int, len(s2)+1)
	curr := make([]int, len(s2)+1)
	maxLen := 0

	for i := 1; i <= len(s1); i++ {
		for j := 1; j <= len(s2); j++ {
			if s1[i-1] == s2[j-1] {
				curr[j] = prev[j-1] + 1
				if curr[j] > maxLen {
					maxLen = curr[j]
				}
			} else {
				curr[j] = 0
			}
		}
		prev, curr = curr, prev
	}

	return maxLen
}

// findSignificantOverlaps finds phrases that appear in both strings.
// It returns up to 10 significant overlapping phrases between the two strings.
// The function uses a greedy approach to find the longest non-overlapping phrases.
func findSignificantOverlaps(s1, s2 string, minLength int) []string {
	if len(s1) < minLength || len(s2) < minLength {
		return nil
	}

	// Pre-allocate with expected capacity (max 10 overlaps)
	overlaps := make([]string, 0, 10)
	words1 := strings.Fields(s1)

	// Build phrases of increasing length and check for overlap
	for startIdx := 0; startIdx < len(words1); startIdx++ {
		for endIdx := startIdx + 1; endIdx <= len(words1); endIdx++ {
			phrase := strings.Join(words1[startIdx:endIdx], " ")

			if len(phrase) < minLength {
				continue
			}
			if len(phrase) > 200 {
				break // Don't check very long phrases
			}

			if strings.Contains(s2, phrase) {
				// Check if this phrase is not a subset of an existing overlap
				isSubset := false
				for _, existing := range overlaps {
					if strings.Contains(existing, phrase) {
						isSubset = true
						break
					}
				}

				if !isSubset {
					// Remove any existing overlaps that are subsets of this one
					filtered := overlaps[:0]
					for _, existing := range overlaps {
						if !strings.Contains(phrase, existing) {
							filtered = append(filtered, existing)
						}
					}
					overlaps = append(filtered, phrase)
				}
			}
		}

		// Limit the number of overlaps we track
		if len(overlaps) >= 10 {
			break
		}
	}

	return overlaps
}

// =============================================================================
// Citation Feedback Integration
// =============================================================================

// CitationFeedbackRecorder uses detected citations to automatically record
// retrieval feedback for the chunks that were cited.
type CitationFeedbackRecorder struct {
	detector     *CitationDetector
	feedbackHook RetrievalFeedbackHook
}

// NewCitationFeedbackRecorder creates a new CitationFeedbackRecorder.
func NewCitationFeedbackRecorder(detector *CitationDetector, feedbackHook RetrievalFeedbackHook) *CitationFeedbackRecorder {
	return &CitationFeedbackRecorder{
		detector:     detector,
		feedbackHook: feedbackHook,
	}
}

// RecordFromResponse analyzes a response and records feedback for cited chunks.
// Chunks that are cited are marked as useful, others as not useful.
func (cfr *CitationFeedbackRecorder) RecordFromResponse(response string, allChunkIDs []string, queryContext RetrievalContext) error {
	// Detect citations
	citations := cfr.detector.DetectCitations(response, allChunkIDs)

	// Build set of cited chunk IDs
	citedChunks := make(map[string]DetectedCitation, len(citations))
	for _, c := range citations {
		// Keep the highest confidence citation for each chunk
		if existing, exists := citedChunks[c.ChunkID]; !exists || c.Confidence > existing.Confidence {
			citedChunks[c.ChunkID] = c
		}
	}

	// Record feedback for all chunks
	var lastErr error
	for _, chunkID := range allChunkIDs {
		citation, wasCited := citedChunks[chunkID]
		wasUseful := wasCited && citation.Confidence > 0.3 // Only consider useful if confidence is reasonable

		ctx := queryContext
		if wasCited {
			if ctx.Metadata == nil {
				ctx.Metadata = make(map[string]string)
			}
			ctx.Metadata["citation_pattern"] = citation.Pattern
			ctx.Metadata["citation_confidence"] = fmt.Sprintf("%.2f", citation.Confidence)
			ctx.Metadata["citation_explicit"] = fmt.Sprintf("%t", citation.IsExplicit)
		}

		if err := cfr.feedbackHook.RecordRetrieval(chunkID, wasUseful, ctx); err != nil {
			lastErr = err
		}
	}

	return lastErr
}

// GetDetector returns the underlying CitationDetector.
func (cfr *CitationFeedbackRecorder) GetDetector() *CitationDetector {
	return cfr.detector
}
