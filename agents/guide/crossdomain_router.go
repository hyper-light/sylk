package guide

import (
	"context"
	"sync"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
)

type CrossDomainRoute struct {
	Agent      concurrency.AgentType
	Domain     domain.Domain
	Priority   int
	Confidence float64
}

type CrossDomainDecision struct {
	Routes          []CrossDomainRoute
	IsCrossDomain   bool
	PrimaryRoute    CrossDomainRoute
	SecondaryRoutes []CrossDomainRoute
	RoutingMethod   string
	TotalConfidence float64
}

type CrossDomainRouter struct {
	maxRoutes         int
	minConfidence     float64
	parallelThreshold float64
	routingStrategy   RoutingStrategy
}

type RoutingStrategy int

const (
	StrategyPrimaryOnly RoutingStrategy = iota
	StrategyPrimaryFirst
	StrategyParallel
	StrategyWeighted
)

type CrossDomainRouterConfig struct {
	MaxRoutes         int
	MinConfidence     float64
	ParallelThreshold float64
	Strategy          RoutingStrategy
}

func NewCrossDomainRouter(config *CrossDomainRouterConfig) *CrossDomainRouter {
	cfg := applyRouterDefaults(config)
	return &CrossDomainRouter{
		maxRoutes:         cfg.MaxRoutes,
		minConfidence:     cfg.MinConfidence,
		parallelThreshold: cfg.ParallelThreshold,
		routingStrategy:   cfg.Strategy,
	}
}

func applyRouterDefaults(config *CrossDomainRouterConfig) *CrossDomainRouterConfig {
	defaults := &CrossDomainRouterConfig{
		MaxRoutes:         3,
		MinConfidence:     0.3,
		ParallelThreshold: 0.7,
		Strategy:          StrategyPrimaryFirst,
	}

	if config == nil {
		return defaults
	}

	if config.MaxRoutes > 0 {
		defaults.MaxRoutes = config.MaxRoutes
	}
	if config.MinConfidence > 0 {
		defaults.MinConfidence = config.MinConfidence
	}
	if config.ParallelThreshold > 0 {
		defaults.ParallelThreshold = config.ParallelThreshold
	}
	defaults.Strategy = config.Strategy

	return defaults
}

func (r *CrossDomainRouter) Route(domainCtx *domain.DomainContext) *CrossDomainDecision {
	if domainCtx == nil || domainCtx.IsEmpty() {
		return r.defaultDecision()
	}

	decision := &CrossDomainDecision{
		IsCrossDomain: domainCtx.IsCrossDomain,
		Routes:        make([]CrossDomainRoute, 0),
	}

	r.buildPrimaryRoute(decision, domainCtx)
	r.buildSecondaryRoutes(decision, domainCtx)
	r.setRoutingMethod(decision)
	r.computeTotalConfidence(decision)

	return decision
}

func (r *CrossDomainRouter) defaultDecision() *CrossDomainDecision {
	return &CrossDomainDecision{
		IsCrossDomain:   false,
		Routes:          []CrossDomainRoute{},
		RoutingMethod:   "default",
		TotalConfidence: 0,
	}
}

func (r *CrossDomainRouter) buildPrimaryRoute(
	decision *CrossDomainDecision,
	domainCtx *domain.DomainContext,
) {
	primary := CrossDomainRoute{
		Agent:      domainCtx.PrimaryDomain.ToAgentType(),
		Domain:     domainCtx.PrimaryDomain,
		Priority:   0,
		Confidence: domainCtx.GetConfidence(domainCtx.PrimaryDomain),
	}

	decision.PrimaryRoute = primary
	decision.Routes = append(decision.Routes, primary)
}

func (r *CrossDomainRouter) buildSecondaryRoutes(
	decision *CrossDomainDecision,
	domainCtx *domain.DomainContext,
) {
	if !domainCtx.IsCrossDomain {
		return
	}

	secondaryRoutes := make([]CrossDomainRoute, 0, len(domainCtx.SecondaryDomains))
	priority := 1

	for _, d := range domainCtx.SecondaryDomains {
		confidence := domainCtx.GetConfidence(d)
		if confidence < r.minConfidence {
			continue
		}

		if len(secondaryRoutes) >= r.maxRoutes-1 {
			break
		}

		route := CrossDomainRoute{
			Agent:      d.ToAgentType(),
			Domain:     d,
			Priority:   priority,
			Confidence: confidence,
		}
		secondaryRoutes = append(secondaryRoutes, route)
		decision.Routes = append(decision.Routes, route)
		priority++
	}

	decision.SecondaryRoutes = secondaryRoutes
}

func (r *CrossDomainRouter) setRoutingMethod(decision *CrossDomainDecision) {
	switch r.routingStrategy {
	case StrategyPrimaryOnly:
		decision.RoutingMethod = "primary_only"
	case StrategyParallel:
		decision.RoutingMethod = "parallel"
	case StrategyWeighted:
		decision.RoutingMethod = "weighted"
	default:
		decision.RoutingMethod = r.determineMethod(decision)
	}
}

func (r *CrossDomainRouter) determineMethod(decision *CrossDomainDecision) string {
	if !decision.IsCrossDomain {
		return "primary_only"
	}

	if decision.PrimaryRoute.Confidence >= r.parallelThreshold {
		return "primary_first"
	}

	return "parallel"
}

func (r *CrossDomainRouter) computeTotalConfidence(decision *CrossDomainDecision) {
	if len(decision.Routes) == 0 {
		decision.TotalConfidence = 0
		return
	}

	var total float64
	for _, route := range decision.Routes {
		total += route.Confidence
	}
	decision.TotalConfidence = total / float64(len(decision.Routes))
}

func (r *CrossDomainRouter) GetAgents(domainCtx *domain.DomainContext) []concurrency.AgentType {
	decision := r.Route(domainCtx)
	agents := make([]concurrency.AgentType, len(decision.Routes))
	for i, route := range decision.Routes {
		agents[i] = route.Agent
	}
	return agents
}

func (r *CrossDomainRouter) ShouldRouteParallel(domainCtx *domain.DomainContext) bool {
	if domainCtx == nil || !domainCtx.IsCrossDomain {
		return false
	}

	if r.routingStrategy == StrategyParallel {
		return true
	}

	if r.routingStrategy == StrategyPrimaryOnly {
		return false
	}

	primaryConf := domainCtx.GetConfidence(domainCtx.PrimaryDomain)
	return primaryConf < r.parallelThreshold
}

type CrossDomainExecutor struct {
	router *CrossDomainRouter
}

func NewCrossDomainExecutor(router *CrossDomainRouter) *CrossDomainExecutor {
	return &CrossDomainExecutor{router: router}
}

type AgentHandler func(ctx context.Context, agent concurrency.AgentType) error

func (e *CrossDomainExecutor) ExecuteRoutes(
	ctx context.Context,
	decision *CrossDomainDecision,
	handler AgentHandler,
) []error {
	if decision.RoutingMethod == "parallel" {
		return e.executeParallel(ctx, decision, handler)
	}
	return e.executeSequential(ctx, decision, handler)
}

func (e *CrossDomainExecutor) executeParallel(
	ctx context.Context,
	decision *CrossDomainDecision,
	handler AgentHandler,
) []error {
	var wg sync.WaitGroup
	errors := make([]error, len(decision.Routes))

	for i, route := range decision.Routes {
		wg.Add(1)
		go func(idx int, r CrossDomainRoute) {
			defer wg.Done()
			errors[idx] = handler(ctx, r.Agent)
		}(i, route)
	}

	wg.Wait()
	return filterNilErrors(errors)
}

func (e *CrossDomainExecutor) executeSequential(
	ctx context.Context,
	decision *CrossDomainDecision,
	handler AgentHandler,
) []error {
	var errors []error

	for _, route := range decision.Routes {
		if err := ctx.Err(); err != nil {
			break
		}
		if err := handler(ctx, route.Agent); err != nil {
			errors = append(errors, err)
		}
	}

	return errors
}

func filterNilErrors(errors []error) []error {
	result := make([]error, 0)
	for _, err := range errors {
		if err != nil {
			result = append(result, err)
		}
	}
	return result
}

func (r *CrossDomainRouter) MaxRoutes() int {
	return r.maxRoutes
}

func (r *CrossDomainRouter) MinConfidence() float64 {
	return r.minConfidence
}

func (r *CrossDomainRouter) ParallelThreshold() float64 {
	return r.parallelThreshold
}

func (r *CrossDomainRouter) Strategy() RoutingStrategy {
	return r.routingStrategy
}
