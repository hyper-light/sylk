package search

import (
	"regexp"
	"sync"
)

// maxHighlightMatches is the maximum number of matches returned by FindAll.
// Derived from a reasonable upper bound on visible matches in a terminal
// viewport (200 lines x 200 columns / minimum match length of 1).
const maxHighlightMatches = 4096

// MatchRange identifies a contiguous span of runes that matched the search
// pattern.
type MatchRange struct {
	Start int
	End   int // exclusive
}

// HighlightMatches finds all search pattern matches within a visible region
// of the content for hlsearch rendering.
type HighlightMatches struct {
	mu     sync.RWMutex
	Active bool
}

// NewHighlightMatches creates an active highlight matcher.
func NewHighlightMatches() *HighlightMatches {
	return &HighlightMatches{Active: true}
}

// SetActive enables or disables match highlighting (toggled by :nohlsearch).
func (hm *HighlightMatches) SetActive(active bool) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.Active = active
}

// IsActive reports whether match highlighting is enabled.
func (hm *HighlightMatches) IsActive() bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.Active
}

// FindAll returns all match ranges within [visibleStart, visibleEnd) of
// content. The result is bounded to maxHighlightMatches entries.
func (hm *HighlightMatches) FindAll(re *regexp.Regexp, content []rune, visibleStart, visibleEnd int) []MatchRange {
	hm.mu.RLock()
	active := hm.Active
	hm.mu.RUnlock()
	if !active || re == nil {
		return nil
	}
	return FindAllInRange(re, content, visibleStart, visibleEnd)
}

// FindAllInRange locates all non-overlapping matches within the specified
// rune range of content.
func FindAllInRange(re *regexp.Regexp, content []rune, visibleStart, visibleEnd int) []MatchRange {
	start := max(visibleStart, 0)
	end := min(visibleEnd, len(content))
	if start >= end {
		return nil
	}
	visible := string(content[start:end])
	byteMatches := re.FindAllStringIndex(visible, maxHighlightMatches)
	if len(byteMatches) == 0 {
		return nil
	}
	result := make([]MatchRange, 0, len(byteMatches))
	for _, loc := range byteMatches {
		if len(result) >= maxHighlightMatches {
			break
		}
		// Convert byte offsets to rune offsets within the visible slice.
		mStart := len([]rune(visible[:loc[0]]))
		mEnd := len([]rune(visible[:loc[1]]))
		result = append(result, MatchRange{
			Start: start + mStart,
			End:   start + mEnd,
		})
	}
	return result
}

// FindAllContent is a convenience method that searches the entire content
// without range restrictions.
func (hm *HighlightMatches) FindAllContent(re *regexp.Regexp, content []rune) []MatchRange {
	return hm.FindAll(re, content, 0, len(content))
}
