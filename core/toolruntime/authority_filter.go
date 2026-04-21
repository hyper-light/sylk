package toolruntime

import "github.com/adalundhe/sylk/core/authority"

// workspaceViewToolNames retains the per-op underlying builders (which
// workspace_read dispatches to internally) so authority classification
// still recognizes them if they ever flow through the filter. The
// consolidated workspace_read skill itself is classified by domain
// (filesystem → disk-read) at the policy level — it isn't in this
// registry because its op space covers both reads and the write
// preflight; authority checks against workspace_read happen at the
// policy level rather than by name.
var workspaceViewToolNames = map[string]struct{}{
	"read_workspace_file":       {},
	"workspace_glob":            {},
	"workspace_grep":            {},
	"inspect_workspace_state":   {},
	"summarize_workspace_state": {},
	"diff_workspace_file":       {},
}

var diskWriteToolNames = map[string]struct{}{
	"write_file":       {},
	"edit_file":        {},
	"create_directory": {},
	"delete_file":      {},
}

var executionToolNames = map[string]struct{}{
	"ast_grep_search": {},
	"bash":            {},
	"format":          {},
	"git":             {},
	"lint":            {},
	"lsp":             {},
	// Phase 2.K / GI-C: run_linter + run_type_checker +
	// run_formatter_check + run_security_scan + check_coverage +
	// analyze_complexity + detect_race_conditions + detect_deadlocks +
	// detect_memory_leaks collapsed into run_analyzer(kind=…).
	"run_analyzer":   {},
	"run_test_suite": {},
}

func ApplyAuthorityProfile(agentType string, manifest *PolicyManifest) *PolicyManifest {
	if manifest == nil {
		return nil
	}
	profile := authority.ProfileFor(agentType)
	filtered := &PolicyManifest{
		AgentID:         manifest.AgentID,
		CapabilityScope: manifest.CapabilityScope,
		Tools:           make(map[string]ToolPolicy, len(manifest.Tools)),
	}
	for name, policy := range manifest.Tools {
		if authorityAllowsTool(profile, policy) {
			filtered.Tools[name] = policy
		}
	}
	return filtered
}

func authorityAllowsTool(profile authority.Profile, policy ToolPolicy) bool {
	if isSyntheticTool(policy.Name) {
		return true
	}
	switch {
	case requiresWorkspaceViews(policy.Name):
		return profile.AllowsWorkspaceTools()
	case requiresExecution(policy.Name):
		return profile.ExecScope != authority.ExecScopeNone
	case requiresDiskWrite(policy):
		return profile.AllowsFileWrites()
	case requiresDiskRead(policy):
		return profile.AllowsFileReads()
	default:
		return true
	}
}

func requiresWorkspaceViews(name string) bool {
	_, ok := workspaceViewToolNames[name]
	return ok
}

func requiresExecution(name string) bool {
	_, ok := executionToolNames[name]
	return ok
}

func requiresDiskWrite(policy ToolPolicy) bool {
	if policy.Domain == DomainFilesystem && policy.Effect == EffectMutating {
		return true
	}
	_, ok := diskWriteToolNames[policy.Name]
	return ok
}

func requiresDiskRead(policy ToolPolicy) bool {
	return policy.Domain == DomainFilesystem
}
