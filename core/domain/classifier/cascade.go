package classifier

import (
	"context"
	"sort"
	"sync"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
)

const (
	defaultSingleDomainThreshold = 0.75
	defaultCrossDomainThreshold  = 0.65
)

// ClassificationCascade orchestrates multiple classification stages.
// Flow: Lexical → Embedding → Context → LLM (fallback)
type ClassificationCascade struct {
	stages                []ClassificationStage
	config                *domain.DomainConfig
	singleDomainThreshold float64
	crossDomainThreshold  float64
	mu                    sync.RWMutex
}

// CascadeConfig configures the classification cascade.
type CascadeConfig struct {
	SingleDomainThreshold float64
	CrossDomainThreshold  float64
}

// NewClassificationCascade creates a new ClassificationCascade.
func NewClassificationCascade(
	config *domain.DomainConfig,
	cascadeConfig *CascadeConfig,
) *ClassificationCascade {
	cc := &ClassificationCascade{
		stages:                make([]ClassificationStage, 0),
		config:                config,
		singleDomainThreshold: defaultSingleDomainThreshold,
		crossDomainThreshold:  defaultCrossDomainThreshold,
	}

	if cascadeConfig != nil {
		cc.applyCascadeConfig(cascadeConfig)
	}

	if config != nil {
		cc.applyDomainConfig(config)
	}

	return cc
}

func (c *ClassificationCascade) applyCascadeConfig(config *CascadeConfig) {
	if config.SingleDomainThreshold > 0 {
		c.singleDomainThreshold = config.SingleDomainThreshold
	}
	if config.CrossDomainThreshold > 0 {
		c.crossDomainThreshold = config.CrossDomainThreshold
	}
}

func (c *ClassificationCascade) applyDomainConfig(config *domain.DomainConfig) {
	if config.SingleDomainThreshold > 0 {
		c.singleDomainThreshold = config.SingleDomainThreshold
	}
	if config.CrossDomainThreshold > 0 {
		c.crossDomainThreshold = config.CrossDomainThreshold
	}
}

// AddStage adds a classification stage to the cascade.
func (c *ClassificationCascade) AddStage(stage ClassificationStage) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stages = append(c.stages, stage)
	c.sortStages()
}

func (c *ClassificationCascade) sortStages() {
	sort.Slice(c.stages, func(i, j int) bool {
		return c.stages[i].Priority() < c.stages[j].Priority()
	})
}

// Classify runs the classification cascade and returns a DomainContext.
func (c *ClassificationCascade) Classify(
	ctx context.Context,
	query string,
	caller concurrency.AgentType,
) (*domain.DomainContext, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	c.mu.RLock()
	stages := make([]ClassificationStage, len(c.stages))
	copy(stages, c.stages)
	c.mu.RUnlock()

	accumulated := NewStageResult()
	var methods []string

	for _, stage := range stages {
		result, err := c.runStage(ctx, stage, query, caller)
		if err != nil {
			continue // Non-fatal: skip stage
		}

		accumulated.Merge(result)
		methods = append(methods, result.Method)

		if c.shouldTerminate(accumulated) {
			break
		}
	}

	return c.buildDomainContext(query, accumulated, methods), nil
}

func (c *ClassificationCascade) runStage(
	ctx context.Context,
	stage ClassificationStage,
	query string,
	caller concurrency.AgentType,
) (*StageResult, error) {
	return stage.Classify(ctx, query, caller)
}

func (c *ClassificationCascade) shouldTerminate(result *StageResult) bool {
	if result.IsEmpty() {
		return false
	}

	_, maxConf := result.HighestConfidence()
	return maxConf >= c.singleDomainThreshold && result.DomainCount() == 1
}

func (c *ClassificationCascade) buildDomainContext(
	query string,
	result *StageResult,
	methods []string,
) *domain.DomainContext {
	dc := domain.NewDomainContext(query)

	if result.IsEmpty() {
		return c.applyDefaults(dc)
	}

	dc.DetectedDomains = result.Domains
	dc.DomainConfidences = result.Confidences
	dc.ClassificationMethod = c.joinMethods(methods)

	c.populatePrimaryAndSecondary(dc, result)
	c.collectSignals(dc, result)
	c.determineCrossDomain(dc)

	return dc
}

func (c *ClassificationCascade) applyDefaults(dc *domain.DomainContext) *domain.DomainContext {
	if c.config != nil && len(c.config.DefaultDomains) > 0 {
		dc.DetectedDomains = c.config.DefaultDomains
		dc.PrimaryDomain = c.config.DefaultDomains[0]
		dc.ClassificationMethod = "default"
	}
	return dc
}

func (c *ClassificationCascade) joinMethods(methods []string) string {
	if len(methods) == 0 {
		return ""
	}
	if len(methods) == 1 {
		return methods[0]
	}
	return methods[len(methods)-1] // Return final contributing method
}

func (c *ClassificationCascade) populatePrimaryAndSecondary(
	dc *domain.DomainContext,
	result *StageResult,
) {
	primary, _ := result.HighestConfidence()
	dc.PrimaryDomain = primary

	for _, d := range result.Domains {
		if d != primary && result.Confidences[d] >= c.crossDomainThreshold {
			dc.SecondaryDomains = append(dc.SecondaryDomains, d)
		}
	}
}

func (c *ClassificationCascade) collectSignals(
	dc *domain.DomainContext,
	result *StageResult,
) {
	for d, sigs := range result.Signals {
		dc.Signals[d] = sigs
	}
}

func (c *ClassificationCascade) determineCrossDomain(dc *domain.DomainContext) {
	dc.IsCrossDomain = len(dc.SecondaryDomains) > 0
}

// StageCount returns the number of stages in the cascade.
func (c *ClassificationCascade) StageCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.stages)
}

// GetStages returns a copy of the stages (for inspection/testing).
func (c *ClassificationCascade) GetStages() []ClassificationStage {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stages := make([]ClassificationStage, len(c.stages))
	copy(stages, c.stages)
	return stages
}

// UpdateThresholds updates the classification thresholds.
func (c *ClassificationCascade) UpdateThresholds(single, cross float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if single > 0 {
		c.singleDomainThreshold = single
	}
	if cross > 0 {
		c.crossDomainThreshold = cross
	}
}

// GetThresholds returns the current thresholds.
func (c *ClassificationCascade) GetThresholds() (single, cross float64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.singleDomainThreshold, c.crossDomainThreshold
}
