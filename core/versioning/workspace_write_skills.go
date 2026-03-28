package versioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	Scope          WorkspaceWriteScope  `json:"scope"`
	Path           string               `json:"path"`
	PipelineID     string               `json:"pipeline_id,omitempty"`
	TargetView     WorkspaceView        `json:"target_view"`
	PreparedAt     time.Time            `json:"prepared_at"`
	LeaseExpiresAt time.Time            `json:"lease_expires_at,omitempty"`
	Disk           WorkspaceLayerState  `json:"disk"`
	Global         *WorkspaceLayerState `json:"global,omitempty"`
	Pipeline       *WorkspaceLayerState `json:"pipeline,omitempty"`
}

type WorkspaceWriteRepair struct {
	Refreshed     bool     `json:"refreshed,omitempty"`
	Rebound       bool     `json:"rebound,omitempty"`
	OriginalPath  string   `json:"original_path,omitempty"`
	EffectivePath string   `json:"effective_path,omitempty"`
	Reasons       []string `json:"reasons,omitempty"`
}

const defaultWorkspaceWriteLease = 2 * time.Minute

var ErrWorkspaceWriteLeaseExpired = errors.New("workspace write lease expired")

type WorkspaceWriteStaleReason string

const (
	WorkspaceWriteStaleReasonLeaseExpired    WorkspaceWriteStaleReason = "lease_expired"
	WorkspaceWriteStaleReasonTargetChanged   WorkspaceWriteStaleReason = "target_changed"
	WorkspaceWriteStaleReasonReferenceChange WorkspaceWriteStaleReason = "reference_changed"
)

type WorkspaceWriteStaleError struct {
	Scope  WorkspaceWriteScope
	Path   string
	Reason WorkspaceWriteStaleReason
	Cause  error
}

func (e *WorkspaceWriteStaleError) Error() string {
	if e == nil {
		return "workspace write basis is stale"
	}
	cause := "state changed"
	if e.Cause != nil {
		cause = strings.TrimSpace(e.Cause.Error())
	}
	return fmt.Sprintf("%s write basis is stale: %s; rerun prepare_%s_write_context", e.Scope, cause, e.Scope)
}

func (e *WorkspaceWriteStaleError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
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

type visiblePathRegistrar interface {
	RegisterVisiblePath(path string)
}

type workspaceWriteOperation string

const (
	workspaceWriteOperationWriteFile       workspaceWriteOperation = "write_file"
	workspaceWriteOperationEditFile        workspaceWriteOperation = "edit_file"
	workspaceWriteOperationDeleteFile      workspaceWriteOperation = "delete_file"
	workspaceWriteOperationCreateDirectory workspaceWriteOperation = "create_directory"
)

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

func WorkspaceWriteStale(err error) (*WorkspaceWriteStaleError, bool) {
	var stale *WorkspaceWriteStaleError
	if !errors.As(err, &stale) || stale == nil {
		return nil, false
	}
	return stale, true
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
		Usage(prepareWriteContextUsage(scope)).
		Requirement(prepareWriteContextRequirement(scope)).
		Satisfies(prepareWriteContextOutcome(scope)).
		Avoid(prepareWriteContextAvoid(scope)).
		BestPractice(prepareWriteContextBestPractice(scope)).
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
		Usage(writeFileUsage(scope)).
		Requirement(writeFileRequirement(scope)).
		Satisfies(writeFileOutcome(scope)).
		Avoid(writeFileAvoid(scope)).
		BestPractice(sharedWriteToolBestPractice()).
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
			activeBasis, repair, err := resolveWorkspaceMutationBasis(ctx, fa, views, scope, path, params.Basis, workspaceWriteOperationWriteFile)
			if err != nil {
				return nil, err
			}
			exists, _ := fa.Exists(ctx, path)
			if err := fa.WriteFile(ctx, path, []byte(params.Content)); err != nil {
				return nil, fmt.Errorf("failed to write file: %w", err)
			}
			result := map[string]any{
				"path":    path,
				"scope":   scope,
				"action":  fileWriteAction(exists),
				"bytes":   len(params.Content),
				"lines":   len(strings.Split(params.Content, "\n")),
				"success": true,
			}
			attachWorkspaceWriteRepair(result, repair)
			attachRefreshedWorkspaceWriteBasis(ctx, result, views, scope, path, activeBasis.PipelineID)
			return result, nil
		}).
		Build()
}

func newEditFileSkill(name string, scope WorkspaceWriteScope, cfg WorkspaceWriteSkillConfig) *skills.Skill {
	return skills.NewSkill(name).
		Description(editFileDescription(scope)).
		Domain("filesystem").
		Keywords("edit", string(scope), "file", "workspace", "vfs", "basis").
		Priority(93).
		Usage(editFileUsage(scope)).
		Requirement(editFileRequirement(scope)).
		Satisfies(editFileOutcome(scope)).
		Avoid(editFileAvoid(scope)).
		BestPractice(sharedWriteToolBestPractice()).
		BestPractice(editFileBestPractice(scope)).
		StringParam("path", "File path to edit inside the allowed workspace layer", true).
		ArrayObjectParam("edits", editFileEditsDescription(scope), editFileProperties(), []string{"old_text", "new_text"}, true).
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
			activeBasis, repair, err := resolveWorkspaceMutationBasis(ctx, fa, views, scope, path, params.Basis, workspaceWriteOperationEditFile)
			if err != nil {
				return nil, err
			}
			if err := fa.EditFile(ctx, path, edits); err != nil {
				return nil, err
			}
			result := map[string]any{
				"path":          path,
				"scope":         scope,
				"edits_applied": len(edits),
				"success":       true,
			}
			attachWorkspaceWriteRepair(result, repair)
			attachRefreshedWorkspaceWriteBasis(ctx, result, views, scope, path, activeBasis.PipelineID)
			return result, nil
		}).
		Build()
}

func newDeleteFileSkill(name string, scope WorkspaceWriteScope, cfg WorkspaceWriteSkillConfig) *skills.Skill {
	return skills.NewSkill(name).
		Description(deleteFileDescription(scope)).
		Domain("filesystem").
		Keywords("delete", "remove", string(scope), "file", "workspace", "basis").
		Priority(92).
		Usage(deleteFileUsage(scope)).
		Requirement(deleteFileRequirement(scope)).
		Satisfies(deleteFileOutcome(scope)).
		Avoid(deleteFileAvoid(scope)).
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
			activeBasis, repair, err := resolveWorkspaceMutationBasis(ctx, fa, views, scope, path, params.Basis, workspaceWriteOperationDeleteFile)
			if err != nil {
				return nil, err
			}
			if err := fa.DeleteFile(ctx, path); err != nil {
				return nil, err
			}
			result := map[string]any{
				"path":    path,
				"scope":   scope,
				"deleted": true,
			}
			attachWorkspaceWriteRepair(result, repair)
			attachRefreshedWorkspaceWriteBasis(ctx, result, views, scope, path, activeBasis.PipelineID)
			return result, nil
		}).
		Build()
}

func newCreateDirectorySkill(name string, scope WorkspaceWriteScope, cfg WorkspaceWriteSkillConfig) *skills.Skill {
	return skills.NewSkill(name).
		Description(createDirectoryDescription(scope)).
		Domain("filesystem").
		Keywords("mkdir", "directory", string(scope), "workspace", "basis").
		Priority(92).
		Usage(createDirectoryUsage(scope)).
		Requirement(createDirectoryRequirement(scope)).
		Satisfies(createDirectoryOutcome(scope)).
		Avoid(createDirectoryAvoid(scope)).
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
			activeBasis, repair, err := resolveWorkspaceMutationBasis(ctx, fa, views, scope, path, params.Basis, workspaceWriteOperationCreateDirectory)
			if err != nil {
				return nil, err
			}
			if err := fa.MkdirAll(ctx, path); err != nil {
				return nil, fmt.Errorf("failed to create directory: %w", err)
			}
			result := map[string]any{
				"path":    path,
				"scope":   scope,
				"created": true,
			}
			attachWorkspaceWriteRepair(result, repair)
			attachRefreshedWorkspaceWriteBasis(ctx, result, views, scope, path, activeBasis.PipelineID)
			return result, nil
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

func prepareWriteContextUsage(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Call before the first pipeline-local write, edit, delete, or directory creation for a path. Re-run only when the lease expires, the tool returns a refreshed next_basis, or you intentionally switch to a different target path."
	}
	return "Call before the first global write, edit, delete, or directory creation for a path. Re-run only when the lease expires, the tool returns a refreshed next_basis, or you intentionally switch to a different target path."
}

func prepareWriteContextRequirement(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Use the concrete target path you plan to mutate in the pipeline workspace. For new files, use the intended output path even if it does not exist yet."
	}
	return "Use the concrete target path you plan to mutate in the global workspace. For new files, use the intended output path even if it does not exist yet."
}

func prepareWriteContextOutcome(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Produces a leased pipeline write basis plus relevant diffs for the target path."
	}
	return "Produces a leased global write basis plus relevant diffs for the target path."
}

func prepareWriteContextAvoid(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Do not call speculatively for many paths you may never mutate. Prepare the concrete pipeline target when you are ready to write."
	}
	return "Do not call speculatively for many paths you may never mutate. Prepare the concrete global target when you are ready to write."
}

func prepareWriteContextBestPractice(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Carry the returned basis into the first mutation, then keep reusing next_basis for follow-up writes to the same pipeline path while the lease remains active."
	}
	return "Carry the returned basis into the first mutation, then keep reusing next_basis for follow-up writes to the same global path while the lease remains active."
}

func sharedWriteToolBestPractice() string {
	return "When the tool returns next_basis, feed it into the next mutation on the same path instead of immediately repreparing."
}

func writeFileUsage(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Use for full-file creation or replacement after prepare_pipeline_write_context. This is the concrete mutation step after planning."
	}
	return "Use for full-file creation or replacement after prepare_global_write_context. This is the concrete mutation step after planning."
}

func writeFileRequirement(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Requires a fresh or still-leased basis from prepare_pipeline_write_context for the same target path."
	}
	return "Requires a fresh or still-leased basis from prepare_global_write_context for the same target path."
}

func writeFileOutcome(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Creates or overwrites the pipeline-local file and returns next_basis for the same path."
	}
	return "Creates or overwrites the global file and returns next_basis for the same path."
}

func writeFileAvoid(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Do not use for small targeted edits when edit_pipeline_file expresses the change more precisely."
	}
	return "Do not use for small targeted edits when edit_global_file expresses the change more precisely."
}

func editFileUsage(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Use when you know the exact current text to replace inside an existing pipeline file and want a narrower mutation than a full rewrite."
	}
	return "Use when you know the exact current text to replace inside an existing global file and want a narrower mutation than a full rewrite."
}

func editFileRequirement(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Requires a fresh or still-leased basis from prepare_pipeline_write_context for the same target path. Each edit item must include exact old_text from the current file plus the desired new_text."
	}
	return "Requires a fresh or still-leased basis from prepare_global_write_context for the same target path. Each edit item must include exact old_text from the current file plus the desired new_text."
}

func editFileOutcome(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Applies targeted edits to the pipeline-local file and returns next_basis for continued work."
	}
	return "Applies targeted edits to the global file and returns next_basis for continued work."
}

func editFileAvoid(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Do not use for brand-new files, broad rewrites, or cases where you cannot supply exact old_text for each change; use write_pipeline_file instead."
	}
	return "Do not use for brand-new files, broad rewrites, or cases where you cannot supply exact old_text for each change; use write_global_file instead."
}

func deleteFileUsage(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Use to remove a pipeline-local file after inspecting the workspace state and confirming the deletion is in scope."
	}
	return "Use to remove a global file after inspecting the workspace state and confirming the deletion is in scope."
}

func deleteFileRequirement(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Requires a fresh or still-leased basis from prepare_pipeline_write_context for the same target path."
	}
	return "Requires a fresh or still-leased basis from prepare_global_write_context for the same target path."
}

func deleteFileOutcome(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Deletes the pipeline-local file and returns refreshed path state."
	}
	return "Deletes the global file and returns refreshed path state."
}

func deleteFileAvoid(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Do not use as a substitute for cleaning generated output unless the task explicitly requires deleting that pipeline path."
	}
	return "Do not use as a substitute for cleaning generated output unless the task explicitly requires deleting that global path."
}

func createDirectoryUsage(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Use to create the concrete pipeline directory structure needed before writing nested files."
	}
	return "Use to create the concrete global directory structure needed before writing nested files."
}

func createDirectoryRequirement(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Requires a fresh or still-leased basis for the target directory path or a child path that can be rebound safely."
	}
	return "Requires a fresh or still-leased basis for the target directory path or a child path that can be rebound safely."
}

func createDirectoryOutcome(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Creates the pipeline-local directory and returns refreshed basis metadata when available."
	}
	return "Creates the global directory and returns refreshed basis metadata when available."
}

func createDirectoryAvoid(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Do not call repeatedly for the same directory if it already exists; proceed to the file writes instead."
	}
	return "Do not call repeatedly for the same directory if it already exists; proceed to the file writes instead."
}

func writeFileDescription(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Write full file content into the pipeline VFS only. Requires a fresh or still-leased basis from prepare_pipeline_write_context and returns a refreshed next_basis on success."
	}
	return "Write full file content into the global VFS only. Requires a fresh or still-leased basis from prepare_global_write_context and returns a refreshed next_basis on success."
}

func editFileDescription(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Apply precise search/replace edits inside the pipeline VFS only. Each edit item must include exact old_text and new_text. Requires a fresh or still-leased basis from prepare_pipeline_write_context and returns a refreshed next_basis on success."
	}
	return "Apply precise search/replace edits inside the global VFS only. Each edit item must include exact old_text and new_text. Requires a fresh or still-leased basis from prepare_global_write_context and returns a refreshed next_basis on success."
}

func editFileBestPractice(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Read the current pipeline file first, copy the exact old_text you intend to replace, and switch to write_pipeline_file instead of edit_pipeline_file when the change is effectively a full rewrite."
	}
	return "Read the current global file first, copy the exact old_text you intend to replace, and switch to write_global_file instead of edit_global_file when the change is effectively a full rewrite."
}

func editFileEditsDescription(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Search/replace edits to apply. Every item must include exact old_text from the current pipeline file and the replacement new_text."
	}
	return "Search/replace edits to apply. Every item must include exact old_text from the current global file and the replacement new_text."
}

func editFileProperties() map[string]*skills.Property {
	return map[string]*skills.Property{
		"old_text": {
			Type:        "string",
			Description: "Exact current text to find in the file before replacement. Required for every edit.",
		},
		"new_text": {
			Type:        "string",
			Description: "Replacement text for the matched old_text. Use an empty string to delete the matched text.",
		},
	}
}

func deleteFileDescription(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Delete a file from the pipeline VFS only. Requires a fresh or still-leased basis from prepare_pipeline_write_context and returns a refreshed next_basis on success."
	}
	return "Delete a file from the global VFS only. Requires a fresh or still-leased basis from prepare_global_write_context and returns a refreshed next_basis on success."
}

func createDirectoryDescription(scope WorkspaceWriteScope) string {
	if scope == WorkspaceWriteScopePipeline {
		return "Create a directory inside the pipeline VFS only. Requires a fresh or still-leased basis from prepare_pipeline_write_context for the target directory path and returns a refreshed next_basis when available."
	}
	return "Create a directory inside the global VFS only. Requires a fresh or still-leased basis from prepare_global_write_context for the target directory path and returns a refreshed next_basis when available."
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
		"scope":            {Type: "string", Description: "Scope returned by prepare_*_write_context."},
		"path":             {Type: "string", Description: "Target path prepared for mutation."},
		"pipeline_id":      {Type: "string", Description: "Pipeline ID when the basis is for pipeline-local writes."},
		"target_view":      {Type: "string", Description: "Target workspace view this basis authorizes."},
		"prepared_at":      {Type: "string", Description: "When the basis snapshot was prepared."},
		"lease_expires_at": {Type: "string", Description: "When the write lease expires and prepare_*_write_context must be rerun."},
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

func resolveWorkspaceMutationBasis(
	ctx context.Context,
	fa FileAccess,
	views WorkspaceViewAccess,
	scope WorkspaceWriteScope,
	path string,
	basis WorkspaceWriteBasis,
	op workspaceWriteOperation,
) (WorkspaceWriteBasis, *WorkspaceWriteRepair, error) {
	activeBasis, repair, err := maybeRebindWorkspaceMutationBasis(ctx, fa, views, scope, path, basis, op)
	if err != nil {
		return WorkspaceWriteBasis{}, nil, err
	}
	if err := validateWorkspaceWriteBasis(ctx, views, scope, path, activeBasis); err != nil {
		return refreshWorkspaceMutationBasis(ctx, fa, views, scope, path, activeBasis, repair, err)
	}
	return activeBasis, compactWorkspaceWriteRepair(repair), nil
}

func maybeRebindWorkspaceMutationBasis(
	ctx context.Context,
	fa FileAccess,
	views WorkspaceViewAccess,
	scope WorkspaceWriteScope,
	path string,
	basis WorkspaceWriteBasis,
	op workspaceWriteOperation,
) (WorkspaceWriteBasis, *WorkspaceWriteRepair, error) {
	if !canRebindWorkspaceMutationBasis(op, path, basis.Path) {
		return basis, nil, nil
	}
	refreshed, err := refreshWorkspaceWriteBasisWithAccess(ctx, views, fa, scope, path, basis.PipelineID)
	if err != nil {
		return WorkspaceWriteBasis{}, nil, err
	}
	return refreshed, &WorkspaceWriteRepair{
		Refreshed:     true,
		Rebound:       true,
		OriginalPath:  strings.TrimSpace(basis.Path),
		EffectivePath: strings.TrimSpace(path),
		Reasons:       []string{"path_rebound"},
	}, nil
}

func refreshWorkspaceMutationBasis(
	ctx context.Context,
	fa FileAccess,
	views WorkspaceViewAccess,
	scope WorkspaceWriteScope,
	path string,
	basis WorkspaceWriteBasis,
	repair *WorkspaceWriteRepair,
	cause error,
) (WorkspaceWriteBasis, *WorkspaceWriteRepair, error) {
	if !workspaceWriteAutoRefreshable(cause) {
		return WorkspaceWriteBasis{}, nil, cause
	}
	refreshed, err := refreshWorkspaceWriteBasisWithAccess(ctx, views, fa, scope, path, basis.PipelineID)
	if err != nil {
		return WorkspaceWriteBasis{}, nil, err
	}
	if err := validateWorkspaceWriteBasis(ctx, views, scope, path, refreshed); err != nil {
		return WorkspaceWriteBasis{}, nil, err
	}
	return refreshed, mergeWorkspaceWriteRepair(repair, &WorkspaceWriteRepair{
		Refreshed:     true,
		OriginalPath:  strings.TrimSpace(basis.Path),
		EffectivePath: strings.TrimSpace(path),
		Reasons:       []string{workspaceWriteRepairReason(cause)},
	}), nil
}

func canRebindWorkspaceMutationBasis(op workspaceWriteOperation, targetPath, basisPath string) bool {
	if op != workspaceWriteOperationCreateDirectory {
		return false
	}
	return workspacePathIsDescendant(targetPath, basisPath)
}

func workspacePathIsDescendant(parentPath, childPath string) bool {
	target := normalizeWorkspaceMutationPath(parentPath)
	basis := normalizeWorkspaceMutationPath(childPath)
	if !validWorkspaceDescendantPair(target, basis) {
		return false
	}
	return workspaceRelativePathWithinTarget(target, basis)
}

func validWorkspaceDescendantPair(target, basis string) bool {
	return target != "" && basis != "" && target != basis
}

func workspaceRelativePathWithinTarget(target, basis string) bool {
	rel, err := filepath.Rel(target, basis)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, "..")
}

func workspaceWriteAutoRefreshable(err error) bool {
	if errors.Is(err, ErrWorkspaceWriteLeaseExpired) {
		return true
	}
	_, ok := WorkspaceWriteStale(err)
	return ok
}

func workspaceWriteRepairReason(err error) string {
	stale, ok := WorkspaceWriteStale(err)
	if !ok || stale == nil || stale.Reason == "" {
		return "state_refreshed"
	}
	return string(stale.Reason)
}

func refreshWorkspaceWriteBasisWithAccess(
	ctx context.Context,
	views WorkspaceViewAccess,
	fa FileAccess,
	scope WorkspaceWriteScope,
	path string,
	pipelineID string,
) (WorkspaceWriteBasis, error) {
	registerVisiblePathIfSupported(fa, path)
	return RefreshWorkspaceWriteBasis(ctx, views, scope, path, pipelineID)
}

func registerVisiblePathIfSupported(fa FileAccess, path string) {
	registrar, ok := fa.(visiblePathRegistrar)
	if !ok {
		return
	}
	registrar.RegisterVisiblePath(strings.TrimSpace(path))
}

func mergeWorkspaceWriteRepair(base, extra *WorkspaceWriteRepair) *WorkspaceWriteRepair {
	base, extra = normalizeWorkspaceWriteRepairPair(base, extra)
	single := firstWorkspaceWriteRepair(base, extra)
	if base == nil || extra == nil {
		return compactWorkspaceWriteRepair(single)
	}
	return compactWorkspaceWriteRepair(combineWorkspaceWriteRepairs(base, extra))
}

func combineWorkspaceWriteRepairs(base, extra *WorkspaceWriteRepair) *WorkspaceWriteRepair {
	return &WorkspaceWriteRepair{
		Refreshed:     base.Refreshed || extra.Refreshed,
		Rebound:       base.Rebound || extra.Rebound,
		OriginalPath:  firstNonEmpty(base.OriginalPath, extra.OriginalPath),
		EffectivePath: firstNonEmpty(extra.EffectivePath, base.EffectivePath),
		Reasons:       append(append([]string{}, base.Reasons...), extra.Reasons...),
	}
}

func compactWorkspaceWriteRepair(repair *WorkspaceWriteRepair) *WorkspaceWriteRepair {
	if repair == nil {
		return nil
	}
	repair.Reasons = uniqueWorkspaceWriteReasons(repair.Reasons)
	if workspaceWriteRepairEmpty(repair) {
		return nil
	}
	return repair
}

func normalizeWorkspaceWriteRepairPair(base, extra *WorkspaceWriteRepair) (*WorkspaceWriteRepair, *WorkspaceWriteRepair) {
	return compactWorkspaceWriteRepair(base), compactWorkspaceWriteRepair(extra)
}

func firstWorkspaceWriteRepair(values ...*WorkspaceWriteRepair) *WorkspaceWriteRepair {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func workspaceWriteRepairEmpty(repair *WorkspaceWriteRepair) bool {
	if repair == nil {
		return false
	}
	return !workspaceWriteRepairHasState(repair) &&
		!workspaceWriteRepairHasPaths(repair) &&
		len(repair.Reasons) == 0
}

func workspaceWriteRepairHasState(repair *WorkspaceWriteRepair) bool {
	return repair != nil && (repair.Refreshed || repair.Rebound)
}

func workspaceWriteRepairHasPaths(repair *WorkspaceWriteRepair) bool {
	return repair != nil && (repair.OriginalPath != "" || repair.EffectivePath != "")
}

func uniqueWorkspaceWriteReasons(reasons []string) []string {
	if len(reasons) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(reasons))
	ordered := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		trimmed := strings.TrimSpace(reason)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		ordered = append(ordered, trimmed)
	}
	return ordered
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeWorkspaceMutationPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	return filepath.Clean(trimmed)
}

func buildWorkspaceWriteBasis(scope WorkspaceWriteScope, state *WorkspacePathState, preparedAt time.Time) WorkspaceWriteBasis {
	basis := WorkspaceWriteBasis{
		Scope:          scope,
		Path:           workspaceStatePath(state),
		PipelineID:     workspaceStatePipelineID(state),
		TargetView:     writeScopeTargetView(scope),
		PreparedAt:     preparedAt,
		LeaseExpiresAt: workspaceWriteLeaseExpiry(preparedAt),
	}
	if state != nil {
		basis.Disk = state.Disk
		basis.Global = cloneWorkspaceLayerState(state.Global)
		basis.Pipeline = cloneWorkspaceLayerState(state.Pipeline)
	}
	return basis
}

func workspaceWriteLeaseExpiry(preparedAt time.Time) time.Time {
	if preparedAt.IsZero() {
		return time.Time{}
	}
	return preparedAt.Add(defaultWorkspaceWriteLease)
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
	if errors.Is(err, ErrFileNotFound) || os.IsNotExist(err) {
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
	if err := validateWriteLease(basis, time.Now().UTC()); err != nil {
		return staleBasisError(scope, path, WorkspaceWriteStaleReasonLeaseExpired, err)
	}
	if err := validateTargetLayerSnapshot(scope, basis, state); err != nil {
		return staleBasisError(scope, path, WorkspaceWriteStaleReasonTargetChanged, err)
	}
	if err := validateReferenceLayers(scope, basis, state); err != nil {
		return staleBasisError(scope, path, WorkspaceWriteStaleReasonReferenceChange, err)
	}
	return nil
}

func validateBasisIdentity(scope WorkspaceWriteScope, path string, basis WorkspaceWriteBasis) error {
	if basis.Scope != scope {
		return fmt.Errorf("basis scope %q does not match %q", basis.Scope, scope)
	}
	if normalizeWorkspaceMutationPath(basis.Path) != normalizeWorkspaceMutationPath(path) {
		return fmt.Errorf("basis path %q does not match %q", basis.Path, path)
	}
	if basis.TargetView != writeScopeTargetView(scope) {
		return fmt.Errorf("basis target_view %q does not match %q", basis.TargetView, writeScopeTargetView(scope))
	}
	return nil
}

func validateTargetLayerSnapshot(scope WorkspaceWriteScope, basis WorkspaceWriteBasis, state *WorkspacePathState) error {
	name, expected, actual := scopedLayerSnapshots(scope, basis, state)
	if expected == nil || actual == nil {
		return fmt.Errorf("%s state changed", name)
	}
	return validateLayerSnapshot(name, *expected, *actual)
}

func validateReferenceLayers(scope WorkspaceWriteScope, basis WorkspaceWriteBasis, state *WorkspacePathState) error {
	leaseActive := basisLeaseActive(time.Now().UTC(), basis)
	if err := validateReferenceLayerSnapshot("disk", &basis.Disk, &state.Disk, leaseActive); err != nil {
		return err
	}
	if scope == WorkspaceWriteScopeGlobal {
		return nil
	}
	return validateReferenceLayerSnapshot("global", basis.Global, state.Global, leaseActive)
}

func validateReferenceLayerSnapshot(name string, expected, actual *WorkspaceLayerState, leaseActive bool) error {
	if expected == nil && actual == nil {
		return nil
	}
	if expected == nil || actual == nil {
		if leaseActive {
			return nil
		}
		return fmt.Errorf("%s state changed", name)
	}
	if layersComparableForReference(*expected, *actual) {
		return validateComparableLayerSnapshot(name, *expected, *actual)
	}
	if leaseActive {
		return nil
	}
	return validateLayerSnapshot(name, *expected, *actual)
}

func layersComparableForReference(expected, actual WorkspaceLayerState) bool {
	return expected.Available &&
		actual.Available &&
		strings.TrimSpace(expected.Error) == "" &&
		strings.TrimSpace(actual.Error) == ""
}

func validateComparableLayerSnapshot(name string, expected, actual WorkspaceLayerState) error {
	if expected.Exists != actual.Exists {
		return fmt.Errorf("%s existence changed", name)
	}
	if !expected.Exists && !actual.Exists {
		return nil
	}
	if expected.IsDir != actual.IsDir {
		return fmt.Errorf("%s path type changed", name)
	}
	if expected.IsDir && actual.IsDir {
		return nil
	}
	if expected.ContentHash != actual.ContentHash {
		return fmt.Errorf("%s content changed", name)
	}
	return nil
}

func validateLayerSnapshot(name string, expected, actual WorkspaceLayerState) error {
	if expected.Available != actual.Available {
		return fmt.Errorf("%s availability changed", name)
	}
	if expected.Exists != actual.Exists {
		return fmt.Errorf("%s existence changed", name)
	}
	if expected.IsDir != actual.IsDir {
		return fmt.Errorf("%s path type changed", name)
	}
	if strings.TrimSpace(expected.Error) != strings.TrimSpace(actual.Error) {
		return fmt.Errorf("%s error state changed", name)
	}
	if expected.IsDir && actual.IsDir {
		return nil
	}
	if expected.ContentHash != actual.ContentHash {
		return fmt.Errorf("%s content changed", name)
	}
	return nil
}

func scopedLayerSnapshots(
	scope WorkspaceWriteScope,
	basis WorkspaceWriteBasis,
	state *WorkspacePathState,
) (string, *WorkspaceLayerState, *WorkspaceLayerState) {
	if scope == WorkspaceWriteScopePipeline {
		return "pipeline", basis.Pipeline, state.Pipeline
	}
	return "global", basis.Global, state.Global
}

func validateWriteLease(basis WorkspaceWriteBasis, now time.Time) error {
	deadline := basisLeaseDeadline(basis)
	if deadline.IsZero() || now.After(deadline) {
		return ErrWorkspaceWriteLeaseExpired
	}
	return nil
}

func basisLeaseActive(now time.Time, basis WorkspaceWriteBasis) bool {
	deadline := basisLeaseDeadline(basis)
	return !deadline.IsZero() && !now.After(deadline)
}

func basisLeaseDeadline(basis WorkspaceWriteBasis) time.Time {
	if !basis.LeaseExpiresAt.IsZero() {
		return basis.LeaseExpiresAt
	}
	return workspaceWriteLeaseExpiry(basis.PreparedAt)
}

func staleBasisError(
	scope WorkspaceWriteScope,
	path string,
	reason WorkspaceWriteStaleReason,
	cause error,
) error {
	return &WorkspaceWriteStaleError{
		Scope:  scope,
		Path:   strings.TrimSpace(path),
		Reason: reason,
		Cause:  cause,
	}
}

func normalizeFileEdits(inputs []FileEditInput) ([]FileEdit, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("at least one edit is required")
	}
	edits := make([]FileEdit, 0, len(inputs))
	for i, edit := range inputs {
		if strings.TrimSpace(edit.OldText) == "" {
			return nil, fmt.Errorf("edit %d: old_text is required; edit tools only support precise search/replace edits, so read the current file and include the exact text to replace or use the matching write_* tool for a broader rewrite", i)
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

func buildRefreshedWorkspaceWriteBasis(
	ctx context.Context,
	views WorkspaceViewAccess,
	scope WorkspaceWriteScope,
	path string,
	pipelineID string,
) *WorkspaceWriteBasis {
	if views == nil {
		return nil
	}
	state, err := views.InspectPath(ctx, path, pipelineID)
	if err != nil {
		return nil
	}
	if err := ensureWriteTargetAvailable(scope, state); err != nil {
		return nil
	}
	preparedAt := time.Now().UTC()
	basis := buildWorkspaceWriteBasis(scope, state, preparedAt)
	return &basis
}

func RefreshWorkspaceWriteBasis(
	ctx context.Context,
	views WorkspaceViewAccess,
	scope WorkspaceWriteScope,
	path string,
	pipelineID string,
) (WorkspaceWriteBasis, error) {
	basis := buildRefreshedWorkspaceWriteBasis(ctx, views, scope, path, pipelineID)
	if basis == nil {
		return WorkspaceWriteBasis{}, fmt.Errorf("unable to refresh %s write basis for %s", scope, strings.TrimSpace(path))
	}
	return *basis, nil
}

func attachRefreshedWorkspaceWriteBasis(
	ctx context.Context,
	result map[string]any,
	views WorkspaceViewAccess,
	scope WorkspaceWriteScope,
	path string,
	pipelineID string,
) {
	if result == nil {
		return
	}
	if basis := buildRefreshedWorkspaceWriteBasis(ctx, views, scope, path, pipelineID); basis != nil {
		result["next_basis"] = basis
	}
}

func attachWorkspaceWriteRepair(result map[string]any, repair *WorkspaceWriteRepair) {
	if result == nil || repair == nil {
		return
	}
	result["basis_repair"] = repair
}

func changeContentHash(mod FileModification) string {
	if mod.ContentHash.IsZero() {
		return ""
	}
	return mod.ContentHash.String()
}
