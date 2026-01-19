package session

import (
	"context"
	"sync"
	"time"
)

// =============================================================================
// Search Context Types
// =============================================================================

// SearchEntry represents a recorded search query
type SearchEntry struct {
	Query       string
	Timestamp   time.Time
	ResultCount int
}

// RecentFileEntry represents a recently accessed file
type RecentFileEntry struct {
	Path       string
	AccessedAt time.Time
	Source     string // "search", "edit", "read"
}

// SearchPreferences holds user search preferences
type SearchPreferences struct {
	DefaultLimit   int
	PreferredTypes []string
	ExcludePaths   []string
	FuzzyDefault   bool
}

// Search context limits
const (
	maxRecentSearches = 50
	maxRecentFiles    = 100
)

// =============================================================================
// Session Context
// =============================================================================

// Context provides session-scoped state and services
type Context struct {
	mu sync.RWMutex

	// Session reference
	session *Session

	// Go context for cancellation
	ctx    context.Context
	cancel context.CancelFunc

	// Session-scoped services (set by coordinator)
	services map[string]any

	// Route cache for this session
	routeCache map[string]any

	// Pending requests for this session
	pendingRequests map[string]any

	// Search context
	recentSearches    []SearchEntry
	recentFiles       []RecentFileEntry
	searchPreferences SearchPreferences
}

// NewContext creates a new session context
func NewContext(session *Session) *Context {
	ctx, cancel := context.WithCancel(context.Background())

	return &Context{
		session:         session,
		ctx:             ctx,
		cancel:          cancel,
		services:        make(map[string]any),
		routeCache:      make(map[string]any),
		pendingRequests: make(map[string]any),
		recentSearches:  make([]SearchEntry, 0),
		recentFiles:     make([]RecentFileEntry, 0),
		searchPreferences: SearchPreferences{
			DefaultLimit: 10,
			FuzzyDefault: false,
		},
	}
}

// =============================================================================
// Session Access
// =============================================================================

// Session returns the session
func (c *Context) Session() *Session {
	return c.session
}

// ID returns the session ID
func (c *Context) ID() string {
	return c.session.ID()
}

// State returns the session state
func (c *Context) State() State {
	return c.session.State()
}

// =============================================================================
// Go Context
// =============================================================================

// Context returns the Go context
func (c *Context) Context() context.Context {
	return c.ctx
}

// Done returns a channel that's closed when the context is cancelled
func (c *Context) Done() <-chan struct{} {
	return c.ctx.Done()
}

// Cancel cancels the context
func (c *Context) Cancel() {
	c.cancel()
}

// WithTimeout returns a child context with timeout
func (c *Context) WithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.ctx, d)
}

// =============================================================================
// Services
// =============================================================================

// SetService registers a service
func (c *Context) SetService(name string, service any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.services[name] = service
}

// GetService retrieves a service
func (c *Context) GetService(name string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.services[name]
	return s, ok
}

func (c *Context) MustGetService(name string) any {
	s, ok := c.GetService(name)
	if !ok {
		return nil
	}
	return s
}

// =============================================================================
// Route Cache
// =============================================================================

// CacheRoute caches a route
func (c *Context) CacheRoute(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.routeCache[key] = value
}

// GetCachedRoute retrieves a cached route
func (c *Context) GetCachedRoute(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.routeCache[key]
	return v, ok
}

// ClearRouteCache clears the route cache
func (c *Context) ClearRouteCache() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.routeCache = make(map[string]any)
}

// =============================================================================
// Pending Requests
// =============================================================================

// AddPendingRequest adds a pending request
func (c *Context) AddPendingRequest(id string, request any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingRequests[id] = request
}

// GetPendingRequest retrieves a pending request
func (c *Context) GetPendingRequest(id string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.pendingRequests[id]
	return r, ok
}

// RemovePendingRequest removes a pending request
func (c *Context) RemovePendingRequest(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pendingRequests, id)
}

// PendingRequestCount returns the number of pending requests
func (c *Context) PendingRequestCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.pendingRequests)
}

// =============================================================================
// Search Context
// =============================================================================

// AddRecentSearch adds a search to history (max 50, FIFO eviction)
func (c *Context) AddRecentSearch(query string, resultCount int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry := SearchEntry{
		Query:       query,
		Timestamp:   time.Now(),
		ResultCount: resultCount,
	}

	c.recentSearches = append(c.recentSearches, entry)
	if len(c.recentSearches) > maxRecentSearches {
		c.recentSearches = c.recentSearches[1:]
	}
}

// GetRecentSearches returns recent searches up to the specified limit
func (c *Context) GetRecentSearches(limit int) []SearchEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if limit <= 0 || limit > len(c.recentSearches) {
		limit = len(c.recentSearches)
	}

	// Return most recent first (from end of slice)
	result := make([]SearchEntry, limit)
	for i := 0; i < limit; i++ {
		result[i] = c.recentSearches[len(c.recentSearches)-1-i]
	}
	return result
}

// AddRecentFile adds a file to recent files (max 100, FIFO eviction)
func (c *Context) AddRecentFile(path, source string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry := RecentFileEntry{
		Path:       path,
		AccessedAt: time.Now(),
		Source:     source,
	}

	c.recentFiles = append(c.recentFiles, entry)
	if len(c.recentFiles) > maxRecentFiles {
		c.recentFiles = c.recentFiles[1:]
	}
}

// GetRecentFiles returns recent files up to the specified limit
func (c *Context) GetRecentFiles(limit int) []RecentFileEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if limit <= 0 || limit > len(c.recentFiles) {
		limit = len(c.recentFiles)
	}

	// Return most recent first (from end of slice)
	result := make([]RecentFileEntry, limit)
	for i := 0; i < limit; i++ {
		result[i] = c.recentFiles[len(c.recentFiles)-1-i]
	}
	return result
}

// SetSearchPreferences sets search preferences
func (c *Context) SetSearchPreferences(prefs SearchPreferences) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.searchPreferences = prefs
}

// GetSearchPreferences returns search preferences
func (c *Context) GetSearchPreferences() SearchPreferences {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.searchPreferences
}

// =============================================================================
// Metadata Shortcuts
// =============================================================================

// GetMetadata returns session metadata
func (c *Context) GetMetadata(key string) (any, bool) {
	return c.session.GetMetadata(key)
}

// SetMetadata sets session metadata
func (c *Context) SetMetadata(key string, value any) {
	c.session.SetMetadata(key, value)
}

// =============================================================================
// Serialization
// =============================================================================

// Serialize serializes the context state
func (c *Context) Serialize() ([]byte, error) {
	// Serialize the session (which contains context data)
	return c.session.Serialize()
}

// Restore restores the context state
func (c *Context) Restore(data []byte) error {
	return c.session.Restore(data)
}

// =============================================================================
// Context Key for use with context.Context
// =============================================================================

type contextKey struct{}

// SessionContextKey is the key for storing SessionContext in context.Context
var SessionContextKey = contextKey{}

// FromContext retrieves the session context from a Go context
func FromContext(ctx context.Context) (*Context, bool) {
	sc, ok := ctx.Value(SessionContextKey).(*Context)
	return sc, ok
}

// ToContext adds the session context to a Go context
func (c *Context) ToContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, SessionContextKey, c)
}
