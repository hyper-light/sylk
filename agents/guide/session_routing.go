package guide

import (
	"context"
	"sync"
	"time"
)

// =============================================================================
// Session-Scoped Routing
// =============================================================================

// SessionRouter provides session-aware routing with per-session caches and preferences
type SessionRouter struct {
	mu sync.RWMutex

	// Per-session route caches
	sessionCaches map[string]*RouteCache

	// Per-session routing preferences
	sessionPrefs map[string]*SessionRoutingPrefs

	// Global defaults
	defaultCacheConfig RouteCacheConfig
	defaultPrefs       SessionRoutingPrefs

	// Parent guide for fallback routing
	guide *Guide

	// Statistics
	stats SessionRouterStats
}

// SessionRoutingPrefs contains per-session routing preferences
type SessionRoutingPrefs struct {
	// Preferred agents for this session (agent ID -> priority boost)
	PreferredAgents map[string]int `json:"preferred_agents,omitempty"`

	// Blocked agents for this session
	BlockedAgents map[string]bool `json:"blocked_agents,omitempty"`

	// Custom confidence thresholds (override global)
	ExecuteThreshold float64 `json:"execute_threshold,omitempty"`
	LogThreshold     float64 `json:"log_threshold,omitempty"`
	SuggestThreshold float64 `json:"suggest_threshold,omitempty"`

	// Session-specific routing rules
	Rules []SessionRoutingRule `json:"rules,omitempty"`

	// Whether to use global cache as fallback
	UseGlobalCache bool `json:"use_global_cache"`

	// Whether to populate global cache from session
	PopulateGlobalCache bool `json:"populate_global_cache"`
}

// SessionRoutingRule defines a custom routing rule for a session
type SessionRoutingRule struct {
	// Pattern to match input
	Pattern string `json:"pattern"`

	// Compiled pattern (not serialized)
	compiled *CompiledPattern

	// Target agent for matches
	TargetAgentID string `json:"target_agent_id"`

	// Priority (higher wins)
	Priority int `json:"priority"`

	// Whether rule is enabled
	Enabled bool `json:"enabled"`
}

// SessionRouterStats contains session router statistics
type SessionRouterStats struct {
	TotalSessions    int   `json:"total_sessions"`
	ActiveSessions   int   `json:"active_sessions"`
	TotalRoutes      int64 `json:"total_routes"`
	SessionHits      int64 `json:"session_hits"`
	GlobalFallbacks  int64 `json:"global_fallbacks"`
	RuleMatches      int64 `json:"rule_matches"`
	PreferenceBoosts int64 `json:"preference_boosts"`
}

// NewSessionRouter creates a new session-aware router
func NewSessionRouter(guide *Guide) *SessionRouter {
	return &SessionRouter{
		sessionCaches: make(map[string]*RouteCache),
		sessionPrefs:  make(map[string]*SessionRoutingPrefs),
		defaultCacheConfig: RouteCacheConfig{
			MaxSize: 1000,
			TTL:     30 * time.Minute,
		},
		defaultPrefs: SessionRoutingPrefs{
			UseGlobalCache:      true,
			PopulateGlobalCache: true,
		},
		guide: guide,
	}
}

// =============================================================================
// Session Management
// =============================================================================

// GetOrCreateSession gets or creates routing state for a session
func (sr *SessionRouter) GetOrCreateSession(sessionID string) *RouteCache {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if cache, ok := sr.sessionCaches[sessionID]; ok {
		return cache
	}

	cache := NewRouteCache(sr.defaultCacheConfig)
	sr.sessionCaches[sessionID] = cache
	sr.sessionPrefs[sessionID] = &SessionRoutingPrefs{
		PreferredAgents:     make(map[string]int),
		BlockedAgents:       make(map[string]bool),
		UseGlobalCache:      sr.defaultPrefs.UseGlobalCache,
		PopulateGlobalCache: sr.defaultPrefs.PopulateGlobalCache,
	}
	sr.stats.TotalSessions++
	sr.stats.ActiveSessions++

	return cache
}

// RemoveSession removes routing state for a session
func (sr *SessionRouter) RemoveSession(sessionID string) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	delete(sr.sessionCaches, sessionID)
	delete(sr.sessionPrefs, sessionID)
	sr.stats.ActiveSessions--
}

// SetSessionPrefs sets routing preferences for a session
func (sr *SessionRouter) SetSessionPrefs(sessionID string, prefs *SessionRoutingPrefs) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	sr.sessionPrefs[sessionID] = prefs
}

// GetSessionPrefs gets routing preferences for a session
func (sr *SessionRouter) GetSessionPrefs(sessionID string) *SessionRoutingPrefs {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	if prefs, ok := sr.sessionPrefs[sessionID]; ok {
		return prefs
	}
	return &sr.defaultPrefs
}

// =============================================================================
// Routing
// =============================================================================

// Route routes a request with session awareness
func (sr *SessionRouter) Route(ctx context.Context, sessionID string, request *RouteRequest) (*RouteResult, error) {
	sr.mu.RLock()
	cache, hasCache := sr.sessionCaches[sessionID]
	prefs, hasPrefs := sr.sessionPrefs[sessionID]
	sr.mu.RUnlock()

	sr.stats.TotalRoutes++

	// Try session cache first
	if hasCache {
		if cached := cache.Get(request.Input); cached != nil {
			sr.stats.SessionHits++
			return sr.cachedToResult(cached), nil
		}
	}

	// Try session-specific rules
	if hasPrefs && len(prefs.Rules) > 0 {
		if result := sr.matchRules(request.Input, prefs.Rules); result != nil {
			sr.stats.RuleMatches++
			return result, nil
		}
	}

	// Try global cache if enabled
	if hasPrefs && prefs.UseGlobalCache && sr.guide != nil {
		if cached := sr.guide.routeCache.Get(request.Input); cached != nil {
			sr.stats.GlobalFallbacks++
			result := sr.cachedToResult(cached)

			// Cache in session for next time
			if hasCache {
				cache.Set(request.Input, result)
			}

			return result, nil
		}
	}

	// Full classification via guide
	result, err := sr.guide.router.Route(ctx, request)
	if err != nil {
		return nil, err
	}

	// Apply preference boosts
	if hasPrefs {
		result = sr.applyPreferences(result, prefs)
	}

	// Cache the result
	if hasCache && result.ClassificationMethod == "llm" {
		cache.Set(request.Input, result)
	}

	// Populate global cache if enabled
	if hasPrefs && prefs.PopulateGlobalCache && sr.guide != nil && result.ClassificationMethod == "llm" {
		sr.guide.routeCache.Set(request.Input, result)
	}

	return result, nil
}

// matchRules tries to match input against session rules
func (sr *SessionRouter) matchRules(input string, rules []SessionRoutingRule) *RouteResult {
	var bestMatch *SessionRoutingRule
	bestPriority := -1

	for i := range rules {
		rule := &rules[i]
		if !rule.Enabled {
			continue
		}

		// Compile pattern if needed
		if rule.compiled == nil {
			compiled, err := NewCompiledPattern(rule.Pattern)
			if err != nil {
				continue
			}
			rule.compiled = compiled
		}

		if rule.compiled.MatchString(input) && rule.Priority > bestPriority {
			bestMatch = rule
			bestPriority = rule.Priority
		}
	}

	if bestMatch != nil {
		return &RouteResult{
			TargetAgent:          TargetAgent(bestMatch.TargetAgentID),
			Confidence:           1.0,
			ClassificationMethod: "session_rule",
		}
	}

	return nil
}

// applyPreferences applies session preferences to a route result
func (sr *SessionRouter) applyPreferences(result *RouteResult, prefs *SessionRoutingPrefs) *RouteResult {
	agentID := string(result.TargetAgent)

	// Check if agent is blocked
	if prefs.BlockedAgents[agentID] {
		result.Rejected = true
		result.Reason = "agent blocked by session preferences"
		return result
	}

	// Apply confidence thresholds
	if prefs.ExecuteThreshold > 0 && result.Confidence >= prefs.ExecuteThreshold {
		result.Action = RouteActionExecute
	} else if prefs.LogThreshold > 0 && result.Confidence >= prefs.LogThreshold {
		result.Action = RouteActionLog
	} else if prefs.SuggestThreshold > 0 && result.Confidence >= prefs.SuggestThreshold {
		result.Action = RouteActionSuggest
	}

	// Note: preference boosts would affect agent selection if we had multiple candidates
	// For now, track that we checked preferences
	if _, hasBoost := prefs.PreferredAgents[agentID]; hasBoost {
		sr.stats.PreferenceBoosts++
	}

	return result
}

// cachedToResult converts a cached route to a route result
func (sr *SessionRouter) cachedToResult(cached *CachedRoute) *RouteResult {
	return &RouteResult{
		TargetAgent:          TargetAgent(cached.TargetAgentID),
		Intent:               cached.Intent,
		Domain:               cached.Domain,
		Confidence:           cached.Confidence,
		ClassificationMethod: "session_cache",
	}
}

// =============================================================================
// Preference Management
// =============================================================================

// SetPreferredAgent sets a preferred agent for a session
func (sr *SessionRouter) SetPreferredAgent(sessionID, agentID string, priority int) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	prefs, ok := sr.sessionPrefs[sessionID]
	if !ok {
		prefs = &SessionRoutingPrefs{
			PreferredAgents: make(map[string]int),
			BlockedAgents:   make(map[string]bool),
		}
		sr.sessionPrefs[sessionID] = prefs
	}

	prefs.PreferredAgents[agentID] = priority
}

// BlockAgent blocks an agent for a session
func (sr *SessionRouter) BlockAgent(sessionID, agentID string) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	prefs, ok := sr.sessionPrefs[sessionID]
	if !ok {
		prefs = &SessionRoutingPrefs{
			PreferredAgents: make(map[string]int),
			BlockedAgents:   make(map[string]bool),
		}
		sr.sessionPrefs[sessionID] = prefs
	}

	prefs.BlockedAgents[agentID] = true
}

// UnblockAgent unblocks an agent for a session
func (sr *SessionRouter) UnblockAgent(sessionID, agentID string) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if prefs, ok := sr.sessionPrefs[sessionID]; ok {
		delete(prefs.BlockedAgents, agentID)
	}
}

// AddSessionRule adds a routing rule for a session
func (sr *SessionRouter) AddSessionRule(sessionID string, rule SessionRoutingRule) error {
	// Pre-compile the pattern
	compiled, err := NewCompiledPattern(rule.Pattern)
	if err != nil {
		return err
	}
	rule.compiled = compiled
	rule.Enabled = true

	sr.mu.Lock()
	defer sr.mu.Unlock()

	prefs, ok := sr.sessionPrefs[sessionID]
	if !ok {
		prefs = &SessionRoutingPrefs{
			PreferredAgents: make(map[string]int),
			BlockedAgents:   make(map[string]bool),
		}
		sr.sessionPrefs[sessionID] = prefs
	}

	prefs.Rules = append(prefs.Rules, rule)
	return nil
}

// =============================================================================
// Statistics
// =============================================================================

// Stats returns session router statistics
func (sr *SessionRouter) Stats() SessionRouterStats {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	return sr.stats
}

// SessionStats returns statistics for a specific session
func (sr *SessionRouter) SessionStats(sessionID string) *SessionCacheStats {
	sr.mu.RLock()
	cache, ok := sr.sessionCaches[sessionID]
	sr.mu.RUnlock()

	if !ok {
		return nil
	}

	stats := cache.Stats()
	return &SessionCacheStats{
		SessionID:  sessionID,
		CacheStats: stats,
	}
}

// SessionCacheStats contains cache statistics for a session
type SessionCacheStats struct {
	SessionID  string          `json:"session_id"`
	CacheStats RouteCacheStats `json:"cache_stats"`
}

// =============================================================================
// Cleanup
// =============================================================================

// Cleanup removes expired entries from all session caches
func (sr *SessionRouter) Cleanup() int {
	sr.mu.RLock()
	caches := make([]*RouteCache, 0, len(sr.sessionCaches))
	for _, cache := range sr.sessionCaches {
		caches = append(caches, cache)
	}
	sr.mu.RUnlock()

	total := 0
	for _, cache := range caches {
		total += cache.Cleanup()
	}
	return total
}

// CleanupInactiveSessions removes sessions that haven't been used
// Note: This is a simple implementation that removes sessions with empty caches
// A more sophisticated implementation would track last access time per session
func (sr *SessionRouter) CleanupInactiveSessions(maxAge time.Duration) int {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	removed := 0

	for sessionID, cache := range sr.sessionCaches {
		stats := cache.Stats()
		// Remove sessions with no cached routes
		if stats.Size == 0 {
			delete(sr.sessionCaches, sessionID)
			delete(sr.sessionPrefs, sessionID)
			removed++
			sr.stats.ActiveSessions--
		}
	}

	return removed
}
