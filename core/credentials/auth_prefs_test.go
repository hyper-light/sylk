package credentials

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveAndLoadAuthPref_ProjectConfigCanonicalizesOpenAIOAuth(t *testing.T) {
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "repo")
	configDir := filepath.Join(projectDir, ".sylk")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}

	if err := SaveAuthPref("openai", "oauth"); err != nil {
		t.Fatalf("SaveAuthPref() error = %v", err)
	}
	if got := LoadAuthPref("openai"); got != "chatgpt" {
		t.Fatalf("LoadAuthPref(openai) = %q, want chatgpt", got)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) == "" {
		t.Fatal("expected config.yaml to be updated")
	}
	if got := string(data); !strings.Contains(got, "chatgpt") {
		t.Fatalf("config.yaml = %q, want persisted chatgpt method", got)
	}
}
