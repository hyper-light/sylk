// Package concurrency provides PH.7 PipelineController Handoff Integration.
// This file extends PipelineController with HandoffManager integration for
// per-agent context tracking and handoff coordination.
package concurrency

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/pipeline"
)

// Errors for handoff integration.
var (
	ErrAgentNotFound       = errors.New("agent not found")
	ErrHandoffInProgress   = errors.New("handoff already in progress")
	ErrInvalidContextUsage = errors.New("context usage must be between 0 and 1")
	ErrNoHandoffManager    = errors.New("handoff manager not configured")
	ErrSupervisorNotFound  = errors.New("supervisor not found for agent")
	ErrAgentNotHandoffable = errors.New("agent does not implement HandoffableAgent")
)

// HandoffableAgent defines the interface for agents that can participate in handoffs.
// All pipeline agents must implement this interface to support context-based handoffs.
type HandoffableAgent interface {
	// BuildHandoffState creates the archivable state for this agent.
	BuildHandoffState(ctx context.Context) (pipeline.ArchivableHandoffState, error)
	// InjectHandoffState restores state from a previous agent instance.
	InjectHandoffState(state any) error
	// GetContextUsage returns current context window usage (0.0-1.0).
	GetContextUsage() float64
	// ID returns the agent's unique identifier.
	ID() string
	// Type returns the agent type (engineer, designer, etc).
	Type() string
}

// HandoffResult wraps pipeline.HandoffResult for the integration layer.
type HandoffResult struct {
	Success    bool
	NewAgentID string
	OldAgentID string
	HandoffID  string
	Duration   time.Duration
	Error      error
}

// PipelineHandoffConfig configures the handoff controller behavior.
type PipelineHandoffConfig struct {
	DefaultThreshold float64 // Default handoff threshold (0.75)
}

// DefaultPipelineHandoffConfig returns sensible defaults.
func DefaultPipelineHandoffConfig() PipelineHandoffConfig {
	return PipelineHandoffConfig{
		DefaultThreshold: 0.75,
	}
}

// PipelineHandoffController wraps PipelineController with handoff capabilities.
// It adds per-agent context tracking and handoff coordination.
type PipelineHandoffController struct {
	controller        *PipelineController
	handoffManager    *pipeline.HandoffManager
	config            PipelineHandoffConfig
	contextUsageMu    sync.RWMutex
	agentContextUsage map[string]float64
}

// NewPipelineHandoffController creates a new handoff-enabled controller.
func NewPipelineHandoffController(
	controller *PipelineController,
	handoffManager *pipeline.HandoffManager,
	config PipelineHandoffConfig,
) *PipelineHandoffController {
	config = normalizeHandoffConfig(config)
	return &PipelineHandoffController{
		controller:        controller,
		handoffManager:    handoffManager,
		config:            config,
		agentContextUsage: make(map[string]float64),
	}
}

// normalizeHandoffConfig applies defaults to zero values.
func normalizeHandoffConfig(cfg PipelineHandoffConfig) PipelineHandoffConfig {
	if cfg.DefaultThreshold <= 0 {
		cfg.DefaultThreshold = DefaultPipelineHandoffConfig().DefaultThreshold
	}
	return cfg
}

// Controller returns the underlying PipelineController.
func (c *PipelineHandoffController) Controller() *PipelineController {
	return c.controller
}

// HandoffManager returns the configured HandoffManager.
func (c *PipelineHandoffController) HandoffManager() *pipeline.HandoffManager {
	return c.handoffManager
}

// UpdateContextUsage records the current context usage for an agent.
// Usage must be between 0.0 and 1.0.
func (c *PipelineHandoffController) UpdateContextUsage(agentID string, usage float64) error {
	if err := validateContextUsage(usage); err != nil {
		return err
	}

	c.contextUsageMu.Lock()
	c.agentContextUsage[agentID] = usage
	c.contextUsageMu.Unlock()

	return nil
}

// validateContextUsage ensures usage is within valid bounds.
func validateContextUsage(usage float64) error {
	if usage < 0 || usage > 1 {
		return ErrInvalidContextUsage
	}
	return nil
}

// GetContextUsage retrieves the current context usage for an agent.
func (c *PipelineHandoffController) GetContextUsage(agentID string) (float64, bool) {
	c.contextUsageMu.RLock()
	usage, exists := c.agentContextUsage[agentID]
	c.contextUsageMu.RUnlock()
	return usage, exists
}

// ShouldHandoff determines if an agent should trigger a handoff.
// Returns true if the agent's context usage exceeds the threshold.
func (c *PipelineHandoffController) ShouldHandoff(agentID string) bool {
	c.contextUsageMu.RLock()
	usage, exists := c.agentContextUsage[agentID]
	c.contextUsageMu.RUnlock()

	if !exists {
		return false
	}

	return usage >= c.config.DefaultThreshold
}

// TriggerAgentHandoff initiates a handoff for the specified agent.
// The agent must implement HandoffableAgent.
func (c *PipelineHandoffController) TriggerAgentHandoff(
	ctx context.Context,
	agent HandoffableAgent,
) (*HandoffResult, error) {
	if c.handoffManager == nil {
		return nil, ErrNoHandoffManager
	}

	state, err := agent.BuildHandoffState(ctx)
	if err != nil {
		return &HandoffResult{Success: false, Error: err}, err
	}

	usage := agent.GetContextUsage()
	req := c.buildHandoffRequest(agent, state, usage)

	result, err := c.handoffManager.TriggerHandoff(ctx, req)
	return c.convertResult(result, err), err
}

// buildHandoffRequest creates a HandoffRequest from agent state.
func (c *PipelineHandoffController) buildHandoffRequest(
	agent HandoffableAgent,
	state pipeline.ArchivableHandoffState,
	usage float64,
) *pipeline.HandoffRequest {
	return &pipeline.HandoffRequest{
		AgentID:       agent.ID(),
		AgentType:     agent.Type(),
		SessionID:     state.GetSessionID(),
		PipelineID:    c.controller.PipelineID(),
		HandoffIndex:  state.GetHandoffIndex(),
		TriggerReason: "context_threshold",
		ContextUsage:  usage,
		State:         state,
	}
}

// convertResult converts pipeline.HandoffResult to our HandoffResult.
func (c *PipelineHandoffController) convertResult(
	r *pipeline.HandoffResult,
	err error,
) *HandoffResult {
	if r == nil {
		return &HandoffResult{Success: false, Error: err}
	}

	return &HandoffResult{
		Success:    r.Success,
		NewAgentID: r.NewAgentID,
		OldAgentID: r.OldAgentID,
		HandoffID:  r.HandoffID,
		Duration:   r.Duration,
		Error:      r.Error,
	}
}

// ReplaceAgentSupervisor replaces the supervisor for an agent after handoff.
// This updates the controller's supervisor map with the new agent's supervisor.
func (c *PipelineHandoffController) ReplaceAgentSupervisor(
	ctx context.Context,
	oldAgentID string,
	newSupervisor *AgentSupervisor,
) error {
	if newSupervisor == nil {
		return ErrSupervisorNotFound
	}

	c.controller.UnregisterSupervisor(oldAgentID)
	c.controller.RegisterSupervisor(newSupervisor.AgentID(), newSupervisor)

	c.removeAgentContextUsage(oldAgentID)
	return nil
}

// removeAgentContextUsage removes context tracking for an agent.
func (c *PipelineHandoffController) removeAgentContextUsage(agentID string) {
	c.contextUsageMu.Lock()
	delete(c.agentContextUsage, agentID)
	c.contextUsageMu.Unlock()
}

// RegisterAgent registers an agent for context tracking.
func (c *PipelineHandoffController) RegisterAgent(agentID string) {
	c.contextUsageMu.Lock()
	c.agentContextUsage[agentID] = 0
	c.contextUsageMu.Unlock()
}

// UnregisterAgent removes an agent from context tracking.
func (c *PipelineHandoffController) UnregisterAgent(agentID string) {
	c.removeAgentContextUsage(agentID)
}

// AgentCount returns the number of tracked agents.
func (c *PipelineHandoffController) AgentCount() int {
	c.contextUsageMu.RLock()
	count := len(c.agentContextUsage)
	c.contextUsageMu.RUnlock()
	return count
}

// AllAgentUsages returns a snapshot of all agent context usages.
func (c *PipelineHandoffController) AllAgentUsages() map[string]float64 {
	c.contextUsageMu.RLock()
	result := make(map[string]float64, len(c.agentContextUsage))
	for k, v := range c.agentContextUsage {
		result[k] = v
	}
	c.contextUsageMu.RUnlock()
	return result
}

// Config returns the handoff configuration.
func (c *PipelineHandoffController) Config() PipelineHandoffConfig {
	return c.config
}

// SetThreshold updates the default handoff threshold.
func (c *PipelineHandoffController) SetThreshold(threshold float64) error {
	if err := validateContextUsage(threshold); err != nil {
		return err
	}
	c.config.DefaultThreshold = threshold
	return nil
}
