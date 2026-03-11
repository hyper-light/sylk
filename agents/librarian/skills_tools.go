package librarian

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
	"github.com/adalundhe/sylk/core/search/codebase"
	"github.com/adalundhe/sylk/core/skills"
)

// ---------------------------------------------------------------------------
// read_file
// ---------------------------------------------------------------------------

type readFileParams struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func readFileSkill(l *Librarian) *skills.Skill {
	return skills.NewSkill("read_file").
		Description("Read file contents. Use offset and limit for large files.").
		Domain("filesystem").
		Keywords("read", "file", "content", "cat", "view").
		Priority(95).
		StringParam("path", "Path to file (relative to project root)", true).
		IntParam("offset", "Starting line offset (0-based)", false).
		IntParam("limit", "Max lines to return (default: 1000)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params readFileParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Path) == "" {
				return nil, fmt.Errorf("path is required")
			}
			content, err := os.ReadFile(resolvePath(l.config.WorkingDirectory, params.Path))
			if err != nil {
				return nil, err
			}
			return sliceFileContent(params.Path, string(content), params.Offset, params.Limit), nil
		}).
		Build()
}

func resolvePath(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if strings.TrimSpace(root) == "" {
		return path
	}
	return filepath.Join(root, path)
}

func sliceFileContent(path, content string, offset, limit int) map[string]any {
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

func clampOffset(offset, total int) int {
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
// glob — gitignore-aware, gobwas/glob file discovery
// ---------------------------------------------------------------------------

type globParams struct {
	Pattern string   `json:"pattern"`
	Path    string   `json:"path,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
}

func globSkill(l *Librarian) *skills.Skill {
	return skills.NewSkill("glob").
		Description("Find files matching a glob pattern. Supports ** for recursive matching. Respects .gitignore rules.").
		Domain("filesystem").
		Keywords("glob", "find files", "pattern", "match", "list").
		Priority(90).
		StringParam("pattern", "Glob pattern (e.g. '**/*.go', 'agents/**/types.go')", true).
		StringParam("path", "Base path (default: project root)", false).
		ArrayParam("exclude", "Excluded glob patterns", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params globParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Pattern) == "" {
				return nil, fmt.Errorf("pattern is required")
			}
			root := resolveRoot(l.config.WorkingDirectory, params.Path)
			matches, err := codebase.GlobFiles(codebase.GlobConfig{
				Root:            root,
				Pattern:         params.Pattern,
				ExcludePatterns: params.Exclude,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"pattern": params.Pattern, "matches": matches, "count": len(matches)}, nil
		}).
		Build()
}

func resolveRoot(workDir, path string) string {
	if strings.TrimSpace(path) == "" {
		return workDir
	}
	return resolvePath(workDir, path)
}

// ---------------------------------------------------------------------------
// grep — parallel, gitignore-aware regex search with tree-sitter context
// ---------------------------------------------------------------------------

type grepParams struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path,omitempty"`
	Include         string `json:"include,omitempty"`
	MaxMatches      int    `json:"max_matches,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
	WithSymbols     bool   `json:"with_symbols,omitempty"`
}

func grepSkill(l *Librarian) *skills.Skill {
	return skills.NewSkill("grep").
		Description("Parallel, gitignore-aware regex search across the codebase (pure-Go ripgrep equivalent). For finding symbol definitions/references, prefer find_symbol which uses tree-sitter structural analysis. Set with_symbols=true to annotate matches with their enclosing function/type via tree-sitter.").
		Domain("filesystem").
		Keywords("grep", "search", "regex", "find text", "ripgrep", "rg").
		Priority(90).
		StringParam("pattern", "Regular expression pattern", true).
		StringParam("path", "Path to search (default: project root)", false).
		StringParam("include", "Include filename glob pattern (e.g. '**/*.go')", false).
		IntParam("max_matches", "Maximum number of matches (default: 200)", false).
		BoolParam("case_insensitive", "Case-insensitive matching", false).
		BoolParam("with_symbols", "Annotate matches with enclosing symbol name via tree-sitter", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params grepParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Pattern) == "" {
				return nil, fmt.Errorf("pattern is required")
			}

			flags := ""
			if params.CaseInsensitive {
				flags = "(?i)"
			}
			regex, err := regexp.Compile(flags + params.Pattern)
			if err != nil {
				return nil, fmt.Errorf("invalid regex: %w", err)
			}

			var includes []string
			if params.Include != "" {
				includes = []string{params.Include}
			}

			matches, err := codebase.Search(ctx, codebase.SearchConfig{
				Root:            resolveRoot(l.config.WorkingDirectory, params.Path),
				Pattern:         regex,
				IncludePatterns: includes,
				MaxMatches:      params.MaxMatches,
				WithSymbols:     params.WithSymbols,
			})
			if err != nil {
				return nil, err
			}

			return map[string]any{
				"pattern":   params.Pattern,
				"matches":   matches,
				"count":     len(matches),
				"truncated": len(matches) >= params.MaxMatches && params.MaxMatches > 0,
			}, nil
		}).
		Build()
}

// ---------------------------------------------------------------------------
// find_symbol — tree-sitter-powered structural symbol search
// ---------------------------------------------------------------------------

type findSymbolParams struct {
	Name    string `json:"name"`
	Include string `json:"include,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

func findSymbolSkill(l *Librarian) *skills.Skill {
	return skills.NewSkill("find_symbol").
		Description("Find functions, methods, types, and classes by name using tree-sitter structural analysis. More precise than regex grep — understands language syntax.").
		Domain("code").
		Keywords("symbol", "function", "method", "type", "class", "struct", "interface", "definition").
		Priority(100).
		StringParam("name", "Symbol name or regex pattern to match", true).
		StringParam("include", "File glob filter (e.g. '**/*.go')", false).
		IntParam("limit", "Max results (default: 50)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params findSymbolParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(params.Name) == "" {
				return nil, fmt.Errorf("name is required")
			}
			if params.Limit <= 0 {
				params.Limit = 50
			}

			nameRegex, err := regexp.Compile(params.Name)
			if err != nil {
				return nil, fmt.Errorf("invalid name pattern: %w", err)
			}

			var includes []string
			if params.Include != "" {
				includes = []string{params.Include}
			}

			symbols, err := codebase.SearchSymbols(ctx, l.config.WorkingDirectory, nameRegex, includes, params.Limit)
			if err != nil {
				return nil, err
			}

			return map[string]any{
				"name":    params.Name,
				"symbols": symbols,
				"count":   len(symbols),
			}, nil
		}).
		Build()
}

// ---------------------------------------------------------------------------
// git (consolidated: status, diff, log, show, blame, ls_files, branch_list)
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

func gitSkill(l *Librarian) *skills.Skill {
	type handler = func(context.Context, *gitInput) (any, error)
	dispatch := map[string]handler{
		"status": func(ctx context.Context, _ *gitInput) (any, error) {
			output, err := runCommandInDir(ctx, l.config.WorkingDirectory, "git", "status")
			if err != nil {
				return nil, err
			}
			return map[string]any{"command": "git status", "output": output}, nil
		},
		"diff": func(ctx context.Context, _ *gitInput) (any, error) {
			output, err := runCommandInDir(ctx, l.config.WorkingDirectory, "git", "diff", "--no-ext-diff")
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
			output, err := runCommandInDir(ctx, l.config.WorkingDirectory, "git", args...)
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
			output, err := runCommandInDir(ctx, l.config.WorkingDirectory, "git", "show", "--stat", "--no-color", ref)
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
			output, err := runCommandInDir(ctx, l.config.WorkingDirectory, "git", args...)
			if err != nil {
				return nil, err
			}
			return map[string]any{"file": p.File, "output": output}, nil
		},
		"ls_files": func(ctx context.Context, _ *gitInput) (any, error) {
			output, err := runCommandInDir(ctx, l.config.WorkingDirectory, "git", "ls-files")
			if err != nil {
				return nil, err
			}
			return map[string]any{"command": "git ls-files", "output": output}, nil
		},
		"branch_list": func(ctx context.Context, _ *gitInput) (any, error) {
			output, err := runCommandInDir(ctx, l.config.WorkingDirectory, "git", "branch", "--list")
			if err != nil {
				return nil, err
			}
			return map[string]any{"command": "git branch --list", "output": output}, nil
		},
	}

	return skills.NewSkill("git").
		Description("Execute git read operations.\n\n"+
			"Commands:\n"+
			"- status: Show repository working tree status\n"+
			"- diff: Show uncommitted diff (--no-ext-diff)\n"+
			"- log: Show commit history (params: count, path)\n"+
			"- show: Show commit/object details with --stat (params: ref)\n"+
			"- blame: Show line-level authorship (params: file [required], start_line, end_line)\n"+
			"- ls_files: List all tracked files\n"+
			"- branch_list: List local branches").
		Domain("git_read").
		Keywords("git", "status", "diff", "log", "show", "blame", "branch",
			"history", "commits", "changes", "ls-files").
		Priority(75).
		EnumParam("command", "Git command to execute", []string{
			"status", "diff", "log", "show", "blame", "ls_files", "branch_list",
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

func astGrepSearchSkill(l *Librarian) *skills.Skill {
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
			args := buildAstGrepArgs(params)
			output, err := runCommandInDirWithBinary(ctx, l.config.WorkingDirectory, "ast-grep", args...)
			if shared.CommandUnavailable(err) {
				output, err = runCommandInDirWithBinary(ctx, l.config.WorkingDirectory, "sg", args...)
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

type lspInput struct {
	Action    string `json:"action"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Column    int    `json:"column,omitempty"`
	Query     string `json:"query,omitempty"`
	Direction string `json:"direction,omitempty"`
}

func lspSkill(l *Librarian) *skills.Skill {
	type handler = func(context.Context, *lspInput) (any, error)

	locationAction := func(subcommand string) handler {
		return func(ctx context.Context, p *lspInput) (any, error) {
			if strings.TrimSpace(p.File) == "" {
				return nil, fmt.Errorf("file is required for %s", subcommand)
			}
			loc := lspLocation(p.File, p.Line, p.Column)
			output, status := runGoplsCommand(ctx, l.config.WorkingDirectory, subcommand, loc)
			if status != "ok" {
				return map[string]any{"status": status, "reason": output}, nil
			}
			return map[string]any{"status": "ok", "output": output}, nil
		}
	}

	dispatch := map[string]handler{
		"goto_definition": locationAction("definition"),
		"find_references": locationAction("references"),
		"hover":           locationAction("hover"),
		"symbols": func(ctx context.Context, p *lspInput) (any, error) {
			argument := strings.TrimSpace(p.Query)
			if argument == "" {
				argument = strings.TrimSpace(p.File)
			}
			if argument == "" {
				argument = "."
			}
			output, status := runGoplsCommand(ctx, l.config.WorkingDirectory, "symbols", argument)
			if status != "ok" {
				return map[string]any{"status": status, "reason": output}, nil
			}
			return map[string]any{"status": "ok", "output": output}, nil
		},
		"call_hierarchy": func(ctx context.Context, p *lspInput) (any, error) {
			loc := lspLocation(p.File, p.Line, p.Column)
			refs, refsStatus := runGoplsCommand(ctx, l.config.WorkingDirectory, "references", loc)
			defn, defStatus := runGoplsCommand(ctx, l.config.WorkingDirectory, "definition", loc)
			if refsStatus != "ok" && defStatus != "ok" {
				return map[string]any{"status": "unavailable", "reason": refs}, nil
			}
			return map[string]any{
				"status":     "ok",
				"direction":  p.Direction,
				"references": refs,
				"definition": defn,
			}, nil
		},
	}

	return skills.NewSkill("lsp").
		Description("Query gopls language server for code intelligence.\n\n"+
			"Actions:\n"+
			"- goto_definition: Navigate to symbol definition (params: file, line, column)\n"+
			"- find_references: Find all references to a symbol (params: file, line, column)\n"+
			"- hover: Get type info and documentation for a symbol (params: file, line, column)\n"+
			"- symbols: List symbols in a file or search workspace (params: file or query)\n"+
			"- call_hierarchy: Trace callers/callees of a symbol (params: file, line, column, direction)").
		Domain("lsp").
		Keywords("lsp", "symbol", "definition", "references", "hover",
			"outline", "structure", "call hierarchy", "callers", "callees").
		Priority(60).
		EnumParam("action", "LSP action to execute", []string{
			"goto_definition", "find_references", "hover", "symbols", "call_hierarchy",
		}, true).
		StringParam("file", "File path", false).
		IntParam("line", "Line number (1-based)", false).
		IntParam("column", "Column number (1-based)", false).
		StringParam("query", "Symbol query for workspace search", false).
		StringParam("direction", "Call hierarchy direction: incoming|outgoing|both", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params lspInput
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			fn, ok := dispatch[params.Action]
			if !ok {
				return nil, fmt.Errorf("unknown lsp action: %q", params.Action)
			}
			return fn(ctx, &params)
		}).
		Build()
}

// ---------------------------------------------------------------------------
// Shared utilities
// ---------------------------------------------------------------------------

func lspLocation(file string, line, column int) string {
	return fmt.Sprintf("%s:%d:%d", file, line, column)
}

func runGoplsCommand(ctx context.Context, workDir, subcommand, arg string) (string, string) {
	output, err := runCommandInDir(ctx, workDir, "gopls", subcommand, arg)
	if shared.CommandUnavailable(err) {
		return "gopls is not installed", "unavailable"
	}
	if err != nil {
		return err.Error(), "error"
	}
	return output, "ok"
}

func runCommandInDir(ctx context.Context, workDir, command string, args ...string) (string, error) {
	return runCommandInDirWithBinary(ctx, workDir, command, args...)
}

func runCommandInDirWithBinary(ctx context.Context, workDir, binary string, args ...string) (string, error) {
	output, err := shared.RunStrictDiskCommand(ctx, shared.StrictDiskExecConfig{
		AgentID:    "librarian",
		AgentType:  "librarian",
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
