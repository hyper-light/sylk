package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// HO.10.2 HandoffManager - Orchestrates All Handoff Components
// =============================================================================
//
// HandoffManager is the top-level orchestrator for context handoffs. It combines:
//   - HandoffController: Decision logic for when to handoff
//   - HandoffExecutor: Execution of handoff operations
//   - ProfileLearner: Learning from handoff outcomes
//
// Key features:
//   - EvaluateAndExecute: Evaluates and executes handoff if triggered
//   - ForceHandoff: Forces an immediate handoff
//   - UpdateContext: Continuously maintains PreparedContext
//   - Background evaluation: Periodic automatic evaluation
//   - Lifecycle management: Start(), Stop() with graceful shutdown
//   - Thread-safe: All operations protected by appropriate synchronization
//
// Example usage:
//
//	manager := NewHandoffManager(config, controller, executor, learner)
//	manager.Start()
//	defer manager.Stop()
//
//	manager.UpdateContext(messages)
//	result := manager.EvaluateAndExecute(ctx)

// Manager errors
var (
	ErrManagerClosed      = errors.New("handoff manager is closed")
	ErrManagerNotStarted  = errors.New("handoff manager not started")
	ErrNoContextPrepared  = errors.New("no context prepared for handoff")
	ErrEvaluationDisabled = errors.New("automatic evaluation is disabled")
)

// HandoffStatus represents the current status of the handoff manager.
type HandoffStatus int

const (
	// StatusStopped indicates the manager is not running.
	StatusStopped HandoffStatus = iota

	// StatusRunning indicates the manager is actively running.
	StatusRunning

	// StatusEvaluating indicates a handoff evaluation is in progress.
	StatusEvaluating

	// StatusExecuting indicates a handoff execution is in progress.
	StatusExecuting

	// StatusShuttingDown indicates the manager is shutting down.
	StatusShuttingDown
)

// String returns a human-readable name for the status.
func (s HandoffStatus) String() string {
	switch s {
	case StatusStopped:
		return "Stopped"
	case StatusRunning:
		return "Running"
	case StatusEvaluating:
		return "Evaluating"
	case StatusExecuting:
		return "Executing"
	case StatusShuttingDown:
		return "ShuttingDown"
	default:
		return "Unknown"
	}
}

// =============================================================================
// Manager Configuration
// =============================================================================

// HandoffManagerConfig configures the HandoffManager behavior.
type HandoffManagerConfig struct {
	// EvaluationInterval controls how often automatic evaluation runs.
	// Set to 0 to disable automatic evaluation.
	EvaluationInterval time.Duration `json:"evaluation_interval"`

	// MinEvaluationInterval is the minimum time between evaluations.
	MinEvaluationInterval time.Duration `json:"min_evaluation_interval"`

	// DefaultTimeout is the default timeout for handoff operations.
	DefaultTimeout time.Duration `json:"default_timeout"`

	// EnableAutoEvaluation enables automatic periodic evaluation.
	EnableAutoEvaluation bool `json:"enable_auto_evaluation"`

	// EnableLearning enables learning from handoff outcomes.
	EnableLearning bool `json:"enable_learning"`

	// EnableContextMaintenance enables background context maintenance.
	EnableContextMaintenance bool `json:"enable_context_maintenance"`

	// GracefulShutdownTimeout is the maximum time to wait for shutdown.
	GracefulShutdownTimeout time.Duration `json:"graceful_shutdown_timeout"`

	// AgentID identifies the agent using this manager.
	AgentID string `json:"agent_id"`

	// ModelName identifies the model used by this manager.
	ModelName string `json:"model_name"`
}

// DefaultHandoffManagerConfig returns sensible defaults.
func DefaultHandoffManagerConfig() *HandoffManagerConfig {
	return &HandoffManagerConfig{
		EvaluationInterval:       30 * time.Second,
		MinEvaluationInterval:    5 * time.Second,
		DefaultTimeout:           60 * time.Second,
		EnableAutoEvaluation:     true,
		EnableLearning:           true,
		EnableContextMaintenance: true,
		GracefulShutdownTimeout:  30 * time.Second,
		AgentID:                  "default",
		ModelName:                "default",
	}
}

// Clone creates a deep copy of the config.
func (c *HandoffManagerConfig) Clone() *HandoffManagerConfig {
	if c == nil {
		return nil
	}

	return &HandoffManagerConfig{
		EvaluationInterval:       c.EvaluationInterval,
		MinEvaluationInterval:    c.MinEvaluationInterval,
		DefaultTimeout:           c.DefaultTimeout,
		EnableAutoEvaluation:     c.EnableAutoEvaluation,
		EnableLearning:           c.EnableLearning,
		EnableContextMaintenance: c.EnableContextMaintenance,
		GracefulShutdownTimeout:  c.GracefulShutdownTimeout,
		AgentID:                  c.AgentID,
		ModelName:                c.ModelName,
	}
}

// =============================================================================
// HandoffManager
// =============================================================================

// HandoffManager orchestrates all handoff components.
type HandoffManager struct {
	mu sync.RWMutex

	// Core components
	controller *HandoffController
	executor   *HandoffExecutor
	learner    *ProfileLearner

	// Configuration
	config *HandoffManagerConfig

	// Context state
	preparedContext *PreparedContext
	contextMu       sync.RWMutex

	// Status tracking
	status        atomic.Int32
	lastEvaluation time.Time
	lastHandoff    time.Time

	// Lifecycle management
	stopCh  chan struct{}
	doneCh  chan struct{}
	started atomic.Bool
	closed  atomic.Bool

	// Statistics
	evaluations      atomic.Int64
	handoffsExecuted atomic.Int64
	handoffsForced   atomic.Int64
	handoffsFailed   atomic.Int64

	// Recent results for monitoring
	recentResults *CircularBuffer[*HandoffResult]
}

// NewHandoffManager creates a new HandoffManager with the specified components.
// If any component is nil, a default instance is created.
func NewHandoffManager(
	config *HandoffManagerConfig,
	controller *HandoffController,
	executor *HandoffExecutor,
	learner *ProfileLearner,
) *HandoffManager {
	if config == nil {
		config = DefaultHandoffManagerConfig()
	}
	if controller == nil {
		controller = NewHandoffController(nil, nil, nil)
	}
	if executor == nil {
		executor = NewHandoffExecutor(nil, nil)
	}
	if learner == nil {
		learner = NewProfileLearner(nil, nil)
	}

	manager := &HandoffManager{
		controller:      controller,
		executor:        executor,
		learner:         learner,
		config:          config.Clone(),
		preparedContext: NewPreparedContextDefault(),
		stopCh:          make(chan struct{}),
		doneCh:          make(chan struct{}),
		recentResults:   NewCircularBuffer[*HandoffResult](100),
	}

	manager.status.Store(int32(StatusStopped))

	return manager
}

// =============================================================================
// Lifecycle Management
// =============================================================================

// Start begins the manager's background operations.
// Safe to call multiple times; subsequent calls are no-ops if already running.
func (m *HandoffManager) Start() error {
	if m.closed.Load() {
		return ErrManagerClosed
	}

	if m.started.Swap(true) {
		return nil // Already started
	}

	m.status.Store(int32(StatusRunning))

	// Start background goroutine for periodic evaluation
	go m.backgroundLoop()

	return nil
}

// Stop gracefully stops the manager.
// Blocks until background operations complete or timeout.
func (m *HandoffManager) Stop() error {
	if m.closed.Swap(true) {
		return ErrManagerClosed
	}

	if !m.started.Load() {
		close(m.doneCh)
		return nil
	}

	m.status.Store(int32(StatusShuttingDown))

	// Signal background goroutine to stop
	close(m.stopCh)

	// Wait for completion or timeout
	select {
	case <-m.doneCh:
		// Clean shutdown
	case <-time.After(m.config.GracefulShutdownTimeout):
		// Force close after timeout
	}

	m.status.Store(int32(StatusStopped))

	// Close executor
	if m.executor != nil {
		m.executor.Close()
	}

	return nil
}

// IsRunning returns true if the manager is currently running.
func (m *HandoffManager) IsRunning() bool {
	return m.started.Load() && !m.closed.Load()
}

// IsClosed returns true if the manager has been closed.
func (m *HandoffManager) IsClosed() bool {
	return m.closed.Load()
}

// GetStatus returns the current status of the manager.
func (m *HandoffManager) GetStatus() HandoffStatus {
	return HandoffStatus(m.status.Load())
}

// =============================================================================
// Context Management
// =============================================================================

// UpdateContext adds new messages to the prepared context.
// This maintains the rolling context for potential handoffs.
func (m *HandoffManager) UpdateContext(messages []Message) {
	if m.closed.Load() {
		return
	}

	m.contextMu.Lock()
	defer m.contextMu.Unlock()

	for _, msg := range messages {
		m.preparedContext.AddMessage(msg)
	}
}

// AddMessage adds a single message to the prepared context.
func (m *HandoffManager) AddMessage(msg Message) {
	if m.closed.Load() {
		return
	}

	m.contextMu.Lock()
	defer m.contextMu.Unlock()

	m.preparedContext.AddMessage(msg)
}

// SetPreparedContext replaces the current prepared context.
func (m *HandoffManager) SetPreparedContext(ctx *PreparedContext) {
	if m.closed.Load() || ctx == nil {
		return
	}

	m.contextMu.Lock()
	defer m.contextMu.Unlock()

	// Create a copy via snapshot/restore
	snapshot := ctx.Snapshot()
	restored, err := FromSnapshot(snapshot)
	if err == nil {
		m.preparedContext = restored
	}
}

// GetPreparedContext returns a copy of the current prepared context.
func (m *HandoffManager) GetPreparedContext() *PreparedContext {
	m.contextMu.RLock()
	defer m.contextMu.RUnlock()

	if m.preparedContext == nil {
		return nil
	}
	// Create a copy via snapshot/restore
	snapshot := m.preparedContext.Snapshot()
	restored, err := FromSnapshot(snapshot)
	if err != nil {
		return nil
	}
	return restored
}

// =============================================================================
// Evaluation and Execution
// =============================================================================

// EvaluateAndExecute evaluates whether a handoff should occur and executes it if triggered.
// Returns the HandoffResult if a handoff was executed, nil if no handoff was triggered.
func (m *HandoffManager) EvaluateAndExecute(ctx context.Context) *HandoffResult {
	if m.closed.Load() {
		return &HandoffResult{
			Success:   false,
			Error:     ErrManagerClosed,
			Timestamp: time.Now(),
		}
	}

	if ctx == nil {
		ctx = context.Background()
	}

	// Set status to evaluating
	m.status.Store(int32(StatusEvaluating))
	defer func() {
		if m.IsRunning() {
			m.status.Store(int32(StatusRunning))
		}
	}()

	m.evaluations.Add(1)
	m.mu.Lock()
	m.lastEvaluation = time.Now()
	m.mu.Unlock()

	// Get prepared context
	m.contextMu.RLock()
	preparedCtx := m.preparedContext
	if preparedCtx == nil {
		m.contextMu.RUnlock()
		return nil
	}
	// Create a copy via snapshot
	snapshot := preparedCtx.Snapshot()
	m.contextMu.RUnlock()

	preparedCtxCopy, err := FromSnapshot(snapshot)
	if err != nil {
		return nil
	}

	// Create context state for evaluation
	contextState := &ContextState{
		ContextSize:    preparedCtxCopy.TokenCount(),
		MaxContextSize: 100000, // Use a reasonable default
		TokenCount:     preparedCtxCopy.TokenCount(),
		TurnNumber:     len(preparedCtxCopy.RecentMessages()),
	}

	// Evaluate using controller
	decision := m.controller.EvaluateHandoff(contextState)

	if decision == nil || !decision.ShouldHandoff {
		// No handoff needed
		return nil
	}

	// Execute handoff
	return m.executeHandoff(ctx, decision, preparedCtxCopy)
}

// ForceHandoff forces an immediate handoff regardless of evaluation.
// Returns the HandoffResult from the forced handoff.
func (m *HandoffManager) ForceHandoff(ctx context.Context, reason string) *HandoffResult {
	if m.closed.Load() {
		return &HandoffResult{
			Success:   false,
			Error:     ErrManagerClosed,
			Timestamp: time.Now(),
		}
	}

	if ctx == nil {
		ctx = context.Background()
	}

	// Get prepared context
	m.contextMu.RLock()
	preparedCtx := m.preparedContext
	if preparedCtx == nil {
		m.contextMu.RUnlock()
		return &HandoffResult{
			Success:   false,
			Error:     ErrNoContextPrepared,
			Timestamp: time.Now(),
		}
	}
	// Create a copy via snapshot
	snapshot := preparedCtx.Snapshot()
	m.contextMu.RUnlock()

	preparedCtxCopy, err := FromSnapshot(snapshot)
	if err != nil {
		return &HandoffResult{
			Success:   false,
			Error:     err,
			Timestamp: time.Now(),
		}
	}

	// Create forced decision
	decision := NewHandoffDecision(
		true,
		1.0, // Maximum confidence for forced handoff
		"Forced handoff: "+reason,
		TriggerUserRequest,
		DecisionFactors{
			ContextUtilization: 1.0, // Force indicates full utilization
		},
	)

	m.handoffsForced.Add(1)

	return m.executeHandoff(ctx, decision, preparedCtxCopy)
}

// executeHandoff performs the actual handoff execution.
func (m *HandoffManager) executeHandoff(
	ctx context.Context,
	decision *HandoffDecision,
	preparedCtx *PreparedContext,
) *HandoffResult {
	// Set status to executing
	m.status.Store(int32(StatusExecuting))
	defer func() {
		if m.IsRunning() {
			m.status.Store(int32(StatusRunning))
		}
	}()

	// Prepare transfer
	transfer, err := m.executor.PrepareTransfer(decision, preparedCtx)
	if err != nil {
		result := &HandoffResult{
			Success:   false,
			Error:     err,
			Timestamp: time.Now(),
		}
		m.recordResult(result)
		return result
	}

	// Execute with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, m.config.DefaultTimeout)
	defer cancel()

	result := m.executor.ExecuteHandoff(timeoutCtx, transfer)

	// Record result
	m.recordResult(result)

	// Update statistics
	m.handoffsExecuted.Add(1)
	if !result.Success {
		m.handoffsFailed.Add(1)
	}

	m.mu.Lock()
	m.lastHandoff = time.Now()
	m.mu.Unlock()

	// Learn from outcome if enabled
	if m.config.EnableLearning && m.learner != nil {
		m.learnFromOutcome(result, preparedCtx)
	}

	return result
}

// recordResult records the result for monitoring.
func (m *HandoffManager) recordResult(result *HandoffResult) {
	if result == nil {
		return
	}
	m.recentResults.Push(result)
}

// learnFromOutcome updates the learner based on handoff outcome.
func (m *HandoffManager) learnFromOutcome(result *HandoffResult, preparedCtx *PreparedContext) {
	if m.learner == nil || result == nil {
		return
	}

	contextSize := 0
	turnNumber := 0
	if preparedCtx != nil {
		contextSize = preparedCtx.TokenCount()
		turnNumber = len(preparedCtx.RecentMessages())
	}

	quality := 0.0
	if result.Success {
		quality = 1.0
	}

	m.learner.RecordHandoffOutcome(
		m.config.AgentID,
		m.config.ModelName,
		contextSize,
		turnNumber,
		result.Success,
		quality,
	)
}

// =============================================================================
// Background Loop
// =============================================================================

// backgroundLoop runs periodic evaluation and maintenance.
func (m *HandoffManager) backgroundLoop() {
	defer close(m.doneCh)

	if !m.config.EnableAutoEvaluation || m.config.EvaluationInterval <= 0 {
		// No automatic evaluation, just wait for stop signal
		<-m.stopCh
		return
	}

	ticker := time.NewTicker(m.config.EvaluationInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.runPeriodicEvaluation()
		}
	}
}

// runPeriodicEvaluation runs a single periodic evaluation.
func (m *HandoffManager) runPeriodicEvaluation() {
	// Check minimum interval
	m.mu.RLock()
	timeSinceLastEval := time.Since(m.lastEvaluation)
	m.mu.RUnlock()

	if timeSinceLastEval < m.config.MinEvaluationInterval {
		return
	}

	// Run evaluation
	ctx, cancel := context.WithTimeout(context.Background(), m.config.DefaultTimeout)
	defer cancel()

	m.EvaluateAndExecute(ctx)
}

// =============================================================================
// Status and Statistics
// =============================================================================

// HandoffManagerStats contains statistics about the manager.
type HandoffManagerStats struct {
	// Status is the current manager status.
	Status HandoffStatus `json:"status"`

	// Evaluations is the total number of evaluations performed.
	Evaluations int64 `json:"evaluations"`

	// HandoffsExecuted is the total number of handoffs executed.
	HandoffsExecuted int64 `json:"handoffs_executed"`

	// HandoffsForced is the number of forced handoffs.
	HandoffsForced int64 `json:"handoffs_forced"`

	// HandoffsFailed is the number of failed handoffs.
	HandoffsFailed int64 `json:"handoffs_failed"`

	// SuccessRate is the success rate of handoffs.
	SuccessRate float64 `json:"success_rate"`

	// LastEvaluation is when the last evaluation occurred.
	LastEvaluation time.Time `json:"last_evaluation"`

	// LastHandoff is when the last handoff occurred.
	LastHandoff time.Time `json:"last_handoff"`

	// IsRunning indicates if the manager is running.
	IsRunning bool `json:"is_running"`

	// IsClosed indicates if the manager is closed.
	IsClosed bool `json:"is_closed"`

	// ControllerStats contains controller statistics.
	ControllerStats *ControllerStats `json:"controller_stats,omitempty"`

	// ExecutorStats contains executor statistics.
	ExecutorStats *ExecutorStats `json:"executor_stats,omitempty"`

	// LearnerStats contains learner statistics.
	LearnerStats *ProfileLearnerStats `json:"learner_stats,omitempty"`
}

// Stats returns statistics about the manager.
func (m *HandoffManager) Stats() HandoffManagerStats {
	m.mu.RLock()
	lastEval := m.lastEvaluation
	lastHandoff := m.lastHandoff
	m.mu.RUnlock()

	executed := m.handoffsExecuted.Load()
	failed := m.handoffsFailed.Load()

	successRate := 0.0
	if executed > 0 {
		successRate = float64(executed-failed) / float64(executed)
	}

	stats := HandoffManagerStats{
		Status:           HandoffStatus(m.status.Load()),
		Evaluations:      m.evaluations.Load(),
		HandoffsExecuted: executed,
		HandoffsForced:   m.handoffsForced.Load(),
		HandoffsFailed:   failed,
		SuccessRate:      successRate,
		LastEvaluation:   lastEval,
		LastHandoff:      lastHandoff,
		IsRunning:        m.IsRunning(),
		IsClosed:         m.IsClosed(),
	}

	// Add component statistics
	if m.controller != nil {
		controllerStats := m.controller.GetStats()
		stats.ControllerStats = &controllerStats
	}
	if m.executor != nil {
		executorStats := m.executor.Stats()
		stats.ExecutorStats = &executorStats
	}
	if m.learner != nil {
		learnerStats := m.learner.Stats()
		stats.LearnerStats = &learnerStats
	}

	return stats
}

// RecentResults returns the most recent handoff results.
func (m *HandoffManager) RecentResults(n int) []*HandoffResult {
	if n <= 0 {
		return nil
	}
	return m.recentResults.RecentN(n)
}

// =============================================================================
// Configuration
// =============================================================================

// GetConfig returns a copy of the current configuration.
func (m *HandoffManager) GetConfig() *HandoffManagerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.Clone()
}

// SetConfig updates the configuration.
// Note: Some changes may not take effect until the manager is restarted.
func (m *HandoffManager) SetConfig(config *HandoffManagerConfig) {
	if config == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = config.Clone()
}

// =============================================================================
// Component Access
// =============================================================================

// GetController returns the HandoffController for direct access.
func (m *HandoffManager) GetController() *HandoffController {
	return m.controller
}

// GetExecutor returns the HandoffExecutor for direct access.
func (m *HandoffManager) GetExecutor() *HandoffExecutor {
	return m.executor
}

// GetLearner returns the ProfileLearner for direct access.
func (m *HandoffManager) GetLearner() *ProfileLearner {
	return m.learner
}

// =============================================================================
// JSON Serialization
// =============================================================================

// handoffManagerJSON is used for JSON marshaling/unmarshaling.
type handoffManagerJSON struct {
	Config          *HandoffManagerConfig `json:"config"`
	PreparedContext *PreparedContext      `json:"prepared_context,omitempty"`
	LastEvaluation  string                `json:"last_evaluation"`
	LastHandoff     string                `json:"last_handoff"`
	Evaluations     int64                 `json:"evaluations"`
	Executed        int64                 `json:"executed"`
	Forced          int64                 `json:"forced"`
	Failed          int64                 `json:"failed"`
	Status          string                `json:"status"`
}

// MarshalJSON implements json.Marshaler.
func (m *HandoffManager) MarshalJSON() ([]byte, error) {
	m.mu.RLock()
	lastEval := m.lastEvaluation
	lastHandoff := m.lastHandoff
	config := m.config
	m.mu.RUnlock()

	m.contextMu.RLock()
	preparedCtx := m.preparedContext
	m.contextMu.RUnlock()

	return json.Marshal(handoffManagerJSON{
		Config:          config,
		PreparedContext: preparedCtx,
		LastEvaluation:  lastEval.Format(time.RFC3339Nano),
		LastHandoff:     lastHandoff.Format(time.RFC3339Nano),
		Evaluations:     m.evaluations.Load(),
		Executed:        m.handoffsExecuted.Load(),
		Forced:          m.handoffsForced.Load(),
		Failed:          m.handoffsFailed.Load(),
		Status:          HandoffStatus(m.status.Load()).String(),
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (m *HandoffManager) UnmarshalJSON(data []byte) error {
	var temp handoffManagerJSON
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if temp.Config != nil {
		m.config = temp.Config
	}

	if temp.LastEvaluation != "" {
		if t, err := time.Parse(time.RFC3339Nano, temp.LastEvaluation); err == nil {
			m.lastEvaluation = t
		}
	}
	if temp.LastHandoff != "" {
		if t, err := time.Parse(time.RFC3339Nano, temp.LastHandoff); err == nil {
			m.lastHandoff = t
		}
	}

	m.evaluations.Store(temp.Evaluations)
	m.handoffsExecuted.Store(temp.Executed)
	m.handoffsForced.Store(temp.Forced)
	m.handoffsFailed.Store(temp.Failed)

	m.contextMu.Lock()
	if temp.PreparedContext != nil {
		m.preparedContext = temp.PreparedContext
	}
	m.contextMu.Unlock()

	return nil
}
