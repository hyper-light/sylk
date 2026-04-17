package versioning

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestVFSFileAccess_GrepRespectsSparseWorkspace(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed.go")
	hidden := filepath.Join(dir, "hidden.go")
	if err := os.WriteFile(hidden, []byte("needle"), 0644); err != nil {
		t.Fatalf("WriteFile hidden: %v", err)
	}

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID:   "pipe1",
		SessionID:    "sess-1",
		WorkingDir:   dir,
		AllowedPaths: []string{allowed},
	}, nil, nil)
	defer vfs.Close()
	vfs.SeedFile(allowed, []byte("needle"))

	fa := NewVFSFileAccess(vfs, dir)
	matches, err := fa.Grep(context.Background(), dir, "needle", "*.go", 0, 10)
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(matches))
	}
	if matches[0].File != "allowed.go" {
		t.Fatalf("match file = %q, want allowed.go", matches[0].File)
	}
}

func TestVFSFileAccess_WriteFileWidensSparseWorkspaceHierarchy(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed.go")

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID:   "pipe1",
		SessionID:    "sess-1",
		WorkingDir:   dir,
		AllowedPaths: []string{allowed},
	}, nil, nil)
	defer vfs.Close()

	fa := NewVFSFileAccess(vfs, dir)
	ctx := context.Background()

	if err := fa.WriteFile(ctx, filepath.Join("generated", "new.txt"), []byte("draft")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	content, err := fa.ReadFile(ctx, filepath.Join("generated", "new.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "draft" {
		t.Fatalf("content = %q, want %q", string(content), "draft")
	}

	rootEntries, err := fa.ListDir(ctx, ".")
	if err != nil {
		t.Fatalf("ListDir(.): %v", err)
	}
	if len(rootEntries) != 1 || rootEntries[0].Name() != "generated" {
		t.Fatalf("root entries = %v, want [generated]", dirEntryNames(rootEntries))
	}

	generatedEntries, err := fa.ListDir(ctx, "generated")
	if err != nil {
		t.Fatalf("ListDir(generated): %v", err)
	}
	if len(generatedEntries) != 1 || generatedEntries[0].Name() != "new.txt" {
		t.Fatalf("generated entries = %v, want [new.txt]", dirEntryNames(generatedEntries))
	}
}

func TestReadFileToolResult_AllowsSessionPlanArtifactsOutsideSparseWorkspace(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed.go")
	planPath := filepath.Join(dir, ".sylk", "sessions", "sess-1", "plans", "plan_v1.0.md")
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(planPath, []byte("# Plan\n\nhello"), 0o644); err != nil {
		t.Fatalf("WriteFile plan: %v", err)
	}

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID:   "pipe1",
		SessionID:    "sess-1",
		WorkingDir:   dir,
		AllowedPaths: []string{allowed},
	}, nil, nil)
	defer vfs.Close()
	vfs.SeedFile(allowed, []byte("package main"))

	fa := NewVFSFileAccess(vfs, dir)
	result, err := ReadFileToolResult(context.Background(), fa, planPath, 0, 0)
	if err != nil {
		t.Fatalf("ReadFileToolResult: %v", err)
	}
	if got, _ := result["kind"].(string); got != "file" {
		t.Fatalf("kind = %q, want file", got)
	}
	content, _ := result["content"].(string)
	if content != "# Plan\n\nhello" {
		t.Fatalf("content = %q, want %q", content, "# Plan\n\nhello")
	}
}

func TestVFSFileAccess_MkdirAllWidensSparseWorkspaceHierarchy(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed.go")

	vfs := NewPipelineVFS(VFSConfig{
		PipelineID:   "pipe1",
		SessionID:    "sess-1",
		WorkingDir:   dir,
		AllowedPaths: []string{allowed},
	}, nil, nil)
	defer vfs.Close()

	fa := NewVFSFileAccess(vfs, dir)
	ctx := context.Background()

	if err := fa.MkdirAll(ctx, filepath.Join("generated", "nested")); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	rootEntries, err := fa.ListDir(ctx, ".")
	if err != nil {
		t.Fatalf("ListDir(.): %v", err)
	}
	if len(rootEntries) != 1 || rootEntries[0].Name() != "generated" {
		t.Fatalf("root entries = %v, want [generated]", dirEntryNames(rootEntries))
	}

	generatedEntries, err := fa.ListDir(ctx, "generated")
	if err != nil {
		t.Fatalf("ListDir(generated): %v", err)
	}
	if len(generatedEntries) != 1 || generatedEntries[0].Name() != "nested" {
		t.Fatalf("generated entries = %v, want [nested]", dirEntryNames(generatedEntries))
	}

	info, err := fa.Stat(ctx, filepath.Join("generated", "nested"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected generated/nested to be a directory")
	}
}

func dirEntryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
