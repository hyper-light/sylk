package versioning

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/skills"
)

type WorkspaceWriteScope string

const (
	WorkspaceWriteScopeGlobal   WorkspaceWriteScope = "global"
	WorkspaceWriteScopePipeline WorkspaceWriteScope = "pipeline"
)

type WorkspaceWriteSkillConfig struct {
	GetFileAccess      FileAccessProvider
	GetViews           WorkspaceViewAccessFunc
	DefaultPipelineID  DefaultPipelineIDFunc
	Differ             Differ
	WritesEnabledCheck func() bool
}

type WorkspaceWriteBasis struct {
	Scope      WorkspaceWriteScope  `json:"scope"`
	Path       string               `json:"path"`
	PipelineID string               `json:"pipeline_id,omitempty"`
	TargetView WorkspaceView        `json:"target_view"`
	PreparedAt time.Time            `json:"prepared_at"`
	Disk       WorkspaceLayerState  `json:"disk"`
	Global     *WorkspaceLayerState `json:"global,omitempty"`
	Pipeline   *WorkspaceLayerState `json:"pipeline,omitempty"`
}

type PreparedWorkspaceWriteContext struct {
	Scope         WorkspaceWriteScope `json:"scope"`
	Path          string              `json:"path"`
	PipelineID    string              `json:"pipeline_id,omitempty"`
	TargetView    WorkspaceView       `json:"target_view"`
	Basis         WorkspaceWriteBasis `json:"basis"`
	State         *WorkspacePathState `json:"state"`
	RelevantDiffs []WorkspaceFileDiff `json:"relevant_diffs"`
	PreparedAt    time.Time           `json:"prepared_at"`
}

type WorkspaceFileDiff struct {
	Path         string        `json:"path"`
	PipelineID   string        `json:"pipeline_id,omitempty"`
	BaseView     WorkspaceView `json:"base_view"`
	TargetView   WorkspaceView `json:"target_view"`
	BaseExists   bool          `json:"base_exists"`
	TargetExists bool          `json:"target_exists"`
	Identical    bool          `json:"identical"`
	Hunks        []DiffHunkOut `json:"hunks"`
	Stats        DiffStatsOut  `json:"stats"`
	Rendered     string        `json:"rendered_diff"`
}

type WorkspaceChangeSummary struct {
	Scope   WorkspaceWriteScope `json:"scope"`
	Count   int                 `json:"count"`
	Changes []WorkspaceChange   `json:"changes"`
}

type WorkspaceChange struct {
	Path        string    `json:"path"`
	Operation   string    `json:"operation"`
	Bytes       int       `json:"bytes"`
	ContentHash string    `json:"content_hash,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

type overlayModificationReader interface {
	Modifications() []FileModification
}

type prepareWriteContextInput struct {
	Path       string `json:"path"`
	PipelineID string `json:"pipeline_id,omitempty"`
}

type writeFileWithBasisInput struct {
	Path    string              `json:"path"`
	Content string              `json:"content"`
	Basis   WorkspaceWriteBasis `json:"basis"`
}

type editFileWithBasisInput struct {
	Path  string              `json:"path"`
	Edits []FileEditInput     `json:"edits"`
	Basis WorkspaceWriteBasis `json:"basis"`
}

type deleteFileWithBasisInput struct {
	Path  string              `json:"path"`
	Basis WorkspaceWriteBasis `json:"basis"`
}

type mkdirWithBasisInput struct {
	Path  string              `json:"path"`
	Basis WorkspaceWriteBasis `json:"basis"`
}

type FileEditInput struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

type diffWorkspaceFileInput struct {
	Path       string `json:"path"`
	BaseView   string `json:"base_view"`
	TargetView string `json:"target_view"`
	PipelineID string `json:"pipeline_id,omitempty"`
}

func NewPreparePipelineWriteContextSkill(
	getViews WorkspaceViewAccessFunc,
	defaultPipelineID DefaultPipelineIDFunc,
	differ Differ,
) *skills.Skill {
	return newPrepareWriteContextSkill(
		"prepare_pipeline_write_context",
		WorkspaceWriteScopePipeline,
		getViews,
		defaultPipelineID,
		differ,
	)
}

func NewPrepareGlobalWriteContextSkill(getViews WorkspaceViewAccessFunc, differ Differ) *skills.Skill {
	return newPrepareWriteContextSkill(
		"prepare_global_write_context",
		WorkspaceWriteScopeGlobal,
		getViews,
		nil,
		differ,
	)
}

func NewWritePipelineFileSkill(cfg WorkspaceWriteSkillConfig) *skills.Skill {
	return newWriteFileSkill("write_pipeline_file", WorkspaceWriteScopePipeline, cfg)
}

func NewEditPipelineFileSkill(cfg WorkspaceWriteSkillConfig) *skills.Skill {
	return newEditFileSkill("edit_pipeline_file", WorkspaceWriteScopePipeline, cfg)
}

func NewDeletePipelineFileSkill(cfg WorkspaceWriteSkillConfig) *skills.Skill {
	return newDeleteFileSkill("delete_pipeline_file", WorkspaceWriteScopePipeline, cfg)
}

func NewCreatePipelineDirectorySkill(cfg WorkspaceWriteSkillConfig) *skills.Skill {
	return newCreateDirectorySkill("create_pipeline_directory", WorkspaceWriteScopePipeline, cfg)
}

func NewWriteGlobalFileSkill(cfg WorkspaceWriteSkillConfig) *skills.Skill {
	return newWriteFileSkill("write_global_file", WorkspaceWriteScopeGlobal, cfg)
}

func NewEditGlobalFileSkill(cfg WorkspaceWriteSkillConfig) *skills.Skill {
	return newEditFileSkill("edit_global_file", WorkspaceWriteScopeGlobal, cfg)
}

func NewDeleteGlobalFileSkill(cfg WorkspaceWriteSkillConfig) *skills.Skill {
	return newDeleteFileSkill("delete_global_file", WorkspaceWriteScopeGlobal, cfg)
}

func NewCreateGlobalDirectorySkill(cfg WorkspaceWriteSkillConfig) *skills.Skill {
	return newCreateDirectorySkill("create_global_directory", WorkspaceWriteScopeGlobal, cfg)
}

func NewListPipelineChangesSkill(getFA FileAccessProvider) *skills.Skill {
	return newListChangesSkill("list_pipeline_changes", WorkspaceWriteScopePipeline, getFA)
}

func NewListGlobalChangesSkill(getFA FileAccessProvider) *skills.Skill {
	return newListChangesSkill("list_global_changes", WorkspaceWriteScopeGlobal, getFA)
}

func NewDiffWorkspaceFileSkill(
	getViews WorkspaceViewAccessFunc,
	defaultPipelineID DefaultPipelineIDFunc,
	differ Differ,
) *skills.Skill {
	return skills.NewSkill("diff_workspace_file").
		Description("Compare one file across two explicit workspace views and return a structured diff plus rendered patch text.").
		Domain("filesystem").
		Keywords("workspace", "diff", "disk", "global", "pipeline", "compare").
		Priority(90).
		StringParam("path", "Path to diff across workspace views", true).
		EnumParam("base_view", "Baseline workspace view", workspaceViewEnumValues(), true).
		EnumParam("target_view", "Target workspace view", workspaceViewEnumValues(), true).
		StringParam("pipeline_id", "Task pipeline ID when using pipeline view", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params diffWorkspaceFileInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			path := strings.TrimSpace(params.Path)
			if path == "" {
				return nil, fmt.Errorf("path is required")
			}
			views := getViews()
			if views == nil {
				return nil, fmt.Errorf("workspace views are unavailable")
			}
			pipelineID := resolveSkillPipelineID(params.PipelineID, defaultPipelineID)
			return buildWorkspaceFileDiff(ctx, views, resolveWorkspaceDiffer(differ), path, WorkspaceView(params.BaseView), WorkspaceView(params.TargetView), pipelineID)
		}).
		Build()
}

func ValidateWorkspaceWriteBasis(
	ctx context.Context,
	views WorkspaceViewAccess,
	scope WorkspaceWriteScope,
	path string,
	basis WorkspaceWriteBasis,
) error {
	if views == nil {
		return fmt.Errorf("workspace views are unavailable")
	}
	return validateWorkspaceWriteBasis(ctx, views, scope, strings.TrimSpace(path), basis)
}

func newPrepareWriteContextSkill(
	name string,
	scope WorkspaceWriteScope,
	getViews WorkspaceViewAccessFunc,
	defaultPipelineID DefaultPipelineIDFunc,
	differ Differ,
) *skills.Skill {
	return skills.NewSkill(name).
		Description(prepareWriteContextDescription(scope)).
		Domain("filesystem").
		Keywords("workspace", "prepare", "write", "context", "disk", "global", "pipeline").
		Priority(93).
		StringParam("path", "Target file path to inspect before writing", true).
		StringParam("pipeline_id", "Task pipeline ID when preparing pipeline-local writes", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params prepareWriteContextInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			path := strings.TrimSpace(params.Path)
			if path == "" {
				return nil, fmt.Errorf("path is required")
			}
			views := getViews()
			if views == nil {
				return nil, fmt.Errorf("workspace views are unavailable")
			}
			pipelineID := resolveSkillPipelineID(params.PipelineID, defaultPipelineID)
			state, err := views.InspectPath(ctx, path, pipelineID)
			if err != nil {
				return nil, err
			}
			if err := ensureWriteTargetAvailable(scope, state); err != nil {
				return nil, err
			}
			preparedAt := time.Now().UTC()
			result := PreparedWorkspaceWriteContext{
				Scope:         scope,
				Path:          path,
				PipelineID:    pipelineID,
				TargetView:    writeScopeTargetView(scope),
				Basis:         buildWorkspaceWriteBasis(scope, state, preparedAt),
				State:         state,
				PreparedAt:    preparedAt,
				RelevantDiffs: buildRelevantWorkspaceDiffs(ctx, views, resolveWorkspaceDiffer(differ), scope, path, pipelineID, state),
			}
			return result, nil
		}).
		Build()
}

func newWriteFileSkill(name string, scope WorkspaceWriteScope, cfg WorkspaceWriteSkillConfig) *skills.Skill {
	return skills.NewSkill(name).
		Description(writeFileDescription(scope)).
		Domain("filesystem").
		Keywords("write", string(scope), "file", "workspace", "vfs", "basis").
		Priority(94).
		StringParam("path", "File path to write inside the allowed workspace layer", true).
		StringParam("content", "Full file content to write", true).
		ObjectParam("basis", "Write basis returned by the matching prepare_*_write_context skill.", workspaceWriteBasisProperties(), true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params writeFileWithBasisInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			path := strings.TrimSpace(params.Path)
			if path == "" {
				return nil, fmt.Errorf("path is required")
			}
			fa, views, err := resolveWorkspaceWriteAccess(cfg)
			if err != nil {
				return nil, err
			}
			if err := ensureWorkspaceWritesEnabled(cfg, fa); err != nil {
				return nil, err
			}
			if err := validateWorkspaceWriteBasis(ctx, views, scope, path, params.Basis); err != nil {
				return nil, err
			}
			exists, _ := fa.Exists(ctx, path)
			if err := fa.WriteFile(ctx, path, []byte(params.Content)); err != nil {
				return nil, fmt.Errorf("failed to write file: %w", err)
			}
			return map[string]any{
				"path":    path,
				"scope":   scope,
				"action":  fileWriteAction(exists),
				"bytes":   len(params.Content),
				"lines":   len(strings.Split(params.Content, "\n")),
				"success": true,
			}, nil
		}).
		Build()
}

func newEditFileSkill(name string, scope WorkspaceWriteScope, cfg WorkspaceWriteSkillConfig) *skills.Skill {
	return skills.NewSkill(name).
		Description(editFileDescription(scope)).
		Domain("filesystem").
		Keywords("edit", string(scope), "file", "workspace", "vfs", "basis").
		Priority(93).
		StringParam("path", "File path to edit inside the allowed workspace layer", true).
		ArrayParam("edits", "Search/replace edits to apply", "object", true).
		ObjectParam("basis", "Write basis returned by the matching prepare_*_write_context skill.", workspaceWriteBasisProperties(), true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params editFileWithBasisInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			path := strings.TrimSpace(params.Path)
			if path == "" {
				return nil, fmt.Errorf("path is required")
			}
			edits, err := normalizeFileEdits(params.Edits)
			if err != nil {
				return nil, err
			}
			fa, views, err := resolveWorkspaceWriteAccess(cfg)
			if err != nil {
				return nil, err
			}
			if err := ensureWorkspaceWritesEnabled(cfg, fa); err != nil {
				return nil, err
			}
			if err := validateWorkspaceWriteBasis(ctx, views, scope, path, params.Basis); err != nil {
				return nil, err
			}
			if err := fa.EditFile(ctx, path, edits); err != nil {
				return nil, err
			}
			return map[string]any{
				"path":          path,
				"scope":         scope,
				"edits_applied": len(edits),
				"success":       true,
			}, nil
		}).
		Build()
}

func newDeleteFileSkill(name string, scope WorkspaceWriteScope, cfg WorkspaceWriteSkillConfig) *skills.Skill {
	return skills.NewSkill(name).
		Description(deleteFileDescription(scope)).
		Domain("filesystem").
		Keywords("delete", "remove", string(scope), "file", "workspace", "basis").
		Priority(92).
		StringParam("path", "File path to delete inside the allowed workspace layer", true).
		ObjectParam("basis", "Write basis returned by the matching prepare_*_write_context skill.", workspaceWriteBasisProperties(), true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params deleteFileWithBasisInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			path := strings.TrimSpace(params.Path)
			if path == "" {
				return nil, fmt.Errorf("path is required")
			}
			fa, views, err := resolveWorkspaceWriteAccess(cfg)
			if err != nil {
				return nil, err
			}
			if err := ensureWorkspaceWritesEnabled(cfg, fa); err != nil {
				return nil, err
			}
			if err := validateWorkspaceWriteBasis(ctx, views, scope, path, params.Basis); err != nil {
				return nil, err
			}
			if err := fa.DeleteFile(ctx, path); err != nil {
				return nil, err
			}
			return map[string]any{
				"path":    path,
				"scope":   scope,
				"deleted": true,
			}, nil
		}).
		Build()
}

func newCreateDirectorySkill(name string, scope WorkspaceWriteScope, cfg WorkspaceWriteSkillConfig) *skills.Skill {
	return skills.NewSkill(name).
		Description(createDirectoryDescription(scope)).
		Domain("filesystem").
		Keywords("mkdir", "directory", string(scope), "workspace", "basis").
		Priority(92).
		StringParam("path", "Directory path to create inside the allowed workspace layer", true).
		ObjectParam("basis", "Write basis returned by the matching prepare_*_write_context skill for the target directory path.", workspaceWriteBasisProperties(), true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params mkdirWithBasisInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			path := strings.TrimSpace(params.Path)
			if path == "" {
				return nil, fmt.Errorf("path is required")
			}
			fa, views, err := resolveWorkspaceWriteAccess(cfg)
			if err != nil {
				return nil, err
			}
			if err := ensureWorkspaceWritesEnabled(cfg, fa); err != nil {
				return nil, err
			}
			if err := validateWorkspaceWriteBasis(ctx, views, scope, path, params.Basis); err != nil {
				return nil, err
			}
			if err := fa.MkdirAll(ctx, path); err != nil {
				return nil, fmt.Errorf("failed to create directory: %w", err)
			}
			return map[string]any{
				"path":    path,
				"scope":   scope,
				"created": true,
			}, nil
		}).
		Build()
}

func newListChangesSkill(name string, scope WorkspaceWriteScope, getFA FileAccessProvider) *skills.Skill {
	return skills.NewSkill(name).
		Description(listChangesDescription(scope)).
		Domain("filesystem").
		Keywords("list", "changes", string(scope), "workspace", "overlay", "pending").
		Priority(88).
		Handler(func(ctx context.Context, _ json.RawMessage) (any, error) {
			fa, err := resolveSkillFileAccess(getFA)
			if err != nil {
				return nil, err
			}
			reader, ok := fa.(overlayModificationReader)
			if !ok {
				return &WorkspaceChangeSummary{Scope: scope}, nil
			}
			return summarizeWorkspaceChanges(scope, reader.Modifications()), nil
		}).
		Build()
}

func prepareWriteContextDescription(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Read disk, global, and pipeline state for one target path before mutating the pipeline VFS. Returns a write basis plus diffs so the agent can reason about committed versus in-progress state explicitly."
	}
	return "Read disk and global state for one target path before mutating the global VFS. Returns a write basis plus diffs so the agent can reason about committed versus in-progress state explicitly."
}

func writeFileDescription(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Write full file content into the pipeline VFS only. Requires a fresh basis from prepare_pipeline_write_context."
	}
	return "Write full file content into the global VFS only. Requires a fresh basis from prepare_global_write_context."
}

func editFileDescription(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Apply search/replace edits inside the pipeline VFS only. Requires a fresh basis from prepare_pipeline_write_context."
	}
	return "Apply search/replace edits inside the global VFS only. Requires a fresh basis from prepare_global_write_context."
}

func deleteFileDescription(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Delete a file from the pipeline VFS only. Requires a fresh basis from prepare_pipeline_write_context."
	}
	return "Delete a file from the global VFS only. Requires a fresh basis from prepare_global_write_context."
}

func createDirectoryDescription(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Create a directory inside the pipeline VFS only. Requires a fresh basis from prepare_pipeline_write_context for the target directory path."
	}
	return "Create a directory inside the global VFS only. Requires a fresh basis from prepare_global_write_context for the target directory path."
}

func listChangesDescription(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "List the current staged pipeline-VFS changes for this agent's task."
	}
	return "List the current staged global-VFS changes for this session."
}

func workspaceViewEnumValues() []string {
	return []string{string(WorkspaceViewDisk), string(WorkspaceViewGlobal), string(WorkspaceViewPipeline)}
}

func workspaceWriteBasisProperties() map[string]*skills.Property {
	return map[string]*skills.Property{
		"scope":       {Type: "string", Description: "Scope returned by prepare_*_write_context."},
		"path":        {Type: "string", Description: "Target path prepared for mutation."},
		"pipeline_id": {Type: "string", Description: "Pipeline ID when the basis is for pipeline-local writes."},
		"target_view": {Type: "string", Description: "Target workspace view this basis authorizes."},
	}
}

func resolveWorkspaceWriteAccess(cfg WorkspaceWriteSkillConfig) (FileAccess, WorkspaceViewAccess, error) {
	fa, err := resolveSkillFileAccess(cfg.GetFileAccess)
	if err != nil {
		return nil, nil, err
	}
	if cfg.GetViews == nil {
		return nil, nil, fmt.Errorf("workspace views are unavailable")
	}
	views := cfg.GetViews()
	if views == nil {
		return nil, nil, fmt.Errorf("workspace views are unavailable")
	}
	return fa, views, nil
}

func ensureWorkspaceWritesEnabled(cfg WorkspaceWriteSkillConfig, fa FileAccess) error {
	if fa == nil {
		return fmt.Errorf("file access is unavailable")
	}
	if cfg.WritesEnabledCheck != nil && !cfg.WritesEnabledCheck() {
		return fmt.Errorf("file writes are disabled")
	}
	if fa.IsReadOnly() {
		return fmt.Errorf("file writes are disabled")
	}
	return nil
}

func buildWorkspaceWriteBasis(scope WorkspaceWriteScope, state *WorkspacePathState, preparedAt time.Time) WorkspaceWriteBasis {
	basis := WorkspaceWriteBasis{
		Scope:      scope,
		Path:       workspaceStatePath(state),
		PipelineID: workspaceStatePipelineID(state),
		TargetView: writeScopeTargetView(scope),
		PreparedAt: preparedAt,
	}
	if state != nil {
		basis.Disk = state.Disk
		basis.Global = cloneWorkspaceLayerState(state.Global)
		basis.Pipeline = cloneWorkspaceLayerState(state.Pipeline)
	}
	return basis
}

func workspaceStatePath(state *WorkspacePathState) string {
	if state == nil {
		return ""
	}
	return state.Path
}

func workspaceStatePipelineID(state *WorkspacePathState) string {
	if state == nil {
		return ""
	}
	return state.PipelineID
}

func cloneWorkspaceLayerState(state *WorkspaceLayerState) *WorkspaceLayerState {
	if state == nil {
		return nil
	}
	cloned := *state
	return &cloned
}

func ensureWriteTargetAvailable(scope WorkspaceWriteScope, state *WorkspacePathState) error {
	layer := workspaceLayerForScope(scope, state)
	if layer == nil {
		return fmt.Errorf("%s workspace view is unavailable", scope)
	}
	if !layer.Available || strings.TrimSpace(layer.Error) != "" {
		return fmt.Errorf("%s workspace view is unavailable: %s", scope, strings.TrimSpace(layer.Error))
	}
	return nil
}

func workspaceLayerForScope(scope WorkspaceWriteScope, state *WorkspacePathState) *WorkspaceLayerState {
	if state == nil {
		return nil
	}
	if scope == WorkspaceWriteScopePipeline {
		return state.Pipeline
	}
	return state.Global
}

func buildRelevantWorkspaceDiffs(
	ctx context.Context,
	views WorkspaceViewAccess,
	differ Differ,
	scope WorkspaceWriteScope,
	path string,
	pipelineID string,
	state *WorkspacePathState,
) []WorkspaceFileDiff {
	pairs := relevantWorkspaceDiffPairs(scope, state)
	diffs := make([]WorkspaceFileDiff, 0, len(pairs))
	for _, pair := range pairs {
		diff, err := buildWorkspaceFileDiff(ctx, views, differ, path, pair[0], pair[1], pipelineID)
		if err != nil {
			continue
		}
		diffs = append(diffs, diff)
	}
	return diffs
}

func relevantWorkspaceDiffPairs(scope WorkspaceWriteScope, state *WorkspacePathState) [][2]WorkspaceView {
	pairs := [][2]WorkspaceView{}
	if state == nil {
		return pairs
	}
	if state.Global != nil && state.Global.Available && strings.TrimSpace(state.Global.Error) == "" {
		pairs = append(pairs, [2]WorkspaceView{WorkspaceViewDisk, WorkspaceViewGlobal})
	}
	if scope != WorkspaceWriteScopePipeline {
		return pairs
	}
	if state.Pipeline != nil && state.Pipeline.Available && strings.TrimSpace(state.Pipeline.Error) == "" {
		pairs = append(pairs, [2]WorkspaceView{WorkspaceViewDisk, WorkspaceViewPipeline})
	}
	if state.Global != nil && state.Pipeline != nil && state.Global.Available && state.Pipeline.Available &&
		strings.TrimSpace(state.Global.Error) == "" && strings.TrimSpace(state.Pipeline.Error) == "" {
		pairs = append(pairs, [2]WorkspaceView{WorkspaceViewGlobal, WorkspaceViewPipeline})
	}
	return pairs
}

func resolveWorkspaceDiffer(differ Differ) Differ {
	if differ != nil {
		return differ
	}
	return NewMyersDiffer(3)
}

func buildWorkspaceFileDiff(
	ctx context.Context,
	views WorkspaceViewAccess,
	differ Differ,
	path string,
	baseView WorkspaceView,
	targetView WorkspaceView,
	pipelineID string,
) (WorkspaceFileDiff, error) {
	baseContent, baseExists, err := readWorkspaceDiffContent(ctx, views, baseView, path, pipelineID)
	if err != nil {
		return WorkspaceFileDiff{}, err
	}
	targetContent, targetExists, err := readWorkspaceDiffContent(ctx, views, targetView, path, pipelineID)
	if err != nil {
		return WorkspaceFileDiff{}, err
	}
	diff := differ.DiffBytes(baseContent, targetContent)
	hunks := diffHunksOut(diff)
	stats := diffStatsOut(diff)
	return WorkspaceFileDiff{
		Path:         path,
		PipelineID:   pipelineID,
		BaseView:     baseView,
		TargetView:   targetView,
		BaseExists:   baseExists,
		TargetExists: targetExists,
		Identical:    diffIsEmpty(diff),
		Hunks:        hunks,
		Stats:        stats,
		Rendered:     renderWorkspaceDiff(baseView, targetView, path, hunks),
	}, nil
}

func readWorkspaceDiffContent(
	ctx context.Context,
	views WorkspaceViewAccess,
	view WorkspaceView,
	path string,
	pipelineID string,
) ([]byte, bool, error) {
	content, err := views.ReadFile(ctx, view, path, pipelineID)
	if err == nil {
		return content, true, nil
	}
	if err == ErrFileNotFound {
		return nil, false, nil
	}
	return nil, false, err
}

func diffHunksOut(diff *FileDiff) []DiffHunkOut {
	if diff == nil {
		return nil
	}
	result := make([]DiffHunkOut, 0, len(diff.Hunks))
	for _, h := range diff.Hunks {
		result = append(result, DiffHunkOut{
			OldStart: h.OldStart,
			OldCount: h.OldCount,
			NewStart: h.NewStart,
			NewCount: h.NewCount,
			Lines:    diffLinesOut(h.Lines),
		})
	}
	return result
}

func diffLinesOut(lines []DiffLine) []DiffLineOut {
	result := make([]DiffLineOut, 0, len(lines))
	for _, line := range lines {
		result = append(result, DiffLineOut{
			Type:    diffLineTypeToString(line.Type),
			Content: line.Content,
			OldLine: line.OldLine,
			NewLine: line.NewLine,
		})
	}
	return result
}

func diffStatsOut(diff *FileDiff) DiffStatsOut {
	if diff == nil {
		return DiffStatsOut{}
	}
	return DiffStatsOut{
		Additions: diff.Stats.Additions,
		Deletions: diff.Stats.Deletions,
		Changes:   diff.Stats.Changes,
	}
}

func diffIsEmpty(diff *FileDiff) bool {
	if diff == nil {
		return true
	}
	return len(diff.Hunks) == 0 && diff.Stats.Additions == 0 && diff.Stats.Deletions == 0 && diff.Stats.Changes == 0
}

func renderWorkspaceDiff(baseView, targetView WorkspaceView, path string, hunks []DiffHunkOut) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("--- %s:%s\n", baseView, path))
	b.WriteString(fmt.Sprintf("+++ %s:%s\n", targetView, path))
	for _, hunk := range hunks {
		b.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", hunk.OldStart, hunk.OldCount, hunk.NewStart, hunk.NewCount))
		for _, line := range hunk.Lines {
			b.WriteString(renderWorkspaceDiffLine(line))
		}
	}
	return strings.TrimSpace(b.String())
}

func renderWorkspaceDiffLine(line DiffLineOut) string {
	return diffLinePrefix(line.Type) + line.Content + "\n"
}

func diffLinePrefix(lineType string) string {
	switch lineType {
	case "add":
		return "+"
	case "delete":
		return "-"
	default:
		return " "
	}
}

func validateWorkspaceWriteBasis(
	ctx context.Context,
	views WorkspaceViewAccess,
	scope WorkspaceWriteScope,
	path string,
	basis WorkspaceWriteBasis,
) error {
	if err := validateBasisIdentity(scope, path, basis); err != nil {
		return err
	}
	state, err := views.InspectPath(ctx, path, basis.PipelineID)
	if err != nil {
		return err
	}
	if err := ensureWriteTargetAvailable(scope, state); err != nil {
		return err
	}
	if err := validateLayerSnapshot("disk", basis.Disk, state.Disk); err != nil {
		return staleBasisError(scope, err)
	}
	if err := validateScopedLayers(scope, basis, state); err != nil {
		return staleBasisError(scope, err)
	}
	return nil
}

func validateBasisIdentity(scope WorkspaceWriteScope, path string, basis WorkspaceWriteBasis) error {
	if basis.Scope != scope {
		return fmt.Errorf("basis scope %q does not match %q", basis.Scope, scope)
	}
	if strings.TrimSpace(basis.Path) != path {
		return fmt.Errorf("basis path %q does not match %q", basis.Path, path)
	}
	if basis.TargetView != writeScopeTargetView(scope) {
		return fmt.Errorf("basis target_view %q does not match %q", basis.TargetView, writeScopeTargetView(scope))
	}
	return nil
}

func validateScopedLayers(scope WorkspaceWriteScope, basis WorkspaceWriteBasis, state *WorkspacePathState) error {
	if scope == WorkspaceWriteScopeGlobal {
		return validateOptionalLayerSnapshot("global", basis.Global, state.Global)
	}
	if err := validateOptionalLayerSnapshot("global", basis.Global, state.Global); err != nil {
		return err
	}
	return validateOptionalLayerSnapshot("pipeline", basis.Pipeline, state.Pipeline)
}

func validateOptionalLayerSnapshot(name string, expected, actual *WorkspaceLayerState) error {
	if expected == nil && actual == nil {
		return nil
	}
	if expected == nil || actual == nil {
		return fmt.Errorf("%s state changed", name)
	}
	return validateLayerSnapshot(name, *expected, *actual)
}

func validateLayerSnapshot(name string, expected, actual WorkspaceLayerState) error {
	if expected.Available != actual.Available {
		return fmt.Errorf("%s availability changed", name)
	}
	if expected.Exists != actual.Exists {
		return fmt.Errorf("%s existence changed", name)
	}
	if strings.TrimSpace(expected.Error) != strings.TrimSpace(actual.Error) {
		return fmt.Errorf("%s error state changed", name)
	}
	if expected.ContentHash != actual.ContentHash {
		return fmt.Errorf("%s content changed", name)
	}
	return nil
}

func staleBasisError(scope WorkspaceWriteScope, cause error) error {
	return fmt.Errorf("%s write basis is stale: %w; rerun prepare_%s_write_context", scope, cause, scope)
}

func normalizeFileEdits(inputs []FileEditInput) ([]FileEdit, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("at least one edit is required")
	}
	edits := make([]FileEdit, 0, len(inputs))
	for i, edit := range inputs {
		if strings.TrimSpace(edit.OldText) == "" {
			return nil, fmt.Errorf("edit %d: old_text is required", i)
		}
		edits = append(edits, FileEdit{
			OldText: edit.OldText,
			NewText: edit.NewText,
		})
	}
	return edits, nil
}

func writeScopeTargetView(scope WorkspaceWriteScope) WorkspaceView {
	if scope == WorkspaceWriteScopePipeline {
		return WorkspaceViewPipeline
	}
	return WorkspaceViewGlobal
}

func fileWriteAction(exists bool) string {
	if exists {
		return "modify"
	}
	return "create"
}

func summarizeWorkspaceChanges(scope WorkspaceWriteScope, mods []FileModification) *WorkspaceChangeSummary {
	changes := make([]WorkspaceChange, 0, len(mods))
	for _, mod := range mods {
		changes = append(changes, WorkspaceChange{
			Path:        mod.OriginalPath,
			Operation:   mod.Operation.String(),
			Bytes:       len(mod.NewContent),
			ContentHash: changeContentHash(mod),
			Timestamp:   mod.Timestamp,
		})
	}
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Path < changes[j].Path
	})
	return &WorkspaceChangeSummary{
		Scope:   scope,
		Count:   len(changes),
		Changes: changes,
	}
}

func changeContentHash(mod FileModification) string {
	if mod.ContentHash.IsZero() {
		return ""
	}
	return mod.ContentHash.String()
}
