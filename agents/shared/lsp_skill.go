package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// LSPGoplsFn retains the original gopls callback signature so existing
// per-agent wrappers drop in unchanged. New callers that want polyglot
// + VFS-aware lookups should set LSPSkillConfig.Backend directly with
// a CompositeBackend or a bare TreesitterBackend.
//
// Deprecated in favor of LSPBackend; kept alongside so engineer,
// architect, and librarian can migrate at their own pace.
type LSPGoplsFn = GoplsFn

// LSPSkillConfig configures the shared lsp skill. Backend is the
// primary dispatch target; when nil and RunGopls+GetWorkDir are set,
// a gopls-only backend is synthesized (legacy behavior). The
// preferred shape is Backend = &CompositeBackend{...} built at the
// agent boundary so every agent chooses which engines to compose.
type LSPSkillConfig struct {
	// Backend is the dispatch target. Set this directly for polyglot
	// + VFS-aware support (e.g. a CompositeBackend that pairs gopls
	// with TreesitterBackend reading through the agent's FileAccess).
	Backend LSPBackend

	// RunGopls + GetWorkDir are the legacy inputs. When Backend is
	// nil and these are set, the skill synthesizes a gopls-only
	// backend so per-agent wrappers that predate Backend continue to
	// work. New wrappers should prefer Backend.
	RunGopls   LSPGoplsFn
	GetWorkDir func() string

	Priority      int
	Domain        string
	Usage         string
	Satisfies     string
	Avoid         string
	TokenEstimate int
}

type lspInput struct {
	Action    string `json:"action"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Column    int    `json:"column,omitempty"`
	Query     string `json:"query,omitempty"`
	Direction string `json:"direction,omitempty"`
}

// NewLSPSkill returns the polyglot, VFS-aware lsp skill. The skill
// dispatches to cfg.Backend for all five actions; each backend owns
// its own capability matrix and read strategy. See docs on
// TreesitterBackend / GoplsBackend / CompositeBackend for the shapes
// agents typically compose at registration time.
func NewLSPSkill(cfg LSPSkillConfig) *skills.Skill {
	backend := cfg.Backend
	if backend == nil {
		if cfg.RunGopls == nil {
			panic("shared.NewLSPSkill: Backend or RunGopls is required")
		}
		// Legacy path: synthesize a gopls-only backend so older
		// wrappers continue to compile and run.
		backend = &GoplsBackend{Run: cfg.RunGopls, GetWorkDir: cfg.GetWorkDir}
	}

	priority := cfg.Priority
	if priority == 0 {
		priority = 60
	}
	domain := strings.TrimSpace(cfg.Domain)
	if domain == "" {
		domain = "lsp"
	}

	b := skills.NewSkill("lsp").
		Description(lspDescription).
		Domain(domain).
		Keywords("lsp", "symbol", "definition", "references", "hover",
			"outline", "structure", "call hierarchy", "callers", "callees",
			"treesitter", "polyglot", "vfs").
		Priority(priority).
		EnumParam("action", "LSP action to execute", []string{
			"goto_definition", "find_references", "hover", "symbols", "call_hierarchy",
		}, true).
		StringParam("file", "File path (workspace-relative). Read via VFS so in-flight edits are visible.", false).
		IntParam("line", "Line number (1-based)", false).
		IntParam("column", "Column number (1-based)", false).
		StringParam("query", "Symbol query for workspace search (symbols only)", false).
		StringParam("direction", "Call hierarchy direction: incoming|outgoing|both", false)

	if trimmed := strings.TrimSpace(cfg.Usage); trimmed != "" {
		b = b.Usage(trimmed)
	} else {
		b = b.Usage(lspDefaultUsage)
	}
	if trimmed := strings.TrimSpace(cfg.Satisfies); trimmed != "" {
		b = b.Satisfies(trimmed)
	} else {
		b = b.Satisfies(lspDefaultSatisfies)
	}
	if trimmed := strings.TrimSpace(cfg.Avoid); trimmed != "" {
		b = b.Avoid(trimmed)
	} else {
		b = b.Avoid(lspDefaultAvoid)
	}
	if cfg.TokenEstimate > 0 {
		b = b.TokenEstimate(cfg.TokenEstimate)
	}

	return b.Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
		var params lspInput
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
		switch params.Action {
		case "goto_definition":
			return dispatchLocation(ctx, backend.GotoDefinition, params, "goto_definition")
		case "find_references":
			return dispatchLocation(ctx, backend.FindReferences, params, "find_references")
		case "hover":
			return dispatchLocation(ctx, backend.Hover, params, "hover")
		case "symbols":
			result, err := backend.Symbols(ctx, LSPSymbolsRequest{
				File:  strings.TrimSpace(params.File),
				Query: strings.TrimSpace(params.Query),
			})
			return result, err
		case "call_hierarchy":
			if strings.TrimSpace(params.File) == "" {
				return nil, fmt.Errorf("file is required for call_hierarchy")
			}
			result, err := backend.CallHierarchy(ctx, LSPCallHierarchyRequest{
				File:      params.File,
				Line:      params.Line,
				Column:    params.Column,
				Direction: strings.TrimSpace(params.Direction),
			})
			return result, err
		default:
			return nil, fmt.Errorf("unknown lsp action: %q", params.Action)
		}
	}).
		Build()
}

func dispatchLocation(
	ctx context.Context,
	fn func(context.Context, LSPLocationRequest) (LSPActionResult, error),
	params lspInput,
	action string,
) (any, error) {
	if strings.TrimSpace(params.File) == "" {
		return nil, fmt.Errorf("file is required for %s", action)
	}
	return fn(ctx, LSPLocationRequest{
		File:   params.File,
		Line:   params.Line,
		Column: params.Column,
	})
}

const (
	lspDescription = `Query the language server for code intelligence — polyglot and VFS-aware. Unlike text grep which only sees on-disk content and string matches, this resolves symbols through the same workspace view the agent is editing: in-flight VFS writes are visible before they hit disk, and all 20+ supported languages (via treesitter) work the same way. Go projects also get a gopls-backed accelerator for cross-file lookups when available.

Actions:
- goto_definition: Navigate to a symbol's definition (params: file, line, column). Same-file resolution works for any language; cross-file is best-effort outside Go.
- find_references: Find all references to a symbol (params: file, line, column). Same-file precise; cross-file via workspace search.
- hover: Get the structural node and surrounding context at a position (params: file, line, column). Polyglot.
- symbols: List functions, types, and imports in a file (params: file) OR search the workspace for a symbol name (params: query). Polyglot.
- call_hierarchy: Trace callers/callees (params: file, line, column, direction). Cross-file precise for Go via gopls; same-file approximation for other languages.`

	lspDefaultUsage = `Reach for lsp when you need a symbol's actual identity — who defines it, who calls it, what type it has — rather than who mentions its name. Grep matches strings across files; lsp resolves symbols against the workspace view the agent is editing, so in-flight edits are visible and same-name identifiers in different scopes don't collide.`

	lspDefaultSatisfies = `Produces symbol-level evidence for implementation planning, safe refactors, and cross-reference auditing. The VFS-aware read path means the answer reflects the agent's current working state, not stale disk content.`

	lspDefaultAvoid = `Do not use lsp when a textual grep would suffice (searching for a literal string, log messages, error text). Do not call goto_definition before you have a concrete file + cursor position — the tool cannot disambiguate which symbol you mean from a bare name.`
)
