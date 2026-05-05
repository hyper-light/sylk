package compiler

import (
	"strings"

	"github.com/adalundhe/sylk/core/mcp"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// DeriveEffect maps a remote tool's hints to a Sylk EffectKind.
//
// Precedence:
//  1. Explicit catalog override (TrustedReadOnly).
//  2. ReadOnlyHint when set to true.
//  3. DestructiveHint when set to true → EffectMutating.
//  4. Unknown (no hints) → EffectMutating. Per design: "unknown -> mutating
//     unless a trusted server-specific override says otherwise."
func DeriveEffect(desc mcp.ToolDescriptor, opts CompileToolOptions) toolruntime.EffectKind {
	if opts.TrustedReadOnly {
		return toolruntime.EffectReadOnly
	}
	if desc.Annotations == nil {
		return toolruntime.EffectMutating
	}
	if hint := desc.Annotations.ReadOnlyHint; hint != nil && *hint {
		return toolruntime.EffectReadOnly
	}
	return toolruntime.EffectMutating
}

// DeriveExecution maps an effect to a Sylk ExecutionMode.
//
// Read-only tools execute locally (the model is allowed to invoke them
// directly without Guardian); mutating tools execute under Guardian control.
// Local-worker mode is reserved for read-only tools that need long-running
// background scheduling — defaulting all read-only MCP tools to local
// matches the design's "remote read-only -> ExecutionModeLocal" mapping.
func DeriveExecution(effect toolruntime.EffectKind) toolruntime.ExecutionMode {
	if effect == toolruntime.EffectMutating {
		return toolruntime.ExecutionModeGuardian
	}
	return toolruntime.ExecutionModeLocal
}

// DeriveDomain maps a tool descriptor + catalog metadata to a stable Sylk
// effect domain. Falls back to system when nothing more specific can be
// inferred so Guardian gives the call appropriate scrutiny.
func DeriveDomain(desc mcp.ToolDescriptor, opts CompileToolOptions) string {
	if domain := firstCatalogDomain(opts.ServerDomains); domain != "" {
		return domain
	}
	if domain := domainFromHints(desc.Annotations); domain != "" {
		return domain
	}
	if domain := domainFromName(desc.Name); domain != "" {
		return domain
	}
	return string(toolruntime.DomainSystem)
}

// DeriveKeywords combines descriptor cues into a deduplicated keyword list
// the loader can use for trigger detection.
func DeriveKeywords(desc mcp.ToolDescriptor, opts CompileToolOptions) []string {
	seen := map[string]struct{}{}
	var keywords []string
	addKeyword := func(k string) {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" {
			return
		}
		if _, dup := seen[k]; dup {
			return
		}
		seen[k] = struct{}{}
		keywords = append(keywords, k)
	}
	for _, token := range strings.FieldsFunc(desc.Name, isTokenSeparator) {
		addKeyword(token)
	}
	for _, token := range strings.Fields(desc.Description) {
		if len(token) >= 4 {
			addKeyword(token)
		}
	}
	for _, alias := range opts.Aliases {
		addKeyword(alias)
	}
	return keywords
}

func firstCatalogDomain(domains []string) string {
	for _, candidate := range domains {
		trimmed := strings.TrimSpace(candidate)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// domainFromHints maps MCP-level hints to Sylk domains where the mapping is
// unambiguous; empty otherwise.
func domainFromHints(hints *mcp.ToolHints) string {
	if hints == nil {
		return ""
	}
	if hints.OpenWorldHint != nil && *hints.OpenWorldHint {
		return string(toolruntime.DomainResearch)
	}
	if hints.ReadOnlyHint != nil && *hints.ReadOnlyHint {
		return string(toolruntime.DomainDiscovery)
	}
	return ""
}

// domainFromName uses prefix heuristics to map common tool families.
// Conservative — any uncertainty falls back to the empty string so the
// catalog or system fallback wins.
func domainFromName(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(lower, "git_"), strings.Contains(lower, "_pull_request"), strings.Contains(lower, "branch"):
		return string(toolruntime.DomainGit)
	case strings.HasPrefix(lower, "search_"), strings.HasPrefix(lower, "find_"), strings.HasPrefix(lower, "list_"):
		return string(toolruntime.DomainDiscovery)
	case strings.HasPrefix(lower, "read_"), strings.HasPrefix(lower, "fetch_"):
		return string(toolruntime.DomainResearch)
	case strings.HasPrefix(lower, "create_"), strings.HasPrefix(lower, "write_"), strings.HasPrefix(lower, "update_"), strings.HasPrefix(lower, "delete_"):
		return string(toolruntime.DomainCode)
	}
	return ""
}

func isTokenSeparator(r rune) bool {
	return r == '_' || r == '-' || r == '.' || r == ' ' || r == '/'
}
