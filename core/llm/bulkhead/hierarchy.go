// Package bulkhead provides isolation and circuit breaking for LLM operations.
package bulkhead

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// HierarchicalBulkhead manages the session->provider->model hierarchy.
type HierarchicalBulkhead struct {
	sessionID string
	configs   map[BulkheadLevel]BulkheadConfig

	session   *Bulkhead
	providers sync.Map // provider -> *Bulkhead
	models    sync.Map // "provider:model" -> *Bulkhead

	mu sync.RWMutex
}

// HierarchyStats provides statistics for all bulkheads in the hierarchy.
type HierarchyStats struct {
	SessionID string
	Session   BulkheadStats
	Providers map[string]BulkheadStats
	Models    map[string]BulkheadStats
}

// HierarchicalSlot represents acquired slots at all levels.
type HierarchicalSlot struct {
	hierarchy *HierarchicalBulkhead
	provider  string
	model     string
	session   *Bulkhead
	provBulk  *Bulkhead
	modelBulk *Bulkhead
	released  atomic.Bool
}

// NewHierarchicalBulkhead creates a complete bulkhead hierarchy for a session.
func NewHierarchicalBulkhead(sessionID string, configs map[BulkheadLevel]BulkheadConfig) *HierarchicalBulkhead {
	configs = ensureConfigs(configs)

	return &HierarchicalBulkhead{
		sessionID: sessionID,
		configs:   configs,
		session:   NewBulkhead(sessionID, LevelSession, configs[LevelSession]),
	}
}

// ensureConfigs returns default configs if nil is provided.
func ensureConfigs(configs map[BulkheadLevel]BulkheadConfig) map[BulkheadLevel]BulkheadConfig {
	if configs == nil {
		return DefaultBulkheadConfigs()
	}
	return configs
}

// Acquire acquires slots at all three levels (session, provider, model).
// Returns HierarchicalSlot on success.
// On any level failure, releases already-acquired levels.
func (hb *HierarchicalBulkhead) Acquire(ctx context.Context, provider, model string) (*HierarchicalSlot, error) {
	if err := hb.session.Acquire(ctx); err != nil {
		return nil, fmt.Errorf("session bulkhead: %w", err)
	}

	slot, err := hb.acquireProviderAndModel(ctx, provider, model)
	if err != nil {
		hb.session.Release()
		return nil, err
	}

	return slot, nil
}

// acquireProviderAndModel acquires provider and model level slots.
func (hb *HierarchicalBulkhead) acquireProviderAndModel(ctx context.Context, provider, model string) (*HierarchicalSlot, error) {
	providerBulk := hb.getOrCreateProvider(provider)
	if err := providerBulk.Acquire(ctx); err != nil {
		return nil, fmt.Errorf("provider bulkhead: %w", err)
	}

	slot, err := hb.acquireModel(ctx, provider, model, providerBulk)
	if err != nil {
		providerBulk.Release()
		return nil, err
	}

	return slot, nil
}

// acquireModel acquires the model level slot and creates the HierarchicalSlot.
func (hb *HierarchicalBulkhead) acquireModel(ctx context.Context, provider, model string, providerBulk *Bulkhead) (*HierarchicalSlot, error) {
	modelBulk := hb.getOrCreateModel(provider, model)
	if err := modelBulk.Acquire(ctx); err != nil {
		return nil, fmt.Errorf("model bulkhead: %w", err)
	}

	return hb.createSlot(provider, model, providerBulk, modelBulk), nil
}

// createSlot creates a new HierarchicalSlot with the given bulkheads.
func (hb *HierarchicalBulkhead) createSlot(provider, model string, providerBulk, modelBulk *Bulkhead) *HierarchicalSlot {
	return &HierarchicalSlot{
		hierarchy: hb,
		provider:  provider,
		model:     model,
		session:   hb.session,
		provBulk:  providerBulk,
		modelBulk: modelBulk,
	}
}

// getOrCreateProvider uses double-checked locking for lazy creation.
func (hb *HierarchicalBulkhead) getOrCreateProvider(provider string) *Bulkhead {
	if v, ok := hb.providers.Load(provider); ok {
		return v.(*Bulkhead)
	}

	return hb.createProviderBulkhead(provider)
}

// createProviderBulkhead creates a new provider bulkhead with locking.
func (hb *HierarchicalBulkhead) createProviderBulkhead(provider string) *Bulkhead {
	hb.mu.Lock()
	defer hb.mu.Unlock()

	if v, ok := hb.providers.Load(provider); ok {
		return v.(*Bulkhead)
	}

	bulk := NewBulkhead(
		hb.sessionID+":"+provider,
		LevelProvider,
		hb.configs[LevelProvider],
	)
	hb.providers.Store(provider, bulk)
	return bulk
}

// getOrCreateModel uses double-checked locking for lazy creation.
func (hb *HierarchicalBulkhead) getOrCreateModel(provider, model string) *Bulkhead {
	key := provider + ":" + model
	if v, ok := hb.models.Load(key); ok {
		return v.(*Bulkhead)
	}

	return hb.createModelBulkhead(provider, model, key)
}

// createModelBulkhead creates a new model bulkhead with locking.
func (hb *HierarchicalBulkhead) createModelBulkhead(provider, model, key string) *Bulkhead {
	hb.mu.Lock()
	defer hb.mu.Unlock()

	if v, ok := hb.models.Load(key); ok {
		return v.(*Bulkhead)
	}

	bulk := NewBulkhead(
		hb.sessionID+":"+key,
		LevelModel,
		hb.configs[LevelModel],
	)
	hb.models.Store(key, bulk)
	return bulk
}

// Stats returns HierarchyStats with all bulkhead states.
func (hb *HierarchicalBulkhead) Stats() HierarchyStats {
	stats := HierarchyStats{
		SessionID: hb.sessionID,
		Session:   hb.session.Stats(),
		Providers: make(map[string]BulkheadStats),
		Models:    make(map[string]BulkheadStats),
	}

	hb.collectProviderStats(&stats)
	hb.collectModelStats(&stats)

	return stats
}

// collectProviderStats populates provider stats in the HierarchyStats.
func (hb *HierarchicalBulkhead) collectProviderStats(stats *HierarchyStats) {
	hb.providers.Range(func(key, value any) bool {
		stats.Providers[key.(string)] = value.(*Bulkhead).Stats()
		return true
	})
}

// collectModelStats populates model stats in the HierarchyStats.
func (hb *HierarchicalBulkhead) collectModelStats(stats *HierarchyStats) {
	hb.models.Range(func(key, value any) bool {
		stats.Models[key.(string)] = value.(*Bulkhead).Stats()
		return true
	})
}

// SessionID returns the session identifier.
func (hb *HierarchicalBulkhead) SessionID() string {
	return hb.sessionID
}

// Stop gracefully shuts down all bulkheads in the hierarchy.
func (hb *HierarchicalBulkhead) Stop() {
	hb.session.Stop()
	hb.stopProviders()
	hb.stopModels()
}

// stopProviders stops all provider bulkheads.
func (hb *HierarchicalBulkhead) stopProviders() {
	hb.providers.Range(func(key, value any) bool {
		value.(*Bulkhead).Stop()
		return true
	})
}

// stopModels stops all model bulkheads.
func (hb *HierarchicalBulkhead) stopModels() {
	hb.models.Range(func(key, value any) bool {
		value.(*Bulkhead).Stop()
		return true
	})
}

// Release returns slots at all levels (model -> provider -> session).
func (hs *HierarchicalSlot) Release() {
	if hs.released.CompareAndSwap(false, true) {
		hs.modelBulk.Release()
		hs.provBulk.Release()
		hs.session.Release()
	}
}

// RecordSuccess records success at all levels.
func (hs *HierarchicalSlot) RecordSuccess() {
	hs.modelBulk.RecordSuccess()
	hs.provBulk.RecordSuccess()
	hs.session.RecordSuccess()
}

// RecordFailure records failure at all levels.
func (hs *HierarchicalSlot) RecordFailure() {
	hs.modelBulk.RecordFailure()
	hs.provBulk.RecordFailure()
	hs.session.RecordFailure()
}

// Provider returns the provider name for this slot.
func (hs *HierarchicalSlot) Provider() string {
	return hs.provider
}

// Model returns the model name for this slot.
func (hs *HierarchicalSlot) Model() string {
	return hs.model
}

// IsReleased returns true if the slot has been released.
func (hs *HierarchicalSlot) IsReleased() bool {
	return hs.released.Load()
}
