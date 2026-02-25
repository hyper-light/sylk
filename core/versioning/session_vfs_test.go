package versioning

import (
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

	// Add a version via the adapter.
	fv := NewFileVersion("test.go", []byte("content"), nil, nil, "p1", "s1", NewVectorClock())
	if err := adapter.AddVersion(fv); err != nil {
		t.Fatalf("AddVersion: %v", err)
	}

	// GetHead.
	head, err := adapter.GetHead("test.go")
	if err != nil {
		t.Fatalf("GetHead: %v", err)
	}
	if head.ID != fv.ID {
		t.Fatalf("expected ID %v, got %v", fv.ID, head.ID)
	}

	// GetVersion.
	ver, err := adapter.GetVersion(fv.ID)
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if ver.FilePath != "test.go" {
		t.Fatalf("expected path test.go, got %s", ver.FilePath)
	}

	// GetHistory.
	hist, err := adapter.GetHistory("test.go", 10)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("expected 1 version, got %d", len(hist))
	}
}
