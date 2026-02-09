// Package search provides the command palette and fuzzy finder overlay for
// the Sylk TUI. It delegates to pluggable providers for file, agent, command,
// session, and knowledge results.
package search

import (
	"strings"
	"unicode"
)

// ---------------------------------------------------------------------------
// Scoring constants (table-driven, no magic numbers)
// ---------------------------------------------------------------------------

// bonusTable defines the scoring bonuses applied during fuzzy matching.
// Each bonus is derived from its semantic purpose rather than from an
// arbitrary number.
var bonusTable = struct {
	// BaseMatch is the minimum score for any match.
	BaseMatch int
	// SubstringPosition rewards matches that start early in the candidate.
	// Value: max bonus diminishes linearly with position.
	SubstringPositionMax int
	// Consecutive rewards adjacent query characters matched in sequence.
	Consecutive int
	// WordBoundary rewards a match that starts at a word boundary
	// (e.g., after a separator or at a case transition).
	WordBoundary int
	// ExactPrefix rewards when the candidate starts with the query.
	ExactPrefix int
}{
	BaseMatch:            1,
	SubstringPositionMax: 10,
	Consecutive:          3,
	WordBoundary:         5,
	ExactPrefix:          8,
}

// noMatch is returned when the query does not match the candidate at all.
const noMatch = -1

// Score computes a fuzzy match score for query against candidate.
// Returns noMatch (-1) when no match is found. Higher scores are better.
// Matching is case-insensitive.
func Score(query, candidate string) int {
	if query == "" {
		return bonusTable.BaseMatch
	}
	if candidate == "" {
		return noMatch
	}

	lowerQuery := strings.ToLower(query)
	lowerCandidate := strings.ToLower(candidate)

	score, ok := computeScore(lowerQuery, lowerCandidate, candidate)
	if !ok {
		return noMatch
	}
	return score
}

// computeScore walks the candidate rune-by-rune, greedily matching query
// characters and accumulating bonuses.
func computeScore(lowerQuery, lowerCandidate, originalCandidate string) (int, bool) {
	queryRunes := []rune(lowerQuery)
	candidateRunes := []rune(lowerCandidate)
	origRunes := []rune(originalCandidate)

	score := bonusTable.BaseMatch
	qi := 0
	consecutive := 0
	firstMatchPos := -1

	for ci := range candidateRunes {
		if qi >= len(queryRunes) {
			break
		}
		if candidateRunes[ci] != queryRunes[qi] {
			consecutive = 0
			continue
		}

		// Record first match position for substring position bonus.
		if firstMatchPos < 0 {
			firstMatchPos = ci
		}

		// Consecutive character bonus.
		if consecutive > 0 {
			score += bonusTable.Consecutive
		}
		consecutive++

		// Word boundary bonus: match at start of a word segment.
		if isWordBoundary(origRunes, ci) {
			score += bonusTable.WordBoundary
		}

		qi++
	}

	// All query characters must be matched.
	if qi < len(queryRunes) {
		return 0, false
	}

	// Substring position bonus: earlier matches score higher.
	score += substringPositionBonus(firstMatchPos, len(candidateRunes))

	// Exact prefix bonus.
	if strings.HasPrefix(lowerCandidate, lowerQuery) {
		score += bonusTable.ExactPrefix
	}

	return score, true
}

// substringPositionBonus returns a bonus inversely proportional to the first
// match position within the candidate. A match at position 0 gets the maximum
// bonus; a match near the end gets zero.
func substringPositionBonus(firstMatchPos, candidateLen int) int {
	if candidateLen <= 1 || firstMatchPos < 0 {
		return bonusTable.SubstringPositionMax
	}
	// Linear decay from max to 0 based on position fraction.
	fraction := 1.0 - float64(firstMatchPos)/float64(candidateLen)
	bonus := int(fraction * float64(bonusTable.SubstringPositionMax))
	if bonus < 0 {
		return 0
	}
	return bonus
}

// isWordBoundary reports whether index i in runes is at a word boundary:
// the first character, after a separator (space, slash, dot, underscore,
// hyphen), or at a lower-to-upper case transition.
func isWordBoundary(runes []rune, i int) bool {
	if i == 0 {
		return true
	}
	prev := runes[i-1]
	curr := runes[i]

	// After a separator character.
	if isSeparator(prev) {
		return true
	}
	// CamelCase boundary: lowercase followed by uppercase.
	if unicode.IsLower(prev) && unicode.IsUpper(curr) {
		return true
	}
	return false
}

// separatorSet defines characters that act as word separators for boundary
// detection. Derived from common path and identifier separators.
var separatorSet = [256]bool{
	' ':  true,
	'/':  true,
	'\\': true,
	'.':  true,
	'_':  true,
	'-':  true,
	':':  true,
}

// isSeparator reports whether r is a word separator.
func isSeparator(r rune) bool {
	if r < 256 {
		return separatorSet[r]
	}
	return false
}
