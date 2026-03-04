package archivalist

import (
	"sync"

	"github.com/adalundhe/sylk/core/handoff"
)

// Default Beta distribution priors per agent type.
// Higher Alpha relative to Beta means higher initial trust.
var defaultAgentPriors = map[string]struct{ alpha, beta float64 }{
	"librarian":  {4, 2}, // trusted for code knowledge
	"engineer":   {3, 3}, // moderate trust
	"architect":  {3, 3}, // moderate trust
	"designer":   {2, 3}, // slightly lower — design context is less code-centric
	"inspector":  {3, 2}, // trusted for validation results
	"tester":     {3, 2}, // trusted for test results
	"guardian":   {4, 2}, // trusted for security knowledge
	"academic":   {3, 3}, // moderate trust
}

// CrossAgentWeightManager maintains per-agent Bayesian trust multipliers.
// Each agent's contributions to the knowledge base are weighted by a
// LearnedWeight (Beta distribution) that is updated based on whether
// the contributed information proved useful during retrieval.
type CrossAgentWeightManager struct {
	mu      sync.RWMutex
	weights map[string]*handoff.LearnedWeight
}

// NewCrossAgentWeightManager creates a new manager with default priors.
func NewCrossAgentWeightManager() *CrossAgentWeightManager {
	return &CrossAgentWeightManager{
		weights: make(map[string]*handoff.LearnedWeight),
	}
}

// WeighResult returns the trust multiplier for a given agent.
// Returns the mean of the agent's Beta distribution.
// Unknown agents get a neutral prior of Beta(2,2) → mean 0.5.
func (m *CrossAgentWeightManager) WeighResult(agentType string) float64 {
	m.mu.RLock()
	w, ok := m.weights[agentType]
	m.mu.RUnlock()

	if ok {
		return w.Mean()
	}

	// Initialize on first access
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if w, ok := m.weights[agentType]; ok {
		return w.Mean()
	}

	w = m.createWeight(agentType)
	m.weights[agentType] = w
	return w.Mean()
}

// RecordOutcome updates the trust multiplier for an agent based on
// whether their contributed information was useful.
func (m *CrossAgentWeightManager) RecordOutcome(agentType string, wasUseful bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	w, ok := m.weights[agentType]
	if !ok {
		w = m.createWeight(agentType)
		m.weights[agentType] = w
	}

	observation := 0.0
	if wasUseful {
		observation = 1.0
	}
	w.Update(observation, nil)
}

func (m *CrossAgentWeightManager) createWeight(agentType string) *handoff.LearnedWeight {
	priors, ok := defaultAgentPriors[agentType]
	if !ok {
		return handoff.NewLearnedWeight(2, 2) // neutral prior
	}
	return handoff.NewLearnedWeight(priors.alpha, priors.beta)
}

// Snapshot returns a copy of all current agent weights for observability.
func (m *CrossAgentWeightManager) Snapshot() map[string]float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshot := make(map[string]float64, len(m.weights))
	for agent, w := range m.weights {
		snapshot[agent] = w.Mean()
	}
	return snapshot
}
