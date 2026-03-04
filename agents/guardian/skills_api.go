package guardian

import (
	"context"
	"fmt"

	"github.com/adalundhe/sylk/core/skills"
)

// guardianCoreSkillNames returns skills always loaded, available in every tool loop turn.
func guardianCoreSkillNames() []string {
	return []string{
		"git_safety",
		"content_scan",
		"agent_health",
		"review_gate",
		"agent_logs",
		"system_status",
		"vfs_status",
	}
}

// guardianAllSkillNames returns all skills including filesystem read tools and reroute.
func guardianAllSkillNames() []string {
	return []string{
		"git_safety",
		"rollback",
		"content_scan",
		"agent_health",
		"review_gate",
		"agent_logs",
		"system_status",
		"vfs_status",
		"read_file",
		"glob",
		"grep",
		"ask_user_question",
		"reroute_request",
	}
}

// registerGuardianSafetyHook validates only allowed skills are invoked.
// Demand-pages unloaded skills on first use.
func registerGuardianSafetyHook(hooks *skills.HookRegistry, registry *skills.Registry, allowed []string) {
	if hooks == nil {
		return
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = true
	}
	hooks.RegisterPreToolCallHook(
		"guardian_safety_catch",
		skills.HookPriorityHigh,
		func(ctx context.Context, data *skills.ToolCallHookData) skills.HookResult {
			if data == nil {
				return skills.HookResult{Continue: true}
			}
			if !allowedSet[data.ToolName] {
				return skills.HookResult{
					Continue: false,
					Error:    fmt.Errorf("SECURITY VIOLATION: Guardian is not permitted to execute tool %q", data.ToolName),
				}
			}
			// Demand-page: load the skill if allowed but not yet loaded.
			if registry != nil {
				skill := registry.Get(data.ToolName)
				if skill != nil && !skill.Loaded {
					registry.Load(data.ToolName)
				}
			}
			return skills.HookResult{Continue: true}
		},
	)
}
