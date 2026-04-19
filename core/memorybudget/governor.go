package memorybudget

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
)

type Scope string

const (
	ScopeWorkspaceImage Scope = "workspace-image"
	ScopeOverlay        Scope = "overlay"
	ScopeExecution      Scope = "execution"
)

const (
	totalLimitEnv         = "SYLK_MEMORY_TOTAL_MAX_BYTES"
	workspaceLimitEnv     = "SYLK_MEMORY_WORKSPACE_MAX_BYTES"
	overlayLimitEnv       = "SYLK_MEMORY_OVERLAY_MAX_BYTES"
	executionLimitEnv     = "SYLK_MEMORY_EXECUTION_MAX_BYTES"
	defaultTotalLimit     = 2 << 30
	defaultWorkspaceLimit = 1 << 30
	defaultOverlayLimit   = 512 << 20
	defaultExecutionLimit = 512 << 20
)

var (
	ErrMemoryLimitExceeded = errors.New("memory budget exceeded")
	defaultOnce            sync.Once
	defaultGovernor        *Governor
)

// EvictionCallback is invoked by the Governor when a Reserve/Resize would
// exceed a scope's limit. Implementations free bytes from their own cache
// and return the number of bytes they released. The Governor retries the
// original reservation after the callback chain finishes; succeeding
// callbacks shrink `used` via the Release() on their own reservations.
//
// The callback receives:
//   - scope: the scope whose limit would be exceeded. Callbacks typically
//     ignore scope and always evict from their own cache; multi-scope
//     consumers branch on scope to choose which cache to shrink.
//   - requested: the number of bytes the caller needs to free. Implementations
//     should try to release at least this much, but may return less if
//     further eviction would drop essential data.
//
// Callbacks MUST be non-blocking and bounded in runtime — they run inside
// the Governor's reservation lock, so slow evictions throttle every other
// reservation attempt.
type EvictionCallback func(scope Scope, requested int64) int64

type Governor struct {
	mu         sync.Mutex
	totalLimit int64
	totalUsed  int64
	limits     map[Scope]int64
	used       map[Scope]int64

	// callbacksMu guards only the eviction-callback registry — separated
	// from mu so registration / unregistration does not contend with
	// hot-path reservation traffic.
	callbacksMu sync.RWMutex
	callbacks   []EvictionCallback
}

type Reservation struct {
	governor *Governor
	scope    Scope
	bytes    int64
}

func Current() *Governor {
	defaultOnce.Do(func() {
		defaultGovernor = newGovernorFromEnv()
	})
	return defaultGovernor
}

func NewGovernor(total, workspace, overlay, execution int64) *Governor {
	return &Governor{
		totalLimit: normalizeLimit(total, defaultTotalLimit),
		limits: map[Scope]int64{
			ScopeWorkspaceImage: normalizeLimit(workspace, defaultWorkspaceLimit),
			ScopeOverlay:        normalizeLimit(overlay, defaultOverlayLimit),
			ScopeExecution:      normalizeLimit(execution, defaultExecutionLimit),
		},
		used: make(map[Scope]int64),
	}
}

func newGovernorFromEnv() *Governor {
	return NewGovernor(
		envInt64(totalLimitEnv, defaultTotalLimit),
		envInt64(workspaceLimitEnv, defaultWorkspaceLimit),
		envInt64(overlayLimitEnv, defaultOverlayLimit),
		envInt64(executionLimitEnv, defaultExecutionLimit),
	)
}

func (g *Governor) Reserve(scope Scope, bytes int64) (*Reservation, error) {
	reservation := &Reservation{governor: g, scope: scope}
	if err := reservation.Resize(bytes); err != nil {
		return nil, err
	}
	return reservation, nil
}

func (r *Reservation) Resize(target int64) error {
	if r == nil || r.governor == nil {
		return nil
	}
	if target < 0 {
		target = 0
	}
	return r.governor.resize(r, target)
}

func (r *Reservation) Release() {
	if r == nil {
		return
	}
	_ = r.Resize(0)
}

func (g *Governor) resize(reservation *Reservation, target int64) error {
	g.mu.Lock()
	current := reservation.bytes
	delta := target - current
	if delta <= 0 || g.canGrow(reservation.scope, delta) {
		err := g.applyDeltaLocked(reservation, target, delta)
		g.mu.Unlock()
		return err
	}
	// Growth would exceed a scope or total limit. Drop the lock, fire
	// eviction callbacks, then re-check on retry. Callbacks typically
	// mutate g.used through their own Reservations, so holding g.mu
	// during the callback would deadlock; the callbacks acquire it
	// themselves via Release / Resize on their reservations.
	g.mu.Unlock()
	freed := g.fireEvictionCallbacks(reservation.scope, delta)
	g.mu.Lock()
	defer g.mu.Unlock()
	if freed == 0 || !g.canGrow(reservation.scope, delta) {
		return ErrMemoryLimitExceeded
	}
	return g.applyDeltaLocked(reservation, target, delta)
}

// applyDeltaLocked commits a reservation delta. Caller must hold g.mu.
// Split out so the pre-eviction fast path and the post-eviction retry
// path share commit logic.
func (g *Governor) applyDeltaLocked(reservation *Reservation, target, delta int64) error {
	g.used[reservation.scope] += delta
	g.totalUsed += delta
	reservation.bytes = target
	return nil
}

// fireEvictionCallbacks invokes every registered callback in registration
// order, stopping early when enough bytes have been released to satisfy
// the pending request. Returns the total bytes freed across all callbacks
// that fired.
//
// MEM-06: this is the OnLimitExceeded surface. MEM-07: callbacks are
// registered via RegisterEvictionCallback; the Governor owns the registry
// so eviction strategies can register on package init and every
// reservation path reaches them without re-wiring.
func (g *Governor) fireEvictionCallbacks(scope Scope, requested int64) int64 {
	g.callbacksMu.RLock()
	defer g.callbacksMu.RUnlock()
	var total int64
	for _, cb := range g.callbacks {
		freed := cb(scope, requested-total)
		if freed > 0 {
			total += freed
		}
		if total >= requested {
			return total
		}
	}
	return total
}

// RegisterEvictionCallback adds cb to the eviction-callback chain.
// Callbacks are invoked in registration order when a reservation would
// exceed a scope's limit. Nil callbacks are rejected silently.
func (g *Governor) RegisterEvictionCallback(cb EvictionCallback) {
	if g == nil || cb == nil {
		return
	}
	g.callbacksMu.Lock()
	defer g.callbacksMu.Unlock()
	g.callbacks = append(g.callbacks, cb)
}

// ClearEvictionCallbacks removes every registered callback. Used by tests
// to ensure isolation between cases; production code should not normally
// clear callbacks.
func (g *Governor) ClearEvictionCallbacks() {
	if g == nil {
		return
	}
	g.callbacksMu.Lock()
	defer g.callbacksMu.Unlock()
	g.callbacks = nil
}

// CallbackCount returns the number of registered eviction callbacks.
// Used by tests and diagnostics.
func (g *Governor) CallbackCount() int {
	if g == nil {
		return 0
	}
	g.callbacksMu.RLock()
	defer g.callbacksMu.RUnlock()
	return len(g.callbacks)
}

func (g *Governor) canGrow(scope Scope, delta int64) bool {
	if limit := g.limits[scope]; limit > 0 && g.used[scope]+delta > limit {
		return false
	}
	return g.totalLimit <= 0 || g.totalUsed+delta <= g.totalLimit
}

func envInt64(name string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func normalizeLimit(value, fallback int64) int64 {
	if value <= 0 {
		return fallback
	}
	return value
}
