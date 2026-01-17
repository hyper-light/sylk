package credentials

import (
	"slices"
	"sync"
)

type CredentialScope struct {
	AgentType   string   `yaml:"agent_type"`
	Allowed     []string `yaml:"allowed"`
	Denied      []string `yaml:"denied"`
	RequireAuth bool     `yaml:"require_auth"`
}

type ProjectCredentialOverrides struct {
	mu        sync.RWMutex
	overrides map[string]*CredentialScope
}

type ScopeManager struct {
	mu              sync.RWMutex
	defaultScopes   map[string]*CredentialScope
	projectOverride *ProjectCredentialOverrides
}

func NewScopeManager() *ScopeManager {
	sm := &ScopeManager{
		defaultScopes: make(map[string]*CredentialScope),
		projectOverride: &ProjectCredentialOverrides{
			overrides: make(map[string]*CredentialScope),
		},
	}
	sm.loadDefaultScopes()
	return sm
}

func (sm *ScopeManager) loadDefaultScopes() {
	defaults := defaultCredentialScopes()
	for _, scope := range defaults {
		sm.defaultScopes[scope.AgentType] = scope
	}
}

func defaultCredentialScopes() []*CredentialScope {
	return []*CredentialScope{
		{
			AgentType:   "librarian",
			Allowed:     []string{"openai", "anthropic", "voyage"},
			Denied:      []string{"github", "aws"},
			RequireAuth: false,
		},
		{
			AgentType:   "academic",
			Allowed:     []string{"openai", "anthropic", "google", "serpapi"},
			Denied:      []string{"github", "aws"},
			RequireAuth: false,
		},
		{
			AgentType:   "archivalist",
			Allowed:     []string{"openai", "anthropic"},
			Denied:      []string{"github", "aws"},
			RequireAuth: false,
		},
		{
			AgentType:   "engineer",
			Allowed:     []string{"openai", "anthropic", "github"},
			Denied:      []string{},
			RequireAuth: true,
		},
		{
			AgentType:   "designer",
			Allowed:     []string{"openai", "anthropic", "figma"},
			Denied:      []string{},
			RequireAuth: true,
		},
		{
			AgentType:   "inspector",
			Allowed:     []string{"openai", "anthropic"},
			Denied:      []string{"github", "aws"},
			RequireAuth: false,
		},
		{
			AgentType:   "tester",
			Allowed:     []string{"openai", "anthropic"},
			Denied:      []string{"github", "aws"},
			RequireAuth: false,
		},
		{
			AgentType:   "guide",
			Allowed:     []string{"openai", "anthropic"},
			Denied:      []string{},
			RequireAuth: false,
		},
		{
			AgentType:   "architect",
			Allowed:     []string{"openai", "anthropic"},
			Denied:      []string{},
			RequireAuth: false,
		},
		{
			AgentType:   "orchestrator",
			Allowed:     []string{},
			Denied:      []string{"*"},
			RequireAuth: false,
		},
	}
}

func (sm *ScopeManager) GetScope(agentType string) *CredentialScope {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if override := sm.getProjectOverride(agentType); override != nil {
		return sm.mergeScopes(sm.defaultScopes[agentType], override)
	}

	if scope, ok := sm.defaultScopes[agentType]; ok {
		return scope
	}

	return &CredentialScope{
		AgentType: agentType,
		Allowed:   []string{},
		Denied:    []string{"*"},
	}
}

func (sm *ScopeManager) getProjectOverride(agentType string) *CredentialScope {
	sm.projectOverride.mu.RLock()
	defer sm.projectOverride.mu.RUnlock()
	return sm.projectOverride.overrides[agentType]
}

func (sm *ScopeManager) mergeScopes(base, override *CredentialScope) *CredentialScope {
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}

	merged := &CredentialScope{
		AgentType:   base.AgentType,
		Allowed:     intersectAllowed(base.Allowed, override.Allowed),
		Denied:      unionDenied(base.Denied, override.Denied),
		RequireAuth: base.RequireAuth || override.RequireAuth,
	}

	return merged
}

func intersectAllowed(base, override []string) []string {
	if len(override) == 0 {
		return base
	}
	return filterBySet(base, toSet(override))
}

func toSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, p := range items {
		set[p] = true
	}
	return set
}

func filterBySet(items []string, set map[string]bool) []string {
	var result []string
	for _, p := range items {
		if set[p] {
			result = append(result, p)
		}
	}
	return result
}

func unionDenied(base, override []string) []string {
	return appendUnique(appendUnique(nil, base), override)
}

func appendUnique(result, items []string) []string {
	seen := toSet(result)
	for _, p := range items {
		if !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	return result
}

func (sm *ScopeManager) SetProjectOverride(scope *CredentialScope) {
	sm.projectOverride.mu.Lock()
	defer sm.projectOverride.mu.Unlock()
	sm.projectOverride.overrides[scope.AgentType] = scope
}

func (sm *ScopeManager) ClearProjectOverrides() {
	sm.projectOverride.mu.Lock()
	defer sm.projectOverride.mu.Unlock()
	sm.projectOverride.overrides = make(map[string]*CredentialScope)
}

func (sm *ScopeManager) IsAllowed(agentType, provider string) bool {
	scope := sm.GetScope(agentType)
	return isProviderAllowed(scope, provider)
}

func isProviderAllowed(scope *CredentialScope, provider string) bool {
	if isInDenyList(scope.Denied, provider) {
		return false
	}
	return isInAllowList(scope.Allowed, provider)
}

func isInDenyList(denied []string, provider string) bool {
	for _, d := range denied {
		if d == "*" || d == provider {
			return true
		}
	}
	return false
}

func isInAllowList(allowed []string, provider string) bool {
	return slices.Contains(allowed, provider)
}

func (sm *ScopeManager) RequiresAuth(agentType, provider string) bool {
	scope := sm.GetScope(agentType)
	return scope.RequireAuth
}

func (sm *ScopeManager) ListAllowedProviders(agentType string) []string {
	scope := sm.GetScope(agentType)

	var allowed []string
	for _, p := range scope.Allowed {
		if !isInDenyList(scope.Denied, p) {
			allowed = append(allowed, p)
		}
	}
	return allowed
}
