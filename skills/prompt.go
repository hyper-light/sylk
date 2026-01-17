package skills

import (
	"html"
	"strings"
)

// ToPrompt generates the <available_skills> XML block for system prompts
func ToPrompt(skills []Skill) string {
	if len(skills) == 0 {
		return "<available_skills>\n</available_skills>"
	}

	var b strings.Builder
	b.WriteString("<available_skills>\n")

	for _, s := range skills {
		writeSkillXML(&b, s)
	}

	b.WriteString("</available_skills>")
	return b.String()
}

func writeSkillXML(b *strings.Builder, s Skill) {
	b.WriteString("<skill>\n")
	b.WriteString("<name>\n")
	b.WriteString(html.EscapeString(s.Name))
	b.WriteString("\n</name>\n")
	b.WriteString("<description>\n")
	b.WriteString(html.EscapeString(s.Description))
	b.WriteString("\n</description>\n")
	b.WriteString("<location>\n")
	b.WriteString(s.Path)
	b.WriteString("\n</location>\n")
	b.WriteString("</skill>\n")
}
