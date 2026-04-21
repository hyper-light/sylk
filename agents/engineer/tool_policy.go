package engineer

import (
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func engineerVisibleSkillNames() []string {
	base := shared.AppendMemoryForestVisibleSkillNames([]string{
		"search_skills",
		// Phase 2.K / CR-2: 12 workspace skills collapsed into 3 verbs.
		"workspace_read",
		"workspace_write",
		"lsp",
		"discover_project_tools",
		"discover_code_patterns",
		"format",
		"lint",
		"ask_user_clarification",
		// Phase 2.K / GT-4 + GI-5: collapsed into dependency(action=…).
		"dependency",
		// challenge_agent / handoff_next / validate_work /
		// process_validation collapsed into pipeline_protocol(action=…).
		// The per-verb skills stay in the registry for internal state-
		// machine + WAL callers but are no longer LLM-visible.
		"pipeline_protocol",
		"audit",
		"bash",
		"report_confidence",
	}, "engineer")
	// Activity Fabric: ambient awareness primitives must be visible
	// by default. See docs/SCRIBE_FABRIC.md.
	return fabric.AppendFabricAwarenessSkillNames(base)
}

func engineerMutatingSkillNames() []string {
	return shared.AppendMemoryForestMutatingSkillNames([]string{
		// Phase 2.K / CR-2: 4 write skills collapsed into workspace_write.
		"workspace_write",
		"bash",
		"dependency",
		"format",
		"challenge_agent",
		"handoff_next",
		"validate_work",
		"process_validation",
		"report_confidence",
		"reroute_request",
	})
}

func engineerToolManifest(registry *skills.Registry) *toolruntime.PolicyManifest {
	return toolruntime.BuildManifestFromRegistry(toolruntime.ManifestBuildConfig{
		AgentID:          "engineer",
		CapabilityScope:  "engineer.default",
		Registry:         registry,
		VisibleByDefault: shared.FilterRegisteredSkillNames(registry, engineerVisibleSkillNames()),
		Mutating:         shared.FilterRegisteredSkillNames(registry, engineerMutatingSkillNames()),
	})
}
