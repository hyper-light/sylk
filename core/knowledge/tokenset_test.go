package knowledge

import (
	"testing"
)

// =============================================================================
// TokenSet Constructor Tests
// =============================================================================

func TestNewTokenSet(t *testing.T) {
	ts := NewTokenSet()

	if ts.Size() != 0 {
		t.Errorf("expected empty set, got size %d", ts.Size())
	}

	if !ts.IsEmpty() {
		t.Error("expected IsEmpty to be true")
	}
}

func TestNewTokenSetWithCapacity(t *testing.T) {
	ts := NewTokenSetWithCapacity(100)

	if ts.Size() != 0 {
		t.Errorf("expected empty set, got size %d", ts.Size())
	}
}

func TestFromString(t *testing.T) {
	tests := []struct {
		input         string
		expectedSize  int
		shouldContain []string
	}{
		{"hello world", 2, []string{"hello", "world"}},
		{"Hello World", 2, []string{"hello", "world"}},
		{"one, two, three!", 3, []string{"one", "two", "three"}},
		{"duplicate duplicate", 1, []string{"duplicate"}},
		{"", 0, nil},
		{"   ", 0, nil},
	}

	for _, tc := range tests {
		ts := FromString(tc.input)
		if ts.Size() != tc.expectedSize {
			t.Errorf("FromString(%q): expected size %d, got %d", tc.input, tc.expectedSize, ts.Size())
		}

		for _, token := range tc.shouldContain {
			if !ts.Contains(token) {
				t.Errorf("FromString(%q): expected to contain %q", tc.input, token)
			}
		}
	}
}

func TestFromStringWithConfig(t *testing.T) {
	t.Run("with code tokenizer config", func(t *testing.T) {
		config := CodeTokenizerConfig()
		ts := FromStringWithConfig("getUserName", config)

		// Should split camelCase
		if !ts.Contains("get") {
			t.Error("expected to contain 'get'")
		}
		if !ts.Contains("user") {
			t.Error("expected to contain 'user'")
		}
		if !ts.Contains("name") {
			t.Error("expected to contain 'name'")
		}
	})

	t.Run("with snake_case splitting", func(t *testing.T) {
		config := CodeTokenizerConfig()
		ts := FromStringWithConfig("get_user_name", config)

		if !ts.Contains("get") {
			t.Error("expected to contain 'get'")
		}
		if !ts.Contains("user") {
			t.Error("expected to contain 'user'")
		}
		if !ts.Contains("name") {
			t.Error("expected to contain 'name'")
		}
	})

	t.Run("with n-gram generation", func(t *testing.T) {
		config := NGramTokenizerConfig(3)
		ts := FromStringWithConfig("hello", config)

		// Should contain 3-grams: hel, ell, llo
		if !ts.Contains("hel") {
			t.Error("expected to contain 'hel' n-gram")
		}
		if !ts.Contains("ell") {
			t.Error("expected to contain 'ell' n-gram")
		}
		if !ts.Contains("llo") {
			t.Error("expected to contain 'llo' n-gram")
		}
	})

	t.Run("with min token length", func(t *testing.T) {
		config := DefaultTokenizerConfig()
		config.MinTokenLength = 3
		ts := FromStringWithConfig("a to the code", config)

		// 'a' and 'to' should be filtered out
		if ts.Contains("a") {
			t.Error("expected 'a' to be filtered out")
		}
		if ts.Contains("to") {
			t.Error("expected 'to' to be filtered out")
		}
		if !ts.Contains("the") {
			t.Error("expected to contain 'the'")
		}
		if !ts.Contains("code") {
			t.Error("expected to contain 'code'")
		}
	})
}

func TestFromStrings(t *testing.T) {
	ts := FromStrings([]string{"hello", "world", "hello"})

	if ts.Size() != 2 {
		t.Errorf("expected size 2, got %d", ts.Size())
	}

	if !ts.Contains("hello") {
		t.Error("expected to contain 'hello'")
	}
	if !ts.Contains("world") {
		t.Error("expected to contain 'world'")
	}
}

func TestFromTokens(t *testing.T) {
	ts := FromTokens([]string{"token1", "token2", "token1"})

	if ts.Size() != 2 {
		t.Errorf("expected size 2, got %d", ts.Size())
	}
}

// =============================================================================
// TokenSet Basic Operations Tests
// =============================================================================

func TestTokenSet_Contains(t *testing.T) {
	ts := FromString("hello world")

	if !ts.Contains("hello") {
		t.Error("expected to contain 'hello'")
	}
	if !ts.Contains("world") {
		t.Error("expected to contain 'world'")
	}
	if ts.Contains("missing") {
		t.Error("expected not to contain 'missing'")
	}
}

func TestTokenSet_ContainsNormalized(t *testing.T) {
	ts := FromString("hello world") // Tokens are lowercase

	if !ts.ContainsNormalized("HELLO") {
		t.Error("expected ContainsNormalized to find 'HELLO' as 'hello'")
	}
}

func TestTokenSet_Add(t *testing.T) {
	ts := NewTokenSet()
	ts.Add("token1")
	ts.Add("token2")
	ts.Add("") // Should be ignored

	if ts.Size() != 2 {
		t.Errorf("expected size 2, got %d", ts.Size())
	}
}

func TestTokenSet_AddAll(t *testing.T) {
	ts := NewTokenSet()
	ts.AddAll("token1", "token2", "token3")

	if ts.Size() != 3 {
		t.Errorf("expected size 3, got %d", ts.Size())
	}
}

func TestTokenSet_Remove(t *testing.T) {
	ts := FromTokens([]string{"a", "b", "c"})
	ts.Remove("b")

	if ts.Size() != 2 {
		t.Errorf("expected size 2, got %d", ts.Size())
	}
	if ts.Contains("b") {
		t.Error("expected 'b' to be removed")
	}
}

func TestTokenSet_Tokens(t *testing.T) {
	ts := FromTokens([]string{"a", "b", "c"})
	tokens := ts.Tokens()

	if len(tokens) != 3 {
		t.Errorf("expected 3 tokens, got %d", len(tokens))
	}

	// Check all tokens are present (order not guaranteed)
	tokenSet := make(map[string]bool)
	for _, tok := range tokens {
		tokenSet[tok] = true
	}

	for _, expected := range []string{"a", "b", "c"} {
		if !tokenSet[expected] {
			t.Errorf("expected token %q in result", expected)
		}
	}
}

func TestTokenSet_Clear(t *testing.T) {
	ts := FromString("hello world")
	ts.Clear()

	if !ts.IsEmpty() {
		t.Error("expected empty set after clear")
	}
}

func TestTokenSet_Clone(t *testing.T) {
	ts := FromString("hello world")
	clone := ts.Clone()

	if clone.Size() != ts.Size() {
		t.Error("clone should have same size")
	}

	// Modifying clone should not affect original
	clone.Add("new")
	if ts.Contains("new") {
		t.Error("original should not be affected by clone modification")
	}
}

// =============================================================================
// Set Operations Tests
// =============================================================================

func TestTokenSet_Overlap(t *testing.T) {
	tests := []struct {
		name     string
		set1     []string
		set2     []string
		expected float64
	}{
		{
			name:     "identical sets",
			set1:     []string{"a", "b", "c"},
			set2:     []string{"a", "b", "c"},
			expected: 1.0,
		},
		{
			name:     "no overlap",
			set1:     []string{"a", "b"},
			set2:     []string{"c", "d"},
			expected: 0.0,
		},
		{
			name:     "partial overlap",
			set1:     []string{"a", "b", "c"},
			set2:     []string{"b", "c", "d"},
			expected: 0.5, // 2/4
		},
		{
			name:     "empty sets",
			set1:     []string{},
			set2:     []string{},
			expected: 0.0,
		},
		{
			name:     "one empty set",
			set1:     []string{"a", "b"},
			set2:     []string{},
			expected: 0.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts1 := FromTokens(tc.set1)
			ts2 := FromTokens(tc.set2)

			result := ts1.Overlap(ts2)
			if result != tc.expected {
				t.Errorf("expected overlap %f, got %f", tc.expected, result)
			}
		})
	}
}

func TestTokenSet_Intersection(t *testing.T) {
	ts1 := FromTokens([]string{"a", "b", "c"})
	ts2 := FromTokens([]string{"b", "c", "d"})

	result := ts1.Intersection(ts2)

	if result.Size() != 2 {
		t.Errorf("expected size 2, got %d", result.Size())
	}
	if !result.Contains("b") || !result.Contains("c") {
		t.Error("expected intersection to contain 'b' and 'c'")
	}
}

func TestTokenSet_Union(t *testing.T) {
	ts1 := FromTokens([]string{"a", "b"})
	ts2 := FromTokens([]string{"b", "c"})

	result := ts1.Union(ts2)

	if result.Size() != 3 {
		t.Errorf("expected size 3, got %d", result.Size())
	}
	for _, expected := range []string{"a", "b", "c"} {
		if !result.Contains(expected) {
			t.Errorf("expected union to contain %q", expected)
		}
	}
}

func TestTokenSet_Difference(t *testing.T) {
	ts1 := FromTokens([]string{"a", "b", "c"})
	ts2 := FromTokens([]string{"b", "c", "d"})

	result := ts1.Difference(ts2)

	if result.Size() != 1 {
		t.Errorf("expected size 1, got %d", result.Size())
	}
	if !result.Contains("a") {
		t.Error("expected difference to contain 'a'")
	}
}

func TestTokenSet_SymmetricDifference(t *testing.T) {
	ts1 := FromTokens([]string{"a", "b", "c"})
	ts2 := FromTokens([]string{"b", "c", "d"})

	result := ts1.SymmetricDifference(ts2)

	if result.Size() != 2 {
		t.Errorf("expected size 2, got %d", result.Size())
	}
	if !result.Contains("a") || !result.Contains("d") {
		t.Error("expected symmetric difference to contain 'a' and 'd'")
	}
}

func TestTokenSet_IsSubsetOf(t *testing.T) {
	ts1 := FromTokens([]string{"a", "b"})
	ts2 := FromTokens([]string{"a", "b", "c"})
	ts3 := FromTokens([]string{"a", "d"})

	if !ts1.IsSubsetOf(ts2) {
		t.Error("expected ts1 to be subset of ts2")
	}
	if ts1.IsSubsetOf(ts3) {
		t.Error("expected ts1 not to be subset of ts3")
	}
}

func TestTokenSet_IsSupersetOf(t *testing.T) {
	ts1 := FromTokens([]string{"a", "b", "c"})
	ts2 := FromTokens([]string{"a", "b"})

	if !ts1.IsSupersetOf(ts2) {
		t.Error("expected ts1 to be superset of ts2")
	}
}

// =============================================================================
// Similarity Metrics Tests
// =============================================================================

func TestTokenSet_OverlapCoefficient(t *testing.T) {
	ts1 := FromTokens([]string{"a", "b"})
	ts2 := FromTokens([]string{"a", "b", "c", "d", "e"})

	// Overlap coefficient = |intersection| / min(|A|, |B|) = 2/2 = 1.0
	result := ts1.OverlapCoefficient(ts2)
	if result != 1.0 {
		t.Errorf("expected overlap coefficient 1.0, got %f", result)
	}
}

func TestTokenSet_Dice(t *testing.T) {
	ts1 := FromTokens([]string{"a", "b", "c"})
	ts2 := FromTokens([]string{"b", "c", "d"})

	// Dice = 2*|intersection| / (|A| + |B|) = 2*2 / 6 = 0.666...
	result := ts1.Dice(ts2)
	expected := 4.0 / 6.0
	if result != expected {
		t.Errorf("expected Dice %f, got %f", expected, result)
	}
}

func TestTokenSet_CommonTokenCount(t *testing.T) {
	ts1 := FromTokens([]string{"a", "b", "c"})
	ts2 := FromTokens([]string{"b", "c", "d"})

	result := ts1.CommonTokenCount(ts2)
	if result != 2 {
		t.Errorf("expected 2 common tokens, got %d", result)
	}
}

// =============================================================================
// Pre-filtering Tests
// =============================================================================

func TestTokenSet_QuickReject(t *testing.T) {
	tests := []struct {
		name       string
		set1       []string
		set2       []string
		threshold  float64
		shouldReject bool
	}{
		{
			name:         "similar sets pass threshold",
			set1:         []string{"a", "b", "c"},
			set2:         []string{"a", "b", "c"},
			threshold:    0.5,
			shouldReject: false,
		},
		{
			name:         "dissimilar sets rejected",
			set1:         []string{"a"},
			set2:         []string{"b", "c", "d", "e"},
			threshold:    0.5,
			shouldReject: true,
		},
		{
			name:         "empty set rejected",
			set1:         []string{},
			set2:         []string{"a", "b"},
			threshold:    0.1,
			shouldReject: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts1 := FromTokens(tc.set1)
			ts2 := FromTokens(tc.set2)

			rejected := ts1.QuickReject(ts2, tc.threshold)
			if rejected != tc.shouldReject {
				t.Errorf("expected QuickReject=%v, got %v", tc.shouldReject, rejected)
			}
		})
	}
}

func TestTokenSet_QuickRejectByCount(t *testing.T) {
	ts1 := FromTokens([]string{"a", "b"})
	ts2 := FromTokens([]string{"a", "c", "d"})

	// Max common can be 2, so requiring 3 should reject
	if !ts1.QuickRejectByCount(ts2, 3) {
		t.Error("expected rejection when requiring 3 common tokens")
	}

	// Requiring 1 should not reject
	if ts1.QuickRejectByCount(ts2, 1) {
		t.Error("expected not to reject when requiring 1 common token")
	}
}

// =============================================================================
// Utility Function Tests
// =============================================================================

func TestTokenSetFromIdentifier(t *testing.T) {
	tests := []struct {
		identifier string
		expected   []string
	}{
		{"getUserName", []string{"get", "user", "name"}},
		{"get_user_name", []string{"get", "user", "name"}},
		{"HTTPClient", []string{"http", "client"}},
	}

	for _, tc := range tests {
		ts := TokenSetFromIdentifier(tc.identifier)
		for _, expected := range tc.expected {
			if !ts.Contains(expected) {
				t.Errorf("TokenSetFromIdentifier(%q): expected to contain %q", tc.identifier, expected)
			}
		}
	}
}

func TestComputeTokenOverlap(t *testing.T) {
	result := ComputeTokenOverlap("hello world", "hello there")
	if result <= 0 {
		t.Error("expected positive overlap for strings with common token 'hello'")
	}
}

func TestComputeIdentifierOverlap(t *testing.T) {
	result := ComputeIdentifierOverlap("getUserName", "getUsername")
	if result <= 0 {
		t.Error("expected positive overlap for similar identifiers")
	}
}

func TestPrefilterCandidates(t *testing.T) {
	query := FromString("hello world")

	type candidate struct {
		text string
	}

	candidates := []candidate{
		{text: "hello world example"},
		{text: "goodbye world"},
		{text: "completely different"},
		{text: "hello there friend"},
	}

	getTokenSet := func(c candidate) TokenSet {
		return FromString(c.text)
	}

	result := PrefilterCandidates(query, candidates, getTokenSet, 0.3)

	// Should filter out "completely different" which has no overlap
	if len(result) >= len(candidates) {
		t.Error("expected some candidates to be filtered out")
	}
}

// =============================================================================
// Tokenization Implementation Tests
// =============================================================================

func TestSplitCamelCase(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"getUserName", []string{"get", "user", "name"}},
		{"XMLParser", []string{"xml", "parser"}},
		{"SimpleCase", []string{"simple", "case"}},
		{"lowercase", []string{"lowercase"}},
		{"ABC", []string{"abc"}},
		{"", nil},
	}

	for _, tc := range tests {
		result := splitCamelCase(tc.input)
		if len(result) != len(tc.expected) {
			t.Errorf("splitCamelCase(%q) = %v, want %v", tc.input, result, tc.expected)
		}
	}
}

func TestSplitSnakeCase(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"get_user_name", []string{"get", "user", "name"}},
		{"kebab-case", []string{"kebab", "case"}},
		{"no_ending_", []string{"no", "ending"}},
		{"single", []string{"single"}},
		{"", nil},
	}

	for _, tc := range tests {
		result := splitSnakeCase(tc.input)
		if len(result) != len(tc.expected) {
			t.Errorf("splitSnakeCase(%q) = %v, want %v", tc.input, result, tc.expected)
		}
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkFromString(b *testing.B) {
	text := "This is a sample text for benchmarking the tokenization process"
	for i := 0; i < b.N; i++ {
		_ = FromString(text)
	}
}

func BenchmarkFromStringWithNGrams(b *testing.B) {
	text := "This is a sample text"
	config := NGramTokenizerConfig(3)
	for i := 0; i < b.N; i++ {
		_ = FromStringWithConfig(text, config)
	}
}

func BenchmarkOverlap(b *testing.B) {
	ts1 := FromString("hello world this is a test of token overlap")
	ts2 := FromString("hello there this is another test of overlap")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ts1.Overlap(ts2)
	}
}

func BenchmarkContains(b *testing.B) {
	ts := FromString("alpha beta gamma delta epsilon zeta eta theta iota kappa")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ts.Contains("epsilon")
	}
}

func BenchmarkQuickReject(b *testing.B) {
	ts1 := FromString("hello world this is a test")
	ts2 := FromString("completely different unrelated text here")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ts1.QuickReject(ts2, 0.5)
	}
}

func BenchmarkIntersection(b *testing.B) {
	ts1 := FromString("alpha beta gamma delta epsilon zeta eta")
	ts2 := FromString("delta epsilon zeta eta theta iota kappa")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ts1.Intersection(ts2)
	}
}
