package providers

import (
	"context"
	"fmt"
	"sync"
)

// Registry manages multiple provider instances and provides
// a unified interface for provider selection and routing
type Registry struct {
	mu sync.RWMutex

	providers map[ProviderType]Provider
	default_  ProviderType
}

// NewRegistry creates a new provider registry
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[ProviderType]Provider),
	}
}

// Register adds a provider to the registry
func (r *Registry) Register(providerType ProviderType, provider Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := provider.ValidateConfig(); err != nil {
		return fmt.Errorf("invalid provider config for %s: %w", providerType, err)
	}

	r.providers[providerType] = provider

	// Set as default if first provider
	if len(r.providers) == 1 {
		r.default_ = providerType
	}

	return nil
}

// RegisterAnthropic creates and registers an Anthropic provider
func (r *Registry) RegisterAnthropic(config AnthropicConfig) error {
	provider, err := NewAnthropicProvider(config)
	if err != nil {
		return err
	}
	return r.Register(ProviderTypeAnthropic, provider)
}

// RegisterOpenAI creates and registers an OpenAI provider
func (r *Registry) RegisterOpenAI(config OpenAIConfig) error {
	provider, err := NewOpenAIProvider(config)
	if err != nil {
		return err
	}
	return r.Register(ProviderTypeOpenAI, provider)
}

// RegisterGoogle creates and registers a Google provider
func (r *Registry) RegisterGoogle(ctx context.Context, config GoogleConfig) error {
	provider, err := NewGoogleProvider(ctx, config)
	if err != nil {
		return err
	}
	return r.Register(ProviderTypeGoogle, provider)
}

// Get returns a provider by type
func (r *Registry) Get(providerType ProviderType) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	provider, ok := r.providers[providerType]
	if !ok {
		return nil, fmt.Errorf("provider not registered: %s", providerType)
	}
	return provider, nil
}

// Default returns the default provider
func (r *Registry) Default() (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.default_ == "" {
		return nil, fmt.Errorf("no default provider set")
	}
	return r.providers[r.default_], nil
}

// SetDefault sets the default provider
func (r *Registry) SetDefault(providerType ProviderType) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.providers[providerType]; !ok {
		return fmt.Errorf("provider not registered: %s", providerType)
	}
	r.default_ = providerType
	return nil
}

// Available returns all registered provider types
func (r *Registry) Available() []ProviderType {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]ProviderType, 0, len(r.providers))
	for t := range r.providers {
		types = append(types, t)
	}
	return types
}

// Has checks if a provider type is registered
func (r *Registry) Has(providerType ProviderType) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.providers[providerType]
	return ok
}

// GetForModel returns the first provider that supports the given model
func (r *Registry) GetForModel(model string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, provider := range r.providers {
		if provider.SupportsModel(model) {
			return provider, nil
		}
	}
	return nil, fmt.Errorf("no provider supports model: %s", model)
}

// Close closes all registered providers
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var errs []error
	for name, provider := range r.providers {
		if err := provider.Close(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing providers: %v", errs)
	}
	return nil
}

// RegistryBuilder provides a fluent interface for building a registry
type RegistryBuilder struct {
	registry *Registry
	ctx      context.Context
	errors   []error
}

// NewRegistryBuilder creates a new builder
func NewRegistryBuilder(ctx context.Context) *RegistryBuilder {
	return &RegistryBuilder{
		registry: NewRegistry(),
		ctx:      ctx,
	}
}

// WithAnthropic adds an Anthropic provider
func (b *RegistryBuilder) WithAnthropic(config AnthropicConfig) *RegistryBuilder {
	if err := b.registry.RegisterAnthropic(config); err != nil {
		b.errors = append(b.errors, fmt.Errorf("anthropic: %w", err))
	}
	return b
}

// WithOpenAI adds an OpenAI provider
func (b *RegistryBuilder) WithOpenAI(config OpenAIConfig) *RegistryBuilder {
	if err := b.registry.RegisterOpenAI(config); err != nil {
		b.errors = append(b.errors, fmt.Errorf("openai: %w", err))
	}
	return b
}

// WithGoogle adds a Google provider
func (b *RegistryBuilder) WithGoogle(config GoogleConfig) *RegistryBuilder {
	if err := b.registry.RegisterGoogle(b.ctx, config); err != nil {
		b.errors = append(b.errors, fmt.Errorf("google: %w", err))
	}
	return b
}

// WithDefault sets the default provider
func (b *RegistryBuilder) WithDefault(providerType ProviderType) *RegistryBuilder {
	if err := b.registry.SetDefault(providerType); err != nil {
		b.errors = append(b.errors, fmt.Errorf("default: %w", err))
	}
	return b
}

// Build returns the configured registry
func (b *RegistryBuilder) Build() (*Registry, error) {
	if len(b.errors) > 0 {
		return nil, fmt.Errorf("registry build errors: %v", b.errors)
	}
	return b.registry, nil
}

// MustBuild returns the registry or panics on error
func (b *RegistryBuilder) MustBuild() *Registry {
	registry, err := b.Build()
	if err != nil {
		panic(err)
	}
	return registry
}
