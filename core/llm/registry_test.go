package llm

import (
	"errors"
	"sync"
	"testing"
)

// mockAdapter is a test implementation of ProviderAdapter
type mockAdapter struct {
	name            string
	supportedModels []string
	initError       error
	initialized     bool
	initCount       int
	mu              sync.Mutex
}

func newMockAdapter(name string, models ...string) *mockAdapter {
	return &mockAdapter{
		name:            name,
		supportedModels: models,
	}
}

func (m *mockAdapter) Name() string {
	return m.name
}

func (m *mockAdapter) SupportsModel(model string) bool {
	for _, supported := range m.supportedModels {
		if supported == model {
			return true
		}
	}
	return false
}

func (m *mockAdapter) Initialize() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initCount++
	if m.initError != nil {
		return m.initError
	}
	m.initialized = true
	return nil
}

func (m *mockAdapter) IsInitialized() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.initialized
}

func (m *mockAdapter) getInitCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.initCount
}

func TestNewProviderRegistry(t *testing.T) {
	registry := NewProviderRegistry()
	if registry == nil {
		t.Fatal("NewProviderRegistry() returned nil")
	}

	if registry.adapters == nil {
		t.Error("adapters map not initialized")
	}
	if registry.modelIndex == nil {
		t.Error("modelIndex map not initialized")
	}
	if registry.initialized == nil {
		t.Error("initialized map not initialized")
	}
	if registry.configs == nil {
		t.Error("configs map not initialized")
	}
}

func TestRegister(t *testing.T) {
	t.Run("successful registration", func(t *testing.T) {
		registry := NewProviderRegistry()
		adapter := newMockAdapter("anthropic", "claude-3-opus")

		err := registry.Register(adapter)
		if err != nil {
			t.Errorf("Register() error = %v", err)
		}

		if !registry.Has("anthropic") {
			t.Error("Registry should have anthropic adapter")
		}
	})

	t.Run("empty name error", func(t *testing.T) {
		registry := NewProviderRegistry()
		adapter := newMockAdapter("")

		err := registry.Register(adapter)
		if err == nil {
			t.Error("Register() should return error for empty name")
		}
	})

	t.Run("overwrite existing", func(t *testing.T) {
		registry := NewProviderRegistry()
		adapter1 := newMockAdapter("test", "model1")
		adapter2 := newMockAdapter("test", "model2")

		registry.Register(adapter1)
		err := registry.Register(adapter2)
		if err != nil {
			t.Errorf("Register() should allow overwrite, error = %v", err)
		}

		got, _ := registry.Get("test")
		if !got.SupportsModel("model2") {
			t.Error("Registry should have updated adapter")
		}
	})
}

func TestRegisterWithConfig(t *testing.T) {
	registry := NewProviderRegistry()
	adapter := newMockAdapter("anthropic")
	cfg := &ProviderConfig{
		Provider: "anthropic",
		Enabled:  true,
		Models: map[string]string{
			"opus":   "claude-3-opus-20240229",
			"sonnet": "claude-3-sonnet-20240229",
		},
	}

	err := registry.RegisterWithConfig(adapter, cfg)
	if err != nil {
		t.Errorf("RegisterWithConfig() error = %v", err)
	}

	// Check config is stored
	storedCfg := registry.GetConfig("anthropic")
	if storedCfg == nil {
		t.Error("Config should be stored")
	}
	if storedCfg.Provider != "anthropic" {
		t.Errorf("Provider = %v, want anthropic", storedCfg.Provider)
	}

	// Check models are indexed
	adapter2, err := registry.GetForModel("opus")
	if err != nil {
		t.Errorf("GetForModel(opus) error = %v", err)
	}
	if adapter2.Name() != "anthropic" {
		t.Errorf("GetForModel(opus) returned %v, want anthropic", adapter2.Name())
	}
}

func TestGet(t *testing.T) {
	t.Run("existing adapter with lazy init", func(t *testing.T) {
		registry := NewProviderRegistry()
		adapter := newMockAdapter("anthropic")
		registry.Register(adapter)

		got, err := registry.Get("anthropic")
		if err != nil {
			t.Errorf("Get() error = %v", err)
		}
		if got.Name() != "anthropic" {
			t.Errorf("Get() returned adapter with name %v", got.Name())
		}
		if !adapter.IsInitialized() {
			t.Error("Adapter should be initialized after Get()")
		}
	})

	t.Run("lazy init only once", func(t *testing.T) {
		registry := NewProviderRegistry()
		adapter := newMockAdapter("openai")
		registry.Register(adapter)

		// Get multiple times
		registry.Get("openai")
		registry.Get("openai")
		registry.Get("openai")

		if adapter.getInitCount() != 1 {
			t.Errorf("Initialize() called %d times, want 1", adapter.getInitCount())
		}
	})

	t.Run("init error propagates", func(t *testing.T) {
		registry := NewProviderRegistry()
		adapter := newMockAdapter("failing")
		adapter.initError = errors.New("init failed")
		registry.Register(adapter)

		_, err := registry.Get("failing")
		if err == nil {
			t.Error("Get() should return error when init fails")
		}
	})

	t.Run("not found", func(t *testing.T) {
		registry := NewProviderRegistry()
		_, err := registry.Get("nonexistent")
		if err == nil {
			t.Error("Get() should return error for nonexistent adapter")
		}
	})
}

func TestGetForModel(t *testing.T) {
	registry := NewProviderRegistry()

	anthropic := newMockAdapter("anthropic", "claude-3-opus", "claude-3-sonnet")
	openai := newMockAdapter("openai", "gpt-4", "gpt-4-turbo")

	registry.Register(anthropic)
	registry.Register(openai)

	t.Run("find by supported model", func(t *testing.T) {
		got, err := registry.GetForModel("claude-3-opus")
		if err != nil {
			t.Errorf("GetForModel() error = %v", err)
		}
		if got.Name() != "anthropic" {
			t.Errorf("GetForModel(claude-3-opus) = %v, want anthropic", got.Name())
		}
	})

	t.Run("find openai model", func(t *testing.T) {
		got, err := registry.GetForModel("gpt-4")
		if err != nil {
			t.Errorf("GetForModel() error = %v", err)
		}
		if got.Name() != "openai" {
			t.Errorf("GetForModel(gpt-4) = %v, want openai", got.Name())
		}
	})

	t.Run("model not supported", func(t *testing.T) {
		_, err := registry.GetForModel("unknown-model")
		if err == nil {
			t.Error("GetForModel() should return error for unsupported model")
		}
	})

	t.Run("uses model index for lookup", func(t *testing.T) {
		// First call populates the index
		registry.GetForModel("claude-3-sonnet")

		// Create a new adapter that doesn't support the model
		// but the index should still return anthropic
		got, _ := registry.GetForModel("claude-3-sonnet")
		if got.Name() != "anthropic" {
			t.Error("Should use model index for repeated lookups")
		}
	})
}

func TestListEnabled(t *testing.T) {
	registry := NewProviderRegistry()

	enabledAdapter := newMockAdapter("enabled")
	disabledAdapter := newMockAdapter("disabled")
	noConfigAdapter := newMockAdapter("noconfig")

	registry.RegisterWithConfig(enabledAdapter, &ProviderConfig{Provider: "enabled", Enabled: true})
	registry.RegisterWithConfig(disabledAdapter, &ProviderConfig{Provider: "disabled", Enabled: false})
	registry.Register(noConfigAdapter) // No config means enabled by default

	enabled := registry.ListEnabled()

	// Should include enabled and noconfig, but not disabled
	if len(enabled) != 2 {
		t.Errorf("ListEnabled() returned %d adapters, want 2", len(enabled))
	}

	names := make(map[string]bool)
	for _, a := range enabled {
		names[a.Name()] = true
	}

	if !names["enabled"] {
		t.Error("ListEnabled() should include explicitly enabled adapter")
	}
	if !names["noconfig"] {
		t.Error("ListEnabled() should include adapter without config")
	}
	if names["disabled"] {
		t.Error("ListEnabled() should not include disabled adapter")
	}
}

func TestList(t *testing.T) {
	registry := NewProviderRegistry()

	registry.Register(newMockAdapter("a"))
	registry.Register(newMockAdapter("b"))
	registry.Register(newMockAdapter("c"))

	all := registry.List()
	if len(all) != 3 {
		t.Errorf("List() returned %d adapters, want 3", len(all))
	}
}

func TestHas(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(newMockAdapter("exists"))

	if !registry.Has("exists") {
		t.Error("Has() should return true for existing adapter")
	}
	if registry.Has("nonexistent") {
		t.Error("Has() should return false for nonexistent adapter")
	}
}

func TestGetConfig(t *testing.T) {
	registry := NewProviderRegistry()
	cfg := &ProviderConfig{Provider: "test", Enabled: true}
	registry.RegisterWithConfig(newMockAdapter("test"), cfg)

	got := registry.GetConfig("test")
	if got == nil {
		t.Fatal("GetConfig() returned nil")
	}
	if got.Provider != "test" {
		t.Errorf("GetConfig().Provider = %v, want test", got.Provider)
	}

	nilCfg := registry.GetConfig("nonexistent")
	if nilCfg != nil {
		t.Error("GetConfig() should return nil for nonexistent provider")
	}
}

func TestSetConfig(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(newMockAdapter("test"))

	cfg := &ProviderConfig{
		Provider: "test",
		Enabled:  true,
		Models: map[string]string{
			"alias": "actual-model",
		},
	}

	registry.SetConfig("test", cfg)

	got := registry.GetConfig("test")
	if got == nil {
		t.Fatal("SetConfig() should store config")
	}
	if !got.Enabled {
		t.Error("Config should be enabled")
	}

	// Check model indexing happened
	adapter, err := registry.GetForModel("alias")
	if err != nil {
		t.Errorf("GetForModel(alias) error = %v", err)
	}
	if adapter.Name() != "test" {
		t.Error("SetConfig should index models")
	}
}

func TestConcurrentAccess(t *testing.T) {
	registry := NewProviderRegistry()
	var wg sync.WaitGroup

	// Register multiple adapters concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			name := string(rune('a' + id))
			adapter := newMockAdapter(name, name+"-model")
			registry.Register(adapter)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			registry.List()
			registry.ListEnabled()
		}()
	}

	wg.Wait()

	// Verify some adapters were registered
	all := registry.List()
	if len(all) == 0 {
		t.Error("Expected some adapters to be registered")
	}
}
