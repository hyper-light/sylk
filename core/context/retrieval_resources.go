// Package context provides lossless context virtualization types for the Sylk
// multi-agent coding application. This file defines RetrievalResources for
// managing file handle budgets during retrieval operations.
package context

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/resources"
)

// ErrResourcesClosed is returned when operations are attempted on closed resources.
var ErrResourcesClosed = errors.New("retrieval resources are closed")

// TrackedHandle represents a file handle acquired from the budget.
// It tracks metadata about when the handle was acquired and its state.
type TrackedHandle struct {
	// Name identifies this handle (e.g., "bleve-index", "wal-writer").
	Name string

	// AcquiredAt is when this handle was acquired.
	AcquiredAt time.Time

	// Released indicates if this handle has been released.
	Released bool
}

// RetrievalResources wraps AgentFileBudget for retrieval system use.
// It provides tracked file handle acquisition for WAL, Bleve, and VectorDB.
type RetrievalResources struct {
	agentBudget *resources.AgentFileBudget
	handles     []TrackedHandle
	mu          sync.Mutex
	closed      bool
}

// NewRetrievalResources creates a new RetrievalResources with the given budget.
func NewRetrievalResources(agentBudget *resources.AgentFileBudget) *RetrievalResources {
	return &RetrievalResources{
		agentBudget: agentBudget,
		handles:     make([]TrackedHandle, 0),
	}
}

// Acquire acquires a single file handle from the budget.
// Returns a release function and any error encountered.
func (r *RetrievalResources) Acquire(ctx context.Context, name string) (func(), error) {
	if err := r.checkClosed(); err != nil {
		return nil, err
	}

	if err := r.agentBudget.Acquire(ctx); err != nil {
		return nil, err
	}

	idx := r.trackHandle(name)
	return r.createReleaseFunc(idx), nil
}

// checkClosed returns an error if resources are closed.
func (r *RetrievalResources) checkClosed() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrResourcesClosed
	}
	return nil
}

// trackHandle adds a handle to tracking and returns its index.
func (r *RetrievalResources) trackHandle(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	handle := TrackedHandle{
		Name:       name,
		AcquiredAt: time.Now(),
		Released:   false,
	}
	r.handles = append(r.handles, handle)
	return len(r.handles) - 1
}

// createReleaseFunc creates a release function for the handle at idx.
func (r *RetrievalResources) createReleaseFunc(idx int) func() {
	return func() {
		r.releaseByIndex(idx)
	}
}

// releaseByIndex releases the handle at the given index.
func (r *RetrievalResources) releaseByIndex(idx int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if idx >= 0 && idx < len(r.handles) && !r.handles[idx].Released {
		r.handles[idx].Released = true
		r.agentBudget.Release()
	}
}

// AcquireN acquires N file handles at once from the budget.
// Returns a single release function that releases all handles.
func (r *RetrievalResources) AcquireN(ctx context.Context, name string, n int) (func(), error) {
	if err := r.checkClosed(); err != nil {
		return nil, err
	}

	indices, err := r.acquireMultiple(ctx, name, n)
	if err != nil {
		return nil, err
	}

	return r.createReleaseAllFunc(indices), nil
}

// acquireMultiple acquires n handles, rolling back on failure.
func (r *RetrievalResources) acquireMultiple(ctx context.Context, name string, n int) ([]int, error) {
	indices := make([]int, 0, n)

	for i := 0; i < n; i++ {
		if err := r.agentBudget.Acquire(ctx); err != nil {
			r.rollbackAcquires(indices)
			return nil, err
		}
		idx := r.trackHandle(name)
		indices = append(indices, idx)
	}

	return indices, nil
}

// rollbackAcquires releases all handles at the given indices.
func (r *RetrievalResources) rollbackAcquires(indices []int) {
	for _, idx := range indices {
		r.releaseByIndex(idx)
	}
}

// createReleaseAllFunc creates a function that releases all given indices.
func (r *RetrievalResources) createReleaseAllFunc(indices []int) func() {
	return func() {
		for _, idx := range indices {
			r.releaseByIndex(idx)
		}
	}
}

// Available returns the number of available handles from the budget.
func (r *RetrievalResources) Available() int64 {
	return r.agentBudget.Available()
}

// ActiveHandles returns a copy of all tracked handles (both active and released).
func (r *RetrievalResources) ActiveHandles() []TrackedHandle {
	r.mu.Lock()
	defer r.mu.Unlock()

	result := make([]TrackedHandle, 0, len(r.handles))
	for _, h := range r.handles {
		if !h.Released {
			result = append(result, h)
		}
	}
	return result
}

// Close releases all unreleased handles and marks resources as closed.
func (r *RetrievalResources) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}

	r.releaseAllUnreleased()
	r.closed = true
	return nil
}

// releaseAllUnreleased releases all handles that haven't been released.
// Must be called with mu held.
func (r *RetrievalResources) releaseAllUnreleased() {
	for i := range r.handles {
		if !r.handles[i].Released {
			r.handles[i].Released = true
			r.agentBudget.Release()
		}
	}
}

// IsClosed returns whether the resources have been closed.
func (r *RetrievalResources) IsClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}
