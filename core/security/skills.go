package security

import (
	"path/filepath"
)

type ValidateContentResult struct {
	Safe       bool     `json:"safe"`
	Findings   []string `json:"findings"`
	Suggestion string   `json:"suggestion"`
}

type CheckFileSensitivityResult struct {
	Sensitive bool   `json:"sensitive"`
	Pattern   string `json:"pattern,omitempty"`
	Handling  string `json:"handling"`
}

type SanitizeForDisplayResult struct {
	Sanitized      string `json:"sanitized"`
	RedactionCount int    `json:"redaction_count"`
}

type SecuritySkills struct {
	sanitizer *SecretSanitizer
}

func NewSecuritySkills(sanitizer *SecretSanitizer) *SecuritySkills {
	if sanitizer == nil {
		sanitizer = NewSecretSanitizer()
	}
	return &SecuritySkills{sanitizer: sanitizer}
}

func (s *SecuritySkills) ValidateContent(content string) *ValidateContentResult {
	detection := s.sanitizer.CheckUserPrompt(content)

	result := &ValidateContentResult{
		Safe:     !detection.HasFindings(),
		Findings: make([]string, 0),
	}

	if !detection.HasFindings() {
		return result
	}

	for _, f := range detection.Findings {
		result.Findings = append(result.Findings, f.PatternName)
	}

	result.Suggestion = buildSuggestion(detection)
	return result
}

func buildSuggestion(detection *SecretDetection) string {
	if len(detection.Findings) == 0 {
		return ""
	}

	severity := detection.HighestSeverity()
	switch severity {
	case SecretSeverityCritical:
		return "Critical secrets detected. Use environment variables or a secrets manager instead of hardcoding."
	case SecretSeverityHigh:
		return "High-risk secrets detected. Consider using environment variables."
	default:
		return "Potential secrets detected. Review the content before sharing."
	}
}

func (s *SecuritySkills) CheckFileSensitivity(path string) *CheckFileSensitivityResult {
	filename := filepath.Base(path)

	for _, pattern := range SensitiveFilePatterns() {
		if matched, _ := matchGlob(pattern, filename); matched {
			return &CheckFileSensitivityResult{
				Sensitive: true,
				Pattern:   pattern,
				Handling:  determineHandling(pattern),
			}
		}
	}

	return &CheckFileSensitivityResult{
		Sensitive: false,
		Handling:  "normal",
	}
}

var skipPatterns = map[string]bool{
	".env": true, ".env.*": true, "*.env": true,
	"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true, "*.ppk": true,
}

var redactPatterns = map[string]bool{
	"*credentials*": true, "*secret*": true, "*.pem": true, "*.key": true,
}

func determineHandling(pattern string) string {
	if skipPatterns[pattern] {
		return "skip"
	}
	if redactPatterns[pattern] {
		return "redact"
	}
	return "skip"
}

func (s *SecuritySkills) SanitizeForDisplay(content string) *SanitizeForDisplayResult {
	sanitized, count := s.sanitizer.SanitizeToolOutput(content)
	return &SanitizeForDisplayResult{
		Sanitized:      sanitized,
		RedactionCount: count,
	}
}

func (s *SecuritySkills) Domain() string {
	return "security"
}

func (s *SecuritySkills) SkillNames() []string {
	return []string{
		"validate_content",
		"check_file_sensitivity",
		"sanitize_for_display",
	}
}

type SkillInput struct {
	Content string `json:"content,omitempty"`
	Path    string `json:"path,omitempty"`
}

type skillExecutor func(*SecuritySkills, *SkillInput) any

var skillExecutors = map[string]skillExecutor{
	"validate_content":       func(s *SecuritySkills, i *SkillInput) any { return s.ValidateContent(i.Content) },
	"check_file_sensitivity": func(s *SecuritySkills, i *SkillInput) any { return s.CheckFileSensitivity(i.Path) },
	"sanitize_for_display":   func(s *SecuritySkills, i *SkillInput) any { return s.SanitizeForDisplay(i.Content) },
}

func (s *SecuritySkills) Execute(skillName string, input *SkillInput) any {
	if input == nil {
		return nil
	}
	if executor, ok := skillExecutors[skillName]; ok {
		return executor(s, input)
	}
	return nil
}
