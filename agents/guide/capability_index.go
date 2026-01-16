package guide

import (
	"sort"
	"strings"
	"sync"
)

// =============================================================================
// Capability Index
// =============================================================================

// CapabilityIndex provides efficient lookup of agents by capability
type CapabilityIndex struct {
	mu sync.RWMutex

	// Intent index: intent -> []*AgentRegistration
	byIntent map[Intent][]*AgentRegistration

	// Domain index: domain -> []*AgentRegistration
	byDomain map[Domain][]*AgentRegistration

	// Tag index: tag -> []*AgentRegistration
	byTag map[string][]*AgentRegistration

	// Keyword index: keyword -> []*AgentRegistration
	byKeyword map[string][]*AgentRegistration

	// Pattern index for compiled patterns
	patterns []*patternEntry

	// All registered agents
	agents map[string]*AgentRegistration

	// Statistics
	stats CapabilityIndexStats
}

// patternEntry pairs a pattern with its owning agent
type patternEntry struct {
	agent   *AgentRegistration
	pattern *CompiledPattern
}

// CapabilityIndexStats contains index statistics
type CapabilityIndexStats struct {
	TotalAgents   int `json:"total_agents"`
	IntentCount   int `json:"intent_count"`
	DomainCount   int `json:"domain_count"`
	TagCount      int `json:"tag_count"`
	KeywordCount  int `json:"keyword_count"`
	PatternCount  int `json:"pattern_count"`
	TotalLookups  int `json:"total_lookups"`
	IntentLookups int `json:"intent_lookups"`
	DomainLookups int `json:"domain_lookups"`
	TagLookups    int `json:"tag_lookups"`
	KeywordHits   int `json:"keyword_hits"`
	PatternHits   int `json:"pattern_hits"`
}

// NewCapabilityIndex creates a new capability index
func NewCapabilityIndex() *CapabilityIndex {
	return &CapabilityIndex{
		byIntent:  make(map[Intent][]*AgentRegistration),
		byDomain:  make(map[Domain][]*AgentRegistration),
		byTag:     make(map[string][]*AgentRegistration),
		byKeyword: make(map[string][]*AgentRegistration),
		patterns:  make([]*patternEntry, 0),
		agents:    make(map[string]*AgentRegistration),
	}
}

// =============================================================================
// Indexing
// =============================================================================

// Index adds an agent to all relevant indexes
func (ci *CapabilityIndex) Index(agent *AgentRegistration) {
	ci.mu.Lock()
	defer ci.mu.Unlock()

	// Remove old entries if re-indexing
	ci.removeUnlocked(agent.ID)

	// Store agent
	ci.agents[agent.ID] = agent

	// Index by intents
	for _, intent := range agent.Capabilities.Intents {
		ci.byIntent[intent] = append(ci.byIntent[intent], agent)
	}

	// Index by domains
	for _, domain := range agent.Capabilities.Domains {
		ci.byDomain[domain] = append(ci.byDomain[domain], agent)
	}

	// Index by tags
	for _, tag := range agent.Capabilities.Tags {
		normalizedTag := strings.ToLower(tag)
		ci.byTag[normalizedTag] = append(ci.byTag[normalizedTag], agent)
	}

	// Index by keywords
	for _, keyword := range agent.Capabilities.Keywords {
		normalizedKW := strings.ToLower(keyword)
		ci.byKeyword[normalizedKW] = append(ci.byKeyword[normalizedKW], agent)
	}

	// Index patterns
	for _, pattern := range agent.Capabilities.Patterns {
		ci.patterns = append(ci.patterns, &patternEntry{
			agent:   agent,
			pattern: pattern,
		})
	}

	// Update stats
	ci.stats.TotalAgents = len(ci.agents)
	ci.stats.IntentCount = len(ci.byIntent)
	ci.stats.DomainCount = len(ci.byDomain)
	ci.stats.TagCount = len(ci.byTag)
	ci.stats.KeywordCount = len(ci.byKeyword)
	ci.stats.PatternCount = len(ci.patterns)
}

// Remove removes an agent from all indexes
func (ci *CapabilityIndex) Remove(agentID string) {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	ci.removeUnlocked(agentID)
}

// removeUnlocked removes an agent without holding the lock
func (ci *CapabilityIndex) removeUnlocked(agentID string) {
	if _, ok := ci.agents[agentID]; !ok {
		return
	}

	ci.removeIntent(agentID)
	ci.removeDomain(agentID)
	ci.removeTags(agentID)
	ci.removeKeywords(agentID)
	ci.removePatterns(agentID)
	ci.removeAgentRecord(agentID)
	ci.updateStats()
}

func (ci *CapabilityIndex) removeIntent(agentID string) {
	ci.removeIntentIndex(agentID)
}

func (ci *CapabilityIndex) removeDomain(agentID string) {
	ci.removeDomainIndex(agentID)
}

func (ci *CapabilityIndex) removeTags(agentID string) {
	ci.removeStringIndex(agentID, ci.byTag)
}

func (ci *CapabilityIndex) removeKeywords(agentID string) {
	ci.removeStringIndex(agentID, ci.byKeyword)
}

func (ci *CapabilityIndex) removeIntentIndex(agentID string) {
	for intent, agents := range ci.byIntent {
		ci.byIntent[intent] = removeAgent(agents, agentID)
		if len(ci.byIntent[intent]) == 0 {
			delete(ci.byIntent, intent)
		}
	}
}

func (ci *CapabilityIndex) removeDomainIndex(agentID string) {
	for domain, agents := range ci.byDomain {
		ci.byDomain[domain] = removeAgent(agents, agentID)
		if len(ci.byDomain[domain]) == 0 {
			delete(ci.byDomain, domain)
		}
	}
}

func (ci *CapabilityIndex) removeStringIndex(agentID string, index map[string][]*AgentRegistration) {
	for key, agents := range index {
		index[key] = removeAgent(agents, agentID)
		if len(index[key]) == 0 {
			delete(index, key)
		}
	}
}

func (ci *CapabilityIndex) removePatterns(agentID string) {
	newPatterns := make([]*patternEntry, 0, len(ci.patterns))
	for _, entry := range ci.patterns {
		if entry.agent.ID != agentID {
			newPatterns = append(newPatterns, entry)
		}
	}
	ci.patterns = newPatterns
}

func (ci *CapabilityIndex) removeAgentRecord(agentID string) {
	delete(ci.agents, agentID)
}

func (ci *CapabilityIndex) updateStats() {
	ci.stats.TotalAgents = len(ci.agents)
	ci.stats.IntentCount = len(ci.byIntent)
	ci.stats.DomainCount = len(ci.byDomain)
	ci.stats.TagCount = len(ci.byTag)
	ci.stats.KeywordCount = len(ci.byKeyword)
	ci.stats.PatternCount = len(ci.patterns)
}

// removeAgent removes an agent from a slice by ID
func removeAgent(agents []*AgentRegistration, agentID string) []*AgentRegistration {
	result := make([]*AgentRegistration, 0, len(agents))
	for _, agent := range agents {
		if agent.ID != agentID {
			result = append(result, agent)
		}
	}
	return result
}

// =============================================================================
// Lookup
// =============================================================================

// FindByIntent finds agents that support a specific intent
func (ci *CapabilityIndex) FindByIntent(intent Intent) []*AgentRegistration {
	ci.mu.Lock()
	defer ci.mu.Unlock()

	ci.stats.TotalLookups++
	ci.stats.IntentLookups++

	agents := ci.byIntent[intent]
	if len(agents) == 0 {
		// Return agents that support all intents (empty intent list)
		return ci.findUniversalAgentsLocked()
	}
	return copyAgents(agents)
}

// FindByDomain finds agents that support a specific domain
func (ci *CapabilityIndex) FindByDomain(domain Domain) []*AgentRegistration {
	ci.mu.Lock()
	defer ci.mu.Unlock()

	ci.stats.TotalLookups++
	ci.stats.DomainLookups++

	agents := ci.byDomain[domain]
	if len(agents) == 0 {
		// Return agents that support all domains (empty domain list)
		return ci.findUniversalAgentsLocked()
	}
	return copyAgents(agents)
}

// FindByTag finds agents with a specific capability tag
func (ci *CapabilityIndex) FindByTag(tag string) []*AgentRegistration {
	ci.mu.Lock()
	defer ci.mu.Unlock()

	ci.stats.TotalLookups++
	ci.stats.TagLookups++

	return copyAgents(ci.byTag[strings.ToLower(tag)])
}

// FindByKeyword finds agents that match a keyword
func (ci *CapabilityIndex) FindByKeyword(keyword string) []*AgentRegistration {
	ci.mu.Lock()
	defer ci.mu.Unlock()

	ci.stats.TotalLookups++

	normalizedKW := strings.ToLower(keyword)
	if agents, ok := ci.byKeyword[normalizedKW]; ok {
		ci.stats.KeywordHits++
		return copyAgents(agents)
	}
	return nil
}

// FindByKeywords finds agents that match any of the keywords
func (ci *CapabilityIndex) FindByKeywords(keywords []string) []*AgentRegistration {
	ci.mu.Lock()
	defer ci.mu.Unlock()

	ci.stats.TotalLookups++

	seen := make(map[string]bool)
	var result []*AgentRegistration

	for _, keyword := range keywords {
		normalizedKW := strings.ToLower(keyword)
		if agents, ok := ci.byKeyword[normalizedKW]; ok {
			ci.stats.KeywordHits++
			for _, agent := range agents {
				if !seen[agent.ID] {
					seen[agent.ID] = true
					result = append(result, agent)
				}
			}
		}
	}

	return result
}

// FindByPattern finds agents whose patterns match the input
func (ci *CapabilityIndex) FindByPattern(input string) []*AgentRegistration {
	ci.mu.Lock()
	defer ci.mu.Unlock()

	ci.stats.TotalLookups++

	seen := make(map[string]bool)
	var result []*AgentRegistration

	for _, entry := range ci.patterns {
		if entry.pattern.MatchString(input) {
			ci.stats.PatternHits++
			if !seen[entry.agent.ID] {
				seen[entry.agent.ID] = true
				result = append(result, entry.agent)
			}
		}
	}

	return result
}

// FindBestMatch finds the best agent for the given criteria
func (ci *CapabilityIndex) FindBestMatch(result *RouteResult) *AgentRegistration {
	ci.mu.Lock()
	defer ci.mu.Unlock()

	ci.stats.TotalLookups++

	candidates := ci.collectCandidates(result)
	scoredCandidates := ci.scoreCandidates(candidates, result)
	return ci.pickBestCandidate(scoredCandidates)
}

type scoredAgent struct {
	agent *AgentRegistration
	score int
}

func (ci *CapabilityIndex) collectCandidates(result *RouteResult) []*AgentRegistration {
	candidates := make([]*AgentRegistration, 0)
	candidates = ci.appendIntentCandidates(candidates, result.Intent)
	candidates = ci.appendDomainCandidates(candidates, result.Domain)
	candidates = ci.appendUniversalCandidates(candidates)
	return candidates
}

func (ci *CapabilityIndex) appendIntentCandidates(candidates []*AgentRegistration, intent Intent) []*AgentRegistration {
	if intentAgents, ok := ci.byIntent[intent]; ok {
		candidates = append(candidates, intentAgents...)
	}
	return candidates
}

func (ci *CapabilityIndex) appendDomainCandidates(candidates []*AgentRegistration, domain Domain) []*AgentRegistration {
	domainAgents, ok := ci.byDomain[domain]
	if !ok {
		return candidates
	}
	return ci.appendUniqueCandidates(candidates, domainAgents)
}

func (ci *CapabilityIndex) appendUniversalCandidates(candidates []*AgentRegistration) []*AgentRegistration {
	for _, agent := range ci.agents {
		if ci.isUniversalAgent(agent) {
			candidates = ci.appendIfMissing(candidates, agent)
		}
	}
	return candidates
}

func (ci *CapabilityIndex) appendUniqueCandidates(candidates []*AgentRegistration, additions []*AgentRegistration) []*AgentRegistration {
	for _, agent := range additions {
		candidates = ci.appendIfMissing(candidates, agent)
	}
	return candidates
}

func (ci *CapabilityIndex) appendIfMissing(candidates []*AgentRegistration, agent *AgentRegistration) []*AgentRegistration {
	if ci.hasCandidate(candidates, agent.ID) {
		return candidates
	}
	return append(candidates, agent)
}

func (ci *CapabilityIndex) hasCandidate(candidates []*AgentRegistration, agentID string) bool {
	for _, candidate := range candidates {
		if candidate.ID == agentID {
			return true
		}
	}
	return false
}

func (ci *CapabilityIndex) isUniversalAgent(agent *AgentRegistration) bool {
	return len(agent.Capabilities.Intents) == 0 && len(agent.Capabilities.Domains) == 0
}

func (ci *CapabilityIndex) scoreCandidates(candidates []*AgentRegistration, result *RouteResult) []scoredAgent {
	scored := make([]scoredAgent, 0, len(candidates))
	for _, agent := range candidates {
		if agent.Accepts(result) {
			scored = append(scored, scoredAgent{agent: agent, score: agent.MatchScore(result)})
		}
	}
	return scored
}

func (ci *CapabilityIndex) pickBestCandidate(scoredCandidates []scoredAgent) *AgentRegistration {
	if len(scoredCandidates) == 0 {
		return nil
	}
	sort.Slice(scoredCandidates, func(i, j int) bool {
		return scoredCandidates[i].score > scoredCandidates[j].score
	})
	return scoredCandidates[0].agent
}

// FindAll finds all agents matching multiple criteria
func (ci *CapabilityIndex) FindAll(query CapabilityQuery) []*AgentRegistration {
	ci.mu.Lock()
	defer ci.mu.Unlock()

	ci.stats.TotalLookups++

	candidates := ci.initCandidates(query)
	candidates = ci.filterByDomain(candidates, query.Domain)
	candidates = ci.filterByTags(candidates, query.Tags)
	candidates = ci.filterByTemporalFocus(candidates, query.TemporalFocus)

	return ci.sortCandidates(candidates)
}

func (ci *CapabilityIndex) initCandidates(query CapabilityQuery) map[string]*AgentRegistration {
	if query.Intent == "" {
		return ci.allAgents()
	}
	return ci.intentCandidates(query.Intent)
}

func (ci *CapabilityIndex) allAgents() map[string]*AgentRegistration {
	candidates := make(map[string]*AgentRegistration, len(ci.agents))
	for id, agent := range ci.agents {
		candidates[id] = agent
	}
	return candidates
}

func (ci *CapabilityIndex) intentCandidates(intent Intent) map[string]*AgentRegistration {
	candidates := make(map[string]*AgentRegistration)
	agents, ok := ci.byIntent[intent]
	if ok {
		ci.addAgents(candidates, agents)
		return candidates
	}
	ci.addUniversalIntentCandidates(candidates)
	return candidates
}

func (ci *CapabilityIndex) addAgents(candidates map[string]*AgentRegistration, agents []*AgentRegistration) {
	for _, agent := range agents {
		candidates[agent.ID] = agent
	}
}

func (ci *CapabilityIndex) addUniversalIntentCandidates(candidates map[string]*AgentRegistration) {
	for id, agent := range ci.agents {
		if len(agent.Capabilities.Intents) == 0 {
			candidates[id] = agent
		}
	}
}

func (ci *CapabilityIndex) filterByDomain(candidates map[string]*AgentRegistration, domain Domain) map[string]*AgentRegistration {
	if domain == "" {
		return candidates
	}
	filtered := make(map[string]*AgentRegistration)
	for id, agent := range candidates {
		if agent.Capabilities.SupportsDomain(domain) {
			filtered[id] = agent
		}
	}
	return filtered
}

func (ci *CapabilityIndex) filterByTags(candidates map[string]*AgentRegistration, tags []string) map[string]*AgentRegistration {
	if len(tags) == 0 {
		return candidates
	}
	filtered := make(map[string]*AgentRegistration)
	for id, agent := range candidates {
		if ci.matchesAnyTag(agent, tags) {
			filtered[id] = agent
		}
	}
	return filtered
}

func (ci *CapabilityIndex) matchesAnyTag(agent *AgentRegistration, tags []string) bool {
	for _, requiredTag := range tags {
		for _, agentTag := range agent.Capabilities.Tags {
			if strings.EqualFold(agentTag, requiredTag) {
				return true
			}
		}
	}
	return false
}

func (ci *CapabilityIndex) filterByTemporalFocus(candidates map[string]*AgentRegistration, focus TemporalFocus) map[string]*AgentRegistration {
	if focus == "" {
		return candidates
	}
	filtered := make(map[string]*AgentRegistration)
	for id, agent := range candidates {
		if ci.matchesTemporalFocus(agent, focus) {
			filtered[id] = agent
		}
	}
	return filtered
}

func (ci *CapabilityIndex) matchesTemporalFocus(agent *AgentRegistration, focus TemporalFocus) bool {
	if agent.Constraints.TemporalFocus == "" {
		return true
	}
	return agent.Constraints.TemporalFocus == focus
}

func (ci *CapabilityIndex) sortCandidates(candidates map[string]*AgentRegistration) []*AgentRegistration {
	result := make([]*AgentRegistration, 0, len(candidates))
	for _, agent := range candidates {
		result = append(result, agent)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Priority > result[j].Priority
	})

	return result
}

// CapabilityQuery defines search criteria for agents
type CapabilityQuery struct {
	Intent        Intent        `json:"intent,omitempty"`
	Domain        Domain        `json:"domain,omitempty"`
	Tags          []string      `json:"tags,omitempty"`
	TemporalFocus TemporalFocus `json:"temporal_focus,omitempty"`
}

// findUniversalAgentsLocked returns agents that support all intents/domains (must hold lock)
func (ci *CapabilityIndex) findUniversalAgentsLocked() []*AgentRegistration {
	var result []*AgentRegistration
	for _, agent := range ci.agents {
		if len(agent.Capabilities.Intents) == 0 {
			result = append(result, agent)
		}
	}
	return result
}

// copyAgents creates a copy of the agent slice
func copyAgents(agents []*AgentRegistration) []*AgentRegistration {
	if len(agents) == 0 {
		return nil
	}
	result := make([]*AgentRegistration, len(agents))
	copy(result, agents)
	return result
}

// =============================================================================
// Statistics
// =============================================================================

// Stats returns index statistics
func (ci *CapabilityIndex) Stats() CapabilityIndexStats {
	ci.mu.RLock()
	defer ci.mu.RUnlock()
	return ci.stats
}

// GetAll returns all indexed agents
func (ci *CapabilityIndex) GetAll() []*AgentRegistration {
	ci.mu.RLock()
	defer ci.mu.RUnlock()

	result := make([]*AgentRegistration, 0, len(ci.agents))
	for _, agent := range ci.agents {
		result = append(result, agent)
	}
	return result
}

// Count returns the number of indexed agents
func (ci *CapabilityIndex) Count() int {
	ci.mu.RLock()
	defer ci.mu.RUnlock()
	return len(ci.agents)
}
