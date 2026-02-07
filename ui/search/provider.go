package search

import (
	"context"
	"sort"
	"sync"
)

// ---------------------------------------------------------------------------
// Result
// ---------------------------------------------------------------------------

// Result represents a single searchable item returned by a Provider.
type Result struct {
	Title       string
	Description string
	Category    string
	Score       int
	Action      func()
}

// ---------------------------------------------------------------------------
// Provider interface
// ---------------------------------------------------------------------------

// Provider defines the contract for a search result source.
// Implementations must be safe for concurrent use.
type Provider interface {
	// Search returns up to limit results matching query.
	// Implementations must respect context cancellation.
	Search(ctx context.Context, query string, limit int) ([]Result, error)

	// Category returns the human-readable category label for this provider.
	Category() string
}

// ---------------------------------------------------------------------------
// Concrete provider stubs
// ---------------------------------------------------------------------------

// FileProvider searches project files by path.
type FileProvider struct {
	mu    sync.RWMutex
	files []string // bounded file list
}

// NewFileProvider creates a FileProvider with the given initial file list.
func NewFileProvider(files []string) *FileProvider {
	cpy := make([]string, len(files))
	copy(cpy, files)
	return &FileProvider{files: cpy}
}

func (p *FileProvider) Category() string { return "Files" }

func (p *FileProvider) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return fuzzySearch(ctx, query, limit, p.files, p.Category(), nil)
}

// SetFiles replaces the file list atomically.
func (p *FileProvider) SetFiles(files []string) {
	cpy := make([]string, len(files))
	copy(cpy, files)
	p.mu.Lock()
	p.files = cpy
	p.mu.Unlock()
}

// AgentProvider searches available agents by name.
type AgentProvider struct {
	mu     sync.RWMutex
	agents []string
}

// NewAgentProvider creates an AgentProvider with the given agent names.
func NewAgentProvider(agents []string) *AgentProvider {
	cpy := make([]string, len(agents))
	copy(cpy, agents)
	return &AgentProvider{agents: cpy}
}

func (p *AgentProvider) Category() string { return "Agents" }

func (p *AgentProvider) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return fuzzySearch(ctx, query, limit, p.agents, p.Category(), nil)
}

// CommandProvider searches available commands.
type CommandProvider struct {
	mu       sync.RWMutex
	commands []string
	actions  map[string]func()
}

// NewCommandProvider creates a CommandProvider with the given command names and actions.
func NewCommandProvider(commands []string, actions map[string]func()) *CommandProvider {
	cpy := make([]string, len(commands))
	copy(cpy, commands)
	actCpy := make(map[string]func(), len(actions))
	for k, v := range actions {
		actCpy[k] = v
	}
	return &CommandProvider{commands: cpy, actions: actCpy}
}

func (p *CommandProvider) Category() string { return "Commands" }

func (p *CommandProvider) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return fuzzySearch(ctx, query, limit, p.commands, p.Category(), p.actions)
}

// SessionProvider searches available sessions.
type SessionProvider struct {
	mu       sync.RWMutex
	sessions []string
}

// NewSessionProvider creates a SessionProvider with the given session names.
func NewSessionProvider(sessions []string) *SessionProvider {
	cpy := make([]string, len(sessions))
	copy(cpy, sessions)
	return &SessionProvider{sessions: cpy}
}

func (p *SessionProvider) Category() string { return "Sessions" }

func (p *SessionProvider) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return fuzzySearch(ctx, query, limit, p.sessions, p.Category(), nil)
}

// KnowledgeProvider searches the knowledge base.
type KnowledgeProvider struct {
	mu    sync.RWMutex
	items []string
}

// NewKnowledgeProvider creates a KnowledgeProvider with the given knowledge item titles.
func NewKnowledgeProvider(items []string) *KnowledgeProvider {
	cpy := make([]string, len(items))
	copy(cpy, items)
	return &KnowledgeProvider{items: cpy}
}

func (p *KnowledgeProvider) Category() string { return "Knowledge" }

func (p *KnowledgeProvider) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return fuzzySearch(ctx, query, limit, p.items, p.Category(), nil)
}

// ---------------------------------------------------------------------------
// ProviderRegistry
// ---------------------------------------------------------------------------

// maxRegisteredProviders bounds the number of providers to prevent unbounded growth.
// Derived from the number of concrete provider types (files, agents, commands,
// sessions, knowledge) plus a margin for extensions.
const maxRegisteredProviders = 10

// ProviderRegistry aggregates multiple providers and merges their results.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers []Provider
}

// NewProviderRegistry creates an empty registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		providers: make([]Provider, 0, maxRegisteredProviders),
	}
}

// Register adds a provider to the registry. Returns false if the registry
// is at capacity.
func (r *ProviderRegistry) Register(p Provider) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.providers) >= maxRegisteredProviders {
		return false
	}
	r.providers = append(r.providers, p)
	return true
}

// Search queries all registered providers and merges results, returning
// up to limit results sorted by score descending.
func (r *ProviderRegistry) Search(ctx context.Context, query string, limit int) []Result {
	r.mu.RLock()
	providers := make([]Provider, len(r.providers))
	copy(providers, r.providers)
	r.mu.RUnlock()

	// Collect results from all providers concurrently, bounded by provider count.
	type providerResult struct {
		results []Result
	}

	ch := make(chan providerResult, len(providers))
	var wg sync.WaitGroup

	for _, p := range providers {
		wg.Add(1)
		go func(prov Provider) {
			defer wg.Done()
			results, err := prov.Search(ctx, query, limit)
			if err != nil {
				ch <- providerResult{}
				return
			}
			ch <- providerResult{results: results}
		}(p)
	}

	// Wait for all provider goroutines then close the channel.
	// The channel is buffered to len(providers), so all sends complete
	// before Wait returns — no untracked goroutine needed.
	wg.Wait()
	close(ch)

	var merged []Result
	for pr := range ch {
		merged = append(merged, pr.results...)
	}

	// Sort by score descending.
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

// ---------------------------------------------------------------------------
// Shared fuzzy search helper
// ---------------------------------------------------------------------------

// fuzzySearch is a generic helper that scores candidates against a query
// using the fuzzy scorer, respects context cancellation, and returns
// bounded results.
func fuzzySearch(ctx context.Context, query string, limit int, candidates []string, category string, actions map[string]func()) ([]Result, error) {
	type scored struct {
		title string
		score int
	}

	matches := make([]scored, 0, min(len(candidates), limit))

	for _, c := range candidates {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		s := Score(query, c)
		if s < 0 {
			continue
		}
		matches = append(matches, scored{title: c, score: s})
	}

	// Sort by score descending.
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].score > matches[j].score
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}

	results := make([]Result, len(matches))
	for i, m := range matches {
		var action func()
		if actions != nil {
			action = actions[m.title]
		}
		results[i] = Result{
			Title:    m.title,
			Category: category,
			Score:    m.score,
			Action:   action,
		}
	}
	return results, nil
}
