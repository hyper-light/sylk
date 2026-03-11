package versioning

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/skills"
)

func TestPreparePipelineWriteContextSkillReturnsBasisAndDiffs(t *testing.T) {
	dir := t.TempDir()
	if err := NewDiskFileAccess(dir, false).WriteFile(context.Background(), "hello.txt", []byte("disk")); err != nil {
		t.Fatalf("seed disk: %v", err)
	}

	svfs, views, ctx := newWorkspaceWriteSkillHarness(t, dir)
	defer svfs.Close()

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

	skill := NewPreparePipelineWriteContextSkill(
		func() WorkspaceViewAccess { return views },
		func() string { return "task-1" },
		NewMyersDiffer(1),
	)
	input, _ := json.Marshal(map[string]any{"path": "hello.txt"})
	result, err := skill.Handler(ctx, input)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	data := result.(PreparedWorkspaceWriteContext)
	if data.Basis.Scope != WorkspaceWriteScopePipeline {
		t.Fatalf("basis scope = %q, want %q", data.Basis.Scope, WorkspaceWriteScopePipeline)
	}
	if data.Basis.TargetView != WorkspaceViewPipeline {
		t.Fatalf("target view = %q, want %q", data.Basis.TargetView, WorkspaceViewPipeline)
	}
	if len(data.RelevantDiffs) != 3 {
		t.Fatalf("relevant diffs len = %d, want 3", len(data.RelevantDiffs))
	}
	if !hasWorkspaceDiff(data.RelevantDiffs, WorkspaceViewDisk, WorkspaceViewPipeline) {
		t.Fatal("expected disk->pipeline diff")
	}
	if !hasWorkspaceDiff(data.RelevantDiffs, WorkspaceViewGlobal, WorkspaceViewPipeline) {
		t.Fatal("expected global->pipeline diff")
	}
}

func TestWritePipelineFileSkillRejectsStaleBasis(t *testing.T) {
	dir := t.TempDir()
	if err := NewDiskFileAccess(dir, false).WriteFile(context.Background(), "hello.txt", []byte("disk")); err != nil {
		t.Fatalf("seed disk: %v", err)
	}

	svfs, views, ctx := newWorkspaceWriteSkillHarness(t, dir)
	defer svfs.Close()

	pipe, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  "sess-1",
		WorkingDir: dir,
		Files:      []string{"hello.txt"},
	})
	if err != nil {
		t.Fatalf("begin pipeline: %v", err)
	}

	prepare := NewPreparePipelineWriteContextSkill(
		func() WorkspaceViewAccess { return views },
		func() string { return "task-1" },
		nil,
	)
	prepared := invokePreparedWriteContext(t, ctx, prepare, `{"path":"hello.txt"}`)

	if err := pipe.Write(ctx, filepath.Join(dir, "hello.txt"), []byte("changed")); err != nil {
		t.Fatalf("mutate pipeline after prepare: %v", err)
	}

	writeSkill := NewWritePipelineFileSkill(WorkspaceWriteSkillConfig{
		GetFileAccess:     func() FileAccess { return svfs.NewPipelineFileAccess(pipe) },
		GetViews:          func() WorkspaceViewAccess { return views },
		DefaultPipelineID: func() string { return "task-1" },
	})
	input, _ := json.Marshal(map[string]any{
		"path":    "hello.txt",
		"content": "next",
		"basis":   prepared.Basis,
	})
	_, err = writeSkill.Handler(ctx, input)
	if err == nil {
		t.Fatal("expected stale basis error")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Fatalf("error = %v, want stale basis error", err)
	}
}

func TestWritePipelineFileSkillAcceptsFreshBasis(t *testing.T) {
	dir := t.TempDir()
	if err := NewDiskFileAccess(dir, false).WriteFile(context.Background(), "hello.txt", []byte("disk")); err != nil {
		t.Fatalf("seed disk: %v", err)
	}

	svfs, views, ctx := newWorkspaceWriteSkillHarness(t, dir)
	defer svfs.Close()

	pipe, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  "sess-1",
		WorkingDir: dir,
		Files:      []string{"hello.txt"},
	})
	if err != nil {
		t.Fatalf("begin pipeline: %v", err)
	}
	prepare := NewPreparePipelineWriteContextSkill(
		func() WorkspaceViewAccess { return views },
		func() string { return "task-1" },
		nil,
	)
	prepared := invokePreparedWriteContext(t, ctx, prepare, `{"path":"hello.txt"}`)
	writeSkill := NewWritePipelineFileSkill(WorkspaceWriteSkillConfig{
		GetFileAccess:     func() FileAccess { return svfs.NewPipelineFileAccess(pipe) },
		GetViews:          func() WorkspaceViewAccess { return views },
		DefaultPipelineID: func() string { return "task-1" },
	})
	input, _ := json.Marshal(map[string]any{
		"path":    "hello.txt",
		"content": "pipeline",
		"basis":   prepared.Basis,
	})
	if _, err := writeSkill.Handler(ctx, input); err != nil {
		t.Fatalf("Handler: %v", err)
	}

	content, err := pipe.Read(ctx, filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := string(content); got != "pipeline" {
		t.Fatalf("content = %q, want %q", got, "pipeline")
	}
}

func TestListGlobalChangesSkillReturnsOverlayChanges(t *testing.T) {
	dir := t.TempDir()
	if err := NewDiskFileAccess(dir, false).WriteFile(context.Background(), "hello.txt", []byte("disk")); err != nil {
		t.Fatalf("seed disk: %v", err)
	}

	svfs, _, ctx := newWorkspaceWriteSkillHarness(t, dir)
	defer svfs.Close()

	fa := svfs.NewGlobalFileAccess(false)
	if err := fa.WriteFile(ctx, "hello.txt", []byte("global")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	skill := NewListGlobalChangesSkill(func() FileAccess { return fa })
	result, err := skill.Handler(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	data := result.(*WorkspaceChangeSummary)
	if data.Count != 1 {
		t.Fatalf("count = %d, want 1", data.Count)
	}
	if data.Changes[0].Operation != "modify" {
		t.Fatalf("operation = %q, want %q", data.Changes[0].Operation, "modify")
	}
}

func TestDiffWorkspaceFileSkillReturnsRenderedDiff(t *testing.T) {
	dir := t.TempDir()
	if err := NewDiskFileAccess(dir, false).WriteFile(context.Background(), "hello.txt", []byte("disk")); err != nil {
		t.Fatalf("seed disk: %v", err)
	}

	svfs, views, ctx := newWorkspaceWriteSkillHarness(t, dir)
	defer svfs.Close()

	if err := svfs.NewGlobalFileAccess(false).WriteFile(ctx, "hello.txt", []byte("global")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	skill := NewDiffWorkspaceFileSkill(func() WorkspaceViewAccess { return views }, nil, NewMyersDiffer(1))
	input, _ := json.Marshal(map[string]any{
		"path":        "hello.txt",
		"base_view":   "disk",
		"target_view": "global",
	})
	result, err := skill.Handler(ctx, input)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	diff := result.(WorkspaceFileDiff)
	if diff.Identical {
		t.Fatal("expected non-identical diff")
	}
	if !strings.Contains(diff.Rendered, "--- disk:hello.txt") {
		t.Fatalf("rendered diff missing disk header: %q", diff.Rendered)
	}
}

func newWorkspaceWriteSkillHarness(t *testing.T, dir string) (*SessionVFS, *SessionWorkspaceViews, context.Context) {
	t.Helper()
	svfs, err := NewSessionVFS(SessionVFSConfig{
		SessionID:  "sess-1",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("NewSessionVFS: %v", err)
	}
	views := NewSessionWorkspaceViews(SessionWorkspaceViewsConfig{
		DefaultView:       WorkspaceViewPipeline,
		DefaultPipelineID: "task-1",
		Session:           svfs,
		WorkingDir:        dir,
		DiskFallback:      NewDiskFileAccess(dir, true),
	})
	return svfs, views, WithSessionID(context.Background(), "sess-1")
}

func invokePreparedWriteContext(t *testing.T, ctx context.Context, skill *skills.Skill, input string) PreparedWorkspaceWriteContext {
	t.Helper()
	result, err := skill.Handler(ctx, json.RawMessage(input))
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	return result.(PreparedWorkspaceWriteContext)
}

func hasWorkspaceDiff(diffs []WorkspaceFileDiff, base, target WorkspaceView) bool {
	for _, diff := range diffs {
		if diff.BaseView == base && diff.TargetView == target {
			return true
		}
	}
	return false
}
