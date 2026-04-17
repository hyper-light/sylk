package prompts

import (
	"errors"
	iofs "io/fs"
	"strings"
	"testing"
)

func TestLoad_ReturnsPromptContent(t *testing.T) {
	content, err := Load("architect", "system")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if strings.TrimSpace(content) == "" {
		t.Fatal("Load returned empty prompt content")
	}
}

func TestLoad_MissingPromptReturnsError(t *testing.T) {
	_, err := Load("missing-agent", "missing-prompt")
	if err == nil {
		t.Fatal("expected missing prompt error")
	}
	var missing *MissingPromptError
	if !errors.As(err, &missing) {
		t.Fatalf("expected MissingPromptError, got %T", err)
	}
	if missing.Path != "missing-agent/missing-prompt.md" {
		t.Fatalf("path = %q, want %q", missing.Path, "missing-agent/missing-prompt.md")
	}
	if !errors.Is(err, iofs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist, got %v", err)
	}
}

func TestMustLoad_RecordsMissingPromptInsteadOfPanicking(t *testing.T) {
	snapshot := snapshotMissingLoads()
	t.Cleanup(func() { restoreMissingLoads(snapshot) })

	got := MustLoad("missing-agent", "missing-prompt")
	if got != "[missing embedded prompt: missing-agent/missing-prompt.md]" {
		t.Fatalf("MustLoad = %q", got)
	}
	if err := Validate(); err == nil {
		t.Fatal("Validate returned nil, want missing prompt error")
	}
}

func snapshotMissingLoads() map[string]error {
	missingMu.Lock()
	defer missingMu.Unlock()
	snapshot := make(map[string]error, len(missingLoads))
	for path, err := range missingLoads {
		snapshot[path] = err
	}
	return snapshot
}

func restoreMissingLoads(snapshot map[string]error) {
	missingMu.Lock()
	defer missingMu.Unlock()
	missingLoads = make(map[string]error, len(snapshot))
	for path, err := range snapshot {
		missingLoads[path] = err
	}
}
