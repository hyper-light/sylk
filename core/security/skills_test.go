package security

import (
	"strings"
	"testing"
)

func TestValidateContent_Safe(t *testing.T) {
	t.Parallel()

	skills := NewSecuritySkills(nil)
	result := skills.ValidateContent("Please help me write a function")

	if !result.Safe {
		t.Error("normal content should be safe")
	}
	if len(result.Findings) != 0 {
		t.Error("should have no findings")
	}
}

func TestValidateContent_Unsafe(t *testing.T) {
	t.Parallel()

	skills := NewSecuritySkills(nil)
	result := skills.ValidateContent("Use this key: AKIAIOSFODNN7EXAMPLE")

	if result.Safe {
		t.Error("content with secret should not be safe")
	}
	if len(result.Findings) == 0 {
		t.Error("should have findings")
	}
	if result.Suggestion == "" {
		t.Error("should have suggestion")
	}
}

func TestValidateContent_CriticalSuggestion(t *testing.T) {
	t.Parallel()

	skills := NewSecuritySkills(nil)
	result := skills.ValidateContent("Key: -----BEGIN RSA PRIVATE KEY-----")

	if result.Safe {
		t.Error("private key should not be safe")
	}
	if !strings.Contains(result.Suggestion, "Critical") {
		t.Error("critical secrets should get critical suggestion")
	}
}

func TestCheckFileSensitivity_Env(t *testing.T) {
	t.Parallel()

	skills := NewSecuritySkills(nil)

	tests := []struct {
		path      string
		sensitive bool
		handling  string
	}{
		{".env", true, "skip"},
		{".env.local", true, "skip"},
		{"config.env", true, "skip"},
	}

	for _, tt := range tests {
		result := skills.CheckFileSensitivity(tt.path)
		if result.Sensitive != tt.sensitive {
			t.Errorf("%s: got sensitive=%v, want %v", tt.path, result.Sensitive, tt.sensitive)
		}
		if result.Handling != tt.handling {
			t.Errorf("%s: got handling=%s, want %s", tt.path, result.Handling, tt.handling)
		}
	}
}

func TestCheckFileSensitivity_Keys(t *testing.T) {
	t.Parallel()

	skills := NewSecuritySkills(nil)

	tests := []struct {
		path      string
		sensitive bool
	}{
		{"server.pem", true},
		{"private.key", true},
		{"id_rsa", true},
		{"id_ed25519", true},
	}

	for _, tt := range tests {
		result := skills.CheckFileSensitivity(tt.path)
		if result.Sensitive != tt.sensitive {
			t.Errorf("%s: got sensitive=%v, want %v", tt.path, result.Sensitive, tt.sensitive)
		}
	}
}

func TestCheckFileSensitivity_Normal(t *testing.T) {
	t.Parallel()

	skills := NewSecuritySkills(nil)

	normalFiles := []string{
		"main.go",
		"README.md",
		"package.json",
		"config.yaml",
	}

	for _, path := range normalFiles {
		result := skills.CheckFileSensitivity(path)
		if result.Sensitive {
			t.Errorf("%s should not be sensitive", path)
		}
		if result.Handling != "normal" {
			t.Errorf("%s handling should be normal, got %s", path, result.Handling)
		}
	}
}

func TestSanitizeForDisplay_WithSecrets(t *testing.T) {
	t.Parallel()

	skills := NewSecuritySkills(nil)
	result := skills.SanitizeForDisplay("Key: AKIAIOSFODNN7EXAMPLE")

	if result.RedactionCount == 0 {
		t.Error("should have redactions")
	}
	if strings.Contains(result.Sanitized, "AKIAIOSFODNN7EXAMPLE") {
		t.Error("secret should be redacted")
	}
	if !strings.Contains(result.Sanitized, "[REDACTED]") {
		t.Error("should contain redaction marker")
	}
}

func TestSanitizeForDisplay_Clean(t *testing.T) {
	t.Parallel()

	skills := NewSecuritySkills(nil)
	content := "Hello, World!"
	result := skills.SanitizeForDisplay(content)

	if result.RedactionCount != 0 {
		t.Error("should have no redactions")
	}
	if result.Sanitized != content {
		t.Error("clean content should be unchanged")
	}
}

func TestSecuritySkills_Domain(t *testing.T) {
	t.Parallel()

	skills := NewSecuritySkills(nil)
	if skills.Domain() != "security" {
		t.Error("domain should be security")
	}
}

func TestSecuritySkills_SkillNames(t *testing.T) {
	t.Parallel()

	skills := NewSecuritySkills(nil)
	names := skills.SkillNames()

	expected := []string{"validate_content", "check_file_sensitivity", "sanitize_for_display"}
	if len(names) != len(expected) {
		t.Errorf("expected %d skills, got %d", len(expected), len(names))
	}

	for _, e := range expected {
		found := false
		for _, n := range names {
			if n == e {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing skill: %s", e)
		}
	}
}

func TestSecuritySkills_Execute(t *testing.T) {
	t.Parallel()

	skills := NewSecuritySkills(nil)

	t.Run("validate_content", func(t *testing.T) {
		result := skills.Execute("validate_content", &SkillInput{Content: "test"})
		if _, ok := result.(*ValidateContentResult); !ok {
			t.Error("wrong result type")
		}
	})

	t.Run("check_file_sensitivity", func(t *testing.T) {
		result := skills.Execute("check_file_sensitivity", &SkillInput{Path: "test.txt"})
		if _, ok := result.(*CheckFileSensitivityResult); !ok {
			t.Error("wrong result type")
		}
	})

	t.Run("sanitize_for_display", func(t *testing.T) {
		result := skills.Execute("sanitize_for_display", &SkillInput{Content: "test"})
		if _, ok := result.(*SanitizeForDisplayResult); !ok {
			t.Error("wrong result type")
		}
	})

	t.Run("unknown_skill", func(t *testing.T) {
		result := skills.Execute("unknown", &SkillInput{})
		if result != nil {
			t.Error("unknown skill should return nil")
		}
	})

	t.Run("nil_input", func(t *testing.T) {
		result := skills.Execute("validate_content", nil)
		if result != nil {
			t.Error("nil input should return nil")
		}
	})
}

func TestSecuritySkills_WithCustomSanitizer(t *testing.T) {
	t.Parallel()

	sanitizer := NewSecretSanitizer()
	sanitizer.SetRedactText("***HIDDEN***")

	skills := NewSecuritySkills(sanitizer)
	result := skills.SanitizeForDisplay("Key: AKIAIOSFODNN7EXAMPLE")

	if !strings.Contains(result.Sanitized, "***HIDDEN***") {
		t.Error("should use custom sanitizer")
	}
}
