package shared

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/core/treesitter"
	"github.com/adalundhe/sylk/core/versioning"
)

// LSPBackend is the abstraction the lsp skill dispatches against.
// Implementations may be language-server-backed (gopls),
// treesitter-backed (polyglot, in-process), or composite (delegate
// per language). Every backend reads files through versioning.FileAccess
// so in-flight VFS edits are visible — the historical gopls-only path
// read real disk and missed unsynced work.
type LSPBackend interface {
	GotoDefinition(ctx context.Context, req LSPLocationRequest) (LSPActionResult, error)
	FindReferences(ctx context.Context, req LSPLocationRequest) (LSPActionResult, error)
	Hover(ctx context.Context, req LSPLocationRequest) (LSPActionResult, error)
	Symbols(ctx context.Context, req LSPSymbolsRequest) (LSPActionResult, error)
	CallHierarchy(ctx context.Context, req LSPCallHierarchyRequest) (LSPActionResult, error)
}

// LSPLocationRequest identifies a cursor position in a file.
// Line and Column are 1-based to match the agent-facing skill params.
type LSPLocationRequest struct {
	File   string
	Line   int
	Column int
}

// LSPSymbolsRequest requests a file's symbol outline or a workspace
// symbol search. At most one of File / Query should be populated.
type LSPSymbolsRequest struct {
	File  string
	Query string
}

// LSPCallHierarchyRequest requests incoming or outgoing call chains
// for a symbol. Direction is "incoming" | "outgoing" | "both" (empty
// is treated as "both").
type LSPCallHierarchyRequest struct {
	File      string
	Line      int
	Column    int
	Direction string
}

// LSPActionResult is the shape every backend returns. The handler
// serializes this to JSON for the agent. Status values mirror the
// original skill: "ok" | "unavailable" | "error" | "not_supported".
// Source identifies which backend produced the result so the agent
// can interpret provenance — "treesitter", "gopls", or a composite
// tag like "gopls+treesitter".
type LSPActionResult struct {
	Status  string         `json:"status"`
	Source  string         `json:"source,omitempty"`
	Reason  string         `json:"reason,omitempty"`
	Output  string         `json:"output,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

// --- Composite backend --------------------------------------------

// CompositeBackend routes each request to the first backend that
// reports a usable result. Typical use: Go files → gopls (cross-file
// index), all others → treesitter (polyglot, VFS-aware). When a
// backend is absent or returns "unavailable" / "not_supported" the
// next backend in order is tried; an "error" result short-circuits.
type CompositeBackend struct {
	Primary   LSPBackend // typically gopls for Go projects
	Secondary LSPBackend // typically treesitter
	// GoFirst, when true, attempts Primary only for Go files and
	// skips straight to Secondary for everything else. Avoids
	// spawning gopls on a Python file just to watch it fail.
	GoFirst bool
}

func (c *CompositeBackend) GotoDefinition(ctx context.Context, req LSPLocationRequest) (LSPActionResult, error) {
	return c.dispatch(req.File, func(b LSPBackend) (LSPActionResult, error) {
		return b.GotoDefinition(ctx, req)
	})
}

func (c *CompositeBackend) FindReferences(ctx context.Context, req LSPLocationRequest) (LSPActionResult, error) {
	return c.dispatch(req.File, func(b LSPBackend) (LSPActionResult, error) {
		return b.FindReferences(ctx, req)
	})
}

func (c *CompositeBackend) Hover(ctx context.Context, req LSPLocationRequest) (LSPActionResult, error) {
	return c.dispatch(req.File, func(b LSPBackend) (LSPActionResult, error) {
		return b.Hover(ctx, req)
	})
}

func (c *CompositeBackend) Symbols(ctx context.Context, req LSPSymbolsRequest) (LSPActionResult, error) {
	return c.dispatch(req.File, func(b LSPBackend) (LSPActionResult, error) {
		return b.Symbols(ctx, req)
	})
}

func (c *CompositeBackend) CallHierarchy(ctx context.Context, req LSPCallHierarchyRequest) (LSPActionResult, error) {
	return c.dispatch(req.File, func(b LSPBackend) (LSPActionResult, error) {
		return b.CallHierarchy(ctx, req)
	})
}

func (c *CompositeBackend) dispatch(file string, call func(LSPBackend) (LSPActionResult, error)) (LSPActionResult, error) {
	tryPrimary := c.Primary != nil && (!c.GoFirst || isGoFile(file))
	if tryPrimary {
		if result, err := call(c.Primary); err == nil && isUsableResult(result) {
			return result, nil
		}
	}
	if c.Secondary != nil {
		return call(c.Secondary)
	}
	return LSPActionResult{Status: "unavailable", Reason: "no backend available"}, nil
}

func isUsableResult(r LSPActionResult) bool {
	switch r.Status {
	case "ok":
		return true
	default:
		return false
	}
}

func isGoFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(path)), ".go")
}

// --- Treesitter backend -------------------------------------------

// TreesitterBackend implements LSPBackend via in-process treesitter
// parses. Reads every file through a FileAccess so VFS overlays are
// visible (the agent's in-flight edits, pipeline state, session-merged
// work). Handles 20+ languages out of the box via the grammars in
// core/treesitter/queries.
//
// Capability matrix:
//   - Symbols:   full polyglot support (ParseResult exposes
//     Functions, Types, Imports for every grammar that has a query).
//   - Hover:     polyglot — returns the structural node at the cursor
//     position with surrounding context.
//   - GotoDefinition / FindReferences: same-file resolution works for
//     any language (name-based match on declarations and references).
//     Cross-file is best-effort via FileAccess.Grep; for precise
//     cross-file Go work, compose with a gopls-backed Primary.
//   - CallHierarchy: returns the surrounding function plus an
//     indication that the feature is a same-file approximation.
type TreesitterBackend struct {
	Tool       *treesitter.TreeSitterTool
	FileAccess func() versioning.FileAccess
	// WorkspaceRoot is the project root used to resolve relative file
	// paths. When empty, paths are used as-is.
	WorkspaceRoot func() string
}

func (t *TreesitterBackend) readFile(ctx context.Context, file string) ([]byte, string, error) {
	if t == nil || t.FileAccess == nil {
		return nil, "", fmt.Errorf("treesitter backend: no file access configured")
	}
	fa := t.FileAccess()
	if fa == nil {
		return nil, "", fmt.Errorf("treesitter backend: file access is nil")
	}
	content, err := fa.ReadFile(ctx, file)
	if err != nil {
		return nil, "", err
	}
	return content, file, nil
}

func (t *TreesitterBackend) Symbols(ctx context.Context, req LSPSymbolsRequest) (LSPActionResult, error) {
	file := strings.TrimSpace(req.File)
	query := strings.TrimSpace(req.Query)
	if file == "" && query == "" {
		return LSPActionResult{Status: "error", Reason: "symbols requires file or query"}, nil
	}
	// File-scoped symbol listing.
	if file != "" {
		content, resolved, err := t.readFile(ctx, file)
		if err != nil {
			return LSPActionResult{Status: "error", Source: "treesitter", Reason: err.Error()}, nil
		}
		result, err := t.Tool.Parse(ctx, resolved, content)
		if err != nil {
			return LSPActionResult{Status: "error", Source: "treesitter", Reason: err.Error()}, nil
		}
		return LSPActionResult{
			Status: "ok",
			Source: "treesitter",
			Data: map[string]any{
				"file":      resolved,
				"language":  result.Language,
				"functions": result.Functions,
				"types":     result.Types,
				"imports":   result.Imports,
			},
		}, nil
	}
	// Workspace symbol search — best-effort via FileAccess.Grep on the
	// query token across source files under the workspace root.
	matches, err := t.workspaceSymbolSearch(ctx, query)
	if err != nil {
		return LSPActionResult{Status: "error", Source: "treesitter", Reason: err.Error()}, nil
	}
	return LSPActionResult{
		Status: "ok",
		Source: "treesitter",
		Data: map[string]any{
			"query":   query,
			"matches": matches,
		},
	}, nil
}

func (t *TreesitterBackend) Hover(ctx context.Context, req LSPLocationRequest) (LSPActionResult, error) {
	content, resolved, err := t.readFile(ctx, req.File)
	if err != nil {
		return LSPActionResult{Status: "error", Source: "treesitter", Reason: err.Error()}, nil
	}
	result, err := t.Tool.Parse(ctx, resolved, content)
	if err != nil {
		return LSPActionResult{Status: "error", Source: "treesitter", Reason: err.Error()}, nil
	}
	// Walk the node tree for the node whose extent contains
	// (line, column). Line/column from the caller are 1-based; the
	// treesitter NodeInfo exposes 0-based rows and columns.
	target := findNodeAt(result.RootNode, req.Line-1, req.Column-1)
	lines := strings.Split(string(content), "\n")
	snippet := hoverSnippet(lines, req.Line-1, 2)
	data := map[string]any{
		"file":     resolved,
		"language": result.Language,
		"snippet":  snippet,
	}
	if target != nil {
		data["node_type"] = target.Type
		data["start_line"] = target.StartLine + 1
		data["end_line"] = target.EndLine + 1
	}
	return LSPActionResult{Status: "ok", Source: "treesitter", Data: data}, nil
}

func (t *TreesitterBackend) GotoDefinition(ctx context.Context, req LSPLocationRequest) (LSPActionResult, error) {
	content, resolved, err := t.readFile(ctx, req.File)
	if err != nil {
		return LSPActionResult{Status: "error", Source: "treesitter", Reason: err.Error()}, nil
	}
	result, err := t.Tool.Parse(ctx, resolved, content)
	if err != nil {
		return LSPActionResult{Status: "error", Source: "treesitter", Reason: err.Error()}, nil
	}
	symbol := symbolAtCursor(content, req.Line-1, req.Column-1)
	if symbol == "" {
		return LSPActionResult{Status: "unavailable", Source: "treesitter", Reason: "no identifier at position"}, nil
	}
	// Look up the symbol against this file's function/type/import
	// tables. This is precise for declarations defined in the same
	// file; cross-file lookups fall back to a workspace name search.
	if def, ok := lookupSameFileDefinition(symbol, result); ok {
		return LSPActionResult{
			Status: "ok",
			Source: "treesitter",
			Data: map[string]any{
				"file":   resolved,
				"symbol": symbol,
				"same_file": true,
				"definitions": []map[string]any{def},
			},
		}, nil
	}
	matches, err := t.workspaceSymbolSearch(ctx, symbol)
	if err != nil {
		return LSPActionResult{Status: "error", Source: "treesitter", Reason: err.Error()}, nil
	}
	return LSPActionResult{
		Status: "ok",
		Source: "treesitter",
		Data: map[string]any{
			"file":        resolved,
			"symbol":      symbol,
			"same_file":   false,
			"definitions": matches,
		},
	}, nil
}

func (t *TreesitterBackend) FindReferences(ctx context.Context, req LSPLocationRequest) (LSPActionResult, error) {
	content, resolved, err := t.readFile(ctx, req.File)
	if err != nil {
		return LSPActionResult{Status: "error", Source: "treesitter", Reason: err.Error()}, nil
	}
	symbol := symbolAtCursor(content, req.Line-1, req.Column-1)
	if symbol == "" {
		return LSPActionResult{Status: "unavailable", Source: "treesitter", Reason: "no identifier at position"}, nil
	}
	matches, err := t.workspaceSymbolSearch(ctx, symbol)
	if err != nil {
		return LSPActionResult{Status: "error", Source: "treesitter", Reason: err.Error()}, nil
	}
	return LSPActionResult{
		Status: "ok",
		Source: "treesitter",
		Data: map[string]any{
			"file":       resolved,
			"symbol":     symbol,
			"references": matches,
		},
	}, nil
}

func (t *TreesitterBackend) CallHierarchy(ctx context.Context, req LSPCallHierarchyRequest) (LSPActionResult, error) {
	// Same-file approximation: find the enclosing function at the
	// cursor and, for outgoing, list calls syntactically contained
	// in its body. Full call hierarchies need a cross-file index.
	content, resolved, err := t.readFile(ctx, req.File)
	if err != nil {
		return LSPActionResult{Status: "error", Source: "treesitter", Reason: err.Error()}, nil
	}
	result, err := t.Tool.Parse(ctx, resolved, content)
	if err != nil {
		return LSPActionResult{Status: "error", Source: "treesitter", Reason: err.Error()}, nil
	}
	fn, _ := enclosingFunction(result, req.Line-1)
	data := map[string]any{
		"file":     resolved,
		"language": result.Language,
		"note":     "call_hierarchy via treesitter is a same-file approximation; compose with gopls for cross-file Go",
	}
	if fn != nil {
		data["enclosing"] = map[string]any{
			"name":       fn.Name,
			"start_line": fn.StartLine,
			"end_line":   fn.EndLine,
			"parameters": fn.Parameters,
			"return":     fn.ReturnType,
			"is_method":  fn.IsMethod,
		}
	}
	return LSPActionResult{Status: "ok", Source: "treesitter", Data: data}, nil
}

// workspaceSymbolSearch scans source files under the configured
// workspace root for the given name. Best-effort — limited by the
// FileAccess.Grep implementation. Returns file + line pairs.
func (t *TreesitterBackend) workspaceSymbolSearch(ctx context.Context, name string) ([]map[string]any, error) {
	if t.FileAccess == nil {
		return nil, nil
	}
	fa := t.FileAccess()
	if fa == nil {
		return nil, nil
	}
	root := "."
	if t.WorkspaceRoot != nil {
		if resolved := strings.TrimSpace(t.WorkspaceRoot()); resolved != "" {
			root = resolved
		}
	}
	grepMatches, err := fa.Grep(ctx, root, `\b`+name+`\b`, "", 0, 256)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(grepMatches))
	for _, m := range grepMatches {
		out = append(out, map[string]any{
			"file": m.File,
			"line": m.Line,
			"text": m.Line,
		})
	}
	// Stable ordering for deterministic agent-facing output.
	sort.SliceStable(out, func(i, j int) bool {
		a := fmt.Sprint(out[i]["file"])
		b := fmt.Sprint(out[j]["file"])
		if a == b {
			return fmt.Sprint(out[i]["line"]) < fmt.Sprint(out[j]["line"])
		}
		return a < b
	})
	return out, nil
}

// --- Gopls backend ------------------------------------------------

// GoplsFn retains the historical signature so existing per-agent
// wrappers (engineer's broker-aware runGoplsCommand, architect's and
// librarian's disk-rooted variants) drop in unchanged.
type GoplsFn func(ctx context.Context, workDir, subcommand, arg string) (output string, status string)

// GoplsBackend wraps a GoplsFn. When gopls is unavailable the
// returned result carries Status="unavailable", which the composite
// backend treats as "try the next backend."
type GoplsBackend struct {
	Run        GoplsFn
	GetWorkDir func() string
}

func (g *GoplsBackend) workDir() string {
	if g == nil || g.GetWorkDir == nil {
		return ""
	}
	return g.GetWorkDir()
}

func (g *GoplsBackend) call(ctx context.Context, sub, arg string) LSPActionResult {
	if g == nil || g.Run == nil {
		return LSPActionResult{Status: "unavailable", Source: "gopls", Reason: "gopls backend not configured"}
	}
	output, status := g.Run(ctx, g.workDir(), sub, arg)
	if status != "ok" {
		return LSPActionResult{Status: status, Source: "gopls", Reason: output}
	}
	return LSPActionResult{Status: "ok", Source: "gopls", Output: output}
}

func (g *GoplsBackend) GotoDefinition(ctx context.Context, req LSPLocationRequest) (LSPActionResult, error) {
	return g.call(ctx, "definition", lspLocationArg(req)), nil
}

func (g *GoplsBackend) FindReferences(ctx context.Context, req LSPLocationRequest) (LSPActionResult, error) {
	return g.call(ctx, "references", lspLocationArg(req)), nil
}

func (g *GoplsBackend) Hover(ctx context.Context, req LSPLocationRequest) (LSPActionResult, error) {
	return g.call(ctx, "hover", lspLocationArg(req)), nil
}

func (g *GoplsBackend) Symbols(ctx context.Context, req LSPSymbolsRequest) (LSPActionResult, error) {
	arg := strings.TrimSpace(req.Query)
	if arg == "" {
		arg = strings.TrimSpace(req.File)
	}
	if arg == "" {
		arg = "."
	}
	return g.call(ctx, "symbols", arg), nil
}

func (g *GoplsBackend) CallHierarchy(ctx context.Context, req LSPCallHierarchyRequest) (LSPActionResult, error) {
	loc := lspLocationArg(LSPLocationRequest{File: req.File, Line: req.Line, Column: req.Column})
	refs := g.call(ctx, "references", loc)
	defn := g.call(ctx, "definition", loc)
	if refs.Status != "ok" && defn.Status != "ok" {
		return LSPActionResult{Status: "unavailable", Source: "gopls", Reason: refs.Reason}, nil
	}
	return LSPActionResult{
		Status: "ok",
		Source: "gopls",
		Data: map[string]any{
			"direction":  req.Direction,
			"references": refs.Output,
			"definition": defn.Output,
		},
	}, nil
}

// --- tree helpers -------------------------------------------------

// findNodeAt walks a NodeInfo tree and returns the deepest node
// whose extent contains (line, column) — both 0-based.
func findNodeAt(root *treesitter.NodeInfo, line, column int) *treesitter.NodeInfo {
	if root == nil {
		return nil
	}
	if !nodeContains(root, line, column) {
		return nil
	}
	// Walk children depth-first; return deepest containing node.
	best := root
	for i := range root.Children {
		if candidate := findNodeAt(&root.Children[i], line, column); candidate != nil {
			best = candidate
		}
	}
	return best
}

func nodeContains(n *treesitter.NodeInfo, line, column int) bool {
	if n == nil {
		return false
	}
	l := uint32(line)
	c := uint32(column)
	if l < n.StartLine || l > n.EndLine {
		return false
	}
	if l == n.StartLine && c < n.StartCol {
		return false
	}
	if l == n.EndLine && c > n.EndCol {
		return false
	}
	return true
}

// hoverSnippet returns `radius` lines around line (0-based) as
// a single string so the hover result carries readable context.
func hoverSnippet(lines []string, line, radius int) string {
	if len(lines) == 0 {
		return ""
	}
	start := line - radius
	if start < 0 {
		start = 0
	}
	end := line + radius + 1
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start:end], "\n")
}

// symbolAtCursor returns the identifier-like token at a 0-based
// (line, column) position in content. Returns "" when the cursor is
// not over an identifier character.
func symbolAtCursor(content []byte, line, column int) string {
	lines := strings.Split(string(content), "\n")
	if line < 0 || line >= len(lines) {
		return ""
	}
	row := lines[line]
	if column < 0 || column >= len(row) {
		return ""
	}
	if !isIdentChar(row[column]) {
		return ""
	}
	start := column
	for start > 0 && isIdentChar(row[start-1]) {
		start--
	}
	end := column + 1
	for end < len(row) && isIdentChar(row[end]) {
		end++
	}
	return row[start:end]
}

func isIdentChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '_':
		return true
	}
	return false
}

// lookupSameFileDefinition searches the parsed file for a declaration
// whose name matches `name`. Matches against function/type/import
// tables (which treesitter extracts for every supported grammar).
func lookupSameFileDefinition(name string, result *treesitter.ParseResult) (map[string]any, bool) {
	if result == nil {
		return nil, false
	}
	for _, f := range result.Functions {
		if f.Name == name {
			return map[string]any{
				"kind":       "function",
				"name":       f.Name,
				"start_line": f.StartLine,
				"end_line":   f.EndLine,
				"parameters": f.Parameters,
				"is_method":  f.IsMethod,
			}, true
		}
	}
	for _, tp := range result.Types {
		if tp.Name == name {
			return map[string]any{
				"kind":       "type",
				"name":       tp.Name,
				"type_kind":  tp.Kind,
				"start_line": tp.StartLine,
				"end_line":   tp.EndLine,
			}, true
		}
	}
	for _, imp := range result.Imports {
		if imp.Alias == name || filepath.Base(imp.Path) == name {
			return map[string]any{
				"kind": "import",
				"path": imp.Path,
				"line": imp.Line,
			}, true
		}
	}
	return nil, false
}

// enclosingFunction returns the FunctionInfo whose span contains the
// 0-based line, if any. Used by CallHierarchy to report the callee
// the user's cursor is inside.
func enclosingFunction(result *treesitter.ParseResult, line int) (*treesitter.FunctionInfo, bool) {
	if result == nil {
		return nil, false
	}
	l := uint32(line)
	for i := range result.Functions {
		f := &result.Functions[i]
		if l >= f.StartLine && l <= f.EndLine {
			return f, true
		}
	}
	return nil, false
}

func lspLocationArg(req LSPLocationRequest) string {
	return fmt.Sprintf("%s:%d:%d", req.File, req.Line, req.Column)
}
