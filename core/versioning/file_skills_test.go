package versioning

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestNewReadFileSkill_DirectoryReturnsPreview(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)
	if err := os.Mkdir(filepath.Join(dir, "ui"), 0755); err != nil {
		t.Fatalf("mkdir ui: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ui", "theme.go"), []byte("package ui"), 0644); err != nil {
		t.Fatalf("write theme.go: %v", err)
	}

	skill := NewReadFileSkill(fa)
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

func TestNewEditFileSkillDeclaresRequiredEditFields(t *testing.T) {
	dir := t.TempDir()
	skill := NewEditFileSkill(NewDiskFileAccess(dir, false))
	editsProp := skill.InputSchema.Properties["edits"]
	if editsProp == nil {
		t.Fatal("expected edits property on edit_file schema")
	}
	if editsProp.Items == nil || editsProp.Items.Type != "object" {
		t.Fatalf("edits item schema = %+v, want object items", editsProp.Items)
	}
	if editsProp.Items.Properties["old_text"] == nil || editsProp.Items.Properties["new_text"] == nil {
		t.Fatalf("expected old_text/new_text item properties, got %+v", editsProp.Items.Properties)
	}
	required := strings.Join(editsProp.Items.Required, ",")
	if !strings.Contains(required, "old_text") || !strings.Contains(required, "new_text") {
		t.Fatalf("required edit fields = %v, want old_text and new_text", editsProp.Items.Required)
	}
}

func TestNewEditFileSkill_MissingOldTextReturnsRecoveryGuidance(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)
	skill := NewEditFileSkill(fa)
	input, _ := json.Marshal(map[string]any{
		"path": "edit.txt",
		"edits": []map[string]string{
			{"new_text": "go"},
		},
	})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for missing old_text")
	}
	if !strings.Contains(err.Error(), "old_text is required") {
		t.Fatalf("error = %v, want old_text requirement", err)
	}
	if !strings.Contains(err.Error(), "write_file") {
		t.Fatalf("error = %v, want write_file guidance", err)
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
