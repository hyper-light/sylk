package engineer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/skills"
)

func (e *Engineer) registerCoreSkills() {
	e.skills.Register(readFileSkill(e))
	e.skills.Register(writeFileSkill(e))
	e.skills.Register(editFileSkill(e))
	e.skills.Register(runCommandSkill(e))
	e.skills.Register(runTestsSkill(e))
	e.skills.Register(globSkill(e))
	e.skills.Register(grepSkill(e))
}

// =============================================================================
// read_file - Read file contents
// =============================================================================

type readFileParams struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func readFileSkill(e *Engineer) *skills.Skill {
	return skills.NewSkill("read_file").
		Description("Read the contents of a file. Returns file content with optional offset and limit for large files.").
		Domain("filesystem").
		Keywords("read", "file", "content", "view", "cat").
		Priority(100).
		StringParam("path", "Path to the file to read (relative to working directory)", true).
		IntParam("offset", "Line offset to start reading from (0-based)", false).
		IntParam("limit", "Maximum number of lines to read (default: 1000)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params readFileParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Path == "" {
				return nil, fmt.Errorf("path is required")
			}

			fullPath := resolvePath(e.config.EngineerConfig.WorkingDirectory, params.Path)

			content, err := os.ReadFile(fullPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}

			lines := strings.Split(string(content), "\n")

			// Apply offset and limit
			offset := params.Offset
			if offset < 0 {
				offset = 0
			}
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
			truncated := false
			if endLine > len(lines) {
				endLine = len(lines)
			} else {
				truncated = true
			}

			selectedLines := lines[offset:endLine]

			return map[string]any{
				"path":        params.Path,
				"content":     strings.Join(selectedLines, "\n"),
				"total_lines": len(lines),
				"offset":      offset,
				"limit":       limit,
				"truncated":   truncated,
			}, nil
		}).
		Build()
}

// =============================================================================
// write_file - Write/create file
// =============================================================================

type writeFileParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func writeFileSkill(e *Engineer) *skills.Skill {
	return skills.NewSkill("write_file").
		Description("Write content to a file. Creates the file if it doesn't exist, overwrites if it does.").
		Domain("filesystem").
		Keywords("write", "file", "create", "save", "output").
		Priority(95).
		StringParam("path", "Path to the file to write (relative to working directory)", true).
		StringParam("content", "Content to write to the file", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params writeFileParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Path == "" {
				return nil, fmt.Errorf("path is required")
			}

			if !e.config.EngineerConfig.EnableFileWrites {
				return nil, fmt.Errorf("file writes are disabled")
			}

			fullPath := resolvePath(e.config.EngineerConfig.WorkingDirectory, params.Path)

			// Ensure directory exists
			dir := filepath.Dir(fullPath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("failed to create directory: %w", err)
			}

			// Check if file exists for action type
			_, err := os.Stat(fullPath)
			action := FileActionCreate
			if err == nil {
				action = FileActionModify
			}

			if err := os.WriteFile(fullPath, []byte(params.Content), 0644); err != nil {
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

// =============================================================================
// edit_file - Edit specific sections of a file
// =============================================================================

type editFileParams struct {
	Path  string     `json:"path"`
	Edits []editSpec `json:"edits"`
}

type editSpec struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

func editFileSkill(e *Engineer) *skills.Skill {
	return skills.NewSkill("edit_file").
		Description("Edit specific sections of a file using search and replace. Each edit specifies old text to find and new text to replace it with.").
		Domain("filesystem").
		Keywords("edit", "modify", "replace", "change", "update").
		Priority(90).
		StringParam("path", "Path to the file to edit (relative to working directory)", true).
		ArrayParam("edits", "List of edits to apply, each with old_text and new_text", "object", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params editFileParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Path == "" {
				return nil, fmt.Errorf("path is required")
			}

			if len(params.Edits) == 0 {
				return nil, fmt.Errorf("at least one edit is required")
			}

			if !e.config.EngineerConfig.EnableFileWrites {
				return nil, fmt.Errorf("file writes are disabled")
			}

			fullPath := resolvePath(e.config.EngineerConfig.WorkingDirectory, params.Path)

			content, err := os.ReadFile(fullPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}

			originalContent := string(content)
			modifiedContent := originalContent
			editsApplied := 0

			for i, edit := range params.Edits {
				if edit.OldText == "" {
					return nil, fmt.Errorf("edit %d: old_text is required", i)
				}

				if !strings.Contains(modifiedContent, edit.OldText) {
					return nil, fmt.Errorf("edit %d: old_text not found in file", i)
				}

				modifiedContent = strings.Replace(modifiedContent, edit.OldText, edit.NewText, 1)
				editsApplied++
			}

			if err := os.WriteFile(fullPath, []byte(modifiedContent), 0644); err != nil {
				return nil, fmt.Errorf("failed to write file: %w", err)
			}

			// Calculate diff stats
			oldLines := strings.Split(originalContent, "\n")
			newLines := strings.Split(modifiedContent, "\n")

			return map[string]any{
				"path":          params.Path,
				"edits_applied": editsApplied,
				"lines_before":  len(oldLines),
				"lines_after":   len(newLines),
				"success":       true,
			}, nil
		}).
		Build()
}

// =============================================================================
// run_command - Execute shell command
// =============================================================================

type runCommandParams struct {
	Command    string `json:"command"`
	WorkingDir string `json:"working_dir,omitempty"`
	TimeoutMs  int    `json:"timeout_ms,omitempty"`
}

func runCommandSkill(e *Engineer) *skills.Skill {
	return skills.NewSkill("run_command").
		Description("Execute a shell command. Only approved command patterns are allowed for safety.").
		Domain("code").
		Keywords("run", "execute", "command", "shell", "bash").
		Priority(85).
		StringParam("command", "Command to execute", true).
		StringParam("working_dir", "Working directory for command execution", false).
		IntParam("timeout_ms", "Command timeout in milliseconds (default: 30000)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params runCommandParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Command == "" {
				return nil, fmt.Errorf("command is required")
			}

			if !e.config.EngineerConfig.EnableCommands {
				return nil, fmt.Errorf("command execution is disabled")
			}

			// Check if command is approved
			if !isCommandApproved(params.Command, e.config.EngineerConfig.ApprovedCommands) {
				return nil, fmt.Errorf("command not approved: %s", params.Command)
			}

			// Check blocklist
			if isCommandBlocked(params.Command, e.config.EngineerConfig.ApprovedCommands) {
				return nil, fmt.Errorf("command is blocked: %s", params.Command)
			}

			workDir := params.WorkingDir
			if workDir == "" {
				workDir = e.config.EngineerConfig.WorkingDirectory
			}

			timeout := time.Duration(params.TimeoutMs) * time.Millisecond
			if timeout <= 0 {
				timeout = e.config.EngineerConfig.CommandTimeout
			}

			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			cmd := exec.CommandContext(ctx, "sh", "-c", params.Command)
			cmd.Dir = workDir

			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			startTime := time.Now()
			err := cmd.Run()
			duration := time.Since(startTime)

			exitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					return nil, fmt.Errorf("failed to execute command: %w", err)
				}
			}

			return &CommandExecution{
				Command:    params.Command,
				ExitCode:   exitCode,
				Stdout:     stdout.String(),
				Stderr:     stderr.String(),
				Duration:   duration,
				StartTime:  startTime,
				WorkingDir: workDir,
			}, nil
		}).
		Build()
}

func isCommandApproved(command string, approved ApprovedCommandPatterns) bool {
	for _, pattern := range approved.Patterns {
		matched, err := regexp.MatchString(pattern, command)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func isCommandBlocked(command string, approved ApprovedCommandPatterns) bool {
	for _, pattern := range approved.Blocklist {
		matched, err := regexp.MatchString(pattern, command)
		if err == nil && matched {
			return true
		}
	}
	return false
}

// =============================================================================
// run_tests - Run project tests
// =============================================================================

type runTestsParams struct {
	Pattern  string `json:"pattern,omitempty"`
	Verbose  bool   `json:"verbose,omitempty"`
	Coverage bool   `json:"coverage,omitempty"`
}

func runTestsSkill(e *Engineer) *skills.Skill {
	return skills.NewSkill("run_tests").
		Description("Run project tests. Supports pattern matching, verbose output, and coverage reporting.").
		Domain("testing").
		Keywords("test", "run", "check", "verify", "coverage").
		Priority(80).
		StringParam("pattern", "Test pattern to run (e.g., './...' for all, './pkg/...' for specific package)", false).
		BoolParam("verbose", "Enable verbose test output", false).
		BoolParam("coverage", "Enable coverage reporting", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params runTestsParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if !e.config.EngineerConfig.EnableCommands {
				return nil, fmt.Errorf("command execution is disabled")
			}

			// Build test command
			args := []string{"test"}

			if params.Pattern != "" {
				args = append(args, params.Pattern)
			} else {
				args = append(args, "./...")
			}

			if params.Verbose {
				args = append(args, "-v")
			}

			if params.Coverage {
				args = append(args, "-cover")
			}

			command := "go " + strings.Join(args, " ")

			ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()

			cmd := exec.CommandContext(ctx, "go", args...)
			cmd.Dir = e.config.EngineerConfig.WorkingDirectory

			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			startTime := time.Now()
			err := cmd.Run()
			duration := time.Since(startTime)

			exitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				}
			}

			// Parse test results
			output := stdout.String()
			passed := exitCode == 0
			testCount := countTestsInOutput(output)

			return map[string]any{
				"command":   command,
				"passed":    passed,
				"exit_code": exitCode,
				"tests_run": testCount,
				"duration":  duration.String(),
				"stdout":    output,
				"stderr":    stderr.String(),
			}, nil
		}).
		Build()
}

func countTestsInOutput(output string) int {
	// Count "--- PASS:" and "--- FAIL:" lines
	count := 0
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "--- PASS:") || strings.Contains(line, "--- FAIL:") {
			count++
		}
	}
	return count
}

// =============================================================================
// glob - Find files by pattern
// =============================================================================

type globParams struct {
	Pattern string   `json:"pattern"`
	Exclude []string `json:"exclude,omitempty"`
}

func globSkill(e *Engineer) *skills.Skill {
	return skills.NewSkill("glob").
		Description("Find files matching a glob pattern. Supports ** for recursive matching.").
		Domain("filesystem").
		Keywords("glob", "find", "files", "pattern", "match").
		Priority(75).
		StringParam("pattern", "Glob pattern (e.g., '**/*.go', 'src/**/*.ts')", true).
		ArrayParam("exclude", "Patterns to exclude (e.g., 'vendor/**', 'node_modules/**')", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params globParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Pattern == "" {
				return nil, fmt.Errorf("pattern is required")
			}

			workDir := e.config.EngineerConfig.WorkingDirectory
			if workDir == "" {
				workDir = "."
			}

			matches, err := findFilesGlob(workDir, params.Pattern, params.Exclude)
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

func findFilesGlob(root, pattern string, exclude []string) ([]string, error) {
	var matches []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		// Check exclusions
		for _, excl := range exclude {
			matched, _ := filepath.Match(excl, relPath)
			if matched {
				return nil
			}
			// Also check with doublestar-style matching for directories
			if strings.Contains(excl, "**") {
				excl = strings.ReplaceAll(excl, "**", "*")
				matched, _ = filepath.Match(excl, relPath)
				if matched {
					return nil
				}
			}
		}

		// Check pattern match
		matched, err := filepath.Match(pattern, relPath)
		if err != nil {
			// Try simpler matching for complex patterns
			if strings.Contains(pattern, "**") {
				// Handle ** patterns
				simplePat := strings.ReplaceAll(pattern, "**", "*")
				matched, _ = filepath.Match(simplePat, relPath)
			}
		}

		if matched {
			matches = append(matches, relPath)
		} else if strings.Contains(pattern, "**") {
			// For **/*.go patterns, check if file extension matches
			parts := strings.Split(pattern, "/")
			if len(parts) > 0 {
				lastPart := parts[len(parts)-1]
				if strings.HasPrefix(lastPart, "*.") {
					ext := strings.TrimPrefix(lastPart, "*")
					if strings.HasSuffix(relPath, ext) {
						matches = append(matches, relPath)
					}
				}
			}
		}

		return nil
	})

	return matches, err
}

// =============================================================================
// grep - Search file contents
// =============================================================================

type grepParams struct {
	Pattern      string `json:"pattern"`
	Path         string `json:"path,omitempty"`
	Include      string `json:"include,omitempty"`
	ContextLines int    `json:"context_lines,omitempty"`
	MaxMatches   int    `json:"max_matches,omitempty"`
}

type grepMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
	Context string `json:"context,omitempty"`
}

func grepSkill(e *Engineer) *skills.Skill {
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
			var params grepParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.Pattern == "" {
				return nil, fmt.Errorf("pattern is required")
			}

			regex, err := regexp.Compile(params.Pattern)
			if err != nil {
				return nil, fmt.Errorf("invalid regex pattern: %w", err)
			}

			searchPath := params.Path
			if searchPath == "" {
				searchPath = e.config.EngineerConfig.WorkingDirectory
			}
			if searchPath == "" {
				searchPath = "."
			}

			maxMatches := params.MaxMatches
			if maxMatches <= 0 {
				maxMatches = 100
			}

			matches, err := searchFiles(searchPath, regex, params.Include, params.ContextLines, maxMatches)
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

func searchFiles(root string, pattern *regexp.Regexp, include string, contextLines, maxMatches int) ([]grepMatch, error) {
	var matches []grepMatch

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			// Skip common non-code directories
			if info.Name() == "vendor" || info.Name() == "node_modules" || info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		// Check include pattern
		if include != "" {
			matched, _ := filepath.Match(include, info.Name())
			if !matched {
				return nil
			}
		}

		// Skip binary files (simple heuristic)
		if isBinaryFile(info.Name()) {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		relPath, _ := filepath.Rel(root, path)
		lines := strings.Split(string(content), "\n")

		for i, line := range lines {
			if len(matches) >= maxMatches {
				return filepath.SkipAll
			}

			if pattern.MatchString(line) {
				match := grepMatch{
					File:    relPath,
					Line:    i + 1,
					Content: strings.TrimSpace(line),
				}

				if contextLines > 0 {
					start := i - contextLines
					if start < 0 {
						start = 0
					}
					end := i + contextLines + 1
					if end > len(lines) {
						end = len(lines)
					}
					match.Context = strings.Join(lines[start:end], "\n")
				}

				matches = append(matches, match)
			}
		}

		return nil
	})

	return matches, err
}

func isBinaryFile(name string) bool {
	binaryExts := []string{".exe", ".dll", ".so", ".dylib", ".bin", ".o", ".a", ".png", ".jpg", ".jpeg", ".gif", ".ico", ".pdf", ".zip", ".tar", ".gz"}
	for _, ext := range binaryExts {
		if strings.HasSuffix(strings.ToLower(name), ext) {
			return true
		}
	}
	return false
}

// =============================================================================
// Helpers
// =============================================================================

func resolvePath(workDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if workDir == "" {
		return path
	}
	return filepath.Join(workDir, path)
}
