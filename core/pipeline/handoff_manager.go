package pipeline

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
)

//go:generate mockery --name=HandoffArchiverService --output=./mocks --outpkg=mocks
//go:generate mockery --name=AgentFactoryService --output=./mocks --outpkg=mocks
//go:generate mockery --name=AgentService --output=./mocks --outpkg=mocks

// HandoffStatus represents the current status of a handoff operation.
type HandoffStatus int

const (
	HandoffStatusPending HandoffStatus = iota
	HandoffStatusArchiving
	HandoffStatusCreatingNew
	HandoffStatusInjecting
	HandoffStatusTerminatingOld
	HandoffStatusCompleted
	HandoffStatusFailed
)

// handoffStatusNames maps HandoffStatus values to their string representations.
var handoffStatusNames = map[HandoffStatus]string{
	HandoffStatusPending:        "pending",
	HandoffStatusArchiving:      "archiving",
	HandoffStatusCreatingNew:    "creating_new",
	HandoffStatusInjecting:      "injecting",
	HandoffStatusTerminatingOld: "terminating_old",
	HandoffStatusCompleted:      "completed",
	HandoffStatusFailed:         "failed",
}

// String returns the string representation of HandoffStatus.
func (s HandoffStatus) String() string {
	if name, ok := handoffStatusNames[s]; ok {
		return name
	}
	return "unknown"
}

// HandoffConfig contains configuration for handoff behavior.
type HandoffConfig struct {
	DefaultThreshold float64       // Initial 75% (0.75)
	RetryMaxAttempts int           // Default 3
	RetryBackoff     time.Duration // Initial backoff duration
	HandoffTimeout   time.Duration // Maximum time for handoff
}

// DefaultHandoffConfig returns a HandoffConfig with sensible defaults.
func DefaultHandoffConfig() HandoffConfig {
	return HandoffConfig{
		DefaultThreshold: 0.75,
		RetryMaxAttempts: 3,
		RetryBackoff:     100 * time.Millisecond,
		HandoffTimeout:   30 * time.Second,
	}
}

// ActiveHandoffState represents an in-progress handoff.
type ActiveHandoffState struct {
	ID             string
	AgentID        string
	AgentType      string
	SessionID      string
	PipelineID     string
	HandoffIndex   int       // How many handoffs this agent has had
	TriggerReason  string    // "context_threshold", "quality_degradation"
	TriggerContext float64   // Context usage when triggered
	StartedAt      time.Time
	State          any // The agent-specific handoff state
	Status         HandoffStatus
}

// HandoffRequest is the input to TriggerHandoff.
type HandoffRequest struct {
	AgentID       string
	AgentType     string
	SessionID     string
	PipelineID    string
	HandoffIndex  int
	TriggerReason string
	ContextUsage  float64
	State         any // Agent-specific state
}

// HandoffResult is the output of TriggerHandoff.
type HandoffResult struct {
	Success    bool
	NewAgentID string
	OldAgentID string
	HandoffID  string
	Duration   time.Duration
	Error      error
}

// AgentConfig for creating new agents.
type AgentConfig struct {
	SessionID    string
	PipelineID   string
	HandoffState any
	HandoffIndex int
}

// HandoffArchiverService stores handoff states to dual storage.
type HandoffArchiverService interface {
	Archive(ctx context.Context, state ArchivableHandoffState) error
	Query(ctx context.Context, query ArchiveQuery) ([]*HandoffArchiveEntry, error)
}

// AgentFactoryService creates new agent instances.
type AgentFactoryService interface {
	CreateAgent(ctx context.Context, agentType string, config AgentConfig) (AgentService, error)
}

// AgentService is the minimal interface for agents during handoff.
type AgentService interface {
	ID() string
	Type() string
	InjectHandoffState(state any) error
	Terminate(ctx context.Context) error
}

// HandoffManager coordinates all agent handoffs.
type HandoffManager struct {
	mu           sync.RWMutex
	config       HandoffConfig
	archiver     HandoffArchiverService
	agentFactory AgentFactoryService
	active       map[string]*ActiveHandoffState // Currently active handoffs by agent ID
}

// NewHandoffManager creates a new HandoffManager with the given configuration.
func NewHandoffManager(
	config HandoffConfig,
	archiver HandoffArchiverService,
	factory AgentFactoryService,
) *HandoffManager {
	return &HandoffManager{
		config:       config,
		archiver:     archiver,
		agentFactory: factory,
		active:       make(map[string]*ActiveHandoffState),
	}
}

// ShouldHandoff determines if an agent should trigger a handoff based on context usage.
func (m *HandoffManager) ShouldHandoff(agentID string, contextUsage float64) bool {
	return contextUsage >= m.config.DefaultThreshold
}

// TriggerHandoff executes a handoff for the specified agent.
func (m *HandoffManager) TriggerHandoff(
	ctx context.Context,
	req *HandoffRequest,
) (*HandoffResult, error) {
	startTime := time.Now()
	handoffID := uuid.New().String()

	state := m.createActiveHandoffState(handoffID, req)
	if err := m.registerActiveHandoff(req.AgentID, state); err != nil {
		return m.failedResult(handoffID, req.AgentID, startTime, err), err
	}

	result, err := m.executeHandoff(ctx, state, req)
	m.unregisterActiveHandoff(req.AgentID)

	result.Duration = time.Since(startTime)
	return result, err
}

// createActiveHandoffState initializes a new ActiveHandoffState from the request.
func (m *HandoffManager) createActiveHandoffState(handoffID string, req *HandoffRequest) *ActiveHandoffState {
	return &ActiveHandoffState{
		ID:             handoffID,
		AgentID:        req.AgentID,
		AgentType:      req.AgentType,
		SessionID:      req.SessionID,
		PipelineID:     req.PipelineID,
		HandoffIndex:   req.HandoffIndex,
		TriggerReason:  req.TriggerReason,
		TriggerContext: req.ContextUsage,
		StartedAt:      time.Now(),
		State:          req.State,
		Status:         HandoffStatusPending,
	}
}

// registerActiveHandoff adds a handoff to the active map.
func (m *HandoffManager) registerActiveHandoff(agentID string, state *ActiveHandoffState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.active[agentID]; exists {
		return fmt.Errorf("handoff already in progress for agent %s", agentID)
	}
	m.active[agentID] = state
	return nil
}

// unregisterActiveHandoff removes a handoff from the active map.
func (m *HandoffManager) unregisterActiveHandoff(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.active, agentID)
}

// executeHandoff performs the core handoff steps with retry logic.
func (m *HandoffManager) executeHandoff(
	ctx context.Context,
	state *ActiveHandoffState,
	req *HandoffRequest,
) (*HandoffResult, error) {
	ctx, cancel := context.WithTimeout(ctx, m.config.HandoffTimeout)
	defer cancel()

	if err := m.archiveState(ctx, state); err != nil {
		return m.failedResult(state.ID, req.AgentID, state.StartedAt, err), err
	}

	newAgent, err := m.createNewAgent(ctx, state, req)
	if err != nil {
		return m.failedResult(state.ID, req.AgentID, state.StartedAt, err), err
	}

	if err := m.injectState(ctx, state, newAgent, req); err != nil {
		return m.failedResult(state.ID, req.AgentID, state.StartedAt, err), err
	}

	m.updateHandoffStatus(state, HandoffStatusCompleted)

	return &HandoffResult{
		Success:    true,
		NewAgentID: newAgent.ID(),
		OldAgentID: req.AgentID,
		HandoffID:  state.ID,
	}, nil
}

// archiveState archives the handoff state with retries.
func (m *HandoffManager) archiveState(ctx context.Context, state *ActiveHandoffState) error {
	m.updateHandoffStatus(state, HandoffStatusArchiving)

	// Create a BaseArchivableState to pass to the archiver
	archiveState := &BaseArchivableState{
		AgentID:        state.AgentID,
		AgentType:      state.AgentType,
		SessionID:      state.SessionID,
		PipelineID:     state.PipelineID,
		TriggerReason:  state.TriggerReason,
		TriggerContext: state.TriggerContext,
		HandoffIndex:   state.HandoffIndex,
		StartedAt:      state.StartedAt,
		CompletedAt:    time.Now(),
		Summary:        fmt.Sprintf("Handoff for %s agent in session %s", state.AgentType, state.SessionID),
	}

	return m.withRetry(ctx, func() error {
		return m.archiver.Archive(ctx, archiveState)
	})
}

// createNewAgent creates a new agent instance with retries.
func (m *HandoffManager) createNewAgent(
	ctx context.Context,
	state *ActiveHandoffState,
	req *HandoffRequest,
) (AgentService, error) {
	m.updateHandoffStatus(state, HandoffStatusCreatingNew)

	agentConfig := AgentConfig{
		SessionID:    req.SessionID,
		PipelineID:   req.PipelineID,
		HandoffState: req.State,
		HandoffIndex: req.HandoffIndex + 1,
	}

	var agent AgentService
	err := m.withRetry(ctx, func() error {
		var createErr error
		agent, createErr = m.agentFactory.CreateAgent(ctx, req.AgentType, agentConfig)
		return createErr
	})

	return agent, err
}

// injectState injects the handoff state into the new agent with retries.
func (m *HandoffManager) injectState(
	ctx context.Context,
	state *ActiveHandoffState,
	newAgent AgentService,
	req *HandoffRequest,
) error {
	m.updateHandoffStatus(state, HandoffStatusInjecting)

	return m.withRetry(ctx, func() error {
		return newAgent.InjectHandoffState(req.State)
	})
}

// updateHandoffStatus updates the status of an active handoff.
func (m *HandoffManager) updateHandoffStatus(state *ActiveHandoffState, status HandoffStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state.Status = status
}

// withRetry executes a function with exponential backoff retry logic.
func (m *HandoffManager) withRetry(ctx context.Context, fn func() error) error {
	err := fn()
	if err == nil {
		return nil
	}

	return m.retryLoop(ctx, fn, err)
}

// retryLoop handles retry attempts after initial failure.
func (m *HandoffManager) retryLoop(ctx context.Context, fn func() error, lastErr error) error {
	for attempt := 1; attempt <= m.config.RetryMaxAttempts; attempt++ {
		_ = m.waitWithBackoff(ctx, attempt-1)

		lastErr = fn()
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

// waitWithBackoff waits with exponential backoff before the next retry.
func (m *HandoffManager) waitWithBackoff(ctx context.Context, attempt int) error {
	delay := m.calculateBackoff(attempt)

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// calculateBackoff computes the backoff delay with jitter.
func (m *HandoffManager) calculateBackoff(attempt int) time.Duration {
	multiplier := math.Pow(2, float64(attempt))
	delay := time.Duration(float64(m.config.RetryBackoff) * multiplier)

	// Add 10% jitter
	jitterRange := float64(delay) * 0.1
	jitter := (rand.Float64()*2 - 1) * jitterRange

	return time.Duration(float64(delay) + jitter)
}

// failedResult creates a HandoffResult for a failed handoff.
func (m *HandoffManager) failedResult(
	handoffID string,
	agentID string,
	startTime time.Time,
	err error,
) *HandoffResult {
	return &HandoffResult{
		Success:    false,
		OldAgentID: agentID,
		HandoffID:  handoffID,
		Duration:   time.Since(startTime),
		Error:      err,
	}
}

// GetActiveHandoff retrieves an active handoff by agent ID.
func (m *HandoffManager) GetActiveHandoff(agentID string) (*ActiveHandoffState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, exists := m.active[agentID]
	return state, exists
}

// CancelHandoff cancels an in-progress handoff.
func (m *HandoffManager) CancelHandoff(ctx context.Context, handoffID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for agentID, state := range m.active {
		if state.ID == handoffID {
			state.Status = HandoffStatusFailed
			delete(m.active, agentID)
			return nil
		}
	}

	return fmt.Errorf("handoff %s not found", handoffID)
}
