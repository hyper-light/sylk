package versioning

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSessionWorkspaceViews_ReadAcrossDiskGlobalAndPipeline(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hello.txt")
	if err := NewDiskFileAccess(dir, false).WriteFile(context.Background(), "hello.txt", []byte("disk")); err != nil {
		t.Fatalf("seed disk: %v", err)
	}

	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	ctx := WithSessionID(context.Background(), "sess-1")
	if err := svfs.GlobalVFS().Write(ctx, target, []byte("global")); err != nil {
		t.Fatalf("seed global: %v", err)
	}

	pipe, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  "sess-1",
		WorkingDir: dir,
		Files:      []string{"hello.txt"},
	})
	if err != nil {
		t.Fatalf("begin pipeline: %v", err)
	}
	if err := pipe.Write(ctx, target, []byte("pipeline")); err != nil {
		t.Fatalf("seed pipeline: %v", err)
	}

	views := NewSessionWorkspaceViews(SessionWorkspaceViewsConfig{
		DefaultView:       WorkspaceViewPipeline,
		DefaultPipelineID: "task-1",
		Session:           svfs,
		WorkingDir:        dir,
		DiskFallback:      NewDiskFileAccess(dir, true),
	})

	diskContent, err := views.ReadFile(ctx, WorkspaceViewDisk, "hello.txt", "")
	if err != nil {
		t.Fatalf("read disk: %v", err)
	}
	globalContent, err := views.ReadFile(ctx, WorkspaceViewGlobal, "hello.txt", "")
	if err != nil {
		t.Fatalf("read global: %v", err)
	}
	pipelineContent, err := views.ReadFile(ctx, WorkspaceViewPipeline, "hello.txt", "")
	if err != nil {
		t.Fatalf("read pipeline: %v", err)
	}

	if got := string(diskContent); got != "disk" {
		t.Fatalf("disk content = %q, want %q", got, "disk")
	}
	if got := string(globalContent); got != "global" {
		t.Fatalf("global content = %q, want %q", got, "global")
	}
	if got := string(pipelineContent); got != "pipeline" {
		t.Fatalf("pipeline content = %q, want %q", got, "pipeline")
	}
}

func TestSessionWorkspaceViews_InspectPathShowsLayerDifferences(t *testing.T) {
	dir := t.TempDir()
	if err := NewDiskFileAccess(dir, false).WriteFile(context.Background(), "hello.txt", []byte("disk")); err != nil {
		t.Fatalf("seed disk: %v", err)
	}
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	defer svfs.Close()

	ctx := WithSessionID(context.Background(), "sess-1")
	target := filepath.Join(dir, "hello.txt")
	if err := svfs.GlobalVFS().Write(ctx, target, []byte("global")); err != nil {
		t.Fatalf("seed global: %v", err)
	}
	pipe, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  "sess-1",
		WorkingDir: dir,
		Files:      []string{"hello.txt"},
	})
	if err != nil {
		t.Fatalf("begin pipeline: %v", err)
	}
	if err := pipe.Write(ctx, target, []byte("pipeline")); err != nil {
		t.Fatalf("seed pipeline: %v", err)
	}

	views := NewSessionWorkspaceViews(SessionWorkspaceViewsConfig{
		DefaultView:       WorkspaceViewPipeline,
		DefaultPipelineID: "task-1",
		Session:           svfs,
		WorkingDir:        dir,
		DiskFallback:      NewDiskFileAccess(dir, true),
	})

	state, err := views.InspectPath(ctx, "hello.txt", "")
	if err != nil {
		t.Fatalf("inspect path: %v", err)
	}
	if !state.GlobalDiffersFromDisk {
		t.Fatal("expected global to differ from disk")
	}
	if !state.GlobalDiffKnown {
		t.Fatal("expected global diff to be known")
	}
	if !state.PipelineDiffersFromDisk {
		t.Fatal("expected pipeline to differ from disk")
	}
	if !state.PipelineDiffFromDiskKnown {
		t.Fatal("expected pipeline diff from disk to be known")
	}
	if !state.PipelineDiffersFromGlobal {
		t.Fatal("expected pipeline to differ from global")
	}
	if !state.PipelineDiffFromGlobalKnown {
		t.Fatal("expected pipeline diff from global to be known")
	}
	if state.SourceOfTruth != WorkspaceViewDisk {
		t.Fatalf("source_of_truth = %q, want %q", state.SourceOfTruth, WorkspaceViewDisk)
	}
}

func TestSessionWorkspaceViews_InspectPathReportsUnavailableGlobalLayer(t *testing.T) {
	dir := t.TempDir()
	if err := NewDiskFileAccess(dir, false).WriteFile(context.Background(), "hello.txt", []byte("disk")); err != nil {
		t.Fatalf("seed disk: %v", err)
	}
	views := NewSessionWorkspaceViews(SessionWorkspaceViewsConfig{
		DefaultView:  WorkspaceViewDisk,
		WorkingDir:   dir,
		DiskFallback: NewDiskFileAccess(dir, true),
	})

	state, err := views.InspectPath(context.Background(), "hello.txt", "")
	if err != nil {
		t.Fatalf("inspect path: %v", err)
	}
	if state.Global == nil {
		t.Fatal("expected global layer state to be present even when unavailable")
	}
	if state.Global.Available {
		t.Fatal("expected global layer to be unavailable")
	}
	if state.Global.Error == "" {
		t.Fatal("expected unavailable global layer to carry an error")
	}
	if state.GlobalDiffKnown {
		t.Fatal("expected global diff to be unknown when global layer is unavailable")
	}
	if len(state.ViewsUnavailable) == 0 || state.ViewsUnavailable[0] != WorkspaceViewGlobal {
		t.Fatalf("expected global in unavailable views, got %v", state.ViewsUnavailable)
	}
}

func TestSessionWorkspaceViews_SummarizePathsCapturesChangedAndUnavailableViews(t *testing.T) {
	dir := t.TempDir()
	if err := NewDiskFileAccess(dir, false).WriteFile(context.Background(), "hello.txt", []byte("disk")); err != nil {
		t.Fatalf("seed disk: %v", err)
	}
	views := NewSessionWorkspaceViews(SessionWorkspaceViewsConfig{
		DefaultView:  WorkspaceViewDisk,
		WorkingDir:   dir,
		DiskFallback: NewDiskFileAccess(dir, true),
	})

	summary, err := views.SummarizePaths(context.Background(), []string{"hello.txt"}, "")
	if err != nil {
		t.Fatalf("summarize paths: %v", err)
	}
	if len(summary.Paths) != 1 || summary.Paths[0] != "hello.txt" {
		t.Fatalf("unexpected paths: %v", summary.Paths)
	}
	if len(summary.PathsWithUnavailableViews) != 1 || summary.PathsWithUnavailableViews[0] != "hello.txt" {
		t.Fatalf("expected unavailable view path, got %v", summary.PathsWithUnavailableViews)
	}
	if len(summary.ViewsUnavailable) == 0 || summary.ViewsUnavailable[0] != WorkspaceViewGlobal {
		t.Fatalf("expected global to be unavailable, got %v", summary.ViewsUnavailable)
	}
}
