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

type Governor struct {
	mu         sync.Mutex
	totalLimit int64
	totalUsed  int64
	limits     map[Scope]int64
	used       map[Scope]int64
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
	defer g.mu.Unlock()
	current := reservation.bytes
	delta := target - current
	if delta > 0 && !g.canGrow(reservation.scope, delta) {
		return ErrMemoryLimitExceeded
	}
	g.used[reservation.scope] += delta
	g.totalUsed += delta
	reservation.bytes = target
	return nil
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
