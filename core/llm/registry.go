package llm

import (
	"fmt"
	"sync"
)

// ModelInfo contains metadata and pricing for a model.
type ModelInfo struct {
	ID              string
	Name            string
	MaxContext      int
	InputPricePerM  float64 // Price per million input tokens
	OutputPricePerM float64 // Price per million output tokens
}

type ProviderAdapter interface {
	Name() string
	SupportsModel(model string) bool
	SupportedModels() []ModelInfo
	Initialize() error
	IsInitialized() bool
}

type ProviderRegistry struct {
	mu          sync.RWMutex
	adapters    map[string]ProviderAdapter
	modelIndex  map[string]string
	modelPrices map[string]ModelInfo
	initialized map[string]bool
	configs     map[string]*ProviderConfig
}

func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		adapters:    make(map[string]ProviderAdapter),
		modelIndex:  make(map[string]string),
		modelPrices: make(map[string]ModelInfo),
		initialized: make(map[string]bool),
		configs:     make(map[string]*ProviderConfig),
	}
}

func (r *ProviderRegistry) Register(adapter ProviderAdapter) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := adapter.Name()
	if name == "" {
		return fmt.Errorf("adapter name cannot be empty")
	}

	r.adapters[name] = adapter
	r.initialized[name] = false
	r.indexModelPrices(adapter)
	return nil
}

func (r *ProviderRegistry) indexModelPrices(adapter ProviderAdapter) {
	for _, model := range adapter.SupportedModels() {
		r.modelPrices[model.ID] = model
	}
}

func (r *ProviderRegistry) RegisterWithConfig(adapter ProviderAdapter, cfg *ProviderConfig) error {
	if err := r.Register(adapter); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	name := adapter.Name()
	r.configs[name] = cfg
	r.indexModels(name, cfg)
	return nil
}

func (r *ProviderRegistry) indexModels(providerName string, cfg *ProviderConfig) {
	for alias := range cfg.Models {
		r.modelIndex[alias] = providerName
	}
}

func (r *ProviderRegistry) Get(name string) (ProviderAdapter, error) {
	r.mu.RLock()
	adapter, ok := r.adapters[name]
	initialized := r.initialized[name]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("provider not found: %s", name)
	}

	if !initialized {
		return r.initializeAndReturn(name, adapter)
	}

	return adapter, nil
}

func (r *ProviderRegistry) initializeAndReturn(name string, adapter ProviderAdapter) (ProviderAdapter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.initialized[name] {
		return adapter, nil
	}

	if err := adapter.Initialize(); err != nil {
		return nil, fmt.Errorf("initializing provider %s: %w", name, err)
	}

	r.initialized[name] = true
	return adapter, nil
}

func (r *ProviderRegistry) GetForModel(model string) (ProviderAdapter, error) {
	r.mu.RLock()
	providerName, ok := r.modelIndex[model]
	r.mu.RUnlock()

	if ok {
		return r.Get(providerName)
	}

	return r.findProviderForModel(model)
}

func (r *ProviderRegistry) findProviderForModel(model string) (ProviderAdapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for name, adapter := range r.adapters {
		if adapter.SupportsModel(model) {
			r.modelIndex[model] = name
			return adapter, nil
		}
	}

	return nil, fmt.Errorf("no provider supports model: %s", model)
}

func (r *ProviderRegistry) ListEnabled() []ProviderAdapter {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var enabled []ProviderAdapter
	for name, adapter := range r.adapters {
		cfg := r.configs[name]
		if cfg == nil || cfg.Enabled {
			enabled = append(enabled, adapter)
		}
	}
	return enabled
}

func (r *ProviderRegistry) List() []ProviderAdapter {
	r.mu.RLock()
	defer r.mu.RUnlock()

	adapters := make([]ProviderAdapter, 0, len(r.adapters))
	for _, adapter := range r.adapters {
		adapters = append(adapters, adapter)
	}
	return adapters
}

func (r *ProviderRegistry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.adapters[name]
	return ok
}

func (r *ProviderRegistry) GetConfig(name string) *ProviderConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.configs[name]
}

func (r *ProviderRegistry) SetConfig(name string, cfg *ProviderConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.configs[name] = cfg
	r.indexModels(name, cfg)
}

func (r *ProviderRegistry) GetModelInfo(modelID string) (ModelInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	info, ok := r.modelPrices[modelID]
	return info, ok
}
