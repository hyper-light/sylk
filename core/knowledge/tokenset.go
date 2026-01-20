// Package knowledge provides entity extraction and knowledge graph functionality.
// PF.1.3: Token Set Utility for O(1) fuzzy matching pre-filtering.
// TokenSet provides efficient set-based token operations using map[string]struct{}
// for constant-time membership testing and Jaccard similarity calculation.
package knowledge

import (
	"strings"
	"unicode"
)

// =============================================================================
// TokenSet Type
// =============================================================================

// TokenSet represents a set of unique tokens for efficient O(1) operations.
// It is used for fuzzy matching pre-filtering to avoid expensive string
// comparisons when token overlap is insufficient.
type TokenSet struct {
	tokens map[string]struct{}
}

// TokenizerConfig configures how strings are tokenized into TokenSets.
type TokenizerConfig struct {
	// SplitOnWhitespace enables splitting on whitespace characters.
	SplitOnWhitespace bool

	// SplitOnPunctuation enables splitting on punctuation characters.
	SplitOnPunctuation bool

	// SplitOnCamelCase enables splitting camelCase identifiers.
	SplitOnCamelCase bool

	// SplitOnSnakeCase enables splitting snake_case and kebab-case identifiers.
	SplitOnSnakeCase bool

	// ToLowercase normalizes all tokens to lowercase.
	ToLowercase bool

	// MinTokenLength filters out tokens shorter than this length.
	MinTokenLength int

	// GenerateNGrams enables character n-gram generation.
	GenerateNGrams bool

	// NGramSize is the size of character n-grams to generate (if enabled).
	NGramSize int
}

// DefaultTokenizerConfig returns a sensible default configuration for
// general text tokenization.
func DefaultTokenizerConfig() TokenizerConfig {
	return TokenizerConfig{
		SplitOnWhitespace:  true,
		SplitOnPunctuation: true,
		SplitOnCamelCase:   false,
		SplitOnSnakeCase:   false,
		ToLowercase:        true,
		MinTokenLength:     1,
		GenerateNGrams:     false,
		NGramSize:          3,
	}
}

// CodeTokenizerConfig returns a configuration optimized for code identifiers.
func CodeTokenizerConfig() TokenizerConfig {
	return TokenizerConfig{
		SplitOnWhitespace:  true,
		SplitOnPunctuation: true,
		SplitOnCamelCase:   true,
		SplitOnSnakeCase:   true,
		ToLowercase:        true,
		MinTokenLength:     2,
		GenerateNGrams:     false,
		NGramSize:          3,
	}
}

// NGramTokenizerConfig returns a configuration that generates character n-grams.
func NGramTokenizerConfig(ngramSize int) TokenizerConfig {
	if ngramSize < 2 {
		ngramSize = 2
	}
	return TokenizerConfig{
		SplitOnWhitespace:  true,
		SplitOnPunctuation: true,
		SplitOnCamelCase:   false,
		SplitOnSnakeCase:   false,
		ToLowercase:        true,
		MinTokenLength:     1,
		GenerateNGrams:     true,
		NGramSize:          ngramSize,
	}
}

// =============================================================================
// TokenSet Constructors
// =============================================================================

// NewTokenSet creates an empty TokenSet.
func NewTokenSet() TokenSet {
	return TokenSet{
		tokens: make(map[string]struct{}),
	}
}

// NewTokenSetWithCapacity creates an empty TokenSet with the given capacity hint.
func NewTokenSetWithCapacity(capacity int) TokenSet {
	return TokenSet{
		tokens: make(map[string]struct{}, capacity),
	}
}

// FromString creates a TokenSet from a string using default tokenization.
func FromString(s string) TokenSet {
	return FromStringWithConfig(s, DefaultTokenizerConfig())
}

// FromStringWithConfig creates a TokenSet from a string using the given configuration.
func FromStringWithConfig(s string, config TokenizerConfig) TokenSet {
	if s == "" {
		return NewTokenSet()
	}

	// Estimate capacity based on average word length
	estimatedTokens := len(s) / 5
	if estimatedTokens < 4 {
		estimatedTokens = 4
	}

	ts := NewTokenSetWithCapacity(estimatedTokens)
	tokenize(s, config, &ts)
	return ts
}

// FromStrings creates a TokenSet from multiple strings.
func FromStrings(strings []string) TokenSet {
	return FromStringsWithConfig(strings, DefaultTokenizerConfig())
}

// FromStringsWithConfig creates a TokenSet from multiple strings with config.
func FromStringsWithConfig(strings []string, config TokenizerConfig) TokenSet {
	// Estimate total capacity
	totalLen := 0
	for _, s := range strings {
		totalLen += len(s)
	}
	estimatedTokens := totalLen / 5
	if estimatedTokens < 4 {
		estimatedTokens = 4
	}

	ts := NewTokenSetWithCapacity(estimatedTokens)
	for _, s := range strings {
		tokenize(s, config, &ts)
	}
	return ts
}

// FromTokens creates a TokenSet from a slice of pre-tokenized strings.
func FromTokens(tokens []string) TokenSet {
	ts := NewTokenSetWithCapacity(len(tokens))
	for _, t := range tokens {
		if t != "" {
			ts.tokens[t] = struct{}{}
		}
	}
	return ts
}

// =============================================================================
// TokenSet Methods
// =============================================================================

// Contains returns true if the token is in the set.
// This operation is O(1).
func (ts TokenSet) Contains(token string) bool {
	_, ok := ts.tokens[token]
	return ok
}

// ContainsNormalized checks if the lowercase version of the token is in the set.
func (ts TokenSet) ContainsNormalized(token string) bool {
	_, ok := ts.tokens[strings.ToLower(token)]
	return ok
}

// Add adds a token to the set.
func (ts *TokenSet) Add(token string) {
	if token != "" {
		ts.tokens[token] = struct{}{}
	}
}

// AddAll adds multiple tokens to the set.
func (ts *TokenSet) AddAll(tokens ...string) {
	for _, t := range tokens {
		if t != "" {
			ts.tokens[t] = struct{}{}
		}
	}
}

// Remove removes a token from the set.
func (ts *TokenSet) Remove(token string) {
	delete(ts.tokens, token)
}

// Size returns the number of unique tokens in the set.
func (ts TokenSet) Size() int {
	return len(ts.tokens)
}

// IsEmpty returns true if the set contains no tokens.
func (ts TokenSet) IsEmpty() bool {
	return len(ts.tokens) == 0
}

// Tokens returns all tokens as a slice.
func (ts TokenSet) Tokens() []string {
	result := make([]string, 0, len(ts.tokens))
	for t := range ts.tokens {
		result = append(result, t)
	}
	return result
}

// Clear removes all tokens from the set.
func (ts *TokenSet) Clear() {
	ts.tokens = make(map[string]struct{})
}

// Clone creates a copy of the TokenSet.
func (ts TokenSet) Clone() TokenSet {
	clone := NewTokenSetWithCapacity(len(ts.tokens))
	for t := range ts.tokens {
		clone.tokens[t] = struct{}{}
	}
	return clone
}

// =============================================================================
// Set Operations
// =============================================================================

// Overlap computes the Jaccard similarity coefficient between two TokenSets.
// Jaccard(A, B) = |A ∩ B| / |A ∪ B|
// Returns a value between 0.0 (no overlap) and 1.0 (identical sets).
// Returns 0.0 if both sets are empty.
func (ts TokenSet) Overlap(other TokenSet) float64 {
	if ts.IsEmpty() && other.IsEmpty() {
		return 0.0
	}
	if ts.IsEmpty() || other.IsEmpty() {
		return 0.0
	}

	// Count intersection
	intersectionCount := 0
	smaller, larger := ts.tokens, other.tokens
	if len(smaller) > len(larger) {
		smaller, larger = larger, smaller
	}

	for t := range smaller {
		if _, ok := larger[t]; ok {
			intersectionCount++
		}
	}

	// Union size = |A| + |B| - |A ∩ B|
	unionCount := len(ts.tokens) + len(other.tokens) - intersectionCount

	if unionCount == 0 {
		return 0.0
	}

	return float64(intersectionCount) / float64(unionCount)
}

// Intersection returns a new TokenSet containing only tokens present in both sets.
func (ts TokenSet) Intersection(other TokenSet) TokenSet {
	smaller, larger := ts.tokens, other.tokens
	if len(smaller) > len(larger) {
		smaller, larger = larger, smaller
	}

	result := NewTokenSetWithCapacity(len(smaller))
	for t := range smaller {
		if _, ok := larger[t]; ok {
			result.tokens[t] = struct{}{}
		}
	}
	return result
}

// Union returns a new TokenSet containing all tokens from both sets.
func (ts TokenSet) Union(other TokenSet) TokenSet {
	result := NewTokenSetWithCapacity(len(ts.tokens) + len(other.tokens))
	for t := range ts.tokens {
		result.tokens[t] = struct{}{}
	}
	for t := range other.tokens {
		result.tokens[t] = struct{}{}
	}
	return result
}

// Difference returns a new TokenSet containing tokens in ts but not in other.
func (ts TokenSet) Difference(other TokenSet) TokenSet {
	result := NewTokenSetWithCapacity(len(ts.tokens))
	for t := range ts.tokens {
		if _, ok := other.tokens[t]; !ok {
			result.tokens[t] = struct{}{}
		}
	}
	return result
}

// SymmetricDifference returns tokens that are in either set but not both.
func (ts TokenSet) SymmetricDifference(other TokenSet) TokenSet {
	result := NewTokenSetWithCapacity(len(ts.tokens) + len(other.tokens))
	for t := range ts.tokens {
		if _, ok := other.tokens[t]; !ok {
			result.tokens[t] = struct{}{}
		}
	}
	for t := range other.tokens {
		if _, ok := ts.tokens[t]; !ok {
			result.tokens[t] = struct{}{}
		}
	}
	return result
}

// IsSubsetOf returns true if all tokens in ts are also in other.
func (ts TokenSet) IsSubsetOf(other TokenSet) bool {
	if len(ts.tokens) > len(other.tokens) {
		return false
	}
	for t := range ts.tokens {
		if _, ok := other.tokens[t]; !ok {
			return false
		}
	}
	return true
}

// IsSupersetOf returns true if ts contains all tokens in other.
func (ts TokenSet) IsSupersetOf(other TokenSet) bool {
	return other.IsSubsetOf(ts)
}

// =============================================================================
// Similarity Metrics
// =============================================================================

// OverlapCoefficient computes the Szymkiewicz-Simpson overlap coefficient.
// Overlap(A, B) = |A ∩ B| / min(|A|, |B|)
// This is more suitable when sets have very different sizes.
func (ts TokenSet) OverlapCoefficient(other TokenSet) float64 {
	if ts.IsEmpty() || other.IsEmpty() {
		return 0.0
	}

	intersectionCount := 0
	smaller, larger := ts.tokens, other.tokens
	if len(smaller) > len(larger) {
		smaller, larger = larger, smaller
	}

	for t := range smaller {
		if _, ok := larger[t]; ok {
			intersectionCount++
		}
	}

	minSize := len(smaller)
	if minSize == 0 {
		return 0.0
	}

	return float64(intersectionCount) / float64(minSize)
}

// Dice computes the Sorensen-Dice coefficient.
// Dice(A, B) = 2|A ∩ B| / (|A| + |B|)
func (ts TokenSet) Dice(other TokenSet) float64 {
	if ts.IsEmpty() && other.IsEmpty() {
		return 0.0
	}
	if ts.IsEmpty() || other.IsEmpty() {
		return 0.0
	}

	intersectionCount := 0
	smaller, larger := ts.tokens, other.tokens
	if len(smaller) > len(larger) {
		smaller, larger = larger, smaller
	}

	for t := range smaller {
		if _, ok := larger[t]; ok {
			intersectionCount++
		}
	}

	totalSize := len(ts.tokens) + len(other.tokens)
	if totalSize == 0 {
		return 0.0
	}

	return float64(2*intersectionCount) / float64(totalSize)
}

// CommonTokenCount returns the number of tokens common to both sets.
func (ts TokenSet) CommonTokenCount(other TokenSet) int {
	smaller, larger := ts.tokens, other.tokens
	if len(smaller) > len(larger) {
		smaller, larger = larger, smaller
	}

	count := 0
	for t := range smaller {
		if _, ok := larger[t]; ok {
			count++
		}
	}
	return count
}

// =============================================================================
// Pre-filtering Support
// =============================================================================

// QuickReject determines if two TokenSets can be quickly rejected as non-matching
// based on minimum overlap threshold. Returns true if the sets definitely don't
// match (can be rejected), false if they might match (need further comparison).
//
// This is useful for pre-filtering before expensive fuzzy matching operations.
func (ts TokenSet) QuickReject(other TokenSet, minOverlapThreshold float64) bool {
	// Empty sets can't meet any positive threshold
	if ts.IsEmpty() || other.IsEmpty() {
		return minOverlapThreshold > 0
	}

	// Upper bound on Jaccard: intersection can be at most min(|A|, |B|)
	// Jaccard <= min(|A|, |B|) / max(|A|, |B|)
	minSize := len(ts.tokens)
	maxSize := len(other.tokens)
	if minSize > maxSize {
		minSize, maxSize = maxSize, minSize
	}

	maxPossibleJaccard := float64(minSize) / float64(maxSize)

	// If even the maximum possible Jaccard is below threshold, reject
	return maxPossibleJaccard < minOverlapThreshold
}

// QuickRejectByCount rejects based on minimum common token count.
// Returns true if the sets definitely have fewer than minCommon tokens in common.
func (ts TokenSet) QuickRejectByCount(other TokenSet, minCommon int) bool {
	// Upper bound on common tokens is min(|A|, |B|)
	minSize := len(ts.tokens)
	if len(other.tokens) < minSize {
		minSize = len(other.tokens)
	}
	return minSize < minCommon
}

// =============================================================================
// Tokenization Implementation
// =============================================================================

// tokenize tokenizes a string according to the config and adds tokens to the set.
func tokenize(s string, config TokenizerConfig, ts *TokenSet) {
	if s == "" {
		return
	}

	// Note: Don't apply lowercase here if camelCase splitting is enabled,
	// since camelCase detection requires uppercase letters
	originalCase := s
	if config.ToLowercase && !config.SplitOnCamelCase {
		s = strings.ToLower(s)
	}

	var words []string

	// Split into words based on configuration
	if config.SplitOnWhitespace || config.SplitOnPunctuation {
		if config.SplitOnCamelCase {
			// Use original case for camelCase detection
			words = splitOnSeparators(originalCase, config.SplitOnWhitespace, config.SplitOnPunctuation)
		} else {
			words = splitOnSeparators(s, config.SplitOnWhitespace, config.SplitOnPunctuation)
		}
	} else {
		if config.SplitOnCamelCase {
			words = []string{originalCase}
		} else {
			words = []string{s}
		}
	}

	// Further split each word if camelCase or snake_case splitting is enabled
	for _, word := range words {
		if word == "" {
			continue
		}

		var subTokens []string

		if config.SplitOnCamelCase {
			// splitCamelCase already lowercases the output
			subTokens = splitCamelCase(word)
		} else {
			subTokens = []string{word}
		}

		for _, token := range subTokens {
			if config.SplitOnSnakeCase {
				snakeTokens := splitSnakeCase(token)
				for _, st := range snakeTokens {
					addToken(ts, st, config)
				}
			} else {
				addToken(ts, token, config)
			}
		}
	}

	// Generate n-grams if enabled
	if config.GenerateNGrams && config.NGramSize > 0 {
		addNGrams(s, config.NGramSize, ts)
	}
}

// addToken adds a token to the set if it meets the minimum length requirement.
func addToken(ts *TokenSet, token string, config TokenizerConfig) {
	if len(token) >= config.MinTokenLength {
		ts.tokens[token] = struct{}{}
	}
}

// splitOnSeparators splits a string on whitespace and/or punctuation.
func splitOnSeparators(s string, whitespace, punctuation bool) []string {
	var result []string
	var current strings.Builder

	for _, r := range s {
		isSeparator := false
		if whitespace && unicode.IsSpace(r) {
			isSeparator = true
		}
		if punctuation && unicode.IsPunct(r) {
			isSeparator = true
		}

		if isSeparator {
			if current.Len() > 0 {
				result = append(result, current.String())
				current.Reset()
			}
		} else {
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 {
		result = append(result, current.String())
	}

	return result
}

// splitCamelCase splits a camelCase or PascalCase identifier into words.
func splitCamelCase(s string) []string {
	if s == "" {
		return nil
	}

	var result []string
	var current strings.Builder

	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			// Check if this is the start of a new word
			prevLower := unicode.IsLower(runes[i-1])
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])

			if prevLower || (unicode.IsUpper(runes[i-1]) && nextLower) {
				if current.Len() > 0 {
					result = append(result, current.String())
					current.Reset()
				}
			}
		}
		current.WriteRune(unicode.ToLower(r))
	}

	if current.Len() > 0 {
		result = append(result, current.String())
	}

	return result
}

// splitSnakeCase splits a snake_case or kebab-case identifier into words.
func splitSnakeCase(s string) []string {
	if s == "" {
		return nil
	}

	var result []string
	var current strings.Builder

	for _, r := range s {
		if r == '_' || r == '-' {
			if current.Len() > 0 {
				result = append(result, current.String())
				current.Reset()
			}
		} else {
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 {
		result = append(result, current.String())
	}

	return result
}

// addNGrams generates character n-grams from the input string and adds them to the set.
func addNGrams(s string, n int, ts *TokenSet) {
	if n <= 0 {
		return
	}

	// Remove spaces for n-gram generation
	s = strings.ReplaceAll(s, " ", "")
	runes := []rune(s)

	if len(runes) < n {
		return
	}

	for i := 0; i <= len(runes)-n; i++ {
		ngram := string(runes[i : i+n])
		ts.tokens[ngram] = struct{}{}
	}
}

// =============================================================================
// Utility Functions
// =============================================================================

// TokenSetFromIdentifier creates a TokenSet optimized for code identifier matching.
// Splits on camelCase, snake_case, and normalizes to lowercase.
func TokenSetFromIdentifier(identifier string) TokenSet {
	return FromStringWithConfig(identifier, CodeTokenizerConfig())
}

// ComputeTokenOverlap is a convenience function to compute Jaccard similarity
// between two strings without explicitly creating TokenSets.
func ComputeTokenOverlap(s1, s2 string) float64 {
	ts1 := FromString(s1)
	ts2 := FromString(s2)
	return ts1.Overlap(ts2)
}

// ComputeIdentifierOverlap computes Jaccard similarity between code identifiers.
func ComputeIdentifierOverlap(id1, id2 string) float64 {
	ts1 := TokenSetFromIdentifier(id1)
	ts2 := TokenSetFromIdentifier(id2)
	return ts1.Overlap(ts2)
}

// PrefilterCandidates filters a slice of candidates by minimum token overlap.
// Returns only candidates whose TokenSet has at least minOverlap Jaccard similarity
// with the query TokenSet.
func PrefilterCandidates[T any](query TokenSet, candidates []T, getTokenSet func(T) TokenSet, minOverlap float64) []T {
	if query.IsEmpty() || len(candidates) == 0 {
		return nil
	}

	result := make([]T, 0, len(candidates)/2)
	for _, candidate := range candidates {
		candidateSet := getTokenSet(candidate)
		if !query.QuickReject(candidateSet, minOverlap) {
			overlap := query.Overlap(candidateSet)
			if overlap >= minOverlap {
				result = append(result, candidate)
			}
		}
	}
	return result
}
