package versioning

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if data.Basis.LeaseExpiresAt.IsZero() {
		t.Fatal("expected leased write basis")
	}
	if !hasWorkspaceDiff(data.RelevantDiffs, WorkspaceViewDisk, WorkspaceViewPipeline) {
		t.Fatal("expected disk->pipeline diff")
	}
	if !hasWorkspaceDiff(data.RelevantDiffs, WorkspaceViewGlobal, WorkspaceViewPipeline) {
		t.Fatal("expected global->pipeline diff")
	}
}

func TestWritePipelineFileSkillAutoRefreshesStaleBasis(t *testing.T) {
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
	result, err := writeSkill.Handler(ctx, input)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	payload := result.(map[string]any)
	repairRaw, ok := payload["basis_repair"]
	if !ok {
		t.Fatal("expected basis_repair after stale auto-refresh")
	}
	repair, ok := repairRaw.(*WorkspaceWriteRepair)
	if !ok {
		t.Fatalf("basis_repair type = %T, want *WorkspaceWriteRepair", repairRaw)
	}
	if !repair.Refreshed {
		t.Fatalf("expected basis repair to refresh stale state, got %+v", repair)
	}

	content, err := pipe.Read(ctx, filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := string(content); got != "next" {
		t.Fatalf("content = %q, want %q", got, "next")
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
	result, err := writeSkill.Handler(ctx, input)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	payload := result.(map[string]any)
	nextBasisRaw, ok := payload["next_basis"]
	if !ok {
		t.Fatal("expected refreshed next_basis in write result")
	}
	nextBasis, ok := nextBasisRaw.(*WorkspaceWriteBasis)
	if !ok {
		t.Fatalf("next_basis type = %T, want *WorkspaceWriteBasis", nextBasisRaw)
	}
	if nextBasis.LeaseExpiresAt.IsZero() {
		t.Fatal("expected refreshed write lease on next_basis")
	}
	if nextBasis.Path != "hello.txt" {
		t.Fatalf("next_basis path = %q, want %q", nextBasis.Path, "hello.txt")
	}

	content, err := pipe.Read(ctx, filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := string(content); got != "pipeline" {
		t.Fatalf("content = %q, want %q", got, "pipeline")
	}
}

func TestCreatePipelineDirectorySkillRebindsChildFileBasis(t *testing.T) {
	dir := t.TempDir()
	svfs, views, ctx := newWorkspaceWriteSkillHarness(t, dir)
	defer svfs.Close()

	pipe, err := svfs.BeginPipeline(BeginPipelineConfig{
		PipelineID: "task-1",
		SessionID:  "sess-1",
		WorkingDir: dir,
		Files:      []string{"src/hello_world/__init__.py"},
	})
	if err != nil {
		t.Fatalf("begin pipeline: %v", err)
	}

	prepare := NewPreparePipelineWriteContextSkill(
		func() WorkspaceViewAccess { return views },
		func() string { return "task-1" },
		nil,
	)
	prepared := invokePreparedWriteContext(t, ctx, prepare, `{"path":"src/hello_world/__init__.py"}`)

	createSkill := NewCreatePipelineDirectorySkill(WorkspaceWriteSkillConfig{
		GetFileAccess:     func() FileAccess { return svfs.NewPipelineFileAccess(pipe) },
		GetViews:          func() WorkspaceViewAccess { return views },
		DefaultPipelineID: func() string { return "task-1" },
	})
	input, _ := json.Marshal(map[string]any{
		"path":  "src/hello_world",
		"basis": prepared.Basis,
	})
	result, err := createSkill.Handler(ctx, input)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	payload := result.(map[string]any)
	repairRaw, ok := payload["basis_repair"]
	if !ok {
		t.Fatal("expected basis_repair after rebinding directory basis")
	}
	repair, ok := repairRaw.(*WorkspaceWriteRepair)
	if !ok {
		t.Fatalf("basis_repair type = %T, want *WorkspaceWriteRepair", repairRaw)
	}
	if !repair.Rebound || !repair.Refreshed {
		t.Fatalf("expected rebound + refreshed repair, got %+v", repair)
	}
	nextBasisRaw, ok := payload["next_basis"]
	if !ok {
		t.Fatal("expected next_basis for created directory")
	}
	nextBasis, ok := nextBasisRaw.(*WorkspaceWriteBasis)
	if !ok {
		t.Fatalf("next_basis type = %T, want *WorkspaceWriteBasis", nextBasisRaw)
	}
	if nextBasis.Path != "src/hello_world" {
		t.Fatalf("next_basis path = %q, want %q", nextBasis.Path, "src/hello_world")
	}
	if nextBasis.Pipeline == nil || !nextBasis.Pipeline.IsDir {
		t.Fatalf("expected directory basis after create, got %+v", nextBasis.Pipeline)
	}

	secondInput, _ := json.Marshal(map[string]any{
		"path":  "src/hello_world",
		"basis": nextBasis,
	})
	if _, err := createSkill.Handler(ctx, secondInput); err != nil {
		t.Fatalf("second Handler: %v", err)
	}
}

func TestValidateWorkspaceWriteBasis_AllowsReferenceAvailabilityChurnWithinLease(t *testing.T) {
	basis := WorkspaceWriteBasis{
		Scope:          WorkspaceWriteScopePipeline,
		Path:           "hello.txt",
		PipelineID:     "task-1",
		TargetView:     WorkspaceViewPipeline,
		PreparedAt:     time.Now().UTC(),
		LeaseExpiresAt: time.Now().UTC().Add(defaultWorkspaceWriteLease),
		Disk: WorkspaceLayerState{
			View:        WorkspaceViewDisk,
			Available:   true,
			Exists:      true,
			ContentHash: "disk-hash",
		},
		Global: &WorkspaceLayerState{
			View:        WorkspaceViewGlobal,
			Available:   true,
			Exists:      true,
			ContentHash: "global-hash",
		},
		Pipeline: &WorkspaceLayerState{
			View:        WorkspaceViewPipeline,
			Available:   true,
			Exists:      true,
			ContentHash: "pipeline-hash",
		},
	}
	views := stubWorkspaceViewAccess{
		state: &WorkspacePathState{
			Path:       "hello.txt",
			PipelineID: "task-1",
			Disk: WorkspaceLayerState{
				View:      WorkspaceViewDisk,
				Available: false,
				Error:     "disk warming",
			},
			Global: &WorkspaceLayerState{
				View:        WorkspaceViewGlobal,
				Available:   true,
				Exists:      true,
				ContentHash: "global-hash",
			},
			Pipeline: &WorkspaceLayerState{
				View:        WorkspaceViewPipeline,
				Available:   true,
				Exists:      true,
				ContentHash: "pipeline-hash",
			},
		},
	}

	if err := ValidateWorkspaceWriteBasis(context.Background(), views, WorkspaceWriteScopePipeline, "hello.txt", basis); err != nil {
		t.Fatalf("ValidateWorkspaceWriteBasis: %v", err)
	}
}

func TestValidateWorkspaceWriteBasis_RejectsReferenceAvailabilityChurnAfterLeaseExpiry(t *testing.T) {
	basis := WorkspaceWriteBasis{
		Scope:          WorkspaceWriteScopePipeline,
		Path:           "hello.txt",
		PipelineID:     "task-1",
		TargetView:     WorkspaceViewPipeline,
		PreparedAt:     time.Now().UTC().Add(-2 * defaultWorkspaceWriteLease),
		LeaseExpiresAt: time.Now().UTC().Add(-time.Second),
		Disk: WorkspaceLayerState{
			View:        WorkspaceViewDisk,
			Available:   true,
			Exists:      true,
			ContentHash: "disk-hash",
		},
		Global: &WorkspaceLayerState{
			View:        WorkspaceViewGlobal,
			Available:   true,
			Exists:      true,
			ContentHash: "global-hash",
		},
		Pipeline: &WorkspaceLayerState{
			View:        WorkspaceViewPipeline,
			Available:   true,
			Exists:      true,
			ContentHash: "pipeline-hash",
		},
	}
	views := stubWorkspaceViewAccess{
		state: &WorkspacePathState{
			Path:       "hello.txt",
			PipelineID: "task-1",
			Disk: WorkspaceLayerState{
				View:      WorkspaceViewDisk,
				Available: false,
				Error:     "disk warming",
			},
			Global: &WorkspaceLayerState{
				View:        WorkspaceViewGlobal,
				Available:   true,
				Exists:      true,
				ContentHash: "global-hash",
			},
			Pipeline: &WorkspaceLayerState{
				View:        WorkspaceViewPipeline,
				Available:   true,
				Exists:      true,
				ContentHash: "pipeline-hash",
			},
		},
	}

	err := ValidateWorkspaceWriteBasis(context.Background(), views, WorkspaceWriteScopePipeline, "hello.txt", basis)
	if err == nil {
		t.Fatal("expected stale basis error after lease expiry")
	}
	if !strings.Contains(err.Error(), "lease expired") {
		t.Fatalf("error = %v, want lease expiry", err)
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

type stubWorkspaceViewAccess struct {
	state *WorkspacePathState
	err   error
}

func (s stubWorkspaceViewAccess) ReadFile(context.Context, WorkspaceView, string, string) ([]byte, error) {
	return nil, ErrFileNotFound
}

func (s stubWorkspaceViewAccess) Glob(context.Context, WorkspaceView, string, string, []string, string) ([]string, error) {
	return nil, nil
}

func (s stubWorkspaceViewAccess) Grep(context.Context, WorkspaceView, string, string, string, int, int, string) ([]GrepMatch, error) {
	return nil, nil
}

func (s stubWorkspaceViewAccess) InspectPath(context.Context, string, string) (*WorkspacePathState, error) {
	return s.state, s.err
}

func (s stubWorkspaceViewAccess) SummarizePaths(context.Context, []string, string) (*WorkspaceSummary, error) {
	return nil, nil
}

func (s stubWorkspaceViewAccess) DefaultView() WorkspaceView {
	return WorkspaceViewPipeline
}
