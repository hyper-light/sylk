package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/security"
)

func TestUserPromptFlow_APIKey(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()

	// Use a realistic OpenAI key format: sk-[20+ alphanumeric chars]
	prompt := "Use this API key: sk-abcdefghijklmnopqrstuvwxyz12345"
	result := sanitizer.CheckUserPrompt(prompt)

	if !result.HasFindings() {
		t.Error("expected prompt with API key to have findings")
	}
	if len(result.Findings) == 0 {
		t.Error("expected findings for API key")
	}

	hasKeyType := false
	for _, finding := range result.Findings {
		if strings.Contains(finding.PatternName, "openai") {
			hasKeyType = true
		}
	}
	if !hasKeyType {
		t.Error("expected finding to identify OpenAI key pattern")
	}
}

func TestUserPromptFlow_PrivateKey(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()

	prompt := `Please use this key:
-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEA0m59l2u9iDnMbrXHfq
-----END RSA PRIVATE KEY-----`

	result := sanitizer.CheckUserPrompt(prompt)

	if !result.HasFindings() {
		t.Error("expected prompt with private key to have findings")
	}
}

func TestUserPromptFlow_CleanPrompt(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()

	prompt := "Please help me write a function to parse JSON files"
	result := sanitizer.CheckUserPrompt(prompt)

	if result.HasFindings() {
		t.Errorf("expected clean prompt to pass, got findings: %v", result.Findings)
	}
}

func TestLibrarianIndexingFlow_EnvFile(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()

	envContent := `
DATABASE_URL=postgres://user:password123@localhost/db
API_KEY=sk-secret-key-12345
DEBUG=true
`

	result, sanitizeResult := sanitizer.SanitizeForIndex("/project/.env", []byte(envContent))

	if !sanitizeResult.IsStubbed {
		t.Error("expected .env file to be stubbed")
	}

	if strings.Contains(string(result), "password123") {
		t.Error("stubbed content should not contain password")
	}
	if strings.Contains(string(result), "sk-secret-key") {
		t.Error("stubbed content should not contain API key")
	}
}

func TestLibrarianIndexingFlow_ConfigWithSecrets(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()

	configContent := `
{
  "database": {
    "host": "localhost",
    "password": "super-secret-password"
  },
  "api_key": "sk-abcdefghijklmnopqrstuvwxyz12345"
}
`

	result, sanitizeResult := sanitizer.SanitizeForIndex("/project/config.json", []byte(configContent))

	if !sanitizeResult.Sanitized && sanitizeResult.RedactionCount == 0 {
		t.Log("config.json: checking for API key redaction")
	}

	resultStr := string(result)
	if strings.Contains(resultStr, "sk-abcdefghij") {
		t.Error("config should have API key redacted")
	}
}

func TestLibrarianIndexingFlow_NormalCode(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()

	codeContent := `
package main

func main() {
    fmt.Println("Hello, World!")
}
`

	result, sanitizeResult := sanitizer.SanitizeForIndex("/project/main.go", []byte(codeContent))

	if sanitizeResult.IsStubbed {
		t.Error("normal code should not be stubbed")
	}
	if sanitizeResult.RedactionCount > 0 {
		t.Errorf("expected no redactions in normal code, got %d", sanitizeResult.RedactionCount)
	}
	if string(result) != codeContent {
		t.Error("normal code content should be unchanged")
	}
}

func TestToolOutputFlow_ConnectionString(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()

	output := `Connected to database:
postgres://admin:MyP@ssw0rd!@db.example.com:5432/production
Query completed in 2ms`

	sanitized, count := sanitizer.SanitizeToolOutput(output)

	if strings.Contains(sanitized, "MyP@ssw0rd!") {
		t.Error("password should be redacted from tool output")
	}
	if count == 0 {
		t.Error("expected at least one redaction")
	}
}

func TestToolOutputFlow_CleanOutput(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()

	output := "File created successfully at /path/to/file.txt"
	sanitized, count := sanitizer.SanitizeToolOutput(output)

	if sanitized != output {
		t.Error("clean output should be unchanged")
	}
	if count != 0 {
		t.Errorf("expected no redactions, got %d", count)
	}
}

func TestAgentToAgentFlow_SecretInMessage(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()
	hook := security.NewInterAgentSecretHook(sanitizer, nil)

	data := &security.DispatchHookData{
		SourceAgent: "engineer",
		TargetAgent: "architect",
		Message:     "Here's the API key: ghp_abc123xyz456789012345678901234567890",
		SessionID:   "test-session",
	}

	result, err := hook.Handle(context.Background(), data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result.Message, "ghp_abc123") {
		t.Error("GitHub token should be redacted in transit")
	}
	if !strings.Contains(result.Message, "[REDACTED") {
		t.Error("expected redaction marker in message")
	}
}

func TestEnvVarIsolationFlow_APIKeyParam(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()
	hook := security.NewPreToolEnvVarHook(sanitizer, nil)

	data := &security.ToolCallHookData{
		ToolName: "http_request",
		Parameters: map[string]interface{}{
			"url":           "https://api.example.com",
			"authorization": "Bearer sk-live-abc123xyz456",
		},
		AgentID: "test-agent",
	}

	_, err := hook.Handle(context.Background(), data)
	if err == nil {
		t.Error("expected error when API key passed as parameter")
	}

	envErr, ok := err.(*security.EnvVarInjectionError)
	if !ok {
		t.Errorf("expected EnvVarInjectionError, got: %T", err)
	} else if envErr.ParamName != "authorization" {
		t.Errorf("expected param 'authorization', got: %s", envErr.ParamName)
	}
}

func TestEnvVarIsolationFlow_NormalParam(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()
	hook := security.NewPreToolEnvVarHook(sanitizer, nil)

	data := &security.ToolCallHookData{
		ToolName: "read_file",
		Parameters: map[string]interface{}{
			"path": "/path/to/file.txt",
		},
		AgentID: "test-agent",
	}

	_, err := hook.Handle(context.Background(), data)
	if err != nil {
		t.Errorf("expected no error for normal params, got: %v", err)
	}
}

func TestNoSecretsLeakToLLMContext(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()

	testCases := []struct {
		name    string
		content string
	}{
		{"AWS Access Key", "AKIAIOSFODNN7EXAMPLE"},
		{"GitHub Token", "ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
		{"OpenAI Key", "sk-abcdefghijklmnopqrstuvwxyz12345"},
		{"Slack Token", "xoxb-123456789012-1234567890123-abcdefghijklmn"},
		{"Private Key", "-----BEGIN RSA PRIVATE KEY-----\nMIIE...\n-----END RSA PRIVATE KEY-----"},
		{"Database URL", "postgres://user:secret@host:5432/db"},
		{"JWT Token", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := sanitizer.CheckUserPrompt(tc.content)
			if !result.HasFindings() {
				t.Errorf("%s should be detected as secret", tc.name)
			}

			sanitized, count := sanitizer.SanitizeToolOutput(tc.content)
			if count == 0 || sanitized == tc.content {
				t.Errorf("%s should be redacted from tool output", tc.name)
			}
		})
	}
}

func TestNoFalsePositivesOnCommonPatterns(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()

	falsePositiveCases := []string{
		"The function returns a key-value pair",
		"Use API endpoint /api/users",
		"The token count is 1500",
		"Set password_length=16 in config",
		"AWS region: us-east-1",
		"GitHub repository: owner/repo",
		"Base64 encoding: YmFzZTY0",
		"Random UUID: 123e4567-e89b-12d3-a456-426614174000",
		"Version string: v1.2.3-beta",
	}

	for _, content := range falsePositiveCases {
		name := content
		if len(name) > 30 {
			name = content[:30]
		}
		t.Run(name, func(t *testing.T) {
			result := sanitizer.CheckUserPrompt(content)
			if result.HasFindings() {
				t.Errorf("false positive detected for: %s, findings: %v", content, result.Findings)
			}
		})
	}
}

func TestPerformance_SanitizationReasonable(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()

	content := strings.Repeat("This is a normal line of text without secrets. ", 100)

	iterations := 10
	start := time.Now()

	for i := 0; i < iterations; i++ {
		_, _ = sanitizer.SanitizeToolOutput(content)
	}

	elapsed := time.Since(start)
	avgTime := elapsed / time.Duration(iterations)

	if avgTime > 100*time.Millisecond {
		t.Errorf("sanitization too slow: avg %v, expected < 100ms", avgTime)
	}
}

func TestPrePromptSecretDetectionHook(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()
	hook := security.NewPrePromptSecretHook(sanitizer, nil)

	t.Run("blocks secret", func(t *testing.T) {
		data := &security.PromptHookData{
			Prompt:  "Use API key sk-abcdefghijklmnopqrstuvwxyz12345",
			AgentID: "test",
		}

		_, err := hook.Handle(context.Background(), data)
		if err == nil {
			t.Error("expected error for prompt with secret")
		}

		secretErr, ok := err.(*security.SecretDetectedError)
		if !ok {
			t.Errorf("expected SecretDetectedError, got: %T", err)
		} else if len(secretErr.Findings) == 0 {
			t.Error("expected findings in error")
		}
	})

	t.Run("allows clean prompt", func(t *testing.T) {
		data := &security.PromptHookData{
			Prompt:  "Help me write a function",
			AgentID: "test",
		}

		_, err := hook.Handle(context.Background(), data)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})
}

func TestToolOutputSanitizationHook(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()
	hook := security.NewPostToolSecretHook(sanitizer, nil)

	data := &security.ToolCallHookData{
		ToolName: "exec",
		Output:   "Connection string: mongodb://user:password@host/db",
		AgentID:  "test",
	}

	result, err := hook.Handle(context.Background(), data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result.Output, "password") {
		t.Error("password should be redacted from tool result")
	}
}

func TestSecretTestFiles(t *testing.T) {
	fixturesDir := filepath.Join("..", "fixtures", "secret_test_files")

	if _, err := os.Stat(fixturesDir); os.IsNotExist(err) {
		t.Skip("fixtures directory not available")
	}

	sanitizer := security.NewSecretSanitizer()

	testFiles := map[string]bool{
		".env":        true,
		".env.local":  true,
		"config.json": false,
		"main.go":     false,
		"id_rsa":      true,
		"credentials": true,
	}

	for filename, shouldStub := range testFiles {
		t.Run(filename, func(t *testing.T) {
			path := filepath.Join(fixturesDir, filename)

			content, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("test file not available: %s", filename)
				return
			}

			_, result := sanitizer.SanitizeForIndex(path, content)

			if result.IsStubbed != shouldStub {
				t.Errorf("file %s: expected stubbed=%v, got %v", filename, shouldStub, result.IsStubbed)
			}
		})
	}
}

func TestE2E_CompleteFlow(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()

	userPrompt := "Check my database connection"
	promptResult := sanitizer.CheckUserPrompt(userPrompt)
	if promptResult.HasFindings() {
		t.Fatal("clean prompt should pass")
	}

	envContent := "DB_PASSWORD=secret123"
	_, indexResult := sanitizer.SanitizeForIndex("/.env", []byte(envContent))
	if !indexResult.IsStubbed {
		t.Error("env file should be stubbed")
	}

	toolOutput := "Connection: postgres://user:mysecretpassword@host:5432/db"
	sanitizedOutput, count := sanitizer.SanitizeToolOutput(toolOutput)
	if count == 0 || strings.Contains(sanitizedOutput, "mysecretpassword") {
		t.Error("tool output should have connection string redacted")
	}

	interAgentHook := security.NewInterAgentSecretHook(sanitizer, nil)
	msgData := &security.DispatchHookData{
		SourceAgent: "engineer",
		TargetAgent: "architect",
		Message:     "Found key: sk-abcdefghijklmnopqrstuvwxyz12345",
		SessionID:   "test",
	}
	sanitizedMsg, _ := interAgentHook.Handle(context.Background(), msgData)
	if strings.Contains(sanitizedMsg.Message, "sk-abcdefgh") {
		t.Error("inter-agent message should be sanitized")
	}
}

func TestAuditLogging(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "audit-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logPath := filepath.Join(tempDir, "audit.log")

	auditLogger, err := security.NewAuditLogger(security.AuditLogConfig{
		LogPath: logPath,
	})
	if err != nil {
		t.Fatalf("failed to create audit logger: %v", err)
	}

	sanitizer := security.NewSecretSanitizer()
	hook := security.NewPrePromptSecretHook(sanitizer, auditLogger)

	data := &security.PromptHookData{
		Prompt:    "API key: sk-abcdefghijklmnopqrstuvwxyz12345",
		SessionID: "session-1",
		AgentID:   "agent-1",
	}

	_, _ = hook.Handle(context.Background(), data)

	auditLogger.Close()

	querier := security.NewAuditQuerier(logPath)
	entries, err := querier.Query(security.QueryFilter{
		Categories: []security.AuditCategory{security.AuditCategorySecret},
	})

	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}

	if len(entries) == 0 {
		t.Error("expected audit entry for secret detection")
	}

	found := false
	for _, e := range entries {
		if e.EventType == "secret_detected_prompt" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected secret_detected_prompt event type in audit log")
	}
}

func TestMetricsTracking(t *testing.T) {
	sanitizer := security.NewSecretSanitizer()

	sanitizer.SanitizeForIndex("/test/.env", []byte("SECRET=value"))
	sanitizer.SanitizeToolOutput("API key: sk-abcdefghijklmnopqrstuvwxyz12345")

	metrics := sanitizer.GetMetrics()

	if metrics.TotalScanned == 0 {
		t.Error("expected TotalScanned > 0")
	}
	if metrics.TotalRedacted == 0 {
		t.Error("expected TotalRedacted > 0")
	}
}
