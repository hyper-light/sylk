package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// HO.8.1 HandoffExecutor - Executes Handoff Operations
// =============================================================================
//
// HandoffExecutor is responsible for executing handoff operations between
// agent sessions. It validates context before transfer, handles rollback on
// failure, and provides hooks for pre/post handoff callbacks.
//
// Thread Safety:
//   - All operations are protected by appropriate synchronization
//   - Safe for concurrent use from multiple goroutines
//
// Example usage:
//
//	executor := NewHandoffExecutor(sessionCreator, config)
//	executor.SetHooks(hooks)
//	transfer := executor.PrepareTransfer(decision, context)
//	if err := executor.ValidateTransfer(transfer); err != nil {
//	    // Handle validation error
//	}
//	result := executor.ExecuteTransfer(ctx, transfer)
//	executor.RecordOutcome(result)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrTransferValidationFailed indicates the transfer failed validation.
	ErrTransferValidationFailed = errors.New("transfer validation failed")

	// ErrSessionCreationFailed indicates new session could not be created.
	ErrSessionCreationFailed = errors.New("session creation failed")

	// ErrContextTransferFailed indicates context transfer failed.
	ErrContextTransferFailed = errors.New("context transfer failed")

	// ErrHandoffTimeout indicates the handoff operation timed out.
	ErrHandoffTimeout = errors.New("handoff operation timed out")

	// ErrRollbackFailed indicates rollback after failure also failed.
	ErrRollbackFailed = errors.New("rollback failed")

	// ErrPreHookFailed indicates pre-handoff hook failed.
	ErrPreHookFailed = errors.New("pre-handoff hook failed")

	// ErrPostHookFailed indicates post-handoff hook failed.
	ErrPostHookFailed = errors.New("post-handoff hook failed")

	// ErrExecutorClosed indicates the executor has been closed.
	ErrExecutorClosed = errors.New("executor is closed")

	// ErrNilDecision indicates a nil decision was provided.
	ErrNilDecision = errors.New("nil decision provided")

	// ErrNilContext indicates a nil context was provided.
	ErrNilContext = errors.New("nil context provided")

	// ErrMaxRetriesExceeded indicates maximum retries were exceeded.
	ErrMaxRetriesExceeded = errors.New("maximum retries exceeded")
)

// =============================================================================
// SessionCreator Interface
// =============================================================================

// SessionCreator is an interface for creating new sessions during handoff.
type SessionCreator interface {
	// CreateSession creates a new session and returns its ID.
	CreateSession(ctx context.Context, config SessionConfig) (string, error)

	// TransferContext transfers context to the new session.
	TransferContext(ctx context.Context, sessionID string, transfer *ContextTransfer) error

	// ActivateSession activates the new session.
	ActivateSession(ctx context.Context, sessionID string) error

	// DeactivateSession deactivates a session (used for rollback).
	DeactivateSession(ctx context.Context, sessionID string) error

	// DeleteSession removes a session (used for rollback).
	DeleteSession(ctx context.Context, sessionID string) error
}

// SessionConfig holds configuration for creating a new session.
type SessionConfig struct {
	// Name is the human-readable name for the session.
	Name string `json:"name"`

	// Description provides context about the session.
	Description string `json:"description"`

	// ParentSessionID is the ID of the parent session (for lineage).
	ParentSessionID string `json:"parent_session_id"`

	// Metadata holds arbitrary key-value pairs.
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// =============================================================================
// ContextTransfer - What Gets Transferred
// =============================================================================

// ContextTransfer represents the data being transferred during a handoff.
type ContextTransfer struct {
	// ID is a unique identifier for this transfer.
	ID string `json:"id"`

	// SourceSessionID is the ID of the source session.
	SourceSessionID string `json:"source_session_id"`

	// Decision is the handoff decision that triggered this transfer.
	Decision *HandoffDecision `json:"decision"`

	// PreparedContext is the prepared context to transfer.
	PreparedContext *PreparedContext `json:"prepared_context,omitempty"`

	// Snapshot is a serialized snapshot if PreparedContext is large.
	Snapshot *PreparedContextSnapshot `json:"snapshot,omitempty"`

	// Summary is the conversation summary.
	Summary string `json:"summary"`

	// RecentMessages are the most recent messages.
	RecentMessages []Message `json:"recent_messages"`

	// ToolStates are the current tool states.
	ToolStates map[string]ToolState `json:"tool_states"`

	// KeyTopics are the extracted key topics.
	KeyTopics []string `json:"key_topics"`

	// Metadata contains transfer-specific metadata.
	Metadata map[string]string `json:"metadata,omitempty"`

	// TokenCount is the estimated token count.
	TokenCount int `json:"token_count"`

	// CreatedAt is when the transfer was created.
	CreatedAt time.Time `json:"created_at"`

	// Validated indicates if the transfer has been validated.
	Validated bool `json:"validated"`

	// ValidationErrors contains any validation errors.
	ValidationErrors []string `json:"validation_errors,omitempty"`
}

// NewContextTransfer creates a new ContextTransfer from a decision and prepared context.
func NewContextTransfer(
	sourceSessionID string,
	decision *HandoffDecision,
	preparedCtx *PreparedContext,
) *ContextTransfer {
	transfer := &ContextTransfer{
		ID:              generateTransferID(),
		SourceSessionID: sourceSessionID,
		Decision:        decision,
		PreparedContext: preparedCtx,
		Metadata:        make(map[string]string),
		CreatedAt:       time.Now(),
		Validated:       false,
	}

	// Extract data from prepared context
	if preparedCtx != nil {
		transfer.Summary = preparedCtx.Summary()
		transfer.RecentMessages = preparedCtx.RecentMessages()
		transfer.ToolStates = preparedCtx.ToolStates()
		transfer.KeyTopics = preparedCtx.KeyTopics()
		transfer.TokenCount = preparedCtx.TokenCount()
		transfer.Snapshot = snapshotPtr(preparedCtx.Snapshot())
	}

	return transfer
}

// snapshotPtr returns a pointer to the snapshot.
func snapshotPtr(s PreparedContextSnapshot) *PreparedContextSnapshot {
	return &s
}

// generateTransferID generates a unique transfer ID.
func generateTransferID() string {
	return fmt.Sprintf("transfer-%d-%d", time.Now().UnixNano(), time.Now().Nanosecond()%1000)
}

// =============================================================================
// HandoffHooks Interface
// =============================================================================

// HandoffHooks provides callbacks for handoff lifecycle events.
type HandoffHooks interface {
	// PreHandoff is called before the handoff begins.
	// If it returns an error, the handoff is aborted.
	PreHandoff(ctx context.Context, transfer *ContextTransfer) error

	// PostHandoff is called after the handoff completes (success or failure).
	PostHandoff(ctx context.Context, result *HandoffResult)

	// OnRollback is called when a rollback is performed.
	OnRollback(ctx context.Context, transfer *ContextTransfer, rollbackErr error)
}

// NoOpHooks provides a default no-op implementation of HandoffHooks.
type NoOpHooks struct{}

func (h *NoOpHooks) PreHandoff(ctx context.Context, transfer *ContextTransfer) error { return nil }
func (h *NoOpHooks) PostHandoff(ctx context.Context, result *HandoffResult)          {}
func (h *NoOpHooks) OnRollback(ctx context.Context, transfer *ContextTransfer, rollbackErr error) {
}

// =============================================================================
// HandoffResult - Outcome of a Handoff
// =============================================================================

// HandoffResult represents the outcome of a handoff operation.
type HandoffResult struct {
	// Success indicates whether the handoff succeeded.
	Success bool `json:"success"`

	// NewSessionID is the ID of the newly created session.
	NewSessionID string `json:"new_session_id,omitempty"`

	// TransferredContext contains what was transferred.
	TransferredContext *ContextTransfer `json:"transferred_context"`

	// Duration is how long the handoff took.
	Duration time.Duration `json:"duration"`

	// Error is the error if the handoff failed.
	Error error `json:"-"`

	// ErrorMessage is the string representation of the error.
	ErrorMessage string `json:"error_message,omitempty"`

	// RetryCount is how many retries were attempted.
	RetryCount int `json:"retry_count"`

	// RolledBack indicates if a rollback was performed.
	RolledBack bool `json:"rolled_back"`

	// RollbackError is any error from the rollback.
	RollbackError error `json:"-"`

	// RollbackErrorMessage is the string representation of rollback error.
	RollbackErrorMessage string `json:"rollback_error_message,omitempty"`

	// Timestamp is when the result was created.
	Timestamp time.Time `json:"timestamp"`

	// Metrics contains performance metrics.
	Metrics HandoffMetrics `json:"metrics"`
}

// HandoffMetrics contains performance metrics for a handoff.
type HandoffMetrics struct {
	// ValidationDuration is how long validation took.
	ValidationDuration time.Duration `json:"validation_duration"`

	// SessionCreationDuration is how long session creation took.
	SessionCreationDuration time.Duration `json:"session_creation_duration"`

	// ContextTransferDuration is how long context transfer took.
	ContextTransferDuration time.Duration `json:"context_transfer_duration"`

	// ActivationDuration is how long activation took.
	ActivationDuration time.Duration `json:"activation_duration"`

	// TokensTransferred is the number of tokens transferred.
	TokensTransferred int `json:"tokens_transferred"`

	// MessagesTransferred is the number of messages transferred.
	MessagesTransferred int `json:"messages_transferred"`

	// ToolStatesTransferred is the number of tool states transferred.
	ToolStatesTransferred int `json:"tool_states_transferred"`
}

// MarshalJSON implements json.Marshaler for HandoffResult.
func (hr *HandoffResult) MarshalJSON() ([]byte, error) {
	type Alias HandoffResult
	return json.Marshal(&struct {
		*Alias
		Duration string `json:"duration"`
	}{
		Alias:    (*Alias)(hr),
		Duration: hr.Duration.String(),
	})
}

// =============================================================================
// ExecutorConfig - Configuration for HandoffExecutor
// =============================================================================

// ExecutorConfig holds configuration for the HandoffExecutor.
type ExecutorConfig struct {
	// DefaultTimeout is the default timeout for handoff operations.
	DefaultTimeout time.Duration `json:"default_timeout"`

	// MaxRetries is the maximum number of retry attempts.
	MaxRetries int `json:"max_retries"`

	// RetryBaseDelay is the base delay for exponential backoff.
	RetryBaseDelay time.Duration `json:"retry_base_delay"`

	// RetryMaxDelay is the maximum delay between retries.
	RetryMaxDelay time.Duration `json:"retry_max_delay"`

	// RetryMultiplier is the multiplier for exponential backoff.
	RetryMultiplier float64 `json:"retry_multiplier"`

	// ValidationEnabled enables transfer validation.
	ValidationEnabled bool `json:"validation_enabled"`

	// MinTokenCount is the minimum token count for valid transfer.
	MinTokenCount int `json:"min_token_count"`

	// MaxTokenCount is the maximum token count for valid transfer.
	MaxTokenCount int `json:"max_token_count"`

	// RequireSummary requires a non-empty summary for valid transfer.
	RequireSummary bool `json:"require_summary"`

	// RequireMessages requires at least one message for valid transfer.
	RequireMessages bool `json:"require_messages"`

	// RollbackOnFailure enables automatic rollback on failure.
	RollbackOnFailure bool `json:"rollback_on_failure"`

	// CollectMetrics enables metrics collection.
	CollectMetrics bool `json:"collect_metrics"`
}

// DefaultExecutorConfig returns sensible defaults for the executor.
func DefaultExecutorConfig() *ExecutorConfig {
	return &ExecutorConfig{
		DefaultTimeout:    30 * time.Second,
		MaxRetries:        3,
		RetryBaseDelay:    100 * time.Millisecond,
		RetryMaxDelay:     5 * time.Second,
		RetryMultiplier:   2.0,
		ValidationEnabled: true,
		MinTokenCount:     0,
		MaxTokenCount:     1000000, // 1M tokens max
		RequireSummary:    false,
		RequireMessages:   false,
		RollbackOnFailure: true,
		CollectMetrics:    true,
	}
}

// Clone creates a deep copy of the config.
func (ec *ExecutorConfig) Clone() *ExecutorConfig {
	if ec == nil {
		return nil
	}
	return &ExecutorConfig{
		DefaultTimeout:    ec.DefaultTimeout,
		MaxRetries:        ec.MaxRetries,
		RetryBaseDelay:    ec.RetryBaseDelay,
		RetryMaxDelay:     ec.RetryMaxDelay,
		RetryMultiplier:   ec.RetryMultiplier,
		ValidationEnabled: ec.ValidationEnabled,
		MinTokenCount:     ec.MinTokenCount,
		MaxTokenCount:     ec.MaxTokenCount,
		RequireSummary:    ec.RequireSummary,
		RequireMessages:   ec.RequireMessages,
		RollbackOnFailure: ec.RollbackOnFailure,
		CollectMetrics:    ec.CollectMetrics,
	}
}

// =============================================================================
// ExecutorStats - Statistics for the Executor
// =============================================================================

// ExecutorStats contains statistics about executor operations.
type ExecutorStats struct {
	// TotalHandoffs is the total number of handoff attempts.
	TotalHandoffs int64 `json:"total_handoffs"`

	// SuccessfulHandoffs is the number of successful handoffs.
	SuccessfulHandoffs int64 `json:"successful_handoffs"`

	// FailedHandoffs is the number of failed handoffs.
	FailedHandoffs int64 `json:"failed_handoffs"`

	// TotalRetries is the total number of retries across all handoffs.
	TotalRetries int64 `json:"total_retries"`

	// TotalRollbacks is the number of rollbacks performed.
	TotalRollbacks int64 `json:"total_rollbacks"`

	// RollbackFailures is the number of failed rollbacks.
	RollbackFailures int64 `json:"rollback_failures"`

	// ValidationFailures is the number of validation failures.
	ValidationFailures int64 `json:"validation_failures"`

	// TotalDuration is the cumulative duration of all handoffs.
	TotalDuration time.Duration `json:"total_duration"`

	// AverageDuration is the average handoff duration.
	AverageDuration time.Duration `json:"average_duration"`

	// TotalTokensTransferred is the cumulative tokens transferred.
	TotalTokensTransferred int64 `json:"total_tokens_transferred"`

	// SuccessRate is the success rate (0-1).
	SuccessRate float64 `json:"success_rate"`
}

// =============================================================================
// HandoffExecutor
// =============================================================================

// HandoffExecutor executes handoff operations between agent sessions.
type HandoffExecutor struct {
	mu sync.RWMutex

	// sessionCreator creates new sessions.
	sessionCreator SessionCreator

	// config holds executor configuration.
	config *ExecutorConfig

	// hooks provides lifecycle callbacks.
	hooks HandoffHooks

	// stats holds execution statistics.
	stats executorStatsInternal

	// outcomes stores recent handoff outcomes for learning.
	outcomes *CircularBuffer[*HandoffResult]

	// closed indicates if the executor is closed.
	closed atomic.Bool
}

// executorStatsInternal holds internal statistics with atomic counters.
type executorStatsInternal struct {
	totalHandoffs          atomic.Int64
	successfulHandoffs     atomic.Int64
	failedHandoffs         atomic.Int64
	totalRetries           atomic.Int64
	totalRollbacks         atomic.Int64
	rollbackFailures       atomic.Int64
	validationFailures     atomic.Int64
	totalDurationNs        atomic.Int64
	totalTokensTransferred atomic.Int64
}

// NewHandoffExecutor creates a new HandoffExecutor.
// If sessionCreator is nil, a no-op creator is used (for testing).
// If config is nil, default configuration is used.
func NewHandoffExecutor(sessionCreator SessionCreator, config *ExecutorConfig) *HandoffExecutor {
	if config == nil {
		config = DefaultExecutorConfig()
	}

	return &HandoffExecutor{
		sessionCreator: sessionCreator,
		config:         config.Clone(),
		hooks:          &NoOpHooks{},
		outcomes:       NewCircularBuffer[*HandoffResult](100),
	}
}

// SetHooks sets the handoff hooks.
func (he *HandoffExecutor) SetHooks(hooks HandoffHooks) {
	he.mu.Lock()
	defer he.mu.Unlock()

	if hooks == nil {
		hooks = &NoOpHooks{}
	}
	he.hooks = hooks
}

// GetConfig returns a copy of the current configuration.
func (he *HandoffExecutor) GetConfig() *ExecutorConfig {
	he.mu.RLock()
	defer he.mu.RUnlock()
	return he.config.Clone()
}

// SetConfig updates the executor configuration.
func (he *HandoffExecutor) SetConfig(config *ExecutorConfig) {
	if config == nil {
		return
	}
	he.mu.Lock()
	defer he.mu.Unlock()
	he.config = config.Clone()
}

// =============================================================================
// PrepareTransfer
// =============================================================================

// PrepareTransfer creates a ContextTransfer from a decision and prepared context.
func (he *HandoffExecutor) PrepareTransfer(
	decision *HandoffDecision,
	preparedCtx *PreparedContext,
) (*ContextTransfer, error) {
	if he.closed.Load() {
		return nil, ErrExecutorClosed
	}

	if decision == nil {
		return nil, ErrNilDecision
	}

	// Get source session ID from metadata if available
	sourceSessionID := ""
	if preparedCtx != nil {
		if id, ok := preparedCtx.GetMetadata("session_id"); ok {
			sourceSessionID = id
		}
	}

	transfer := NewContextTransfer(sourceSessionID, decision, preparedCtx)

	// Add handoff metadata
	transfer.Metadata["trigger"] = decision.Trigger.String()
	transfer.Metadata["confidence"] = fmt.Sprintf("%.2f", decision.Confidence)
	if decision.Reason != "" {
		transfer.Metadata["reason"] = decision.Reason
	}

	return transfer, nil
}

// =============================================================================
// ValidateTransfer
// =============================================================================

// ValidateTransfer validates a context transfer.
func (he *HandoffExecutor) ValidateTransfer(transfer *ContextTransfer) error {
	if he.closed.Load() {
		return ErrExecutorClosed
	}

	he.mu.RLock()
	config := he.config
	he.mu.RUnlock()

	if !config.ValidationEnabled {
		transfer.Validated = true
		return nil
	}

	var errs []string

	// Validate token count
	if transfer.TokenCount < config.MinTokenCount {
		errs = append(errs, fmt.Sprintf("token count %d below minimum %d",
			transfer.TokenCount, config.MinTokenCount))
	}
	if transfer.TokenCount > config.MaxTokenCount {
		errs = append(errs, fmt.Sprintf("token count %d exceeds maximum %d",
			transfer.TokenCount, config.MaxTokenCount))
	}

	// Validate summary if required
	if config.RequireSummary && transfer.Summary == "" {
		errs = append(errs, "summary is required but empty")
	}

	// Validate messages if required
	if config.RequireMessages && len(transfer.RecentMessages) == 0 {
		errs = append(errs, "at least one message is required")
	}

	// Validate decision
	if transfer.Decision == nil {
		errs = append(errs, "decision is required")
	}

	if len(errs) > 0 {
		transfer.ValidationErrors = errs
		transfer.Validated = false
		he.stats.validationFailures.Add(1)
		return fmt.Errorf("%w: %v", ErrTransferValidationFailed, errs)
	}

	transfer.Validated = true
	transfer.ValidationErrors = nil
	return nil
}

// =============================================================================
// ExecuteTransfer
// =============================================================================

// ExecuteTransfer executes a context transfer with the configured timeout.
// Alias for ExecuteHandoff for API compatibility.
func (he *HandoffExecutor) ExecuteTransfer(ctx context.Context, transfer *ContextTransfer) *HandoffResult {
	return he.ExecuteHandoff(ctx, transfer)
}

// ExecuteHandoff executes a handoff operation.
func (he *HandoffExecutor) ExecuteHandoff(ctx context.Context, transfer *ContextTransfer) *HandoffResult {
	startTime := time.Now()

	// Initialize result
	result := &HandoffResult{
		Success:            false,
		TransferredContext: transfer,
		Timestamp:          startTime,
	}

	// Check if closed
	if he.closed.Load() {
		result.Error = ErrExecutorClosed
		result.ErrorMessage = ErrExecutorClosed.Error()
		result.Duration = time.Since(startTime)
		return result
	}

	he.mu.RLock()
	config := he.config
	hooks := he.hooks
	he.mu.RUnlock()

	// Track handoff attempt
	he.stats.totalHandoffs.Add(1)

	// Apply timeout
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, config.DefaultTimeout)
		defer cancel()
	}

	// Call pre-handoff hook
	if err := hooks.PreHandoff(ctx, transfer); err != nil {
		result.Error = fmt.Errorf("%w: %v", ErrPreHookFailed, err)
		result.ErrorMessage = result.Error.Error()
		result.Duration = time.Since(startTime)
		he.stats.failedHandoffs.Add(1)
		he.recordOutcome(result)
		return result
	}

	// Validate if not already validated
	if !transfer.Validated {
		validationStart := time.Now()
		if err := he.ValidateTransfer(transfer); err != nil {
			result.Error = err
			result.ErrorMessage = err.Error()
			result.Duration = time.Since(startTime)
			result.Metrics.ValidationDuration = time.Since(validationStart)
			he.stats.failedHandoffs.Add(1)
			hooks.PostHandoff(ctx, result)
			he.recordOutcome(result)
			return result
		}
		result.Metrics.ValidationDuration = time.Since(validationStart)
	}

	// Execute with retries
	var lastErr error
	var newSessionID string

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		if attempt > 0 {
			he.stats.totalRetries.Add(1)
			result.RetryCount = attempt

			// Exponential backoff
			delay := he.calculateBackoff(attempt, config)
			select {
			case <-ctx.Done():
				result.Error = fmt.Errorf("%w: %v", ErrHandoffTimeout, ctx.Err())
				result.ErrorMessage = result.Error.Error()
				result.Duration = time.Since(startTime)
				he.stats.failedHandoffs.Add(1)
				hooks.PostHandoff(ctx, result)
				he.recordOutcome(result)
				return result
			case <-time.After(delay):
				// Continue with retry
			}
		}

		// Attempt handoff
		newSessionID, lastErr = he.attemptHandoff(ctx, transfer, result, config)
		if lastErr == nil {
			break
		}
	}

	// Handle final result
	if lastErr != nil {
		result.Error = lastErr
		result.ErrorMessage = lastErr.Error()
		result.Duration = time.Since(startTime)

		// Attempt rollback if configured and we have a session
		if config.RollbackOnFailure && newSessionID != "" {
			he.performRollback(ctx, transfer, newSessionID, result, hooks)
		}

		he.stats.failedHandoffs.Add(1)
		hooks.PostHandoff(ctx, result)
		he.recordOutcome(result)
		return result
	}

	// Success
	result.Success = true
	result.NewSessionID = newSessionID
	result.Duration = time.Since(startTime)

	// Update metrics
	if config.CollectMetrics {
		result.Metrics.TokensTransferred = transfer.TokenCount
		result.Metrics.MessagesTransferred = len(transfer.RecentMessages)
		result.Metrics.ToolStatesTransferred = len(transfer.ToolStates)
	}

	// Update stats
	he.stats.successfulHandoffs.Add(1)
	he.stats.totalDurationNs.Add(int64(result.Duration))
	he.stats.totalTokensTransferred.Add(int64(transfer.TokenCount))

	// Call post-handoff hook
	hooks.PostHandoff(ctx, result)
	he.recordOutcome(result)

	return result
}

// attemptHandoff performs a single handoff attempt.
func (he *HandoffExecutor) attemptHandoff(
	ctx context.Context,
	transfer *ContextTransfer,
	result *HandoffResult,
	config *ExecutorConfig,
) (string, error) {
	// Check if session creator is available
	if he.sessionCreator == nil {
		return "", ErrSessionCreationFailed
	}

	// Create session configuration
	sessionConfig := SessionConfig{
		Name:            fmt.Sprintf("handoff-%s", transfer.ID),
		Description:     fmt.Sprintf("Handoff from session %s", transfer.SourceSessionID),
		ParentSessionID: transfer.SourceSessionID,
		Metadata: map[string]interface{}{
			"handoff_trigger": transfer.Decision.Trigger.String(),
			"handoff_time":    transfer.CreatedAt.Format(time.RFC3339),
		},
	}

	// Create new session
	creationStart := time.Now()
	newSessionID, err := he.sessionCreator.CreateSession(ctx, sessionConfig)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrSessionCreationFailed, err)
	}
	if config.CollectMetrics {
		result.Metrics.SessionCreationDuration = time.Since(creationStart)
	}

	// Transfer context
	transferStart := time.Now()
	if err := he.sessionCreator.TransferContext(ctx, newSessionID, transfer); err != nil {
		return newSessionID, fmt.Errorf("%w: %v", ErrContextTransferFailed, err)
	}
	if config.CollectMetrics {
		result.Metrics.ContextTransferDuration = time.Since(transferStart)
	}

	// Activate session
	activationStart := time.Now()
	if err := he.sessionCreator.ActivateSession(ctx, newSessionID); err != nil {
		return newSessionID, fmt.Errorf("%w: activation failed: %v", ErrContextTransferFailed, err)
	}
	if config.CollectMetrics {
		result.Metrics.ActivationDuration = time.Since(activationStart)
	}

	return newSessionID, nil
}

// performRollback attempts to rollback a failed handoff.
func (he *HandoffExecutor) performRollback(
	ctx context.Context,
	transfer *ContextTransfer,
	newSessionID string,
	result *HandoffResult,
	hooks HandoffHooks,
) {
	he.stats.totalRollbacks.Add(1)
	result.RolledBack = true

	// Create rollback context with timeout
	rollbackCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Attempt to deactivate and delete the session
	var rollbackErr error
	if he.sessionCreator != nil {
		if err := he.sessionCreator.DeactivateSession(rollbackCtx, newSessionID); err != nil {
			rollbackErr = err
		}
		if err := he.sessionCreator.DeleteSession(rollbackCtx, newSessionID); err != nil {
			if rollbackErr != nil {
				rollbackErr = fmt.Errorf("%v; delete: %v", rollbackErr, err)
			} else {
				rollbackErr = err
			}
		}
	}

	if rollbackErr != nil {
		he.stats.rollbackFailures.Add(1)
		result.RollbackError = rollbackErr
		result.RollbackErrorMessage = rollbackErr.Error()
	}

	// Call rollback hook
	hooks.OnRollback(ctx, transfer, rollbackErr)
}

// calculateBackoff calculates the delay for exponential backoff.
func (he *HandoffExecutor) calculateBackoff(attempt int, config *ExecutorConfig) time.Duration {
	delay := float64(config.RetryBaseDelay) * math.Pow(config.RetryMultiplier, float64(attempt-1))
	if delay > float64(config.RetryMaxDelay) {
		delay = float64(config.RetryMaxDelay)
	}
	return time.Duration(delay)
}

// recordOutcome stores the outcome for learning.
func (he *HandoffExecutor) recordOutcome(result *HandoffResult) {
	he.mu.Lock()
	defer he.mu.Unlock()
	he.outcomes.Push(result)
}

// =============================================================================
// RecordOutcome
// =============================================================================

// RecordOutcome records a handoff outcome for learning feedback.
// This is called externally to provide additional outcome information.
func (he *HandoffExecutor) RecordOutcome(result *HandoffResult) {
	if he.closed.Load() {
		return
	}

	he.recordOutcome(result)
}

// =============================================================================
// Statistics and Getters
// =============================================================================

// Stats returns executor statistics.
func (he *HandoffExecutor) Stats() ExecutorStats {
	total := he.stats.totalHandoffs.Load()
	successful := he.stats.successfulHandoffs.Load()
	totalDuration := time.Duration(he.stats.totalDurationNs.Load())

	successRate := 0.0
	averageDuration := time.Duration(0)
	if total > 0 {
		successRate = float64(successful) / float64(total)
		if successful > 0 {
			averageDuration = totalDuration / time.Duration(successful)
		}
	}

	return ExecutorStats{
		TotalHandoffs:          total,
		SuccessfulHandoffs:     successful,
		FailedHandoffs:         he.stats.failedHandoffs.Load(),
		TotalRetries:           he.stats.totalRetries.Load(),
		TotalRollbacks:         he.stats.totalRollbacks.Load(),
		RollbackFailures:       he.stats.rollbackFailures.Load(),
		ValidationFailures:     he.stats.validationFailures.Load(),
		TotalDuration:          totalDuration,
		AverageDuration:        averageDuration,
		TotalTokensTransferred: he.stats.totalTokensTransferred.Load(),
		SuccessRate:            successRate,
	}
}

// RecentOutcomes returns recent handoff outcomes.
func (he *HandoffExecutor) RecentOutcomes(n int) []*HandoffResult {
	he.mu.RLock()
	defer he.mu.RUnlock()
	return he.outcomes.RecentN(n)
}

// Close closes the executor.
func (he *HandoffExecutor) Close() error {
	if he.closed.Swap(true) {
		return ErrExecutorClosed
	}
	return nil
}

// IsClosed returns true if the executor is closed.
func (he *HandoffExecutor) IsClosed() bool {
	return he.closed.Load()
}
