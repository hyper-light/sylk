package security

import (
	"strings"
	"sync"
	"testing"
)

func TestSanitizeForIndex_SensitiveFile(t *testing.T) {
	t.Parallel()

	s := NewSecretSanitizer()

	tests := []string{".env", ".env.local", "config.pem", "private.key", "credentials.json"}

	for _, filename := range tests {
		content := []byte("SECRET_KEY=abc123supersecret")
		result, info := s.SanitizeForIndex(filename, content)

		if !info.IsStubbed {
			t.Errorf("file %s should be stubbed", filename)
		}
		if !strings.Contains(string(result), "not indexed for security") {
			t.Errorf("stub for %s should contain security message", filename)
		}
		if !strings.Contains(string(result), filename) {
			t.Errorf("stub for %s should contain filename", filename)
		}
	}
}

func TestSanitizeForIndex_NormalFile(t *testing.T) {
	t.Parallel()

	s := NewSecretSanitizer()
	content := []byte(`package main

const apiKey = "sk-1234567890abcdefghijklmnopqrstuvwxyzABCDEF"

func main() {}
`)

	result, info := s.SanitizeForIndex("main.go", content)

	if info.IsStubbed {
		t.Error("normal file should not be stubbed")
	}
	if !info.Sanitized {
		t.Error("content with API key should be sanitized")
	}
	if info.RedactionCount == 0 {
		t.Error("should have redactions")
	}
	if strings.Contains(string(result), "sk-1234567890") {
		t.Error("API key should be redacted")
	}
	if !strings.Contains(string(result), "[REDACTED]") {
		t.Error("should contain redaction marker")
	}
}

func TestSanitizeForIndex_CleanFile(t *testing.T) {
	t.Parallel()

	s := NewSecretSanitizer()
	content := []byte(`package main

func main() {
	fmt.Println("Hello, World!")
}
`)

	result, info := s.SanitizeForIndex("main.go", content)

	if info.IsStubbed {
		t.Error("clean file should not be stubbed")
	}
	if info.Sanitized {
		t.Error("clean file should not be marked as sanitized")
	}
	if info.RedactionCount != 0 {
		t.Error("clean file should have no redactions")
	}
	if string(result) != string(content) {
		t.Error("clean content should be unchanged")
	}
}

func TestCheckUserPrompt_WithSecrets(t *testing.T) {
	t.Parallel()

	s := NewSecretSanitizer()

	tests := []struct {
		prompt      string
		shouldFind  bool
		patternName string
	}{
		{"Please use api_key = 'abc123def456ghi789jkl012mno'", true, "api_key_generic"},
		{"Here's my key: AKIAIOSFODNN7EXAMPLE", true, "aws_access_key"},
		{"Token: ghp_1234567890abcdefghijklmnopqrstuvwxyz", true, "github_pat"},
		{"Use this: sk-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN", true, "openai_key"},
		{"Just a normal question about coding", false, ""},
	}

	for _, tt := range tests {
		detection := s.CheckUserPrompt(tt.prompt)

		if tt.shouldFind && !detection.HasFindings() {
			t.Errorf("prompt %q should have findings", tt.prompt)
		}
		if !tt.shouldFind && detection.HasFindings() {
			t.Errorf("prompt %q should not have findings", tt.prompt)
		}
		if tt.shouldFind {
			found := false
			for _, f := range detection.Findings {
				if f.PatternName == tt.patternName {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("prompt %q should match pattern %s", tt.prompt, tt.patternName)
			}
		}
	}
}

func TestCheckUserPrompt_MaskedContext(t *testing.T) {
	t.Parallel()

	s := NewSecretSanitizer()
	prompt := "My AWS key is AKIAIOSFODNN7EXAMPLE and it works"

	detection := s.CheckUserPrompt(prompt)

	if !detection.HasFindings() {
		t.Fatal("should detect AWS key")
	}

	finding := detection.Findings[0]
	if strings.Contains(finding.Context, "AKIAIOSFODNN7EXAMPLE") {
		t.Error("context should mask the secret")
	}
	if !strings.Contains(finding.Context, "***") {
		t.Error("context should contain mask marker")
	}
}

func TestSanitizeToolOutput(t *testing.T) {
	t.Parallel()

	s := NewSecretSanitizer()

	tests := []struct {
		name          string
		output        string
		expectRedact  bool
		redactCount   int
		shouldContain string
	}{
		{
			name:          "with_api_key",
			output:        `{"api_key": "sk-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN"}`,
			expectRedact:  true,
			redactCount:   1,
			shouldContain: "[REDACTED]",
		},
		{
			name:          "multiple_secrets",
			output:        `key1: AKIAIOSFODNN7EXAMPLE, key2: AKIAI44QH8DHBEXAMPLE`,
			expectRedact:  true,
			redactCount:   2,
			shouldContain: "[REDACTED]",
		},
		{
			name:          "no_secrets",
			output:        `{"status": "ok", "count": 42}`,
			expectRedact:  false,
			redactCount:   0,
			shouldContain: `"status": "ok"`,
		},
		{
			name:          "empty_output",
			output:        "",
			expectRedact:  false,
			redactCount:   0,
			shouldContain: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, count := s.SanitizeToolOutput(tt.output)

			if tt.expectRedact && count == 0 {
				t.Error("should have redactions")
			}
			if !tt.expectRedact && count > 0 {
				t.Error("should not have redactions")
			}
			if tt.shouldContain != "" && !strings.Contains(result, tt.shouldContain) {
				t.Errorf("result should contain %q", tt.shouldContain)
			}
		})
	}
}

func TestSanitizeToolOutput_NoLeakedSecrets(t *testing.T) {
	t.Parallel()

	s := NewSecretSanitizer()
	secrets := []string{
		"AKIAIOSFODNN7EXAMPLE",
		"ghp_1234567890abcdefghijklmnopqrstuvwxyz",
		"sk-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN",
		"-----BEGIN RSA PRIVATE KEY-----",
	}

	output := strings.Join(secrets, " | ")
	result, _ := s.SanitizeToolOutput(output)

	for _, secret := range secrets {
		if strings.Contains(result, secret) {
			t.Errorf("secret %q should be redacted", secret)
		}
	}
}

func TestMetrics(t *testing.T) {
	t.Parallel()

	s := NewSecretSanitizer()

	s.SanitizeForIndex(".env", []byte("secret"))
	s.SanitizeForIndex(".env.local", []byte("secret"))

	s.SanitizeForIndex("main.go", []byte("api_key = 'abcdefghijklmnopqrstuvwx'"))
	s.SanitizeToolOutput("AKIAIOSFODNN7EXAMPLE")

	metrics := s.GetMetrics()

	if metrics.TotalScanned < 2 {
		t.Error("should track scanned files")
	}
	if len(metrics.SkippedFiles) < 2 {
		t.Error("should track skipped files")
	}
	if metrics.TotalRedacted == 0 {
		t.Error("should track redactions")
	}
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()

	s := NewSecretSanitizer()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(3)

		go func() {
			defer wg.Done()
			s.SanitizeForIndex("test.go", []byte("api_key = 'abcdefghijklmnopqrstuvwx'"))
		}()

		go func() {
			defer wg.Done()
			s.CheckUserPrompt("AKIAIOSFODNN7EXAMPLE")
		}()

		go func() {
			defer wg.Done()
			s.SanitizeToolOutput("ghp_1234567890abcdefghijklmnopqrstuvwxyz")
		}()
	}

	wg.Wait()

	metrics := s.GetMetrics()
	if metrics.TotalRedacted == 0 {
		t.Error("concurrent operations should track redactions")
	}
}

func TestCustomRedactText(t *testing.T) {
	t.Parallel()

	s := NewSecretSanitizer()
	s.SetRedactText("***HIDDEN***")

	content := []byte("key: AKIAIOSFODNN7EXAMPLE")
	result, _ := s.SanitizeForIndex("test.txt", content)

	if !strings.Contains(string(result), "***HIDDEN***") {
		t.Error("should use custom redact text")
	}
	if strings.Contains(string(result), "[REDACTED]") {
		t.Error("should not use default redact text")
	}
}

func TestSecretDetection_HighestSeverity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		findings []SecretFinding
		expected SecretSeverity
	}{
		{
			name:     "empty",
			findings: nil,
			expected: SecretSeverityLow,
		},
		{
			name: "single_critical",
			findings: []SecretFinding{
				{Severity: SecretSeverityCritical},
			},
			expected: SecretSeverityCritical,
		},
		{
			name: "mixed",
			findings: []SecretFinding{
				{Severity: SecretSeverityLow},
				{Severity: SecretSeverityHigh},
				{Severity: SecretSeverityMedium},
			},
			expected: SecretSeverityHigh,
		},
		{
			name: "all_low",
			findings: []SecretFinding{
				{Severity: SecretSeverityLow},
				{Severity: SecretSeverityLow},
			},
			expected: SecretSeverityLow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &SecretDetection{Findings: tt.findings}
			if got := d.HighestSeverity(); got != tt.expected {
				t.Errorf("got %s, want %s", got, tt.expected)
			}
		})
	}
}

func TestAddRemovePattern(t *testing.T) {
	t.Parallel()

	s := NewSecretSanitizer()

	content := []byte("CUSTOM_ABC123DEF456GHI789")
	_, info := s.SanitizeForIndex("test.txt", content)
	initialCount := info.RedactionCount

	s.AddPattern(newPattern("custom_test", `CUSTOM_[A-Z0-9]{18}`, SecretSeverityHigh))

	_, info2 := s.SanitizeForIndex("test.txt", content)
	if info2.RedactionCount <= initialCount {
		t.Error("custom pattern should add redactions")
	}

	s.RemovePattern("custom_test")

	_, info3 := s.SanitizeForIndex("test.txt", content)
	if info3.RedactionCount != initialCount {
		t.Error("redaction count should return to initial after removing pattern")
	}
}

func TestNoFalsePositives_Sanitizer(t *testing.T) {
	t.Parallel()

	s := NewSecretSanitizer()

	normalCode := []string{
		`func getAPIKey() string { return os.Getenv("API_KEY") }`,
		`// Comment about passwords`,
		`const MaxKeyLength = 256`,
		`type Config struct { KeyPath string }`,
	}

	for _, code := range normalCode {
		_, info := s.SanitizeForIndex("test.go", []byte(code))
		if info.RedactionCount > 0 {
			t.Errorf("false positive on: %q", code)
		}
	}
}
