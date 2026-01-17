package errors

import (
	"sync"
	"time"
)

const (
	ResourceLLM        = "llm"
	ResourceFile       = "file"
	ResourceNetwork    = "network"
	ResourceSubprocess = "subprocess"
)

type CircuitRegistry struct {
	breakers       sync.Map
	defaultConfigs map[string]CircuitBreakerConfig
}

func NewCircuitRegistry() *CircuitRegistry {
	return &CircuitRegistry{
		defaultConfigs: initDefaultConfigs(),
	}
}

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

func (r *CircuitRegistry) Get(resourceID string) *CircuitBreaker {
	if cb, ok := r.breakers.Load(resourceID); ok {
		return cb.(*CircuitBreaker)
	}
	return r.getOrCreate(resourceID)
}

func (r *CircuitRegistry) getOrCreate(resourceID string) *CircuitBreaker {
	config := r.resolveDefaultConfig(resourceID)
	newCB := NewCircuitBreaker(resourceID, config)

	actual, loaded := r.breakers.LoadOrStore(resourceID, newCB)
	if loaded {
		return actual.(*CircuitBreaker)
	}
	return newCB
}

func (r *CircuitRegistry) resolveDefaultConfig(resourceID string) CircuitBreakerConfig {
	resourceType := extractResourceType(resourceID)
	if config, ok := r.defaultConfigs[resourceType]; ok {
		return config
	}
	return DefaultCircuitBreakerConfig()
}

func extractResourceType(resourceID string) string {
	prefixes := []string{ResourceLLM, ResourceFile, ResourceNetwork, ResourceSubprocess}
	for _, prefix := range prefixes {
		if hasPrefix(resourceID, prefix) {
			return prefix
		}
	}
	return ""
}

func hasPrefix(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}

func (r *CircuitRegistry) GetOrCreate(resourceID string, config CircuitBreakerConfig) *CircuitBreaker {
	if cb, ok := r.breakers.Load(resourceID); ok {
		return cb.(*CircuitBreaker)
	}

	newCB := NewCircuitBreaker(resourceID, config)
	actual, loaded := r.breakers.LoadOrStore(resourceID, newCB)
	if loaded {
		return actual.(*CircuitBreaker)
	}
	return newCB
}

func (r *CircuitRegistry) AllBreakers() []*CircuitBreaker {
	var breakers []*CircuitBreaker
	r.breakers.Range(func(_, value any) bool {
		breakers = append(breakers, value.(*CircuitBreaker))
		return true
	})
	return breakers
}

func (r *CircuitRegistry) DefaultConfigs() map[string]CircuitBreakerConfig {
	configs := make(map[string]CircuitBreakerConfig, len(r.defaultConfigs))
	for k, v := range r.defaultConfigs {
		configs[k] = v
	}
	return configs
}

func (r *CircuitRegistry) Remove(resourceID string) {
	r.breakers.Delete(resourceID)
}

func (r *CircuitRegistry) Reset(resourceID string) {
	if cb, ok := r.breakers.Load(resourceID); ok {
		cb.(*CircuitBreaker).ForceReset()
	}
}

func (r *CircuitRegistry) ResetAll() {
	r.breakers.Range(func(_, value any) bool {
		value.(*CircuitBreaker).ForceReset()
		return true
	})
}

func (r *CircuitRegistry) Count() int {
	count := 0
	r.breakers.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

func (r *CircuitRegistry) OpenBreakers() []*CircuitBreaker {
	var open []*CircuitBreaker
	r.breakers.Range(func(_, value any) bool {
		cb := value.(*CircuitBreaker)
		if cb.State() == CircuitOpen {
			open = append(open, cb)
		}
		return true
	})
	return open
}
