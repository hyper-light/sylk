// Package context provides CV.5 VirtualContextManager for the Context Virtualization System.
// VirtualContextManager is the central coordinator for context virtualization, routing
// knowledge agents to eviction and pipeline/standalone agents to handoff.
package context

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// AgentCategory defines how an agent handles context pressure.
type AgentCategory string

const (
	// AgentCategoryKnowledge agents use tiered eviction (Librarian, Archivalist, Academic, Architect).
	AgentCategoryKnowledge AgentCategory = "knowledge"
	// AgentCategoryPipeline agents use same-type handoff (Engineer, Designer, Inspector, Tester).
	AgentCategoryPipeline AgentCategory = "pipeline"
	// AgentCategoryStandalone agents use same-type handoff (Guide, Orchestrator).
	AgentCategoryStandalone AgentCategory = "standalone"
)

// agentCategoryMap maps agent types to their categories.
var agentCategoryMap = map[string]AgentCategory{
	"librarian":    AgentCategoryKnowledge,
	"archivalist":  AgentCategoryKnowledge,
	"academic":     AgentCategoryKnowledge,
	"architect":    AgentCategoryKnowledge,
	"engineer":     AgentCategoryPipeline,
	"designer":     AgentCategoryPipeline,
	"inspector":    AgentCategoryPipeline,
	"tester":       AgentCategoryPipeline,
	"guide":        AgentCategoryStandalone,
	"orchestrator": AgentCategoryStandalone,
}

// VirtualContextManagerConfig holds configuration for VirtualContextManager.
type VirtualContextManagerConfig struct {
	ContentStore   *UniversalContentStore
	ConfigRegistry *AgentConfigRegistry
	RefGenerator   *ReferenceGenerator
}

// VirtualContextManager coordinates context virtualization across all agents.
type VirtualContextManager struct {
	contentStore   *UniversalContentStore
	configRegistry *AgentConfigRegistry
	refGenerator   *ReferenceGenerator
	agentContexts  map[string]*AgentContextState
	metrics        *EvictionMetrics
	mu             sync.RWMutex
}

// AgentContextState tracks the context state for a single agent.
type AgentContextState struct {
	AgentID    string
	AgentType  string
	SessionID  string
	Category   AgentCategory
	TokenCount int
	MaxTokens  int
	TurnNumber int
	LastAccess time.Time
	EntryIDs   []string
}

// EvictionRequest represents a request to evict content from an agent's context.
type EvictionRequest struct {
	AgentID       string
	TargetPercent float64
	Reason        string
}

// EvictionResponse contains the result of an eviction operation.
type EvictionResponse struct {
	AgentID           string
	TokensFreed       int
	EntriesEvicted    int
	ReferencesCreated int
	Duration          time.Duration
}

// HandoffTrigger contains information for triggering an agent handoff.
type HandoffTrigger struct {
	AgentID       string
	AgentType     string
	SessionID     string
	Category      AgentCategory
	ContextUsage  float64
	TriggerReason string
}

var (
	ErrAgentNotFound     = errors.New("agent not found")
	ErrInvalidPercent    = errors.New("target percent must be between 0 and 1")
	ErrNilConfigRegistry = errors.New("config registry cannot be nil")
)

// NewVirtualContextManager creates a new VirtualContextManager.
func NewVirtualContextManager(cfg VirtualContextManagerConfig) (*VirtualContextManager, error) {
	if err := validateVCMConfig(cfg); err != nil {
		return nil, err
	}

	return &VirtualContextManager{
		contentStore:   cfg.ContentStore,
		configRegistry: cfg.ConfigRegistry,
		refGenerator:   cfg.RefGenerator,
		agentContexts:  make(map[string]*AgentContextState),
		metrics:        NewEvictionMetrics(),
	}, nil
}

func validateVCMConfig(cfg VirtualContextManagerConfig) error {
	if cfg.ContentStore == nil {
		return ErrNilContentStore
	}
	if cfg.ConfigRegistry == nil {
		return ErrNilConfigRegistry
	}
	return nil
}

// RegisterAgent registers an agent with the context manager.
func (m *VirtualContextManager) RegisterAgent(
	agentID, agentType, sessionID string,
	maxTokens int,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	category := getAgentCategory(agentType)
	m.agentContexts[agentID] = &AgentContextState{
		AgentID:    agentID,
		AgentType:  agentType,
		SessionID:  sessionID,
		Category:   category,
		MaxTokens:  maxTokens,
		LastAccess: time.Now(),
		EntryIDs:   make([]string, 0),
	}

	return nil
}

func getAgentCategory(agentType string) AgentCategory {
	if cat, ok := agentCategoryMap[agentType]; ok {
		return cat
	}
	return AgentCategoryStandalone
}

// UnregisterAgent removes an agent from the context manager.
func (m *VirtualContextManager) UnregisterAgent(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.agentContexts, agentID)
}

// TrackContent adds content to an agent's tracked context.
func (m *VirtualContextManager) TrackContent(agentID string, entry *ContentEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.agentContexts[agentID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrAgentNotFound, agentID)
	}

	state.TokenCount += entry.TokenCount
	state.EntryIDs = append(state.EntryIDs, entry.ID)
	state.TurnNumber = entry.TurnNumber
	state.LastAccess = time.Now()

	return nil
}

// GetContextState returns the context state for an agent.
func (m *VirtualContextManager) GetContextState(agentID string) (*AgentContextState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.agentContexts[agentID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAgentNotFound, agentID)
	}

	return cloneAgentContextState(state), nil
}

func cloneAgentContextState(state *AgentContextState) *AgentContextState {
	clone := *state
	clone.EntryIDs = make([]string, len(state.EntryIDs))
	copy(clone.EntryIDs, state.EntryIDs)
	return &clone
}

// ShouldEvict determines if an agent should trigger eviction.
func (m *VirtualContextManager) ShouldEvict(agentID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.agentContexts[agentID]
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrAgentNotFound, agentID)
	}

	config := m.configRegistry.GetConfig(state.AgentType)
	if config == nil {
		return false, nil
	}

	return config.ShouldEvict(state.TokenCount), nil
}

// GetPressureAction determines the appropriate action for an agent under pressure.
func (m *VirtualContextManager) GetPressureAction(agentID string) (AgentCategory, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.agentContexts[agentID]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrAgentNotFound, agentID)
	}

	return state.Category, nil
}

// ForceEvict triggers immediate eviction for an agent under memory pressure.
func (m *VirtualContextManager) ForceEvict(
	ctx context.Context,
	agentID string,
	targetPercent float64,
) (*EvictionResponse, error) {
	if err := validateTargetPercent(targetPercent); err != nil {
		return nil, err
	}

	state, err := m.getStateForEviction(agentID)
	if err != nil {
		return nil, err
	}

	if state.Category != AgentCategoryKnowledge {
		return nil, fmt.Errorf("agent %s is not a knowledge agent, use handoff instead", agentID)
	}

	return m.executeEviction(ctx, state, targetPercent)
}

func validateTargetPercent(percent float64) error {
	if percent <= 0 || percent > 1 {
		return ErrInvalidPercent
	}
	return nil
}

func (m *VirtualContextManager) getStateForEviction(agentID string) (*AgentContextState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.agentContexts[agentID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAgentNotFound, agentID)
	}

	return cloneAgentContextState(state), nil
}

func (m *VirtualContextManager) executeEviction(
	ctx context.Context,
	state *AgentContextState,
	targetPercent float64,
) (*EvictionResponse, error) {
	startTime := time.Now()

	targetTokens := int(float64(state.TokenCount) * targetPercent)
	entries, err := m.loadEntriesForEviction(state)
	if err != nil {
		return nil, fmt.Errorf("load entries: %w", err)
	}

	selected, err := m.selectForEviction(ctx, state, entries, targetTokens)
	if err != nil {
		return nil, fmt.Errorf("select for eviction: %w", err)
	}

	refsCreated, err := m.createReferencesAndRemove(state, selected)
	if err != nil {
		return nil, fmt.Errorf("create references: %w", err)
	}

	tokensFreed := CalculateTotalTokens(selected)
	m.updateStateAfterEviction(state.AgentID, selected, tokensFreed)

	return &EvictionResponse{
		AgentID:           state.AgentID,
		TokensFreed:       tokensFreed,
		EntriesEvicted:    len(selected),
		ReferencesCreated: refsCreated,
		Duration:          time.Since(startTime),
	}, nil
}

func (m *VirtualContextManager) loadEntriesForEviction(state *AgentContextState) ([]*ContentEntry, error) {
	if len(state.EntryIDs) == 0 {
		return nil, nil
	}
	return m.contentStore.GetByIDs(state.EntryIDs)
}

func (m *VirtualContextManager) selectForEviction(
	ctx context.Context,
	state *AgentContextState,
	entries []*ContentEntry,
	targetTokens int,
) ([]*ContentEntry, error) {
	config := m.configRegistry.GetConfig(state.AgentType)
	if config == nil {
		return SortEntriesByAge(entries), nil
	}

	evictable := ExcludeByContentTypes(entries, config.PreserveTypes)
	if len(config.Strategies) == 0 {
		return SelectUntilTokenLimit(SortEntriesByAge(evictable), targetTokens), nil
	}

	return m.applyStrategies(ctx, config.Strategies, evictable, targetTokens)
}

func (m *VirtualContextManager) applyStrategies(
	ctx context.Context,
	strategies []EvictionStrategy,
	entries []*ContentEntry,
	targetTokens int,
) ([]*ContentEntry, error) {
	var selected []*ContentEntry
	remaining := entries
	tokensSelected := 0

	for _, strategy := range strategies {
		if tokensSelected >= targetTokens {
			break
		}

		needed := targetTokens - tokensSelected
		result, err := strategy.SelectForEviction(ctx, remaining, needed)
		if err != nil {
			return selected, err
		}

		selected = append(selected, result...)
		tokensSelected += CalculateTotalTokens(result)
		remaining = removeSelectedEntries(remaining, result)
	}

	return selected, nil
}

func removeSelectedEntries(remaining, selected []*ContentEntry) []*ContentEntry {
	if len(selected) == 0 {
		return remaining
	}

	selectedSet := make(map[string]struct{}, len(selected))
	for _, e := range selected {
		selectedSet[e.ID] = struct{}{}
	}

	result := make([]*ContentEntry, 0, len(remaining)-len(selected))
	for _, e := range remaining {
		if _, ok := selectedSet[e.ID]; !ok {
			result = append(result, e)
		}
	}
	return result
}

func (m *VirtualContextManager) createReferencesAndRemove(
	state *AgentContextState,
	entries []*ContentEntry,
) (int, error) {
	if len(entries) == 0 || m.refGenerator == nil {
		return 0, nil
	}

	ref, err := m.refGenerator.GenerateReference(entries)
	if err != nil {
		return 0, err
	}

	ref.SessionID = state.SessionID
	ref.AgentID = state.AgentID

	if err := m.contentStore.StoreReference(ref); err != nil {
		return 0, err
	}

	return 1, nil
}

func (m *VirtualContextManager) updateStateAfterEviction(
	agentID string,
	evicted []*ContentEntry,
	tokensFreed int,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.agentContexts[agentID]
	if !ok {
		return
	}

	state.TokenCount -= tokensFreed
	state.EntryIDs = filterOutEvictedIDs(state.EntryIDs, evicted)
}

func filterOutEvictedIDs(ids []string, evicted []*ContentEntry) []string {
	evictedSet := make(map[string]struct{}, len(evicted))
	for _, e := range evicted {
		evictedSet[e.ID] = struct{}{}
	}

	result := make([]string, 0, len(ids)-len(evicted))
	for _, id := range ids {
		if _, ok := evictedSet[id]; !ok {
			result = append(result, id)
		}
	}
	return result
}

// GetHandoffTrigger returns a HandoffTrigger for non-knowledge agents.
func (m *VirtualContextManager) GetHandoffTrigger(agentID string) (*HandoffTrigger, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.agentContexts[agentID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAgentNotFound, agentID)
	}

	if state.Category == AgentCategoryKnowledge {
		return nil, fmt.Errorf("agent %s is a knowledge agent, use eviction instead", agentID)
	}

	contextUsage := calculateContextUsage(state)

	return &HandoffTrigger{
		AgentID:       state.AgentID,
		AgentType:     state.AgentType,
		SessionID:     state.SessionID,
		Category:      state.Category,
		ContextUsage:  contextUsage,
		TriggerReason: "context_threshold",
	}, nil
}

func calculateContextUsage(state *AgentContextState) float64 {
	if state.MaxTokens == 0 {
		return 0
	}
	return float64(state.TokenCount) / float64(state.MaxTokens)
}

// GetTokenCount returns the current token count for an agent.
func (m *VirtualContextManager) GetTokenCount(agentID string) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.agentContexts[agentID]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrAgentNotFound, agentID)
	}

	return state.TokenCount, nil
}

// GetContextUsage returns the context usage percentage for an agent.
func (m *VirtualContextManager) GetContextUsage(agentID string) (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.agentContexts[agentID]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrAgentNotFound, agentID)
	}

	return calculateContextUsage(state), nil
}

// ListAgents returns all registered agent IDs.
func (m *VirtualContextManager) ListAgents() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.agentContexts))
	for id := range m.agentContexts {
		ids = append(ids, id)
	}
	return ids
}

// GetMetrics returns the eviction metrics.
func (m *VirtualContextManager) GetMetrics() *EvictionMetrics {
	return m.metrics
}
