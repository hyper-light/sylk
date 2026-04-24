package guide

import (
	"context"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
)

// goroutineScopeAdapter adapts *concurrency.GoroutineScope to the
// claims.ScopeProvider interface. The only difference is the function
// type: GoroutineScope.Go takes concurrency.WorkFunc (a named type),
// while ScopeProvider.Go takes func(context.Context) error (anonymous).
// They have the same underlying signature.
type goroutineScopeAdapter struct {
	scope *concurrency.GoroutineScope
}

// adaptScope returns a claims.ScopeProvider. When scope is nil the
// return is an untyped nil interface — callers can safely compare it
// against nil, avoiding the typed-nil-interface pitfall that would
// otherwise cause downstream ScopeProvider.Go calls to panic.
func adaptScope(scope *concurrency.GoroutineScope) claims.ScopeProvider {
	if scope == nil {
		return nil
	}
	return &goroutineScopeAdapter{scope: scope}
}

func (a *goroutineScopeAdapter) Go(description string, timeout time.Duration, fn func(context.Context) error) error {
	if a == nil || a.scope == nil {
		return nil
	}
	return a.scope.Go(description, timeout, concurrency.WorkFunc(fn))
}
