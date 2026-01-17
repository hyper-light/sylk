package session

import (
	"context"
	"sync"
	"time"
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
