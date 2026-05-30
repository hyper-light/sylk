package claims

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	defaultValidationRuntimeConcurrency = 1
	defaultValidationRuntimeInputBytes  = 64 * 1024
	defaultValidationRuntimeOutputBytes = 64 * 1024
)

type ClaimsValidationRuntimeConfig struct {
	Dispatcher                   ValidationDispatcher
	Registry                     *ValidatorRegistry
	QualityBarEvaluator          AgentTurnEvaluator
	Scope                        ScopeProvider
	ActorID                      string
	AutoValidatePostedTestaments bool
	ConcurrencyBudget            int
	Timeout                      time.Duration
	QualityBarTimeout            time.Duration
	MaxInputBytes                int
	MaxOutputBytes               int
	MaxQualityBarInlineBytes     int
}

func (b *ClaimsBoard) ConfigureValidationRuntime(cfg ClaimsValidationRuntimeConfig) {
	b.mu.Lock()
	b.validationRuntime = cloneClaimsValidationRuntimeConfig(&cfg)
	b.mu.Unlock()
}

func (b *ClaimsBoard) ValidatePostedTestament(ctx context.Context, testamentID, actorID string) (ClaimValidationOutcome, error) {
	cfg := b.validationRuntimeSnapshot()
	if cfg == nil {
		return ClaimValidationOutcome{}, fmt.Errorf("%w: validation runtime is not configured", ErrClaimOrchestratorInvalid)
	}
	return b.validatePostedTestamentWithRuntime(ctxOrBackground(ctx), strings.TrimSpace(testamentID), strings.TrimSpace(actorID), *cfg)
}

func (b *ClaimsBoard) autoValidatePostedTestaments(ctx context.Context, testaments []Testament) {
	cfg := b.validationRuntimeSnapshot()
	if cfg == nil || !cfg.AutoValidatePostedTestaments {
		return
	}
	for i := range testaments {
		if err := b.autoValidatePostedTestament(ctx, &testaments[i], *cfg); err != nil {
			b.RecordNotificationError(err.Error())
		}
	}
}

func (b *ClaimsBoard) autoValidatePostedTestament(ctx context.Context, testament *Testament, cfg ClaimsValidationRuntimeConfig) error {
	if testament == nil {
		return nil
	}
	_, err := b.validatePostedTestamentWithRuntime(ctxOrBackground(ctx), testament.ID, b.validationActorForTestament(testament, cfg), cfg)
	return err
}

func (b *ClaimsBoard) validatePostedTestamentWithRuntime(ctx context.Context, testamentID, actorID string, cfg ClaimsValidationRuntimeConfig) (ClaimValidationOutcome, error) {
	orchestrator, err := b.claimValidationOrchestrator(cfg, actorID)
	if err != nil {
		return ClaimValidationOutcome{}, err
	}
	return orchestrator.ValidateTestament(ctx, testamentID)
}

func (b *ClaimsBoard) claimValidationOrchestrator(cfg ClaimsValidationRuntimeConfig, actorID string) (*ClaimOrchestrator, error) {
	dispatcher, err := b.validationRuntimeDispatcher(cfg)
	if err != nil {
		return nil, err
	}
	quality, err := b.validationRuntimeQualityBar(cfg, actorID)
	if err != nil {
		return nil, err
	}
	artifactOrchestrator, err := NewArtifactOrchestrator(ArtifactOrchestratorConfig{
		Board:             b,
		Dispatcher:        dispatcher,
		QualityBar:        quality,
		Scope:             firstNonNilScope(cfg.Scope, b.scope),
		ActorID:           firstNonEmpty(actorID, cfg.ActorID),
		ConcurrencyBudget: positiveOrDefault(cfg.ConcurrencyBudget, defaultValidationRuntimeConcurrency),
		Timeout:           cfg.Timeout,
	})
	if err != nil {
		return nil, err
	}
	builder, err := NewResultTestamentBuilder(ResultTestamentBuilderConfig{Board: b, ActorID: firstNonEmpty(actorID, cfg.ActorID)})
	if err != nil {
		return nil, err
	}
	return NewClaimOrchestrator(ClaimOrchestratorConfig{
		Board:                b,
		ArtifactOrchestrator: artifactOrchestrator,
		ResultBuilder:        builder,
		Scope:                firstNonNilScope(cfg.Scope, b.scope),
		ActorID:              firstNonEmpty(actorID, cfg.ActorID),
		ConcurrencyBudget:    positiveOrDefault(cfg.ConcurrencyBudget, defaultValidationRuntimeConcurrency),
		Timeout:              cfg.Timeout,
	})
}

func (b *ClaimsBoard) validationRuntimeDispatcher(cfg ClaimsValidationRuntimeConfig) (ValidationDispatcher, error) {
	if cfg.Dispatcher != nil {
		return cfg.Dispatcher, nil
	}
	if cfg.Registry == nil {
		return nil, fmt.Errorf("%w: validation dispatcher or registry is required", ErrArtifactOrchestratorInvalid)
	}
	return NewBoardValidatorDispatcher(BoardValidatorDispatcherConfig{
		Board:          b,
		Registry:       cfg.Registry,
		MaxInputBytes:  int64(positiveOrDefault(cfg.MaxInputBytes, defaultValidationRuntimeInputBytes)),
		MaxOutputBytes: int64(positiveOrDefault(cfg.MaxOutputBytes, defaultValidationRuntimeOutputBytes)),
	})
}

func (b *ClaimsBoard) validationRuntimeQualityBar(cfg ClaimsValidationRuntimeConfig, actorID string) (*QualityBarDispatcher, error) {
	if cfg.QualityBarEvaluator == nil {
		return nil, nil
	}
	return NewQualityBarDispatcher(QualityBarDispatcherConfig{
		Board:          b,
		Evaluator:      cfg.QualityBarEvaluator,
		Scope:          firstNonNilScope(cfg.Scope, b.scope),
		ActorID:        firstNonEmpty(actorID, cfg.ActorID),
		Timeout:        cfg.QualityBarTimeout,
		MaxInlineBytes: cfg.MaxQualityBarInlineBytes,
	})
}

func (b *ClaimsBoard) validationRuntimeSnapshot() *ClaimsValidationRuntimeConfig {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	cfg := cloneClaimsValidationRuntimeConfig(b.validationRuntime)
	b.mu.RUnlock()
	return cfg
}

func (b *ClaimsBoard) validationActorForTestament(testament *Testament, cfg ClaimsValidationRuntimeConfig) string {
	if testament == nil {
		return strings.TrimSpace(cfg.ActorID)
	}
	claimID := ClaimIDFromRelations(testament.Relations)
	claim, ok := b.CloneClaim(claimID)
	if ok && claim != nil {
		return firstNonEmpty(cfg.ActorID, IssuerAgentID(claim.Relations), testament.AgentID)
	}
	return firstNonEmpty(cfg.ActorID, testament.AgentID)
}

func cloneClaimsValidationRuntimeConfig(cfg *ClaimsValidationRuntimeConfig) *ClaimsValidationRuntimeConfig {
	if cfg == nil {
		return nil
	}
	cp := *cfg
	return &cp
}

func firstNonNilScope(values ...ScopeProvider) ScopeProvider {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
