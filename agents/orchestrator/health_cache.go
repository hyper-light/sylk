package orchestrator

import (
	"sync"
	"time"

	"github.com/dgraph-io/ristretto"
)

// maxAgents is the upper bound on concurrently monitored agents, used to
// derive Ristretto sizing. The orchestrator tracks agents registered via bus
// announcements; 32 covers all agent types with headroom.
const maxAgents = 32

// Cache key constants.
const (
	healthKeyLatest      = "health:latest"
	healthKeyAgentPrefix = "health:agent:"
)

// HealthCacheConfig configures the Ristretto-backed health cache.
type HealthCacheConfig struct {
	NumCounters int64
	MaxCost     int64
	BufferItems int64
	TTL         time.Duration
}

// DefaultHealthCacheConfig derives cache sizing from maxAgents and the
// HeartbeatInterval. No magic numbers.
func DefaultHealthCacheConfig(hcfg HealthConfig) HealthCacheConfig {
	// Total keys: 1 (latest) + maxAgents (per-agent)
	totalKeys := int64(1 + maxAgents)

	// Ristretto recommendation: NumCounters ≈ 10× expected items
	numCounters := totalKeys * 10

	// Estimated cost per entry: AgentCheckResult is ~180 bytes,
	// HealthCheckResult with maxAgents agents is ~200 + maxAgents*180.
	// Use a conservative per-entry estimate.
	estimatedCostPerEntry := int64(200 + maxAgents*180)
	maxCost := totalKeys * estimatedCostPerEntry

	// TTL: 2× heartbeat interval so entries survive one missed cycle.
	ttl := 2 * hcfg.HeartbeatInterval

	return HealthCacheConfig{
		NumCounters: numCounters,
		MaxCost:     maxCost,
		BufferItems: 64,
		TTL:         ttl,
	}
}

// HealthCache wraps a Ristretto instance for fast health result retrieval.
// Thread-safe: Ristretto is internally concurrent-safe; the closed guard
// uses a mutex to prevent use-after-close.
type HealthCache struct {
	cache  *ristretto.Cache
	ttl    time.Duration
	mu     sync.RWMutex
	closed bool
}

// NewHealthCache creates a new HealthCache.
func NewHealthCache(cfg HealthCacheConfig) (*HealthCache, error) {
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: cfg.NumCounters,
		MaxCost:     cfg.MaxCost,
		BufferItems: cfg.BufferItems,
	})
	if err != nil {
		return nil, err
	}

	return &HealthCache{
		cache: cache,
		ttl:   cfg.TTL,
	}, nil
}

// SetLatest stores the most recent full health check result.
func (hc *HealthCache) SetLatest(result *HealthCheckResult) {
	hc.mu.RLock()
	if hc.closed {
		hc.mu.RUnlock()
		return
	}
	hc.mu.RUnlock()

	hc.cache.SetWithTTL(healthKeyLatest, result, result.Cost(), hc.ttl)
}

// SetAgent stores per-agent health check state.
func (hc *HealthCache) SetAgent(agentID string, result *AgentCheckResult) {
	hc.mu.RLock()
	if hc.closed {
		hc.mu.RUnlock()
		return
	}
	hc.mu.RUnlock()

	// Per-agent cost: fixed struct fields + agentID string length.
	cost := int64(120) + int64(len(agentID))
	hc.cache.SetWithTTL(healthKeyAgentPrefix+agentID, result, cost, hc.ttl)
}

// GetLatest retrieves the most recent health check result.
func (hc *HealthCache) GetLatest() (*HealthCheckResult, bool) {
	hc.mu.RLock()
	if hc.closed {
		hc.mu.RUnlock()
		return nil, false
	}
	hc.mu.RUnlock()

	val, found := hc.cache.Get(healthKeyLatest)
	if !found {
		return nil, false
	}
	result, ok := val.(*HealthCheckResult)
	return result, ok
}

// GetAgent retrieves per-agent health check state.
func (hc *HealthCache) GetAgent(agentID string) (*AgentCheckResult, bool) {
	hc.mu.RLock()
	if hc.closed {
		hc.mu.RUnlock()
		return nil, false
	}
	hc.mu.RUnlock()

	val, found := hc.cache.Get(healthKeyAgentPrefix + agentID)
	if !found {
		return nil, false
	}
	result, ok := val.(*AgentCheckResult)
	return result, ok
}

// Close releases cache resources. Safe to call multiple times.
func (hc *HealthCache) Close() {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	if hc.closed {
		return
	}
	hc.closed = true
	hc.cache.Close()
}
