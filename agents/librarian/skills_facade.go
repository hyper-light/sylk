package librarian

// skills_facade consolidates the librarian's search and package tools
// into polymorphic façade skills that dispatch by a kind/action enum.
//
// The per-verb builders remain in skills.go and skills_clone.go for
// internal registration; the façade skills are the sole entries exposed
// to the LLM. Collapsing search_codebase + find_pattern + find_symbol
// into a single `search(kind=…)` verb and clone_repository +
// list_packages + remove_package into a single `package(action=…)` verb
// shrinks the catalog without losing any capability.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/skills"
)

// ─── search ──────────────────────────────────────────────────────────────

func searchFacadeSkill(l *Librarian) *skills.Skill {
	// Build the underlying per-kind skills once; dispatch on kind.
	codebase := searchCodebaseSkill(l)
	pattern := findPatternSkill(l)
	symbol := findSymbolSkill(l)

	return skills.NewSkill("search").
		Description("Search the codebase. One primitive for every search operation; kind selects the engine.\n\n" +
			"Kinds:\n" +
			"- codebase: Free-text search for code patterns, files, or symbols (params: query [required], types, path_prefix, limit, fuzzy)\n" +
			"- pattern: Detect coding patterns and conventions by pattern type (params: pattern_type ∈ {error_handling, logging, testing, naming, imports, comments} [required], scope, include_examples)\n" +
			"- symbol: Tree-sitter structural symbol search — functions, methods, types, classes — by name regex (params: name [required], include, limit). **Preferred for definition lookups** because it understands language syntax instead of regex-matching raw text.").
		Domain("code").
		Keywords("search", "find", "grep", "code", "pattern", "convention", "symbol", "function", "method", "type", "class").
		Priority(100).
		EnumParam("kind", "Which search engine to use", []string{"codebase", "pattern", "symbol"}, true).
		// codebase.
		StringParam("query", "Search query text (kind=codebase)", false).
		ArrayParam("types", "Filter by symbol types (kind=codebase)", "string", false).
		StringParam("path_prefix", "Filter by path prefix (kind=codebase)", false).
		BoolParam("fuzzy", "Enable fuzzy matching (kind=codebase)", false).
		// pattern.
		EnumParam("pattern_type", "Pattern class (kind=pattern)", []string{
			"error_handling", "logging", "testing", "naming", "imports", "comments",
		}, false).
		StringParam("scope", "Path prefix scope (kind=pattern)", false).
		BoolParam("include_examples", "Include code examples (kind=pattern)", false).
		// symbol.
		StringParam("name", "Symbol name or regex (kind=symbol)", false).
		StringParam("include", "File glob filter (kind=symbol)", false).
		// shared.
		IntParam("limit", "Maximum number of results (kind=codebase|symbol)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var probe struct {
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal(input, &probe); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			switch strings.TrimSpace(probe.Kind) {
			case "codebase":
				if l.searchHandler == nil {
					return nil, fmt.Errorf("search(kind=codebase) requires a configured SearchHandler")
				}
				return codebase.Handler(ctx, input)
			case "pattern":
				return pattern.Handler(ctx, input)
			case "symbol":
				return symbol.Handler(ctx, input)
			default:
				return nil, fmt.Errorf("unknown search kind: %q (expected codebase|pattern|symbol)", probe.Kind)
			}
		}).
		Build()
}

// ─── package ─────────────────────────────────────────────────────────────

func packageFacadeSkill(l *Librarian) *skills.Skill {
	clone := cloneRepositorySkill(l)
	list := listPackagesSkill(l)
	remove := removePackageSkill(l)

	return skills.NewSkill("package").
		Description("Manage cloned remote packages in the local package store. One primitive for every package operation; action selects the verb.\n\n" +
			"Actions:\n" +
			"- clone: Clone a remote git repository (shallow, size-bounded) into the local store so it becomes searchable via workspace_read / find_symbol (params: url [required], branch)\n" +
			"- list: List all cloned packages with repo name, URL, branch, file count, and clone time\n" +
			"- remove: Remove a previously cloned package from the local store (params: package [required] — owner/repo key)").
		Domain("code").
		Keywords("package", "repository", "repo", "clone", "fetch", "download", "list", "remove", "remote", "git").
		Priority(85).
		EnumParam("action", "Which package operation to perform", []string{"clone", "list", "remove"}, true).
		StringParam("url", "Repository URL (action=clone)", false).
		StringParam("branch", "Branch to clone (action=clone)", false).
		StringParam("package", "Package key owner/repo (action=remove)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var probe struct {
				Action string `json:"action"`
			}
			if err := json.Unmarshal(input, &probe); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			switch strings.TrimSpace(probe.Action) {
			case "clone":
				return clone.Handler(ctx, input)
			case "list":
				return list.Handler(ctx, input)
			case "remove":
				return remove.Handler(ctx, input)
			default:
				return nil, fmt.Errorf("unknown package action: %q (expected clone|list|remove)", probe.Action)
			}
		}).
		Build()
}
