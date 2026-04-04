package versioning

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNewSessionVFS(t *testing.T) {
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
	if _, ok := svfs.WAL().(*MemoryVersionedWAL); !ok {
		t.Fatalf("expected in-memory semantic WAL, got %T", svfs.WAL())
	}
	if _, err := os.Stat(filepath.Join(dir, ".sylk", "sessions", "test-session", "versioning", "wal")); !os.IsNotExist(err) {
		t.Fatalf("expected no on-disk semantic WAL root, stat err=%v", err)
	}
}

func TestSessionVFS_BeginPipelineSeedsFromGlobalOverlay(t *testing.T) {
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
	target := filepath.Join(dir, "pkg", "hello.py")
	if err := svfs.GlobalVFS().Write(ctx, target, []byte("print('overlay')")); err != nil {
		t.Fatalf("global write: %v", err)
	}

	pVFS, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "pipe1",
		SessionID:  "test-session",
		WorkingDir: dir,
		Files:      []string{target},
	})
	if err != nil {
		t.Fatalf("BeginPipeline: %v", err)
	}

	content, err := pVFS.Read(ctx, target)
	if err != nil {
		t.Fatalf("pipeline read: %v", err)
	}
	if string(content) != "print('overlay')" {
		t.Fatalf("pipeline content = %q, want overlay snapshot", string(content))
	}
}

func TestSessionVFS_StatsReflectLiveState(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	ctx := context.Background()
	target := filepath.Join(dir, "hello.txt")
	if err := svfs.GlobalVFS().Write(ctx, target, []byte("hello")); err != nil {
		t.Fatalf("global write: %v", err)
	}
	if _, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "pipe1",
		SessionID:  "test-session",
		WorkingDir: dir,
		Files:      []string{target},
	}); err != nil {
		t.Fatalf("BeginPipeline: %v", err)
	}

	stats := svfs.Stats()
	if stats.ActivePipelines != 1 {
		t.Fatalf("ActivePipelines = %d, want 1", stats.ActivePipelines)
	}
	if stats.TrackedFiles < 1 {
		t.Fatalf("TrackedFiles = %d, want >= 1", stats.TrackedFiles)
	}
}

func TestSessionVFS_NewDiskFileAccess(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
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

func TestSessionVFS_NewDiskFileAccessReadsLiveDiskState(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	target := filepath.Join(dir, "live.txt")
	if err := os.WriteFile(target, []byte("live"), 0644); err != nil {
		t.Fatalf("write live disk file: %v", err)
	}

	content, err := svfs.NewDiskFileAccess(true).ReadFile(context.Background(), "live.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := string(content); got != "live" {
		t.Fatalf("disk content = %q, want %q", got, "live")
	}
}

func TestSessionVFS_NewGlobalFileAccess(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	fa := svfs.NewGlobalFileAccess(false)
	if fa == nil {
		t.Fatal("GlobalVFSFileAccess should not be nil")
	}
	if fa.IsReadOnly() {
		t.Fatal("expected writable")
	}
}

func TestSessionVFS_GlobalDraftWritesAdvanceWAL(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	fa := svfs.NewGlobalFileAccess(false)
	target := filepath.Join(dir, "draft.txt")
	if err := fa.WriteFile(context.Background(), target, []byte("draft")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if ver := svfs.CurrentVersion(); ver.Minor != 1 {
		t.Fatalf("CurrentVersion minor = %d, want 1", ver.Minor)
	}

	content, err := svfs.GlobalVFS().Read(context.Background(), target)
	if err != nil {
		t.Fatalf("GlobalVFS.Read: %v", err)
	}
	if string(content) != "draft" {
		t.Fatalf("content = %q, want %q", string(content), "draft")
	}
}

func TestSessionVFS_ReviewCandidateStaysOutOfCheckpointUntilAccepted(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(target, []byte("disk"), 0644); err != nil {
		t.Fatalf("seed disk: %v", err)
	}

	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	ctx := context.Background()
	pipe, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  "test-session",
		WorkingDir: dir,
		Files:      []string{"hello.txt"},
	})
	if err != nil {
		t.Fatalf("BeginPipeline: %v", err)
	}
	if err := pipe.Write(ctx, target, []byte("candidate")); err != nil {
		t.Fatalf("pipeline write: %v", err)
	}

	candidate, err := svfs.ExtractReviewCandidate("task-1")
	if err != nil {
		t.Fatalf("ExtractReviewCandidate: %v", err)
	}
	if candidate == nil {
		t.Fatal("expected extracted review candidate")
	}
	if svfs.HasPipeline("task-1") {
		t.Fatal("expected pipeline to be closed after extraction")
	}

	globalContent, err := svfs.NewGlobalFileAccess(true).ReadFile(ctx, "hello.txt")
	if err != nil {
		t.Fatalf("global read before accept: %v", err)
	}
	if got := string(globalContent); got != "disk" {
		t.Fatalf("global content before accept = %q, want %q", got, "disk")
	}

	if err := svfs.ActivateReviewCandidate(ctx, candidate.ID); err != nil {
		t.Fatalf("ActivateReviewCandidate: %v", err)
	}
	reviewContent, err := svfs.NewReviewFileAccess(true).ReadFile(ctx, "hello.txt")
	if err != nil {
		t.Fatalf("review read: %v", err)
	}
	if got := string(reviewContent); got != "candidate" {
		t.Fatalf("review content = %q, want %q", got, "candidate")
	}

	if _, changed, err := svfs.AcceptActiveReviewCandidate(ctx, "task-1"); err != nil {
		t.Fatalf("AcceptActiveReviewCandidate: %v", err)
	} else if !changed {
		t.Fatal("expected accept to promote the candidate")
	}

	globalContent, err = svfs.NewGlobalFileAccess(true).ReadFile(ctx, "hello.txt")
	if err != nil {
		t.Fatalf("global read after accept: %v", err)
	}
	if got := string(globalContent); got != "candidate" {
		t.Fatalf("global content after accept = %q, want %q", got, "candidate")
	}
}

func TestSessionVFS_FlushCommitsDraftAndSeedsNextPipelineFromDisk(t *testing.T) {
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
	target := filepath.Join(dir, "pkg", "state.txt")
	if err := svfs.NewGlobalFileAccess(false).WriteFile(ctx, target, []byte("draft-1")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := svfs.DiskFlusher().Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	onDisk, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(onDisk) != "draft-1" {
		t.Fatalf("disk content = %q, want %q", string(onDisk), "draft-1")
	}

	if mods := svfs.GlobalVFS().GetModifications(); len(mods) != 0 {
		t.Fatalf("global overlay retained %d modifications after flush", len(mods))
	}

	pVFS, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "pipe-after-flush",
		SessionID:  "test-session",
		WorkingDir: dir,
		Files:      []string{target},
	})
	if err != nil {
		t.Fatalf("BeginPipeline: %v", err)
	}

	content, err := pVFS.Read(ctx, target)
	if err != nil {
		t.Fatalf("pipeline read: %v", err)
	}
	if string(content) != "draft-1" {
		t.Fatalf("pipeline content = %q, want %q", string(content), "draft-1")
	}
}

func TestSessionVFS_DoesNotReplayGlobalDraftAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	cfg := SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	}

	svfs, err := NewSessionVFS(cfg)
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}

	ctx := context.Background()
	targetDir := filepath.Join(dir, "draft", "generated")
	if err := svfs.NewGlobalFileAccess(false).MkdirAll(ctx, targetDir); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := svfs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		t.Fatalf("expected draft directory to remain off-disk before flush, stat err=%v", err)
	}

	reopened, err := NewSessionVFS(cfg)
	if err != nil {
		t.Fatalf("reopen NewSessionVFS: %v", err)
	}
	defer reopened.Close()

	_, err = reopened.NewGlobalFileAccess(false).Stat(ctx, targetDir)
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("Stat after reopen error = %v, want %v", err, ErrFileNotFound)
	}
	exists, err := reopened.NewGlobalFileAccess(false).Exists(ctx, targetDir)
	if err != nil {
		t.Fatalf("Exists after reopen: %v", err)
	}
	if exists {
		t.Fatal("expected global draft directory to disappear after reopen in strict RAM mode")
	}
}

func dirEntryNamesFS(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestNewSessionVFS_RejectsPersistentSessionState(t *testing.T) {
	dir := t.TempDir()
	_, err := NewSessionVFS(SessionVFSConfig{
		SessionID:           "test-session",
		WorkingDir:          dir,
		PersistSessionState: true,
	})
	if !errors.Is(err, ErrPersistentSessionStateDisabled) {
		t.Fatalf("NewSessionVFS error = %v, want %v", err, ErrPersistentSessionStateDisabled)
	}
}

func TestSessionVFS_BeginAndCommitPipeline(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
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
	ver, err := svfs.CommitPipeline(ctx, "pipe1")
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

func TestSessionVFS_CommitPipelineSkipsTransientExecutionArtifacts(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	pipe, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "pipe1",
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("BeginPipeline: %v", err)
	}

	fa := svfs.NewPipelineFileAccess(pipe)
	ctx := context.Background()
	execCtx := WithWorkspaceMutationOrigin(ctx, WorkspaceMutationOriginCommandExecution)
	durablePath := filepath.Join(dir, "pkg", "main.py")
	transientPath := filepath.Join(dir, "pkg", "__pycache__", "main.cpython-312.pyc")

	if err := fa.WriteFile(ctx, durablePath, []byte("print('ok')")); err != nil {
		t.Fatalf("WriteFile durable: %v", err)
	}
	if err := fa.WriteFile(execCtx, transientPath, []byte("bytecode")); err != nil {
		t.Fatalf("WriteFile transient: %v", err)
	}

	ver, err := svfs.CommitPipeline(ctx, "pipe1")
	if err != nil {
		t.Fatalf("CommitPipeline: %v", err)
	}
	if ver.IsZero() {
		t.Fatal("expected durable pipeline write to advance version")
	}

	content, err := svfs.GlobalVFS().Read(ctx, durablePath)
	if err != nil {
		t.Fatalf("GlobalVFS.Read durable: %v", err)
	}
	if got := string(content); got != "print('ok')" {
		t.Fatalf("durable content = %q, want %q", got, "print('ok')")
	}
	if _, err := svfs.GlobalVFS().Read(ctx, transientPath); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("GlobalVFS.Read transient error = %v, want %v", err, ErrFileNotFound)
	}
}

func TestSessionVFS_RollbackPipeline(t *testing.T) {
	dir := t.TempDir()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	_, err = svfs.BeginPipeline(BeginPipelineConfig{
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
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "test-session",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}

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
