package global

import "github.com/adalundhe/sylk/core/providers"

// conversationAllowedSkills returns the whitelist of read-only skill names
// permitted during conversation mode. These skills query state without
// mutating audit results or triggering analysis runs.
func conversationAllowedSkills() []string {
	return []string{
		"search_skills",
		"read_file",
		"glob",
		"grep",
		"escalate_findings",
		"reroute_request",
	}
}

// buildConversationToolDefinitions returns the subset of tool definitions
// that are safe for conversation mode (read-only queries only).
func (gi *GlobalInspector) buildConversationToolDefinitions() []providers.Tool {
	allTools := gi.buildToolDefinitions()
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
