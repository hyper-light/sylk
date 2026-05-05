// Package fabric is the top-level MCP capability plane orchestrator.
//
// One Fabric owns: layered catalog → per-server connections → durable
// journal → projection → Guardian wiring → outcome listener → ingest
// fan-out. Located in a subpackage so the wire-types package (core/mcp)
// stays free of cyclic imports from the higher-level layers.
package fabric

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/mcp"
)

// FabricVersion identifies this implementation in MCP handshakes.
const FabricVersion = "0.1.0-sylk"

// FabricImplementation is the canonical Implementation block Sylk announces.
var FabricImplementation = mcp.Implementation{
	Name:    "sylk",
	Version: FabricVersion,
}

// FabricStatus is a per-server health summary surfaced through the
// diagnostics path.
type FabricStatus struct {
	ServerID         string
	Connected        bool
	HandshakeAt      time.Time
	CapabilityFP     string
	ToolCount        int
	ResourceCount    int
	PromptCount      int
	LastError        string
	LastErrorAt      time.Time
}

// fabricRegistry is a small concurrent map for per-server status. Bounded by
// the catalog size, so growth is bounded by configuration, not by load.
type fabricRegistry struct {
	mu     sync.RWMutex
	status map[string]*FabricStatus
}

func newFabricRegistry() *fabricRegistry {
	return &fabricRegistry{status: map[string]*FabricStatus{}}
}

func (r *fabricRegistry) upsert(id string, mutator func(*FabricStatus)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, ok := r.status[id]
	if !ok {
		cur = &FabricStatus{ServerID: id}
		r.status[id] = cur
	}
	mutator(cur)
}

func (r *fabricRegistry) snapshot() []FabricStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]FabricStatus, 0, len(r.status))
	for _, s := range r.status {
		out = append(out, *s)
	}
	return out
}

func (r *fabricRegistry) drop(id string) {
	r.mu.Lock()
	delete(r.status, id)
	r.mu.Unlock()
}

// errFabricNotConnected is returned when callers try to drive a server that
// has no active session.
var errFabricNotConnected = errors.New("mcp fabric: server not connected")

// scopeForFabric returns a child scope rooted at the supplied parent scope.
// The fabric uses its own scope so its goroutines (catalog watcher, per-server
// connection workers) shut down with the fabric without dragging the parent
// scope down.
func scopeForFabric(parent context.Context, scopeName string, base *concurrency.GoroutineScope) (*concurrency.GoroutineScope, bool) {
	if base != nil {
		return base, false
	}
	return concurrency.NewGoroutineScope(parent, scopeName, nil), true
}

// timeoutForCapabilitySync derives the per-server initial sync deadline.
// Anchor: handshake (1 RTT) + tools/list, resources/list, prompts/list (3
// RTTs). One RTT is bounded by perRoundTripBudget; we add headroom for big
// inventories. Multiplier is named, no magic numbers.
const (
	syncRoundTrips    = 4
	syncRoundTripBudget = 750 * time.Millisecond
	syncMaxDuration   = syncRoundTrips * syncRoundTripBudget
)

func capabilitySyncDeadline() time.Duration {
	return syncMaxDuration
}

// safeID returns a non-empty server id used as part of goroutine names.
func safeID(id string) string {
	if id == "" {
		return "anonymous"
	}
	return id
}

// fmtError is a format helper that keeps fabric error strings consistent.
func fmtError(format string, args ...any) error {
	return fmt.Errorf("mcp fabric: "+format, args...)
}
