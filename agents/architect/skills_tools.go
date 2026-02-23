package architect

import (
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
			content, err := os.ReadFile(resolveArchitectPath(a.config.WorkingDirectory, params.Path))
			if err != nil {
				return nil, err
			}
			return sliceFileContent(params.Path, string(content), params.Offset, params.Limit), nil
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

func gitStatusSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("git_status").
		Description("Show repository status.").
		Domain("git_read").
		Keywords("git", "history", "status", "diff", "changes").
		Priority(75).
		Usage("Use to understand the current working tree state before planning — which files are modified, staged, or untracked. Essential for determining if the codebase is clean before delegating work.").
		Example(`{}`).
		BestPractice("Check git_status before delegation to ensure no uncommitted changes conflict with the planned work.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", "status")
			if err != nil {
				return nil, err
			}
			return map[string]any{"command": "git status", "output": output}, nil
		}).
		Build()
}

func gitDiffSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("git_diff").
		Description("Show uncommitted diff.").
		Domain("git_read").
		Keywords("git", "history", "status", "diff", "changes").
		Priority(75).
		Usage("Use to examine uncommitted changes in detail — understanding what has been modified and how. Critical for plan revision when the codebase has changed since the plan was created.").
		Example(`{}`).
		BestPractice("Review the diff before revising a plan — changes may have already addressed some planned tasks.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", "diff", "--no-ext-diff")
			if err != nil {
				return nil, err
			}
			return map[string]any{"command": "git diff --no-ext-diff", "output": output}, nil
		}).
		Build()
}

func gitLsFilesSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("git_ls_files").
		Description("List tracked files.").
		Domain("git_read").
		Keywords("git", "history", "status", "diff", "changes").
		Priority(75).
		Usage("Use to enumerate all tracked files in the repository. Useful for understanding project scope and file organization. Do NOT use for pattern matching — use `glob` instead.").
		Example(`{}`).
		BestPractice("For large repos, prefer `glob` with a specific pattern over ls-files — ls-files returns every tracked file.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", "ls-files")
			if err != nil {
				return nil, err
			}
			return map[string]any{"command": "git ls-files", "output": output}, nil
		}).
		Build()
}

func gitBranchListSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("git_branch_list").
		Description("List branches.").
		Domain("git_read").
		Keywords("git", "history", "status", "diff", "changes").
		Priority(75).
		Usage("Use to discover available branches for context — understanding parallel work streams, feature branches, or release state.").
		Example(`{}`).
		BestPractice("Cross-reference with git_log to understand branch divergence when planning merge-related tasks.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", "branch", "--list")
			if err != nil {
				return nil, err
			}
			return map[string]any{"command": "git branch --list", "output": output}, nil
		}).
		Build()
}

func gitFetchSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("git_fetch").
		Description("Fetch remote refs without modifying working tree files.").
		Domain("git_read").
		Keywords("git fetch", "remote update", "sync refs").
		Priority(70).
		Usage("Use to sync remote refs before making branch or remote-dependent planning decisions. Fetches all remotes with pruning and tags. Safe operation — does not modify working tree files.").
		Example(`{}`).
		BestPractice("Run git_fetch before git_branch_list when remote branch state matters for the plan.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", "fetch", "--all", "--prune", "--tags")
			if err != nil {
				return map[string]any{
					"status": "error",
					"error":  err.Error(),
				}, nil
			}
			return map[string]any{
				"status": "ok",
				"output": output,
			}, nil
		}).
		Build()
}

type gitLogParams struct {
	Count int    `json:"count,omitempty"`
	Path  string `json:"path,omitempty"`
}

func gitLogSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("git_log").
		Description("Show commit history for context gathering.").
		Domain("git_read").
		Keywords("git log", "history", "commits").
		Priority(75).
		IntParam("count", "Number of commits", false).
		StringParam("path", "Optional path filter", false).
		Usage("Use to understand recent commit history for planning context — identifying what changed, when, and by whom. Use path filter to scope history to relevant directories. Do NOT request excessive counts — 10-20 commits typically suffice for context.").
		Example(`{"count": 15, "path": "core/auth/"}`).
		BestPractice("Use path filtering to focus history on the module being planned — repo-wide history is noisy.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params gitLogParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			count := params.Count
			if count <= 0 {
				count = 10
			}
			args := []string{"log", fmt.Sprintf("--max-count=%d", count), "--oneline"}
			if strings.TrimSpace(params.Path) != "" {
				args = append(args, "--", params.Path)
			}
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", args...)
			if err != nil {
				return nil, err
			}
			return map[string]any{"output": output, "count": count}, nil
		}).
		Build()
}

type gitShowParams struct {
	Ref string `json:"ref,omitempty"`
}

func gitShowSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("git_show").
		Description("Show details for a commit or object.").
		Domain("git_read").
		Keywords("git show", "commit details").
		Priority(70).
		StringParam("ref", "Commit ref (default HEAD)", false).
		Usage("Use to inspect a specific commit's changes — understanding what a particular commit introduced. Defaults to HEAD if no ref is provided. Useful for understanding the most recent change before planning.").
		Example(`{"ref": "HEAD~3"}`).
		BestPractice("Use --stat output (default) to get an overview before deciding if the full diff is needed.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params gitShowParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			ref := "HEAD"
			if strings.TrimSpace(params.Ref) != "" {
				ref = params.Ref
			}
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", "show", "--stat", "--no-color", ref)
			if err != nil {
				return nil, err
			}
			return map[string]any{"ref": ref, "output": output}, nil
		}).
		Build()
}

type gitBlameParams struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}

func gitBlameSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("git_blame").
		Description("Show line-level authorship history for a file.").
		Domain("git_read").
		Keywords("git blame", "who changed", "history").
		Priority(70).
		StringParam("file", "File path", true).
		IntParam("start_line", "Start line", false).
		IntParam("end_line", "End line", false).
		Usage("Use to understand authorship and change history for specific file regions — identifying who last modified critical code and when. Use start_line/end_line to scope blame to the relevant section.").
		Example(`{"file": "core/auth/handler.go", "start_line": 50, "end_line": 80}`).
		BestPractice("Scope blame to specific line ranges — full-file blame on large files produces excessive output.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params gitBlameParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.File) == "" {
				return nil, fmt.Errorf("file is required")
			}
			args := buildGitBlameArgs(params)
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", args...)
			if err != nil {
				return nil, err
			}
			return map[string]any{"file": params.File, "output": output}, nil
		}).
		Build()
}

func buildGitBlameArgs(params gitBlameParams) []string {
	args := []string{"blame", "--", params.File}
	if params.StartLine > 0 && params.EndLine >= params.StartLine {
		args = []string{
			"blame",
			fmt.Sprintf("-L%d,%d", params.StartLine, params.EndLine),
			"--",
			params.File,
		}
	}
	return args
}

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
			binary, ok := findFirstBinary("ast-grep", "sg")
			if !ok {
				return map[string]any{"status": "unavailable", "reason": "ast-grep not installed"}, nil
			}
			args := buildAstGrepArgs(params)
			output, err := runCommandInDirWithBinary(ctx, a.config.WorkingDirectory, binary, args...)
			if err != nil {
				return nil, err
			}
			return map[string]any{"status": "ok", "binary": binary, "output": output}, nil
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

type lspLocationParams struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

type lspSymbolsParams struct {
	File  string `json:"file,omitempty"`
	Query string `json:"query,omitempty"`
}

type lspCallHierarchyParams struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Direction string `json:"direction"`
}

func lspGoToDefinitionSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("lsp_go_to_definition").
		Description("Navigate to symbol definition via gopls.").
		Domain("lsp").
		Keywords("lsp", "symbol", "definition", "references", "hover").
		Priority(60).
		StringParam("file", "File path", true).
		IntParam("line", "Line (1-based)", true).
		IntParam("column", "Column (1-based)", true).
		Usage("Use to navigate to a symbol's definition — understanding where a type, function, or variable is defined. Requires gopls to be running. Essential for tracing dependency chains during planning.").
		Example(`{"file": "core/auth/handler.go", "line": 42, "column": 15}`).
		BestPractice("Combine with lsp_find_references to understand both definition and usage scope of a symbol.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params lspLocationParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.File) == "" {
				return nil, fmt.Errorf("file is required")
			}
			output, status := runGoplsCommand(ctx, a.config.WorkingDirectory, "definition", lspLocation(params))
			if status != "ok" {
				return map[string]any{"status": status, "reason": output}, nil
			}
			return map[string]any{"status": "ok", "output": output}, nil
		}).
		Build()
}

func lspFindReferencesSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("lsp_find_references").
		Description("Find symbol references via gopls.").
		Domain("lsp").
		Keywords("lsp", "symbol", "definition", "references", "hover").
		Priority(60).
		StringParam("file", "File path", true).
		IntParam("line", "Line (1-based)", true).
		IntParam("column", "Column (1-based)", true).
		Usage("Use to find all references to a symbol — understanding the blast radius of a planned change. Critical for assessing risk when modifying shared interfaces or widely-used functions.").
		Example(`{"file": "core/auth/handler.go", "line": 42, "column": 15}`).
		BestPractice("High reference counts indicate a symbol is widely used — plan changes to such symbols with extra caution and broader test coverage.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params lspLocationParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.File) == "" {
				return nil, fmt.Errorf("file is required")
			}
			output, status := runGoplsCommand(ctx, a.config.WorkingDirectory, "references", lspLocation(params))
			if status != "ok" {
				return map[string]any{"status": status, "reason": output}, nil
			}
			return map[string]any{"status": "ok", "output": output}, nil
		}).
		Build()
}

func lspHoverSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("lsp_hover").
		Description("Get symbol hover info via gopls.").
		Domain("lsp").
		Keywords("lsp", "symbol", "definition", "references", "hover").
		Priority(60).
		StringParam("file", "File path", true).
		IntParam("line", "Line (1-based)", true).
		IntParam("column", "Column (1-based)", true).
		Usage("Use to get type information and documentation for a symbol at a specific location. Useful for understanding parameter types and return values without reading the full definition file.").
		Example(`{"file": "core/auth/handler.go", "line": 42, "column": 15}`).
		BestPractice("Use hover for quick type checks — go_to_definition for full implementation details.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params lspLocationParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.File) == "" {
				return nil, fmt.Errorf("file is required")
			}
			output, status := runGoplsCommand(ctx, a.config.WorkingDirectory, "hover", lspLocation(params))
			if status != "ok" {
				return map[string]any{"status": status, "reason": output}, nil
			}
			return map[string]any{"status": "ok", "output": output}, nil
		}).
		Build()
}

func lspSymbolsSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("lsp_symbols").
		Description("List symbols from gopls for a file or workspace query.").
		Domain("lsp").
		Keywords("lsp symbols", "outline", "structure").
		Priority(55).
		StringParam("file", "Optional file path", false).
		StringParam("query", "Optional symbol query", false).
		Usage("Use to list symbols (functions, types, variables) in a file or search workspace symbols by query. Useful for understanding module structure and available APIs during planning.").
		Example(`{"file": "core/auth/handler.go"}`).
		Example(`{"query": "HandleWebSocket"}`).
		BestPractice("Use file-scoped symbols for module overview and query-scoped symbols for cross-module discovery.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params lspSymbolsParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			argument := strings.TrimSpace(params.Query)
			if argument == "" {
				argument = strings.TrimSpace(params.File)
			}
			if argument == "" {
				argument = "."
			}
			output, status := runGoplsCommand(ctx, a.config.WorkingDirectory, "symbols", argument)
			if status != "ok" {
				return map[string]any{"status": status, "reason": output}, nil
			}
			return map[string]any{"status": "ok", "output": output}, nil
		}).
		Build()
}

func lspCallHierarchySkill(a *Architect) *skills.Skill {
	return skills.NewSkill("lsp_call_hierarchy").
		Description("Approximate call hierarchy by combining gopls references/definition queries.").
		Domain("lsp").
		Keywords("lsp call hierarchy", "callers", "callees").
		Priority(55).
		StringParam("file", "File path", true).
		IntParam("line", "Line (1-based)", true).
		IntParam("column", "Column (1-based)", true).
		StringParam("direction", "incoming|outgoing|both", true).
		Usage("Use to trace call relationships for a symbol — identifying callers (incoming) and callees (outgoing). Built on combined references/definition queries. Essential for understanding execution flow when planning refactors.").
		Example(`{"file": "core/auth/handler.go", "line": 42, "column": 15, "direction": "incoming"}`).
		BestPractice("Use direction=incoming to find callers of a function (who calls this?) and direction=outgoing to find callees (what does this call?).").
		BestPractice("If both references and definition queries fail, gopls may not be running — check the status field.").
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params lspCallHierarchyParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			loc := lspLocation(lspLocationParams{File: params.File, Line: params.Line, Column: params.Column})
			refs, refsStatus := runGoplsCommand(ctx, a.config.WorkingDirectory, "references", loc)
			defn, defStatus := runGoplsCommand(ctx, a.config.WorkingDirectory, "definition", loc)
			if refsStatus != "ok" && defStatus != "ok" {
				return map[string]any{"status": "unavailable", "reason": refs}, nil
			}
			return map[string]any{
				"status":     "ok",
				"direction":  params.Direction,
				"references": refs,
				"definition": defn,
			}, nil
		}).
		Build()
}

func lspLocation(params lspLocationParams) string {
	return fmt.Sprintf("%s:%d:%d", params.File, params.Line, params.Column)
}

func runGoplsCommand(ctx context.Context, workDir string, subcommand string, arg string) (string, string) {
	if _, ok := findFirstBinary("gopls"); !ok {
		return "gopls is not installed", "unavailable"
	}
	output, err := runCommandInDir(ctx, workDir, "gopls", subcommand, arg)
	if err != nil {
		return err.Error(), "error"
	}
	return output, "ok"
}

func findFirstBinary(candidates ...string) (string, bool) {
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, true
		}
	}
	return "", false
}

func runCommandInDir(ctx context.Context, workDir string, command string, args ...string) (string, error) {
	return runCommandInDirWithBinary(ctx, workDir, command, args...)
}

func runCommandInDirWithBinary(ctx context.Context, workDir string, binary string, args ...string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(callCtx, binary, args...)
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s failed: %w\n%s", binary, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}
