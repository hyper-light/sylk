package tools

import (
	"sync"
	"testing"
)

func TestNewImportanceDetector(t *testing.T) {
	d := NewImportanceDetector(DefaultImportancePatterns())
	if d == nil {
		t.Fatal("expected non-nil detector")
	}
	if d.PatternCount() != len(DefaultImportancePatterns()) {
		t.Errorf("expected %d patterns, got %d", len(DefaultImportancePatterns()), d.PatternCount())
	}
}

func TestNewImportanceDetector_InvalidPattern(t *testing.T) {
	patterns := []string{`(?i)error`, `[invalid`}
	d := NewImportanceDetector(patterns)
	if d.PatternCount() != 1 {
		t.Errorf("expected 1 valid pattern, got %d", d.PatternCount())
	}
}

func TestImportanceDetector_IsImportant_Error(t *testing.T) {
	d := NewImportanceDetector(DefaultImportancePatterns())

	tests := []struct {
		line     string
		expected bool
	}{
		{"Error: something failed", true},
		{"ERROR: uppercase", true},
		{"error in lowercase", true},
		{"normal output line", false},
		{"Warning: be careful", true},
		{"failed to connect", true},
		{"Exception thrown", true},
		{"panic: runtime error", true},
		{"main.go:42:15: undefined", true},
		{"  at someFunction()", true},
		{"just regular text", false},
	}

	for _, tc := range tests {
		if got := d.IsImportant(tc.line); got != tc.expected {
			t.Errorf("IsImportant(%q) = %v, want %v", tc.line, got, tc.expected)
		}
	}
}

func TestImportanceDetector_AddPattern(t *testing.T) {
	d := NewImportanceDetector(nil)
	if d.PatternCount() != 0 {
		t.Fatal("expected empty detector")
	}

	if err := d.AddPattern(`(?i)critical`); err != nil {
		t.Fatalf("AddPattern failed: %v", err)
	}

	if d.PatternCount() != 1 {
		t.Errorf("expected 1 pattern, got %d", d.PatternCount())
	}

	if !d.IsImportant("CRITICAL: system down") {
		t.Error("expected CRITICAL to be important")
	}
}

func TestImportanceDetector_AddPattern_Invalid(t *testing.T) {
	d := NewImportanceDetector(nil)

	if err := d.AddPattern(`[invalid`); err == nil {
		t.Error("expected error for invalid pattern")
	}

	if d.PatternCount() != 0 {
		t.Errorf("expected 0 patterns after invalid add, got %d", d.PatternCount())
	}
}

func TestImportanceDetector_Concurrent(t *testing.T) {
	d := NewImportanceDetector(DefaultImportancePatterns())
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.IsImportant("Error: concurrent test")
			d.PatternCount()
		}()
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			d.AddPattern(`(?i)test` + string(rune('0'+n)))
		}(i)
	}

	wg.Wait()
}

func TestImportanceDetector_EmptyLine(t *testing.T) {
	d := NewImportanceDetector(DefaultImportancePatterns())
	if d.IsImportant("") {
		t.Error("empty line should not be important")
	}
}

func TestImportanceDetector_FileLineColumn(t *testing.T) {
	d := NewImportanceDetector(DefaultImportancePatterns())

	tests := []struct {
		line     string
		expected bool
	}{
		{"main.go:10:5: error here", true},
		{"src/file.ts:100:20: warning", true},
		{"package.json:1:1: parse error", true},
		{"no file reference here", false},
	}

	for _, tc := range tests {
		if got := d.IsImportant(tc.line); got != tc.expected {
			t.Errorf("IsImportant(%q) = %v, want %v", tc.line, got, tc.expected)
		}
	}
}

func TestImportanceDetector_StackTrace(t *testing.T) {
	d := NewImportanceDetector(DefaultImportancePatterns())

	stackLines := []string{
		"    at Object.<anonymous> (/path/to/file.js:10:5)",
		"    at Module._compile (internal/modules/cjs/loader.js:999:30)",
		"\tat main.main(main.go:15)",
	}

	for _, line := range stackLines {
		if !d.IsImportant(line) {
			t.Errorf("expected stack trace line %q to be important", line)
		}
	}
}
