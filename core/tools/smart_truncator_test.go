package tools

import (
	"strings"
	"testing"
)

func TestNewSmartTruncator(t *testing.T) {
	config := DefaultSmartTruncatorConfig()
	detector := NewImportanceDetector(DefaultImportancePatterns())
	tr := NewSmartTruncator(config, detector)

	if tr == nil {
		t.Fatal("expected non-nil truncator")
	}
	if tr.keepFirst != 50 {
		t.Errorf("expected keepFirst=50, got %d", tr.keepFirst)
	}
	if tr.keepLast != 100 {
		t.Errorf("expected keepLast=100, got %d", tr.keepLast)
	}
}

func TestSmartTruncator_Truncate_Small(t *testing.T) {
	config := SmartTruncatorConfig{KeepFirstLines: 5, KeepLastLines: 5, MaxBufferSize: 1024}
	detector := NewImportanceDetector(DefaultImportancePatterns())
	tr := NewSmartTruncator(config, detector)

	stdout := []byte("line1\nline2\nline3\n")
	result := tr.Truncate(stdout, nil)

	if result.Truncated {
		t.Error("small output should not be truncated")
	}
	if result.TotalLines != 4 {
		t.Errorf("expected 4 total lines, got %d", result.TotalLines)
	}
	if len(result.FirstLines) != 4 {
		t.Errorf("expected 4 first lines, got %d", len(result.FirstLines))
	}
}

func TestSmartTruncator_Truncate_Large(t *testing.T) {
	config := SmartTruncatorConfig{KeepFirstLines: 2, KeepLastLines: 2, MaxBufferSize: 100}
	detector := NewImportanceDetector(DefaultImportancePatterns())
	tr := NewSmartTruncator(config, detector)

	var lines []string
	for i := 0; i < 10; i++ {
		lines = append(lines, "line content here that is reasonably long")
	}
	stdout := []byte(strings.Join(lines, "\n"))
	result := tr.Truncate(stdout, nil)

	if !result.Truncated {
		t.Error("large output should be truncated")
	}
	if len(result.FirstLines) != 2 {
		t.Errorf("expected 2 first lines, got %d", len(result.FirstLines))
	}
	if len(result.LastLines) != 2 {
		t.Errorf("expected 2 last lines, got %d", len(result.LastLines))
	}
}

func TestSmartTruncator_Truncate_ImportantLines(t *testing.T) {
	config := SmartTruncatorConfig{KeepFirstLines: 1, KeepLastLines: 1, MaxBufferSize: 1024}
	detector := NewImportanceDetector(DefaultImportancePatterns())
	tr := NewSmartTruncator(config, detector)

	stdout := []byte("first line\nnormal\nError: important\nmore normal\nlast line")
	result := tr.Truncate(stdout, nil)

	if len(result.ImportantLines) != 1 {
		t.Errorf("expected 1 important line, got %d", len(result.ImportantLines))
	}
	if result.ImportantLines[0] != "Error: important" {
		t.Errorf("expected 'Error: important', got %q", result.ImportantLines[0])
	}
}

func TestSmartTruncator_Truncate_DedupeImportant(t *testing.T) {
	config := SmartTruncatorConfig{KeepFirstLines: 2, KeepLastLines: 2, MaxBufferSize: 1024}
	detector := NewImportanceDetector(DefaultImportancePatterns())
	tr := NewSmartTruncator(config, detector)

	stdout := []byte("Error: in first\nline2\nError: middle\nline4\nError: in last")
	result := tr.Truncate(stdout, nil)

	if len(result.ImportantLines) != 1 {
		t.Errorf("expected 1 deduped important line, got %d: %v", len(result.ImportantLines), result.ImportantLines)
	}
}

func TestSmartTruncator_GenerateSummary_WithErrors(t *testing.T) {
	config := SmartTruncatorConfig{KeepFirstLines: 10, KeepLastLines: 10, MaxBufferSize: 1024}
	detector := NewImportanceDetector(DefaultImportancePatterns())
	tr := NewSmartTruncator(config, detector)

	stdout := []byte("Error: one\nWarning: two\nError: three\nnormal line")
	result := tr.Truncate(stdout, nil)

	if !strings.Contains(result.Summary, "2 errors") {
		t.Errorf("expected '2 errors' in summary, got %q", result.Summary)
	}
	if !strings.Contains(result.Summary, "1 warnings") {
		t.Errorf("expected '1 warnings' in summary, got %q", result.Summary)
	}
}

func TestSmartTruncator_GenerateSummary_NoErrors(t *testing.T) {
	config := SmartTruncatorConfig{KeepFirstLines: 10, KeepLastLines: 10, MaxBufferSize: 1024}
	detector := NewImportanceDetector(DefaultImportancePatterns())
	tr := NewSmartTruncator(config, detector)

	stdout := []byte("normal\noutput\nlines")
	result := tr.Truncate(stdout, nil)

	if !strings.Contains(result.Summary, "3 lines of output") {
		t.Errorf("expected '3 lines of output' in summary, got %q", result.Summary)
	}
}

func TestSmartTruncator_Truncate_StderrCombined(t *testing.T) {
	config := SmartTruncatorConfig{KeepFirstLines: 10, KeepLastLines: 10, MaxBufferSize: 1024}
	detector := NewImportanceDetector(DefaultImportancePatterns())
	tr := NewSmartTruncator(config, detector)

	stdout := []byte("stdout line\n")
	stderr := []byte("Error: stderr line\n")
	result := tr.Truncate(stdout, stderr)

	if result.TotalLines != 3 {
		t.Errorf("expected 3 total lines, got %d", result.TotalLines)
	}
	if !strings.Contains(result.Summary, "1 errors") {
		t.Errorf("expected error count in summary, got %q", result.Summary)
	}
}

func TestSmartTruncator_Truncate_Empty(t *testing.T) {
	config := DefaultSmartTruncatorConfig()
	detector := NewImportanceDetector(DefaultImportancePatterns())
	tr := NewSmartTruncator(config, detector)

	result := tr.Truncate(nil, nil)

	if result.TotalLines != 1 {
		t.Errorf("expected 1 total line (empty split), got %d", result.TotalLines)
	}
	if result.Truncated {
		t.Error("empty output should not be truncated")
	}
}

func TestSmartTruncator_ExtractFirstLines(t *testing.T) {
	config := SmartTruncatorConfig{KeepFirstLines: 3, KeepLastLines: 3, MaxBufferSize: 1024}
	detector := NewImportanceDetector(nil)
	tr := NewSmartTruncator(config, detector)

	lines := []string{"a", "b", "c", "d", "e"}
	result := tr.extractFirstLines(lines)

	if len(result) != 3 {
		t.Errorf("expected 3 lines, got %d", len(result))
	}
	if result[0] != "a" || result[2] != "c" {
		t.Errorf("unexpected first lines: %v", result)
	}
}

func TestSmartTruncator_ExtractLastLines(t *testing.T) {
	config := SmartTruncatorConfig{KeepFirstLines: 3, KeepLastLines: 2, MaxBufferSize: 1024}
	detector := NewImportanceDetector(nil)
	tr := NewSmartTruncator(config, detector)

	lines := []string{"a", "b", "c", "d", "e"}
	result := tr.extractLastLines(lines)

	if len(result) != 2 {
		t.Errorf("expected 2 lines, got %d", len(result))
	}
	if result[0] != "d" || result[1] != "e" {
		t.Errorf("unexpected last lines: %v", result)
	}
}

func TestSmartTruncator_ExtractLastLines_TooFew(t *testing.T) {
	config := SmartTruncatorConfig{KeepFirstLines: 3, KeepLastLines: 10, MaxBufferSize: 1024}
	detector := NewImportanceDetector(nil)
	tr := NewSmartTruncator(config, detector)

	lines := []string{"a", "b", "c"}
	result := tr.extractLastLines(lines)

	if result != nil {
		t.Errorf("expected nil for small input, got %v", result)
	}
}
