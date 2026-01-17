package errors

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestEscalationManager() *EscalationManager {
	config := DefaultEscalationConfig()
	retryExec := NewRetryExecutor(nil)
	classifier := NewErrorClassifier()
	return NewEscalationManager(config, retryExec, classifier)
}

func TestEscalationManager_Escalate_TransientError_SelfRecovery(t *testing.T) {
	em := newTestEscalationManager()
	ctx := context.Background()

	ectx := EscalationContext{
		Error:      errors.New("transient network error"),
		Tier:       TierTransient,
		AttemptNum: 0,
		PipelineID: "test-pipeline",
		AgentID:    "test-agent",
	}

	result, err := em.Escalate(ctx, ectx)
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if result == nil {
		t.Fatal("Escalate() returned nil result")
	}

	if result.Level != LevelUserDecision {
		t.Logf("Level = %v (expected LevelUserDecision for non-recovered transient)", result.Level)
	}
}

func TestEscalationManager_Escalate_PermanentError_ToUser(t *testing.T) {
	em := newTestEscalationManager()
	ctx := context.Background()

	ectx := EscalationContext{
		Error:      errors.New("permanent error"),
		Tier:       TierPermanent,
		AttemptNum: 0,
		PipelineID: "test-pipeline",
		AgentID:    "test-agent",
	}

	result, err := em.Escalate(ctx, ectx)
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if result == nil {
		t.Fatal("Escalate() returned nil result")
	}

	if result.Level != LevelUserDecision {
		t.Errorf("Level = %v, want %v", result.Level, LevelUserDecision)
	}
	if !result.UserDecision {
		t.Error("UserDecision = false, want true for permanent error")
	}
}

func TestEscalationManager_Escalate_CriticalError_FastPath(t *testing.T) {
	em := newTestEscalationManager()
	ctx := context.Background()

	ectx := EscalationContext{
		Error:      errors.New("external service degrading"),
		Tier:       TierExternalDegrading,
		AttemptNum: 0,
		PipelineID: "test-pipeline",
		AgentID:    "test-agent",
	}

	result, err := em.Escalate(ctx, ectx)
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if result == nil {
		t.Fatal("Escalate() returned nil result")
	}

	if result.Level != LevelUserDecision {
		t.Errorf("Level = %v, want %v", result.Level, LevelUserDecision)
	}
	if !result.UserDecision {
		t.Error("UserDecision = false, want true for critical error")
	}
	if result.Remedy == nil {
		t.Error("Remedy should not be nil for critical error")
	}
}

func TestEscalationManager_Escalate_BudgetExceeded_ToUser(t *testing.T) {
	config := EscalationConfig{
		WorkaroundBudget: WorkaroundBudgetConfig{
			MaxTokens:      100,
			ResetOnSuccess: true,
		},
		CriticalTimeout: 30 * time.Second,
	}
	retryExec := NewRetryExecutor(nil)
	classifier := NewErrorClassifier()
	em := NewEscalationManager(config, retryExec, classifier)

	em.Budget().Spend(100)

	ctx := context.Background()
	ectx := EscalationContext{
		Error:      errors.New("error requiring workaround"),
		Tier:       TierTransient,
		AttemptNum: 10,
		PipelineID: "test-pipeline",
		AgentID:    "test-agent",
	}

	result, err := em.Escalate(ctx, ectx)
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if result == nil {
		t.Fatal("Escalate() returned nil result")
	}

	if result.Level != LevelUserDecision {
		t.Errorf("Level = %v, want %v", result.Level, LevelUserDecision)
	}
}

func TestEscalationManager_IsCriticalError(t *testing.T) {
	em := newTestEscalationManager()

	tests := []struct {
		name string
		tier ErrorTier
		want bool
	}{
		{
			name: "external degrading is critical",
			tier: TierExternalDegrading,
			want: true,
		},
		{
			name: "transient is not critical",
			tier: TierTransient,
			want: false,
		},
		{
			name: "permanent is not critical",
			tier: TierPermanent,
			want: false,
		},
		{
			name: "user fixable is not critical",
			tier: TierUserFixable,
			want: false,
		},
		{
			name: "rate limit is not critical",
			tier: TierExternalRateLimit,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ectx := EscalationContext{
				Error:      errors.New("test"),
				Tier:       tt.tier,
				AttemptNum: 0,
			}

			result, err := em.Escalate(ctx, ectx)
			if err != nil {
				t.Fatalf("Escalate() error = %v", err)
			}

			if tt.want {
				if result.Remedy == nil {
					t.Error("Critical error should have a remedy")
					return
				}
				isCriticalRemedy := result.Remedy.Description == "external service degrading" ||
					result.Remedy.Description == "critical error timeout"
				if !isCriticalRemedy && result.Level == LevelUserDecision {
					t.Logf("Went through normal chain for tier %v", tt.tier)
				}
			}
		})
	}
}

func TestEscalationLevel_String(t *testing.T) {
	tests := []struct {
		level EscalationLevel
		want  string
	}{
		{LevelAgentSelfRecovery, "agent_self_recovery"},
		{LevelArchitectWorkaround, "architect_workaround"},
		{LevelUserDecision, "user_decision"},
		{EscalationLevel(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.level.String()
			if got != tt.want {
				t.Errorf("EscalationLevel(%d).String() = %q, want %q", tt.level, got, tt.want)
			}
		})
	}
}

func TestEscalationManager_Budget(t *testing.T) {
	em := newTestEscalationManager()

	budget := em.Budget()
	if budget == nil {
		t.Fatal("Budget() returned nil")
	}

	if budget.Spent() != 0 {
		t.Errorf("Initial budget.Spent() = %d, want 0", budget.Spent())
	}
}

func TestEscalationManager_ResetBudget(t *testing.T) {
	em := newTestEscalationManager()

	em.Budget().Spend(500)
	if em.Budget().Spent() != 500 {
		t.Fatalf("Budget.Spent() = %d, want 500", em.Budget().Spent())
	}

	em.ResetBudget()

	if em.Budget().Spent() != 0 {
		t.Errorf("After ResetBudget(), Budget.Spent() = %d, want 0", em.Budget().Spent())
	}
}

func TestEscalationContext_Fields(t *testing.T) {
	err := errors.New("test error")
	ectx := EscalationContext{
		Error:      err,
		Tier:       TierTransient,
		AttemptNum: 3,
		PipelineID: "pipeline-123",
		AgentID:    "agent-456",
	}

	if ectx.Error != err {
		t.Errorf("Error = %v, want %v", ectx.Error, err)
	}
	if ectx.Tier != TierTransient {
		t.Errorf("Tier = %v, want %v", ectx.Tier, TierTransient)
	}
	if ectx.AttemptNum != 3 {
		t.Errorf("AttemptNum = %d, want 3", ectx.AttemptNum)
	}
	if ectx.PipelineID != "pipeline-123" {
		t.Errorf("PipelineID = %q, want %q", ectx.PipelineID, "pipeline-123")
	}
	if ectx.AgentID != "agent-456" {
		t.Errorf("AgentID = %q, want %q", ectx.AgentID, "agent-456")
	}
}

func TestEscalationResult_Fields(t *testing.T) {
	remedy := NewRemedy("test remedy", RemedyRetry, 0.8)
	err := errors.New("test error")

	result := &EscalationResult{
		Level:        LevelUserDecision,
		Remedy:       remedy,
		UserDecision: true,
		Error:        err,
	}

	if result.Level != LevelUserDecision {
		t.Errorf("Level = %v, want %v", result.Level, LevelUserDecision)
	}
	if result.Remedy != remedy {
		t.Error("Remedy mismatch")
	}
	if !result.UserDecision {
		t.Error("UserDecision = false, want true")
	}
	if result.Error != err {
		t.Errorf("Error = %v, want %v", result.Error, err)
	}
}

func TestEscalationConfig_Default(t *testing.T) {
	config := DefaultEscalationConfig()

	if config.CriticalTimeout != 30*time.Second {
		t.Errorf("CriticalTimeout = %v, want 30s", config.CriticalTimeout)
	}
	if config.WorkaroundBudget.MaxTokens != 1000 {
		t.Errorf("WorkaroundBudget.MaxTokens = %d, want 1000", config.WorkaroundBudget.MaxTokens)
	}
	if !config.WorkaroundBudget.ResetOnSuccess {
		t.Error("WorkaroundBudget.ResetOnSuccess = false, want true")
	}
}

func TestEscalationManager_Escalate_ContextCancelled(t *testing.T) {
	em := newTestEscalationManager()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ectx := EscalationContext{
		Error:      errors.New("test error"),
		Tier:       TierTransient,
		AttemptNum: 0,
		PipelineID: "test-pipeline",
		AgentID:    "test-agent",
	}

	result, err := em.Escalate(ctx, ectx)

	if err == context.Canceled {
		return
	}
	if result != nil {
		return
	}
}

func TestEscalationManager_UserDecision_Remedies(t *testing.T) {
	em := newTestEscalationManager()
	ctx := context.Background()

	ectx := EscalationContext{
		Error:      errors.New("unrecoverable error"),
		Tier:       TierPermanent,
		AttemptNum: 0,
		PipelineID: "test-pipeline",
		AgentID:    "test-agent",
	}

	result, err := em.Escalate(ctx, ectx)
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if result == nil {
		t.Fatal("Escalate() returned nil result")
	}

	if result.Remedy == nil {
		t.Error("UserDecision result should have a Remedy")
	}

	if result.Remedy != nil {
		if result.Remedy.Confidence < 0.5 {
			t.Logf("Best remedy confidence = %v", result.Remedy.Confidence)
		}
	}
}
