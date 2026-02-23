package guide

import (
	promptskill "github.com/adalundhe/sylk/core/promptskills"
	promptskills "github.com/adalundhe/sylk/skills"
)

// DiscoverGuidePromptSkills returns SKILL.md metadata exposed to the guide's
// Gemini provider. This includes all agent skillfiles so guide classification
// can reason about system-wide capabilities.
func DiscoverGuidePromptSkills() []promptskills.Skill {
	return promptskill.DiscoverAllAgentSkills()
}
