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
	return gitSimpleSkill(a, "git_status", "Show repository status.", "status")
}

func gitDiffSkill(a *Architect) *skills.Skill {
	return gitSimpleSkill(a, "git_diff", "Show uncommitted diff.", "diff", "--no-ext-diff")
}

func gitLsFilesSkill(a *Architect) *skills.Skill {
	return gitSimpleSkill(a, "git_ls_files", "List tracked files.", "ls-files")
}

func gitBranchListSkill(a *Architect) *skills.Skill {
	return gitSimpleSkill(a, "git_branch_list", "List branches.", "branch", "--list")
}

func gitFetchSkill(a *Architect) *skills.Skill {
	return skills.NewSkill("git_fetch").
		Description("Fetch remote refs without modifying working tree files.").
		Domain("git_read").
		Keywords("git fetch", "remote update", "sync refs").
		Priority(70).
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

func gitSimpleSkill(a *Architect, name string, description string, args ...string) *skills.Skill {
	return skills.NewSkill(name).
		Description(description).
		Domain("git_read").
		Keywords("git", "history", "status", "diff", "changes").
		Priority(75).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			output, err := runCommandInDir(ctx, a.config.WorkingDirectory, "git", args...)
			if err != nil {
				return nil, err
			}
			return map[string]any{"command": strings.Join(append([]string{"git"}, args...), " "), "output": output}, nil
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
	return lspLocationSkill(a, "lsp_go_to_definition", "Navigate to symbol definition via gopls.", "definition")
}

func lspFindReferencesSkill(a *Architect) *skills.Skill {
	return lspLocationSkill(a, "lsp_find_references", "Find symbol references via gopls.", "references")
}

func lspHoverSkill(a *Architect) *skills.Skill {
	return lspLocationSkill(a, "lsp_hover", "Get symbol hover info via gopls.", "hover")
}

func lspLocationSkill(a *Architect, name string, description string, subcommand string) *skills.Skill {
	return skills.NewSkill(name).
		Description(description).
		Domain("lsp").
		Keywords("lsp", "symbol", "definition", "references", "hover").
		Priority(60).
		StringParam("file", "File path", true).
		IntParam("line", "Line (1-based)", true).
		IntParam("column", "Column (1-based)", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params lspLocationParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.File) == "" {
				return nil, fmt.Errorf("file is required")
			}
			output, status := runGoplsCommand(ctx, a.config.WorkingDirectory, subcommand, lspLocation(params))
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
