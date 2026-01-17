package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Discover finds all skills in a directory
func Discover(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read skills dir: %w", err)
	}

	var skills []Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skill, err := ReadProperties(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		skills = append(skills, skill)
	}
	return skills, nil
}

// ReadProperties reads skill properties from SKILL.md frontmatter
func ReadProperties(skillDir string) (Skill, error) {
	skillMD, err := findSkillMD(skillDir)
	if err != nil {
		return Skill{}, err
	}

	content, err := os.ReadFile(skillMD)
	if err != nil {
		return Skill{}, fmt.Errorf("read SKILL.md: %w", err)
	}

	metadata, _, err := ParseFrontmatter(string(content))
	if err != nil {
		return Skill{}, err
	}

	skill, err := metadataToSkill(metadata)
	if err != nil {
		return Skill{}, err
	}
	skill.Path = skillMD
	return skill, nil
}

func ReadSkillString(skillString string) (*Skill, error) {

	metadata, _, err := ParseFrontmatter(skillString)
	if err != nil {
		return &Skill{}, err
	}

	skill, err := metadataToSkill(metadata)
	if err != nil {
		return &Skill{}, err
	}
	skill.Path, err = os.Getwd()
	if err != nil {
		return nil, err
	}

	return &skill, nil

}

// ReadFull reads skill properties and instructions
func ReadFull(skillDir string) (Skill, string, error) {
	skillMD, err := findSkillMD(skillDir)
	if err != nil {
		return Skill{}, "", err
	}

	content, err := os.ReadFile(skillMD)
	if err != nil {
		return Skill{}, "", fmt.Errorf("read SKILL.md: %w", err)
	}

	metadata, body, err := ParseFrontmatter(string(content))
	if err != nil {
		return Skill{}, "", err
	}

	skill, err := metadataToSkill(metadata)
	if err != nil {
		return Skill{}, "", err
	}
	skill.Path = skillMD
	return skill, body, nil
}

func findSkillMD(skillDir string) (string, error) {
	for _, name := range []string{"SKILL.md", "skill.md"} {
		path := filepath.Join(skillDir, name)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", ErrSkillNotFound
}

// ParseFrontmatter extracts YAML frontmatter and markdown body
func ParseFrontmatter(content string) (map[string]any, string, error) {
	if !strings.HasPrefix(content, "---") {
		return nil, "", ErrParseFailed
	}

	parts := strings.SplitN(content[3:], "---", 2)
	if len(parts) < 2 {
		return nil, "", ErrParseFailed
	}

	var metadata map[string]any
	if err := yaml.Unmarshal([]byte(parts[0]), &metadata); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrParseFailed, err)
	}

	return metadata, strings.TrimSpace(parts[1]), nil
}

func metadataToSkill(m map[string]any) (Skill, error) {
	name, _ := m["name"].(string)
	desc, _ := m["description"].(string)

	if name == "" {
		return Skill{}, ErrMissingName
	}
	if desc == "" {
		return Skill{}, ErrMissingDesc
	}

	skill := Skill{
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(desc),
	}

	if v, ok := m["license"].(string); ok {
		skill.License = v
	}
	if v, ok := m["compatibility"].(string); ok {
		skill.Compatibility = v
	}
	if v, ok := m["allowed-tools"].(string); ok {
		skill.AllowedTools = v
	}
	if v, ok := m["metadata"].(map[string]any); ok {
		skill.Metadata = toStringMap(v)
	}

	return skill, nil
}

func toStringMap(m map[string]any) map[string]string {
	result := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	return result
}
