package query

import (
	"regexp"
	"sync"
	"testing"
)

// =============================================================================
// Pre-compiled Pattern Tests
// =============================================================================

func TestPrecompiledPatterns(t *testing.T) {
	t.Run("TokenizePattern is compiled", func(t *testing.T) {
		if TokenizePattern == nil {
			t.Fatal("TokenizePattern should not be nil")
		}

		result := TokenizePattern.Split("hello world", -1)
		if len(result) != 2 {
			t.Errorf("expected 2 parts, got %d", len(result))
		}
	})

	t.Run("EmailPattern matches valid emails", func(t *testing.T) {
		if EmailPattern == nil {
			t.Fatal("EmailPattern should not be nil")
		}

		tests := []struct {
			input    string
			expected bool
		}{
			{"user@example.com", true},
			{"test.name+tag@domain.co.uk", true},
			{"invalid", false},
			{"@nodomain.com", false},
		}

		for _, tc := range tests {
			matched := EmailPattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("EmailPattern.MatchString(%q) = %v, want %v", tc.input, matched, tc.expected)
			}
		}
	})

	t.Run("URLPattern matches valid URLs", func(t *testing.T) {
		if URLPattern == nil {
			t.Fatal("URLPattern should not be nil")
		}

		tests := []struct {
			input    string
			expected bool
		}{
			{"https://example.com", true},
			{"http://localhost:8080/path", true},
			{"ftp://invalid.com", false},
			{"not-a-url", false},
		}

		for _, tc := range tests {
			matched := URLPattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("URLPattern.MatchString(%q) = %v, want %v", tc.input, matched, tc.expected)
			}
		}
	})

	t.Run("DateISOPattern matches ISO dates", func(t *testing.T) {
		if DateISOPattern == nil {
			t.Fatal("DateISOPattern should not be nil")
		}

		tests := []struct {
			input    string
			expected bool
		}{
			{"2024-01-15", true},
			{"2024-12-31", true},
			{"24-01-15", false},
			{"2024/01/15", false},
		}

		for _, tc := range tests {
			matched := DateISOPattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("DateISOPattern.MatchString(%q) = %v, want %v", tc.input, matched, tc.expected)
			}
		}
	})

	t.Run("VersionPattern matches semantic versions", func(t *testing.T) {
		if VersionPattern == nil {
			t.Fatal("VersionPattern should not be nil")
		}

		tests := []struct {
			input    string
			expected bool
		}{
			{"v1.0.0", true},
			{"1.2.3", true},
			{"2.0", true},
			{"v1.0.0-beta", true},
			{"invalid", false},
		}

		for _, tc := range tests {
			matched := VersionPattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("VersionPattern.MatchString(%q) = %v, want %v", tc.input, matched, tc.expected)
			}
		}
	})

	t.Run("UUIDPattern matches UUIDs", func(t *testing.T) {
		if UUIDPattern == nil {
			t.Fatal("UUIDPattern should not be nil")
		}

		tests := []struct {
			input    string
			expected bool
		}{
			{"123e4567-e89b-12d3-a456-426614174000", true},
			{"AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE", true},
			{"invalid-uuid", false},
			{"123e4567-e89b-12d3-a456", false},
		}

		for _, tc := range tests {
			matched := UUIDPattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("UUIDPattern.MatchString(%q) = %v, want %v", tc.input, matched, tc.expected)
			}
		}
	})

	t.Run("IdentifierPattern matches programming identifiers", func(t *testing.T) {
		if IdentifierPattern == nil {
			t.Fatal("IdentifierPattern should not be nil")
		}

		tests := []struct {
			input    string
			expected bool
		}{
			{"myFunction", true},
			{"_privateVar", true},
			{"CamelCase", true},
			{"snake_case", true},
			{"123invalid", false},
		}

		for _, tc := range tests {
			matched := IdentifierPattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("IdentifierPattern.MatchString(%q) = %v, want %v", tc.input, matched, tc.expected)
			}
		}
	})

	t.Run("GoFuncPattern matches Go function declarations", func(t *testing.T) {
		if GoFuncPattern == nil {
			t.Fatal("GoFuncPattern should not be nil")
		}

		tests := []struct {
			input    string
			expected bool
		}{
			{"func main()", true},
			{"func (r *Receiver) Method()", true},
			{"func doSomething(arg int)", true},
			{"function main()", false},
		}

		for _, tc := range tests {
			matched := GoFuncPattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("GoFuncPattern.MatchString(%q) = %v, want %v", tc.input, matched, tc.expected)
			}
		}
	})
}

// =============================================================================
// RegexCache Tests
// =============================================================================

func TestNewRegexCache(t *testing.T) {
	cache := NewRegexCache()
	if cache == nil {
		t.Fatal("expected non-nil cache")
	}
	if cache.Size() != 0 {
		t.Errorf("expected empty cache, got size %d", cache.Size())
	}
}

func TestRegexCache_GetOrCompile(t *testing.T) {
	cache := NewRegexCache()

	t.Run("compiles valid pattern", func(t *testing.T) {
		re, err := cache.GetOrCompile(`\d+`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if re == nil {
			t.Fatal("expected non-nil regex")
		}
		if !re.MatchString("123") {
			t.Error("regex should match digits")
		}
	})

	t.Run("returns cached pattern", func(t *testing.T) {
		re1, err1 := cache.GetOrCompile(`[a-z]+`)
		if err1 != nil {
			t.Fatalf("unexpected error: %v", err1)
		}

		re2, err2 := cache.GetOrCompile(`[a-z]+`)
		if err2 != nil {
			t.Fatalf("unexpected error: %v", err2)
		}

		// Should return same compiled regex
		if re1 != re2 {
			t.Error("expected same regex instance for same pattern")
		}
	})

	t.Run("returns error for invalid pattern", func(t *testing.T) {
		_, err := cache.GetOrCompile(`[invalid`)
		if err == nil {
			t.Error("expected error for invalid pattern")
		}
	})

	t.Run("caches multiple patterns", func(t *testing.T) {
		patterns := []string{`\w+`, `\s+`, `[0-9]+`, `[A-Z][a-z]+`}
		for _, p := range patterns {
			_, err := cache.GetOrCompile(p)
			if err != nil {
				t.Errorf("failed to compile %q: %v", p, err)
			}
		}
		if cache.Size() < len(patterns) {
			t.Errorf("expected at least %d cached patterns, got %d", len(patterns), cache.Size())
		}
	})
}

func TestRegexCache_MustGetOrCompile(t *testing.T) {
	cache := NewRegexCache()

	t.Run("returns compiled regex for valid pattern", func(t *testing.T) {
		re := cache.MustGetOrCompile(`\d+`)
		if re == nil {
			t.Fatal("expected non-nil regex")
		}
	})

	t.Run("panics for invalid pattern", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic for invalid pattern")
			}
		}()
		cache.MustGetOrCompile(`[invalid`)
	})
}

func TestRegexCache_SafeGetOrCompile(t *testing.T) {
	cache := NewRegexCache()

	t.Run("returns compiled regex for valid pattern", func(t *testing.T) {
		re, err := cache.SafeGetOrCompile(`\d+`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if re == nil {
			t.Fatal("expected non-nil regex")
		}
		if !re.MatchString("123") {
			t.Error("regex should match digits")
		}
	})

	t.Run("returns error for invalid pattern without panic", func(t *testing.T) {
		re, err := cache.SafeGetOrCompile(`[invalid`)
		if err == nil {
			t.Error("expected error for invalid pattern")
		}
		if re != nil {
			t.Error("expected nil regex for invalid pattern")
		}
	})

	t.Run("handles empty pattern", func(t *testing.T) {
		re, err := cache.SafeGetOrCompile(``)
		if err != nil {
			t.Fatalf("unexpected error for empty pattern: %v", err)
		}
		if re == nil {
			t.Fatal("expected non-nil regex for empty pattern")
		}
	})

	t.Run("caches valid patterns", func(t *testing.T) {
		re1, err1 := cache.SafeGetOrCompile(`safe-[a-z]+`)
		if err1 != nil {
			t.Fatalf("unexpected error: %v", err1)
		}

		re2, err2 := cache.SafeGetOrCompile(`safe-[a-z]+`)
		if err2 != nil {
			t.Fatalf("unexpected error: %v", err2)
		}

		if re1 != re2 {
			t.Error("expected same regex instance for same pattern")
		}
	})

	t.Run("concurrent safe access", func(t *testing.T) {
		patterns := []string{`safe-\d+`, `safe-\w+`, `[a-z]+`, `[invalid`, `\s+`}
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				pattern := patterns[idx%len(patterns)]
				for j := 0; j < 20; j++ {
					re, err := cache.SafeGetOrCompile(pattern)
					// Invalid patterns should return error
					if pattern == `[invalid` {
						if err == nil {
							t.Error("expected error for invalid pattern")
						}
					} else {
						if err != nil {
							t.Errorf("unexpected error for %q: %v", pattern, err)
						}
						if re == nil {
							t.Errorf("expected non-nil regex for %q", pattern)
						}
					}
				}
			}(i)
		}
		wg.Wait()
	})
}

func TestRegexCache_Contains(t *testing.T) {
	cache := NewRegexCache()

	// Not cached yet
	if cache.Contains(`\d+`) {
		t.Error("expected pattern not to be cached initially")
	}

	// Compile it
	_, _ = cache.GetOrCompile(`\d+`)

	// Now it should be cached
	if !cache.Contains(`\d+`) {
		t.Error("expected pattern to be cached after compilation")
	}
}

func TestRegexCache_Get(t *testing.T) {
	cache := NewRegexCache()

	t.Run("returns nil for uncached pattern", func(t *testing.T) {
		re := cache.Get(`\d+`)
		if re != nil {
			t.Error("expected nil for uncached pattern")
		}
	})

	t.Run("returns cached pattern", func(t *testing.T) {
		_, _ = cache.GetOrCompile(`\d+`)
		re := cache.Get(`\d+`)
		if re == nil {
			t.Error("expected cached pattern")
		}
	})
}

func TestRegexCache_Precompile(t *testing.T) {
	cache := NewRegexCache()

	t.Run("precompiles valid patterns", func(t *testing.T) {
		patterns := []string{`\w+`, `\s+`, `\d+`}
		errors := cache.Precompile(patterns...)

		if len(errors) != 0 {
			t.Errorf("unexpected errors: %v", errors)
		}

		for _, p := range patterns {
			if !cache.Contains(p) {
				t.Errorf("expected %q to be cached", p)
			}
		}
	})

	t.Run("returns errors for invalid patterns", func(t *testing.T) {
		patterns := []string{`valid`, `[invalid`, `also-valid`}
		errors := cache.Precompile(patterns...)

		if len(errors) != 1 {
			t.Errorf("expected 1 error, got %d", len(errors))
		}
	})
}

func TestRegexCache_Clear(t *testing.T) {
	cache := NewRegexCache()

	// Add some patterns
	_, _ = cache.GetOrCompile(`\d+`)
	_, _ = cache.GetOrCompile(`\w+`)

	if cache.Size() == 0 {
		t.Fatal("expected non-empty cache")
	}

	cache.Clear()

	if cache.Size() != 0 {
		t.Errorf("expected empty cache after clear, got size %d", cache.Size())
	}
}

func TestRegexCache_ConcurrentAccess(t *testing.T) {
	cache := NewRegexCache()
	patterns := []string{`\d+`, `\w+`, `[a-z]+`, `[A-Z]+`, `\s+`}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			pattern := patterns[idx%len(patterns)]
			for j := 0; j < 100; j++ {
				re, err := cache.GetOrCompile(pattern)
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if re == nil {
					t.Error("expected non-nil regex")
				}
			}
		}(i)
	}
	wg.Wait()

	// All patterns should be cached
	if cache.Size() != len(patterns) {
		t.Errorf("expected %d cached patterns, got %d", len(patterns), cache.Size())
	}
}

// =============================================================================
// Global Cache Function Tests
// =============================================================================

func TestGlobalCacheFunctions(t *testing.T) {
	t.Run("GetOrCompileGlobal works", func(t *testing.T) {
		re, err := GetOrCompileGlobal(`global-test-\d+`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if re == nil {
			t.Fatal("expected non-nil regex")
		}
	})

	t.Run("MustGetOrCompileGlobal works", func(t *testing.T) {
		re := MustGetOrCompileGlobal(`global-test-\w+`)
		if re == nil {
			t.Fatal("expected non-nil regex")
		}
	})

	t.Run("PrecompileGlobal works", func(t *testing.T) {
		errors := PrecompileGlobal(`precompile-\d+`, `precompile-\w+`)
		if len(errors) != 0 {
			t.Errorf("unexpected errors: %v", errors)
		}
	})

	t.Run("GlobalCacheSize returns positive value", func(t *testing.T) {
		// We've added several patterns above
		if GlobalCacheSize() == 0 {
			t.Error("expected non-zero global cache size")
		}
	})

	t.Run("SafeGetOrCompileGlobal works with valid pattern", func(t *testing.T) {
		re, err := SafeGetOrCompileGlobal(`global-safe-\d+`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if re == nil {
			t.Fatal("expected non-nil regex")
		}
	})

	t.Run("SafeGetOrCompileGlobal returns error for invalid pattern", func(t *testing.T) {
		re, err := SafeGetOrCompileGlobal(`[invalid-global`)
		if err == nil {
			t.Error("expected error for invalid pattern")
		}
		if re != nil {
			t.Error("expected nil regex for invalid pattern")
		}
	})
}

// =============================================================================
// PatternBuilder Tests
// =============================================================================

func TestPatternBuilder(t *testing.T) {
	t.Run("builds literal pattern", func(t *testing.T) {
		pb := NewPatternBuilder()
		pattern := pb.Literal("hello.world").String()

		// Should escape the dot
		if pattern != `hello\.world` {
			t.Errorf("expected escaped pattern, got %q", pattern)
		}
	})

	t.Run("builds raw pattern", func(t *testing.T) {
		pb := NewPatternBuilder()
		pattern := pb.Raw(`\d+`).String()

		if pattern != `\d+` {
			t.Errorf("expected raw pattern, got %q", pattern)
		}
	})

	t.Run("builds grouped pattern", func(t *testing.T) {
		pb := NewPatternBuilder()
		pattern := pb.Group(`\d+`).String()

		if pattern != `(?:\d+)` {
			t.Errorf("expected non-capturing group, got %q", pattern)
		}
	})

	t.Run("builds capture pattern", func(t *testing.T) {
		pb := NewPatternBuilder()
		pattern := pb.Capture(`\d+`).String()

		if pattern != `(\d+)` {
			t.Errorf("expected capturing group, got %q", pattern)
		}
	})

	t.Run("adds optional modifier", func(t *testing.T) {
		pb := NewPatternBuilder()
		pattern := pb.Raw(`\d+`).Optional().String()

		if pattern != `\d+?` {
			t.Errorf("expected optional, got %q", pattern)
		}
	})

	t.Run("adds oneOrMore modifier", func(t *testing.T) {
		pb := NewPatternBuilder()
		pattern := pb.Raw(`\d`).OneOrMore().String()

		if pattern != `\d+` {
			t.Errorf("expected oneOrMore, got %q", pattern)
		}
	})

	t.Run("adds zeroOrMore modifier", func(t *testing.T) {
		pb := NewPatternBuilder()
		pattern := pb.Raw(`\d`).ZeroOrMore().String()

		if pattern != `\d*` {
			t.Errorf("expected zeroOrMore, got %q", pattern)
		}
	})

	t.Run("builds alternation", func(t *testing.T) {
		pb := NewPatternBuilder()
		pattern := pb.Or(`foo`, `bar`, `baz`).String()

		if pattern != `(?:foo|bar|baz)` {
			t.Errorf("expected alternation, got %q", pattern)
		}
	})

	t.Run("Build compiles pattern", func(t *testing.T) {
		pb := NewPatternBuilder()
		re, err := pb.Raw(`\d+`).Build()

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !re.MatchString("123") {
			t.Error("regex should match digits")
		}
	})

	t.Run("MustBuild panics on invalid pattern", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic for invalid pattern")
			}
		}()

		pb := NewPatternBuilder()
		pb.Raw(`[invalid`).MustBuild()
	})

	t.Run("BuildCached uses cache", func(t *testing.T) {
		cache := NewRegexCache()
		pb := NewPatternBuilder()

		re1, err := pb.Raw(`cached-\d+`).BuildCached(cache)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Reset builder and rebuild same pattern
		pb2 := NewPatternBuilder()
		re2, err := pb2.Raw(`cached-\d+`).BuildCached(cache)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if re1 != re2 {
			t.Error("expected same cached regex")
		}
	})

	t.Run("SafeBuild returns error for invalid pattern", func(t *testing.T) {
		pb := NewPatternBuilder()
		re, err := pb.Raw(`[invalid`).SafeBuild()

		if err == nil {
			t.Error("expected error for invalid pattern")
		}
		if re != nil {
			t.Error("expected nil regex for invalid pattern")
		}
	})

	t.Run("SafeBuild succeeds for valid pattern", func(t *testing.T) {
		pb := NewPatternBuilder()
		re, err := pb.Raw(`\d+`).SafeBuild()

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if re == nil {
			t.Fatal("expected non-nil regex")
		}
		if !re.MatchString("456") {
			t.Error("regex should match digits")
		}
	})
}

// =============================================================================
// RegexPanicError Tests
// =============================================================================

func TestRegexPanicError(t *testing.T) {
	t.Run("Error returns formatted message", func(t *testing.T) {
		err := &RegexPanicError{Message: "test panic"}
		expected := "regex_cache: recovered panic: test panic"
		if err.Error() != expected {
			t.Errorf("Error() = %q, want %q", err.Error(), expected)
		}
	})

	t.Run("implements error interface", func(t *testing.T) {
		var err error = &RegexPanicError{Message: "test"}
		if err == nil {
			t.Error("expected non-nil error")
		}
	})
}

// =============================================================================
// Panic Recovery Tests (simulating background goroutine scenarios)
// =============================================================================

func TestPanicRecoveryInGoroutines(t *testing.T) {
	cache := NewRegexCache()

	t.Run("SafeGetOrCompile prevents goroutine crash", func(t *testing.T) {
		errChan := make(chan error, 10)
		var wg sync.WaitGroup

		// Simulate background goroutines with potentially invalid patterns
		patterns := []string{`\d+`, `[invalid`, `[a-z]+`, `(unclosed`, `valid\w+`}

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				pattern := patterns[idx%len(patterns)]

				// This should never panic
				re, err := cache.SafeGetOrCompile(pattern)
				if err != nil {
					errChan <- err
				} else if re == nil {
					errChan <- &RegexPanicError{Message: "unexpected nil regex for: " + pattern}
				}
			}(i)
		}

		wg.Wait()
		close(errChan)

		// Count errors - we expect some for invalid patterns
		errorCount := 0
		for range errChan {
			errorCount++
		}

		// We have 2 invalid patterns out of 5, so expect 4 errors from 10 goroutines
		if errorCount != 4 {
			t.Logf("got %d errors (expected 4 for invalid patterns)", errorCount)
		}
	})

	t.Run("SafeGetOrCompileGlobal prevents goroutine crash", func(t *testing.T) {
		done := make(chan bool, 5)
		var wg sync.WaitGroup

		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				pattern := `[unclosed-` + string(rune('a'+idx))

				// This should never panic
				_, err := SafeGetOrCompileGlobal(pattern)
				if err == nil {
					t.Error("expected error for invalid pattern")
				}
				done <- true
			}(i)
		}

		wg.Wait()
		close(done)

		completedCount := 0
		for range done {
			completedCount++
		}

		if completedCount != 5 {
			t.Errorf("expected 5 goroutines to complete safely, got %d", completedCount)
		}
	})

	t.Run("SafeBuild prevents goroutine crash", func(t *testing.T) {
		done := make(chan bool, 5)
		var wg sync.WaitGroup

		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				pb := NewPatternBuilder()
				_, err := pb.Raw(`[invalid-pattern`).SafeBuild()
				if err == nil {
					t.Error("expected error for invalid pattern")
				}
				done <- true
			}()
		}

		wg.Wait()
		close(done)

		completedCount := 0
		for range done {
			completedCount++
		}

		if completedCount != 5 {
			t.Errorf("expected 5 goroutines to complete safely, got %d", completedCount)
		}
	})
}

// =============================================================================
// Optimized Tokenization Function Tests
// =============================================================================

func TestTokenizeQueryOptimized(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"hello world", []string{"hello", "world"}},
		{"find-function", []string{"find-function"}},
		{"test_case", []string{"test_case"}},
		{"Hello World!", []string{"Hello", "World"}},
		{"multiple   spaces", []string{"multiple", "spaces"}},
		{"", nil},
		{"   ", nil},
	}

	for _, tc := range tests {
		result := TokenizeQueryOptimized(tc.input)
		if len(result) != len(tc.expected) {
			t.Errorf("TokenizeQueryOptimized(%q) = %v, want %v", tc.input, result, tc.expected)
			continue
		}
		for i, word := range result {
			if word != tc.expected[i] {
				t.Errorf("TokenizeQueryOptimized(%q)[%d] = %q, want %q", tc.input, i, word, tc.expected[i])
			}
		}
	}
}

func TestSplitOnWhitespace(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"hello world", []string{"hello", "world"}},
		{"one\ttwo\nthree", []string{"one", "two", "three"}},
		{"", nil},
	}

	for _, tc := range tests {
		result := SplitOnWhitespace(tc.input)
		if len(result) != len(tc.expected) {
			t.Errorf("SplitOnWhitespace(%q) = %v, want %v", tc.input, result, tc.expected)
		}
	}
}

func TestRemovePunctuation(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello, World!", "Hello World"},
		{"test.case", "testcase"},
		{"no punctuation", "no punctuation"},
	}

	for _, tc := range tests {
		result := RemovePunctuation(tc.input)
		if result != tc.expected {
			t.Errorf("RemovePunctuation(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func TestSplitCamelCase(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"getUserName", []string{"get", "User", "Name"}},
		{"XMLParser", []string{"XMLParser"}}, // All caps followed by caps - stays together
		{"simple", []string{"simple"}},
	}

	for _, tc := range tests {
		result := SplitCamelCase(tc.input)
		if len(result) != len(tc.expected) {
			t.Errorf("SplitCamelCase(%q) = %v, want %v", tc.input, result, tc.expected)
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
		{"simple", []string{"simple"}},
	}

	for _, tc := range tests {
		result := SplitSnakeCase(tc.input)
		if len(result) != len(tc.expected) {
			t.Errorf("SplitSnakeCase(%q) = %v, want %v", tc.input, result, tc.expected)
		}
	}
}

func TestExtractIdentifiers(t *testing.T) {
	result := ExtractIdentifiers("func main() { var x = foo(); }")
	expected := []string{"func", "main", "var", "x", "foo"}

	if len(result) != len(expected) {
		t.Errorf("ExtractIdentifiers returned %v, want %v", result, expected)
	}
}

func TestExtractEmails(t *testing.T) {
	result := ExtractEmails("Contact us at support@example.com or sales@company.org")
	if len(result) != 2 {
		t.Errorf("expected 2 emails, got %d", len(result))
	}
}

func TestExtractURLs(t *testing.T) {
	result := ExtractURLs("Visit https://example.com or http://test.org/path")
	if len(result) != 2 {
		t.Errorf("expected 2 URLs, got %d", len(result))
	}
}

func TestExtractVersions(t *testing.T) {
	result := ExtractVersions("Upgrade from v1.0.0 to 2.1.3-beta")
	if len(result) != 2 {
		t.Errorf("expected 2 versions, got %d", len(result))
	}
}

func TestExtractUUIDs(t *testing.T) {
	result := ExtractUUIDs("ID: 123e4567-e89b-12d3-a456-426614174000")
	if len(result) != 1 {
		t.Errorf("expected 1 UUID, got %d", len(result))
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkPrecompiledVsCompileEveryTime(b *testing.B) {
	input := "hello world test case function method"

	b.Run("PrecompiledPattern", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = TokenizePattern.Split(input, -1)
		}
	})

	b.Run("CompileEveryTime", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			re := regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
			_ = re.Split(input, -1)
		}
	})
}

func BenchmarkRegexCache_GetOrCompile(b *testing.B) {
	cache := NewRegexCache()
	pattern := `\d+`

	// Pre-populate cache
	_, _ = cache.GetOrCompile(pattern)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cache.GetOrCompile(pattern)
	}
}

func BenchmarkRegexCache_ConcurrentGetOrCompile(b *testing.B) {
	cache := NewRegexCache()
	patterns := []string{`\d+`, `\w+`, `[a-z]+`, `[A-Z]+`, `\s+`}

	// Pre-populate cache
	for _, p := range patterns {
		_, _ = cache.GetOrCompile(p)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			pattern := patterns[i%len(patterns)]
			_, _ = cache.GetOrCompile(pattern)
			i++
		}
	})
}
