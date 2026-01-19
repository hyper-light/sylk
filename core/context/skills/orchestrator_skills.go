// Package skills provides AR.8.6: Orchestrator Retrieval Skills.
//
// The Orchestrator agent coordinates multi-agent workflows.
// These skills provide:
// - Workflow state retrieval
// - Agent capabilities lookup
// - Coordination context
package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/skills"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// OrchestratorDomain is the skill domain for orchestrator skills.
	OrchestratorDomain = "orchestrator"

	// DefaultAgentLimit is the default max agents to return.
	DefaultAgentLimit = 10

	// DefaultWorkflowDepth is the default workflow history depth.
	DefaultWorkflowDepth = 5
)

// =============================================================================
// Interfaces
// =============================================================================

// WorkflowStateProvider provides workflow state information.
type WorkflowStateProvider interface {
	GetWorkflowState(
		ctx context.Context,
		workflowID string,
		opts *WorkflowStateOptions,
	) (*WorkflowState, error)
}

// AgentCapabilityProvider provides agent capability information.
type AgentCapabilityProvider interface {
	GetAgentCapabilities(
		ctx context.Context,
		agentType string,
		opts *CapabilityOptions,
	) (*AgentCapabilities, error)
}

// CoordinationContextProvider provides coordination context.
type CoordinationContextProvider interface {
	GetCoordinationContext(
		ctx context.Context,
		workflowID string,
		opts *CoordinationOptions,
	) (*CoordinationContext, error)
}

// OrchestratorContentStore is the interface for content store operations.
type OrchestratorContentStore interface {
	Search(query string, filters *ctxpkg.SearchFilters, limit int) ([]*ctxpkg.ContentEntry, error)
}

// OrchestratorSearcher is the interface for tiered search operations.
type OrchestratorSearcher interface {
	SearchWithBudget(ctx context.Context, query string, budget time.Duration) *ctxpkg.TieredSearchResult
}

// =============================================================================
// Types
// =============================================================================

// WorkflowStateOptions configures workflow state retrieval.
type WorkflowStateOptions struct {
	IncludeHistory  bool
	IncludeAgents   bool
	HistoryDepth    int
}

// WorkflowState represents the current state of a workflow.
type WorkflowState struct {
	WorkflowID    string            `json:"workflow_id"`
	Status        string            `json:"status"`
	CurrentPhase  string            `json:"current_phase"`
	ActiveAgents  []string          `json:"active_agents"`
	CompletedTasks []string         `json:"completed_tasks"`
	PendingTasks  []string          `json:"pending_tasks"`
	History       []*WorkflowEvent  `json:"history,omitempty"`
	StartTime     time.Time         `json:"start_time"`
	LastUpdate    time.Time         `json:"last_update"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// WorkflowEvent represents a workflow event.
type WorkflowEvent struct {
	EventID   string    `json:"event_id"`
	EventType string    `json:"event_type"`
	AgentID   string    `json:"agent_id,omitempty"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// CapabilityOptions configures capability retrieval.
type CapabilityOptions struct {
	IncludeSkills   bool
	IncludeLimits   bool
	IncludeExamples bool
}

// AgentCapabilities describes an agent's capabilities.
type AgentCapabilities struct {
	AgentType    string            `json:"agent_type"`
	Description  string            `json:"description"`
	Skills       []string          `json:"skills"`
	Domains      []string          `json:"domains"`
	Strengths    []string          `json:"strengths"`
	Limitations  []string          `json:"limitations,omitempty"`
	TokenLimit   int               `json:"token_limit"`
	Priority     int               `json:"priority"`
	CanHandoff   bool              `json:"can_handoff"`
	HandoffTypes []string          `json:"handoff_types,omitempty"`
}

// CoordinationOptions configures coordination context retrieval.
type CoordinationOptions struct {
	IncludeMessages bool
	IncludeTasks    bool
	MaxMessages     int
}

// CoordinationContext provides context for agent coordination.
type CoordinationContext struct {
	WorkflowID       string            `json:"workflow_id"`
	CurrentObjective string            `json:"current_objective"`
	AgentStatus      []*AgentStatus    `json:"agent_status"`
	RecentMessages   []*AgentMessage   `json:"recent_messages,omitempty"`
	SharedTasks      []*SharedTask     `json:"shared_tasks,omitempty"`
	Constraints      []string          `json:"constraints,omitempty"`
}

// AgentStatus represents an agent's current status.
type AgentStatus struct {
	AgentID     string    `json:"agent_id"`
	AgentType   string    `json:"agent_type"`
	Status      string    `json:"status"`
	CurrentTask string    `json:"current_task,omitempty"`
	LastActive  time.Time `json:"last_active"`
}

// AgentMessage represents an inter-agent message.
type AgentMessage struct {
	MessageID   string    `json:"message_id"`
	FromAgent   string    `json:"from_agent"`
	ToAgent     string    `json:"to_agent,omitempty"`
	Content     string    `json:"content"`
	MessageType string    `json:"message_type"`
	Timestamp   time.Time `json:"timestamp"`
}

// SharedTask represents a task shared between agents.
type SharedTask struct {
	TaskID       string   `json:"task_id"`
	Description  string   `json:"description"`
	AssignedTo   []string `json:"assigned_to"`
	Status       string   `json:"status"`
	Dependencies []string `json:"dependencies,omitempty"`
	Priority     int      `json:"priority"`
}

// =============================================================================
// Dependencies
// =============================================================================

// OrchestratorDependencies holds dependencies for orchestrator skills.
type OrchestratorDependencies struct {
	WorkflowStateProvider       WorkflowStateProvider
	AgentCapabilityProvider     AgentCapabilityProvider
	CoordinationContextProvider CoordinationContextProvider
	ContentStore                OrchestratorContentStore
	Searcher                    OrchestratorSearcher
}

// =============================================================================
// Input/Output Types
// =============================================================================

// GetWorkflowStateInput is input for orchestrator_get_workflow_state.
type GetWorkflowStateInput struct {
	WorkflowID     string `json:"workflow_id"`
	IncludeHistory bool   `json:"include_history,omitempty"`
	IncludeAgents  bool   `json:"include_agents,omitempty"`
	HistoryDepth   int    `json:"history_depth,omitempty"`
}

// GetWorkflowStateOutput is output for orchestrator_get_workflow_state.
type GetWorkflowStateOutput struct {
	Found bool           `json:"found"`
	State *WorkflowState `json:"state,omitempty"`
}

// GetAgentCapabilitiesInput is input for orchestrator_get_agent_capabilities.
type GetAgentCapabilitiesInput struct {
	AgentType       string `json:"agent_type"`
	IncludeSkills   bool   `json:"include_skills,omitempty"`
	IncludeLimits   bool   `json:"include_limits,omitempty"`
	IncludeExamples bool   `json:"include_examples,omitempty"`
}

// GetAgentCapabilitiesOutput is output for orchestrator_get_agent_capabilities.
type GetAgentCapabilitiesOutput struct {
	Found        bool               `json:"found"`
	Capabilities *AgentCapabilities `json:"capabilities,omitempty"`
}

// GetCoordinationContextInput is input for orchestrator_get_coordination_context.
type GetCoordinationContextInput struct {
	WorkflowID      string `json:"workflow_id"`
	IncludeMessages bool   `json:"include_messages,omitempty"`
	IncludeTasks    bool   `json:"include_tasks,omitempty"`
	MaxMessages     int    `json:"max_messages,omitempty"`
}

// GetCoordinationContextOutput is output for orchestrator_get_coordination_context.
type GetCoordinationContextOutput struct {
	Found   bool                 `json:"found"`
	Context *CoordinationContext `json:"context,omitempty"`
}

// =============================================================================
// Skill Constructors
// =============================================================================

// NewGetWorkflowStateSkill creates the orchestrator_get_workflow_state skill.
func NewGetWorkflowStateSkill(deps *OrchestratorDependencies) *skills.Skill {
	return skills.NewSkill("orchestrator_get_workflow_state").
		Domain(OrchestratorDomain).
		Description("Get the current state of a workflow").
		Keywords("workflow", "state", "status", "progress").
		Handler(createGetWorkflowStateHandler(deps)).
		Build()
}

// NewGetAgentCapabilitiesSkill creates the orchestrator_get_agent_capabilities skill.
func NewGetAgentCapabilitiesSkill(deps *OrchestratorDependencies) *skills.Skill {
	return skills.NewSkill("orchestrator_get_agent_capabilities").
		Domain(OrchestratorDomain).
		Description("Get the capabilities of an agent type").
		Keywords("agent", "capability", "skills", "abilities").
		Handler(createGetAgentCapabilitiesHandler(deps)).
		Build()
}

// NewGetCoordinationContextSkill creates the orchestrator_get_coordination_context skill.
func NewGetCoordinationContextSkill(deps *OrchestratorDependencies) *skills.Skill {
	return skills.NewSkill("orchestrator_get_coordination_context").
		Domain(OrchestratorDomain).
		Description("Get coordination context for multi-agent workflows").
		Keywords("coordination", "context", "agents", "workflow").
		Handler(createGetCoordinationContextHandler(deps)).
		Build()
}

// =============================================================================
// Handlers
// =============================================================================

func createGetWorkflowStateHandler(deps *OrchestratorDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in GetWorkflowStateInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if in.WorkflowID == "" {
			return nil, fmt.Errorf("workflow_id is required")
		}

		historyDepth := in.HistoryDepth
		if historyDepth <= 0 {
			historyDepth = DefaultWorkflowDepth
		}

		// Use dedicated provider if available
		if deps.WorkflowStateProvider != nil {
			return getWorkflowStateWithProvider(ctx, deps, in, historyDepth)
		}

		// Fall back to content store search
		if deps.ContentStore != nil {
			return buildWorkflowStateFromStore(deps, in, historyDepth)
		}

		// Fall back to tiered searcher
		if deps.Searcher != nil {
			return buildWorkflowStateFromSearcher(ctx, deps, in, historyDepth)
		}

		return nil, fmt.Errorf("no workflow state provider or content store configured")
	}
}

func getWorkflowStateWithProvider(
	ctx context.Context,
	deps *OrchestratorDependencies,
	in GetWorkflowStateInput,
	historyDepth int,
) (*GetWorkflowStateOutput, error) {
	opts := &WorkflowStateOptions{
		IncludeHistory: in.IncludeHistory,
		IncludeAgents:  in.IncludeAgents,
		HistoryDepth:   historyDepth,
	}

	state, err := deps.WorkflowStateProvider.GetWorkflowState(ctx, in.WorkflowID, opts)
	if err != nil {
		return &GetWorkflowStateOutput{Found: false}, nil
	}

	return &GetWorkflowStateOutput{
		Found: true,
		State: state,
	}, nil
}

func buildWorkflowStateFromStore(
	deps *OrchestratorDependencies,
	in GetWorkflowStateInput,
	historyDepth int,
) (*GetWorkflowStateOutput, error) {
	query := fmt.Sprintf("workflow:%s", in.WorkflowID)
	filters := &ctxpkg.SearchFilters{
		ContentTypes: []ctxpkg.ContentType{ctxpkg.ContentTypePlanWorkflow},
	}

	entries, err := deps.ContentStore.Search(query, filters, historyDepth*2)
	if err != nil {
		return nil, fmt.Errorf("content store search failed: %w", err)
	}

	if len(entries) == 0 {
		return &GetWorkflowStateOutput{Found: false}, nil
	}

	state := buildStateFromEntries(entries, in, historyDepth)
	return &GetWorkflowStateOutput{
		Found: true,
		State: state,
	}, nil
}

func buildWorkflowStateFromSearcher(
	ctx context.Context,
	deps *OrchestratorDependencies,
	in GetWorkflowStateInput,
	historyDepth int,
) (*GetWorkflowStateOutput, error) {
	query := fmt.Sprintf("workflow state %s", in.WorkflowID)
	results := deps.Searcher.SearchWithBudget(ctx, query, ctxpkg.TierWarmBudget)

	if len(results.Results) == 0 {
		return &GetWorkflowStateOutput{Found: false}, nil
	}

	state := buildStateFromEntries(results.Results, in, historyDepth)
	return &GetWorkflowStateOutput{
		Found: true,
		State: state,
	}, nil
}

func buildStateFromEntries(
	entries []*ctxpkg.ContentEntry,
	in GetWorkflowStateInput,
	historyDepth int,
) *WorkflowState {
	state := &WorkflowState{
		WorkflowID:     in.WorkflowID,
		Status:         "active",
		ActiveAgents:   make([]string, 0),
		CompletedTasks: make([]string, 0),
		PendingTasks:   make([]string, 0),
		History:        make([]*WorkflowEvent, 0),
		Metadata:       make(map[string]string),
	}

	for i, entry := range entries {
		if state.StartTime.IsZero() || entry.Timestamp.Before(state.StartTime) {
			state.StartTime = entry.Timestamp
		}
		if entry.Timestamp.After(state.LastUpdate) {
			state.LastUpdate = entry.Timestamp
		}

		// Extract agent info
		if entry.AgentType != "" && !containsStringOrch(state.ActiveAgents, entry.AgentType) {
			state.ActiveAgents = append(state.ActiveAgents, entry.AgentType)
		}

		// Build history if requested
		if in.IncludeHistory && i < historyDepth {
			event := &WorkflowEvent{
				EventID:   entry.ID,
				EventType: string(entry.ContentType),
				AgentID:   entry.AgentID,
				Message:   truncateOrchestratorContent(entry.Content, 200),
				Timestamp: entry.Timestamp,
			}
			state.History = append(state.History, event)
		}

		// Extract current phase from latest entry
		if i == 0 {
			state.CurrentPhase = extractPhaseFromContent(entry.Content)
		}
	}

	return state
}

func createGetAgentCapabilitiesHandler(deps *OrchestratorDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in GetAgentCapabilitiesInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if in.AgentType == "" {
			return nil, fmt.Errorf("agent_type is required")
		}

		// Use dedicated provider if available
		if deps.AgentCapabilityProvider != nil {
			return getCapabilitiesWithProvider(ctx, deps, in)
		}

		// Fall back to predefined capabilities
		return getDefaultCapabilities(in.AgentType)
	}
}

func getCapabilitiesWithProvider(
	ctx context.Context,
	deps *OrchestratorDependencies,
	in GetAgentCapabilitiesInput,
) (*GetAgentCapabilitiesOutput, error) {
	opts := &CapabilityOptions{
		IncludeSkills:   in.IncludeSkills,
		IncludeLimits:   in.IncludeLimits,
		IncludeExamples: in.IncludeExamples,
	}

	caps, err := deps.AgentCapabilityProvider.GetAgentCapabilities(ctx, in.AgentType, opts)
	if err != nil {
		return &GetAgentCapabilitiesOutput{Found: false}, nil
	}

	return &GetAgentCapabilitiesOutput{
		Found:        true,
		Capabilities: caps,
	}, nil
}

func getDefaultCapabilities(agentType string) (*GetAgentCapabilitiesOutput, error) {
	// Default capabilities for known agent types
	caps := getKnownAgentCapabilities(agentType)
	if caps == nil {
		return &GetAgentCapabilitiesOutput{Found: false}, nil
	}

	return &GetAgentCapabilitiesOutput{
		Found:        true,
		Capabilities: caps,
	}, nil
}

func getKnownAgentCapabilities(agentType string) *AgentCapabilities {
	switch strings.ToLower(agentType) {
	case "librarian":
		return &AgentCapabilities{
			AgentType:   "librarian",
			Description: "Code-aware search and symbol resolution",
			Skills:      []string{"search_codebase", "get_symbol_context", "get_pattern_examples"},
			Domains:     []string{"code", "symbols", "patterns"},
			Strengths:   []string{"Code navigation", "Symbol lookup", "Pattern matching"},
			TokenLimit:  8000,
			Priority:    1,
			CanHandoff:  true,
			HandoffTypes: []string{"archivalist", "engineer"},
		}
	case "archivalist":
		return &AgentCapabilities{
			AgentType:   "archivalist",
			Description: "Session history and solution retrieval",
			Skills:      []string{"search_sessions", "get_session_summary", "find_similar_solutions"},
			Domains:     []string{"history", "sessions", "solutions"},
			Strengths:   []string{"Historical context", "Solution patterns", "Session analysis"},
			TokenLimit:  6000,
			Priority:    2,
			CanHandoff:  true,
			HandoffTypes: []string{"librarian", "academic"},
		}
	case "academic":
		return &AgentCapabilities{
			AgentType:   "academic",
			Description: "Research papers and documentation",
			Skills:      []string{"search_research", "get_citations", "summarize_paper"},
			Domains:     []string{"research", "papers", "documentation"},
			Strengths:   []string{"Literature review", "Citation management", "Paper analysis"},
			TokenLimit:  10000,
			Priority:    3,
			CanHandoff:  true,
			HandoffTypes: []string{"archivalist", "guide"},
		}
	case "guide":
		return &AgentCapabilities{
			AgentType:   "guide",
			Description: "Onboarding and tutorials",
			Skills:      []string{"get_onboarding", "find_examples", "explain_concept"},
			Domains:     []string{"tutorials", "examples", "explanations"},
			Strengths:   []string{"User guidance", "Learning paths", "Concept explanation"},
			TokenLimit:  5000,
			Priority:    4,
			CanHandoff:  true,
			HandoffTypes: []string{"librarian", "academic"},
		}
	case "engineer":
		return &AgentCapabilities{
			AgentType:   "engineer",
			Description: "Code implementation and modifications",
			Skills:      []string{"implement_feature", "refactor_code", "fix_bug"},
			Domains:     []string{"implementation", "code", "bugs"},
			Strengths:   []string{"Code writing", "Refactoring", "Bug fixing"},
			TokenLimit:  12000,
			Priority:    1,
			CanHandoff:  true,
			HandoffTypes: []string{"tester", "inspector"},
		}
	case "orchestrator":
		return &AgentCapabilities{
			AgentType:   "orchestrator",
			Description: "Multi-agent workflow coordination",
			Skills:      []string{"get_workflow_state", "get_agent_capabilities", "get_coordination_context"},
			Domains:     []string{"coordination", "workflows", "agents"},
			Strengths:   []string{"Task delegation", "Progress tracking", "Agent coordination"},
			TokenLimit:  4000,
			Priority:    0,
			CanHandoff:  true,
			HandoffTypes: []string{"librarian", "engineer", "guide"},
		}
	default:
		return nil
	}
}

func createGetCoordinationContextHandler(deps *OrchestratorDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in GetCoordinationContextInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if in.WorkflowID == "" {
			return nil, fmt.Errorf("workflow_id is required")
		}

		maxMessages := in.MaxMessages
		if maxMessages <= 0 {
			maxMessages = 10
		}

		// Use dedicated provider if available
		if deps.CoordinationContextProvider != nil {
			return getCoordinationWithProvider(ctx, deps, in, maxMessages)
		}

		// Fall back to content store search
		if deps.ContentStore != nil {
			return buildCoordinationFromStore(deps, in, maxMessages)
		}

		// Fall back to tiered searcher
		if deps.Searcher != nil {
			return buildCoordinationFromSearcher(ctx, deps, in, maxMessages)
		}

		return nil, fmt.Errorf("no coordination context provider or content store configured")
	}
}

func getCoordinationWithProvider(
	ctx context.Context,
	deps *OrchestratorDependencies,
	in GetCoordinationContextInput,
	maxMessages int,
) (*GetCoordinationContextOutput, error) {
	opts := &CoordinationOptions{
		IncludeMessages: in.IncludeMessages,
		IncludeTasks:    in.IncludeTasks,
		MaxMessages:     maxMessages,
	}

	coordCtx, err := deps.CoordinationContextProvider.GetCoordinationContext(ctx, in.WorkflowID, opts)
	if err != nil {
		return &GetCoordinationContextOutput{Found: false}, nil
	}

	return &GetCoordinationContextOutput{
		Found:   true,
		Context: coordCtx,
	}, nil
}

func buildCoordinationFromStore(
	deps *OrchestratorDependencies,
	in GetCoordinationContextInput,
	maxMessages int,
) (*GetCoordinationContextOutput, error) {
	query := fmt.Sprintf("workflow:%s coordination", in.WorkflowID)
	entries, err := deps.ContentStore.Search(query, nil, maxMessages*2)
	if err != nil {
		return nil, fmt.Errorf("content store search failed: %w", err)
	}

	if len(entries) == 0 {
		return &GetCoordinationContextOutput{Found: false}, nil
	}

	coordCtx := buildCoordinationFromEntries(entries, in, maxMessages)
	return &GetCoordinationContextOutput{
		Found:   true,
		Context: coordCtx,
	}, nil
}

func buildCoordinationFromSearcher(
	ctx context.Context,
	deps *OrchestratorDependencies,
	in GetCoordinationContextInput,
	maxMessages int,
) (*GetCoordinationContextOutput, error) {
	query := fmt.Sprintf("workflow coordination %s", in.WorkflowID)
	results := deps.Searcher.SearchWithBudget(ctx, query, ctxpkg.TierWarmBudget)

	if len(results.Results) == 0 {
		return &GetCoordinationContextOutput{Found: false}, nil
	}

	coordCtx := buildCoordinationFromEntries(results.Results, in, maxMessages)
	return &GetCoordinationContextOutput{
		Found:   true,
		Context: coordCtx,
	}, nil
}

func buildCoordinationFromEntries(
	entries []*ctxpkg.ContentEntry,
	in GetCoordinationContextInput,
	maxMessages int,
) *CoordinationContext {
	coordCtx := &CoordinationContext{
		WorkflowID:     in.WorkflowID,
		AgentStatus:    make([]*AgentStatus, 0),
		RecentMessages: make([]*AgentMessage, 0),
		SharedTasks:    make([]*SharedTask, 0),
		Constraints:    make([]string, 0),
	}

	// Track seen agents
	seenAgents := make(map[string]bool)

	for i, entry := range entries {
		// Extract objective from first entry
		if i == 0 {
			coordCtx.CurrentObjective = extractObjective(entry.Content)
		}

		// Track agent status
		if entry.AgentID != "" && !seenAgents[entry.AgentID] {
			seenAgents[entry.AgentID] = true
			status := &AgentStatus{
				AgentID:    entry.AgentID,
				AgentType:  entry.AgentType,
				Status:     "active",
				LastActive: entry.Timestamp,
			}
			coordCtx.AgentStatus = append(coordCtx.AgentStatus, status)
		}

		// Build messages if requested
		if in.IncludeMessages && len(coordCtx.RecentMessages) < maxMessages {
			msg := &AgentMessage{
				MessageID:   entry.ID,
				FromAgent:   entry.AgentID,
				Content:     truncateOrchestratorContent(entry.Content, 300),
				MessageType: string(entry.ContentType),
				Timestamp:   entry.Timestamp,
			}
			coordCtx.RecentMessages = append(coordCtx.RecentMessages, msg)
		}
	}

	return coordCtx
}

// =============================================================================
// Helper Functions
// =============================================================================

func containsStringOrch(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func truncateOrchestratorContent(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}

	// Find a good break point
	breakPoint := maxLen
	for i := maxLen - 1; i > maxLen-50 && i > 0; i-- {
		if content[i] == ' ' || content[i] == '\n' {
			breakPoint = i
			break
		}
	}

	return content[:breakPoint] + "..."
}

func extractPhaseFromContent(content string) string {
	// Look for phase indicators
	lowerContent := strings.ToLower(content)

	phases := []string{"planning", "implementation", "testing", "review", "complete"}
	for _, phase := range phases {
		if strings.Contains(lowerContent, phase) {
			return phase
		}
	}

	return "in_progress"
}

func extractObjective(content string) string {
	// Get first sentence or line as objective
	lines := strings.SplitN(content, "\n", 2)
	objective := strings.TrimSpace(lines[0])

	if len(objective) > 200 {
		objective = objective[:200] + "..."
	}

	return objective
}

// =============================================================================
// Registration
// =============================================================================

// RegisterOrchestratorSkills registers all orchestrator skills with the registry.
func RegisterOrchestratorSkills(
	registry *skills.Registry,
	deps *OrchestratorDependencies,
) error {
	skillsToRegister := []*skills.Skill{
		NewGetWorkflowStateSkill(deps),
		NewGetAgentCapabilitiesSkill(deps),
		NewGetCoordinationContextSkill(deps),
	}

	for _, skill := range skillsToRegister {
		if err := registry.Register(skill); err != nil {
			return fmt.Errorf("failed to register %s: %w", skill.Name, err)
		}
	}

	return nil
}
