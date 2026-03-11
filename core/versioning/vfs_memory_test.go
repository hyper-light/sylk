package versioning

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/memorybudget"
)

func TestPipelineVFSWriteFailsWhenOverlayBudgetExceeded(t *testing.T) {
	dir := t.TempDir()
	vfs := NewPipelineVFS(VFSConfig{
		PipelineID: "pipe-1",
		SessionID:  "sess-1",
		WorkingDir: dir,
	}, nil, nil)
	governor := memorybudget.NewGovernor(32, 32, 4, 32)
	reservation, err := governor.Reserve(memorybudget.ScopeOverlay, 0)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	vfs.memory = reservation

	err = vfs.Write(context.Background(), filepath.Join(dir, "main.go"), []byte("package main"))
	if !errors.Is(err, ErrMemoryBudgetExceeded) {
		t.Fatalf("Write error = %v, want %v", err, ErrMemoryBudgetExceeded)
	}
}

func TestSessionVFSBeginPipelineReadsGlobalOverlayWithoutSeeding(t *testing.T) {
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

	target := filepath.Join(dir, "pkg", "hello.py")
	if err := svfs.GlobalVFS().Write(context.Background(), target, []byte("print('overlay')")); err != nil {
		t.Fatalf("global write: %v", err)
	}
	pipeline, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "pipe-1",
		SessionID:  "test-session",
		WorkingDir: dir,
		Files:      []string{target},
	})
	if err != nil {
		t.Fatalf("BeginPipeline: %v", err)
	}
	if len(pipeline.workingContent) != 0 {
		t.Fatalf("workingContent = %d, want 0 eager seed entries", len(pipeline.workingContent))
	}
	content, err := pipeline.Read(context.Background(), target)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(content) != "print('overlay')" {
		t.Fatalf("content = %q, want overlay snapshot", string(content))
	}
}
