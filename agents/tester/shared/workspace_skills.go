package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

// NewTesterReadWorkspaceFileSkill returns a tester-specific workspace read
// skill that treats missing files as valid red-phase state rather than a hard
// tool failure. Test synthesis should continue even when the implementation is
// absent and the resulting tests are expected to fail.
func NewTesterReadWorkspaceFileSkill(
	getViews versioning.WorkspaceViewAccessFunc,
	defaultPipelineID versioning.DefaultPipelineIDFunc,
) *skills.Skill {
	return skills.NewSkill("read_workspace_file").
		Description("Read file contents from an explicit workspace view. Missing files return structured metadata instead of failing so the tester can continue writing red-phase tests.").
		Domain("filesystem").
		Keywords("workspace", "disk", "global", "pipeline", "read", "compare", "missing file").
		Priority(92).
		EnumParam("view", "Workspace view to read from", []string{string(versioning.WorkspaceViewDisk), string(versioning.WorkspaceViewGlobal), string(versioning.WorkspaceViewPipeline)}, true).
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
			content, err := views.ReadFile(
				ctx,
				versioning.WorkspaceView(params.View),
				params.Path,
				resolveTesterPipelineID(params.PipelineID, defaultPipelineID),
			)
			if err != nil {
				if errors.Is(err, versioning.ErrFileNotFound) || os.IsNotExist(err) {
					return missingWorkspaceContent(params.Path, params.View, params.Offset, params.Limit), nil
				}
				return nil, err
			}
			return sliceTesterWorkspaceContent(params.Path, params.View, content, params.Offset, params.Limit), nil
		}).
		Build()
}

func resolveTesterPipelineID(pipelineID string, defaultPipelineID versioning.DefaultPipelineIDFunc) string {
	if trimmed := strings.TrimSpace(pipelineID); trimmed != "" {
		return trimmed
	}
	if defaultPipelineID == nil {
		return ""
	}
	return strings.TrimSpace(defaultPipelineID())
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

func sliceTesterWorkspaceContent(path, view string, content []byte, offset, limit int) map[string]any {
	lines := strings.Split(string(content), "\n")
	start := max(0, offset)
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
	if limit <= 0 {
		limit = 1000
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
