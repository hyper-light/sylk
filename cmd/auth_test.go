package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsValidProvider(t *testing.T) {
	tests := []struct {
		provider string
		want     bool
	}{
		{"anthropic", true},
		{"openai", true},
		{"google", true},
		{"invalid", false},
		{"ANTHROPIC", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := isValidProvider(tt.provider)
			if got != tt.want {
				t.Errorf("isValidProvider(%q) = %v, want %v", tt.provider, got, tt.want)
			}
		})
	}
}

func TestEnsureCredentialsDir(t *testing.T) {
	dir, err := ensureCredentialsDir()
	if err != nil {
		t.Fatalf("ensureCredentialsDir() error = %v", err)
	}

	if dir == "" {
		t.Error("ensureCredentialsDir() returned empty path")
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", dir, err)
	}

	if !info.IsDir() {
		t.Errorf("%q is not a directory", dir)
	}
}

func TestLoadCredentialsFileNotExists(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	creds, err := loadCredentialsFile()
	if err != nil {
		t.Fatalf("loadCredentialsFile() error = %v", err)
	}

	if creds == nil {
		t.Error("loadCredentialsFile() returned nil map")
	}

	if len(creds) != 0 {
		t.Errorf("loadCredentialsFile() returned %d credentials, want 0", len(creds))
	}
}

func TestSaveAndLoadCredentials(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	testCreds := map[string]string{
		"anthropic": "sk-ant-test123",
		"openai":    "sk-test456",
	}

	if err := saveCredentialsFile(testCreds); err != nil {
		t.Fatalf("saveCredentialsFile() error = %v", err)
	}

	path := filepath.Join(tmpDir, ".sylk", "credentials.yaml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}

	if info.Mode().Perm() != 0600 {
		t.Errorf("file permissions = %o, want 0600", info.Mode().Perm())
	}

	loaded, err := loadCredentialsFile()
	if err != nil {
		t.Fatalf("loadCredentialsFile() error = %v", err)
	}

	if len(loaded) != len(testCreds) {
		t.Errorf("loaded %d credentials, want %d", len(loaded), len(testCreds))
	}

	for k, v := range testCreds {
		if loaded[k] != v {
			t.Errorf("loaded[%q] = %q, want %q", k, loaded[k], v)
		}
	}
}

func TestSaveCredential(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	if err := saveCredential("anthropic", "sk-ant-test"); err != nil {
		t.Fatalf("saveCredential() error = %v", err)
	}

	if err := saveCredential("openai", "sk-openai-test"); err != nil {
		t.Fatalf("saveCredential() error = %v", err)
	}

	creds, err := loadCredentialsFile()
	if err != nil {
		t.Fatalf("loadCredentialsFile() error = %v", err)
	}

	if creds["anthropic"] != "sk-ant-test" {
		t.Errorf("anthropic credential = %q, want %q", creds["anthropic"], "sk-ant-test")
	}

	if creds["openai"] != "sk-openai-test" {
		t.Errorf("openai credential = %q, want %q", creds["openai"], "sk-openai-test")
	}
}

func TestRemoveCredential(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	testCreds := map[string]string{
		"anthropic": "sk-ant-test",
		"openai":    "sk-openai-test",
	}
	if err := saveCredentialsFile(testCreds); err != nil {
		t.Fatalf("saveCredentialsFile() error = %v", err)
	}

	if err := removeCredential("anthropic"); err != nil {
		t.Fatalf("removeCredential() error = %v", err)
	}

	creds, err := loadCredentialsFile()
	if err != nil {
		t.Fatalf("loadCredentialsFile() error = %v", err)
	}

	if _, ok := creds["anthropic"]; ok {
		t.Error("anthropic credential still exists after removal")
	}

	if creds["openai"] != "sk-openai-test" {
		t.Error("openai credential was incorrectly modified")
	}
}

func TestRemoveCredentialNotExists(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	if err := removeCredential("anthropic"); err != nil {
		t.Fatalf("removeCredential() error = %v", err)
	}
}

func TestSaveGoogleCredentialsFile(t *testing.T) {
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	srcPath := filepath.Join(tmpDir, "test-creds.json")
	content := `{"type": "service_account", "project_id": "test"}`
	if err := os.WriteFile(srcPath, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := saveGoogleCredentialsFile(srcPath); err != nil {
		t.Fatalf("saveGoogleCredentialsFile() error = %v", err)
	}

	destPath := filepath.Join(tmpDir, ".sylk", "google-credentials.json")
	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", destPath, err)
	}

	if string(data) != content {
		t.Errorf("file content = %q, want %q", string(data), content)
	}

	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", destPath, err)
	}

	if info.Mode().Perm() != 0600 {
		t.Errorf("file permissions = %o, want 0600", info.Mode().Perm())
	}
}

func TestSaveGoogleCredentialsFileNotExists(t *testing.T) {
	err := saveGoogleCredentialsFile("/nonexistent/path/creds.json")
	if err == nil {
		t.Error("saveGoogleCredentialsFile() expected error for non-existent file")
	}
}
