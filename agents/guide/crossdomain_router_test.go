package guide

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
)

func TestCrossDomainRouter_Route_NilContext(t *testing.T) {
	router := NewCrossDomainRouter(nil)

	decision := router.Route(nil)

	if decision.IsCrossDomain {
		t.Error("Should not be cross-domain for nil context")
	}
	if decision.RoutingMethod != "default" {
		t.Errorf("Expected 'default' method, got %s", decision.RoutingMethod)
	}
	if len(decision.Routes) != 0 {
		t.Errorf("Expected 0 routes, got %d", len(decision.Routes))
	}
}

func TestCrossDomainRouter_Route_EmptyContext(t *testing.T) {
	router := NewCrossDomainRouter(nil)
	ctx := domain.NewDomainContext("test")

	decision := router.Route(ctx)

	if decision.RoutingMethod != "default" {
		t.Errorf("Expected 'default' method, got %s", decision.RoutingMethod)
	}
}

func TestCrossDomainRouter_Route_SingleDomain(t *testing.T) {
	router := NewCrossDomainRouter(nil)

	ctx := domain.NewDomainContext("find the function")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)

	decision := router.Route(ctx)

	if decision.IsCrossDomain {
		t.Error("Should not be cross-domain for single domain")
	}
	if len(decision.Routes) != 1 {
		t.Errorf("Expected 1 route, got %d", len(decision.Routes))
	}
	if decision.PrimaryRoute.Agent != concurrency.AgentLibrarian {
		t.Errorf("Expected librarian, got %s", decision.PrimaryRoute.Agent)
	}
	if decision.PrimaryRoute.Confidence != 0.9 {
		t.Errorf("Expected confidence 0.9, got %f", decision.PrimaryRoute.Confidence)
	}
}

func TestCrossDomainRouter_Route_CrossDomain(t *testing.T) {
	router := NewCrossDomainRouter(nil)

	ctx := domain.NewDomainContext("research and find code")
	ctx.AddDomain(domain.DomainLibrarian, 0.8, nil)
	ctx.AddDomain(domain.DomainAcademic, 0.7, nil)
	ctx.SetCrossDomain(true)

	decision := router.Route(ctx)

	if !decision.IsCrossDomain {
		t.Error("Should be cross-domain")
	}
	if len(decision.Routes) != 2 {
		t.Errorf("Expected 2 routes, got %d", len(decision.Routes))
	}
	if len(decision.SecondaryRoutes) != 1 {
		t.Errorf("Expected 1 secondary route, got %d", len(decision.SecondaryRoutes))
	}
}

func TestCrossDomainRouter_Route_MaxRoutes(t *testing.T) {
	router := NewCrossDomainRouter(&CrossDomainRouterConfig{MaxRoutes: 2})

	ctx := domain.NewDomainContext("complex query")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	ctx.AddDomain(domain.DomainAcademic, 0.8, nil)
	ctx.AddDomain(domain.DomainArchitect, 0.7, nil)
	ctx.SetCrossDomain(true)

	decision := router.Route(ctx)

	if len(decision.Routes) > 2 {
		t.Errorf("Should respect max routes (2), got %d", len(decision.Routes))
	}
}

func TestCrossDomainRouter_Route_MinConfidence(t *testing.T) {
	router := NewCrossDomainRouter(&CrossDomainRouterConfig{MinConfidence: 0.75})

	ctx := domain.NewDomainContext("query")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	ctx.AddDomain(domain.DomainAcademic, 0.5, nil)
	ctx.SetCrossDomain(true)

	decision := router.Route(ctx)

	if len(decision.Routes) != 1 {
		t.Errorf("Should filter low confidence routes, got %d routes", len(decision.Routes))
	}
}

func TestCrossDomainRouter_Route_StrategyPrimaryOnly(t *testing.T) {
	router := NewCrossDomainRouter(&CrossDomainRouterConfig{
		Strategy: StrategyPrimaryOnly,
	})

	ctx := domain.NewDomainContext("query")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	ctx.SetCrossDomain(true)

	decision := router.Route(ctx)

	if decision.RoutingMethod != "primary_only" {
		t.Errorf("Expected 'primary_only', got %s", decision.RoutingMethod)
	}
}

func TestCrossDomainRouter_Route_StrategyParallel(t *testing.T) {
	router := NewCrossDomainRouter(&CrossDomainRouterConfig{
		Strategy: StrategyParallel,
	})

	ctx := domain.NewDomainContext("query")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	ctx.SetCrossDomain(true)

	decision := router.Route(ctx)

	if decision.RoutingMethod != "parallel" {
		t.Errorf("Expected 'parallel', got %s", decision.RoutingMethod)
	}
}

func TestCrossDomainRouter_Route_PrimaryFirst(t *testing.T) {
	router := NewCrossDomainRouter(&CrossDomainRouterConfig{
		Strategy:          StrategyPrimaryFirst,
		ParallelThreshold: 0.7,
	})

	ctx := domain.NewDomainContext("query")
	ctx.AddDomain(domain.DomainLibrarian, 0.8, nil)
	ctx.AddDomain(domain.DomainAcademic, 0.6, nil)
	ctx.SetCrossDomain(true)

	decision := router.Route(ctx)

	if decision.RoutingMethod != "primary_first" {
		t.Errorf("Expected 'primary_first', got %s", decision.RoutingMethod)
	}
}

func TestCrossDomainRouter_Route_ParallelWhenLowConfidence(t *testing.T) {
	router := NewCrossDomainRouter(&CrossDomainRouterConfig{
		Strategy:          StrategyPrimaryFirst,
		ParallelThreshold: 0.8,
	})

	ctx := domain.NewDomainContext("query")
	ctx.AddDomain(domain.DomainLibrarian, 0.6, nil)
	ctx.AddDomain(domain.DomainAcademic, 0.5, nil)
	ctx.SetCrossDomain(true)

	decision := router.Route(ctx)

	if decision.RoutingMethod != "parallel" {
		t.Errorf("Expected 'parallel' for low confidence, got %s", decision.RoutingMethod)
	}
}

func TestCrossDomainRouter_GetAgents(t *testing.T) {
	router := NewCrossDomainRouter(nil)

	ctx := domain.NewDomainContext("query")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	ctx.AddDomain(domain.DomainAcademic, 0.8, nil)
	ctx.SetCrossDomain(true)

	agents := router.GetAgents(ctx)

	if len(agents) != 2 {
		t.Errorf("Expected 2 agents, got %d", len(agents))
	}
	if agents[0] != concurrency.AgentLibrarian {
		t.Errorf("First agent should be librarian, got %s", agents[0])
	}
}

func TestCrossDomainRouter_ShouldRouteParallel_Nil(t *testing.T) {
	router := NewCrossDomainRouter(nil)

	if router.ShouldRouteParallel(nil) {
		t.Error("Should return false for nil context")
	}
}

func TestCrossDomainRouter_ShouldRouteParallel_NotCrossDomain(t *testing.T) {
	router := NewCrossDomainRouter(nil)

	ctx := domain.NewDomainContext("query")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)

	if router.ShouldRouteParallel(ctx) {
		t.Error("Should return false for non-cross-domain")
	}
}

func TestCrossDomainRouter_ShouldRouteParallel_StrategyParallel(t *testing.T) {
	router := NewCrossDomainRouter(&CrossDomainRouterConfig{
		Strategy: StrategyParallel,
	})

	ctx := domain.NewDomainContext("query")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	ctx.SetCrossDomain(true)

	if !router.ShouldRouteParallel(ctx) {
		t.Error("Should return true for parallel strategy")
	}
}

func TestCrossDomainRouter_ShouldRouteParallel_StrategyPrimaryOnly(t *testing.T) {
	router := NewCrossDomainRouter(&CrossDomainRouterConfig{
		Strategy: StrategyPrimaryOnly,
	})

	ctx := domain.NewDomainContext("query")
	ctx.AddDomain(domain.DomainLibrarian, 0.5, nil)
	ctx.SetCrossDomain(true)

	if router.ShouldRouteParallel(ctx) {
		t.Error("Should return false for primary_only strategy")
	}
}

func TestCrossDomainRouter_Defaults(t *testing.T) {
	router := NewCrossDomainRouter(nil)

	if router.MaxRoutes() != 3 {
		t.Errorf("Default max routes should be 3, got %d", router.MaxRoutes())
	}
	if router.MinConfidence() != 0.3 {
		t.Errorf("Default min confidence should be 0.3, got %f", router.MinConfidence())
	}
	if router.ParallelThreshold() != 0.7 {
		t.Errorf("Default parallel threshold should be 0.7, got %f", router.ParallelThreshold())
	}
	if router.Strategy() != StrategyPrimaryFirst {
		t.Errorf("Default strategy should be PrimaryFirst, got %d", router.Strategy())
	}
}

func TestCrossDomainExecutor_ExecuteRoutes_Sequential(t *testing.T) {
	router := NewCrossDomainRouter(&CrossDomainRouterConfig{
		Strategy: StrategyPrimaryFirst,
	})
	executor := NewCrossDomainExecutor(router)

	ctx := domain.NewDomainContext("query")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	ctx.AddDomain(domain.DomainAcademic, 0.8, nil)
	ctx.SetCrossDomain(true)

	decision := router.Route(ctx)

	var callCount int32
	handler := func(_ context.Context, _ concurrency.AgentType) error {
		atomic.AddInt32(&callCount, 1)
		return nil
	}

	errs := executor.ExecuteRoutes(context.Background(), decision, handler)

	if len(errs) != 0 {
		t.Errorf("Expected no errors, got %d", len(errs))
	}
	if atomic.LoadInt32(&callCount) != 2 {
		t.Errorf("Expected 2 calls, got %d", callCount)
	}
}

func TestCrossDomainExecutor_ExecuteRoutes_Parallel(t *testing.T) {
	router := NewCrossDomainRouter(&CrossDomainRouterConfig{
		Strategy: StrategyParallel,
	})
	executor := NewCrossDomainExecutor(router)

	ctx := domain.NewDomainContext("query")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	ctx.AddDomain(domain.DomainAcademic, 0.8, nil)
	ctx.SetCrossDomain(true)

	decision := router.Route(ctx)

	var callCount int32
	handler := func(_ context.Context, _ concurrency.AgentType) error {
		atomic.AddInt32(&callCount, 1)
		return nil
	}

	errs := executor.ExecuteRoutes(context.Background(), decision, handler)

	if len(errs) != 0 {
		t.Errorf("Expected no errors, got %d", len(errs))
	}
	if atomic.LoadInt32(&callCount) != 2 {
		t.Errorf("Expected 2 calls, got %d", callCount)
	}
}

func TestCrossDomainExecutor_ExecuteRoutes_WithErrors(t *testing.T) {
	router := NewCrossDomainRouter(&CrossDomainRouterConfig{
		Strategy: StrategyParallel,
	})
	executor := NewCrossDomainExecutor(router)

	ctx := domain.NewDomainContext("query")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	ctx.AddDomain(domain.DomainAcademic, 0.8, nil)
	ctx.SetCrossDomain(true)

	decision := router.Route(ctx)

	expectedErr := errors.New("test error")
	handler := func(_ context.Context, agent concurrency.AgentType) error {
		if agent == concurrency.AgentLibrarian {
			return expectedErr
		}
		return nil
	}

	errs := executor.ExecuteRoutes(context.Background(), decision, handler)

	if len(errs) != 1 {
		t.Errorf("Expected 1 error, got %d", len(errs))
	}
}

func TestCrossDomainExecutor_ExecuteRoutes_ContextCanceled(t *testing.T) {
	router := NewCrossDomainRouter(&CrossDomainRouterConfig{
		Strategy: StrategyPrimaryFirst,
	})
	executor := NewCrossDomainExecutor(router)

	domainCtx := domain.NewDomainContext("query")
	domainCtx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	domainCtx.AddDomain(domain.DomainAcademic, 0.8, nil)
	domainCtx.SetCrossDomain(true)

	decision := router.Route(domainCtx)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var callCount int32
	handler := func(_ context.Context, _ concurrency.AgentType) error {
		atomic.AddInt32(&callCount, 1)
		return nil
	}

	executor.ExecuteRoutes(ctx, decision, handler)

	if atomic.LoadInt32(&callCount) != 0 {
		t.Errorf("Expected 0 calls after cancel, got %d", callCount)
	}
}

func TestCrossDomainRoute_Fields(t *testing.T) {
	route := CrossDomainRoute{
		Agent:      concurrency.AgentLibrarian,
		Domain:     domain.DomainLibrarian,
		Priority:   0,
		Confidence: 0.9,
	}

	if route.Agent != concurrency.AgentLibrarian {
		t.Error("Agent mismatch")
	}
	if route.Domain != domain.DomainLibrarian {
		t.Error("Domain mismatch")
	}
	if route.Priority != 0 {
		t.Error("Priority mismatch")
	}
	if route.Confidence != 0.9 {
		t.Error("Confidence mismatch")
	}
}

func TestCrossDomainDecision_Fields(t *testing.T) {
	primary := CrossDomainRoute{
		Agent:      concurrency.AgentLibrarian,
		Domain:     domain.DomainLibrarian,
		Priority:   0,
		Confidence: 0.9,
	}

	decision := CrossDomainDecision{
		Routes:          []CrossDomainRoute{primary},
		IsCrossDomain:   true,
		PrimaryRoute:    primary,
		SecondaryRoutes: []CrossDomainRoute{},
		RoutingMethod:   "primary_first",
		TotalConfidence: 0.9,
	}

	if len(decision.Routes) != 1 {
		t.Error("Routes mismatch")
	}
	if !decision.IsCrossDomain {
		t.Error("IsCrossDomain mismatch")
	}
	if decision.RoutingMethod != "primary_first" {
		t.Error("RoutingMethod mismatch")
	}
	if decision.TotalConfidence != 0.9 {
		t.Error("TotalConfidence mismatch")
	}
}

func TestCrossDomainRouter_TotalConfidence(t *testing.T) {
	router := NewCrossDomainRouter(nil)

	ctx := domain.NewDomainContext("query")
	ctx.AddDomain(domain.DomainLibrarian, 0.8, nil)
	ctx.AddDomain(domain.DomainAcademic, 0.6, nil)
	ctx.SetCrossDomain(true)

	decision := router.Route(ctx)

	expectedAvg := (0.8 + 0.6) / 2.0
	if decision.TotalConfidence != expectedAvg {
		t.Errorf("Expected total confidence %f, got %f", expectedAvg, decision.TotalConfidence)
	}
}
