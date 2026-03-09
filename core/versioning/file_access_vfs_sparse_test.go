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
