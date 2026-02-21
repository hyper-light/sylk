package guide

import (
	"context"
	"time"

	corecontext "github.com/adalundhe/sylk/core/context"
	"golang.org/x/sync/errgroup"
)

type EnrichmentService struct {
	bus        EventBus
	registry   AgentRegistry
	health     *HealthMonitor
	maxUXDelay time.Duration
}

func NewEnrichmentService(bus EventBus, reg AgentRegistry, health *HealthMonitor) *EnrichmentService {
	return &EnrichmentService{
		bus:        bus,
		registry:   reg,
		health:     health,
		maxUXDelay: 2 * time.Second,
	}
}

func (e *EnrichmentService) Enrich(ctx context.Context, req *RouteRequest, result *RouteResult) []corecontext.PrefetchSharePayload {
	var providers []*AgentRegistration
	for _, agent := range e.registry.GetAll() {
		if agent.Capabilities.ProvidesEnrichment && agent.Capabilities.SupportsDomain(result.Domain) {
			if e.health == nil || e.health.IsHealthy(agent.ID) {
				providers = append(providers, agent)
			}
		}
	}

	if len(providers) == 0 {
		return nil
	}

	g, groupCtx := errgroup.WithContext(ctx)
	payloads := make(chan corecontext.PrefetchSharePayload, len(providers))

	for _, provider := range providers {
		providerID := provider.ID

		g.Go(func() error {
			dynamicTimeout := 500 * time.Millisecond // Default baseline

			if e.health != nil {
				if stats, ok := e.health.GetInfo(providerID); ok {
					if stats.AvgResponseTimeMs > 0 {
						dynamicTimeout = time.Duration(stats.AvgResponseTimeMs*1.5) * time.Millisecond
					}
				}
			}

			if dynamicTimeout < 50*time.Millisecond {
				dynamicTimeout = 50 * time.Millisecond
			}
			if dynamicTimeout > e.maxUXDelay {
				dynamicTimeout = e.maxUXDelay
			}

			enrichCtx, cancel := context.WithTimeout(groupCtx, dynamicTimeout)
			defer cancel()

			// Here we would perform a direct RPC or await a correlation ID on the bus.
			// This is a stub for the actual synchronous request implementation.
			respData, err := e.requestEnrichmentSync(enrichCtx, providerID, req)
			if err == nil && respData != nil {
				payloads <- *respData
			}
			return nil
		})
	}

	_ = g.Wait()
	close(payloads)

	var valid []corecontext.PrefetchSharePayload
	for p := range payloads {
		valid = append(valid, p)
	}
	return valid
}

func (e *EnrichmentService) requestEnrichmentSync(ctx context.Context, providerID string, req *RouteRequest) (*corecontext.PrefetchSharePayload, error) {
	// Stub for sending a direct message via EventBus and waiting for a response
	// using the provided context timeout.
	return nil, nil
}
