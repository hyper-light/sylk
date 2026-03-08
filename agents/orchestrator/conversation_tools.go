package orchestrator

import "github.com/adalundhe/sylk/core/providers"

// conversationAllowedSkills returns the whitelist of read-only skill names
// permitted during conversation mode. These skills query state without
// mutating workflows, DAGs, or health metrics.
func conversationAllowedSkills() []string {
	return []string{
		"search_skills",
		"query_task",
		"query_workflow",
		"generate_summary",
		"query_agent_health",
		"query_health_history",
		"query_buffer",
		"query_dag_status",
		"query_pipeline_state",
		"analyze_plan",
		"read_plan_file",
		"archivalist_request",
	}
}

// buildConversationToolDefinitions returns the subset of tool definitions
// that are safe for conversation mode (read-only queries only).
func (o *Orchestrator) buildConversationToolDefinitions() []providers.Tool {
	allTools := o.buildToolDefinitions()
	if len(allTools) == 0 {
		return nil
	}

	allowed := make(map[string]struct{}, len(conversationAllowedSkills()))
	for _, name := range conversationAllowedSkills() {
		allowed[name] = struct{}{}
	}

	filtered := make([]providers.Tool, 0, len(allowed))
	for _, tool := range allTools {
		if _, ok := allowed[tool.Name]; ok {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}
