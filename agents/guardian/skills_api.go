package guardian

import (
	"context"
	"fmt"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// guardianPinnedSkillNames returns the minimal set of skills always loaded in the
// tool definition payload. These are the skills Guardian needs for every request.
// Remaining skills are loaded progressively via LoadForInput keyword matching and
// demand-paged by the shared tool runtime when the LLM invokes them.
func guardianPinnedSkillNames() []string {
	return filterSyntheticGuardianRuntimeTools(guardianToolManifest().DefaultVisibleNames())
}

// guardianAllSkillNames returns all skills including filesystem read tools and reroute.
func guardianAllSkillNames() []string {
	return guardianToolManifest().AllowedNames()
}

func guardianToolManifest() *toolruntime.PolicyManifest {
	return guardianToolManifestForRegistry(nil)
}

func guardianToolManifestForRegistry(registry *skills.Registry) *toolruntime.PolicyManifest {
	policies := []toolruntime.ToolPolicy{
		toolruntime.NewToolPolicy("git_safety", toolruntime.EffectReadOnly, toolruntime.DomainGit, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("rollback", toolruntime.EffectMutating, toolruntime.DomainGit, toolruntime.ExecutionModeLocalWorker, toolruntime.WithApprovalSensitive()),
		toolruntime.NewToolPolicy("content_scan", toolruntime.EffectReadOnly, toolruntime.DomainValidation, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("agent_health", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("review_gate", toolruntime.EffectReadOnly, toolruntime.DomainValidation, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("agent_logs", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("system_status", toolruntime.EffectReadOnly, toolruntime.DomainSystem, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("vfs_status", toolruntime.EffectReadOnly, toolruntime.DomainSystem, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("pipeline_status", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("gateway_metrics", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("knowledge_status", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("concurrency_status", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("usage_breakdown", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("quarantine_status", toolruntime.EffectReadOnly, toolruntime.DomainValidation, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("tool_execution_control", toolruntime.EffectReadOnly, toolruntime.DomainControl, toolruntime.ExecutionModeLocal, toolruntime.WithSearchable(false)),
		toolruntime.NewToolPolicy("command_execution_control", toolruntime.EffectReadOnly, toolruntime.DomainControl, toolruntime.ExecutionModeLocal, toolruntime.WithSearchable(false)),
		toolruntime.NewToolPolicy("plan_approval_gate", toolruntime.EffectReadOnly, toolruntime.DomainControl, toolruntime.ExecutionModeLocal, toolruntime.WithSearchable(false)),
		toolruntime.NewToolPolicy("read_file", toolruntime.EffectReadOnly, toolruntime.DomainFilesystem, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("glob", toolruntime.EffectReadOnly, toolruntime.DomainFilesystem, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("grep", toolruntime.EffectReadOnly, toolruntime.DomainFilesystem, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("read_workspace_file", toolruntime.EffectReadOnly, toolruntime.DomainFilesystem, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("workspace_glob", toolruntime.EffectReadOnly, toolruntime.DomainFilesystem, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("workspace_grep", toolruntime.EffectReadOnly, toolruntime.DomainFilesystem, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("inspect_workspace_state", toolruntime.EffectReadOnly, toolruntime.DomainFilesystem, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("summarize_workspace_state", toolruntime.EffectReadOnly, toolruntime.DomainFilesystem, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("diff_workspace_file", toolruntime.EffectReadOnly, toolruntime.DomainFilesystem, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("ask_user_question", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeLocalWorker, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("reroute_request", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeLocalWorker, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("self_diagnostic", toolruntime.EffectReadOnly, toolruntime.DomainSystem, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy(toolruntime.SearchToolName, toolruntime.EffectReadOnly, toolruntime.DomainDiscovery, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault(), toolruntime.WithSearchable(false)),
	}
	policies = shared.AppendMemoryForestToolPolicies(policies, registry, "guardian")
	policies = fabric.AppendFabricAwarenessToolPolicies(policies, registry)
	return toolruntime.ApplyAuthorityProfile("guardian", toolruntime.NewManifest("guardian", "guardian.control", policies...))
}

func filterSyntheticGuardianRuntimeTools(names []string) []string {
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if name == toolruntime.SearchToolName {
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}

// registerGuardianSafetyHook validates only allowed tools are invoked.
func registerGuardianSafetyHook(hooks *skills.HookRegistry, _ *skills.Registry, allowed []string) {
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
			return skills.HookResult{Continue: true}
		},
	)
}
