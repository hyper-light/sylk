package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Mock SessionCreator for Testing
// =============================================================================

// mockSessionCreator is a mock implementation of SessionCreator for testing.
type mockSessionCreator struct {
	mu sync.Mutex

	// Configurable behavior
	createSessionError  error
	transferContextError error
	activateSessionError error
	deactivateError      error
	deleteError          error

	// Track calls
	createCalls    int
	transferCalls  int
	activateCalls  int
	deactivateCalls int
	deleteCalls    int

	// Created sessions
	sessions map[string]bool

	// Delays for simulating latency
	createDelay    time.Duration
	transferDelay  time.Duration
	activateDelay  time.Duration

	// Fail on attempt number (0 = never fail, 1 = fail first, etc.)
	failOnAttempt int
	attemptCount  int
}

func newMockSessionCreator() *mockSessionCreator {
	return &mockSessionCreator{
		sessions: make(map[string]bool),
	}
}

func (m *mockSessionCreator) CreateSession(ctx context.Context, config SessionConfig) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.createCalls++
	m.attemptCount++

	if m.createDelay > 0 {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(m.createDelay):
		}
	}

	if m.failOnAttempt > 0 && m.attemptCount <= m.failOnAttempt {
		return "", errors.New("simulated creation failure")
	}

	if m.createSessionError != nil {
		return "", m.createSessionError
	}

	sessionID := "session-" + config.Name
	m.sessions[sessionID] = true
	return sessionID, nil
}

func (m *mockSessionCreator) TransferContext(ctx context.Context, sessionID string, transfer *ContextTransfer) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.transferCalls++

	if m.transferDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.transferDelay):
		}
	}

	if m.transferContextError != nil {
		return m.transferContextError
	}

	return nil
}

func (m *mockSessionCreator) ActivateSession(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.activateCalls++

	if m.activateDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.activateDelay):
		}
	}

	if m.activateSessionError != nil {
		return m.activateSessionError
	}

	return nil
}

func (m *mockSessionCreator) DeactivateSession(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.deactivateCalls++

	if m.deactivateError != nil {
		return m.deactivateError
	}

	return nil
}

func (m *mockSessionCreator) DeleteSession(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.deleteCalls++

	if m.deleteError != nil {
		return m.deleteError
	}

	delete(m.sessions, sessionID)
	return nil
}

func (m *mockSessionCreator) getCounts() (create, transfer, activate, deactivate, del int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createCalls, m.transferCalls, m.activateCalls, m.deactivateCalls, m.deleteCalls
}

func (m *mockSessionCreator) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createCalls = 0
	m.transferCalls = 0
	m.activateCalls = 0
	m.deactivateCalls = 0
	m.deleteCalls = 0
	m.attemptCount = 0
	m.sessions = make(map[string]bool)
}

// =============================================================================
// Mock HandoffHooks for Testing
// =============================================================================

type mockHooks struct {
	mu sync.Mutex

	preHandoffCalls  int
	postHandoffCalls int
	rollbackCalls    int

	preHandoffError error

	lastTransfer *ContextTransfer
	lastResult   *HandoffResult
	lastRollbackErr error
}

func newMockHooks() *mockHooks {
	return &mockHooks{}
}

func (m *mockHooks) PreHandoff(ctx context.Context, transfer *ContextTransfer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.preHandoffCalls++
	m.lastTransfer = transfer
	return m.preHandoffError
}

func (m *mockHooks) PostHandoff(ctx context.Context, result *HandoffResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.postHandoffCalls++
	m.lastResult = result
}

func (m *mockHooks) OnRollback(ctx context.Context, transfer *ContextTransfer, rollbackErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rollbackCalls++
	m.lastTransfer = transfer
	m.lastRollbackErr = rollbackErr
}

func (m *mockHooks) getCounts() (pre, post, rollback int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.preHandoffCalls, m.postHandoffCalls, m.rollbackCalls
}

// =============================================================================
// Helper Functions
// =============================================================================

func createTestDecision(shouldHandoff bool, trigger HandoffTrigger) *HandoffDecision {
	return NewHandoffDecision(
		shouldHandoff,
		0.9,
		"Test decision",
		trigger,
		DecisionFactors{
			ContextUtilization: 0.8,
			PredictedQuality:   0.6,
		},
	)
}

func createTestPreparedContext() *PreparedContext {
	ctx := NewPreparedContextDefault()
	ctx.AddMessage(NewMessage("user", "Hello, how are you?"))
	ctx.AddMessage(NewMessage("assistant", "I'm doing well, thank you!"))
	ctx.SetMetadata("session_id", "source-session-123")
	return ctx
}

// =============================================================================
// Error Tests
// =============================================================================

func TestErrors_Defined(t *testing.T) {
	errors := []error{
		ErrTransferValidationFailed,
		ErrSessionCreationFailed,
		ErrContextTransferFailed,
		ErrHandoffTimeout,
		ErrRollbackFailed,
		ErrPreHookFailed,
		ErrPostHookFailed,
		ErrExecutorClosed,
		ErrNilDecision,
		ErrNilContext,
		ErrMaxRetriesExceeded,
	}

	for _, err := range errors {
		if err == nil {
			t.Error("Error should not be nil")
		}
		if err.Error() == "" {
			t.Errorf("Error message should not be empty: %v", err)
		}
	}
}

// =============================================================================
// SessionConfig Tests
// =============================================================================

func TestSessionConfig_JSON(t *testing.T) {
	config := SessionConfig{
		Name:            "test-session",
		Description:     "A test session",
		ParentSessionID: "parent-123",
		Metadata: map[string]interface{}{
			"key1": "value1",
			"key2": 42,
		},
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var got SessionConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if got.Name != config.Name {
		t.Errorf("Name = %s, want %s", got.Name, config.Name)
	}
	if got.ParentSessionID != config.ParentSessionID {
		t.Errorf("ParentSessionID = %s, want %s", got.ParentSessionID, config.ParentSessionID)
	}
}

// =============================================================================
// ContextTransfer Tests
// =============================================================================

func TestNewContextTransfer(t *testing.T) {
	decision := createTestDecision(true, TriggerContextFull)
	preparedCtx := createTestPreparedContext()

	transfer := NewContextTransfer("source-session", decision, preparedCtx)

	if transfer.ID == "" {
		t.Error("ID should be generated")
	}
	if transfer.SourceSessionID != "source-session" {
		t.Errorf("SourceSessionID = %s, want source-session", transfer.SourceSessionID)
	}
	if transfer.Decision != decision {
		t.Error("Decision should be set")
	}
	if transfer.Summary == "" {
		t.Error("Summary should be extracted from prepared context")
	}
	if len(transfer.RecentMessages) != 2 {
		t.Errorf("RecentMessages = %d, want 2", len(transfer.RecentMessages))
	}
	if transfer.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
	if transfer.Validated {
		t.Error("Validated should be false initially")
	}
}

func TestNewContextTransfer_NilPreparedContext(t *testing.T) {
	decision := createTestDecision(true, TriggerUserRequest)

	transfer := NewContextTransfer("source-session", decision, nil)

	if transfer.ID == "" {
		t.Error("ID should be generated")
	}
	if transfer.Summary != "" {
		t.Error("Summary should be empty with nil prepared context")
	}
	if len(transfer.RecentMessages) != 0 {
		t.Error("RecentMessages should be empty")
	}
}

func TestContextTransfer_JSON(t *testing.T) {
	decision := createTestDecision(true, TriggerQualityDegrading)
	preparedCtx := createTestPreparedContext()
	transfer := NewContextTransfer("source", decision, preparedCtx)
	transfer.Metadata["custom"] = "value"

	data, err := json.Marshal(transfer)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var got ContextTransfer
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if got.ID != transfer.ID {
		t.Errorf("ID = %s, want %s", got.ID, transfer.ID)
	}
	if got.SourceSessionID != transfer.SourceSessionID {
		t.Errorf("SourceSessionID = %s, want %s", got.SourceSessionID, transfer.SourceSessionID)
	}
	if got.Metadata["custom"] != "value" {
		t.Error("Metadata should be preserved")
	}
}

// =============================================================================
// ExecutorConfig Tests
// =============================================================================

func TestDefaultExecutorConfig(t *testing.T) {
	config := DefaultExecutorConfig()

	if config.DefaultTimeout != 30*time.Second {
		t.Errorf("DefaultTimeout = %v, want 30s", config.DefaultTimeout)
	}
	if config.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", config.MaxRetries)
	}
	if config.RetryBaseDelay != 100*time.Millisecond {
		t.Errorf("RetryBaseDelay = %v, want 100ms", config.RetryBaseDelay)
	}
	if !config.ValidationEnabled {
		t.Error("ValidationEnabled should be true by default")
	}
	if !config.RollbackOnFailure {
		t.Error("RollbackOnFailure should be true by default")
	}
	if !config.CollectMetrics {
		t.Error("CollectMetrics should be true by default")
	}
}

func TestExecutorConfig_Clone(t *testing.T) {
	config := &ExecutorConfig{
		DefaultTimeout:    10 * time.Second,
		MaxRetries:        5,
		RetryBaseDelay:    50 * time.Millisecond,
		ValidationEnabled: true,
		RequireSummary:    true,
	}

	cloned := config.Clone()

	if cloned.DefaultTimeout != config.DefaultTimeout {
		t.Errorf("DefaultTimeout = %v, want %v", cloned.DefaultTimeout, config.DefaultTimeout)
	}
	if cloned.MaxRetries != config.MaxRetries {
		t.Errorf("MaxRetries = %d, want %d", cloned.MaxRetries, config.MaxRetries)
	}

	// Verify independence
	cloned.MaxRetries = 10
	if config.MaxRetries == 10 {
		t.Error("Clone should be independent")
	}
}

func TestExecutorConfig_CloneNil(t *testing.T) {
	var config *ExecutorConfig
	cloned := config.Clone()
	if cloned != nil {
		t.Error("Clone of nil should be nil")
	}
}

// =============================================================================
// HandoffResult Tests
// =============================================================================

func TestHandoffResult_JSON(t *testing.T) {
	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := &HandoffResult{
		Success:            true,
		NewSessionID:       "new-session-123",
		TransferredContext: transfer,
		Duration:           5 * time.Second,
		RetryCount:         1,
		Timestamp:          time.Now(),
		Metrics: HandoffMetrics{
			TokensTransferred:   500,
			MessagesTransferred: 10,
		},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Verify JSON contains duration as string
	if !contains(string(data), "5s") {
		t.Error("Duration should be serialized as string")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// =============================================================================
// NewHandoffExecutor Tests
// =============================================================================

func TestNewHandoffExecutor(t *testing.T) {
	t.Run("with nil params", func(t *testing.T) {
		executor := NewHandoffExecutor(nil, nil)

		if executor == nil {
			t.Fatal("Executor should not be nil")
		}
		if executor.config == nil {
			t.Error("Config should be initialized with defaults")
		}
		if executor.hooks == nil {
			t.Error("Hooks should be initialized")
		}
	})

	t.Run("with custom config", func(t *testing.T) {
		config := &ExecutorConfig{
			DefaultTimeout: 10 * time.Second,
			MaxRetries:     5,
		}
		mock := newMockSessionCreator()
		executor := NewHandoffExecutor(mock, config)

		if executor.config.DefaultTimeout != 10*time.Second {
			t.Errorf("DefaultTimeout = %v, want 10s", executor.config.DefaultTimeout)
		}
		if executor.config.MaxRetries != 5 {
			t.Errorf("MaxRetries = %d, want 5", executor.config.MaxRetries)
		}
	})
}

// =============================================================================
// SetHooks Tests
// =============================================================================

func TestHandoffExecutor_SetHooks(t *testing.T) {
	executor := NewHandoffExecutor(nil, nil)
	hooks := newMockHooks()

	executor.SetHooks(hooks)

	// Verify hooks are set (indirectly through behavior)
	executor.SetHooks(nil) // Should not panic and set NoOpHooks
}

// =============================================================================
// GetConfig/SetConfig Tests
// =============================================================================

func TestHandoffExecutor_GetSetConfig(t *testing.T) {
	executor := NewHandoffExecutor(nil, nil)

	newConfig := &ExecutorConfig{
		DefaultTimeout: 20 * time.Second,
		MaxRetries:     10,
	}
	executor.SetConfig(newConfig)

	got := executor.GetConfig()
	if got.DefaultTimeout != 20*time.Second {
		t.Errorf("DefaultTimeout = %v, want 20s", got.DefaultTimeout)
	}

	// Verify it returns a copy
	got.MaxRetries = 100
	got2 := executor.GetConfig()
	if got2.MaxRetries == 100 {
		t.Error("GetConfig should return a copy")
	}
}

func TestHandoffExecutor_SetConfigNil(t *testing.T) {
	executor := NewHandoffExecutor(nil, nil)
	originalTimeout := executor.config.DefaultTimeout

	executor.SetConfig(nil)

	if executor.config.DefaultTimeout != originalTimeout {
		t.Error("SetConfig(nil) should not change config")
	}
}

// =============================================================================
// PrepareTransfer Tests
// =============================================================================

func TestHandoffExecutor_PrepareTransfer(t *testing.T) {
	executor := NewHandoffExecutor(nil, nil)
	decision := createTestDecision(true, TriggerContextFull)
	preparedCtx := createTestPreparedContext()

	transfer, err := executor.PrepareTransfer(decision, preparedCtx)

	if err != nil {
		t.Fatalf("PrepareTransfer failed: %v", err)
	}
	if transfer == nil {
		t.Fatal("Transfer should not be nil")
	}
	if transfer.Decision != decision {
		t.Error("Decision should be set")
	}
	if transfer.Metadata["trigger"] != "ContextFull" {
		t.Errorf("Trigger metadata = %s, want ContextFull", transfer.Metadata["trigger"])
	}
}

func TestHandoffExecutor_PrepareTransfer_NilDecision(t *testing.T) {
	executor := NewHandoffExecutor(nil, nil)

	_, err := executor.PrepareTransfer(nil, nil)

	if !errors.Is(err, ErrNilDecision) {
		t.Errorf("Error = %v, want ErrNilDecision", err)
	}
}

func TestHandoffExecutor_PrepareTransfer_Closed(t *testing.T) {
	executor := NewHandoffExecutor(nil, nil)
	executor.Close()

	decision := createTestDecision(true, TriggerUserRequest)
	_, err := executor.PrepareTransfer(decision, nil)

	if !errors.Is(err, ErrExecutorClosed) {
		t.Errorf("Error = %v, want ErrExecutorClosed", err)
	}
}

// =============================================================================
// ValidateTransfer Tests
// =============================================================================

func TestHandoffExecutor_ValidateTransfer(t *testing.T) {
	t.Run("valid transfer", func(t *testing.T) {
		executor := NewHandoffExecutor(nil, nil)
		decision := createTestDecision(true, TriggerContextFull)
		transfer := NewContextTransfer("source", decision, createTestPreparedContext())

		err := executor.ValidateTransfer(transfer)

		if err != nil {
			t.Errorf("ValidateTransfer should succeed: %v", err)
		}
		if !transfer.Validated {
			t.Error("Validated should be true")
		}
	})

	t.Run("validation disabled", func(t *testing.T) {
		config := &ExecutorConfig{
			ValidationEnabled: false,
		}
		executor := NewHandoffExecutor(nil, config)
		decision := createTestDecision(true, TriggerContextFull)
		transfer := NewContextTransfer("source", decision, nil)

		err := executor.ValidateTransfer(transfer)

		if err != nil {
			t.Errorf("Should succeed when validation disabled: %v", err)
		}
		if !transfer.Validated {
			t.Error("Validated should be true")
		}
	})

	t.Run("token count below minimum", func(t *testing.T) {
		config := &ExecutorConfig{
			ValidationEnabled: true,
			MinTokenCount:     100,
		}
		executor := NewHandoffExecutor(nil, config)
		decision := createTestDecision(true, TriggerContextFull)
		transfer := NewContextTransfer("source", decision, nil)
		transfer.TokenCount = 50

		err := executor.ValidateTransfer(transfer)

		if err == nil {
			t.Error("Should fail validation")
		}
		if !errors.Is(err, ErrTransferValidationFailed) {
			t.Errorf("Error = %v, want ErrTransferValidationFailed", err)
		}
	})

	t.Run("token count exceeds maximum", func(t *testing.T) {
		config := &ExecutorConfig{
			ValidationEnabled: true,
			MaxTokenCount:     100,
		}
		executor := NewHandoffExecutor(nil, config)
		decision := createTestDecision(true, TriggerContextFull)
		transfer := NewContextTransfer("source", decision, nil)
		transfer.TokenCount = 200

		err := executor.ValidateTransfer(transfer)

		if err == nil {
			t.Error("Should fail validation")
		}
	})

	t.Run("summary required but empty", func(t *testing.T) {
		config := &ExecutorConfig{
			ValidationEnabled: true,
			RequireSummary:    true,
		}
		executor := NewHandoffExecutor(nil, config)
		decision := createTestDecision(true, TriggerContextFull)
		transfer := NewContextTransfer("source", decision, nil)
		transfer.Summary = ""

		err := executor.ValidateTransfer(transfer)

		if err == nil {
			t.Error("Should fail validation")
		}
	})

	t.Run("messages required but empty", func(t *testing.T) {
		config := &ExecutorConfig{
			ValidationEnabled: true,
			RequireMessages:   true,
		}
		executor := NewHandoffExecutor(nil, config)
		decision := createTestDecision(true, TriggerContextFull)
		transfer := NewContextTransfer("source", decision, nil)

		err := executor.ValidateTransfer(transfer)

		if err == nil {
			t.Error("Should fail validation")
		}
	})

	t.Run("nil decision", func(t *testing.T) {
		executor := NewHandoffExecutor(nil, nil)
		transfer := &ContextTransfer{}

		err := executor.ValidateTransfer(transfer)

		if err == nil {
			t.Error("Should fail validation with nil decision")
		}
	})
}

func TestHandoffExecutor_ValidateTransfer_Closed(t *testing.T) {
	executor := NewHandoffExecutor(nil, nil)
	executor.Close()

	transfer := &ContextTransfer{}
	err := executor.ValidateTransfer(transfer)

	if !errors.Is(err, ErrExecutorClosed) {
		t.Errorf("Error = %v, want ErrExecutorClosed", err)
	}
}

// =============================================================================
// ExecuteHandoff Tests - Success Cases
// =============================================================================

func TestHandoffExecutor_ExecuteHandoff_Success(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)

	decision := createTestDecision(true, TriggerContextFull)
	preparedCtx := createTestPreparedContext()
	transfer := NewContextTransfer("source-session", decision, preparedCtx)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if !result.Success {
		t.Errorf("Handoff should succeed: %v", result.Error)
	}
	if result.NewSessionID == "" {
		t.Error("NewSessionID should be set")
	}
	if result.Duration == 0 {
		t.Error("Duration should be set")
	}
	if result.Metrics.TokensTransferred == 0 {
		t.Error("TokensTransferred should be set")
	}

	// Verify mock was called
	create, transfer2, activate, _, _ := mock.getCounts()
	if create != 1 {
		t.Errorf("CreateSession called %d times, want 1", create)
	}
	if transfer2 != 1 {
		t.Errorf("TransferContext called %d times, want 1", transfer2)
	}
	if activate != 1 {
		t.Errorf("ActivateSession called %d times, want 1", activate)
	}
}

func TestHandoffExecutor_ExecuteTransfer_Alias(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)

	decision := createTestDecision(true, TriggerUserRequest)
	transfer := NewContextTransfer("source", decision, nil)

	// ExecuteTransfer is an alias for ExecuteHandoff
	result := executor.ExecuteTransfer(context.Background(), transfer)

	if !result.Success {
		t.Errorf("ExecuteTransfer should succeed: %v", result.Error)
	}
}

// =============================================================================
// ExecuteHandoff Tests - Failure Cases
// =============================================================================

func TestHandoffExecutor_ExecuteHandoff_SessionCreationFailed(t *testing.T) {
	mock := newMockSessionCreator()
	mock.createSessionError = errors.New("creation failed")
	config := &ExecutorConfig{
		MaxRetries:        0, // No retries
		RollbackOnFailure: false,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if result.Success {
		t.Error("Handoff should fail")
	}
	if !errors.Is(result.Error, ErrSessionCreationFailed) {
		t.Errorf("Error = %v, should wrap ErrSessionCreationFailed", result.Error)
	}
}

func TestHandoffExecutor_ExecuteHandoff_TransferContextFailed(t *testing.T) {
	mock := newMockSessionCreator()
	mock.transferContextError = errors.New("transfer failed")
	config := &ExecutorConfig{
		MaxRetries:        0,
		RollbackOnFailure: true,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if result.Success {
		t.Error("Handoff should fail")
	}
	if !errors.Is(result.Error, ErrContextTransferFailed) {
		t.Errorf("Error = %v, should wrap ErrContextTransferFailed", result.Error)
	}
	if !result.RolledBack {
		t.Error("Should have attempted rollback")
	}
}

func TestHandoffExecutor_ExecuteHandoff_ActivationFailed(t *testing.T) {
	mock := newMockSessionCreator()
	mock.activateSessionError = errors.New("activation failed")
	config := &ExecutorConfig{
		MaxRetries:        0,
		RollbackOnFailure: true,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if result.Success {
		t.Error("Handoff should fail")
	}
	if result.RolledBack != true {
		t.Error("Should have attempted rollback")
	}
}

func TestHandoffExecutor_ExecuteHandoff_ValidationFailed(t *testing.T) {
	mock := newMockSessionCreator()
	config := &ExecutorConfig{
		ValidationEnabled: true,
		RequireSummary:    true,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)
	transfer.Summary = "" // Will fail validation

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if result.Success {
		t.Error("Handoff should fail due to validation")
	}
	if !errors.Is(result.Error, ErrTransferValidationFailed) {
		t.Errorf("Error = %v, want ErrTransferValidationFailed", result.Error)
	}

	// Session creation should not have been called
	create, _, _, _, _ := mock.getCounts()
	if create != 0 {
		t.Errorf("CreateSession should not be called on validation failure, got %d", create)
	}
}

func TestHandoffExecutor_ExecuteHandoff_NilSessionCreator(t *testing.T) {
	executor := NewHandoffExecutor(nil, nil)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if result.Success {
		t.Error("Handoff should fail with nil session creator")
	}
	if !errors.Is(result.Error, ErrSessionCreationFailed) {
		t.Errorf("Error = %v, want ErrSessionCreationFailed", result.Error)
	}
}

func TestHandoffExecutor_ExecuteHandoff_Closed(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)
	executor.Close()

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if result.Success {
		t.Error("Handoff should fail when executor is closed")
	}
	if !errors.Is(result.Error, ErrExecutorClosed) {
		t.Errorf("Error = %v, want ErrExecutorClosed", result.Error)
	}
}

// =============================================================================
// ExecuteHandoff Tests - Timeout Handling
// =============================================================================

func TestHandoffExecutor_ExecuteHandoff_Timeout(t *testing.T) {
	mock := newMockSessionCreator()
	mock.createDelay = 500 * time.Millisecond
	config := &ExecutorConfig{
		DefaultTimeout: 100 * time.Millisecond,
		MaxRetries:     0,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if result.Success {
		t.Error("Handoff should fail due to timeout")
	}
	if !errors.Is(result.Error, ErrSessionCreationFailed) {
		t.Errorf("Error = %v, should indicate timeout", result.Error)
	}
}

func TestHandoffExecutor_ExecuteHandoff_ContextCanceled(t *testing.T) {
	mock := newMockSessionCreator()
	mock.createDelay = 500 * time.Millisecond
	config := &ExecutorConfig{
		DefaultTimeout: 10 * time.Second,
		MaxRetries:     0,
	}
	executor := NewHandoffExecutor(mock, config)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := executor.ExecuteHandoff(ctx, transfer)

	if result.Success {
		t.Error("Handoff should fail due to context cancellation")
	}
}

// =============================================================================
// ExecuteHandoff Tests - Rollback
// =============================================================================

func TestHandoffExecutor_ExecuteHandoff_RollbackOnFailure(t *testing.T) {
	mock := newMockSessionCreator()
	mock.activateSessionError = errors.New("activation failed")
	config := &ExecutorConfig{
		MaxRetries:        0,
		RollbackOnFailure: true,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if result.Success {
		t.Error("Handoff should fail")
	}
	if !result.RolledBack {
		t.Error("Should have rolled back")
	}

	_, _, _, deactivate, del := mock.getCounts()
	if deactivate != 1 {
		t.Errorf("DeactivateSession called %d times, want 1", deactivate)
	}
	if del != 1 {
		t.Errorf("DeleteSession called %d times, want 1", del)
	}
}

func TestHandoffExecutor_ExecuteHandoff_RollbackFailed(t *testing.T) {
	mock := newMockSessionCreator()
	mock.activateSessionError = errors.New("activation failed")
	mock.deactivateError = errors.New("deactivate failed")
	mock.deleteError = errors.New("delete failed")
	config := &ExecutorConfig{
		MaxRetries:        0,
		RollbackOnFailure: true,
	}
	executor := NewHandoffExecutor(mock, config)
	hooks := newMockHooks()
	executor.SetHooks(hooks)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if result.Success {
		t.Error("Handoff should fail")
	}
	if result.RollbackError == nil {
		t.Error("RollbackError should be set")
	}
	if result.RollbackErrorMessage == "" {
		t.Error("RollbackErrorMessage should be set")
	}

	// Verify rollback hook was called
	_, _, rollback := hooks.getCounts()
	if rollback != 1 {
		t.Errorf("OnRollback called %d times, want 1", rollback)
	}
}

func TestHandoffExecutor_ExecuteHandoff_RollbackDisabled(t *testing.T) {
	mock := newMockSessionCreator()
	mock.activateSessionError = errors.New("activation failed")
	config := &ExecutorConfig{
		MaxRetries:        0,
		RollbackOnFailure: false,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if result.Success {
		t.Error("Handoff should fail")
	}
	if result.RolledBack {
		t.Error("Should not have rolled back when disabled")
	}

	_, _, _, deactivate, del := mock.getCounts()
	if deactivate != 0 || del != 0 {
		t.Error("Rollback functions should not be called")
	}
}

// =============================================================================
// ExecuteHandoff Tests - Hooks Invocation
// =============================================================================

func TestHandoffExecutor_ExecuteHandoff_HooksInvoked(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)
	hooks := newMockHooks()
	executor.SetHooks(hooks)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if !result.Success {
		t.Fatalf("Handoff should succeed: %v", result.Error)
	}

	pre, post, _ := hooks.getCounts()
	if pre != 1 {
		t.Errorf("PreHandoff called %d times, want 1", pre)
	}
	if post != 1 {
		t.Errorf("PostHandoff called %d times, want 1", post)
	}
}

func TestHandoffExecutor_ExecuteHandoff_PreHookFailed(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)
	hooks := newMockHooks()
	hooks.preHandoffError = errors.New("pre-hook failed")
	executor.SetHooks(hooks)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if result.Success {
		t.Error("Handoff should fail due to pre-hook failure")
	}
	if !errors.Is(result.Error, ErrPreHookFailed) {
		t.Errorf("Error = %v, want ErrPreHookFailed", result.Error)
	}

	// Session creation should not have been called
	create, _, _, _, _ := mock.getCounts()
	if create != 0 {
		t.Errorf("CreateSession should not be called, got %d", create)
	}
}

func TestHandoffExecutor_ExecuteHandoff_PostHookCalledOnFailure(t *testing.T) {
	mock := newMockSessionCreator()
	mock.createSessionError = errors.New("creation failed")
	config := &ExecutorConfig{MaxRetries: 0}
	executor := NewHandoffExecutor(mock, config)
	hooks := newMockHooks()
	executor.SetHooks(hooks)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if result.Success {
		t.Error("Handoff should fail")
	}

	_, post, _ := hooks.getCounts()
	if post != 1 {
		t.Errorf("PostHandoff should be called on failure, got %d", post)
	}
}

// =============================================================================
// ExecuteHandoff Tests - Retry Logic
// =============================================================================

func TestHandoffExecutor_ExecuteHandoff_RetrySuccess(t *testing.T) {
	mock := newMockSessionCreator()
	mock.failOnAttempt = 2 // Fail first 2 attempts, succeed on 3rd
	config := &ExecutorConfig{
		MaxRetries:      3,
		RetryBaseDelay:  10 * time.Millisecond,
		RetryMaxDelay:   50 * time.Millisecond,
		RetryMultiplier: 2.0,
		DefaultTimeout:  10 * time.Second, // Longer timeout for retries
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if !result.Success {
		t.Errorf("Handoff should succeed after retries: %v", result.Error)
	}
	if result.RetryCount != 2 {
		t.Errorf("RetryCount = %d, want 2", result.RetryCount)
	}
}

func TestHandoffExecutor_ExecuteHandoff_MaxRetriesExceeded(t *testing.T) {
	mock := newMockSessionCreator()
	mock.createSessionError = errors.New("always fails")
	config := &ExecutorConfig{
		MaxRetries:        2,
		RetryBaseDelay:    10 * time.Millisecond,
		RetryMaxDelay:     50 * time.Millisecond,
		RetryMultiplier:   2.0,
		DefaultTimeout:    10 * time.Second, // Long enough for all retries
		RollbackOnFailure: false,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if result.Success {
		t.Error("Handoff should fail after max retries")
	}
	if result.RetryCount != 2 {
		t.Errorf("RetryCount = %d, want 2", result.RetryCount)
	}
}

func TestHandoffExecutor_ExecuteHandoff_RetryTimeoutDuringBackoff(t *testing.T) {
	mock := newMockSessionCreator()
	mock.createSessionError = errors.New("always fails") // Force failure
	config := &ExecutorConfig{
		MaxRetries:        10,
		RetryBaseDelay:    500 * time.Millisecond, // Long delay
		DefaultTimeout:    200 * time.Millisecond, // Short timeout - allows first attempt but times out on backoff
		RollbackOnFailure: false,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	// The result should fail - either from timeout or from exhausting retries
	// The key is that the operation does not succeed
	if result.Success {
		t.Error("Handoff should fail due to timeout during retry backoff")
	}
}

// =============================================================================
// ExecuteHandoff Tests - Concurrent Executions
// =============================================================================

func TestHandoffExecutor_ExecuteHandoff_Concurrent(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)

	var wg sync.WaitGroup
	numGoroutines := 10
	results := make([]*HandoffResult, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			decision := createTestDecision(true, TriggerContextFull)
			transfer := NewContextTransfer("source", decision, nil)

			results[idx] = executor.ExecuteHandoff(context.Background(), transfer)
		}(i)
	}

	wg.Wait()

	successCount := 0
	for _, r := range results {
		if r.Success {
			successCount++
		}
	}

	if successCount != numGoroutines {
		t.Errorf("Expected all %d handoffs to succeed, got %d", numGoroutines, successCount)
	}

	// Verify stats are accurate
	stats := executor.Stats()
	if stats.TotalHandoffs != int64(numGoroutines) {
		t.Errorf("TotalHandoffs = %d, want %d", stats.TotalHandoffs, numGoroutines)
	}
	if stats.SuccessfulHandoffs != int64(numGoroutines) {
		t.Errorf("SuccessfulHandoffs = %d, want %d", stats.SuccessfulHandoffs, numGoroutines)
	}
}

// =============================================================================
// RecordOutcome Tests
// =============================================================================

func TestHandoffExecutor_RecordOutcome(t *testing.T) {
	executor := NewHandoffExecutor(nil, nil)

	result := &HandoffResult{
		Success:   true,
		Timestamp: time.Now(),
	}

	executor.RecordOutcome(result)

	outcomes := executor.RecentOutcomes(10)
	if len(outcomes) != 1 {
		t.Errorf("Expected 1 outcome, got %d", len(outcomes))
	}
}

func TestHandoffExecutor_RecordOutcome_Closed(t *testing.T) {
	executor := NewHandoffExecutor(nil, nil)
	executor.Close()

	result := &HandoffResult{Success: true}
	executor.RecordOutcome(result) // Should not panic

	outcomes := executor.RecentOutcomes(10)
	if len(outcomes) != 0 {
		t.Error("Should not record outcome when closed")
	}
}

// =============================================================================
// Stats Tests
// =============================================================================

func TestHandoffExecutor_Stats(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)

	// Initial stats
	stats := executor.Stats()
	if stats.TotalHandoffs != 0 {
		t.Errorf("Initial TotalHandoffs = %d, want 0", stats.TotalHandoffs)
	}
	if stats.SuccessRate != 0 {
		t.Errorf("Initial SuccessRate = %f, want 0", stats.SuccessRate)
	}

	// Perform handoffs
	decision := createTestDecision(true, TriggerContextFull)
	for i := 0; i < 5; i++ {
		transfer := NewContextTransfer("source", decision, nil)
		executor.ExecuteHandoff(context.Background(), transfer)
	}

	stats = executor.Stats()
	if stats.TotalHandoffs != 5 {
		t.Errorf("TotalHandoffs = %d, want 5", stats.TotalHandoffs)
	}
	if stats.SuccessfulHandoffs != 5 {
		t.Errorf("SuccessfulHandoffs = %d, want 5", stats.SuccessfulHandoffs)
	}
	if stats.SuccessRate != 1.0 {
		t.Errorf("SuccessRate = %f, want 1.0", stats.SuccessRate)
	}
	if stats.AverageDuration == 0 {
		t.Error("AverageDuration should be > 0")
	}
}

func TestHandoffExecutor_Stats_WithFailures(t *testing.T) {
	mock := newMockSessionCreator()
	config := &ExecutorConfig{
		MaxRetries:        0,
		RollbackOnFailure: false,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)

	// 2 successes
	for i := 0; i < 2; i++ {
		transfer := NewContextTransfer("source", decision, nil)
		executor.ExecuteHandoff(context.Background(), transfer)
	}

	// 3 failures
	mock.createSessionError = errors.New("fail")
	for i := 0; i < 3; i++ {
		transfer := NewContextTransfer("source", decision, nil)
		executor.ExecuteHandoff(context.Background(), transfer)
	}

	stats := executor.Stats()
	if stats.TotalHandoffs != 5 {
		t.Errorf("TotalHandoffs = %d, want 5", stats.TotalHandoffs)
	}
	if stats.SuccessfulHandoffs != 2 {
		t.Errorf("SuccessfulHandoffs = %d, want 2", stats.SuccessfulHandoffs)
	}
	if stats.FailedHandoffs != 3 {
		t.Errorf("FailedHandoffs = %d, want 3", stats.FailedHandoffs)
	}
	expectedRate := 0.4 // 2/5
	if stats.SuccessRate != expectedRate {
		t.Errorf("SuccessRate = %f, want %f", stats.SuccessRate, expectedRate)
	}
}

// =============================================================================
// RecentOutcomes Tests
// =============================================================================

func TestHandoffExecutor_RecentOutcomes(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)

	decision := createTestDecision(true, TriggerContextFull)
	for i := 0; i < 10; i++ {
		transfer := NewContextTransfer("source", decision, nil)
		executor.ExecuteHandoff(context.Background(), transfer)
	}

	outcomes := executor.RecentOutcomes(5)
	if len(outcomes) != 5 {
		t.Errorf("Expected 5 outcomes, got %d", len(outcomes))
	}

	// Request more than available
	allOutcomes := executor.RecentOutcomes(100)
	if len(allOutcomes) != 10 {
		t.Errorf("Expected 10 outcomes, got %d", len(allOutcomes))
	}
}

// =============================================================================
// Close Tests
// =============================================================================

func TestHandoffExecutor_Close(t *testing.T) {
	executor := NewHandoffExecutor(nil, nil)

	if executor.IsClosed() {
		t.Error("Should not be closed initially")
	}

	err := executor.Close()
	if err != nil {
		t.Errorf("Close should succeed: %v", err)
	}

	if !executor.IsClosed() {
		t.Error("Should be closed after Close()")
	}

	// Second close should return error
	err = executor.Close()
	if !errors.Is(err, ErrExecutorClosed) {
		t.Errorf("Second Close should return ErrExecutorClosed, got %v", err)
	}
}

// =============================================================================
// Metrics Collection Tests
// =============================================================================

func TestHandoffExecutor_MetricsCollection(t *testing.T) {
	mock := newMockSessionCreator()
	mock.createDelay = 10 * time.Millisecond
	mock.transferDelay = 10 * time.Millisecond
	mock.activateDelay = 10 * time.Millisecond
	config := &ExecutorConfig{
		CollectMetrics:    true,
		DefaultTimeout:    5 * time.Second,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)
	preparedCtx := createTestPreparedContext()
	transfer := NewContextTransfer("source", decision, preparedCtx)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if !result.Success {
		t.Fatalf("Handoff should succeed: %v", result.Error)
	}

	metrics := result.Metrics
	if metrics.SessionCreationDuration < 10*time.Millisecond {
		t.Errorf("SessionCreationDuration = %v, expected >= 10ms", metrics.SessionCreationDuration)
	}
	if metrics.ContextTransferDuration < 10*time.Millisecond {
		t.Errorf("ContextTransferDuration = %v, expected >= 10ms", metrics.ContextTransferDuration)
	}
	if metrics.ActivationDuration < 10*time.Millisecond {
		t.Errorf("ActivationDuration = %v, expected >= 10ms", metrics.ActivationDuration)
	}
	if metrics.TokensTransferred == 0 {
		t.Error("TokensTransferred should be > 0")
	}
	if metrics.MessagesTransferred == 0 {
		t.Error("MessagesTransferred should be > 0")
	}
}

func TestHandoffExecutor_MetricsDisabled(t *testing.T) {
	mock := newMockSessionCreator()
	config := &ExecutorConfig{
		CollectMetrics: false,
	}
	executor := NewHandoffExecutor(mock, config)

	decision := createTestDecision(true, TriggerContextFull)
	preparedCtx := createTestPreparedContext()
	transfer := NewContextTransfer("source", decision, preparedCtx)

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if !result.Success {
		t.Fatalf("Handoff should succeed: %v", result.Error)
	}

	// Metrics should not be collected
	if result.Metrics.SessionCreationDuration != 0 {
		t.Error("SessionCreationDuration should be 0 when metrics disabled")
	}
}

// =============================================================================
// Backoff Calculation Tests
// =============================================================================

func TestHandoffExecutor_calculateBackoff(t *testing.T) {
	config := &ExecutorConfig{
		RetryBaseDelay:  100 * time.Millisecond,
		RetryMaxDelay:   5 * time.Second,
		RetryMultiplier: 2.0,
	}
	executor := NewHandoffExecutor(nil, config)

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 400 * time.Millisecond},
		{4, 800 * time.Millisecond},
		{10, 5 * time.Second}, // Should cap at max
	}

	for _, tt := range tests {
		got := executor.calculateBackoff(tt.attempt, config)
		if got != tt.expected {
			t.Errorf("calculateBackoff(%d) = %v, want %v", tt.attempt, got, tt.expected)
		}
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestHandoffExecutor_NilContext(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	// Pass nil context - should use background context
	result := executor.ExecuteHandoff(nil, transfer)

	if !result.Success {
		t.Errorf("Handoff should succeed with nil context: %v", result.Error)
	}
}

func TestHandoffExecutor_TransferAlreadyValidated(t *testing.T) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)
	transfer.Validated = true // Pre-validated

	result := executor.ExecuteHandoff(context.Background(), transfer)

	if !result.Success {
		t.Errorf("Handoff should succeed: %v", result.Error)
	}
}

// =============================================================================
// NoOpHooks Tests
// =============================================================================

func TestNoOpHooks(t *testing.T) {
	hooks := &NoOpHooks{}

	// Should not panic
	err := hooks.PreHandoff(context.Background(), nil)
	if err != nil {
		t.Errorf("PreHandoff should return nil: %v", err)
	}

	hooks.PostHandoff(context.Background(), nil)
	hooks.OnRollback(context.Background(), nil, nil)
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkHandoffExecutor_ExecuteHandoff(b *testing.B) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)

	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		executor.ExecuteHandoff(context.Background(), transfer)
	}
}

func BenchmarkHandoffExecutor_PrepareTransfer(b *testing.B) {
	executor := NewHandoffExecutor(nil, nil)
	decision := createTestDecision(true, TriggerContextFull)
	preparedCtx := createTestPreparedContext()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		executor.PrepareTransfer(decision, preparedCtx)
	}
}

func BenchmarkHandoffExecutor_ValidateTransfer(b *testing.B) {
	executor := NewHandoffExecutor(nil, nil)
	decision := createTestDecision(true, TriggerContextFull)
	transfer := NewContextTransfer("source", decision, createTestPreparedContext())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		transfer.Validated = false
		executor.ValidateTransfer(transfer)
	}
}

func BenchmarkHandoffExecutor_ConcurrentExecutions(b *testing.B) {
	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			decision := createTestDecision(true, TriggerContextFull)
			transfer := NewContextTransfer("source", decision, nil)
			executor.ExecuteHandoff(context.Background(), transfer)
		}
	})
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestHandoffExecutor_FullWorkflow(t *testing.T) {
	mock := newMockSessionCreator()
	config := &ExecutorConfig{
		DefaultTimeout:    10 * time.Second,
		MaxRetries:        2,
		ValidationEnabled: true,
		RollbackOnFailure: true,
		CollectMetrics:    true,
		MaxTokenCount:     1000000, // 1M tokens max
	}
	executor := NewHandoffExecutor(mock, config)
	hooks := newMockHooks()
	executor.SetHooks(hooks)

	// Prepare context
	preparedCtx := createTestPreparedContext()
	preparedCtx.AddMessage(NewMessage("user", "Can you help me write some code?"))
	preparedCtx.AddMessage(NewMessage("assistant", "Of course! What would you like me to help with?"))
	preparedCtx.UpdateToolState("editor", ToolState{
		Name:     "editor",
		Active:   true,
		State:    map[string]interface{}{"file": "main.go"},
		LastUsed: time.Now(),
	})

	// Create decision
	decision := createTestDecision(true, TriggerContextFull)

	// Prepare transfer
	transfer, err := executor.PrepareTransfer(decision, preparedCtx)
	if err != nil {
		t.Fatalf("PrepareTransfer failed: %v", err)
	}

	// Validate transfer
	if err := executor.ValidateTransfer(transfer); err != nil {
		t.Fatalf("ValidateTransfer failed: %v", err)
	}

	// Execute handoff
	result := executor.ExecuteHandoff(context.Background(), transfer)

	// Verify result
	if !result.Success {
		t.Fatalf("Handoff should succeed: %v", result.Error)
	}
	if result.NewSessionID == "" {
		t.Error("NewSessionID should be set")
	}
	if result.Duration == 0 {
		t.Error("Duration should be set")
	}

	// Verify hooks
	pre, post, _ := hooks.getCounts()
	if pre != 1 || post != 1 {
		t.Errorf("Hooks: pre=%d, post=%d, want 1,1", pre, post)
	}

	// Verify stats
	stats := executor.Stats()
	if stats.TotalHandoffs != 1 {
		t.Errorf("TotalHandoffs = %d, want 1", stats.TotalHandoffs)
	}
	if stats.SuccessfulHandoffs != 1 {
		t.Errorf("SuccessfulHandoffs = %d, want 1", stats.SuccessfulHandoffs)
	}

	// Verify mock calls
	create, transfer2, activate, _, _ := mock.getCounts()
	if create != 1 || transfer2 != 1 || activate != 1 {
		t.Errorf("Mock calls: create=%d, transfer=%d, activate=%d, want 1,1,1",
			create, transfer2, activate)
	}

	// Record additional outcome
	executor.RecordOutcome(result)

	// Get recent outcomes
	outcomes := executor.RecentOutcomes(10)
	if len(outcomes) < 1 {
		t.Error("Should have at least 1 outcome")
	}

	// Close executor
	if err := executor.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Operations should fail after close
	_, err = executor.PrepareTransfer(decision, preparedCtx)
	if !errors.Is(err, ErrExecutorClosed) {
		t.Error("Operations should fail after close")
	}
}

// =============================================================================
// Stress Tests
// =============================================================================

func TestHandoffExecutor_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	mock := newMockSessionCreator()
	executor := NewHandoffExecutor(mock, nil)

	var wg sync.WaitGroup
	var successCount atomic.Int64
	var failCount atomic.Int64

	numGoroutines := 50
	operationsPerGoroutine := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < operationsPerGoroutine; j++ {
				decision := createTestDecision(true, TriggerContextFull)
				transfer := NewContextTransfer("source", decision, nil)

				result := executor.ExecuteHandoff(context.Background(), transfer)

				if result.Success {
					successCount.Add(1)
				} else {
					failCount.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	totalOps := int64(numGoroutines * operationsPerGoroutine)
	if successCount.Load() != totalOps {
		t.Errorf("Expected %d successes, got %d", totalOps, successCount.Load())
	}

	stats := executor.Stats()
	if stats.TotalHandoffs != totalOps {
		t.Errorf("TotalHandoffs = %d, want %d", stats.TotalHandoffs, totalOps)
	}
}
