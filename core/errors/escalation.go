// Package errors implements a 5-tier error taxonomy with classification and handling behavior.
package errors

import (
	"context"
	"time"
)

// EscalationLevel represents the escalation tier.
type EscalationLevel int

const (
	// LevelAgentSelfRecovery is Level 1: agent attempts self-recovery.
	LevelAgentSelfRecovery EscalationLevel = iota + 1

	// LevelArchitectWorkaround is Level 2: architect attempts workaround.
	LevelArchitectWorkaround

	// LevelUserDecision is Level 3: escalate to user for decision.
	LevelUserDecision
)

var escalationLevelNames = map[EscalationLevel]string{
	LevelAgentSelfRecovery:   "agent_self_recovery",
	LevelArchitectWorkaround: "architect_workaround",
	LevelUserDecision:        "user_decision",
}

func (l EscalationLevel) String() string {
	if name, ok := escalationLevelNames[l]; ok {
		return name
	}
	return "unknown"
}

// EscalationConfig configures escalation behavior.
type EscalationConfig struct {
	WorkaroundBudget WorkaroundBudgetConfig `yaml:"workaround_budget"`
	CriticalTimeout  time.Duration          `yaml:"critical_timeout"`
}

// DefaultEscalationConfig returns the default configuration.
func DefaultEscalationConfig() EscalationConfig {
	return EscalationConfig{
		WorkaroundBudget: DefaultWorkaroundBudgetConfig(),
		CriticalTimeout:  30 * time.Second,
	}
}

// EscalationContext provides context for an escalation decision.
type EscalationContext struct {
	Error      error
	Tier       ErrorTier
	AttemptNum int
	PipelineID string
	AgentID    string
}

// EscalationResult contains the outcome of an escalation.
type EscalationResult struct {
	Level        EscalationLevel
	Remedy       *Remedy
	UserDecision bool
	Error        error
}

// EscalationManager orchestrates the 3-level escalation chain.
type EscalationManager struct {
	config     EscalationConfig
	budget     *WorkaroundBudget
	retryExec  *RetryExecutor
	classifier *ErrorClassifier
}

// NewEscalationManager creates a new EscalationManager.
func NewEscalationManager(
	config EscalationConfig,
	retryExec *RetryExecutor,
	classifier *ErrorClassifier,
) *EscalationManager {
	return &EscalationManager{
		config:     config,
		budget:     NewWorkaroundBudget(config.WorkaroundBudget),
		retryExec:  retryExec,
		classifier: classifier,
	}
}

// Escalate determines the appropriate response to an error.
func (em *EscalationManager) Escalate(
	ctx context.Context,
	ectx EscalationContext,
) (*EscalationResult, error) {
	if em.isCriticalError(ectx.Tier) {
		return em.handleCriticalError(ctx, ectx)
	}
	return em.runEscalationChain(ctx, ectx)
}

// runEscalationChain attempts each level in sequence.
func (em *EscalationManager) runEscalationChain(
	ctx context.Context,
	ectx EscalationContext,
) (*EscalationResult, error) {
	if result, err := em.tryLevel1(ctx, ectx); result != nil || err != nil {
		return result, err
	}
	if result, err := em.tryLevel2(ctx, ectx); result != nil || err != nil {
		return result, err
	}
	return em.escalateToUser(ectx)
}

// tryLevel1 attempts agent self-recovery.
func (em *EscalationManager) tryLevel1(
	ctx context.Context,
	ectx EscalationContext,
) (*EscalationResult, error) {
	recovered, err := em.tryAgentSelfRecovery(ctx, ectx)
	if err != nil {
		return nil, err
	}
	if recovered {
		return em.buildLevel1Result(), nil
	}
	return nil, nil
}

// buildLevel1Result creates a successful Level 1 result.
func (em *EscalationManager) buildLevel1Result() *EscalationResult {
	return &EscalationResult{
		Level:        LevelAgentSelfRecovery,
		Remedy:       NewRemedy("self-recovered via retry", RemedyRetry, 1.0),
		UserDecision: false,
		Error:        nil,
	}
}

// tryLevel2 attempts architect workaround.
func (em *EscalationManager) tryLevel2(
	ctx context.Context,
	ectx EscalationContext,
) (*EscalationResult, error) {
	remedy, err := em.tryArchitectWorkaround(ctx, ectx)
	if err != nil {
		return nil, err
	}
	if remedy != nil {
		return em.buildLevel2Result(remedy), nil
	}
	return nil, nil
}

// buildLevel2Result creates a successful Level 2 result.
func (em *EscalationManager) buildLevel2Result(remedy *Remedy) *EscalationResult {
	return &EscalationResult{
		Level:        LevelArchitectWorkaround,
		Remedy:       remedy,
		UserDecision: false,
		Error:        nil,
	}
}

// tryAgentSelfRecovery attempts Level 1 self-recovery via retry.
func (em *EscalationManager) tryAgentSelfRecovery(
	ctx context.Context,
	ectx EscalationContext,
) (bool, error) {
	if !em.canRetry(ectx) {
		return false, nil
	}
	return em.attemptRetry(ctx, ectx)
}

// canRetry checks if retry is allowed for the error tier.
func (em *EscalationManager) canRetry(ectx EscalationContext) bool {
	policy := GetRetryPolicy(ectx.Tier)
	return ectx.AttemptNum < policy.MaxAttempts
}

// attemptRetry executes a retry attempt.
func (em *EscalationManager) attemptRetry(
	ctx context.Context,
	ectx EscalationContext,
) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
		return false, nil
	}
}

// tryArchitectWorkaround attempts Level 2 workaround.
func (em *EscalationManager) tryArchitectWorkaround(
	ctx context.Context,
	ectx EscalationContext,
) (*Remedy, error) {
	if !em.hasWorkaroundBudget(ectx) {
		return nil, nil
	}
	return em.generateWorkaround(ctx, ectx)
}

// hasWorkaroundBudget checks if budget is available for workaround.
func (em *EscalationManager) hasWorkaroundBudget(ectx EscalationContext) bool {
	estimatedCost := 100
	return em.budget.CanSpend(estimatedCost, ectx.Tier)
}

// generateWorkaround creates a workaround remedy (placeholder).
func (em *EscalationManager) generateWorkaround(
	ctx context.Context,
	ectx EscalationContext,
) (*Remedy, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, nil
	}
}

// escalateToUser creates Level 3 user decision result.
func (em *EscalationManager) escalateToUser(
	ectx EscalationContext,
) (*EscalationResult, error) {
	remedySet := em.buildUserRemedySet(ectx)
	return em.buildUserDecisionResult(remedySet), nil
}

// buildUserRemedySet creates remedies for user to choose from.
func (em *EscalationManager) buildUserRemedySet(ectx EscalationContext) *RemedySet {
	rs := NewRemedySet(ectx.Error)
	rs.Add(NewRemedy("retry the operation", RemedyRetry, 0.5))
	rs.Add(NewRemedy("abort and report error", RemedyAbort, 0.8))
	rs.Add(NewRemedy("manual intervention required", RemedyManual, 0.3))
	return rs
}

// buildUserDecisionResult creates the Level 3 result.
func (em *EscalationManager) buildUserDecisionResult(rs *RemedySet) *EscalationResult {
	return &EscalationResult{
		Level:        LevelUserDecision,
		Remedy:       rs.Best(),
		UserDecision: true,
		Error:        nil,
	}
}

// isCriticalError checks if the tier requires fast-path escalation.
func (em *EscalationManager) isCriticalError(tier ErrorTier) bool {
	return tier == TierExternalDegrading
}

// handleCriticalError implements fast-path for critical errors.
func (em *EscalationManager) handleCriticalError(
	ctx context.Context,
	ectx EscalationContext,
) (*EscalationResult, error) {
	timeoutCtx, cancel := em.createCriticalContext(ctx)
	defer cancel()
	return em.processCriticalError(timeoutCtx, ectx)
}

// createCriticalContext creates a timeout context for critical errors.
func (em *EscalationManager) createCriticalContext(
	ctx context.Context,
) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, em.config.CriticalTimeout)
}

// processCriticalError handles the critical error with timeout.
func (em *EscalationManager) processCriticalError(
	ctx context.Context,
	ectx EscalationContext,
) (*EscalationResult, error) {
	select {
	case <-ctx.Done():
		return em.buildCriticalTimeoutResult(ectx), nil
	default:
		return em.buildCriticalEscalationResult(ectx), nil
	}
}

// buildCriticalTimeoutResult creates result when critical timeout expires.
func (em *EscalationManager) buildCriticalTimeoutResult(
	ectx EscalationContext,
) *EscalationResult {
	return &EscalationResult{
		Level:        LevelUserDecision,
		Remedy:       NewRemedy("critical error timeout", RemedyManual, 1.0),
		UserDecision: true,
		Error:        ectx.Error,
	}
}

// buildCriticalEscalationResult creates immediate user escalation.
func (em *EscalationManager) buildCriticalEscalationResult(
	ectx EscalationContext,
) *EscalationResult {
	return &EscalationResult{
		Level:        LevelUserDecision,
		Remedy:       NewRemedy("external service degrading", RemedyManual, 0.9),
		UserDecision: true,
		Error:        nil,
	}
}

// Budget returns the workaround budget for inspection.
func (em *EscalationManager) Budget() *WorkaroundBudget {
	return em.budget
}

// ResetBudget resets the workaround budget.
func (em *EscalationManager) ResetBudget() {
	em.budget.Reset()
}
