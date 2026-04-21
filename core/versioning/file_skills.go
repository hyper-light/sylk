package versioning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// FileAccessProvider resolves the current FileAccess at invocation time.
type FileAccessProvider func() FileAccess

// NewReadFileSkill creates a read_file skill backed by the given FileAccess.
func NewReadFileSkill(fa FileAccess) *skills.Skill {
	return NewReadFileSkillFunc(func() FileAccess { return fa })
}

// NewReadFileSkillFunc creates a read_file skill backed by a late-bound FileAccess.
func NewReadFileSkillFunc(getFA FileAccessProvider) *skills.Skill {
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

			fa, err := resolveSkillFileAccess(getFA)
			if err != nil {
				return nil, err
			}

			result, err := ReadFileToolResult(ctx, fa, params.Path, params.Offset, params.Limit)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}
			return result, nil
		}).
		Build()
}

// NewWriteFileSkill creates a write_file skill backed by the given FileAccess.
func NewWriteFileSkill(fa FileAccess) *skills.Skill {
	return NewWriteFileSkillFunc(func() FileAccess { return fa })
}

// NewWriteFileSkillFunc creates a write_file skill backed by a late-bound FileAccess.
func NewWriteFileSkillFunc(getFA FileAccessProvider) *skills.Skill {
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

			fa, err := resolveSkillFileAccess(getFA)
			if err != nil {
				return nil, err
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
	return NewEditFileSkillFunc(func() FileAccess { return fa })
}

// NewEditFileSkillFunc creates an edit_file skill backed by a late-bound FileAccess.
func NewEditFileSkillFunc(getFA FileAccessProvider) *skills.Skill {
	return skills.NewSkill("edit_file").
		Description("Edit specific sections of a file using precise search and replace. Each edit must specify exact old_text to find and new_text to replace it with.").
		Domain("filesystem").
		Keywords("edit", "modify", "replace", "change", "update").
		Priority(90).
		StringParam("path", "Path to the file to edit (relative to working directory)", true).
		ArrayObjectParam("edits", "List of search/replace edits to apply. Every item must include exact old_text and new_text.", map[string]*skills.Property{
			"old_text": {
				Type:        "string",
				Description: "Exact current text to find in the file before replacement. Required for every edit.",
			},
			"new_text": {
				Type:        "string",
				Description: "Replacement text for the matched old_text. Use an empty string to delete the matched text.",
			},
		}, []string{"old_text", "new_text"}, true).
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

			edits := make([]FileEdit, len(params.Edits))
			for i, e := range params.Edits {
				if e.OldText == "" {
					return nil, fmt.Errorf("edit %d: old_text is required; edit_file only supports precise search/replace edits, so read the current file and include the exact text to replace or use write_file for a broader rewrite", i)
				}
				edits[i] = FileEdit{OldText: e.OldText, NewText: e.NewText}
			}

			fa, err := resolveSkillFileAccess(getFA)
			if err != nil {
				return nil, err
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

// NewCreateDirectorySkill creates a create_directory skill backed by the given FileAccess.
func NewCreateDirectorySkill(fa FileAccess) *skills.Skill {
	return NewCreateDirectorySkillFunc(func() FileAccess { return fa })
}

// NewCreateDirectorySkillFunc creates a create_directory skill backed by a late-bound FileAccess.
func NewCreateDirectorySkillFunc(getFA FileAccessProvider) *skills.Skill {
	return skills.NewSkill("create_directory").
		Description("Create a directory path in the active workspace or VFS. Missing parent directories are created automatically.").
		Domain("filesystem").
		Keywords("mkdir", "directory", "folder", "create", "path").
		Priority(92).
		StringParam("path", "Directory path to create", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Path) == "" {
				return nil, fmt.Errorf("path is required")
			}

			fa, err := resolveSkillFileAccess(getFA)
			if err != nil {
				return nil, err
			}
			if fa.IsReadOnly() {
				return nil, fmt.Errorf("directory creation is disabled")
			}
			if err := fa.MkdirAll(ctx, params.Path); err != nil {
				return nil, fmt.Errorf("failed to create directory: %w", err)
			}
			return map[string]any{
				"path":    params.Path,
				"created": true,
			}, nil
		}).
		Build()
}

// NewGlobSkill creates a glob skill backed by the given FileAccess.
func NewGlobSkill(fa FileAccess) *skills.Skill {
	return NewGlobSkillFunc(func() FileAccess { return fa })
}

// NewGlobSkillFunc creates a glob skill backed by a late-bound FileAccess.
// Phase 2.K / 10.H refactor: the Func variant lets agents inject their own
// FileAccess provider (which may be VFS-backed, disk-only, or overlay-
// aware) while sharing the handler body.
func NewGlobSkillFunc(getFA FileAccessProvider) *skills.Skill {
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

			fa, err := resolveSkillFileAccess(getFA)
			if err != nil {
				return nil, err
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

func resolveSkillFileAccess(getFA FileAccessProvider) (FileAccess, error) {
	if getFA == nil {
		return nil, fmt.Errorf("file access is unavailable")
	}
	fa := getFA()
	if fa == nil {
		return nil, fmt.Errorf("file access is unavailable")
	}
	return fa, nil
}

// NewGrepSkill creates a grep skill backed by the given FileAccess.
func NewGrepSkill(fa FileAccess) *skills.Skill {
	return NewGrepSkillFunc(func() FileAccess { return fa })
}

// NewGrepSkillFunc creates a grep skill backed by a late-bound FileAccess.
// Phase 2.K / 10.H refactor: the Func variant lets agents inject their own
// FileAccess provider while sharing the handler body.
func NewGrepSkillFunc(getFA FileAccessProvider) *skills.Skill {
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

			fa, err := resolveSkillFileAccess(getFA)
			if err != nil {
				return nil, err
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
