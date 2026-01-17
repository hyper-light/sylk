package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/storage"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.LLM.Timeout != 2*time.Minute {
		t.Errorf("LLM.Timeout: got %v, want 2m", cfg.LLM.Timeout)
	}
	if cfg.LLM.DefaultProvider != "anthropic" {
		t.Errorf("LLM.DefaultProvider: got %s, want anthropic", cfg.LLM.DefaultProvider)
	}
	if cfg.Memory.GlobalCeilingPercent != 0.8 {
		t.Errorf("Memory.GlobalCeilingPercent: got %v, want 0.8", cfg.Memory.GlobalCeilingPercent)
	}
	if cfg.Broker.DeadlockDetection != true {
		t.Error("Broker.DeadlockDetection should be true")
	}
}

func TestManagerGet(t *testing.T) {
	dirs := &storage.Dirs{
		Config: t.TempDir(),
		Data:   t.TempDir(),
		Cache:  t.TempDir(),
		State:  t.TempDir(),
	}
	m := NewManager(dirs)

	cfg := m.Get()
	if cfg == nil {
		t.Fatal("Get() returned nil")
	}
	if cfg.LLM.DefaultProvider != "anthropic" {
		t.Errorf("Default provider should be anthropic")
	}
}

func TestManagerLoadFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Config: tmpDir,
		Data:   t.TempDir(),
		Cache:  t.TempDir(),
		State:  t.TempDir(),
	}

	configContent := `
llm:
  default_provider: openai
  max_retries: 5
memory:
  global_ceiling_percent: 0.9
`
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	m := NewManager(dirs)
	if err := m.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	cfg := m.Get()
	if cfg.LLM.DefaultProvider != "openai" {
		t.Errorf("Provider: got %s, want openai", cfg.LLM.DefaultProvider)
	}
	if cfg.LLM.MaxRetries != 5 {
		t.Errorf("MaxRetries: got %d, want 5", cfg.LLM.MaxRetries)
	}
	if cfg.Memory.GlobalCeilingPercent != 0.9 {
		t.Errorf("GlobalCeilingPercent: got %v, want 0.9", cfg.Memory.GlobalCeilingPercent)
	}
}

func TestManagerEnvironmentOverride(t *testing.T) {
	dirs := &storage.Dirs{
		Config: t.TempDir(),
		Data:   t.TempDir(),
		Cache:  t.TempDir(),
		State:  t.TempDir(),
	}

	t.Setenv("SYLK_LLM_DEFAULT_PROVIDER", "google")
	t.Setenv("SYLK_LLM_MAX_RETRIES", "10")
	t.Setenv("SYLK_AGENT_DEFAULT_MODEL", "claude-opus-4-20250514")

	m := NewManager(dirs)
	if err := m.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	cfg := m.Get()
	if cfg.LLM.DefaultProvider != "google" {
		t.Errorf("Provider: got %s, want google", cfg.LLM.DefaultProvider)
	}
	if cfg.LLM.MaxRetries != 10 {
		t.Errorf("MaxRetries: got %d, want 10", cfg.LLM.MaxRetries)
	}
	if cfg.Agent.DefaultModel != "claude-opus-4-20250514" {
		t.Errorf("Model: got %s, want claude-opus-4-20250514", cfg.Agent.DefaultModel)
	}
}

func TestManagerOnChange(t *testing.T) {
	dirs := &storage.Dirs{
		Config: t.TempDir(),
		Data:   t.TempDir(),
		Cache:  t.TempDir(),
		State:  t.TempDir(),
	}
	m := NewManager(dirs)

	called := false
	m.OnChange(func(cfg *Config) {
		called = true
	})

	if err := m.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if !called {
		t.Error("OnChange callback should have been called")
	}
}

func TestManagerReload(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := &storage.Dirs{
		Config: tmpDir,
		Data:   t.TempDir(),
		Cache:  t.TempDir(),
		State:  t.TempDir(),
	}

	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("llm:\n  max_retries: 3"), 0644); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	m := NewManager(dirs)
	if err := m.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if m.Get().LLM.MaxRetries != 3 {
		t.Errorf("Initial MaxRetries: got %d, want 3", m.Get().LLM.MaxRetries)
	}

	if err := os.WriteFile(configPath, []byte("llm:\n  max_retries: 7"), 0644); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if err := m.Reload(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	if m.Get().LLM.MaxRetries != 7 {
		t.Errorf("Reloaded MaxRetries: got %d, want 7", m.Get().LLM.MaxRetries)
	}
}

func TestManagerClose(t *testing.T) {
	dirs := &storage.Dirs{
		Config: t.TempDir(),
		Data:   t.TempDir(),
		Cache:  t.TempDir(),
		State:  t.TempDir(),
	}
	m := NewManager(dirs)

	err := m.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	err = m.Close()
	if err != nil {
		t.Errorf("Double close should not fail: %v", err)
	}
}
