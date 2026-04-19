package versioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// WorkspaceViewAccessFunc resolves the current workspace-view accessor.
type WorkspaceViewAccessFunc func() WorkspaceViewAccess

// DefaultPipelineIDFunc returns the current task pipeline ID when one exists.
type DefaultPipelineIDFunc func() string

// probeWorkspaceWriteSurface probes the FileAccess that a subsequent write
// tool would resolve, surfacing ErrVFSNotFound to the LLM during prepare
// rather than later at the actual write call. Without this, prepare passes
// (it inspects via workspace views with disk fallback) and the write fails
// with "VFS not found" — the historical "edit_pipeline_file failed: VFS
// not found" symptom. nil viewsAccess is fine: the prepare's view-only
// inspection still runs.
func probeWorkspaceWriteSurface(ctx context.Context, views WorkspaceViewAccess, view WorkspaceView, pipelineID string) error {
	if views == nil {
		return nil
	}
	probe, ok := views.(workspaceWriteSurfaceProber)
	if !ok {
		return nil
	}
	return probe.ProbeWriteSurface(ctx, view, pipelineID)
}

// workspaceWriteSurfaceProber is the optional capability a WorkspaceViewAccess
// implements to let prepare confirm the write surface before the LLM emits a
// write tool call. SessionWorkspaceViews implements this; ad-hoc test stubs
// can omit it.
type workspaceWriteSurfaceProber interface {
	ProbeWriteSurface(ctx context.Context, view WorkspaceView, pipelineID string) error
}

// NewReadWorkspaceFileSkill creates a view-aware read skill so agents can
// compare disk, session-global, and task-local pipeline state explicitly.
func NewReadWorkspaceFileSkill(getViews WorkspaceViewAccessFunc, defaultPipelineID DefaultPipelineIDFunc) *skills.Skill {
	return skills.NewSkill("read_workspace_file").
		Description("Read file contents from an explicit workspace view: disk (committed source of truth), global (merged but uncommitted session state), or pipeline (task-local unmerged state).").
		Domain("filesystem").
		Keywords("workspace", "disk", "global", "pipeline", "read", "compare").
		Priority(92).
		Usage("Use to inspect the actual state of a path before reasoning about edits. If the file is absent, the skill returns structured missing metadata so create-new-file flows can continue without treating absence as a hard failure.").
		Satisfies("Provides concrete workspace evidence for planning, inspection, implementation, or test synthesis.").
		Avoid("Do not assume the disk view and pipeline view match; choose the view intentionally for the question you are answering.").
		EnumParam("view", "Workspace view to read from", []string{string(WorkspaceViewDisk), string(WorkspaceViewGlobal), string(WorkspaceViewPipeline)}, true).
		StringParam("path", "Path to the file to read", true).
		StringParam("pipeline_id", "Task pipeline ID when reading pipeline-local state", false).
		IntParam("offset", "Line offset to start reading from (0-based)", false).
		IntParam("limit", "Maximum number of lines to read (default: 1000)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				View       string `json:"view"`
				Path       string `json:"path"`
				PipelineID string `json:"pipeline_id,omitempty"`
				Offset     int    `json:"offset,omitempty"`
				Limit      int    `json:"limit,omitempty"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Path) == "" {
				return nil, fmt.Errorf("path is required")
			}
			views := getViews()
			if views == nil {
				return nil, fmt.Errorf("workspace views are unavailable")
			}
			content, err := views.ReadFile(ctx, WorkspaceView(params.View), params.Path, resolveSkillPipelineID(params.PipelineID, defaultPipelineID))
			if err != nil {
				if errors.Is(err, ErrFileNotFound) || os.IsNotExist(err) {
					return missingWorkspaceContent(params.Path, params.View, params.Offset, params.Limit), nil
				}
				if isUnavailableWorkspaceViewError(err) {
					return unavailableWorkspaceContent(params.Path, params.View, params.Offset, params.Limit, err), nil
				}
				return nil, err
			}
			return sliceWorkspaceContent(params.Path, params.View, content, params.Offset, params.Limit), nil
		}).
		Build()
}

// NewWorkspaceGlobSkill creates a view-aware glob skill.
func NewWorkspaceGlobSkill(getViews WorkspaceViewAccessFunc, defaultPipelineID DefaultPipelineIDFunc) *skills.Skill {
	return skills.NewSkill("workspace_glob").
		Description("Find files from an explicit workspace view: disk, global session overlay, or a task pipeline overlay.").
		Domain("filesystem").
		Keywords("workspace", "disk", "global", "pipeline", "glob", "find").
		Priority(86).
		EnumParam("view", "Workspace view to search", []string{string(WorkspaceViewDisk), string(WorkspaceViewGlobal), string(WorkspaceViewPipeline)}, true).
		StringParam("pattern", "Glob pattern such as '**/*.go'", true).
		StringParam("path", "Base directory to search from", false).
		ArrayParam("exclude", "Excluded glob patterns", "string", false).
		StringParam("pipeline_id", "Task pipeline ID when searching pipeline-local state", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				View       string   `json:"view"`
				Pattern    string   `json:"pattern"`
				Path       string   `json:"path,omitempty"`
				Exclude    []string `json:"exclude,omitempty"`
				PipelineID string   `json:"pipeline_id,omitempty"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Pattern) == "" {
				return nil, fmt.Errorf("pattern is required")
			}
			views := getViews()
			if views == nil {
				return nil, fmt.Errorf("workspace views are unavailable")
			}
			matches, err := views.Glob(ctx, WorkspaceView(params.View), params.Path, params.Pattern, params.Exclude, resolveSkillPipelineID(params.PipelineID, defaultPipelineID))
			if err != nil {
				if isUnavailableWorkspaceViewError(err) {
					return unavailableWorkspaceGlob(params.View, params.Pattern, err), nil
				}
				return nil, err
			}
			return map[string]any{
				"view":    params.View,
				"pattern": params.Pattern,
				"matches": matches,
				"count":   len(matches),
			}, nil
		}).
		Build()
}

// NewWorkspaceGrepSkill creates a view-aware grep skill.
func NewWorkspaceGrepSkill(getViews WorkspaceViewAccessFunc, defaultPipelineID DefaultPipelineIDFunc) *skills.Skill {
	return skills.NewSkill("workspace_grep").
		Description("Search file contents from an explicit workspace view: disk, global session overlay, or a task pipeline overlay.").
		Domain("filesystem").
		Keywords("workspace", "disk", "global", "pipeline", "grep", "search").
		Priority(84).
		EnumParam("view", "Workspace view to search", []string{string(WorkspaceViewDisk), string(WorkspaceViewGlobal), string(WorkspaceViewPipeline)}, true).
		StringParam("pattern", "Regular expression pattern to search for", true).
		StringParam("path", "Directory path to search in", false).
		StringParam("include", "File pattern to include such as '*.go'", false).
		IntParam("context_lines", "Number of context lines before and after each match", false).
		IntParam("max_matches", "Maximum number of matches to return (default: 100)", false).
		StringParam("pipeline_id", "Task pipeline ID when searching pipeline-local state", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				View         string `json:"view"`
				Pattern      string `json:"pattern"`
				Path         string `json:"path,omitempty"`
				Include      string `json:"include,omitempty"`
				ContextLines int    `json:"context_lines,omitempty"`
				MaxMatches   int    `json:"max_matches,omitempty"`
				PipelineID   string `json:"pipeline_id,omitempty"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Pattern) == "" {
				return nil, fmt.Errorf("pattern is required")
			}
			views := getViews()
			if views == nil {
				return nil, fmt.Errorf("workspace views are unavailable")
			}
			maxMatches := params.MaxMatches
			if maxMatches <= 0 {
				maxMatches = 100
			}
			matches, err := views.Grep(
				ctx,
				WorkspaceView(params.View),
				params.Path,
				params.Pattern,
				params.Include,
				params.ContextLines,
				maxMatches,
				resolveSkillPipelineID(params.PipelineID, defaultPipelineID),
			)
			if err != nil {
				if isUnavailableWorkspaceViewError(err) {
					return unavailableWorkspaceGrep(params.View, params.Pattern, err), nil
				}
				return nil, err
			}
			return map[string]any{
				"view":      params.View,
				"pattern":   params.Pattern,
				"matches":   matches,
				"count":     len(matches),
				"truncated": len(matches) >= maxMatches,
			}, nil
		}).
		Build()
}

// NewInspectWorkspaceStateSkill creates a skill that compares a path across
// disk, global, and optional pipeline state.
func NewInspectWorkspaceStateSkill(getViews WorkspaceViewAccessFunc, defaultPipelineID DefaultPipelineIDFunc) *skills.Skill {
	return skills.NewSkill("inspect_workspace_state").
		Description("Compare a path across disk (committed source of truth), global session overlay, and task-local pipeline state.").
		Domain("filesystem").
		Keywords("workspace", "state", "compare", "disk", "global", "pipeline", "diff").
		Priority(90).
		Usage("Use when you need to understand whether a path exists, differs, or is only present in a higher overlay before deciding what to validate or mutate.").
		Satisfies("Produces multi-view state evidence for contract synthesis, implementation review, and leased-write decisions.").
		StringParam("path", "Path to inspect across workspace views", true).
		StringParam("pipeline_id", "Task pipeline ID when including pipeline-local state", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Path       string `json:"path"`
				PipelineID string `json:"pipeline_id,omitempty"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Path) == "" {
				return nil, fmt.Errorf("path is required")
			}
			views := getViews()
			if views == nil {
				return nil, fmt.Errorf("workspace views are unavailable")
			}
			return views.InspectPath(ctx, params.Path, resolveSkillPipelineID(params.PipelineID, defaultPipelineID))
		}).
		Build()
}

// NewSummarizeWorkspaceStateSkill creates a bounded multi-path summary skill.
func NewSummarizeWorkspaceStateSkill(getViews WorkspaceViewAccessFunc, defaultPipelineID DefaultPipelineIDFunc) *skills.Skill {
	return skills.NewSkill("summarize_workspace_state").
		Description("Summarize a bounded set of paths across disk (committed source of truth), global session overlay, and task-local pipeline state.").
		Domain("filesystem").
		Keywords("workspace", "summary", "compare", "disk", "global", "pipeline", "changed files", "delta").
		Priority(91).
		Usage("Use for a quick multi-path snapshot before deeper reads or validation. Prefer this over many scattered single-path checks when you are orienting on a task surface.").
		Satisfies("Provides bounded workspace-surface evidence that can seed planning, review, or inspection without reading every file immediately.").
		ArrayParam("paths", "Paths to summarize across workspace views", "string", true).
		StringParam("pipeline_id", "Task pipeline ID when including pipeline-local state", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Paths      []string `json:"paths"`
				PipelineID string   `json:"pipeline_id,omitempty"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if len(params.Paths) == 0 {
				return nil, fmt.Errorf("paths are required")
			}
			views := getViews()
			if views == nil {
				return nil, fmt.Errorf("workspace views are unavailable")
			}
			return views.SummarizePaths(ctx, params.Paths, resolveSkillPipelineID(params.PipelineID, defaultPipelineID))
		}).
		Build()
}

func resolveSkillPipelineID(pipelineID string, defaultPipelineID DefaultPipelineIDFunc) string {
	if trimmed := strings.TrimSpace(pipelineID); trimmed != "" {
		return trimmed
	}
	if defaultPipelineID == nil {
		return ""
	}
	return strings.TrimSpace(defaultPipelineID())
}

func sliceWorkspaceContent(path, view string, content []byte, offset, limit int) map[string]any {
	lines := strings.Split(string(content), "\n")
	start := max(0, offset)
	if limit <= 0 {
		limit = 1000
	}
	if start >= len(lines) {
		return map[string]any{
			"path":        path,
			"view":        view,
			"exists":      true,
			"missing":     false,
			"content":     "",
			"total_lines": len(lines),
			"offset":      start,
			"limit":       limit,
			"truncated":   false,
		}
	}
	end := start + limit
	truncated := end < len(lines)
	if end > len(lines) {
		end = len(lines)
	}
	return map[string]any{
		"path":        path,
		"view":        view,
		"exists":      true,
		"missing":     false,
		"content":     strings.Join(lines[start:end], "\n"),
		"total_lines": len(lines),
		"offset":      start,
		"limit":       limit,
		"truncated":   truncated,
	}
}

func missingWorkspaceContent(path, view string, offset, limit int) map[string]any {
	start := max(0, offset)
	if limit <= 0 {
		limit = 1000
	}
	return map[string]any{
		"path":        path,
		"view":        view,
		"exists":      false,
		"missing":     true,
		"content":     "",
		"total_lines": 0,
		"offset":      start,
		"limit":       limit,
		"truncated":   false,
		"reason":      "file not found in requested workspace view",
	}
}

func unavailableWorkspaceContent(path, view string, offset, limit int, err error) map[string]any {
	start := max(0, offset)
	if limit <= 0 {
		limit = 1000
	}
	return map[string]any{
		"path":        path,
		"view":        view,
		"exists":      false,
		"missing":     false,
		"unavailable": true,
		"content":     "",
		"total_lines": 0,
		"offset":      start,
		"limit":       limit,
		"truncated":   false,
		"reason":      strings.TrimSpace(err.Error()),
	}
}

func unavailableWorkspaceGlob(view, pattern string, err error) map[string]any {
	return map[string]any{
		"view":        view,
		"pattern":     pattern,
		"matches":     []string{},
		"count":       0,
		"unavailable": true,
		"reason":      strings.TrimSpace(err.Error()),
	}
}

func unavailableWorkspaceGrep(view, pattern string, err error) map[string]any {
	return map[string]any{
		"view":        view,
		"pattern":     pattern,
		"matches":     []GrepMatch{},
		"count":       0,
		"truncated":   false,
		"unavailable": true,
		"reason":      strings.TrimSpace(err.Error()),
	}
}

func isUnavailableWorkspaceViewError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrVFSNotFound) {
		return true
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "workspace view") && strings.Contains(message, "unavailable")
}
