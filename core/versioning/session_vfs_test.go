package versioning

import (
	"context"
	"path/filepath"
	"testing"
)

func TestNewSessionVFS(t *testing.T) {
	dir := t.TempDir()
	svfs := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	defer svfs.Close()

	if svfs.CVS() == nil {
		t.Fatal("CVS should not be nil")
	}
	if svfs.VFSManager() == nil {
		t.Fatal("VFSManager should not be nil")
	}
	if svfs.SessionID() != "test-session" {
		t.Fatalf("expected session ID test-session, got %s", svfs.SessionID())
	}
	if svfs.GlobalVFS() == nil {
		t.Fatal("GlobalVFS should not be nil")
	}
	if svfs.MergePipe() == nil {
		t.Fatal("MergePipe should not be nil")
	}
	if svfs.DiskFlusher() == nil {
		t.Fatal("DiskFlusher should not be nil")
	}
	if svfs.WAL() == nil {
		t.Fatal("WAL should not be nil")
	}
}

func TestSessionVFS_NewDiskFileAccess(t *testing.T) {
	dir := t.TempDir()
	svfs := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	defer svfs.Close()

	fa := svfs.NewDiskFileAccess(true)
	if fa == nil {
		t.Fatal("DiskFileAccess should not be nil")
	}
	if !fa.IsReadOnly() {
		t.Fatal("expected read-only")
	}
	if fa.WorkingDir() != dir {
		t.Fatalf("expected working dir %s, got %s", dir, fa.WorkingDir())
	}
}

func TestSessionVFS_NewGlobalFileAccess(t *testing.T) {
	dir := t.TempDir()
	svfs := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	defer svfs.Close()

	fa := svfs.NewGlobalFileAccess(false)
	if fa == nil {
		t.Fatal("GlobalVFSFileAccess should not be nil")
	}
	if fa.IsReadOnly() {
		t.Fatal("expected writable")
	}
}

func TestSessionVFS_BeginAndCommitPipeline(t *testing.T) {
	dir := t.TempDir()
	svfs := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	defer svfs.Close()

	pVFS, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "pipe1",
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("BeginPipeline: %v", err)
	}

	// Write through the pipeline VFS.
	ctx := context.Background()
	if err := pVFS.Write(ctx, filepath.Join(dir, "test.go"), []byte("package test")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Commit pipeline → merge into global VFS.
	ver, err := svfs.CommitPipeline("pipe1")
	if err != nil {
		t.Fatalf("CommitPipeline: %v", err)
	}
	if ver.IsZero() {
		t.Error("expected non-zero version after commit")
	}

	// Global VFS should now have the file.
	content, err := svfs.GlobalVFS().Read(ctx, filepath.Join(dir, "test.go"))
	if err != nil {
		t.Fatalf("GlobalVFS.Read: %v", err)
	}
	if string(content) != "package test" {
		t.Errorf("content = %q, want %q", content, "package test")
	}
}

func TestSessionVFS_RollbackPipeline(t *testing.T) {
	dir := t.TempDir()
	svfs := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	defer svfs.Close()

	_, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "pipe1",
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("BeginPipeline: %v", err)
	}

	if err := svfs.RollbackPipeline("pipe1"); err != nil {
		t.Fatalf("RollbackPipeline: %v", err)
	}

	// WAL version should not have changed.
	if ver := svfs.CurrentVersion(); !ver.IsZero() {
		t.Errorf("expected zero version after rollback, got %s", ver)
	}
}

func TestSessionVFS_DoubleClose(t *testing.T) {
	dir := t.TempDir()
	svfs := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})

	if err := svfs.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := svfs.Close(); err != nil {
		t.Fatalf("second Close should be idempotent: %v", err)
	}
}

func TestDagVersionStoreAdapter(t *testing.T) {
	dag := NewMemoryDAGStore()
	adapter := &dagVersionStoreAdapter{dag: dag}

	fv := NewFileVersion("test.go", []byte("content"), nil, nil, "p1", "s1", NewVectorClock())
	if err := adapter.AddVersion(fv); err != nil {
		t.Fatalf("AddVersion: %v", err)
	}

	head, err := adapter.GetHead("test.go")
	if err != nil {
		t.Fatalf("GetHead: %v", err)
	}
	if head.ID != fv.ID {
		t.Fatalf("expected ID %v, got %v", fv.ID, head.ID)
	}

	ver, err := adapter.GetVersion(fv.ID)
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if ver.FilePath != "test.go" {
		t.Fatalf("expected path test.go, got %s", ver.FilePath)
	}

	hist, err := adapter.GetHistory("test.go", 10)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("expected 1 version, got %d", len(hist))
	}
}
