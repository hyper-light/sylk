package guide

import (
	promptskill "github.com/adalundhe/sylk/core/promptskills"
	coreskills "github.com/adalundhe/sylk/core/skills"
	promptskills "github.com/adalundhe/sylk/skills"
)

// GuideGoSkills returns Go-coded skills compiled into the binary that do not
// require a live Guide instance. Use this at bootstrap before the Guide exists.
func GuideGoSkills() []*coreskills.Skill {
	return []*coreskills.Skill{
		evaluatePlanAcceptanceSkill(nil),
	}
}

// DiscoverGuidePromptSkills returns SKILL.md metadata exposed to the guide's
// Gemini provider. This includes all agent skillfiles so guide classification
// can reason about system-wide capabilities. Go-coded skills are merged so
// that binary builds (where SKILL.md files are absent) still expose skill
// context. Disk skills take precedence on name collision.
func DiscoverGuidePromptSkills(goSkills []*coreskills.Skill) []promptskills.Skill {
	diskSkills := promptskill.DiscoverAllAgentSkills()
	return promptskill.MergePromptSkills(diskSkills, goSkills)
}
