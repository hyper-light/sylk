// Package hooks provides AR.7.3: PressureEvictionHook - PrePrompt hook that forces
// context eviction for knowledge agents under memory pressure.
package hooks

import (
	"context"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/resources"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// PressureEvictionHookName is the unique identifier for this hook.
	PressureEvictionHookName = "pressure_eviction"

	// EvictionPercentHigh is the eviction percentage at high pressure.
	EvictionPercentHigh = 0.25

	// EvictionPercentCritical is the eviction percentage at critical pressure.
	EvictionPercentCritical = 0.50
)

// pressureEvictionAgents lists agents subject to pressure eviction.
var pressureEvictionAgents = map[string]bool{
	"librarian":   true,
	"archivalist": true,
	"academic":    true,
	"architect":   true,
	"guide":       true,
}

// =============================================================================
// Context Manager Interface
// =============================================================================

// EvictableContextManager is the interface for context managers that support eviction.
type EvictableContextManager interface {
	// ForceEvict evicts the given percentage of context from the agent.
	ForceEvict(agentID string, percent float64) (int, error)
}

// =============================================================================
// Pressure Eviction Hook
// =============================================================================

// PressureEvictionHookConfig holds configuration for the eviction hook.
type PressureEvictionHookConfig struct {
	// ContextManager provides eviction capabilities.
	ContextManager EvictableContextManager

	// HighEvictionPercent is the eviction percentage at high pressure.
	// Default: 0.25 (25%)
	HighEvictionPercent float64

	// CriticalEvictionPercent is the eviction percentage at critical pressure.
	// Default: 0.50 (50%)
	CriticalEvictionPercent float64
}

// PressureEvictionHook forces context eviction for knowledge agents under memory pressure.
// It executes at HookPriorityFirst (0) to free memory before other hooks run.
type PressureEvictionHook struct {
	contextManager          EvictableContextManager
	highEvictionPercent     float64
	criticalEvictionPercent float64
	enabled                 bool
}

// NewPressureEvictionHook creates a new pressure eviction hook.
func NewPressureEvictionHook(config PressureEvictionHookConfig) *PressureEvictionHook {
	highPercent := config.HighEvictionPercent
	if highPercent <= 0 {
		highPercent = EvictionPercentHigh
	}

	criticalPercent := config.CriticalEvictionPercent
	if criticalPercent <= 0 {
		criticalPercent = EvictionPercentCritical
	}

	return &PressureEvictionHook{
		contextManager:          config.ContextManager,
		highEvictionPercent:     highPercent,
		criticalEvictionPercent: criticalPercent,
		enabled:                 true,
	}
}

// =============================================================================
// Hook Interface Implementation
// =============================================================================

// Name returns the hook's unique identifier.
func (h *PressureEvictionHook) Name() string {
	return PressureEvictionHookName
}

// Phase returns when this hook executes (PrePrompt).
func (h *PressureEvictionHook) Phase() ctxpkg.HookPhase {
	return ctxpkg.HookPhasePrePrompt
}

// Priority returns the execution order (First = 0).
func (h *PressureEvictionHook) Priority() int {
	return int(ctxpkg.HookPriorityFirst)
}

// Enabled returns whether this hook is currently active.
func (h *PressureEvictionHook) Enabled() bool {
	return h.enabled
}

// SetEnabled enables or disables the hook.
func (h *PressureEvictionHook) SetEnabled(enabled bool) {
	h.enabled = enabled
}

// =============================================================================
// Execution
// =============================================================================

// Execute forces context eviction if pressure level warrants it.
func (h *PressureEvictionHook) Execute(
	ctx context.Context,
	data *ctxpkg.PromptHookData,
) (*ctxpkg.HookResult, error) {
	// Skip if disabled or no context manager
	if !h.enabled || h.contextManager == nil {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Skip if not a knowledge agent
	if !h.isEvictionAgent(data.AgentType) {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Get eviction percentage based on pressure level
	evictionPercent := h.getEvictionPercent(data.PressureLevel)
	if evictionPercent <= 0 {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Perform eviction
	_, err := h.contextManager.ForceEvict(data.AgentID, evictionPercent)
	if err != nil {
		// Log error but don't fail the hook
		return &ctxpkg.HookResult{
			Modified: false,
			Error:    err,
		}, nil
	}

	return &ctxpkg.HookResult{Modified: false}, nil
}

func (h *PressureEvictionHook) isEvictionAgent(agentType string) bool {
	return pressureEvictionAgents[agentType]
}

func (h *PressureEvictionHook) getEvictionPercent(pressureLevel int) float64 {
	level := resources.PressureLevel(pressureLevel)

	switch level {
	case resources.PressureHigh:
		return h.highEvictionPercent
	case resources.PressureCritical:
		return h.criticalEvictionPercent
	default:
		// No eviction at Normal or Elevated
		return 0
	}
}

// =============================================================================
// ToPromptHook Conversion
// =============================================================================

// ToPromptHook converts this hook to a generic PromptHook for registration.
func (h *PressureEvictionHook) ToPromptHook() *ctxpkg.PromptHook {
	return ctxpkg.NewPromptHook(
		h.Name(),
		h.Phase(),
		h.Priority(),
		func(ctx context.Context, data *ctxpkg.PromptHookData) (*ctxpkg.HookResult, error) {
			return h.Execute(ctx, data)
		},
	)
}
