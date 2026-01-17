package pool

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Fairness Controller
// =============================================================================

// FairnessController manages fair resource allocation across entities
type FairnessController struct {
	mu sync.RWMutex

	// Per-entity state
	entities map[string]*entityState

	// Global limits
	globalLimit  int64
	currentUsage int64

	// Configuration
	entityLimit   int64
	decayInterval time.Duration
	decayFactor   float64

	// Closed
	closed atomic.Bool
}

type entityState struct {
	mu           sync.Mutex
	id           string
	currentUsage int64
	totalUsage   int64
	waitTime     time.Duration
	lastActive   time.Time
	weight       float64
}

// FairnessConfig configures the fairness controller
type FairnessConfig struct {
	GlobalLimit   int64         // Maximum total concurrent operations
	EntityLimit   int64         // Maximum per-entity concurrent operations
	DecayInterval time.Duration // How often to decay usage counts
	DecayFactor   float64       // Factor to multiply usage by each decay
}

// DefaultFairnessConfig returns sensible defaults
func DefaultFairnessConfig() FairnessConfig {
	return FairnessConfig{
		GlobalLimit:   100,
		EntityLimit:   20,
		DecayInterval: 5 * time.Second,
		DecayFactor:   0.9,
	}
}

// NewFairnessController creates a new fairness controller
func NewFairnessController(cfg FairnessConfig) *FairnessController {
	cfg = normalizeFairnessConfig(cfg)

	fc := &FairnessController{
		entities:      make(map[string]*entityState),
		globalLimit:   cfg.GlobalLimit,
		entityLimit:   cfg.EntityLimit,
		decayInterval: cfg.DecayInterval,
		decayFactor:   cfg.DecayFactor,
	}

	go fc.decayWorker()
	return fc
}

func normalizeFairnessConfig(cfg FairnessConfig) FairnessConfig {
	cfg.GlobalLimit = defaultInt64IfZero(cfg.GlobalLimit, 100)
	cfg.EntityLimit = defaultInt64IfZero(cfg.EntityLimit, 20)
	cfg.DecayInterval = defaultDurationIfZero(cfg.DecayInterval, 5*time.Second)
	cfg.DecayFactor = normalizeDecayFactor(cfg.DecayFactor)
	return cfg
}

func defaultInt64IfZero(val, def int64) int64 {
	if val <= 0 {
		return def
	}
	return val
}

func defaultDurationIfZero(val, def time.Duration) time.Duration {
	if val <= 0 {
		return def
	}
	return val
}

func normalizeDecayFactor(val float64) float64 {
	if val <= 0 || val > 1 {
		return 0.9
	}
	return val
}

// =============================================================================
// Resource Acquisition
// =============================================================================

// Acquire attempts to acquire a resource slot for an entity
func (fc *FairnessController) Acquire(entityID string) (bool, error) {
	if fc.closed.Load() {
		return false, ErrControllerClosed
	}
	if !fc.hasGlobalCapacity() {
		return false, nil
	}

	entity := fc.getOrCreateEntity(entityID)
	if !fc.tryAcquireEntitySlot(entity) {
		return false, nil
	}

	atomic.AddInt64(&fc.currentUsage, 1)
	return true, nil
}

func (fc *FairnessController) hasGlobalCapacity() bool {
	return atomic.LoadInt64(&fc.currentUsage) < fc.globalLimit
}

func (fc *FairnessController) getOrCreateEntity(entityID string) *entityState {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	entity, ok := fc.entities[entityID]
	if !ok {
		entity = &entityState{id: entityID, lastActive: time.Now(), weight: 1.0}
		fc.entities[entityID] = entity
	}
	return entity
}

func (fc *FairnessController) tryAcquireEntitySlot(entity *entityState) bool {
	entity.mu.Lock()
	defer entity.mu.Unlock()

	if entity.currentUsage >= fc.entityLimit {
		return false
	}

	entity.currentUsage++
	entity.totalUsage++
	entity.lastActive = time.Now()
	return true
}

// Release releases a resource slot for an entity
func (fc *FairnessController) Release(entityID string) error {
	if fc.closed.Load() {
		return ErrControllerClosed
	}

	entity, err := fc.findEntity(entityID)
	if err != nil {
		return err
	}

	fc.releaseEntitySlot(entity)
	fc.decrementGlobalUsage()
	return nil
}

func (fc *FairnessController) findEntity(entityID string) (*entityState, error) {
	fc.mu.RLock()
	entity, ok := fc.entities[entityID]
	fc.mu.RUnlock()

	if !ok {
		return nil, ErrEntityNotFound
	}
	return entity, nil
}

func (fc *FairnessController) releaseEntitySlot(entity *entityState) {
	entity.mu.Lock()
	if entity.currentUsage > 0 {
		entity.currentUsage--
	}
	entity.mu.Unlock()
}

func (fc *FairnessController) decrementGlobalUsage() {
	if atomic.AddInt64(&fc.currentUsage, -1) < 0 {
		atomic.StoreInt64(&fc.currentUsage, 0)
	}
}

// =============================================================================
// Fair Selection
// =============================================================================

// SelectFairest returns the entity that should be prioritized
func (fc *FairnessController) SelectFairest(candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}

	fc.mu.RLock()
	defer fc.mu.RUnlock()

	return fc.findFairestCandidate(candidates)
}

func (fc *FairnessController) findFairestCandidate(candidates []string) string {
	var fairest string
	lowestScore := float64(-1)

	for _, id := range candidates {
		entity, ok := fc.entities[id]
		if !ok {
			return id // New entity gets priority
		}

		score := fc.computeEntityScore(entity)
		fairest, lowestScore = updateFairest(id, score, fairest, lowestScore)
	}

	return fairest
}

func updateFairest(id string, score float64, current string, lowest float64) (string, float64) {
	if lowest < 0 || score < lowest {
		return id, score
	}
	return current, lowest
}

func (fc *FairnessController) computeEntityScore(entity *entityState) float64 {
	entity.mu.Lock()
	defer entity.mu.Unlock()

	score := float64(entity.totalUsage) / entity.weight
	if entity.waitTime > 0 {
		score -= float64(entity.waitTime.Seconds())
	}
	return score
}

// =============================================================================
// Weight Management
// =============================================================================

// SetWeight sets the weight for an entity (higher = more resources)
func (fc *FairnessController) SetWeight(entityID string, weight float64) error {
	if weight <= 0 {
		weight = 1.0
	}

	fc.mu.Lock()
	entity, ok := fc.entities[entityID]
	if !ok {
		entity = &entityState{
			id:         entityID,
			lastActive: time.Now(),
			weight:     weight,
		}
		fc.entities[entityID] = entity
	} else {
		entity.mu.Lock()
		entity.weight = weight
		entity.mu.Unlock()
	}
	fc.mu.Unlock()

	return nil
}

// GetWeight returns the weight for an entity
func (fc *FairnessController) GetWeight(entityID string) float64 {
	fc.mu.RLock()
	entity, ok := fc.entities[entityID]
	fc.mu.RUnlock()

	if !ok {
		return 1.0
	}

	entity.mu.Lock()
	defer entity.mu.Unlock()
	return entity.weight
}

// =============================================================================
// Statistics
// =============================================================================

// EntityStats returns statistics for an entity
func (fc *FairnessController) EntityStats(entityID string) (FairnessEntityStats, bool) {
	fc.mu.RLock()
	entity, ok := fc.entities[entityID]
	fc.mu.RUnlock()

	if !ok {
		return FairnessEntityStats{}, false
	}

	entity.mu.Lock()
	defer entity.mu.Unlock()

	return FairnessEntityStats{
		ID:           entity.id,
		CurrentUsage: entity.currentUsage,
		TotalUsage:   entity.totalUsage,
		WaitTime:     entity.waitTime,
		LastActive:   entity.lastActive,
		Weight:       entity.weight,
	}, true
}

// Stats returns overall statistics
func (fc *FairnessController) Stats() FairnessStats {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	stats := FairnessStats{
		GlobalLimit:  fc.globalLimit,
		EntityLimit:  fc.entityLimit,
		CurrentUsage: atomic.LoadInt64(&fc.currentUsage),
		EntityCount:  len(fc.entities),
		EntityStats:  make(map[string]FairnessEntityStats),
	}

	for id, entity := range fc.entities {
		entity.mu.Lock()
		stats.EntityStats[id] = FairnessEntityStats{
			ID:           entity.id,
			CurrentUsage: entity.currentUsage,
			TotalUsage:   entity.totalUsage,
			WaitTime:     entity.waitTime,
			LastActive:   entity.lastActive,
			Weight:       entity.weight,
		}
		entity.mu.Unlock()
	}

	return stats
}

// FairnessStats contains fairness controller statistics
type FairnessStats struct {
	GlobalLimit  int64                          `json:"global_limit"`
	EntityLimit  int64                          `json:"entity_limit"`
	CurrentUsage int64                          `json:"current_usage"`
	EntityCount  int                            `json:"entity_count"`
	EntityStats  map[string]FairnessEntityStats `json:"entity_stats"`
}

// FairnessEntityStats contains per-entity statistics
type FairnessEntityStats struct {
	ID           string        `json:"id"`
	CurrentUsage int64         `json:"current_usage"`
	TotalUsage   int64         `json:"total_usage"`
	WaitTime     time.Duration `json:"wait_time"`
	LastActive   time.Time     `json:"last_active"`
	Weight       float64       `json:"weight"`
}

// =============================================================================
// Internal
// =============================================================================

// decayWorker periodically decays usage counts
func (fc *FairnessController) decayWorker() {
	ticker := time.NewTicker(fc.decayInterval)
	defer ticker.Stop()

	for range ticker.C {
		if fc.closed.Load() {
			return
		}

		fc.mu.RLock()
		for _, entity := range fc.entities {
			entity.mu.Lock()
			entity.totalUsage = int64(float64(entity.totalUsage) * fc.decayFactor)
			entity.mu.Unlock()
		}
		fc.mu.RUnlock()
	}
}

// Close closes the fairness controller
func (fc *FairnessController) Close() error {
	fc.closed.Store(true)
	return nil
}

// RemoveEntity removes an entity from tracking
func (fc *FairnessController) RemoveEntity(entityID string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	if entity, ok := fc.entities[entityID]; ok {
		entity.mu.Lock()
		atomic.AddInt64(&fc.currentUsage, -entity.currentUsage)
		entity.mu.Unlock()
	}

	delete(fc.entities, entityID)
}

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrControllerClosed indicates the controller is closed
	ErrControllerClosed = errors.New("fairness controller is closed")

	// ErrEntityNotFound indicates the entity was not found
	ErrEntityNotFound = errors.New("entity not found")
)
