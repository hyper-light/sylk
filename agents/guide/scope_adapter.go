package guide

import (
	"context"
	"time"

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

func adaptScope(scope *concurrency.GoroutineScope) *goroutineScopeAdapter {
	if scope == nil {
		return nil
	}
	return &goroutineScopeAdapter{scope: scope}
}

func (a *goroutineScopeAdapter) Go(description string, timeout time.Duration, fn func(context.Context) error) error {
	return a.scope.Go(description, timeout, concurrency.WorkFunc(fn))
}
