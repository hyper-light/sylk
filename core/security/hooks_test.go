package security

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrePromptSecretHook_DetectsSecrets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	cfg := AuditLogConfig{LogPath: logPath}
	logger, _ := NewAuditLogger(cfg)
	defer logger.Close()

	sanitizer := NewSecretSanitizer()
	hook := NewPrePromptSecretHook(sanitizer, logger)

	tests := []struct {
		name        string
		prompt      string
		shouldError bool
	}{
		{"api_key", "Use api_key = 'abc123def456ghi789jkl012mno'", true},
		{"aws_key", "My key is AKIAIOSFODNN7EXAMPLE", true},
		{"github_pat", "Token: ghp_1234567890abcdefghijklmnopqrstuvwxyz", true},
		{"openai_key", "Use sk-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN", true},
		{"clean", "How do I write a function in Go?", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := &PromptHookData{Prompt: tt.prompt, SessionID: "test-session"}
			_, err := hook.Handle(context.Background(), data)

			if tt.shouldError && err == nil {
				t.Error("expected error for secret in prompt")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if tt.shouldError {
				var secretErr *SecretDetectedError
				if !errors.As(err, &secretErr) {
					t.Error("error should be SecretDetectedError")
				}
			}
		})
	}
}

func TestPrePromptSecretHook_ErrorMessage(t *testing.T) {
	t.Parallel()

	sanitizer := NewSecretSanitizer()
	hook := NewPrePromptSecretHook(sanitizer, nil)

	data := &PromptHookData{Prompt: "key: AKIAIOSFODNN7EXAMPLE"}
	_, err := hook.Handle(context.Background(), data)

	if err == nil {
		t.Fatal("expected error")
	}

	var secretErr *SecretDetectedError
	if !errors.As(err, &secretErr) {
		t.Fatal("should be SecretDetectedError")
	}

	msg := secretErr.Error()
	if len(msg) == 0 {
		t.Error("error message should not be empty")
	}

	if strContains(msg, "AKIAIOSFODNN7EXAMPLE") {
		t.Error("error message should not contain the actual secret")
	}
}

func TestPrePromptSecretHook_NilData(t *testing.T) {
	t.Parallel()

	sanitizer := NewSecretSanitizer()
	hook := NewPrePromptSecretHook(sanitizer, nil)

	result, err := hook.Handle(context.Background(), nil)
	if err != nil {
		t.Errorf("nil data should not error: %v", err)
	}
	if result != nil {
		t.Error("nil data should return nil")
	}
}

func TestPostToolSecretHook_RedactsSecrets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	cfg := AuditLogConfig{LogPath: logPath}
	logger, _ := NewAuditLogger(cfg)
	defer logger.Close()

	sanitizer := NewSecretSanitizer()
	hook := NewPostToolSecretHook(sanitizer, logger)

	tests := []struct {
		name          string
		output        string
		shouldRedact  bool
		shouldContain string
	}{
		{
			name:          "aws_key",
			output:        `{"key": "AKIAIOSFODNN7EXAMPLE"}`,
			shouldRedact:  true,
			shouldContain: "[REDACTED]",
		},
		{
			name:          "multiple",
			output:        `key1: AKIAIOSFODNN7EXAMPLE key2: ghp_1234567890abcdefghijklmnopqrstuvwxyz`,
			shouldRedact:  true,
			shouldContain: "[REDACTED]",
		},
		{
			name:          "clean",
			output:        `{"status": "ok"}`,
			shouldRedact:  false,
			shouldContain: `"status": "ok"`,
		},
		{
			name:          "empty",
			output:        "",
			shouldRedact:  false,
			shouldContain: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := &ToolCallHookData{
				ToolName: "test_tool",
				Output:   tt.output,
			}

			result, err := hook.Handle(context.Background(), data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.shouldRedact && !strContains(result.Output, "[REDACTED]") {
				t.Error("output should contain redaction marker")
			}
			if tt.shouldContain != "" && !strContains(result.Output, tt.shouldContain) {
				t.Errorf("output should contain %q", tt.shouldContain)
			}
		})
	}
}

func TestPostToolSecretHook_NoLeakedSecrets(t *testing.T) {
	t.Parallel()

	sanitizer := NewSecretSanitizer()
	hook := NewPostToolSecretHook(sanitizer, nil)

	secrets := []string{
		"AKIAIOSFODNN7EXAMPLE",
		"ghp_1234567890abcdefghijklmnopqrstuvwxyz",
		"sk-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN",
	}

	data := &ToolCallHookData{
		ToolName: "test",
		Output:   strings.Join(secrets, " "),
	}

	result, _ := hook.Handle(context.Background(), data)

	for _, secret := range secrets {
		if strContains(result.Output, secret) {
			t.Errorf("secret %q should be redacted", secret)
		}
	}
}

func TestPreToolEnvVarHook_BlocksSecrets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	cfg := AuditLogConfig{LogPath: logPath}
	logger, _ := NewAuditLogger(cfg)
	defer logger.Close()

	sanitizer := NewSecretSanitizer()
	hook := NewPreToolEnvVarHook(sanitizer, logger)

	tests := []struct {
		name        string
		params      map[string]any
		shouldError bool
	}{
		{
			name: "connection_string",
			params: map[string]any{
				"database": "postgres://user:password123@localhost:5432/db",
			},
			shouldError: true,
		},
		{
			name: "api_key_value",
			params: map[string]any{
				"token": "api_key = 'abc123def456ghi789jkl012mno'",
			},
			shouldError: true,
		},
		{
			name: "normal_string",
			params: map[string]any{
				"path": "/home/user/file.txt",
			},
			shouldError: false,
		},
		{
			name: "file_path",
			params: map[string]any{
				"file": "/etc/hosts",
			},
			shouldError: false,
		},
		{
			name: "integer_param",
			params: map[string]any{
				"count": 42,
			},
			shouldError: false,
		},
		{
			name:        "nil_params",
			params:      nil,
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := &ToolCallHookData{
				ToolName:   "test_tool",
				Parameters: tt.params,
			}

			_, err := hook.Handle(context.Background(), data)

			if tt.shouldError && err == nil {
				t.Error("expected error for secret in parameter")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if tt.shouldError {
				var injErr *EnvVarInjectionError
				if !errors.As(err, &injErr) {
					t.Error("error should be EnvVarInjectionError")
				}
			}
		})
	}
}

func TestPreToolEnvVarHook_ErrorMessage(t *testing.T) {
	t.Parallel()

	sanitizer := NewSecretSanitizer()
	hook := NewPreToolEnvVarHook(sanitizer, nil)

	data := &ToolCallHookData{
		ToolName: "database_tool",
		Parameters: map[string]any{
			"connection": "postgres://user:secret@localhost/db",
		},
	}

	_, err := hook.Handle(context.Background(), data)
	if err == nil {
		t.Fatal("expected error")
	}

	var injErr *EnvVarInjectionError
	if !errors.As(err, &injErr) {
		t.Fatal("should be EnvVarInjectionError")
	}

	if injErr.ToolName != "database_tool" {
		t.Errorf("tool name should be database_tool, got %s", injErr.ToolName)
	}
	if injErr.ParamName != "connection" {
		t.Errorf("param name should be connection, got %s", injErr.ParamName)
	}
}

func TestHookMetadata(t *testing.T) {
	t.Parallel()

	sanitizer := NewSecretSanitizer()

	prePrompt := NewPrePromptSecretHook(sanitizer, nil)
	if prePrompt.Name() != "secret_detection" {
		t.Error("wrong name for pre-prompt hook")
	}
	if prePrompt.Type() != HookTypePrePrompt {
		t.Error("wrong type for pre-prompt hook")
	}
	if prePrompt.Priority() != HookPriorityFirst {
		t.Error("wrong priority for pre-prompt hook")
	}

	postTool := NewPostToolSecretHook(sanitizer, nil)
	if postTool.Name() != "secret_redaction_output" {
		t.Error("wrong name for post-tool hook")
	}
	if postTool.Type() != HookTypePostTool {
		t.Error("wrong type for post-tool hook")
	}
	if postTool.Priority() != HookPriorityLast {
		t.Error("wrong priority for post-tool hook")
	}

	preTool := NewPreToolEnvVarHook(sanitizer, nil)
	if preTool.Name() != "env_var_isolation" {
		t.Error("wrong name for pre-tool hook")
	}
	if preTool.Type() != HookTypePreTool {
		t.Error("wrong type for pre-tool hook")
	}
	if preTool.Priority() != HookPriorityFirst {
		t.Error("wrong priority for pre-tool hook")
	}
}

func TestHooksAuditLogging(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	cfg := AuditLogConfig{LogPath: logPath}
	logger, _ := NewAuditLogger(cfg)

	sanitizer := NewSecretSanitizer()

	prePromptHook := NewPrePromptSecretHook(sanitizer, logger)
	data := &PromptHookData{
		Prompt:    "key: AKIAIOSFODNN7EXAMPLE",
		SessionID: "session-123",
		AgentID:   "agent-456",
	}
	prePromptHook.Handle(context.Background(), data)

	postToolHook := NewPostToolSecretHook(sanitizer, logger)
	toolData := &ToolCallHookData{
		ToolName:  "test_tool",
		Output:    "secret: AKIAIOSFODNN7EXAMPLE",
		SessionID: "session-123",
	}
	postToolHook.Handle(context.Background(), toolData)

	preToolHook := NewPreToolEnvVarHook(sanitizer, logger)
	envData := &ToolCallHookData{
		ToolName: "db_tool",
		Parameters: map[string]any{
			"conn": "postgres://u:p@host/db",
		},
	}

	preToolHook.Handle(context.Background(), envData)

	logger.Close()

	if logger.Sequence() < 3 {
		t.Error("should have logged entries from all hooks")
	}
}

func strContains(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) && findSubstr(s, substr)
}

func findSubstr(s, substr string) bool {
	return strings.Contains(s, substr)
}
