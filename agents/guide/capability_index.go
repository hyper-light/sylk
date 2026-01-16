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
	agent, ok := ci.agents[agentID]
	if !ok {
		return
	}

	// Remove from intent index
	for intent, agents := range ci.byIntent {
		ci.byIntent[intent] = removeAgent(agents, agentID)
		if len(ci.byIntent[intent]) == 0 {
			delete(ci.byIntent, intent)
		}
	}

	// Remove from domain index
	for domain, agents := range ci.byDomain {
		ci.byDomain[domain] = removeAgent(agents, agentID)
		if len(ci.byDomain[domain]) == 0 {
			delete(ci.byDomain, domain)
		}
	}

	// Remove from tag index
	for tag, agents := range ci.byTag {
		ci.byTag[tag] = removeAgent(agents, agentID)
		if len(ci.byTag[tag]) == 0 {
			delete(ci.byTag, tag)
		}
	}

	// Remove from keyword index
	for keyword, agents := range ci.byKeyword {
		ci.byKeyword[keyword] = removeAgent(agents, agentID)
		if len(ci.byKeyword[keyword]) == 0 {
			delete(ci.byKeyword, keyword)
		}
	}

	// Remove patterns
	newPatterns := make([]*patternEntry, 0, len(ci.patterns))
	for _, entry := range ci.patterns {
		if entry.agent.ID != agentID {
			newPatterns = append(newPatterns, entry)
		}
	}
	ci.patterns = newPatterns

	// Remove from agents map
	delete(ci.agents, agentID)

	// Update stats
	ci.stats.TotalAgents = len(ci.agents)
	ci.stats.IntentCount = len(ci.byIntent)
	ci.stats.DomainCount = len(ci.byDomain)
	ci.stats.TagCount = len(ci.byTag)
	ci.stats.KeywordCount = len(ci.byKeyword)
	ci.stats.PatternCount = len(ci.patterns)

	_ = agent // Suppress unused warning
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

	var candidates []*AgentRegistration

	// Start with intent matches
	if intentAgents, ok := ci.byIntent[result.Intent]; ok {
		candidates = append(candidates, intentAgents...)
	}

	// Add domain matches
	if domainAgents, ok := ci.byDomain[result.Domain]; ok {
		for _, agent := range domainAgents {
			found := false
			for _, c := range candidates {
				if c.ID == agent.ID {
					found = true
					break
				}
			}
			if !found {
				candidates = append(candidates, agent)
			}
		}
	}

	// Add universal agents (those without specific intent/domain restrictions)
	for _, agent := range ci.agents {
		if len(agent.Capabilities.Intents) == 0 && len(agent.Capabilities.Domains) == 0 {
			found := false
			for _, c := range candidates {
				if c.ID == agent.ID {
					found = true
					break
				}
			}
			if !found {
				candidates = append(candidates, agent)
			}
		}
	}

	// Score and sort candidates
	type scored struct {
		agent *AgentRegistration
		score int
	}
	scoredCandidates := make([]scored, 0, len(candidates))

	for _, agent := range candidates {
		if agent.Accepts(result) {
			scoredCandidates = append(scoredCandidates, scored{
				agent: agent,
				score: agent.MatchScore(result),
			})
		}
	}

	if len(scoredCandidates) == 0 {
		return nil
	}

	// Sort by score descending
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

	// Start with all agents if no filters
	candidates := make(map[string]*AgentRegistration)

	// Filter by intent
	if query.Intent != "" {
		if agents, ok := ci.byIntent[query.Intent]; ok {
			for _, agent := range agents {
				candidates[agent.ID] = agent
			}
		} else {
			// Include universal agents
			for id, agent := range ci.agents {
				if len(agent.Capabilities.Intents) == 0 {
					candidates[id] = agent
				}
			}
		}
	} else {
		// No intent filter, start with all
		for id, agent := range ci.agents {
			candidates[id] = agent
		}
	}

	// Filter by domain
	if query.Domain != "" {
		filtered := make(map[string]*AgentRegistration)
		for id, agent := range candidates {
			if agent.Capabilities.SupportsDomain(query.Domain) {
				filtered[id] = agent
			}
		}
		candidates = filtered
	}

	// Filter by tags
	if len(query.Tags) > 0 {
		filtered := make(map[string]*AgentRegistration)
		for id, agent := range candidates {
			for _, requiredTag := range query.Tags {
				for _, agentTag := range agent.Capabilities.Tags {
					if strings.EqualFold(agentTag, requiredTag) {
						filtered[id] = agent
						break
					}
				}
			}
		}
		candidates = filtered
	}

	// Filter by temporal focus
	if query.TemporalFocus != "" {
		filtered := make(map[string]*AgentRegistration)
		for id, agent := range candidates {
			if agent.Constraints.TemporalFocus == "" || agent.Constraints.TemporalFocus == query.TemporalFocus {
				filtered[id] = agent
			}
		}
		candidates = filtered
	}

	// Convert to slice and sort by priority
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
