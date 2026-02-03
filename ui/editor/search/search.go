// Package search implements incremental search, match highlighting, and
// the :substitute command for the editor.
package search

import (
	"regexp"
	"sync"
)

// SearchDirection indicates whether a search scans forward or backward.
type SearchDirection int

const (
	Forward  SearchDirection = iota
	Backward
)

// SearchState holds the current search pattern and its compiled form.
type SearchState struct {
	mu        sync.RWMutex
	pattern   string
	compiled  *regexp.Regexp
	direction SearchDirection
}

// NewSearchState creates an empty search state.
func NewSearchState() *SearchState {
	return &SearchState{}
}

// SetPattern compiles the pattern and stores it. Returns an error if the
// pattern is not a valid regular expression.
func (s *SearchState) SetPattern(pattern string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	s.pattern = pattern
	s.compiled = re
	return nil
}

// Pattern returns the current search pattern string.
func (s *SearchState) Pattern() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pattern
}

// Direction returns the current search direction.
func (s *SearchState) Direction() SearchDirection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.direction
}

// SetDirection sets the search direction.
func (s *SearchState) SetDirection(d SearchDirection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.direction = d
}

// Search finds the first match in content starting from a given rune offset,
// in the specified direction. Returns the match start/end rune indices.
func (s *SearchState) Search(content []rune, from int, forward bool) (start, end int, found bool) {
	s.mu.RLock()
	re := s.compiled
	s.mu.RUnlock()
	if re == nil {
		return 0, 0, false
	}
	if forward {
		return searchForward(re, content, from)
	}
	return searchBackward(re, content, from)
}

// NextMatch finds the next match after the given position in the forward
// direction. Wraps around to the beginning of the content if necessary.
func (s *SearchState) NextMatch(content []rune, from int) (start, end int, found bool) {
	s.mu.RLock()
	re := s.compiled
	s.mu.RUnlock()
	if re == nil {
		return 0, 0, false
	}
	start, end, found = searchForward(re, content, from+1)
	if found {
		return start, end, true
	}
	// Wrap around from the beginning.
	return searchForward(re, content, 0)
}

// PrevMatch finds the previous match before the given position in the
// backward direction. Wraps around to the end of the content if necessary.
func (s *SearchState) PrevMatch(content []rune, from int) (start, end int, found bool) {
	s.mu.RLock()
	re := s.compiled
	s.mu.RUnlock()
	if re == nil {
		return 0, 0, false
	}
	start, end, found = searchBackward(re, content, from)
	if found {
		return start, end, true
	}
	// Wrap around from the end.
	return searchBackward(re, content, len(content))
}

// HasPattern reports whether a search pattern has been set.
func (s *SearchState) HasPattern() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.compiled != nil
}

// ---------------------------------------------------------------------------
// Internal search helpers
// ---------------------------------------------------------------------------

// searchForward finds the first match at or after fromRune in the rune slice.
func searchForward(re *regexp.Regexp, content []rune, fromRune int) (start, end int, found bool) {
	if fromRune < 0 {
		fromRune = 0
	}
	if fromRune >= len(content) {
		return 0, 0, false
	}
	text := string(content[fromRune:])
	loc := re.FindStringIndex(text)
	if loc == nil {
		return 0, 0, false
	}
	// Convert byte offsets back to rune offsets.
	matchRunes := []rune(text[:loc[0]])
	matchEndRunes := []rune(text[:loc[1]])
	return fromRune + len(matchRunes), fromRune + len(matchEndRunes), true
}

// searchBackward finds the last match before beforeRune in the rune slice.
func searchBackward(re *regexp.Regexp, content []rune, beforeRune int) (start, end int, found bool) {
	if beforeRune <= 0 {
		return 0, 0, false
	}
	upperBound := min(beforeRune, len(content))
	text := string(content[:upperBound])
	matches := re.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return 0, 0, false
	}
	last := matches[len(matches)-1]
	matchRunes := []rune(text[:last[0]])
	matchEndRunes := []rune(text[:last[1]])
	return len(matchRunes), len(matchEndRunes), true
}
