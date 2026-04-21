package architect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

// ---------------------------------------------------------------------------
// read_file
// ---------------------------------------------------------------------------

type readFileParams struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func readFileSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("read_file").
		Description("Read file contents for planning context gathering.").
		Domain("filesystem").
		Keywords("read", "file", "content", "cat", "view").
		Priority(95).
		StringParam("path", "Path to file", true).
		IntParam("offset", "Starting line offset (0-based)", false).
		IntParam("limit", "Max lines to return", false).
		Usage("Use when the Architect needs to examine file contents for planning context — understanding existing code patterns, checking implementation details, or verifying assumptions. Use offset/limit for large files to avoid excessive token consumption. Do NOT read binary files or files larger than 10K lines — they will overwhelm the planning context.").
		Example(`{"path": "core/auth/handler.go", "offset": 0, "limit": 200}`).
		BestPractice("Start with limit=200 for initial file exploration — increase only if the relevant code is deeper in the file.").
		BestPractice("Check total_lines in the response to understand file scope before requesting more content.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params readFileParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Path) == "" {
				return nil, fmt.Errorf("path is required")
			}
			fa := versioning.NewDiskFileAccess(a.config.WorkingDirectory, true)
			result, err := versioning.ReadFileToolResult(ctx, fa, params.Path, params.Offset, params.Limit)
			if err != nil {
				return nil, err
			}
			return result, nil
		}).
		Build()
}

func resolveArchitectPath(root string, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if strings.TrimSpace(root) == "" {
		return path
	}
	return filepath.Join(root, path)
}

func sliceFileContent(path string, content string, offset int, limit int) map[string]any {
	lines := strings.Split(content, "\n")
	start := clampOffset(offset, len(lines))
	max := clampLimit(limit)
	end := start + max
	if end > len(lines) {
		end = len(lines)
	}
	return map[string]any{
		"path":        path,
		"content":     strings.Join(lines[start:end], "\n"),
		"offset":      start,
		"limit":       max,
		"total_lines": len(lines),
		"truncated":   end < len(lines),
	}
}

func clampOffset(offset int, total int) int {
	if offset < 0 {
		return 0
	}
	if offset > total {
		return total
	}
	return offset
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return 1000
	}
	return limit
}

// ---------------------------------------------------------------------------
// glob
// ---------------------------------------------------------------------------

type globParams struct {
	Pattern string   `json:"pattern"`
	Path    string   `json:"path,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
}

func globSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("glob").
		Description("Find files by glob pattern for planning context.").
		Domain("filesystem").
		Keywords("glob", "find files", "pattern", "match").
		Priority(90).
		StringParam("pattern", "Glob pattern", true).
		StringParam("path", "Base path", false).
		ArrayParam("exclude", "Excluded glob patterns", "string", false).
		Usage("Use to discover files matching a pattern for planning context — finding all files of a type, locating test files, or identifying module structure. Use exclude patterns to skip vendor/node_modules/generated directories. Do NOT use for content search — use `grep` instead.").
		Example(`{"pattern": "**/*.go", "path": "core/auth", "exclude": ["*_test.go", "vendor/**"]}`).
		BestPractice("Always exclude vendor, node_modules, and .git directories to avoid irrelevant matches.").
		BestPractice("Use specific subdirectory paths to narrow results — globbing the entire repo is expensive for large codebases.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params globParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Pattern) == "" {
				return nil, fmt.Errorf("pattern is required")
			}
			root := resolveGlobRoot(a.config.WorkingDirectory, params.Path)
			matches, err := runGlobSearch(root, params.Pattern, params.Exclude)
			if err != nil {
				return nil, err
			}
			return map[string]any{"pattern": params.Pattern, "matches": matches, "count": len(matches)}, nil
		}).
		Build()
}

func resolveGlobRoot(workDir string, path string) string {
	if strings.TrimSpace(path) == "" {
		return workDir
	}
	return resolveArchitectPath(workDir, path)
}

func runGlobSearch(root string, pattern string, exclude []string) ([]string, error) {
	results := make([]string, 0)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info == nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if shouldExcludePath(rel, exclude) {
			return nil
		}
		if matchesGlob(pattern, rel) {
			results = append(results, rel)
		}
		return nil
	})
	return results, err
}

func shouldExcludePath(path string, excludes []string) bool {
	for _, pattern := range excludes {
		if matchesGlob(pattern, path) {
			return true
		}
	}
	return false
}

func matchesGlob(pattern string, candidate string) bool {
	matched, err := filepath.Match(pattern, candidate)
	if err == nil && matched {
		return true
	}
	if strings.Contains(pattern, "**") {
		simplified := strings.ReplaceAll(pattern, "**", "*")
		matched, _ = filepath.Match(simplified, candidate)
		return matched
	}
	return false
}

// ---------------------------------------------------------------------------
// grep
// ---------------------------------------------------------------------------

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
}

func grepSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("grep").
		Description("Search files with regex for planning context extraction.").
		Domain("filesystem").
		Keywords("grep", "search", "regex", "find text").
		Priority(90).
		StringParam("pattern", "Regular expression pattern", true).
		StringParam("path", "Path to search", false).
		StringParam("include", "Include filename pattern", false).
		IntParam("context_lines", "Unused compatibility field", false).
		IntParam("max_matches", "Maximum number of matches", false).
		Usage("Use to find specific patterns, function definitions, interface implementations, or string occurrences across the codebase. Essential for understanding cross-cutting concerns and dependency chains. Use include to filter by file type. Do NOT use for structural code analysis — use `ast_grep_search` for AST-level patterns.").
		Example(`{"pattern": "func.*HandleWebSocket", "path": "core/", "include": "*.go", "max_matches": 50}`).
		BestPractice("Set max_matches to avoid unbounded results — start with 50 and increase if needed.").
		BestPractice("Use include to filter by file extension — scanning all file types produces noisy results.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params grepParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			regex, err := regexp.Compile(params.Pattern)
			if err != nil {
				return nil, err
			}
			root := resolveGlobRoot(a.config.WorkingDirectory, params.Path)
			max := clampMaxMatches(params.MaxMatches)
			matches, err := runGrepSearch(root, regex, params.Include, max)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"pattern":   params.Pattern,
				"matches":   matches,
				"count":     len(matches),
				"truncated": len(matches) >= max,
			}, nil
		}).
		Build()
}

func clampMaxMatches(value int) int {
	if value <= 0 {
		return 100
	}
	return value
}

func runGrepSearch(root string, regex *regexp.Regexp, include string, max int) ([]grepMatch, error) {
	results := make([]grepMatch, 0)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			return skipPlannerDir(info.Name())
		}
		if include != "" && !matchesGlob(include, info.Name()) {
			return nil
		}
		return appendFileMatches(root, path, regex, max, &results)
	})
	return results, err
}

func skipPlannerDir(name string) error {
	if name == ".git" || name == "vendor" || name == "node_modules" {
		return filepath.SkipDir
	}
	return nil
}

func appendFileMatches(root string, path string, regex *regexp.Regexp, max int, out *[]grepMatch) error {
	if len(*out) >= max {
		return filepath.SkipAll
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	lines := strings.Split(string(content), "\n")
	for idx, line := range lines {
		if len(*out) >= max {
			return filepath.SkipAll
		}
		if regex.MatchString(line) {
			*out = append(*out, grepMatch{
				File:    rel,
				Line:    idx + 1,
				Content: strings.TrimSpace(line),
			})
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// git (consolidated: status, diff, log, show, blame, ls_files, branch_list, fetch)
// ---------------------------------------------------------------------------

type gitInput struct {
	Command   string `json:"command"`
	Ref       string `json:"ref,omitempty"`
	Path      string `json:"path,omitempty"`
	File      string `json:"file,omitempty"`
	Count     int    `json:"count,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}

func gitSkill(a *Architect) *skills.Skill {
	type handler = func(context.Context, *gitInput) (any, error)
	dispatch := map[string]handler{
		"status": func(ctx context.Context, _ *gitInput) (any, error) {
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", "status")
			if err != nil {
				return nil, err
			}
			return map[string]any{"command": "git status", "output": output}, nil
		},
		"diff": func(ctx context.Context, _ *gitInput) (any, error) {
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", "diff", "--no-ext-diff")
			if err != nil {
				return nil, err
			}
			return map[string]any{"command": "git diff --no-ext-diff", "output": output}, nil
		},
		"log": func(ctx context.Context, p *gitInput) (any, error) {
			count := p.Count
			if count <= 0 {
				count = 10
			}
			args := []string{"log", fmt.Sprintf("--max-count=%d", count), "--oneline"}
			if strings.TrimSpace(p.Path) != "" {
				args = append(args, "--", p.Path)
			}
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", args...)
			if err != nil {
				return nil, err
			}
			return map[string]any{"output": output, "count": count}, nil
		},
		"show": func(ctx context.Context, p *gitInput) (any, error) {
			ref := "HEAD"
			if strings.TrimSpace(p.Ref) != "" {
				ref = p.Ref
			}
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", "show", "--stat", "--no-color", ref)
			if err != nil {
				return nil, err
			}
			return map[string]any{"ref": ref, "output": output}, nil
		},
		"blame": func(ctx context.Context, p *gitInput) (any, error) {
			if strings.TrimSpace(p.File) == "" {
				return nil, fmt.Errorf("file is required for git blame")
			}
			args := buildGitBlameArgs(p.File, p.StartLine, p.EndLine)
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", args...)
			if err != nil {
				return nil, err
			}
			return map[string]any{"file": p.File, "output": output}, nil
		},
		"ls_files": func(ctx context.Context, _ *gitInput) (any, error) {
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", "ls-files")
			if err != nil {
				return nil, err
			}
			return map[string]any{"command": "git ls-files", "output": output}, nil
		},
		"branch_list": func(ctx context.Context, _ *gitInput) (any, error) {
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", "branch", "--list")
			if err != nil {
				return nil, err
			}
			return map[string]any{"command": "git branch --list", "output": output}, nil
		},
		"fetch": func(ctx context.Context, _ *gitInput) (any, error) {
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", "fetch", "--all", "--prune", "--tags")
			if err != nil {
				return map[string]any{"status": "error", "error": err.Error()}, nil
			}
			return map[string]any{"status": "ok", "output": output}, nil
		},
	}

	return skills.NewSkill("git").
		Description("Execute git read operations for planning context.\n\n"+
			"Commands:\n"+
			"- status: Show repository working tree status\n"+
			"- diff: Show uncommitted diff (--no-ext-diff)\n"+
			"- log: Show commit history (params: count, path)\n"+
			"- show: Show commit/object details with --stat (params: ref)\n"+
			"- blame: Show line-level authorship (params: file [required], start_line, end_line)\n"+
			"- ls_files: List all tracked files\n"+
			"- branch_list: List local branches\n"+
			"- fetch: Fetch all remotes with pruning and tags").
		Domain("git_read").
		Keywords("git", "status", "diff", "log", "show", "blame", "branch", "fetch",
			"history", "commits", "changes", "remote", "ls-files").
		Priority(75).
		TokenEstimate(450).
		EnumParam("command", "Git command to execute", []string{
			"status", "diff", "log", "show", "blame", "ls_files", "branch_list", "fetch",
		}, true).
		StringParam("ref", "Commit ref for show (default HEAD)", false).
		StringParam("path", "Path filter for log", false).
		StringParam("file", "File path for blame (required for blame)", false).
		IntParam("count", "Number of commits for log (default 10)", false).
		IntParam("start_line", "Start line for blame range", false).
		IntParam("end_line", "End line for blame range", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params gitInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			fn, ok := dispatch[params.Command]
			if !ok {
				return nil, fmt.Errorf("unknown git command: %q", params.Command)
			}
			return fn(ctx, &params)
		}).
		Build()
}

func buildGitBlameArgs(file string, startLine, endLine int) []string {
	args := []string{"blame", "--", file}
	if startLine > 0 && endLine >= startLine {
		args = []string{
			"blame",
			fmt.Sprintf("-L%d,%d", startLine, endLine),
			"--",
			file,
		}
	}
	return args
}

// ---------------------------------------------------------------------------
// ast_grep_search
// ---------------------------------------------------------------------------

type astGrepParams struct {
	Pattern string   `json:"pattern"`
	Lang    string   `json:"lang"`
	Paths   []string `json:"paths,omitempty"`
}

func astGrepSearchSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("ast_grep_search").
		Description("Run AST-aware structural search with ast-grep when available.").
		Domain("ast").
		Keywords("ast", "structure", "pattern", "find all").
		Priority(65).
		StringParam("pattern", "AST pattern", true).
		StringParam("lang", "Language (go,typescript,python,...)", true).
		ArrayParam("paths", "Search paths", "string", false).
		Usage("Use for structural code search when regex patterns are insufficient — finding specific function signatures, interface implementations, or code patterns by AST structure. Requires ast-grep (or sg) to be installed. Falls back gracefully with status=unavailable if not installed. Do NOT use for simple text search — `grep` is faster.").
		Example(`{"pattern": "func $NAME(ctx context.Context, $$$ARGS) error", "lang": "go", "paths": ["core/"]}`).
		BestPractice("Use AST variables ($NAME, $$$ARGS) for flexible matching — literal patterns are equivalent to grep.").
		BestPractice("Check the status field in the response — unavailable means ast-grep is not installed, not that there were no matches.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params astGrepParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			args := buildAstGrepArgs(params)
			output, err := runCommandInDirWithBinary(ctx, a.config.WorkingDirectory, "ast-grep", args...)
			if shared.CommandUnavailable(err) {
				output, err = runCommandInDirWithBinary(ctx, a.config.WorkingDirectory, "sg", args...)
			}
			if shared.CommandUnavailable(err) {
				return map[string]any{"status": "unavailable", "reason": "ast-grep not installed"}, nil
			}
			if err != nil {
				return nil, err
			}
			return map[string]any{"status": "ok", "output": output}, nil
		}).
		Build()
}

func buildAstGrepArgs(params astGrepParams) []string {
	args := []string{"run", "-p", params.Pattern, "-l", params.Lang}
	if len(params.Paths) == 0 {
		return append(args, ".")
	}
	return append(args, params.Paths...)
}

// ---------------------------------------------------------------------------
// lsp (consolidated: goto_definition, find_references, hover, symbols, call_hierarchy)
// ---------------------------------------------------------------------------

// lspSkill wires the architect's LSP skill with a composite backend:
// gopls as a Go-specific accelerator layered over a polyglot
// treesitter backend reading through the architect's FileAccess. The
// treesitter path sees VFS overlays directly, which matters during
// planning when in-flight edits have not yet been committed to disk.
func lspSkill(a *Architect) *skills.Skill {
	backend := &shared.CompositeBackend{
		Primary: &shared.GoplsBackend{
			Run:        runGoplsCommand,
			GetWorkDir: func() string { return a.config.WorkingDirectory },
		},
		Secondary: &shared.TreesitterBackend{
			Tool:          shared.SharedTreeSitter(),
			FileAccess:    func() versioning.FileAccess { return a.fileAccess },
			WorkspaceRoot: func() string { return a.config.WorkingDirectory },
		},
		GoFirst: true,
	}
	return shared.NewLSPSkill(shared.LSPSkillConfig{
		Backend:       backend,
		Priority:      60,
		Domain:        "lsp",
		TokenEstimate: 400,
	})
}

// ---------------------------------------------------------------------------
// Shared utilities
// ---------------------------------------------------------------------------

func runGoplsCommand(ctx context.Context, workDir string, subcommand string, arg string) (string, string) {
	output, err := runCommandInDir(ctx, workDir, "gopls", subcommand, arg)
	if shared.CommandUnavailable(err) {
		return "gopls is not installed", "unavailable"
	}
	if err != nil {
		return err.Error(), "error"
	}
	return output, "ok"
}

func runCommandInDir(ctx context.Context, workDir string, command string, args ...string) (string, error) {
	return runCommandInDirWithBinary(ctx, workDir, command, args...)
}

func runCommandInDirWithBinary(ctx context.Context, workDir string, binary string, args ...string) (string, error) {
	output, err := shared.RunStrictDiskCommand(ctx, shared.StrictDiskExecConfig{
		AgentID:    "architect",
		AgentType:  "architect",
		WorkingDir: workDir,
	}, binary, args, strictDiskExecEnv(binary))
	if err != nil && errors.Is(err, commandapproval.ErrApprovalRequired) {
		return "", err
	}
	return output, err
}

func strictDiskExecEnv(binary string) map[string]string {
	if strings.EqualFold(strings.TrimSpace(binary), "git") {
		return map[string]string{"GIT_OPTIONAL_LOCKS": "0"}
	}
	return nil
}
