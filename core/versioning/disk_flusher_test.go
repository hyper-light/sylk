package versioning

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDiskFlusher_Flush(t *testing.T) {
	dir := t.TempDir()
	walDir := filepath.Join(dir, "wal")
	wal, err := OpenVersionedWAL(VersionedWALConfig{Dir: walDir})
	if err != nil {
		t.Fatalf("OpenVersionedWAL: %v", err)
	}
	defer wal.Close()

	globalVFS := NewGlobalVFS(VFSConfig{
		PipelineID: "global",
		WorkingDir: dir,
	})
	defer globalVFS.Close()

	ctx := context.Background()
	// Write a file through the global VFS overlay.
	if err := globalVFS.Write(ctx, filepath.Join(dir, "hello.txt"), []byte("hello world")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	df := NewDiskFlusher(DiskFlusherConfig{
		GlobalVFS:       globalVFS,
		WAL:             wal,
		WorkingDir:      dir,
		AllowDiskExport: true,
	})

	pending := df.PendingChanges()
	if len(pending) != 1 {
		t.Fatalf("PendingChanges len = %d, want 1", len(pending))
	}

	result, err := df.Flush(ctx)
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if result.FilesWritten != 1 {
		t.Errorf("FilesWritten = %d, want 1", result.FilesWritten)
	}
	if result.Version.Major != 1 {
		t.Errorf("Version.Major = %d, want 1", result.Version.Major)
	}

	// Verify file on disk.
	content, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "hello world" {
		t.Errorf("content = %q, want %q", content, "hello world")
	}

	// Overlay should be cleared.
	if mods := globalVFS.GetModifications(); len(mods) != 0 {
		t.Errorf("overlay not cleared: %d mods remain", len(mods))
	}
}

func TestDiskFlusher_RollbackMajor(t *testing.T) {
	dir := t.TempDir()
	walDir := filepath.Join(dir, "wal")
	wal, err := OpenVersionedWAL(VersionedWALConfig{Dir: walDir})
	if err != nil {
		t.Fatalf("OpenVersionedWAL: %v", err)
	}
	defer wal.Close()

	globalVFS := NewGlobalVFS(VFSConfig{
		PipelineID: "global",
		WorkingDir: dir,
	})
	defer globalVFS.Close()

	ctx := context.Background()
	filePath := filepath.Join(dir, "data.txt")

	// Write original to disk.
	os.WriteFile(filePath, []byte("original"), 0644)

	// Modify through global VFS, then flush.
	globalVFS.Write(ctx, filePath, []byte("modified"))
	df := NewDiskFlusher(DiskFlusherConfig{
		GlobalVFS:       globalVFS,
		WAL:             wal,
		WorkingDir:      dir,
		AllowDiskExport: true,
	})
	df.Flush(ctx) // major 1

	// Now rollback to version 0 (before any changes).
	// We need to apply inverse. Since target is major 0 minor 0 (non-major),
	// this will only touch the VFS overlay. For a full disk rollback, we'd
	// target major 1, minor 0.
	// Let's do another modification and flush first.
	globalVFS.Write(ctx, filePath, []byte("modified-v2"))
	df.Flush(ctx) // major 2

	// Rollback to major 1.
	err = df.RollbackToVersion(ctx, SemanticVersion{Major: 1, Minor: 0})
	if err != nil {
		t.Fatalf("RollbackToVersion: %v", err)
	}

	// Verify disk was restored.
	content, _ := os.ReadFile(filePath)
	if string(content) != "modified" {
		t.Errorf("after rollback content = %q, want %q", content, "modified")
	}
}

func TestDiskFlusher_EmptyFlush(t *testing.T) {
	dir := t.TempDir()
	walDir := filepath.Join(dir, "wal")
	wal, _ := OpenVersionedWAL(VersionedWALConfig{Dir: walDir})
	defer wal.Close()

	globalVFS := NewGlobalVFS(VFSConfig{
		PipelineID: "global",
		WorkingDir: dir,
	})
	defer globalVFS.Close()

	df := NewDiskFlusher(DiskFlusherConfig{
		GlobalVFS:       globalVFS,
		WAL:             wal,
		WorkingDir:      dir,
		AllowDiskExport: true,
	})

	result, err := df.Flush(context.Background())
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if result.FilesWritten != 0 {
		t.Errorf("FilesWritten = %d, want 0", result.FilesWritten)
	}
}

func TestDiskFlusher_FlushRollbackOnCheckpointFailure(t *testing.T) {
	dir := t.TempDir()
	backing := NewMemoryVersionedWAL()

	globalVFS := NewGlobalVFS(VFSConfig{
		PipelineID: "global",
		WorkingDir: dir,
	})
	defer globalVFS.Close()

	target := filepath.Join(dir, "rolled-back.txt")
	if err := os.WriteFile(target, []byte("committed"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := globalVFS.Write(context.Background(), target, []byte("draft")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	df := NewDiskFlusher(DiskFlusherConfig{
		GlobalVFS:       globalVFS,
		WAL:             failingCheckpointWAL{SemanticWAL: backing},
		WorkingDir:      dir,
		AllowDiskExport: true,
	})

	if _, err := df.Flush(context.Background()); err == nil {
		t.Fatal("Flush error = nil, want checkpoint failure")
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "committed" {
		t.Fatalf("disk content = %q, want %q", string(content), "committed")
	}

	overlay, err := globalVFS.Read(context.Background(), target)
	if err != nil {
		t.Fatalf("GlobalVFS.Read: %v", err)
	}
	if string(overlay) != "draft" {
		t.Fatalf("overlay content = %q, want %q", string(overlay), "draft")
	}
}

func TestDiskFlusher_FlushSkipsTransientExecutionArtifactsAndRetainsOverlay(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:       "test-session",
		WorkingDir:      dir,
		AllowDiskExport: true,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	ctx := context.Background()
	execCtx := WithWorkspaceMutationOrigin(ctx, WorkspaceMutationOriginCommandExecution)
	fa := svfs.NewGlobalFileAccess(false)

	durablePath := filepath.Join(dir, "app.py")
	transientPath := filepath.Join(dir, "__pycache__", "app.cpython-312.pyc")
	if err := fa.WriteFile(ctx, durablePath, []byte("print('ok')")); err != nil {
		t.Fatalf("WriteFile durable: %v", err)
	}
	if err := fa.WriteFile(execCtx, transientPath, []byte("bytecode")); err != nil {
		t.Fatalf("WriteFile transient: %v", err)
	}

	if pending := svfs.DiskFlusher().PendingChanges(); len(pending) != 1 {
		t.Fatalf("PendingChanges len = %d, want 1 durable change", len(pending))
	}

	if _, err := svfs.DiskFlusher().Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	content, err := os.ReadFile(durablePath)
	if err != nil {
		t.Fatalf("ReadFile durable: %v", err)
	}
	if got := string(content); got != "print('ok')" {
		t.Fatalf("durable disk content = %q, want %q", got, "print('ok')")
	}
	if _, err := os.Stat(transientPath); !os.IsNotExist(err) {
		t.Fatalf("transient path should remain off-disk, stat err=%v", err)
	}

	overlay, err := svfs.GlobalVFS().Read(ctx, transientPath)
	if err != nil {
		t.Fatalf("GlobalVFS.Read transient: %v", err)
	}
	if got := string(overlay); got != "bytecode" {
		t.Fatalf("transient overlay content = %q, want %q", got, "bytecode")
	}
	if pending := svfs.DiskFlusher().PendingChanges(); len(pending) != 0 {
		t.Fatalf("PendingChanges after flush len = %d, want 0 durable changes", len(pending))
	}
}

func TestDiskFlusher_FlushTreatsGitIgnoredExecutionArtifactsAsEphemeral(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("build/\n"), 0644); err != nil {
		t.Fatalf("WriteFile .gitignore: %v", err)
	}

	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:       "test-session",
		WorkingDir:      dir,
		AllowDiskExport: true,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	ctx := context.Background()
	execCtx := WithWorkspaceMutationOrigin(ctx, WorkspaceMutationOriginCommandExecution)
	target := filepath.Join(dir, "build", "runtime.txt")
	if err := svfs.NewGlobalFileAccess(false).WriteFile(execCtx, target, []byte("temp")); err != nil {
		t.Fatalf("WriteFile transient gitignored: %v", err)
	}

	if pending := svfs.DiskFlusher().PendingChanges(); len(pending) != 0 {
		t.Fatalf("PendingChanges len = %d, want 0 for gitignored execution artifact", len(pending))
	}
	if _, err := svfs.DiskFlusher().Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("gitignored execution artifact should remain off-disk, stat err=%v", err)
	}
	if overlay, err := svfs.GlobalVFS().Read(ctx, target); err != nil {
		t.Fatalf("GlobalVFS.Read gitignored transient: %v", err)
	} else if got := string(overlay); got != "temp" {
		t.Fatalf("gitignored transient overlay content = %q, want %q", got, "temp")
	}
}

func TestDiskFlusher_FlushPersistsExplicitWriteToIgnoredPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("build/\n"), 0644); err != nil {
		t.Fatalf("WriteFile .gitignore: %v", err)
	}

	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:       "test-session",
		WorkingDir:      dir,
		AllowDiskExport: true,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	ctx := context.Background()
	target := filepath.Join(dir, "build", "keep.txt")
	if err := svfs.NewGlobalFileAccess(false).WriteFile(ctx, target, []byte("persist")); err != nil {
		t.Fatalf("WriteFile explicit: %v", err)
	}

	if pending := svfs.DiskFlusher().PendingChanges(); len(pending) != 1 {
		t.Fatalf("PendingChanges len = %d, want 1 explicit durable change", len(pending))
	}
	if _, err := svfs.DiskFlusher().Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile explicit: %v", err)
	}
	if got := string(content); got != "persist" {
		t.Fatalf("explicit ignored-path content = %q, want %q", got, "persist")
	}
}

type failingCheckpointWAL struct {
	SemanticWAL
}

func (w failingCheckpointWAL) AppendCheckpoint([]WALFileDelta) (SemanticVersion, error) {
	return SemanticVersion{}, errors.New("checkpoint unavailable")
}
