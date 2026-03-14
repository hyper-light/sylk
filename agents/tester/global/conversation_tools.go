package global

import "github.com/adalundhe/sylk/core/providers"

// conversationAllowedSkills returns the whitelist of read-only skill names
// permitted during conversation mode. These skills query state without
// mutating test plans, harnesses, or diagnosis reports.
//
// Pipeline-only routing and validation skills are intentionally excluded here.
// Conversation mode should stay read-only and should not depend on pipeline
// turn ownership or lifecycle rules.
func conversationAllowedSkills() []string {
	return []string{
		"search_skills",
		"analyze_risk",
		"analyze_batch",
		"analyze_integration_risks",
		"diagnose_failure",
		"reroute_request",
	}
}

// buildConversationToolDefinitions returns the subset of tool definitions
// that are safe for conversation mode (read-only queries only).
func (gt *GlobalTester) buildConversationToolDefinitions() []providers.Tool {
	allTools := gt.buildToolDefinitions()
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
