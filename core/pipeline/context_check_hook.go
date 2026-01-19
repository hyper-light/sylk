// Package pipeline provides agent handoff coordination and context management hooks.
package pipeline

import (
	"context"
	"sync"

	"github.com/adalundhe/sylk/core/skills"
)

// Default context threshold for handoff trigger (75%).
// This is a placeholder - will be replaced by Group 4Q GP-based triggers.
const DefaultContextThreshold = 0.75

// PipelineAgentTypes lists all agent types that participate in pipeline handoffs.
var PipelineAgentTypes = []string{
	"engineer",
	"designer",
	"inspector",
	"tester",
	"orchestrator",
	"guide",
}

// HandoffTrigger interface for triggering agent handoffs.
// Extracted to allow for testing without full HandoffManager.
type HandoffTrigger interface {
	ShouldHandoff(agentID string, contextUsage float64) bool
	TriggerHandoff(ctx context.Context, req *HandoffRequest) (*HandoffResult, error)
}

// AgentContextCheckHook monitors context usage and triggers handoffs.
// Implements skills.PromptHook for post-prompt execution.
type AgentContextCheckHook struct {
	mu              sync.RWMutex
	name            string
	threshold       float64
	handoffManager  HandoffTrigger
	pipelineAgents  map[string]bool
	handoffIndex    map[string]int // tracks handoff count per agent
	lastContextUsed map[string]float64
}

// NewAgentContextCheckHook creates a new context check hook with the given HandoffTrigger.
func NewAgentContextCheckHook(handoffManager HandoffTrigger) *AgentContextCheckHook {
	agentSet := make(map[string]bool, len(PipelineAgentTypes))
	for _, t := range PipelineAgentTypes {
		agentSet[t] = true
	}

	return &AgentContextCheckHook{
		name:            "agent_context_check",
		threshold:       DefaultContextThreshold,
		handoffManager:  handoffManager,
		pipelineAgents:  agentSet,
		handoffIndex:    make(map[string]int),
		lastContextUsed: make(map[string]float64),
	}
}

// Name returns the unique identifier for this hook.
func (h *AgentContextCheckHook) Name() string {
	return h.name
}

// Phase returns HookPhasePostPrompt since this hook runs after LLM response.
func (h *AgentContextCheckHook) Phase() skills.HookPhase {
	return skills.HookPhasePostPrompt
}

// Priority returns HookPriorityHigh (100) - executes last in post-prompt phase.
func (h *AgentContextCheckHook) Priority() skills.HookPriority {
	return skills.HookPriorityHigh // 100 = HookPriorityLast
}

// ShouldRun checks if this hook applies to the given agent type.
func (h *AgentContextCheckHook) ShouldRun(agentType string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.pipelineAgents[agentType]
}

// Execute checks context usage and triggers handoff if threshold exceeded.
func (h *AgentContextCheckHook) Execute(
	ctx context.Context,
	data *skills.PromptHookData,
) skills.HookResult {
	if !h.shouldProcess(data) {
		return skills.HookResult{Continue: true}
	}

	contextUsage := h.calculateContextUsage(data)
	h.recordUsage(data.AgentID, contextUsage)

	if !h.shouldTriggerHandoff(data.AgentID, contextUsage) {
		return skills.HookResult{Continue: true}
	}

	return h.triggerHandoff(ctx, data, contextUsage)
}

// shouldProcess validates data and checks if agent type is eligible.
func (h *AgentContextCheckHook) shouldProcess(data *skills.PromptHookData) bool {
	if data == nil {
		return false
	}
	return h.ShouldRun(getAgentType(data))
}

// calculateContextUsage extracts context usage from hook data.
// Uses TokensUsed if available, otherwise returns 0.
func (h *AgentContextCheckHook) calculateContextUsage(data *skills.PromptHookData) float64 {
	if data.TokensUsed <= 0 {
		return 0.0
	}
	// Assume standard context window of 128K tokens for calculation
	// In production, this would come from model configuration
	const defaultContextWindow = 128000
	return float64(data.TokensUsed) / float64(defaultContextWindow)
}

// recordUsage stores the last context usage for an agent.
func (h *AgentContextCheckHook) recordUsage(agentID string, usage float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastContextUsed[agentID] = usage
}

// shouldTriggerHandoff checks if handoff should be triggered.
func (h *AgentContextCheckHook) shouldTriggerHandoff(agentID string, contextUsage float64) bool {
	if h.handoffManager == nil {
		return false
	}
	return h.handoffManager.ShouldHandoff(agentID, contextUsage)
}

// triggerHandoff initiates the handoff process.
func (h *AgentContextCheckHook) triggerHandoff(
	ctx context.Context,
	data *skills.PromptHookData,
	contextUsage float64,
) skills.HookResult {
	req := h.buildHandoffRequest(data, contextUsage)

	result, err := h.handoffManager.TriggerHandoff(ctx, req)
	if err != nil {
		return skills.HookResult{
			Continue: true,
			Error:    err,
		}
	}

	h.incrementHandoffIndex(data.AgentID)

	return skills.HookResult{
		Continue: result.Success,
	}
}

// buildHandoffRequest creates a HandoffRequest from hook data.
func (h *AgentContextCheckHook) buildHandoffRequest(
	data *skills.PromptHookData,
	contextUsage float64,
) *HandoffRequest {
	h.mu.RLock()
	index := h.handoffIndex[data.AgentID]
	h.mu.RUnlock()

	return &HandoffRequest{
		AgentID:       data.AgentID,
		AgentType:     getAgentType(data),
		SessionID:     data.ConversationID,
		PipelineID:    "", // Will be set by caller if needed
		HandoffIndex:  index,
		TriggerReason: "context_threshold",
		ContextUsage:  contextUsage,
	}
}

// incrementHandoffIndex increments the handoff count for an agent.
func (h *AgentContextCheckHook) incrementHandoffIndex(agentID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handoffIndex[agentID]++
}

// GetThreshold returns the current context threshold.
func (h *AgentContextCheckHook) GetThreshold() float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.threshold
}

// SetThreshold updates the context threshold.
func (h *AgentContextCheckHook) SetThreshold(threshold float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.threshold = threshold
}

// GetLastContextUsage returns the last recorded context usage for an agent.
func (h *AgentContextCheckHook) GetLastContextUsage(agentID string) (float64, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	usage, ok := h.lastContextUsed[agentID]
	return usage, ok
}

// GetHandoffIndex returns the number of handoffs for an agent.
func (h *AgentContextCheckHook) GetHandoffIndex(agentID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.handoffIndex[agentID]
}

// ResetAgent clears tracking data for a specific agent.
func (h *AgentContextCheckHook) ResetAgent(agentID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.handoffIndex, agentID)
	delete(h.lastContextUsed, agentID)
}

// getAgentType extracts agent type from metadata or returns empty string.
func getAgentType(data *skills.PromptHookData) string {
	if data.Metadata == nil {
		return ""
	}
	if agentType, ok := data.Metadata["agent_type"].(string); ok {
		return agentType
	}
	return ""
}
