package versioning

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMergePipe_SinglePipeline(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenVersionedWAL(VersionedWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("OpenVersionedWAL: %v", err)
	}
	defer wal.Close()

	globalVFS := NewPipelineVFS(VFSConfig{
		PipelineID: "global",
		WorkingDir: dir,
	}, nil, nil)
	defer globalVFS.Close()

	mp := NewMergePipe(MergePipeConfig{
		GlobalVFS: globalVFS,
		WAL:       wal,
		OTEngine:  NewOTEngine(),
	})
	mp.Start()
	defer mp.Stop()

	if err := mp.RegisterPipeline("pipe1"); err != nil {
		t.Fatalf("RegisterPipeline: %v", err)
	}

	mods := []FileModification{
		{OriginalPath: dir + "/a.go", Operation: FileOpCreate, NewContent: []byte("package a")},
	}

	ver, err := mp.Merge(context.Background(), "pipe1", mods)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if ver.IsZero() {
		t.Error("expected non-zero version")
	}
	if ver.Minor != 1 {
		t.Errorf("Minor = %d, want 1", ver.Minor)
	}
}

func TestMergePipe_TwoPipelines(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenVersionedWAL(VersionedWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("OpenVersionedWAL: %v", err)
	}
	defer wal.Close()

	globalVFS := NewPipelineVFS(VFSConfig{
		PipelineID: "global",
		WorkingDir: dir,
	}, nil, nil)
	defer globalVFS.Close()

	mp := NewMergePipe(MergePipeConfig{
		GlobalVFS: globalVFS,
		WAL:       wal,
		OTEngine:  NewOTEngine(),
	})
	mp.Start()
	defer mp.Stop()

	mp.RegisterPipeline("pipe1")
	mp.RegisterPipeline("pipe2")

	v1, err := mp.Merge(context.Background(), "pipe1", []FileModification{
		{OriginalPath: dir + "/a.go", Operation: FileOpCreate, NewContent: []byte("from pipe1")},
	})
	if err != nil {
		t.Fatalf("Merge pipe1: %v", err)
	}

	v2, err := mp.Merge(context.Background(), "pipe2", []FileModification{
		{OriginalPath: dir + "/b.go", Operation: FileOpCreate, NewContent: []byte("from pipe2")},
	})
	if err != nil {
		t.Fatalf("Merge pipe2: %v", err)
	}

	if v2.Compare(v1) <= 0 {
		t.Errorf("v2 (%s) should be > v1 (%s)", v2, v1)
	}
}

func TestMergePipe_StopCleanup(t *testing.T) {
	dir := t.TempDir()
	wal, _ := OpenVersionedWAL(VersionedWALConfig{Dir: dir})
	defer wal.Close()

	globalVFS := NewPipelineVFS(VFSConfig{
		PipelineID: "global",
		WorkingDir: dir,
	}, nil, nil)
	defer globalVFS.Close()

	mp := NewMergePipe(MergePipeConfig{
		GlobalVFS: globalVFS,
		WAL:       wal,
		OTEngine:  NewOTEngine(),
	})
	mp.Start()
	mp.Stop()

	// After stop, Merge should fail.
	_, err := mp.Merge(context.Background(), "pipe1", nil)
	if err != ErrMergePipeClosed {
		t.Errorf("expected ErrMergePipeClosed, got %v", err)
	}
}

func TestMergePipe_ContextCancellation(t *testing.T) {
	dir := t.TempDir()
	wal, _ := OpenVersionedWAL(VersionedWALConfig{Dir: dir})
	defer wal.Close()

	globalVFS := NewPipelineVFS(VFSConfig{
		PipelineID: "global",
		WorkingDir: dir,
	}, nil, nil)
	defer globalVFS.Close()

	mp := NewMergePipe(MergePipeConfig{
		GlobalVFS:   globalVFS,
		WAL:         wal,
		OTEngine:    NewOTEngine(),
		MergeBuffer: 1,
	})
	// Don't start — channel will block.

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mp.RegisterPipeline("pipe1")
	_, err := mp.Merge(ctx, "pipe1", []FileModification{
		{OriginalPath: dir + "/a.go", Operation: FileOpCreate, NewContent: []byte("x")},
	})
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	mp.closed = true
	close(mp.stopCh)
	close(mp.doneCh)
}

func TestMergePipe_PreciseDisjointSameFileMerges(t *testing.T) {
	dir := t.TempDir()
	wal := NewMemoryVersionedWAL()
	globalVFS := NewGlobalVFS(VFSConfig{
		PipelineID: "global",
		WorkingDir: dir,
	})
	defer globalVFS.Close()

	ctx := context.Background()
	target := filepath.Join(dir, "a.go")
	if err := os.WriteFile(target, []byte("abcdef"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mp := NewMergePipe(MergePipeConfig{
		GlobalVFS: globalVFS,
		WAL:       wal,
		OTEngine:  NewOTEngine(),
	})
	mp.Start()
	defer mp.Stop()

	if err := mp.RegisterPipeline("pipe1"); err != nil {
		t.Fatalf("RegisterPipeline: %v", err)
	}
	if err := mp.RegisterPipeline("pipe2"); err != nil {
		t.Fatalf("RegisterPipeline: %v", err)
	}

	if _, err := mp.Merge(ctx, "pipe1", []FileModification{
		{
			OriginalPath: target,
			Operation:    FileOpModify,
			OldContent:   []byte("abcdef"),
			NewContent:   []byte("abQQcdef"),
		},
	}); err != nil {
		t.Fatalf("Merge pipe1: %v", err)
	}

	if _, err := mp.Merge(ctx, "pipe2", []FileModification{
		{
			OriginalPath: target,
			Operation:    FileOpModify,
			OldContent:   []byte("abcdef"),
			NewContent:   []byte("abcdeZZf"),
		},
	}); err != nil {
		t.Fatalf("Merge pipe2: %v", err)
	}

	content, err := globalVFS.Read(ctx, target)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(content) != "abQQcdeZZf" {
		t.Fatalf("content = %q, want %q", string(content), "abQQcdeZZf")
	}
}
