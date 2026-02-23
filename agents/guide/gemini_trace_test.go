package guide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeminiTraceFilePath(t *testing.T) {
	t.Setenv(geminiTraceEnvEnable, "")
	t.Setenv(geminiTraceEnvFile, "")
	if got := geminiTraceFilePath(); got != "" {
		t.Fatalf("geminiTraceFilePath() = %q, want empty", got)
	}

	t.Setenv(geminiTraceEnvEnable, "true")
	if got := geminiTraceFilePath(); got != geminiTraceDefaultLog {
		t.Fatalf("geminiTraceFilePath() with enable = %q, want %q", got, geminiTraceDefaultLog)
	}

	t.Setenv(geminiTraceEnvFile, "/tmp/custom-gemini-trace.log")
	if got := geminiTraceFilePath(); got != "/tmp/custom-gemini-trace.log" {
		t.Fatalf("geminiTraceFilePath() with explicit file = %q", got)
	}
}

func TestGeminiTraceWritesLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.log")
	t.Setenv(geminiTraceEnvEnable, "")
	t.Setenv(geminiTraceEnvFile, path)

	geminiTrace("classifier", "stream_no_candidates", map[string]any{
		"chunk_counts": map[string]int{"start": 1, "end": 1},
		"text_len":     0,
	})

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace file: %v", err)
	}
	line := string(content)
	if !strings.Contains(line, `"component":"classifier"`) {
		t.Fatalf("trace line missing component: %q", line)
	}
	if !strings.Contains(line, `"event":"stream_no_candidates"`) {
		t.Fatalf("trace line missing event: %q", line)
	}
}

func TestLooksLikeCompleteJSONObject(t *testing.T) {
	if !looksLikeCompleteJSONObject(`{"intent":"help"}`) {
		t.Fatal("expected complete JSON object to return true")
	}
	if looksLikeCompleteJSONObject(`{"intent":"help"`) {
		t.Fatal("expected incomplete JSON object to return false")
	}
	if looksLikeCompleteJSONObject(`[]`) {
		t.Fatal("expected non-object JSON to return false")
	}
}
