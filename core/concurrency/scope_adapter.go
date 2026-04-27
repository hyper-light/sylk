package concurrency

import (
	"context"
	"time"
)

// ScopeAdapter wraps a GoroutineScope to satisfy any interface requiring
// a Go(description, timeout, func(context.Context) error) method — in
// particular, claims.ScopeProvider. This eliminates duplicate adapter
// structs across packages.
type ScopeAdapter struct {
	Scope *GoroutineScope
}

// Go dispatches fn as a tracked goroutine under the wrapped scope.
func (a *ScopeAdapter) Go(description string, timeout time.Duration, fn func(context.Context) error) error {
	return a.Scope.Go(description, timeout, WorkFunc(fn))
}
