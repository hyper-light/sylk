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
// HO.11.1 PipelineHandoffAdapter - Integration with Pipeline Components
// =============================================================================
//
// PipelineHandoffAdapter wraps HandoffManager to provide a pipeline-compatible
// interface for triggering handoffs. It bridges the core handoff logic with
// the pipeline orchestration layer.
//
// Key features:
//   - Implements PipelineHandoffTrigger interface for pipeline integration
//   - Delegates to HandoffManager for actual handoff execution
//   - Maintains backward compatibility with existing pipeline flow
//   - Non-blocking operations with configurable timeouts
//   - Metrics collection for pipeline handoff operations
//
// Thread Safety:
//   - All operations are protected by appropriate synchronization
//   - Safe for concurrent use from multiple goroutines
//
// Example usage:
//
//	manager := NewHandoffManager(config, controller, executor, learner)
//	adapter := NewPipelineHandoffAdapter(manager, adapterConfig)
//	adapter.Start()
//	defer adapter.Stop()
//
//	if adapter.ShouldHandoff(agentID, contextUsage) {
//	    result, err := adapter.TriggerHandoff(ctx, request)
//	}

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrAdapterClosed indicates the adapter has been closed.
	ErrAdapterClosed = errors.New("pipeline handoff adapter is closed")

	// ErrAdapterNotStarted indicates the adapter has not been started.
	ErrAdapterNotStarted = errors.New("pipeline handoff adapter not started")

	// ErrNilManager indicates a nil HandoffManager was provided.
	ErrNilManager = errors.New("nil handoff manager provided")

	// ErrInvalidRequest indicates an invalid handoff request.
	ErrInvalidRequest = errors.New("invalid handoff request")

	// ErrHandoffInProgress indicates a handoff is already in progress for the agent.
	ErrHandoffInProgress = errors.New("handoff already in progress for agent")
)

// =============================================================================
// PipelineHandoffRequest - Pipeline-specific Handoff Request
// =============================================================================

// PipelineHandoffRequest represents a handoff request from the pipeline.
type PipelineHandoffRequest struct {
	// AgentID is the identifier of the agent requesting handoff.
	AgentID string `json:"agent_id"`

	// AgentType is the type of the agent (e.g., "engineer", "designer").
	AgentType string `json:"agent_type"`

	// SessionID is the session identifier.
	SessionID string `json:"session_id"`

	// PipelineID is the pipeline identifier.
	PipelineID string `json:"pipeline_id"`

	// HandoffIndex is how many handoffs this agent has had.
	HandoffIndex int `json:"handoff_index"`

	// TriggerReason describes why the handoff was triggered.
	TriggerReason string `json:"trigger_reason"`

	// ContextUsage is the current context utilization (0-1).
	ContextUsage float64 `json:"context_usage"`

	// State contains agent-specific handoff state.
	State interface{} `json:"state,omitempty"`

	// Metadata contains additional request metadata.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Validate checks if the request is valid.
func (r *PipelineHandoffRequest) Validate() error {
	if r.AgentID == "" {
		return errors.New("agent_id is required")
	}
	if r.AgentType == "" {
		return errors.New("agent_type is required")
	}
	if r.SessionID == "" {
		return errors.New("session_id is required")
	}
	return nil
}

// =============================================================================
// PipelineHandoffResult - Pipeline-specific Handoff Result
// =============================================================================

// PipelineHandoffResult represents the result of a pipeline handoff.
type PipelineHandoffResult struct {
	// Success indicates whether the handoff succeeded.
	Success bool `json:"success"`

	// NewAgentID is the ID of the newly created agent instance.
	NewAgentID string `json:"new_agent_id,omitempty"`

	// OldAgentID is the ID of the agent that was handed off.
	OldAgentID string `json:"old_agent_id"`

	// HandoffID is the unique identifier for this handoff.
	HandoffID string `json:"handoff_id"`

	// Duration is how long the handoff took.
	Duration time.Duration `json:"duration"`

	// Error is the error if the handoff failed.
	Error error `json:"-"`

	// ErrorMessage is the string representation of the error.
	ErrorMessage string `json:"error_message,omitempty"`

	// Trigger indicates what triggered the handoff.
	Trigger HandoffTrigger `json:"trigger"`

	// Confidence is the decision confidence (0-1).
	Confidence float64 `json:"confidence"`

	// Metrics contains performance metrics.
	Metrics PipelineHandoffMetrics `json:"metrics"`
}

// PipelineHandoffMetrics contains metrics for pipeline handoffs.
type PipelineHandoffMetrics struct {
	// EvaluationDuration is how long decision evaluation took.
	EvaluationDuration time.Duration `json:"evaluation_duration"`

	// ExecutionDuration is how long execution took.
	ExecutionDuration time.Duration `json:"execution_duration"`

	// TokensTransferred is the number of tokens transferred.
	TokensTransferred int `json:"tokens_transferred"`

	// ContextUtilization is the context utilization at handoff time.
	ContextUtilization float64 `json:"context_utilization"`

	// PredictedQuality is the GP-predicted quality at handoff time.
	PredictedQuality float64 `json:"predicted_quality"`
}

// =============================================================================
// PipelineHandoffTrigger - Interface for Pipeline Integration
// =============================================================================

// PipelineHandoffTrigger defines the interface for triggering handoffs from pipelines.
type PipelineHandoffTrigger interface {
	// ShouldHandoff determines if an agent should trigger a handoff.
	ShouldHandoff(agentID string, contextUsage float64) bool

	// TriggerHandoff executes a handoff for the specified agent.
	TriggerHandoff(ctx context.Context, req *PipelineHandoffRequest) (*PipelineHandoffResult, error)
}

// =============================================================================
// PipelineHandoffAdapterConfig - Adapter Configuration
// =============================================================================

// PipelineHandoffAdapterConfig configures the PipelineHandoffAdapter.
type PipelineHandoffAdapterConfig struct {
	// DefaultTimeout is the default timeout for handoff operations.
	DefaultTimeout time.Duration `json:"default_timeout"`

	// ContextThreshold is the context utilization threshold for triggering handoffs.
	ContextThreshold float64 `json:"context_threshold"`

	// UseGPPrediction enables GP-based quality prediction for decisions.
	UseGPPrediction bool `json:"use_gp_prediction"`

	// QualityThreshold is the minimum predicted quality before triggering handoff.
	QualityThreshold float64 `json:"quality_threshold"`

	// NonBlocking enables non-blocking handoff execution.
	NonBlocking bool `json:"non_blocking"`

	// MaxConcurrentHandoffs limits concurrent handoff operations.
	MaxConcurrentHandoffs int `json:"max_concurrent_handoffs"`

	// MetricsEnabled enables metrics collection.
	MetricsEnabled bool `json:"metrics_enabled"`
}

// DefaultPipelineHandoffAdapterConfig returns sensible defaults.
func DefaultPipelineHandoffAdapterConfig() *PipelineHandoffAdapterConfig {
	return &PipelineHandoffAdapterConfig{
		DefaultTimeout:        60 * time.Second,
		ContextThreshold:      0.75,
		UseGPPrediction:       true,
		QualityThreshold:      0.5,
		NonBlocking:           false,
		MaxConcurrentHandoffs: 10,
		MetricsEnabled:        true,
	}
}

// Clone creates a deep copy of the config.
func (c *PipelineHandoffAdapterConfig) Clone() *PipelineHandoffAdapterConfig {
	if c == nil {
		return nil
	}
	return &PipelineHandoffAdapterConfig{
		DefaultTimeout:        c.DefaultTimeout,
		ContextThreshold:      c.ContextThreshold,
		UseGPPrediction:       c.UseGPPrediction,
		QualityThreshold:      c.QualityThreshold,
		NonBlocking:           c.NonBlocking,
		MaxConcurrentHandoffs: c.MaxConcurrentHandoffs,
		MetricsEnabled:        c.MetricsEnabled,
	}
}

// =============================================================================
// PipelineHandoffAdapter
// =============================================================================

// PipelineHandoffAdapter adapts HandoffManager for pipeline integration.
type PipelineHandoffAdapter struct {
	mu sync.RWMutex

	// manager is the underlying HandoffManager.
	manager *HandoffManager

	// config holds adapter configuration.
	config *PipelineHandoffAdapterConfig

	// activeHandoffs tracks handoffs in progress by agent ID.
	activeHandoffs map[string]*activeHandoffInfo

	// concurrencySem limits concurrent handoffs.
	concurrencySem chan struct{}

	// statistics
	stats adapterStatsInternal

	// lifecycle
	started atomic.Bool
	closed  atomic.Bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// activeHandoffInfo tracks an in-progress handoff.
type activeHandoffInfo struct {
	Request   *PipelineHandoffRequest
	StartTime time.Time
	Done      chan *PipelineHandoffResult
}

// adapterStatsInternal holds internal statistics.
type adapterStatsInternal struct {
	totalRequests    atomic.Int64
	successfulHands  atomic.Int64
	failedHandoffs   atomic.Int64
	skippedHandoffs  atomic.Int64
	totalDurationNs  atomic.Int64
	gpEvaluations    atomic.Int64
	thresholdTrigger atomic.Int64
	qualityTriggers  atomic.Int64
}

// NewPipelineHandoffAdapter creates a new PipelineHandoffAdapter.
func NewPipelineHandoffAdapter(
	manager *HandoffManager,
	config *PipelineHandoffAdapterConfig,
) *PipelineHandoffAdapter {
	if config == nil {
		config = DefaultPipelineHandoffAdapterConfig()
	}

	maxConcurrent := config.MaxConcurrentHandoffs
	if maxConcurrent <= 0 {
		maxConcurrent = 10
	}

	return &PipelineHandoffAdapter{
		manager:        manager,
		config:         config.Clone(),
		activeHandoffs: make(map[string]*activeHandoffInfo),
		concurrencySem: make(chan struct{}, maxConcurrent),
		stopCh:         make(chan struct{}),
		doneCh:         make(chan struct{}),
	}
}

// =============================================================================
// Lifecycle Management
// =============================================================================

// Start begins the adapter's operations.
func (a *PipelineHandoffAdapter) Start() error {
	if a.closed.Load() {
		return ErrAdapterClosed
	}

	if a.manager == nil {
		return ErrNilManager
	}

	if a.started.Swap(true) {
		return nil // Already started
	}

	// Ensure manager is started
	if !a.manager.IsRunning() {
		if err := a.manager.Start(); err != nil {
			a.started.Store(false)
			return err
		}
	}

	return nil
}

// Stop gracefully stops the adapter.
func (a *PipelineHandoffAdapter) Stop() error {
	if a.closed.Swap(true) {
		return ErrAdapterClosed
	}

	if !a.started.Load() {
		close(a.doneCh)
		return nil
	}

	close(a.stopCh)

	// Wait for active handoffs to complete with timeout
	timeout := time.After(30 * time.Second)
	for {
		a.mu.RLock()
		active := len(a.activeHandoffs)
		a.mu.RUnlock()

		if active == 0 {
			break
		}

		select {
		case <-timeout:
			// Force close after timeout
			break
		case <-time.After(100 * time.Millisecond):
			continue
		}
		break
	}

	close(a.doneCh)
	return nil
}

// IsRunning returns true if the adapter is running.
func (a *PipelineHandoffAdapter) IsRunning() bool {
	return a.started.Load() && !a.closed.Load()
}

// IsClosed returns true if the adapter is closed.
func (a *PipelineHandoffAdapter) IsClosed() bool {
	return a.closed.Load()
}

// =============================================================================
// PipelineHandoffTrigger Implementation
// =============================================================================

// ShouldHandoff determines if an agent should trigger a handoff.
func (a *PipelineHandoffAdapter) ShouldHandoff(agentID string, contextUsage float64) bool {
	if a.closed.Load() || a.manager == nil {
		return false
	}

	a.mu.RLock()
	config := a.config
	a.mu.RUnlock()

	// Check simple threshold first
	if contextUsage >= config.ContextThreshold {
		a.stats.thresholdTrigger.Add(1)
		return true
	}

	// Use GP prediction if enabled
	if config.UseGPPrediction && a.manager.GetController() != nil {
		a.stats.gpEvaluations.Add(1)

		contextState := &ContextState{
			ContextSize:    int(contextUsage * 100000), // Approximate token count
			MaxContextSize: 100000,
			TokenCount:     0,
			TurnNumber:     0,
		}

		decision := a.manager.GetController().EvaluateHandoff(contextState)
		if decision != nil && decision.ShouldHandoff {
			if decision.Trigger == TriggerQualityDegrading {
				a.stats.qualityTriggers.Add(1)
			}
			return true
		}
	}

	return false
}

// TriggerHandoff executes a handoff for the specified agent.
func (a *PipelineHandoffAdapter) TriggerHandoff(
	ctx context.Context,
	req *PipelineHandoffRequest,
) (*PipelineHandoffResult, error) {
	startTime := time.Now()

	// Validate state
	if a.closed.Load() {
		return a.failedResult(req, startTime, ErrAdapterClosed), ErrAdapterClosed
	}

	if !a.started.Load() {
		return a.failedResult(req, startTime, ErrAdapterNotStarted), ErrAdapterNotStarted
	}

	if a.manager == nil {
		return a.failedResult(req, startTime, ErrNilManager), ErrNilManager
	}

	// Validate request
	if req == nil {
		return a.failedResult(req, startTime, ErrInvalidRequest), ErrInvalidRequest
	}

	if err := req.Validate(); err != nil {
		return a.failedResult(req, startTime, err), err
	}

	a.stats.totalRequests.Add(1)

	// Check for duplicate handoff
	if err := a.registerHandoff(req); err != nil {
		a.stats.skippedHandoffs.Add(1)
		return a.failedResult(req, startTime, err), err
	}
	defer a.unregisterHandoff(req.AgentID)

	// Acquire concurrency semaphore
	select {
	case a.concurrencySem <- struct{}{}:
		defer func() { <-a.concurrencySem }()
	case <-ctx.Done():
		return a.failedResult(req, startTime, ctx.Err()), ctx.Err()
	}

	// Execute handoff
	result := a.executeHandoff(ctx, req, startTime)

	// Update statistics
	if result.Success {
		a.stats.successfulHands.Add(1)
	} else {
		a.stats.failedHandoffs.Add(1)
	}
	a.stats.totalDurationNs.Add(int64(result.Duration))

	return result, result.Error
}

// =============================================================================
// Internal Methods
// =============================================================================

// registerHandoff registers a handoff in progress.
func (a *PipelineHandoffAdapter) registerHandoff(req *PipelineHandoffRequest) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, exists := a.activeHandoffs[req.AgentID]; exists {
		return ErrHandoffInProgress
	}

	a.activeHandoffs[req.AgentID] = &activeHandoffInfo{
		Request:   req,
		StartTime: time.Now(),
		Done:      make(chan *PipelineHandoffResult, 1),
	}

	return nil
}

// unregisterHandoff removes a handoff from active tracking.
func (a *PipelineHandoffAdapter) unregisterHandoff(agentID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.activeHandoffs, agentID)
}

// executeHandoff performs the actual handoff.
func (a *PipelineHandoffAdapter) executeHandoff(
	ctx context.Context,
	req *PipelineHandoffRequest,
	startTime time.Time,
) *PipelineHandoffResult {
	a.mu.RLock()
	config := a.config
	a.mu.RUnlock()

	// Apply timeout
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, config.DefaultTimeout)
		defer cancel()
	}

	// Prepare context for manager
	preparedCtx := a.buildPreparedContext(req)
	a.manager.SetPreparedContext(preparedCtx)

	// Evaluate and execute through manager
	evalStart := time.Now()
	managerResult := a.manager.EvaluateAndExecute(ctx)
	evalDuration := time.Since(evalStart)

	// If no handoff was triggered by evaluation, force it
	if managerResult == nil {
		managerResult = a.manager.ForceHandoff(ctx, req.TriggerReason)
	}

	// Build pipeline result
	return a.buildPipelineResult(req, managerResult, startTime, evalDuration)
}

// buildPreparedContext creates a PreparedContext from the pipeline request.
func (a *PipelineHandoffAdapter) buildPreparedContext(req *PipelineHandoffRequest) *PreparedContext {
	ctx := NewPreparedContextDefault()

	// Set metadata
	ctx.SetMetadata("agent_id", req.AgentID)
	ctx.SetMetadata("agent_type", req.AgentType)
	ctx.SetMetadata("session_id", req.SessionID)
	ctx.SetMetadata("pipeline_id", req.PipelineID)

	for k, v := range req.Metadata {
		ctx.SetMetadata(k, v)
	}

	return ctx
}

// buildPipelineResult creates a PipelineHandoffResult from manager result.
func (a *PipelineHandoffAdapter) buildPipelineResult(
	req *PipelineHandoffRequest,
	managerResult *HandoffResult,
	startTime time.Time,
	evalDuration time.Duration,
) *PipelineHandoffResult {
	result := &PipelineHandoffResult{
		OldAgentID: req.AgentID,
		Duration:   time.Since(startTime),
		Metrics: PipelineHandoffMetrics{
			EvaluationDuration: evalDuration,
			ContextUtilization: req.ContextUsage,
		},
	}

	if managerResult == nil {
		result.Success = false
		result.Error = errors.New("manager returned nil result")
		result.ErrorMessage = result.Error.Error()
		return result
	}

	result.Success = managerResult.Success
	result.NewAgentID = managerResult.NewSessionID
	result.Error = managerResult.Error
	if result.Error != nil {
		result.ErrorMessage = result.Error.Error()
	}

	// Extract transfer information
	if managerResult.TransferredContext != nil {
		result.HandoffID = managerResult.TransferredContext.ID
		result.Metrics.TokensTransferred = managerResult.TransferredContext.TokenCount

		if managerResult.TransferredContext.Decision != nil {
			result.Trigger = managerResult.TransferredContext.Decision.Trigger
			result.Confidence = managerResult.TransferredContext.Decision.Confidence
			result.Metrics.PredictedQuality = managerResult.TransferredContext.Decision.Factors.PredictedQuality
		}
	}

	result.Metrics.ExecutionDuration = result.Duration - evalDuration

	return result
}

// failedResult creates a failed result.
func (a *PipelineHandoffAdapter) failedResult(
	req *PipelineHandoffRequest,
	startTime time.Time,
	err error,
) *PipelineHandoffResult {
	agentID := ""
	if req != nil {
		agentID = req.AgentID
	}

	return &PipelineHandoffResult{
		Success:      false,
		OldAgentID:   agentID,
		Duration:     time.Since(startTime),
		Error:        err,
		ErrorMessage: err.Error(),
	}
}

// =============================================================================
// Statistics and Getters
// =============================================================================

// PipelineAdapterStats contains statistics about the adapter.
type PipelineAdapterStats struct {
	TotalRequests       int64         `json:"total_requests"`
	SuccessfulHandoffs  int64         `json:"successful_handoffs"`
	FailedHandoffs      int64         `json:"failed_handoffs"`
	SkippedHandoffs     int64         `json:"skipped_handoffs"`
	ActiveHandoffs      int           `json:"active_handoffs"`
	TotalDuration       time.Duration `json:"total_duration"`
	AverageDuration     time.Duration `json:"average_duration"`
	SuccessRate         float64       `json:"success_rate"`
	GPEvaluations       int64         `json:"gp_evaluations"`
	ThresholdTriggers   int64         `json:"threshold_triggers"`
	QualityTriggers     int64         `json:"quality_triggers"`
	IsRunning           bool          `json:"is_running"`
	IsClosed            bool          `json:"is_closed"`
}

// Stats returns statistics about the adapter.
func (a *PipelineHandoffAdapter) Stats() PipelineAdapterStats {
	a.mu.RLock()
	activeCount := len(a.activeHandoffs)
	a.mu.RUnlock()

	total := a.stats.totalRequests.Load()
	successful := a.stats.successfulHands.Load()
	totalDuration := time.Duration(a.stats.totalDurationNs.Load())

	successRate := 0.0
	averageDuration := time.Duration(0)
	if total > 0 {
		successRate = float64(successful) / float64(total)
		if successful > 0 {
			averageDuration = totalDuration / time.Duration(successful)
		}
	}

	return PipelineAdapterStats{
		TotalRequests:      total,
		SuccessfulHandoffs: successful,
		FailedHandoffs:     a.stats.failedHandoffs.Load(),
		SkippedHandoffs:    a.stats.skippedHandoffs.Load(),
		ActiveHandoffs:     activeCount,
		TotalDuration:      totalDuration,
		AverageDuration:    averageDuration,
		SuccessRate:        successRate,
		GPEvaluations:      a.stats.gpEvaluations.Load(),
		ThresholdTriggers:  a.stats.thresholdTrigger.Load(),
		QualityTriggers:    a.stats.qualityTriggers.Load(),
		IsRunning:          a.IsRunning(),
		IsClosed:           a.IsClosed(),
	}
}

// GetManager returns the underlying HandoffManager.
func (a *PipelineHandoffAdapter) GetManager() *HandoffManager {
	return a.manager
}

// GetConfig returns a copy of the current configuration.
func (a *PipelineHandoffAdapter) GetConfig() *PipelineHandoffAdapterConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.Clone()
}

// SetConfig updates the adapter configuration.
func (a *PipelineHandoffAdapter) SetConfig(config *PipelineHandoffAdapterConfig) {
	if config == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.config = config.Clone()
}

// GetActiveHandoffs returns information about active handoffs.
func (a *PipelineHandoffAdapter) GetActiveHandoffs() map[string]time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make(map[string]time.Time, len(a.activeHandoffs))
	for agentID, info := range a.activeHandoffs {
		result[agentID] = info.StartTime
	}
	return result
}

// =============================================================================
// JSON Serialization
// =============================================================================

// adapterJSON is used for JSON marshaling.
type adapterJSON struct {
	Config *PipelineHandoffAdapterConfig `json:"config"`
	Stats  PipelineAdapterStats          `json:"stats"`
}

// MarshalJSON implements json.Marshaler.
func (a *PipelineHandoffAdapter) MarshalJSON() ([]byte, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return json.Marshal(adapterJSON{
		Config: a.config,
		Stats:  a.Stats(),
	})
}
