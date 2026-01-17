// Package errors implements a 5-tier error taxonomy with classification and handling behavior.
package errors

import (
	"sync"
	"time"
)

// Resource type constants for default configuration lookup.
const (
	ResourceLLM        = "llm"
	ResourceFile       = "file"
	ResourceNetwork    = "network"
	ResourceSubprocess = "subprocess"
)

// CircuitRegistry manages circuit breakers for multiple resources.
type CircuitRegistry struct {
	mu             sync.RWMutex
	breakers       map[string]*CircuitBreaker
	defaultConfigs map[string]CircuitBreakerConfig
}

// NewCircuitRegistry creates a new circuit registry with default configurations.
func NewCircuitRegistry() *CircuitRegistry {
	return &CircuitRegistry{
		breakers:       make(map[string]*CircuitBreaker),
		defaultConfigs: initDefaultConfigs(),
	}
}

// initDefaultConfigs creates the per-resource default configurations.
func initDefaultConfigs() map[string]CircuitBreakerConfig {
	return map[string]CircuitBreakerConfig{
		ResourceLLM: {
			ConsecutiveFailures:  5,
			FailureRateThreshold: 0.5,
			RateWindowSize:       20,
			CooldownDuration:     30 * time.Second,
			SuccessThreshold:     3,
			NotifyOnStateChange:  true,
		},
		ResourceFile: {
			ConsecutiveFailures:  2,
			FailureRateThreshold: 0.3,
			RateWindowSize:       10,
			CooldownDuration:     10 * time.Second,
			SuccessThreshold:     3,
			NotifyOnStateChange:  true,
		},
		ResourceNetwork: {
			ConsecutiveFailures:  3,
			FailureRateThreshold: 0.5,
			RateWindowSize:       20,
			CooldownDuration:     30 * time.Second,
			SuccessThreshold:     3,
			NotifyOnStateChange:  true,
		},
		ResourceSubprocess: {
			ConsecutiveFailures:  3,
			FailureRateThreshold: 0.4,
			RateWindowSize:       15,
			CooldownDuration:     20 * time.Second,
			SuccessThreshold:     3,
			NotifyOnStateChange:  true,
		},
	}
}

// Get retrieves or creates a circuit breaker with default config for the resource.
func (r *CircuitRegistry) Get(resourceID string) *CircuitBreaker {
	r.mu.RLock()
	cb, exists := r.breakers[resourceID]
	r.mu.RUnlock()

	if exists {
		return cb
	}

	return r.createWithDefaults(resourceID)
}

// createWithDefaults creates a new breaker using default config.
func (r *CircuitRegistry) createWithDefaults(resourceID string) *CircuitBreaker {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if cb, exists := r.breakers[resourceID]; exists {
		return cb
	}

	config := r.resolveDefaultConfig(resourceID)
	cb := NewCircuitBreaker(resourceID, config)
	r.breakers[resourceID] = cb

	return cb
}

// resolveDefaultConfig determines the default config for a resource ID.
func (r *CircuitRegistry) resolveDefaultConfig(resourceID string) CircuitBreakerConfig {
	resourceType := extractResourceType(resourceID)
	if config, ok := r.defaultConfigs[resourceType]; ok {
		return config
	}
	return DefaultCircuitBreakerConfig()
}

// extractResourceType extracts the resource type prefix from an ID.
func extractResourceType(resourceID string) string {
	prefixes := []string{ResourceLLM, ResourceFile, ResourceNetwork, ResourceSubprocess}
	for _, prefix := range prefixes {
		if hasPrefix(resourceID, prefix) {
			return prefix
		}
	}
	return ""
}

// hasPrefix checks if a string starts with a prefix.
func hasPrefix(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}

// GetOrCreate retrieves or creates a circuit breaker with custom config.
func (r *CircuitRegistry) GetOrCreate(resourceID string, config CircuitBreakerConfig) *CircuitBreaker {
	r.mu.RLock()
	cb, exists := r.breakers[resourceID]
	r.mu.RUnlock()

	if exists {
		return cb
	}

	return r.createWithConfig(resourceID, config)
}

// createWithConfig creates a new breaker with the specified config.
func (r *CircuitRegistry) createWithConfig(resourceID string, config CircuitBreakerConfig) *CircuitBreaker {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if cb, exists := r.breakers[resourceID]; exists {
		return cb
	}

	cb := NewCircuitBreaker(resourceID, config)
	r.breakers[resourceID] = cb

	return cb
}

// AllBreakers returns all registered circuit breakers.
func (r *CircuitRegistry) AllBreakers() []*CircuitBreaker {
	r.mu.RLock()
	defer r.mu.RUnlock()

	breakers := make([]*CircuitBreaker, 0, len(r.breakers))
	for _, cb := range r.breakers {
		breakers = append(breakers, cb)
	}

	return breakers
}

// DefaultConfigs returns the per-resource default configurations.
func (r *CircuitRegistry) DefaultConfigs() map[string]CircuitBreakerConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	configs := make(map[string]CircuitBreakerConfig, len(r.defaultConfigs))
	for k, v := range r.defaultConfigs {
		configs[k] = v
	}

	return configs
}

// Remove removes a circuit breaker from the registry.
func (r *CircuitRegistry) Remove(resourceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.breakers, resourceID)
}

// Reset resets a specific circuit breaker to closed state.
func (r *CircuitRegistry) Reset(resourceID string) {
	r.mu.RLock()
	cb, exists := r.breakers[resourceID]
	r.mu.RUnlock()

	if exists {
		cb.ForceReset()
	}
}

// ResetAll resets all circuit breakers to closed state.
func (r *CircuitRegistry) ResetAll() {
	r.mu.RLock()
	breakers := make([]*CircuitBreaker, 0, len(r.breakers))
	for _, cb := range r.breakers {
		breakers = append(breakers, cb)
	}
	r.mu.RUnlock()

	for _, cb := range breakers {
		cb.ForceReset()
	}
}

// Count returns the number of registered circuit breakers.
func (r *CircuitRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.breakers)
}

// OpenBreakers returns all circuit breakers currently in open state.
func (r *CircuitRegistry) OpenBreakers() []*CircuitBreaker {
	r.mu.RLock()
	defer r.mu.RUnlock()

	open := make([]*CircuitBreaker, 0)
	for _, cb := range r.breakers {
		if cb.State() == CircuitOpen {
			open = append(open, cb)
		}
	}

	return open
}
