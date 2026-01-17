package librarian

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/security"
)

func TestLibrarianPreIndexHook_SensitiveFile(t *testing.T) {
	t.Parallel()

	sanitizer := security.NewSecretSanitizer()
	hook := NewLibrarianPreIndexHook(sanitizer, nil)

	tests := []string{".env", ".env.local", "config.pem", "private.key"}

	for _, filename := range tests {
		t.Run(filename, func(t *testing.T) {
			data := &IndexHookData{
				FilePath: filename,
				Content:  []byte("SECRET=value"),
			}

			result, err := hook.Handle(context.Background(), data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !result.Metadata["skipped"].(bool) {
				t.Error("sensitive file should be marked as skipped")
			}
			if !strings.Contains(string(result.Content), "not indexed for security") {
				t.Error("content should be stubbed")
			}
		})
	}
}

func TestLibrarianPreIndexHook_NormalFileWithSecrets(t *testing.T) {
	t.Parallel()

	sanitizer := security.NewSecretSanitizer()
	hook := NewLibrarianPreIndexHook(sanitizer, nil)

	data := &IndexHookData{
		FilePath: "main.go",
		Content:  []byte(`const key = "AKIAIOSFODNN7EXAMPLE"`),
	}

	result, err := hook.Handle(context.Background(), data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Metadata["skipped"].(bool) {
		t.Error("normal file should not be skipped")
	}
	if result.Metadata["redaction_count"].(int) == 0 {
		t.Error("should have redactions")
	}
	if strings.Contains(string(result.Content), "AKIAIOSFODNN7EXAMPLE") {
		t.Error("secret should be redacted")
	}
}

func TestLibrarianPreIndexHook_CleanFile(t *testing.T) {
	t.Parallel()

	sanitizer := security.NewSecretSanitizer()
	hook := NewLibrarianPreIndexHook(sanitizer, nil)

	originalContent := []byte(`func main() { fmt.Println("Hello") }`)
	data := &IndexHookData{
		FilePath: "main.go",
		Content:  originalContent,
	}

	result, err := hook.Handle(context.Background(), data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Metadata["sanitized"].(bool) {
		t.Error("clean file should not be marked as sanitized")
	}
	if result.Metadata["redaction_count"].(int) != 0 {
		t.Error("clean file should have no redactions")
	}
	if string(result.Content) != string(originalContent) {
		t.Error("clean content should be unchanged")
	}
}

func TestLibrarianPreIndexHook_Metadata(t *testing.T) {
	t.Parallel()

	sanitizer := security.NewSecretSanitizer()
	hook := NewLibrarianPreIndexHook(sanitizer, nil)

	data := &IndexHookData{
		FilePath: "config.go",
		Content:  []byte(`apiKey = "AKIAIOSFODNN7EXAMPLE"`),
	}

	result, err := hook.Handle(context.Background(), data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := result.Metadata["sanitized"]; !ok {
		t.Error("metadata should have sanitized field")
	}
	if _, ok := result.Metadata["redaction_count"]; !ok {
		t.Error("metadata should have redaction_count field")
	}
	if _, ok := result.Metadata["skipped"]; !ok {
		t.Error("metadata should have skipped field")
	}

	patterns, ok := result.Metadata["matched_patterns"].([]string)
	if !ok || len(patterns) == 0 {
		t.Error("metadata should have matched_patterns")
	}
}

func TestLibrarianPreIndexHook_NilData(t *testing.T) {
	t.Parallel()

	sanitizer := security.NewSecretSanitizer()
	hook := NewLibrarianPreIndexHook(sanitizer, nil)

	result, err := hook.Handle(context.Background(), nil)
	if err != nil {
		t.Errorf("nil data should not error: %v", err)
	}
	if result != nil {
		t.Error("nil data should return nil")
	}
}

func TestLibrarianPreIndexHook_AuditLogging(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	cfg := security.AuditLogConfig{LogPath: logPath}
	logger, _ := security.NewAuditLogger(cfg)
	defer logger.Close()

	sanitizer := security.NewSecretSanitizer()
	hook := NewLibrarianPreIndexHook(sanitizer, logger)

	hook.Handle(context.Background(), &IndexHookData{
		FilePath: ".env",
		Content:  []byte("secret"),
	})

	hook.Handle(context.Background(), &IndexHookData{
		FilePath: "main.go",
		Content:  []byte(`key = "AKIAIOSFODNN7EXAMPLE"`),
	})

	if logger.Sequence() < 2 {
		t.Error("should have logged sanitization events")
	}
}

func TestLibrarianPreIndexHook_HookMetadata(t *testing.T) {
	t.Parallel()

	sanitizer := security.NewSecretSanitizer()
	hook := NewLibrarianPreIndexHook(sanitizer, nil)

	if hook.Name() != "secret_sanitization_index" {
		t.Error("wrong hook name")
	}
	if hook.Type() != security.HookTypePreIndex {
		t.Error("wrong hook type")
	}
	if hook.Priority() != security.HookPriorityFirst {
		t.Error("wrong hook priority")
	}
}

func TestLibrarianPreIndexHook_ExistingMetadata(t *testing.T) {
	t.Parallel()

	sanitizer := security.NewSecretSanitizer()
	hook := NewLibrarianPreIndexHook(sanitizer, nil)

	data := &IndexHookData{
		FilePath: "test.go",
		Content:  []byte("normal content"),
		Metadata: map[string]interface{}{
			"existing_key": "existing_value",
		},
	}

	result, err := hook.Handle(context.Background(), data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Metadata["existing_key"] != "existing_value" {
		t.Error("existing metadata should be preserved")
	}
	if _, ok := result.Metadata["sanitized"]; !ok {
		t.Error("new metadata should be added")
	}
}
