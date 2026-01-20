package chunking

import (
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Tests for longestCommonSubstringLength
// =============================================================================

func TestLongestCommonSubstringLength_EmptyStrings(t *testing.T) {
	tests := []struct {
		name     string
		s1       string
		s2       string
		expected int
	}{
		{
			name:     "both empty",
			s1:       "",
			s2:       "",
			expected: 0,
		},
		{
			name:     "first empty",
			s1:       "",
			s2:       "hello",
			expected: 0,
		},
		{
			name:     "second empty",
			s1:       "hello",
			s2:       "",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := longestCommonSubstringLength(tt.s1, tt.s2)
			if result != tt.expected {
				t.Errorf("longestCommonSubstringLength(%q, %q) = %d, want %d",
					tt.s1, tt.s2, result, tt.expected)
			}
		})
	}
}

func TestLongestCommonSubstringLength_SingleCharacter(t *testing.T) {
	tests := []struct {
		name     string
		s1       string
		s2       string
		expected int
	}{
		{
			name:     "matching single char",
			s1:       "a",
			s2:       "a",
			expected: 1,
		},
		{
			name:     "non-matching single char",
			s1:       "a",
			s2:       "b",
			expected: 0,
		},
		{
			name:     "single char in longer string",
			s1:       "a",
			s2:       "hello a world",
			expected: 1,
		},
		{
			name:     "single char not in longer string",
			s1:       "z",
			s2:       "hello world",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := longestCommonSubstringLength(tt.s1, tt.s2)
			if result != tt.expected {
				t.Errorf("longestCommonSubstringLength(%q, %q) = %d, want %d",
					tt.s1, tt.s2, result, tt.expected)
			}
		})
	}
}

func TestLongestCommonSubstringLength_IdenticalStrings(t *testing.T) {
	tests := []struct {
		name     string
		s1       string
		s2       string
		expected int
	}{
		{
			name:     "identical short strings",
			s1:       "hello",
			s2:       "hello",
			expected: 5,
		},
		{
			name:     "identical longer strings",
			s1:       "the quick brown fox jumps over the lazy dog",
			s2:       "the quick brown fox jumps over the lazy dog",
			expected: 43,
		},
		{
			name:     "identical single char",
			s1:       "x",
			s2:       "x",
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := longestCommonSubstringLength(tt.s1, tt.s2)
			if result != tt.expected {
				t.Errorf("longestCommonSubstringLength(%q, %q) = %d, want %d",
					tt.s1, tt.s2, result, tt.expected)
			}
		})
	}
}

func TestLongestCommonSubstringLength_NoCommonSubstring(t *testing.T) {
	tests := []struct {
		name     string
		s1       string
		s2       string
		expected int
	}{
		{
			name:     "completely different",
			s1:       "abc",
			s2:       "xyz",
			expected: 0,
		},
		{
			name:     "different words",
			s1:       "hello",
			s2:       "HELLO", // Case sensitive - should not match
			expected: 0,
		},
		{
			name:     "numbers vs letters",
			s1:       "12345",
			s2:       "abcde",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := longestCommonSubstringLength(tt.s1, tt.s2)
			if result != tt.expected {
				t.Errorf("longestCommonSubstringLength(%q, %q) = %d, want %d",
					tt.s1, tt.s2, result, tt.expected)
			}
		})
	}
}

func TestLongestCommonSubstringLength_CommonSubstringAtStart(t *testing.T) {
	tests := []struct {
		name     string
		s1       string
		s2       string
		expected int
	}{
		{
			name:     "common prefix",
			s1:       "hello world",
			s2:       "hello there",
			expected: 6, // "hello " with space
		},
		{
			name:     "longer common prefix",
			s1:       "the quick brown fox",
			s2:       "the quick red dog",
			expected: 10, // "the quick "
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := longestCommonSubstringLength(tt.s1, tt.s2)
			if result != tt.expected {
				t.Errorf("longestCommonSubstringLength(%q, %q) = %d, want %d",
					tt.s1, tt.s2, result, tt.expected)
			}
		})
	}
}

func TestLongestCommonSubstringLength_CommonSubstringAtMiddle(t *testing.T) {
	tests := []struct {
		name     string
		s1       string
		s2       string
		expected int
	}{
		{
			name:     "common middle substring",
			s1:       "abc middle xyz",
			s2:       "def middle uvw",
			expected: 8, // " middle "
		},
		{
			name:     "common word in middle",
			s1:       "I have a cat named fluffy",
			s2:       "My dog named spot runs fast",
			expected: 7, // " named "
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := longestCommonSubstringLength(tt.s1, tt.s2)
			if result != tt.expected {
				t.Errorf("longestCommonSubstringLength(%q, %q) = %d, want %d",
					tt.s1, tt.s2, result, tt.expected)
			}
		})
	}
}

func TestLongestCommonSubstringLength_CommonSubstringAtEnd(t *testing.T) {
	tests := []struct {
		name     string
		s1       string
		s2       string
		expected int
	}{
		{
			name:     "common suffix",
			s1:       "hello world",
			s2:       "brave new world",
			expected: 6, // " world"
		},
		{
			name:     "longer common suffix",
			s1:       "the lazy dog",
			s2:       "my lazy dog",
			expected: 9, // " lazy dog"
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := longestCommonSubstringLength(tt.s1, tt.s2)
			if result != tt.expected {
				t.Errorf("longestCommonSubstringLength(%q, %q) = %d, want %d",
					tt.s1, tt.s2, result, tt.expected)
			}
		})
	}
}

func TestLongestCommonSubstringLength_Unicode(t *testing.T) {
	tests := []struct {
		name     string
		s1       string
		s2       string
		expected int
	}{
		{
			name:     "identical strings with spaces",
			s1:       "hello world",
			s2:       "hello world",
			expected: 11, // "hello world" is 11 bytes
		},
		{
			name:     "common substring with I love",
			s1:       "I love coding",
			s2:       "She says: I love music",
			expected: 7, // "I love " (7 bytes)
		},
		{
			name:     "CJK characters common",
			s1:       "\xe4\xb8\xad\xe6\x96\x87\xe6\xb5\x8b\xe8\xaf\x95", // Chinese: "Chinese test"
			s2:       "\xe4\xb8\xad\xe6\x96\x87\xe5\xad\xa6\xe4\xb9\xa0", // Chinese: "Chinese learning"
			expected: 6, // First two Chinese characters (3 bytes each)
		},
		{
			name:     "mixed unicode cafe",
			s1:       "cafe and code",
			s2:       "my cafe is nice",
			expected: 5, // "cafe " (5 bytes)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := longestCommonSubstringLength(tt.s1, tt.s2)
			if result != tt.expected {
				t.Errorf("longestCommonSubstringLength(%q, %q) = %d, want %d",
					tt.s1, tt.s2, result, tt.expected)
			}
		})
	}
}

func TestLongestCommonSubstringLength_Symmetry(t *testing.T) {
	// The result should be the same regardless of argument order
	tests := []struct {
		name string
		s1   string
		s2   string
	}{
		{
			name: "simple strings",
			s1:   "hello world",
			s2:   "world peace",
		},
		{
			name: "different lengths",
			s1:   "abc",
			s2:   "abcdefghijklmnop",
		},
		{
			name: "unicode strings",
			s1:   "hello world",
			s2:   "say hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result1 := longestCommonSubstringLength(tt.s1, tt.s2)
			result2 := longestCommonSubstringLength(tt.s2, tt.s1)
			if result1 != result2 {
				t.Errorf("asymmetric results: (%q, %q) = %d, (%q, %q) = %d",
					tt.s1, tt.s2, result1, tt.s2, tt.s1, result2)
			}
		})
	}
}

func TestLongestCommonSubstringLength_OverlappingPatterns(t *testing.T) {
	tests := []struct {
		name     string
		s1       string
		s2       string
		expected int
	}{
		{
			name:     "overlapping repeated chars",
			s1:       "aaaa",
			s2:       "aaa",
			expected: 3,
		},
		{
			name:     "one string is substring of other",
			s1:       "abcdef",
			s2:       "cde",
			expected: 3,
		},
		{
			name:     "repeated pattern",
			s1:       "abcabcabc",
			s2:       "abcabc",
			expected: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := longestCommonSubstringLength(tt.s1, tt.s2)
			if result != tt.expected {
				t.Errorf("longestCommonSubstringLength(%q, %q) = %d, want %d",
					tt.s1, tt.s2, result, tt.expected)
			}
		})
	}
}

func TestLongestCommonSubstringLength_Performance(t *testing.T) {
	// Test with larger strings to verify O(n*m) performance
	// With the old O(n^3) algorithm, this would be very slow

	// Create strings of 1000 characters each
	s1 := strings.Repeat("a", 500) + "common" + strings.Repeat("b", 494)
	s2 := strings.Repeat("c", 500) + "common" + strings.Repeat("d", 494)

	start := time.Now()
	result := longestCommonSubstringLength(s1, s2)
	elapsed := time.Since(start)

	// With O(n*m) algorithm, this should complete in well under 1 second
	// With O(n^3) algorithm on 1000 char strings, it would take much longer
	if elapsed > 2*time.Second {
		t.Errorf("performance test too slow: took %v, expected < 2s", elapsed)
	}

	if result != 6 { // "common" is 6 characters
		t.Errorf("longestCommonSubstringLength with large strings = %d, want 6", result)
	}
}

func TestLongestCommonSubstringLength_VeryLargeStrings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large string test in short mode")
	}

	// Test with 5000 character strings
	s1 := strings.Repeat("x", 2500) + "MATCH_THIS_SUBSTRING" + strings.Repeat("y", 2480)
	s2 := strings.Repeat("a", 2500) + "MATCH_THIS_SUBSTRING" + strings.Repeat("b", 2480)

	start := time.Now()
	result := longestCommonSubstringLength(s1, s2)
	elapsed := time.Since(start)

	// Should still complete quickly with O(n*m) algorithm
	if elapsed > 5*time.Second {
		t.Errorf("large string test too slow: took %v, expected < 5s", elapsed)
	}

	if result != 20 { // "MATCH_THIS_SUBSTRING" is 20 characters
		t.Errorf("longestCommonSubstringLength with very large strings = %d, want 20", result)
	}
}

func BenchmarkLongestCommonSubstringLength_Small(b *testing.B) {
	s1 := "hello world"
	s2 := "world peace"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		longestCommonSubstringLength(s1, s2)
	}
}

func BenchmarkLongestCommonSubstringLength_Medium(b *testing.B) {
	s1 := strings.Repeat("a", 100) + "common" + strings.Repeat("b", 94)
	s2 := strings.Repeat("c", 100) + "common" + strings.Repeat("d", 94)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		longestCommonSubstringLength(s1, s2)
	}
}

func BenchmarkLongestCommonSubstringLength_Large(b *testing.B) {
	s1 := strings.Repeat("a", 500) + "common" + strings.Repeat("b", 494)
	s2 := strings.Repeat("c", 500) + "common" + strings.Repeat("d", 494)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		longestCommonSubstringLength(s1, s2)
	}
}
