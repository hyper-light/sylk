package security

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestInterAgentSecretHook_RedactsSecrets(t *testing.T) {
	t.Parallel()

	sanitizer := NewSecretSanitizer()
	hook := NewInterAgentSecretHook(sanitizer, nil)

	tests := []struct {
		name          string
		message       string
		shouldRedact  bool
		shouldContain string
	}{
		{
			name:          "aws_key",
			message:       `Here is the key: AKIAIOSFODNN7EXAMPLE`,
			shouldRedact:  true,
			shouldContain: "[REDACTED]",
		},
		{
			name:          "multiple_secrets",
			message:       `Keys: AKIAIOSFODNN7EXAMPLE and ghp_1234567890abcdefghijklmnopqrstuvwxyz`,
			shouldRedact:  true,
			shouldContain: "[REDACTED]",
		},
		{
			name:          "clean_message",
			message:       `Please process this task`,
			shouldRedact:  false,
			shouldContain: "Please process this task",
		},
		{
			name:          "empty_message",
			message:       "",
			shouldRedact:  false,
			shouldContain: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := &DispatchHookData{
				SourceAgent: "agent-a",
				TargetAgent: "agent-b",
				Message:     tt.message,
			}

			result, err := hook.Handle(context.Background(), data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.shouldRedact && !strings.Contains(result.Message, "[REDACTED]") {
				t.Error("message should contain redaction marker")
			}
			if tt.shouldContain != "" && !strings.Contains(result.Message, tt.shouldContain) {
				t.Errorf("message should contain %q", tt.shouldContain)
			}
		})
	}
}

func TestInterAgentSecretHook_NoLeakedSecrets(t *testing.T) {
	t.Parallel()

	sanitizer := NewSecretSanitizer()
	hook := NewInterAgentSecretHook(sanitizer, nil)

	secrets := []string{
		"AKIAIOSFODNN7EXAMPLE",
		"ghp_1234567890abcdefghijklmnopqrstuvwxyz",
		"sk-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN",
	}

	data := &DispatchHookData{
		SourceAgent: "agent-a",
		TargetAgent: "agent-b",
		Message:     strings.Join(secrets, " | "),
	}

	result, _ := hook.Handle(context.Background(), data)

	for _, secret := range secrets {
		if strings.Contains(result.Message, secret) {
			t.Errorf("secret %q should be redacted", secret)
		}
	}
}

func TestInterAgentSecretHook_NilData(t *testing.T) {
	t.Parallel()

	sanitizer := NewSecretSanitizer()
	hook := NewInterAgentSecretHook(sanitizer, nil)

	result, err := hook.Handle(context.Background(), nil)
	if err != nil {
		t.Errorf("nil data should not error: %v", err)
	}
	if result != nil {
		t.Error("nil data should return nil")
	}
}

func TestInterAgentSecretHook_AuditLogging(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	cfg := AuditLogConfig{LogPath: logPath}
	logger, _ := NewAuditLogger(cfg)
	defer logger.Close()

	sanitizer := NewSecretSanitizer()
	hook := NewInterAgentSecretHook(sanitizer, logger)

	data := &DispatchHookData{
		SourceAgent: "planner",
		TargetAgent: "executor",
		Message:     "Use this key: AKIAIOSFODNN7EXAMPLE",
		SessionID:   "session-123",
	}

	hook.Handle(context.Background(), data)

	if logger.Sequence() == 0 {
		t.Error("should have logged redaction event")
	}
}

func TestInterAgentSecretHook_HookMetadata(t *testing.T) {
	t.Parallel()

	sanitizer := NewSecretSanitizer()
	hook := NewInterAgentSecretHook(sanitizer, nil)

	if hook.Name() != "secret_sanitization_transit" {
		t.Error("wrong hook name")
	}
	if hook.Type() != HookTypePreDispatch {
		t.Error("wrong hook type")
	}
	if hook.Priority() != HookPriorityLast {
		t.Error("wrong hook priority")
	}
}

func TestInterAgentSecretHook_PreservesAgentInfo(t *testing.T) {
	t.Parallel()

	sanitizer := NewSecretSanitizer()
	hook := NewInterAgentSecretHook(sanitizer, nil)

	data := &DispatchHookData{
		SourceAgent: "agent-source",
		TargetAgent: "agent-target",
		Message:     "normal message",
		SessionID:   "session-456",
	}

	result, _ := hook.Handle(context.Background(), data)

	if result.SourceAgent != "agent-source" {
		t.Error("source agent should be preserved")
	}
	if result.TargetAgent != "agent-target" {
		t.Error("target agent should be preserved")
	}
	if result.SessionID != "session-456" {
		t.Error("session ID should be preserved")
	}
}
