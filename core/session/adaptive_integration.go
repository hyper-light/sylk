// Package session provides AR.13.1: Session Manager Integration - bridges adaptive
// retrieval with session lifecycle management.
package session

import (
	"context"
	"sync"

	"github.com/adalundhe/sylk/core/concurrency"
	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/resources"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// =============================================================================
// Configuration
// =============================================================================

// AdaptiveIntegrationConfig holds configuration for adaptive integration.
type AdaptiveIntegrationConfig struct {
	// DefaultHotCacheSize is the default hot cache size for new sessions.
	DefaultHotCacheSize int64

	// VectorDB is the shared VectorGraphDB instance (optional).
	VectorDB *vectorgraphdb.VectorGraphDB

	// BleveDB is the shared Bleve-integrated database (optional).
	BleveDB *vectorgraphdb.BleveIntegratedDB

	// PressureController for pressure notifications (optional).
	PressureController *resources.PressureController

	// GoroutineBudget for WAVE 4 compliance (optional).
	GoroutineBudget *concurrency.GoroutineBudget

	// SkillRegistry for skill registration (optional).
	SkillRegistry *skills.Registry

	// BleveSearcher interface for TieredSearcher (optional).
	BleveSearcher ctxpkg.TieredBleveSearcher

	// VectorSearcher interface for TieredSearcher (optional).
	VectorSearcher ctxpkg.TieredVectorSearcher

	// Embedder for vector search (optional).
	Embedder ctxpkg.TieredEmbeddingGenerator
}

// =============================================================================
// Adaptive Integration
// =============================================================================

// AdaptiveIntegration manages AdaptiveRetrieval instances per session.
// It integrates with the session manager to automatically initialize
// and cleanup adaptive retrieval for each session.
type AdaptiveIntegration struct {
	config AdaptiveIntegrationConfig

	// instances maps session ID to AdaptiveRetrieval
	mu        sync.RWMutex
	instances map[string]*ctxpkg.AdaptiveRetrieval

	// unsubscribe function for session events
	unsubscribe func()
}

// NewAdaptiveIntegration creates a new adaptive integration.
func NewAdaptiveIntegration(config AdaptiveIntegrationConfig) *AdaptiveIntegration {
	return &AdaptiveIntegration{
		config:    config,
		instances: make(map[string]*ctxpkg.AdaptiveRetrieval),
	}
}

// =============================================================================
// Integration
// =============================================================================

// Integrate subscribes to session manager events and returns an unsubscribe function.
func (ai *AdaptiveIntegration) Integrate(manager *Manager) func() {
	ai.unsubscribe = manager.Subscribe(ai.handleEvent)
	return ai.Shutdown
}

func (ai *AdaptiveIntegration) handleEvent(event *Event) {
	switch event.Type {
	case EventCreated:
		ai.handleSessionCreated(event)
	case EventClosed:
		ai.handleSessionClosed(event)
	}
}

func (ai *AdaptiveIntegration) handleSessionCreated(event *Event) {
	_ = ai.InitForSession(context.Background(), event.SessionID)
}

func (ai *AdaptiveIntegration) handleSessionClosed(event *Event) {
	_ = ai.CleanupSession(event.SessionID)
}

// =============================================================================
// Per-Session Management
// =============================================================================

// InitForSession initializes adaptive retrieval for a session.
func (ai *AdaptiveIntegration) InitForSession(ctx context.Context, sessionID string) error {
	ai.mu.Lock()

	// Check if already initialized
	if _, exists := ai.instances[sessionID]; exists {
		ai.mu.Unlock()
		return nil
	}

	ar := ai.createInstance(sessionID)
	ai.instances[sessionID] = ar
	ai.mu.Unlock()

	return ar.Start(ctx)
}

func (ai *AdaptiveIntegration) createInstance(sessionID string) *ctxpkg.AdaptiveRetrieval {
	config := ai.buildConfig(sessionID)
	deps := ai.buildDeps()
	return ctxpkg.New(config, deps)
}

func (ai *AdaptiveIntegration) buildConfig(sessionID string) *ctxpkg.AdaptiveRetrievalConfig {
	config := ctxpkg.DefaultAdaptiveRetrievalConfig()
	config.SessionID = sessionID

	if ai.config.DefaultHotCacheSize > 0 {
		config.HotCacheMaxSize = ai.config.DefaultHotCacheSize
	}

	return config
}

func (ai *AdaptiveIntegration) buildDeps() *ctxpkg.AdaptiveRetrievalDeps {
	return &ctxpkg.AdaptiveRetrievalDeps{
		VectorDB:           ai.config.VectorDB,
		BleveDB:            ai.config.BleveDB,
		PressureController: ai.config.PressureController,
		GoroutineBudget:    ai.config.GoroutineBudget,
		SkillRegistry:      ai.config.SkillRegistry,
		BleveSearcher:      ai.config.BleveSearcher,
		VectorSearcher:     ai.config.VectorSearcher,
		Embedder:           ai.config.Embedder,
	}
}

// CleanupSession cleans up adaptive retrieval for a session.
func (ai *AdaptiveIntegration) CleanupSession(sessionID string) error {
	ai.mu.Lock()
	ar, exists := ai.instances[sessionID]
	if !exists {
		ai.mu.Unlock()
		return nil
	}
	delete(ai.instances, sessionID)
	ai.mu.Unlock()

	return ar.Stop()
}

// GetForSession returns the AdaptiveRetrieval instance for a session.
// Returns nil if not initialized.
func (ai *AdaptiveIntegration) GetForSession(sessionID string) *ctxpkg.AdaptiveRetrieval {
	ai.mu.RLock()
	defer ai.mu.RUnlock()
	return ai.instances[sessionID]
}

// HasSession returns true if the session has adaptive retrieval initialized.
func (ai *AdaptiveIntegration) HasSession(sessionID string) bool {
	ai.mu.RLock()
	defer ai.mu.RUnlock()
	_, exists := ai.instances[sessionID]
	return exists
}

// SessionCount returns the number of sessions with adaptive retrieval.
func (ai *AdaptiveIntegration) SessionCount() int {
	ai.mu.RLock()
	defer ai.mu.RUnlock()
	return len(ai.instances)
}

// =============================================================================
// Lifecycle
// =============================================================================

// Shutdown cleans up all sessions and unsubscribes from events.
func (ai *AdaptiveIntegration) Shutdown() {
	if ai.unsubscribe != nil {
		ai.unsubscribe()
		ai.unsubscribe = nil
	}

	ai.mu.Lock()
	instances := ai.collectInstances()
	ai.instances = make(map[string]*ctxpkg.AdaptiveRetrieval)
	ai.mu.Unlock()

	ai.stopInstances(instances)
}

func (ai *AdaptiveIntegration) collectInstances() []*ctxpkg.AdaptiveRetrieval {
	instances := make([]*ctxpkg.AdaptiveRetrieval, 0, len(ai.instances))
	for _, ar := range ai.instances {
		instances = append(instances, ar)
	}
	return instances
}

func (ai *AdaptiveIntegration) stopInstances(instances []*ctxpkg.AdaptiveRetrieval) {
	for _, ar := range instances {
		_ = ar.Stop()
	}
}

// =============================================================================
// State Accessors
// =============================================================================

// GetState returns the adaptive state for a session.
// Returns nil if the session is not initialized.
func (ai *AdaptiveIntegration) GetState(sessionID string) *ctxpkg.AdaptiveState {
	ar := ai.GetForSession(sessionID)
	if ar == nil {
		return nil
	}
	return ar.GetAdaptiveState()
}

// GetPrefetcher returns the prefetcher for a session.
// Returns nil if the session is not initialized.
func (ai *AdaptiveIntegration) GetPrefetcher(sessionID string) *ctxpkg.SpeculativePrefetcher {
	ar := ai.GetForSession(sessionID)
	if ar == nil {
		return nil
	}
	return ar.GetPrefetcher()
}

// GetSearcher returns the searcher for a session.
// Returns nil if the session is not initialized.
func (ai *AdaptiveIntegration) GetSearcher(sessionID string) *ctxpkg.TieredSearcher {
	ar := ai.GetForSession(sessionID)
	if ar == nil {
		return nil
	}
	return ar.GetSearcher()
}

// GetHookRegistry returns the hook registry for a session.
// Returns nil if the session is not initialized.
func (ai *AdaptiveIntegration) GetHookRegistry(sessionID string) *ctxpkg.AdaptiveHookRegistry {
	ar := ai.GetForSession(sessionID)
	if ar == nil {
		return nil
	}
	return ar.GetHookRegistry()
}

// =============================================================================
// Statistics
// =============================================================================

// IntegrationStats holds statistics for the adaptive integration.
type IntegrationStats struct {
	SessionCount int
	SessionIDs   []string
}

// Stats returns statistics for the adaptive integration.
func (ai *AdaptiveIntegration) Stats() IntegrationStats {
	ai.mu.RLock()
	defer ai.mu.RUnlock()

	ids := make([]string, 0, len(ai.instances))
	for id := range ai.instances {
		ids = append(ids, id)
	}

	return IntegrationStats{
		SessionCount: len(ai.instances),
		SessionIDs:   ids,
	}
}
