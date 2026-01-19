// Package pipeline provides orchestrator handoff state for agent context management.
// PH.2: OrchestratorHandoffState represents the coordination state transferred
// when an Orchestrator agent needs to hand off to a new instance.
package pipeline

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// WorkflowState represents the current state of a workflow being managed.
type WorkflowState struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Phase     string    `json:"phase"`    // "planning", "executing", "validating", "completing"
	Progress  float64   `json:"progress"` // 0.0 - 1.0
	StartTime time.Time `json:"start_time"`
}

// OrchestratorTask represents a task tracked by the orchestrator.
type OrchestratorTask struct {
	ID           string   `json:"id"`
	Description  string   `json:"description"`
	AssignedTo   string   `json:"assigned_to,omitempty"` // Agent type
	PipelineID   string   `json:"pipeline_id,omitempty"`
	Status       string   `json:"status"` // "pending", "in_progress", "completed", "blocked"
	Dependencies []string `json:"dependencies,omitempty"`
}

// PipelineInfo represents information about an active pipeline.
type PipelineInfo struct {
	ID           string   `json:"id"`
	TaskID       string   `json:"task_id"`
	Status       string   `json:"status"`
	ActiveAgents []string `json:"active_agents"`
}

// AgentStateSnapshot captures the state of an agent at handoff time.
type AgentStateSnapshot struct {
	AgentID      string    `json:"agent_id"`
	AgentType    string    `json:"agent_type"`
	ContextUsage float64   `json:"context_usage"`
	LastActivity time.Time `json:"last_activity"`
	CurrentTask  string    `json:"current_task,omitempty"`
}

// WaitCondition represents what the orchestrator is blocked on.
type WaitCondition struct {
	Type        string `json:"type"`   // "pipeline_complete", "agent_response", "user_input"
	Target      string `json:"target"` // Pipeline ID, Agent ID, etc.
	Description string `json:"description"`
}

// PlannedAction represents the next action to take.
type PlannedAction struct {
	Action  string         `json:"action"` // "dispatch_task", "spawn_pipeline", "request_info"
	Target  string         `json:"target"`
	Details map[string]any `json:"details,omitempty"`
}

// OrchestratorHandoffState represents the coordination state transferred
// when an Orchestrator agent needs to hand off to a new instance.
type OrchestratorHandoffState struct {
	// Base metadata for archival interface compliance
	baseState *BaseArchivableState

	// Session context
	SessionID    string `json:"session_id"`
	OriginalGoal string `json:"original_goal"` // User's original request

	// Current workflow state
	CurrentWorkflow *WorkflowState     `json:"current_workflow"` // Active workflow being managed
	PendingTasks    []OrchestratorTask `json:"pending_tasks"`    // Tasks not yet dispatched
	ActivePipelines []PipelineInfo     `json:"active_pipelines"` // Currently running pipelines
	CompletedTasks  []OrchestratorTask `json:"completed_tasks"`  // Finished work

	// Coordination state
	AgentStates map[string]AgentStateSnapshot `json:"agent_states"` // State of each agent
	WaitingOn   []WaitCondition               `json:"waiting_on"`   // What we're blocked on
	NextActions []PlannedAction               `json:"next_actions"` // What to do next

	// Context notes
	KeyDecisions []string `json:"key_decisions"` // Important decisions made
	Blockers     []string `json:"blockers"`      // Current blockers
	ContextNotes string   `json:"context_notes"` // Critical context to preserve

	// Handoff metadata
	HandoffIndex  int       `json:"handoff_index"`
	HandoffReason string    `json:"handoff_reason"`
	Timestamp     time.Time `json:"timestamp"`
}

// NewOrchestratorHandoffState creates a new OrchestratorHandoffState.
func NewOrchestratorHandoffState(sessionID, originalGoal string) *OrchestratorHandoffState {
	return &OrchestratorHandoffState{
		SessionID:       sessionID,
		OriginalGoal:    originalGoal,
		PendingTasks:    make([]OrchestratorTask, 0),
		ActivePipelines: make([]PipelineInfo, 0),
		CompletedTasks:  make([]OrchestratorTask, 0),
		AgentStates:     make(map[string]AgentStateSnapshot),
		WaitingOn:       make([]WaitCondition, 0),
		NextActions:     make([]PlannedAction, 0),
		KeyDecisions:    make([]string, 0),
		Blockers:        make([]string, 0),
		Timestamp:       time.Now(),
	}
}

// GetAgentID returns the agent identifier.
func (s *OrchestratorHandoffState) GetAgentID() string {
	if s.baseState != nil {
		return s.baseState.AgentID
	}
	return ""
}

// GetAgentType returns the agent type.
func (s *OrchestratorHandoffState) GetAgentType() string {
	return "orchestrator"
}

// GetSessionID returns the session identifier.
func (s *OrchestratorHandoffState) GetSessionID() string {
	return s.SessionID
}

// GetPipelineID returns the pipeline identifier (empty for orchestrator).
func (s *OrchestratorHandoffState) GetPipelineID() string {
	if s.baseState != nil {
		return s.baseState.PipelineID
	}
	return ""
}

// GetTriggerReason returns why the handoff was triggered.
func (s *OrchestratorHandoffState) GetTriggerReason() string {
	return s.HandoffReason
}

// GetTriggerContext returns the context value that triggered handoff.
func (s *OrchestratorHandoffState) GetTriggerContext() float64 {
	if s.baseState != nil {
		return s.baseState.TriggerContext
	}
	return 0.75
}

// GetHandoffIndex returns the handoff sequence number.
func (s *OrchestratorHandoffState) GetHandoffIndex() int {
	return s.HandoffIndex
}

// GetStartedAt returns when the agent started.
func (s *OrchestratorHandoffState) GetStartedAt() time.Time {
	if s.baseState != nil {
		return s.baseState.StartedAt
	}
	if s.CurrentWorkflow != nil {
		return s.CurrentWorkflow.StartTime
	}
	return time.Time{}
}

// GetCompletedAt returns when handoff occurred.
func (s *OrchestratorHandoffState) GetCompletedAt() time.Time {
	return s.Timestamp
}

// ToJSON serializes the full state to JSON.
func (s *OrchestratorHandoffState) ToJSON() ([]byte, error) {
	return json.Marshal(s)
}

// GetSummary returns a text summary for embedding.
func (s *OrchestratorHandoffState) GetSummary() string {
	parts := []string{
		fmt.Sprintf("Orchestrator handoff for session %s", s.SessionID),
	}

	parts = s.appendGoalSummary(parts)
	parts = s.appendWorkflowSummary(parts)
	parts = append(parts, fmt.Sprintf("Tasks: %d pending, %d completed",
		len(s.PendingTasks), len(s.CompletedTasks)))
	parts = append(parts, fmt.Sprintf("Pipelines: %d active", len(s.ActivePipelines)))
	parts = s.appendBlockersSummary(parts)
	parts = s.appendReasonSummary(parts)

	return strings.Join(parts, ". ")
}

func (s *OrchestratorHandoffState) appendGoalSummary(parts []string) []string {
	if s.OriginalGoal != "" {
		return append(parts, fmt.Sprintf("Goal: %s", s.OriginalGoal))
	}
	return parts
}

func (s *OrchestratorHandoffState) appendWorkflowSummary(parts []string) []string {
	if s.CurrentWorkflow != nil {
		return append(parts, fmt.Sprintf("Workflow: %s (%s, %.0f%% progress)",
			s.CurrentWorkflow.Name, s.CurrentWorkflow.Phase, s.CurrentWorkflow.Progress*100))
	}
	return parts
}

func (s *OrchestratorHandoffState) appendBlockersSummary(parts []string) []string {
	if len(s.Blockers) > 0 {
		return append(parts, fmt.Sprintf("Blockers: %s", strings.Join(s.Blockers, "; ")))
	}
	return parts
}

func (s *OrchestratorHandoffState) appendReasonSummary(parts []string) []string {
	if s.HandoffReason != "" {
		return append(parts, fmt.Sprintf("Reason: %s", s.HandoffReason))
	}
	return parts
}

// SetBaseState sets the base archival state for interface methods.
func (s *OrchestratorHandoffState) SetBaseState(base *BaseArchivableState) {
	s.baseState = base
}

// OrchestratorHandoffBuilder builds OrchestratorHandoffState instances.
type OrchestratorHandoffBuilder struct {
	state *OrchestratorHandoffState
}

// NewOrchestratorHandoffBuilder creates a new builder.
func NewOrchestratorHandoffBuilder(sessionID, originalGoal string) *OrchestratorHandoffBuilder {
	return &OrchestratorHandoffBuilder{
		state: NewOrchestratorHandoffState(sessionID, originalGoal),
	}
}

// WithAgentID sets the agent ID.
func (b *OrchestratorHandoffBuilder) WithAgentID(agentID string) *OrchestratorHandoffBuilder {
	b.ensureBaseState()
	b.state.baseState.AgentID = agentID
	return b
}

// WithPipelineID sets the pipeline ID.
func (b *OrchestratorHandoffBuilder) WithPipelineID(pipelineID string) *OrchestratorHandoffBuilder {
	b.ensureBaseState()
	b.state.baseState.PipelineID = pipelineID
	return b
}

// WithTriggerContext sets the trigger context value.
func (b *OrchestratorHandoffBuilder) WithTriggerContext(context float64) *OrchestratorHandoffBuilder {
	b.ensureBaseState()
	b.state.baseState.TriggerContext = context
	return b
}

// WithStartedAt sets the started time.
func (b *OrchestratorHandoffBuilder) WithStartedAt(startedAt time.Time) *OrchestratorHandoffBuilder {
	b.ensureBaseState()
	b.state.baseState.StartedAt = startedAt
	return b
}

// WithWorkflow sets the current workflow state.
func (b *OrchestratorHandoffBuilder) WithWorkflow(workflow *WorkflowState) *OrchestratorHandoffBuilder {
	b.state.CurrentWorkflow = workflow
	return b
}

// WithPendingTasks sets the pending tasks.
func (b *OrchestratorHandoffBuilder) WithPendingTasks(tasks []OrchestratorTask) *OrchestratorHandoffBuilder {
	b.state.PendingTasks = tasks
	return b
}

// WithActivePipelines sets the active pipelines.
func (b *OrchestratorHandoffBuilder) WithActivePipelines(pipelines []PipelineInfo) *OrchestratorHandoffBuilder {
	b.state.ActivePipelines = pipelines
	return b
}

// WithCompletedTasks sets the completed tasks.
func (b *OrchestratorHandoffBuilder) WithCompletedTasks(tasks []OrchestratorTask) *OrchestratorHandoffBuilder {
	b.state.CompletedTasks = tasks
	return b
}

// WithAgentStates sets the agent states map.
func (b *OrchestratorHandoffBuilder) WithAgentStates(states map[string]AgentStateSnapshot) *OrchestratorHandoffBuilder {
	b.state.AgentStates = states
	return b
}

// WithWaitingOn sets the wait conditions.
func (b *OrchestratorHandoffBuilder) WithWaitingOn(conditions []WaitCondition) *OrchestratorHandoffBuilder {
	b.state.WaitingOn = conditions
	return b
}

// WithNextActions sets the next actions.
func (b *OrchestratorHandoffBuilder) WithNextActions(actions []PlannedAction) *OrchestratorHandoffBuilder {
	b.state.NextActions = actions
	return b
}

// WithKeyDecisions sets the key decisions.
func (b *OrchestratorHandoffBuilder) WithKeyDecisions(decisions []string) *OrchestratorHandoffBuilder {
	b.state.KeyDecisions = decisions
	return b
}

// WithBlockers sets the blockers.
func (b *OrchestratorHandoffBuilder) WithBlockers(blockers []string) *OrchestratorHandoffBuilder {
	b.state.Blockers = blockers
	return b
}

// WithContextNotes sets the context notes.
func (b *OrchestratorHandoffBuilder) WithContextNotes(notes string) *OrchestratorHandoffBuilder {
	b.state.ContextNotes = notes
	return b
}

// WithHandoffIndex sets the handoff index.
func (b *OrchestratorHandoffBuilder) WithHandoffIndex(index int) *OrchestratorHandoffBuilder {
	b.state.HandoffIndex = index
	return b
}

// WithHandoffReason sets the handoff reason.
func (b *OrchestratorHandoffBuilder) WithHandoffReason(reason string) *OrchestratorHandoffBuilder {
	b.state.HandoffReason = reason
	return b
}

// WithTimestamp sets the timestamp.
func (b *OrchestratorHandoffBuilder) WithTimestamp(timestamp time.Time) *OrchestratorHandoffBuilder {
	b.state.Timestamp = timestamp
	return b
}

// Build returns the constructed state.
func (b *OrchestratorHandoffBuilder) Build() *OrchestratorHandoffState {
	return b.state
}

// ensureBaseState initializes the base state if nil.
func (b *OrchestratorHandoffBuilder) ensureBaseState() {
	if b.state.baseState == nil {
		b.state.baseState = &BaseArchivableState{
			AgentType: "orchestrator",
			SessionID: b.state.SessionID,
		}
	}
}

// BuildOrchestratorHandoffState creates an OrchestratorHandoffState from orchestrator data.
// This is a factory function used by the Orchestrator agent.
func BuildOrchestratorHandoffState(
	agentID string,
	sessionID string,
	originalGoal string,
	workflow *WorkflowState,
	pending []OrchestratorTask,
	active []PipelineInfo,
	completed []OrchestratorTask,
	agents map[string]AgentStateSnapshot,
	waiting []WaitCondition,
	next []PlannedAction,
	decisions []string,
	blockers []string,
	notes string,
	handoffIndex int,
	reason string,
	contextUsage float64,
	startedAt time.Time,
) *OrchestratorHandoffState {
	return NewOrchestratorHandoffBuilder(sessionID, originalGoal).
		WithAgentID(agentID).
		WithTriggerContext(contextUsage).
		WithStartedAt(startedAt).
		WithWorkflow(workflow).
		WithPendingTasks(pending).
		WithActivePipelines(active).
		WithCompletedTasks(completed).
		WithAgentStates(agents).
		WithWaitingOn(waiting).
		WithNextActions(next).
		WithKeyDecisions(decisions).
		WithBlockers(blockers).
		WithContextNotes(notes).
		WithHandoffIndex(handoffIndex).
		WithHandoffReason(reason).
		WithTimestamp(time.Now()).
		Build()
}

// InjectHandoffState restores orchestrator state from a handoff state.
// Returns the components needed to resume orchestrator operations.
type InjectedOrchestratorState struct {
	SessionID       string
	OriginalGoal    string
	CurrentWorkflow *WorkflowState
	PendingTasks    []OrchestratorTask
	ActivePipelines []PipelineInfo
	CompletedTasks  []OrchestratorTask
	AgentStates     map[string]AgentStateSnapshot
	WaitingOn       []WaitCondition
	NextActions     []PlannedAction
	KeyDecisions    []string
	Blockers        []string
	ContextNotes    string
	HandoffIndex    int
}

// InjectOrchestratorHandoffState extracts state components from OrchestratorHandoffState.
func InjectOrchestratorHandoffState(state *OrchestratorHandoffState) (*InjectedOrchestratorState, error) {
	if state == nil {
		return nil, fmt.Errorf("handoff state is nil")
	}

	if state.SessionID == "" {
		return nil, fmt.Errorf("handoff state missing session ID")
	}

	return &InjectedOrchestratorState{
		SessionID:       state.SessionID,
		OriginalGoal:    state.OriginalGoal,
		CurrentWorkflow: state.CurrentWorkflow,
		PendingTasks:    state.PendingTasks,
		ActivePipelines: state.ActivePipelines,
		CompletedTasks:  state.CompletedTasks,
		AgentStates:     state.AgentStates,
		WaitingOn:       state.WaitingOn,
		NextActions:     state.NextActions,
		KeyDecisions:    state.KeyDecisions,
		Blockers:        state.Blockers,
		ContextNotes:    state.ContextNotes,
		HandoffIndex:    state.HandoffIndex + 1, // Increment for new instance
	}, nil
}

// FromJSON deserializes an OrchestratorHandoffState from JSON.
func OrchestratorHandoffStateFromJSON(data []byte) (*OrchestratorHandoffState, error) {
	var state OrchestratorHandoffState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal orchestrator handoff state: %w", err)
	}
	return &state, nil
}

// Ensure OrchestratorHandoffState implements ArchivableHandoffState.
var _ ArchivableHandoffState = (*OrchestratorHandoffState)(nil)
