package agent

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNewAgentModelStore_NoFile(t *testing.T) {
	store := NewAgentModelStore(filepath.Join(t.TempDir(), "missing.yaml"))
	if got := store.ModelFor("guide"); got != "" {
		t.Errorf("ModelFor(guide) = %q, want empty", got)
	}
	if got := store.ProviderFor("guide"); got != "" {
		t.Errorf("ProviderFor(guide) = %q, want empty", got)
	}
}

func TestNewAgentModelStore_LoadsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := []byte(`agents:
    guide:
        provider: google
        model: gemini-3.1-pro-preview
    architect:
        provider: anthropic
        model: claude-opus-4-6
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	store := NewAgentModelStore(path)
	if got := store.ModelFor("guide"); got != "gemini-3.1-pro-preview" {
		t.Errorf("ModelFor(guide) = %q, want gemini-3.1-pro-preview", got)
	}
	if got := store.ProviderFor("guide"); got != "google" {
		t.Errorf("ProviderFor(guide) = %q, want google", got)
	}
	if got := store.ModelFor("architect"); got != "claude-opus-4-6" {
		t.Errorf("ModelFor(architect) = %q, want claude-opus-4-6", got)
	}
	if got := store.ProviderFor("architect"); got != "anthropic" {
		t.Errorf("ProviderFor(architect) = %q, want anthropic", got)
	}
	if got := store.ModelFor("unknown"); got != "" {
		t.Errorf("ModelFor(unknown) = %q, want empty", got)
	}
}

func TestNewAgentModelStore_BackwardCompat_NoProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// Legacy format: no provider key — should derive from model prefix.
	content := []byte(`agents:
    guide:
        model: gemini-3.1-pro-preview
    architect:
        model: claude-opus-4-6
    engineer:
        model: gpt-5.4-pro
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	store := NewAgentModelStore(path)
	cases := map[string]struct{ model, provider string }{
		"guide":     {"gemini-3.1-pro-preview", "google"},
		"architect": {"claude-opus-4-6", "anthropic"},
		"engineer":  {"gpt-5.4-pro", "openai"},
	}
	for agentType, want := range cases {
		entry := store.EntryFor(agentType)
		if entry.Model != want.model {
			t.Errorf("EntryFor(%q).Model = %q, want %q", agentType, entry.Model, want.model)
		}
		if entry.Provider != want.provider {
			t.Errorf("EntryFor(%q).Provider = %q, want %q (derived)", agentType, entry.Provider, want.provider)
		}
	}
}

func TestSetModel_PreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := []byte(`ui:
    git_panel:
        commits:
            columns:
                - id: hash
                  visible: true
agents:
    guide:
        provider: google
        model: gemini-3.1-pro-preview
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	store := NewAgentModelStore(path)
	if err := store.SetModel("architect", "claude-opus-4-6"); err != nil {
		t.Fatal(err)
	}

	// Re-read the file and verify the ui section is preserved.
	reloaded := NewAgentModelStore(path)
	if got := reloaded.ModelFor("guide"); got != "gemini-3.1-pro-preview" {
		t.Errorf("guide model lost: got %q", got)
	}
	if got := reloaded.ModelFor("architect"); got != "claude-opus-4-6" {
		t.Errorf("architect model not saved: got %q", got)
	}
	if got := reloaded.ProviderFor("architect"); got != "anthropic" {
		t.Errorf("architect provider not derived: got %q", got)
	}

	// Verify the ui section still exists in the raw YAML.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(data)
	if !contains(raw, "git_panel") {
		t.Error("ui.git_panel section was lost")
	}
}

func TestSetEntry_WritesProviderAndModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	store := NewAgentModelStore(path)
	if err := store.SetEntry("guide", "google", "gemini-3.1-pro-preview"); err != nil {
		t.Fatal(err)
	}

	reloaded := NewAgentModelStore(path)
	entry := reloaded.EntryFor("guide")
	if entry.Provider != "google" {
		t.Errorf("provider = %q, want google", entry.Provider)
	}
	if entry.Model != "gemini-3.1-pro-preview" {
		t.Errorf("model = %q, want gemini-3.1-pro-preview", entry.Model)
	}

	// Verify both keys present in raw YAML.
	data, _ := os.ReadFile(path)
	raw := string(data)
	if !contains(raw, "provider: google") {
		t.Error("provider key missing from YAML")
	}
	if !contains(raw, "model: gemini-3.1-pro-preview") {
		t.Error("model key missing from YAML")
	}
}

func TestSetModel_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	store := NewAgentModelStore(path)
	if err := store.SetModel("guide", "gemini-3.1-pro-preview"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetModel("guide", "claude-sonnet-4-6"); err != nil {
		t.Fatal(err)
	}

	reloaded := NewAgentModelStore(path)
	if got := reloaded.ModelFor("guide"); got != "claude-sonnet-4-6" {
		t.Errorf("overwrite failed: got %q, want claude-sonnet-4-6", got)
	}
	if got := reloaded.ProviderFor("guide"); got != "anthropic" {
		t.Errorf("provider after overwrite: got %q, want anthropic", got)
	}
}

func TestEnsureDefaults_BatchWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// Pre-populate one agent.
	content := []byte(`agents:
    guide:
        provider: anthropic
        model: claude-sonnet-4-6
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	store := NewAgentModelStore(path)
	defaults := map[string]AgentConfigEntry{
		"guide":     {Provider: "google", Model: "gemini-3.1-pro-preview"},
		"architect": {Provider: "anthropic", Model: "claude-opus-4-6"},
		"engineer":  {Provider: "openai", Model: "gpt-5.4-pro"},
	}
	if err := store.EnsureDefaults(defaults); err != nil {
		t.Fatal(err)
	}

	// guide should NOT be overwritten — it already existed.
	if got := store.ModelFor("guide"); got != "claude-sonnet-4-6" {
		t.Errorf("guide overwritten: got %q, want claude-sonnet-4-6", got)
	}
	// architect and engineer should be written.
	if got := store.ModelFor("architect"); got != "claude-opus-4-6" {
		t.Errorf("architect default missing: got %q", got)
	}
	if got := store.ProviderFor("architect"); got != "anthropic" {
		t.Errorf("architect provider missing: got %q", got)
	}
	if got := store.ModelFor("engineer"); got != "gpt-5.4-pro" {
		t.Errorf("engineer default missing: got %q", got)
	}
	if got := store.ProviderFor("engineer"); got != "openai" {
		t.Errorf("engineer provider missing: got %q", got)
	}
}

func TestEnsureDefaults_NoOpWhenAllPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := []byte(`agents:
    guide:
        provider: google
        model: gemini-3.1-pro-preview
`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	store := NewAgentModelStore(path)
	// Get file mod time before EnsureDefaults.
	infoBefore, _ := os.Stat(path)

	if err := store.EnsureDefaults(map[string]AgentConfigEntry{
		"guide": {Provider: "google", Model: "gemini-3.1-pro-preview"},
	}); err != nil {
		t.Fatal(err)
	}

	// File should not have been rewritten (no dirty entries).
	infoAfter, _ := os.Stat(path)
	if infoBefore.ModTime() != infoAfter.ModTime() {
		t.Error("EnsureDefaults rewrote file when all defaults were present")
	}
}

func TestConcurrentSetModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	store := NewAgentModelStore(path)

	var wg sync.WaitGroup
	agents := []string{"guide", "architect", "engineer", "designer", "inspector", "tester"}
	for _, agent := range agents {
		wg.Add(1)
		go func(a string) {
			defer wg.Done()
			if err := store.SetModel(a, a+"-model"); err != nil {
				t.Errorf("SetModel(%s) error: %v", a, err)
			}
		}(agent)
	}
	wg.Wait()

	// Verify all models persisted.
	reloaded := NewAgentModelStore(path)
	for _, agent := range agents {
		if got := reloaded.ModelFor(agent); got != agent+"-model" {
			t.Errorf("ModelFor(%s) = %q, want %s-model", agent, got, agent)
		}
	}
}

func TestNewAgentModelStore_CorruptedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte(":::invalid yaml:::"), 0644); err != nil {
		t.Fatal(err)
	}

	store := NewAgentModelStore(path)
	if got := store.ModelFor("guide"); got != "" {
		t.Errorf("ModelFor(guide) = %q from corrupted file, want empty", got)
	}

	// SetModel should still work — creates fresh structure.
	if err := store.SetModel("guide", "gemini-3.1-pro-preview"); err != nil {
		t.Fatal(err)
	}
	reloaded := NewAgentModelStore(path)
	if got := reloaded.ModelFor("guide"); got != "gemini-3.1-pro-preview" {
		t.Errorf("after recovery: ModelFor(guide) = %q", got)
	}
}

func TestEntryFor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	store := NewAgentModelStore(path)
	if err := store.SetEntry("architect", "anthropic", "claude-opus-4-6"); err != nil {
		t.Fatal(err)
	}

	entry := store.EntryFor("architect")
	if entry.Provider != "anthropic" || entry.Model != "claude-opus-4-6" {
		t.Errorf("EntryFor = %+v, want {anthropic, claude-opus-4-6}", entry)
	}

	// Missing agent returns zero value.
	empty := store.EntryFor("unknown")
	if empty.Provider != "" || empty.Model != "" {
		t.Errorf("EntryFor(unknown) = %+v, want zero", empty)
	}
}

func TestDeriveProvider(t *testing.T) {
	cases := map[string]string{
		"claude-opus-4-6":        "anthropic",
		"claude-sonnet-4-6":      "anthropic",
		"gemini-3.1-pro-preview": "google",
		"gpt-5.4-pro":         "openai",
		"gpt-5.4-pro":         "openai",
		"unknown-model":          "",
	}
	for modelID, want := range cases {
		got := DeriveProvider(modelID)
		if got != want {
			t.Errorf("DeriveProvider(%q) = %q, want %q", modelID, got, want)
		}
	}
}

// contains checks if substr is present in s (avoids importing strings in test).
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
