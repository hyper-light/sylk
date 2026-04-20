package resolver

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Registry dispatches Resolve requests by ecosystem. Safe for concurrent
// use.
type Registry struct {
	mu        sync.RWMutex
	resolvers map[string]Resolver
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{resolvers: make(map[string]Resolver)}
}

// Install registers r. If a resolver already exists for r.Ecosystem(),
// it is replaced.
func (r *Registry) Install(res Resolver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolvers[res.Ecosystem()] = res
}

// Resolve dispatches the request to the resolver registered for the
// named ecosystem.
func (r *Registry) Resolve(ctx context.Context, ecosystem string, req ResolveRequest) (ResolveResult, error) {
	res, err := r.lookup(ecosystem)
	if err != nil {
		return ResolveResult{}, err
	}
	return res.Resolve(ctx, req)
}

func (r *Registry) lookup(ecosystem string) (Resolver, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	res, ok := r.resolvers[ecosystem]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownEcosystem, ecosystem)
	}
	return res, nil
}

// Ecosystems returns the set of ecosystems that have a registered
// resolver, in undefined order.
func (r *Registry) Ecosystems() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.resolvers))
	for e := range r.resolvers {
		out = append(out, e)
	}
	return out
}

// ErrUnknownEcosystem is returned when no resolver is registered for
// the requested ecosystem.
var ErrUnknownEcosystem = errors.New("resolver: no resolver registered for ecosystem")
