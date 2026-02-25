package versioning

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNewReadFileSkill(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)
	os.WriteFile(filepath.Join(dir, "read.txt"), []byte("line1\nline2\nline3"), 0644)

	skill := NewReadFileSkill(fa)
	if skill.Name != "read_file" {
		t.Fatalf("expected name read_file, got %s", skill.Name)
	}

	input, _ := json.Marshal(map[string]any{"path": "read.txt"})
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	m := result.(map[string]any)
	if m["total_lines"].(int) != 3 {
		t.Fatalf("expected 3 lines, got %v", m["total_lines"])
	}
}

func TestNewWriteFileSkill(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)

	skill := NewWriteFileSkill(fa)
	input, _ := json.Marshal(map[string]any{"path": "write.txt", "content": "hello"})
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	m := result.(map[string]any)
	if m["action"].(string) != "create" {
		t.Fatalf("expected create action, got %s", m["action"])
	}

	// Write again — should be modify.
	result, err = skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	m = result.(map[string]any)
	if m["action"].(string) != "modify" {
		t.Fatalf("expected modify action, got %s", m["action"])
	}
}

func TestNewWriteFileSkill_ReadOnly(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, true)

	skill := NewWriteFileSkill(fa)
	input, _ := json.Marshal(map[string]any{"path": "write.txt", "content": "hello"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for read-only write")
	}
}

func TestNewEditFileSkill(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)
	os.WriteFile(filepath.Join(dir, "edit.txt"), []byte("hello world"), 0644)

	skill := NewEditFileSkill(fa)
	input, _ := json.Marshal(map[string]any{
		"path": "edit.txt",
		"edits": []map[string]string{
			{"old_text": "world", "new_text": "go"},
		},
	})
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	m := result.(map[string]any)
	if m["edits_applied"].(int) != 1 {
		t.Fatalf("expected 1 edit applied, got %v", m["edits_applied"])
	}

	got, _ := os.ReadFile(filepath.Join(dir, "edit.txt"))
	if string(got) != "hello go" {
		t.Fatalf("expected 'hello go', got %q", got)
	}
}

func TestNewGlobSkill(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("go"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("txt"), 0644)

	skill := NewGlobSkill(fa)
	input, _ := json.Marshal(map[string]any{"pattern": "*.go"})
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	m := result.(map[string]any)
	if m["count"].(int) != 1 {
		t.Fatalf("expected 1 match, got %v", m["count"])
	}
}

func TestNewGrepSkill(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)
	os.WriteFile(filepath.Join(dir, "test.go"), []byte("func main() {\n\tfmt.Println(\"hello\")\n}"), 0644)

	skill := NewGrepSkill(fa)
	input, _ := json.Marshal(map[string]any{"pattern": "Println"})
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	m := result.(map[string]any)
	if m["count"].(int) != 1 {
		t.Fatalf("expected 1 match, got %v", m["count"])
	}
}
