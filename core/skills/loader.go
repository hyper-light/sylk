package skills

import (
	"sort"
	"strings"
	"sync"
)

// =============================================================================
// Progressive Loader
// =============================================================================
//
// The progressive loader manages dynamic loading/unloading of skills and
// agent context to optimize token usage. It uses:
// - Keyword detection to load relevant skills
// - Priority-based loading when token budget is constrained
// - Domain grouping for related skill loading
// - Usage-based prioritization (frequently used skills load first)

// LoaderConfig configures the progressive loader
type LoaderConfig struct {
	// MaxLoadedSkills limits concurrent loaded skills (0 = unlimited)
	MaxLoadedSkills int

	// TokenBudget is the max tokens for tool definitions (0 = unlimited)
	TokenBudget int

	// EstimatedTokensPerSkill for budget calculation
	EstimatedTokensPerSkill int

	// AutoLoadDomains are domains to always load
	AutoLoadDomains []string

	// CoreSkills are skill names to always keep loaded
	CoreSkills []string
}

// DefaultLoaderConfig returns sensible defaults
func DefaultLoaderConfig() LoaderConfig {
	return LoaderConfig{
		MaxLoadedSkills:         20,
		TokenBudget:             4000, // ~4k tokens for tool definitions
		EstimatedTokensPerSkill: 200,
		AutoLoadDomains:         []string{},
		CoreSkills:              []string{},
	}
}

// Loader manages progressive skill loading
type Loader struct {
	mu       sync.RWMutex
	registry *Registry
	config   LoaderConfig

	// Track loading decisions for debugging
	loadHistory []LoadEvent
}

// LoadEvent records a loading decision
type LoadEvent struct {
	SkillName string
	Action    string // "load", "unload"
	Reason    string
}

// NewLoader creates a progressive loader for a skill registry
func NewLoader(registry *Registry, config LoaderConfig) *Loader {
	loader := &Loader{
		registry:    registry,
		config:      config,
		loadHistory: make([]LoadEvent, 0),
	}

	// Auto-load core skills and domains
	loader.loadCoreSkills()

	return loader
}

// loadCoreSkills loads skills that should always be available
func (l *Loader) loadCoreSkills() {
	// Load core skills by name
	for _, name := range l.config.CoreSkills {
		if l.registry.Load(name) {
			l.recordEvent(name, "load", "core_skill")
		}
	}

	// Load auto-load domains
	for _, domain := range l.config.AutoLoadDomains {
		count := l.registry.LoadDomain(domain)
		if count > 0 {
			l.recordEvent(domain, "load", "auto_domain")
		}
	}
}

// LoadForInput analyzes input and loads relevant skills
func (l *Loader) LoadForInput(input string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	loaded := []string{}
	inputLower := strings.ToLower(input)

	// Find skills matching keywords in input
	allSkills := l.registry.GetAll()
	candidates := make([]*Skill, 0)

	for _, skill := range allSkills {
		if skill.Loaded {
			continue // Already loaded
		}

		// Check keywords
		for _, kw := range skill.Keywords {
			if strings.Contains(inputLower, strings.ToLower(kw)) {
				candidates = append(candidates, skill)
				break
			}
		}
	}

	// Sort by priority (higher first), then by usage
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority > candidates[j].Priority
		}
		return candidates[i].InvokeCount > candidates[j].InvokeCount
	})

	// Load within budget constraints
	currentLoaded := len(l.registry.GetLoaded())
	tokensUsed := currentLoaded * l.config.EstimatedTokensPerSkill

	for _, skill := range candidates {
		// Check max skills limit
		if l.config.MaxLoadedSkills > 0 && currentLoaded >= l.config.MaxLoadedSkills {
			break
		}

		// Check token budget
		if l.config.TokenBudget > 0 && tokensUsed+l.config.EstimatedTokensPerSkill > l.config.TokenBudget {
			break
		}

		if l.registry.Load(skill.Name) {
			loaded = append(loaded, skill.Name)
			l.recordEvent(skill.Name, "load", "keyword_match")
			currentLoaded++
			tokensUsed += l.config.EstimatedTokensPerSkill
		}
	}

	return loaded
}

// LoadDomain loads all skills in a domain if budget allows
func (l *Loader) LoadDomain(domain string) (int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	skills := l.registry.GetByDomain(domain)
	if len(skills) == 0 {
		return 0, false
	}

	// Check if we have budget
	currentLoaded := len(l.registry.GetLoaded())
	unloadedInDomain := 0
	for _, s := range skills {
		if !s.Loaded {
			unloadedInDomain++
		}
	}

	if l.config.MaxLoadedSkills > 0 && currentLoaded+unloadedInDomain > l.config.MaxLoadedSkills {
		// Need to unload some skills first
		l.unloadLeastUsed(unloadedInDomain)
	}

	count := l.registry.LoadDomain(domain)
	if count > 0 {
		l.recordEvent(domain, "load", "domain_load")
	}

	return count, true
}

// UnloadDomain unloads all skills in a domain (except core skills)
func (l *Loader) UnloadDomain(domain string) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	skills := l.registry.GetByDomain(domain)
	count := 0

	for _, skill := range skills {
		if !skill.Loaded {
			continue
		}

		// Don't unload core skills
		if l.isCoreSkill(skill.Name) {
			continue
		}

		if l.registry.Unload(skill.Name) {
			count++
			l.recordEvent(skill.Name, "unload", "domain_unload")
		}
	}

	return count
}

// UnloadUnused unloads skills that haven't been used
func (l *Loader) UnloadUnused() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	loaded := l.registry.GetLoaded()
	count := 0

	for _, skill := range loaded {
		if skill.InvokeCount > 0 {
			continue
		}

		if l.isCoreSkill(skill.Name) {
			continue
		}

		if l.registry.Unload(skill.Name) {
			count++
			l.recordEvent(skill.Name, "unload", "unused")
		}
	}

	return count
}

// unloadLeastUsed unloads n least-used skills
func (l *Loader) unloadLeastUsed(n int) int {
	loaded := l.registry.GetLoaded()

	// Sort by usage (ascending)
	sort.Slice(loaded, func(i, j int) bool {
		return loaded[i].InvokeCount < loaded[j].InvokeCount
	})

	count := 0
	for _, skill := range loaded {
		if count >= n {
			break
		}

		if l.isCoreSkill(skill.Name) {
			continue
		}

		if l.registry.Unload(skill.Name) {
			count++
			l.recordEvent(skill.Name, "unload", "least_used")
		}
	}

	return count
}

// isCoreSkill checks if a skill is in the core skills list
func (l *Loader) isCoreSkill(name string) bool {
	for _, core := range l.config.CoreSkills {
		if core == name {
			return true
		}
	}
	return false
}

// recordEvent adds a load event to history
func (l *Loader) recordEvent(name, action, reason string) {
	l.loadHistory = append(l.loadHistory, LoadEvent{
		SkillName: name,
		Action:    action,
		Reason:    reason,
	})

	// Keep history bounded
	if len(l.loadHistory) > 100 {
		l.loadHistory = l.loadHistory[50:]
	}
}

// GetLoadHistory returns recent load events
func (l *Loader) GetLoadHistory() []LoadEvent {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := make([]LoadEvent, len(l.loadHistory))
	copy(result, l.loadHistory)
	return result
}

// OptimizeForBudget ensures loaded skills fit within token budget
func (l *Loader) OptimizeForBudget() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.config.TokenBudget <= 0 {
		return 0
	}

	loaded := l.registry.GetLoaded()
	currentTokens := len(loaded) * l.config.EstimatedTokensPerSkill

	if currentTokens <= l.config.TokenBudget {
		return 0
	}

	// Need to unload some skills
	excess := currentTokens - l.config.TokenBudget
	toUnload := (excess + l.config.EstimatedTokensPerSkill - 1) / l.config.EstimatedTokensPerSkill

	return l.unloadLeastUsed(toUnload)
}

// =============================================================================
// Loader Stats
// =============================================================================

// LoaderStats contains loader statistics
type LoaderStats struct {
	LoadedSkills    int     `json:"loaded_skills"`
	TotalSkills     int     `json:"total_skills"`
	EstimatedTokens int     `json:"estimated_tokens"`
	TokenBudget     int     `json:"token_budget"`
	BudgetUsage     float64 `json:"budget_usage"`
	RecentEvents    int     `json:"recent_events"`
}

// Stats returns loader statistics
func (l *Loader) Stats() LoaderStats {
	l.mu.RLock()
	defer l.mu.RUnlock()

	loaded := len(l.registry.GetLoaded())
	total := len(l.registry.GetAll())
	tokens := loaded * l.config.EstimatedTokensPerSkill

	var usage float64
	if l.config.TokenBudget > 0 {
		usage = float64(tokens) / float64(l.config.TokenBudget) * 100
	}

	return LoaderStats{
		LoadedSkills:    loaded,
		TotalSkills:     total,
		EstimatedTokens: tokens,
		TokenBudget:     l.config.TokenBudget,
		BudgetUsage:     usage,
		RecentEvents:    len(l.loadHistory),
	}
}

// =============================================================================
// Context-Aware Loading
// =============================================================================

// LoadContext represents the current conversation context for loading decisions
type LoadContext struct {
	// Recent user inputs for keyword detection
	RecentInputs []string

	// Active domains based on conversation
	ActiveDomains []string

	// Skills recently invoked (prioritize keeping loaded)
	RecentlyInvoked []string

	// Token budget override for this context
	TokenBudget int
}

// LoadForContext performs intelligent loading based on conversation context
func (l *Loader) LoadForContext(ctx LoadContext) LoadResult {
	l.mu.Lock()
	defer l.mu.Unlock()

	result := LoadResult{
		Loaded:   []string{},
		Unloaded: []string{},
	}

	// Determine effective token budget
	budget := l.config.TokenBudget
	if ctx.TokenBudget > 0 {
		budget = ctx.TokenBudget
	}

	// Step 1: Ensure recently invoked skills stay loaded
	for _, name := range ctx.RecentlyInvoked {
		if l.registry.Load(name) {
			result.Loaded = append(result.Loaded, name)
			l.recordEvent(name, "load", "recently_invoked")
		}
	}

	// Step 2: Load active domains
	for _, domain := range ctx.ActiveDomains {
		skills := l.registry.GetByDomain(domain)
		for _, skill := range skills {
			if !skill.Loaded {
				if l.registry.Load(skill.Name) {
					result.Loaded = append(result.Loaded, skill.Name)
					l.recordEvent(skill.Name, "load", "active_domain")
				}
			}
		}
	}

	// Step 3: Analyze recent inputs for keywords
	keywordSkills := make(map[string]*Skill)
	for _, input := range ctx.RecentInputs {
		inputLower := strings.ToLower(input)
		for _, skill := range l.registry.GetAll() {
			if skill.Loaded {
				continue
			}
			for _, kw := range skill.Keywords {
				if strings.Contains(inputLower, strings.ToLower(kw)) {
					keywordSkills[skill.Name] = skill
					break
				}
			}
		}
	}

	// Load keyword-matched skills
	for name := range keywordSkills {
		if l.registry.Load(name) {
			result.Loaded = append(result.Loaded, name)
			l.recordEvent(name, "load", "context_keyword")
		}
	}

	// Step 4: Optimize to fit budget
	if budget > 0 {
		loaded := l.registry.GetLoaded()
		currentTokens := len(loaded) * l.config.EstimatedTokensPerSkill

		if currentTokens > budget {
			// Unload least important skills
			// Sort by: not recently invoked, not in active domain, lowest usage
			type skillScore struct {
				skill *Skill
				score int
			}

			scores := make([]skillScore, 0)
			for _, skill := range loaded {
				if l.isCoreSkill(skill.Name) {
					continue
				}

				score := 0

				// Recently invoked = high score (keep)
				for _, name := range ctx.RecentlyInvoked {
					if name == skill.Name {
						score += 100
						break
					}
				}

				// In active domain = medium score
				for _, domain := range ctx.ActiveDomains {
					if skill.Domain == domain {
						score += 50
						break
					}
				}

				// Usage-based score
				score += int(skill.InvokeCount)

				scores = append(scores, skillScore{skill, score})
			}

			// Sort ascending (lowest score = unload first)
			sort.Slice(scores, func(i, j int) bool {
				return scores[i].score < scores[j].score
			})

			// Unload until within budget
			for _, ss := range scores {
				if currentTokens <= budget {
					break
				}
				if l.registry.Unload(ss.skill.Name) {
					result.Unloaded = append(result.Unloaded, ss.skill.Name)
					l.recordEvent(ss.skill.Name, "unload", "budget_optimization")
					currentTokens -= l.config.EstimatedTokensPerSkill
				}
			}
		}
	}

	return result
}

// LoadResult contains the results of a loading operation
type LoadResult struct {
	Loaded   []string `json:"loaded"`
	Unloaded []string `json:"unloaded"`
}
