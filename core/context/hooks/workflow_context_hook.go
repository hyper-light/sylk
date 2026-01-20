// Package hooks provides AR.7.7: Workflow Context Hook - PrePrompt hook for Orchestrator
// agent that injects current workflow state for coordination decisions.
package hooks

import (
	"context"
	"fmt"
	"strings"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// WorkflowContextHookName is the unique identifier for this hook.
	WorkflowContextHookName = "workflow_context"

	// OrchestratorAgentType is the agent type this hook targets.
	OrchestratorAgentType = "orchestrator"

	// DefaultMaxTasks is the default maximum tasks to include.
	DefaultMaxTasks = 10

	// DefaultMaxPipelines is the default maximum pipelines to include.
	DefaultMaxPipelines = 5
)

// =============================================================================
// Workflow Types
// =============================================================================

// WorkflowState represents the current state of a workflow.
type WorkflowState struct {
	// ID is the unique identifier for the workflow.
	ID string

	// Name is the human-readable workflow name.
	Name string

	// Phase is the current workflow phase (planning, executing, validating, completing).
	Phase string

	// Progress is the completion progress (0.0-1.0).
	Progress float64

	// StartTime is when the workflow started.
	StartTime time.Time
}

// OrchestratorTask represents a task managed by the orchestrator.
type OrchestratorTask struct {
	// ID is the unique task identifier.
	ID string

	// Description describes what the task does.
	Description string

	// AssignedTo is the agent type this task is assigned to.
	AssignedTo string

	// PipelineID is the pipeline handling this task (if any).
	PipelineID string

	// Status is the task status (pending, in_progress, completed, blocked).
	Status string

	// Dependencies are task IDs this task depends on.
	Dependencies []string
}

// PipelineInfo represents information about an active pipeline.
type PipelineInfo struct {
	// ID is the unique pipeline identifier.
	ID string

	// TaskID is the task this pipeline is processing.
	TaskID string

	// Status is the pipeline status.
	Status string

	// ActiveAgents are the agents currently working in this pipeline.
	ActiveAgents []string
}

// AgentStateSnapshot represents the current state of an agent.
type AgentStateSnapshot struct {
	// AgentID is the unique agent identifier.
	AgentID string

	// AgentType is the type of agent.
	AgentType string

	// ContextUsage is the percentage of context window used (0.0-1.0).
	ContextUsage float64

	// LastActivity is when the agent was last active.
	LastActivity time.Time

	// CurrentTask is the task the agent is working on.
	CurrentTask string
}

// WorkflowContext contains all workflow-related context for injection.
type WorkflowContext struct {
	// CurrentWorkflow is the active workflow being managed.
	CurrentWorkflow *WorkflowState

	// PendingTasks are tasks not yet dispatched.
	PendingTasks []OrchestratorTask

	// ActivePipelines are currently running pipelines.
	ActivePipelines []PipelineInfo

	// AgentStates are the current states of all agents.
	AgentStates map[string]AgentStateSnapshot
}

// =============================================================================
// Workflow Context Provider Interface
// =============================================================================

// WorkflowContextProvider provides access to workflow state.
type WorkflowContextProvider interface {
	// GetWorkflowContext returns the current workflow context for a session.
	GetWorkflowContext(ctx context.Context, sessionID string) (*WorkflowContext, error)
}

// =============================================================================
// Workflow Context Hook
// =============================================================================

// WorkflowContextHookConfig holds configuration for the workflow context hook.
type WorkflowContextHookConfig struct {
	// WorkflowProvider provides access to workflow state.
	WorkflowProvider WorkflowContextProvider

	// MaxTasks is the maximum number of tasks to include in context.
	// Default: 10
	MaxTasks int

	// MaxPipelines is the maximum number of pipelines to include.
	// Default: 5
	MaxPipelines int
}

// WorkflowContextHook injects workflow context into Orchestrator agent context.
// It executes at HookPriorityNormal (50) in the PrePrompt phase.
type WorkflowContextHook struct {
	workflowProvider WorkflowContextProvider
	maxTasks         int
	maxPipelines     int
	enabled          bool
}

// NewWorkflowContextHook creates a new workflow context hook.
func NewWorkflowContextHook(config WorkflowContextHookConfig) *WorkflowContextHook {
	maxTasks := config.MaxTasks
	if maxTasks <= 0 {
		maxTasks = DefaultMaxTasks
	}

	maxPipelines := config.MaxPipelines
	if maxPipelines <= 0 {
		maxPipelines = DefaultMaxPipelines
	}

	return &WorkflowContextHook{
		workflowProvider: config.WorkflowProvider,
		maxTasks:         maxTasks,
		maxPipelines:     maxPipelines,
		enabled:          true,
	}
}

// =============================================================================
// Hook Interface Implementation
// =============================================================================

// Name returns the hook's unique identifier.
func (h *WorkflowContextHook) Name() string {
	return WorkflowContextHookName
}

// Phase returns when this hook executes (PrePrompt).
func (h *WorkflowContextHook) Phase() ctxpkg.HookPhase {
	return ctxpkg.HookPhasePrePrompt
}

// Priority returns the execution order (Normal = 50).
func (h *WorkflowContextHook) Priority() int {
	return int(ctxpkg.HookPriorityNormal)
}

// Enabled returns whether this hook is currently active.
func (h *WorkflowContextHook) Enabled() bool {
	return h.enabled
}

// SetEnabled enables or disables the hook.
func (h *WorkflowContextHook) SetEnabled(enabled bool) {
	h.enabled = enabled
}

// =============================================================================
// Execution
// =============================================================================

// Execute injects workflow context into Orchestrator agent context.
func (h *WorkflowContextHook) Execute(
	ctx context.Context,
	data *ctxpkg.PromptHookData,
) (*ctxpkg.HookResult, error) {
	// Skip if disabled or no provider
	if !h.enabled || h.workflowProvider == nil {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Only apply to Orchestrator agent
	if !h.isOrchestratorAgent(data.AgentType) {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Get workflow context
	wfCtx, err := h.workflowProvider.GetWorkflowContext(ctx, data.SessionID)
	if err != nil {
		return &ctxpkg.HookResult{Modified: false, Error: err}, nil
	}

	// Skip if no workflow context
	if wfCtx == nil || h.isEmptyWorkflowContext(wfCtx) {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Build workflow context content
	content := h.buildWorkflowContent(wfCtx)

	// Inject into prefetched content
	data.PrefetchedContent = h.injectWorkflowContext(data.PrefetchedContent, content)

	return &ctxpkg.HookResult{
		Modified:        true,
		ModifiedContent: data,
	}, nil
}

// isOrchestratorAgent checks if the agent type is the Orchestrator agent.
func (h *WorkflowContextHook) isOrchestratorAgent(agentType string) bool {
	return strings.ToLower(agentType) == OrchestratorAgentType
}

// isEmptyWorkflowContext checks if the workflow context has no useful data.
func (h *WorkflowContextHook) isEmptyWorkflowContext(wfCtx *WorkflowContext) bool {
	return wfCtx.CurrentWorkflow == nil &&
		len(wfCtx.PendingTasks) == 0 &&
		len(wfCtx.ActivePipelines) == 0 &&
		len(wfCtx.AgentStates) == 0
}

// buildWorkflowContent creates the workflow context block.
func (h *WorkflowContextHook) buildWorkflowContent(wfCtx *WorkflowContext) string {
	var sb strings.Builder

	sb.WriteString("[WORKFLOW_CONTEXT]\n")
	sb.WriteString("Current workflow state for coordination:\n\n")

	h.writeWorkflowState(&sb, wfCtx.CurrentWorkflow)
	h.writePendingTasks(&sb, wfCtx.PendingTasks)
	h.writeActivePipelines(&sb, wfCtx.ActivePipelines)
	h.writeAgentStates(&sb, wfCtx.AgentStates)

	sb.WriteString("Use this context to coordinate agent activities and pipeline progression.\n")

	return sb.String()
}

// writeWorkflowState writes the current workflow state section.
func (h *WorkflowContextHook) writeWorkflowState(sb *strings.Builder, wf *WorkflowState) {
	if wf == nil {
		return
	}

	sb.WriteString("### Current Workflow\n")
	sb.WriteString(fmt.Sprintf("**Name**: %s (ID: %s)\n", wf.Name, wf.ID))
	sb.WriteString(fmt.Sprintf("**Phase**: %s\n", wf.Phase))
	sb.WriteString(fmt.Sprintf("**Progress**: %.0f%%\n", wf.Progress*100))
	if !wf.StartTime.IsZero() {
		sb.WriteString(fmt.Sprintf("**Duration**: %s\n", time.Since(wf.StartTime).Round(time.Second)))
	}
	sb.WriteString("\n")
}

// writePendingTasks writes the pending tasks section.
func (h *WorkflowContextHook) writePendingTasks(sb *strings.Builder, tasks []OrchestratorTask) {
	if len(tasks) == 0 {
		return
	}

	sb.WriteString("### Pending Tasks\n")

	maxTasks := h.maxTasks
	if len(tasks) < maxTasks {
		maxTasks = len(tasks)
	}

	for i := 0; i < maxTasks; i++ {
		task := tasks[i]
		sb.WriteString(fmt.Sprintf("- **%s**: %s", task.ID, task.Description))
		if task.Status != "" {
			sb.WriteString(fmt.Sprintf(" [%s]", task.Status))
		}
		if task.AssignedTo != "" {
			sb.WriteString(fmt.Sprintf(" → %s", task.AssignedTo))
		}
		sb.WriteString("\n")
	}

	if len(tasks) > maxTasks {
		sb.WriteString(fmt.Sprintf("... and %d more tasks\n", len(tasks)-maxTasks))
	}
	sb.WriteString("\n")
}

// writeActivePipelines writes the active pipelines section.
func (h *WorkflowContextHook) writeActivePipelines(sb *strings.Builder, pipelines []PipelineInfo) {
	if len(pipelines) == 0 {
		return
	}

	sb.WriteString("### Active Pipelines\n")

	maxPipelines := h.maxPipelines
	if len(pipelines) < maxPipelines {
		maxPipelines = len(pipelines)
	}

	for i := 0; i < maxPipelines; i++ {
		p := pipelines[i]
		sb.WriteString(fmt.Sprintf("- **%s**: %s", p.ID, p.Status))
		if len(p.ActiveAgents) > 0 {
			sb.WriteString(fmt.Sprintf(" (agents: %s)", strings.Join(p.ActiveAgents, ", ")))
		}
		sb.WriteString("\n")
	}

	if len(pipelines) > maxPipelines {
		sb.WriteString(fmt.Sprintf("... and %d more pipelines\n", len(pipelines)-maxPipelines))
	}
	sb.WriteString("\n")
}

// writeAgentStates writes the agent states section.
func (h *WorkflowContextHook) writeAgentStates(sb *strings.Builder, states map[string]AgentStateSnapshot) {
	if len(states) == 0 {
		return
	}

	sb.WriteString("### Agent States\n")

	for _, state := range states {
		sb.WriteString(fmt.Sprintf("- **%s** (%s): ", state.AgentID, state.AgentType))
		sb.WriteString(fmt.Sprintf("%.0f%% context", state.ContextUsage*100))
		if state.CurrentTask != "" {
			sb.WriteString(fmt.Sprintf(", working on: %s", state.CurrentTask))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
}

// injectWorkflowContext adds workflow context to the prefetched content.
func (h *WorkflowContextHook) injectWorkflowContext(
	existing *ctxpkg.AugmentedQuery,
	workflowContent string,
) *ctxpkg.AugmentedQuery {
	// Create new AugmentedQuery if none exists
	if existing == nil {
		existing = &ctxpkg.AugmentedQuery{
			OriginalQuery: "",
			Excerpts:      make([]ctxpkg.Excerpt, 0),
			Summaries:     make([]ctxpkg.Summary, 0),
		}
	}

	// Add workflow context as a high-priority excerpt
	workflowExcerpt := ctxpkg.Excerpt{
		Source:     "workflow_context",
		Content:    workflowContent,
		Confidence: 1.0, // Highest priority for orchestration context
		LineRange:  [2]int{0, 0},
		TokenCount: len(workflowContent) / 4, // Rough estimate
	}

	// Prepend workflow context to excerpts using race-safe copy pattern
	newExcerpts := make([]ctxpkg.Excerpt, len(existing.Excerpts)+1)
	newExcerpts[0] = workflowExcerpt
	copy(newExcerpts[1:], existing.Excerpts)
	existing.Excerpts = newExcerpts

	return existing
}

// =============================================================================
// ToPromptHook Conversion
// =============================================================================

// ToPromptHook converts this hook to a generic PromptHook for registration.
func (h *WorkflowContextHook) ToPromptHook() *ctxpkg.PromptHook {
	return ctxpkg.NewPromptHook(
		h.Name(),
		h.Phase(),
		h.Priority(),
		func(ctx context.Context, data *ctxpkg.PromptHookData) (*ctxpkg.HookResult, error) {
			return h.Execute(ctx, data)
		},
	)
}
