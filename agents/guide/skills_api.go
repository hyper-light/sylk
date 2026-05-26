package guide

import (
	"context"
	"fmt"

	contextskills "github.com/adalundhe/sylk/core/context/skills"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func guideCoreSkillNames() []string {
	return filterSyntheticGuideRuntimeTools(guideToolManifest().DefaultVisibleNames())
}

func guideAllSkillNames() []string {
	return guideToolManifest().AllowedNames()
}

func guideToolManifest() *toolruntime.PolicyManifest {
	return guideToolManifestForRegistry(nil)
}

func guideToolManifestForRegistry(registry *skills.Registry) *toolruntime.PolicyManifest {
	policies := []toolruntime.ToolPolicy{
		toolruntime.NewToolPolicy("route", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeLocalWorker, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("clarify", toolruntime.EffectReadOnly, toolruntime.DomainControl, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("guide_route", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeLocalWorker, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("help", toolruntime.EffectReadOnly, toolruntime.DomainControl, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("status", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("agents", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("conversation_context", toolruntime.EffectReadOnly, toolruntime.DomainControl, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("route_to", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeLocalWorker),
		toolruntime.NewToolPolicy("reply_to", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeLocalWorker),
		toolruntime.NewToolPolicy("broadcast", toolruntime.EffectMutating, toolruntime.DomainCommunication, toolruntime.ExecutionModeLocalWorker),
		toolruntime.NewToolPolicy("task_interact", toolruntime.EffectMutating, toolruntime.DomainOrchestration, toolruntime.ExecutionModeLocalWorker),
		toolruntime.NewToolPolicy("get_routing_history", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("get_agent_capabilities", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("self_diagnostic", toolruntime.EffectReadOnly, toolruntime.DomainSystem, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("evaluate_plan_acceptance", toolruntime.EffectReadOnly, toolruntime.DomainPlanning, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("read_workspace_file", toolruntime.EffectReadOnly, toolruntime.DomainFilesystem, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("workspace_glob", toolruntime.EffectReadOnly, toolruntime.DomainFilesystem, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("workspace_grep", toolruntime.EffectReadOnly, toolruntime.DomainFilesystem, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("inspect_workspace_state", toolruntime.EffectReadOnly, toolruntime.DomainFilesystem, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("summarize_workspace_state", toolruntime.EffectReadOnly, toolruntime.DomainFilesystem, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("sessions", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("metrics", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("switch_session", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeLocalWorker),
		toolruntime.NewToolPolicy("create_session", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeLocalWorker),
		toolruntime.NewToolPolicy("close_session", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeLocalWorker),
	}
	for _, name := range contextskills.ForestSkillNamesForAgent("guide") {
		if registry != nil && registry.Get(name) == nil {
			continue
		}
		policies = append(policies, toolruntime.NewToolPolicy(
			name,
			toolruntime.EffectReadOnly,
			toolruntime.DomainMemory,
			toolruntime.ExecutionModeLocal,
			toolruntime.WithVisibleByDefault(),
		))
	}
	for _, name := range contextskills.ForestMutatingSkillNames() {
		if registry != nil && registry.Get(name) == nil {
			continue
		}
		policies = append(policies, toolruntime.NewToolPolicy(
			name,
			toolruntime.EffectMutating,
			toolruntime.DomainMemory,
			toolruntime.ExecutionModeLocalWorker,
		))
	}
	// Claims skills: query and inspect are read-only + visible-by-default;
	// mutation skills use local_worker execution mode.
	policies = append(policies,
		toolruntime.NewToolPolicy("query_claims_board", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("query_board", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("recall_forward", toolruntime.EffectReadOnly, toolruntime.DomainMemory, toolruntime.ExecutionModeLocal, toolruntime.WithVisibleByDefault()),
		toolruntime.NewToolPolicy("post_action", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeLocalWorker),
		toolruntime.NewToolPolicy("submit_testaments", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeLocalWorker),
		toolruntime.NewToolPolicy("update_claim_progress", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeLocalWorker),
		toolruntime.NewToolPolicy("inspect_claim_conflicts", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("evaluate_validation", toolruntime.EffectMutating, toolruntime.DomainControl, toolruntime.ExecutionModeLocalWorker),
		toolruntime.NewToolPolicy("traverse", toolruntime.EffectReadOnly, toolruntime.DomainObservability, toolruntime.ExecutionModeLocal),
		toolruntime.NewToolPolicy("carry_forward", toolruntime.EffectMutating, toolruntime.DomainMemory, toolruntime.ExecutionModeLocalWorker, toolruntime.WithVisibleByDefault()),
	)
	// Activity Fabric awareness skills must be visible-by-default so the
	// guide's classifier can introspect peer activity (who's busy, who
	// challenged whom) without keyword-based progressive loading missing
	// the catalog. AppendFabricAwarenessToolPolicies registers the
	// uniform fabric.* skill set with read-only, local-execution policy.
	policies = fabric.AppendFabricAwarenessToolPolicies(policies, registry)
	return toolruntime.ApplyAuthorityProfile("guide", toolruntime.NewManifest("guide", "guide.routing", policies...))
}

func registerGuideSafetyHook(hooks *skills.HookRegistry, _ *skills.Registry, allowed []string) {
	if hooks == nil {
		return
	}
	allowedSet := guideAllowedToolSet(allowed)
	hooks.RegisterPreToolCallHook(
		"guide_safety_catch",
		skills.HookPriorityHigh,
		guideSafetyHookFunc(allowedSet),
	)
}

func validateGuideSkillConfiguration(registry *skills.Registry) error {
	return guideToolManifestForRegistry(registry).Validate(registry, false)
}

func filterSyntheticGuideRuntimeTools(names []string) []string {
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if name == toolruntime.SearchToolName {
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}

func guideAllowedToolSet(allowed []string) map[string]bool {
	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = true
	}
	return allowedSet
}

func guideSafetyHookFunc(allowedSet map[string]bool) skills.ToolCallHookFunc {
	return func(ctx context.Context, data *skills.ToolCallHookData) skills.HookResult {
		if data == nil {
			return skills.HookResult{Continue: true}
		}
		if allowedSet[data.ToolName] {
			return skills.HookResult{Continue: true}
		}
		return skills.HookResult{
			Continue: false,
			Error:    fmt.Errorf("SECURITY VIOLATION: Guide agent is not permitted to execute tool %q", data.ToolName),
		}
	}
}
