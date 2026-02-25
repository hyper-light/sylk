package versioning

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDiskFileAccess_ReadWriteFile(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)
	ctx := context.Background()

	content := []byte("hello world")
	if err := fa.WriteFile(ctx, "test.txt", content); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := fa.ReadFile(ctx, "test.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
}

func TestDiskFileAccess_ReadOnly(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, true)
	ctx := context.Background()

	if err := fa.WriteFile(ctx, "test.txt", []byte("data")); err == nil {
		t.Fatal("expected error for read-only write")
	}
	if err := fa.EditFile(ctx, "test.txt", []FileEdit{{OldText: "a", NewText: "b"}}); err == nil {
		t.Fatal("expected error for read-only edit")
	}
	if err := fa.DeleteFile(ctx, "test.txt"); err == nil {
		t.Fatal("expected error for read-only delete")
	}
	if !fa.IsReadOnly() {
		t.Fatal("expected IsReadOnly to be true")
	}
}

func TestDiskFileAccess_EditFile(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)
	ctx := context.Background()

	if err := fa.WriteFile(ctx, "edit.txt", []byte("hello world")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := fa.EditFile(ctx, "edit.txt", []FileEdit{{OldText: "world", NewText: "go"}}); err != nil {
		t.Fatalf("EditFile: %v", err)
	}

	got, err := fa.ReadFile(ctx, "edit.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello go" {
		t.Fatalf("got %q, want %q", got, "hello go")
	}
}

func TestDiskFileAccess_EditFile_NotFound(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)
	ctx := context.Background()

	if err := fa.WriteFile(ctx, "edit.txt", []byte("hello")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := fa.EditFile(ctx, "edit.txt", []FileEdit{{OldText: "missing", NewText: "x"}})
	if err == nil {
		t.Fatal("expected error for missing old_text")
	}
}

func TestDiskFileAccess_DeleteFile(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)
	ctx := context.Background()

	if err := fa.WriteFile(ctx, "del.txt", []byte("data")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := fa.DeleteFile(ctx, "del.txt"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}

	exists, _ := fa.Exists(ctx, "del.txt")
	if exists {
		t.Fatal("file should not exist after delete")
	}
}

func TestDiskFileAccess_Exists(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)
	ctx := context.Background()

	exists, err := fa.Exists(ctx, "nonexistent.txt")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Fatal("expected file to not exist")
	}

	if err := fa.WriteFile(ctx, "exists.txt", []byte("data")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	exists, err = fa.Exists(ctx, "exists.txt")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Fatal("expected file to exist")
	}
}

func TestDiskFileAccess_ListDir(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)
	ctx := context.Background()

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0644)

	entries, err := fa.ListDir(ctx, ".")
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestDiskFileAccess_Glob(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)
	ctx := context.Background()

	os.WriteFile(filepath.Join(dir, "a.go"), []byte("go"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("txt"), 0644)

	matches, err := fa.Glob(ctx, ".", "*.go", nil)
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 1 || matches[0] != "a.go" {
		t.Fatalf("expected [a.go], got %v", matches)
	}
}

func TestDiskFileAccess_Grep(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)
	ctx := context.Background()

	os.WriteFile(filepath.Join(dir, "test.go"), []byte("func main() {\n\tfmt.Println(\"hello\")\n}\n"), 0644)

	matches, err := fa.Grep(ctx, ".", "Println", "*.go", 0, 10)
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Line != 2 {
		t.Fatalf("expected line 2, got %d", matches[0].Line)
	}
}

func TestDiskFileAccess_Stat(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)
	ctx := context.Background()

	os.WriteFile(filepath.Join(dir, "stat.txt"), []byte("data"), 0644)

	info, err := fa.Stat(ctx, "stat.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != 4 {
		t.Fatalf("expected size 4, got %d", info.Size())
	}
}

func TestDiskFileAccess_WorkingDir(t *testing.T) {
	fa := NewDiskFileAccess("/tmp/test", false)
	if fa.WorkingDir() != "/tmp/test" {
		t.Fatalf("expected /tmp/test, got %s", fa.WorkingDir())
	}
}

func TestDiskFileAccess_WriteCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	fa := NewDiskFileAccess(dir, false)
	ctx := context.Background()

	if err := fa.WriteFile(ctx, "sub/dir/file.txt", []byte("nested")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := fa.ReadFile(ctx, "sub/dir/file.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "nested" {
		t.Fatalf("got %q, want %q", got, "nested")
	}
}
