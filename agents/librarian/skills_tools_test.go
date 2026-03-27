package librarian

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileSkill_DirectoryReturnsPreview(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "ui"), 0755); err != nil {
		t.Fatalf("mkdir ui: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ui", "theme.go"), []byte("package ui"), 0644); err != nil {
		t.Fatalf("write theme.go: %v", err)
	}

	l, err := New(Config{
		ID:               "librarian-test",
		EnableLLM:        true,
		WorkingDirectory: dir,
	})
	if err != nil {
		t.Fatalf("new librarian: %v", err)
	}

	skill := readFileSkill(l)
	input, _ := json.Marshal(map[string]any{"path": "ui"})
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	m := result.(map[string]any)
	if got, want := m["kind"].(string), "directory"; got != want {
		t.Fatalf("kind = %q, want %q", got, want)
	}
	if !strings.Contains(m["message"].(string), "path is a directory") {
		t.Fatalf("message = %q, want directory guidance", m["message"])
	}
	entries, ok := m["entries"].([]string)
	if !ok {
		t.Fatalf("entries type = %T, want []string", m["entries"])
	}
	if len(entries) != 1 || entries[0] != "theme.go" {
		t.Fatalf("entries = %#v, want [theme.go]", entries)
	}
}
