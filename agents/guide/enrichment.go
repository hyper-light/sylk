package guide

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corecontext "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/deadlinelease"
	"github.com/google/uuid"
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

			// Here we would perform a direct RPC or await a correlation ID on the bus.
			// This is a stub for the actual synchronous request implementation.
			respData, err := e.requestEnrichmentSync(groupCtx, dynamicTimeout, providerID, req)
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

func (e *EnrichmentService) requestEnrichmentSync(
	ctx context.Context,
	attemptTimeout time.Duration,
	providerID string,
	req *RouteRequest,
) (*corecontext.PrefetchSharePayload, error) {
	if e == nil || e.bus == nil || req == nil {
		return nil, fmt.Errorf("enrichment service unavailable")
	}
	correlationID := "enrich_" + uuid.NewString()
	waitCh, sub, err := e.subscribeForEnrichmentResponse(correlationID)
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()
	if err := e.publishEnrichmentAction(providerID, req, correlationID); err != nil {
		return nil, err
	}
	return e.awaitEnrichmentResponse(ctx, attemptTimeout, waitCh)
}

func (e *EnrichmentService) subscribeForEnrichmentResponse(correlationID string) (<-chan *Message, Subscription, error) {
	waitCh := make(chan *Message, 1)
	sub, err := e.bus.SubscribeAsync(TopicResponses("guide", "guide"), func(msg *Message) error {
		if msg == nil || msg.CorrelationID != correlationID {
			return nil
		}
		select {
		case waitCh <- msg:
		default:
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return waitCh, sub, nil
}

func (e *EnrichmentService) publishEnrichmentAction(providerID string, req *RouteRequest, correlationID string) error {
	msg := NewActionMessage(generateMessageID(), &ActionRequest{
		CorrelationID: correlationID,
		SourceAgentID: "guide",
		TargetAgentID: providerID,
		Action:        "enrich_context",
		Data: map[string]any{
			"input":      req.Input,
			"session_id": req.SessionID,
			"source":     req.SourceAgentID,
		},
		Timestamp: time.Now(),
	})
	return e.bus.Publish(TopicRequests(providerID, providerID), msg)
}

func (e *EnrichmentService) awaitEnrichmentResponse(
	ctx context.Context,
	attemptTimeout time.Duration,
	waitCh <-chan *Message,
) (*corecontext.PrefetchSharePayload, error) {
	var msg *Message
	err := deadlinelease.Run(ctx, deadlinelease.Config{
		AttemptTimeout: attemptTimeout,
		MaxRefreshes:   1,
	}, func(waitCtx context.Context) error {
		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case msg = <-waitCh:
			return nil
		}
	})
	if err != nil {
		return nil, deadlinelease.WrapTimeoutError("enrichment request", attemptTimeout, err)
	}
	return parseEnrichmentPayload(msg)
}

func parseEnrichmentPayload(msg *Message) (*corecontext.PrefetchSharePayload, error) {
	if msg == nil {
		return nil, fmt.Errorf("empty enrichment response")
	}
	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		return nil, fmt.Errorf("enrichment response missing route payload")
	}
	if !resp.Success {
		return nil, fmt.Errorf("enrichment provider error: %s", resp.Error)
	}
	if resp.Data == nil {
		return nil, nil
	}
	return decodeEnrichmentPayload(resp.Data)
}

func decodeEnrichmentPayload(data any) (*corecontext.PrefetchSharePayload, error) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var payload corecontext.PrefetchSharePayload
	if err := json.Unmarshal(bytes, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}
