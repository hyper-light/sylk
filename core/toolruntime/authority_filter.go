package toolruntime

import "github.com/adalundhe/sylk/core/authority"

var workspaceViewToolNames = map[string]struct{}{
	"read_workspace_file":            {},
	"workspace_glob":                 {},
	"workspace_grep":                 {},
	"inspect_workspace_state":        {},
	"summarize_workspace_state":      {},
	"diff_workspace_file":            {},
	"prepare_pipeline_write_context": {},
	"prepare_global_write_context":   {},
}

var diskWriteToolNames = map[string]struct{}{
	"write_file":       {},
	"edit_file":        {},
	"create_directory": {},
	"delete_file":      {},
}

var executionToolNames = map[string]struct{}{
	"ast_grep_search":        {},
	"check_coverage":         {},
	"detect_deadlocks":       {},
	"detect_memory_leaks":    {},
	"detect_race_conditions": {},
	"format":                 {},
	"git":                    {},
	"lint":                   {},
	"lsp":                    {},
	"run_command":            {},
	"run_formatter_check":    {},
	"run_linter":             {},
	"run_security_scan":      {},
	"run_test_suite":         {},
	"run_type_checker":       {},
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
