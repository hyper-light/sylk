package versioning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// NewReadFileSkill creates a read_file skill backed by the given FileAccess.
func NewReadFileSkill(fa FileAccess) *skills.Skill {
	return skills.NewSkill("read_file").
		Description("Read the contents of a file. Returns file content with optional offset and limit for large files.").
		Domain("filesystem").
		Keywords("read", "file", "content", "view", "cat").
		Priority(100).
		StringParam("path", "Path to the file to read (relative to working directory)", true).
		IntParam("offset", "Line offset to start reading from (0-based)", false).
		IntParam("limit", "Maximum number of lines to read (default: 1000)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Path   string `json:"path"`
				Offset int    `json:"offset,omitempty"`
				Limit  int    `json:"limit,omitempty"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.Path == "" {
				return nil, fmt.Errorf("path is required")
			}

			content, err := fa.ReadFile(ctx, params.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}

			lines := strings.Split(string(content), "\n")
			offset := max(0, params.Offset)
			if offset >= len(lines) {
				return map[string]any{
					"path":        params.Path,
					"content":     "",
					"total_lines": len(lines),
					"offset":      offset,
					"truncated":   false,
				}, nil
			}

			limit := params.Limit
			if limit <= 0 {
				limit = 1000
			}
			endLine := offset + limit
			truncated := endLine < len(lines)
			if endLine > len(lines) {
				endLine = len(lines)
			}

			return map[string]any{
				"path":        params.Path,
				"content":     strings.Join(lines[offset:endLine], "\n"),
				"total_lines": len(lines),
				"offset":      offset,
				"limit":       limit,
				"truncated":   truncated,
			}, nil
		}).
		Build()
}

// NewWriteFileSkill creates a write_file skill backed by the given FileAccess.
func NewWriteFileSkill(fa FileAccess) *skills.Skill {
	return skills.NewSkill("write_file").
		Description("Write content to a file. Creates the file if it doesn't exist, overwrites if it does.").
		Domain("filesystem").
		Keywords("write", "file", "create", "save", "output").
		Priority(95).
		StringParam("path", "Path to the file to write (relative to working directory)", true).
		StringParam("content", "Content to write to the file", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.Path == "" {
				return nil, fmt.Errorf("path is required")
			}
			if fa.IsReadOnly() {
				return nil, fmt.Errorf("file writes are disabled")
			}

			exists, _ := fa.Exists(ctx, params.Path)
			action := "create"
			if exists {
				action = "modify"
			}

			if err := fa.WriteFile(ctx, params.Path, []byte(params.Content)); err != nil {
				return nil, fmt.Errorf("failed to write file: %w", err)
			}

			lines := strings.Split(params.Content, "\n")
			return map[string]any{
				"path":    params.Path,
				"action":  action,
				"bytes":   len(params.Content),
				"lines":   len(lines),
				"success": true,
			}, nil
		}).
		Build()
}

// NewEditFileSkill creates an edit_file skill backed by the given FileAccess.
func NewEditFileSkill(fa FileAccess) *skills.Skill {
	return skills.NewSkill("edit_file").
		Description("Edit specific sections of a file using search and replace. Each edit specifies old text to find and new text to replace it with.").
		Domain("filesystem").
		Keywords("edit", "modify", "replace", "change", "update").
		Priority(90).
		StringParam("path", "Path to the file to edit (relative to working directory)", true).
		ArrayParam("edits", "List of edits to apply, each with old_text and new_text", "object", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Path  string `json:"path"`
				Edits []struct {
					OldText string `json:"old_text"`
					NewText string `json:"new_text"`
				} `json:"edits"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.Path == "" {
				return nil, fmt.Errorf("path is required")
			}
			if len(params.Edits) == 0 {
				return nil, fmt.Errorf("at least one edit is required")
			}
			if fa.IsReadOnly() {
				return nil, fmt.Errorf("file writes are disabled")
			}

			// Read current content for diff stats.
			oldContent, err := fa.ReadFile(ctx, params.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}
			oldLines := strings.Split(string(oldContent), "\n")

			edits := make([]FileEdit, len(params.Edits))
			for i, e := range params.Edits {
				if e.OldText == "" {
					return nil, fmt.Errorf("edit %d: old_text is required", i)
				}
				edits[i] = FileEdit{OldText: e.OldText, NewText: e.NewText}
			}

			if err := fa.EditFile(ctx, params.Path, edits); err != nil {
				return nil, err
			}

			newContent, _ := fa.ReadFile(ctx, params.Path)
			newLines := strings.Split(string(newContent), "\n")

			return map[string]any{
				"path":          params.Path,
				"edits_applied": len(edits),
				"lines_before":  len(oldLines),
				"lines_after":   len(newLines),
				"success":       true,
			}, nil
		}).
		Build()
}

// NewGlobSkill creates a glob skill backed by the given FileAccess.
func NewGlobSkill(fa FileAccess) *skills.Skill {
	return skills.NewSkill("glob").
		Description("Find files matching a glob pattern. Supports ** for recursive matching.").
		Domain("filesystem").
		Keywords("glob", "find", "files", "pattern", "match").
		Priority(75).
		StringParam("pattern", "Glob pattern (e.g., '**/*.go', 'src/**/*.ts')", true).
		ArrayParam("exclude", "Patterns to exclude (e.g., 'vendor/**', 'node_modules/**')", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Pattern string   `json:"pattern"`
				Exclude []string `json:"exclude,omitempty"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.Pattern == "" {
				return nil, fmt.Errorf("pattern is required")
			}

			root := fa.WorkingDir()
			if root == "" {
				root = "."
			}

			matches, err := fa.Glob(ctx, root, params.Pattern, params.Exclude)
			if err != nil {
				return nil, fmt.Errorf("glob failed: %w", err)
			}

			return map[string]any{
				"pattern": params.Pattern,
				"matches": matches,
				"count":   len(matches),
			}, nil
		}).
		Build()
}

// NewGrepSkill creates a grep skill backed by the given FileAccess.
func NewGrepSkill(fa FileAccess) *skills.Skill {
	return skills.NewSkill("grep").
		Description("Search file contents using regular expressions. Returns matching lines with optional context.").
		Domain("code").
		Keywords("grep", "search", "find", "regex", "pattern").
		Priority(70).
		StringParam("pattern", "Regular expression pattern to search for", true).
		StringParam("path", "Directory path to search in (default: working directory)", false).
		StringParam("include", "File pattern to include (e.g., '*.go', '*.ts')", false).
		IntParam("context_lines", "Number of context lines to include before/after match", false).
		IntParam("max_matches", "Maximum number of matches to return (default: 100)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Pattern      string `json:"pattern"`
				Path         string `json:"path,omitempty"`
				Include      string `json:"include,omitempty"`
				ContextLines int    `json:"context_lines,omitempty"`
				MaxMatches   int    `json:"max_matches,omitempty"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if params.Pattern == "" {
				return nil, fmt.Errorf("pattern is required")
			}

			searchPath := params.Path
			if searchPath == "" {
				searchPath = fa.WorkingDir()
			}
			if searchPath == "" {
				searchPath = "."
			}

			maxMatches := params.MaxMatches
			if maxMatches <= 0 {
				maxMatches = 100
			}

			matches, err := fa.Grep(ctx, searchPath, params.Pattern, params.Include, params.ContextLines, maxMatches)
			if err != nil {
				return nil, fmt.Errorf("search failed: %w", err)
			}

			return map[string]any{
				"pattern":   params.Pattern,
				"matches":   matches,
				"count":     len(matches),
				"truncated": len(matches) >= maxMatches,
			}, nil
		}).
		Build()
}
