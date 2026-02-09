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
	// Single-pass byte→rune conversion: walk the string once, resolving
	// all match byte offsets to rune offsets in O(N) total.
	runeMap := byteToRuneOffsets(visible, byteMatches)
	result := make([]MatchRange, 0, len(byteMatches))
	for _, loc := range byteMatches {
		if len(result) >= maxHighlightMatches {
			break
		}
		result = append(result, MatchRange{
			Start: start + runeMap[loc[0]],
			End:   start + runeMap[loc[1]],
		})
	}
	return result
}

// byteToRuneOffsets walks s once and returns the rune index for each byte
// offset referenced by locs. locs must be sorted ascending (as produced
// by FindAllStringIndex). The returned map covers every distinct byte
// offset in locs.
func byteToRuneOffsets(s string, locs [][]int) map[int]int {
	needed := make(map[int]struct{}, len(locs)*2)
	for _, loc := range locs {
		needed[loc[0]] = struct{}{}
		needed[loc[1]] = struct{}{}
	}
	m := make(map[int]int, len(needed))
	runeIdx := 0
	for byteIdx := range s {
		if _, ok := needed[byteIdx]; ok {
			m[byteIdx] = runeIdx
		}
		runeIdx++
	}
	// Handle end-of-string offset (len(s) is past the last rune start).
	if _, ok := needed[len(s)]; ok {
		m[len(s)] = runeIdx
	}
	return m
}

// FindAllContent is a convenience method that searches the entire content
// without range restrictions.
func (hm *HighlightMatches) FindAllContent(re *regexp.Regexp, content []rune) []MatchRange {
	return hm.FindAll(re, content, 0, len(content))
}
