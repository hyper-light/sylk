package concurrency

import "context"

// scopeContextKey identifies the GoroutineScope stamped on a context by
// agents so downstream skill handlers can dispatch tracked parallel work
// via the same primitive the agent uses for its own goroutines. Using a
// private type for the key prevents collisions with other packages'
// context values.
type scopeContextKey struct{}

// WithScope returns a new context carrying the given GoroutineScope.
// Agents stamp this onto every tool-runtime invocation context so that
// skill handlers — which only see context, not the agent struct — can
// reach for the tracked-goroutine primitive when they need parallelism.
//
// Passing a nil scope returns the parent context unchanged; callers
// that want to strip an inherited scope should do so explicitly by
// stamping a fresh scope rather than relying on nil-as-delete.
func WithScope(ctx context.Context, scope *GoroutineScope) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if scope == nil {
		return ctx
	}
	return context.WithValue(ctx, scopeContextKey{}, scope)
}

// ScopeFromContext returns the GoroutineScope stamped onto ctx, or nil
// if none is present. Handlers that want to dispatch parallel work
// should check for nil and fall back to sequential execution when the
// scope is absent — tests, programmatic invokers, and not-yet-wired
// agents won't have one, and silently spawning an untracked goroutine
// would violate the "no untracked goroutines" invariant.
//
// Typical usage inside a batch skill handler:
//
//	scope := concurrency.ScopeFromContext(ctx)
//	if scope == nil {
//	    return runSequential(ctx, ops)
//	}
//	return runParallel(ctx, scope, ops)
func ScopeFromContext(ctx context.Context) *GoroutineScope {
	if ctx == nil {
		return nil
	}
	value := ctx.Value(scopeContextKey{})
	if value == nil {
		return nil
	}
	scope, _ := value.(*GoroutineScope)
	return scope
}
