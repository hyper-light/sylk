package orchestrator

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/versioning"
)

func TestCommitTaskDraft_MergesTaskPipelineIntoGlobalDraft(t *testing.T) {
	dir := t.TempDir()
	svfs, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	pipe, err := svfs.BeginPipeline(versioning.BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  "sess-1",
		WorkingDir: dir,
		Files:      []string{"hello.txt"},
	})
	if err != nil {
		t.Fatalf("BeginPipeline: %v", err)
	}
	target := filepath.Join(dir, "hello.txt")
	if err := pipe.Write(context.Background(), target, []byte("pipeline")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	o := &Orchestrator{sessionVFS: make(map[string]*versioning.SessionVFS)}
	o.SetSessionVFS("sess-1", svfs)

	task := &TaskRecord{ID: "task-1", SessionID: "sess-1"}
	version, hadDraft, err := o.commitTaskDraft(context.Background(), task)
	if err != nil {
		t.Fatalf("commitTaskDraft: %v", err)
	}
	if !hadDraft {
		t.Fatal("expected tracked draft to be merged")
	}
	if version.IsZero() {
		t.Fatal("expected non-zero global VFS version after merge")
	}

	content, err := svfs.GlobalVFS().Read(context.Background(), target)
	if err != nil {
		t.Fatalf("GlobalVFS.Read: %v", err)
	}
	if string(content) != "pipeline" {
		t.Fatalf("content = %q, want %q", string(content), "pipeline")
	}
	if svfs.HasPipeline("task-1") {
		t.Fatal("expected pipeline draft to be removed after commit")
	}
}
