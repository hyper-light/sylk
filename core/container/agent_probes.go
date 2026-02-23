package container

import (
	"context"
	"time"
)

// AgentLivenessProbe checks that the agent can return its ID without panicking.
// A panic during AgentID() indicates the agent is in a corrupted state and
// should be restarted. Implements ProbeHandler.
type AgentLivenessProbe struct {
	agent ContainerAgent
}

// NewAgentLivenessProbe creates a liveness probe for the given agent.
func NewAgentLivenessProbe(agent ContainerAgent) *AgentLivenessProbe {
	return &AgentLivenessProbe{agent: agent}
}

// Execute calls agent.AgentID() with panic recovery. A successful call
// indicates the agent's core data structures are not corrupted.
func (p *AgentLivenessProbe) Execute(ctx context.Context) (result ProbeResult, err error) {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			result = ProbeResult{
				Success:   false,
				Message:   "agent panicked during liveness check",
				Latency:   time.Since(start),
				Timestamp: start,
			}
			err = nil // panic is a probe failure, not an execution error
		}
	}()

	_ = p.agent.AgentID()

	return ProbeResult{
		Success:   true,
		Message:   "agent alive",
		Latency:   time.Since(start),
		Timestamp: start,
	}, nil
}

// BusReadinessProbe checks if the Guide reports the agent as ready.
// Uses a callback to avoid circular dependency on the Guide package.
// Implements ProbeHandler.
type BusReadinessProbe struct {
	agentID string
	isReady func(string) bool
}

// NewBusReadinessProbe creates a readiness probe that delegates to the
// provided callback. The callback is typically bound to Guide.IsAgentReady.
func NewBusReadinessProbe(agentID string, isReady func(string) bool) *BusReadinessProbe {
	return &BusReadinessProbe{
		agentID: agentID,
		isReady: isReady,
	}
}

// Execute checks the agent's readiness via the callback.
func (p *BusReadinessProbe) Execute(_ context.Context) (ProbeResult, error) {
	start := time.Now()
	ready := p.isReady(p.agentID)

	msg := "agent not ready"
	if ready {
		msg = "agent ready"
	}

	return ProbeResult{
		Success:   ready,
		Message:   msg,
		Latency:   time.Since(start),
		Timestamp: start,
	}, nil
}

// livenessProbeSpec builds a ProbeSpec for liveness checking with timing
// derived from the container's graceful stop duration. Probe period is
// GracefulStop/6 so at least two probes fire within the grace window.
// Initial delay is GracefulStop/3 to allow startup time.
func livenessProbeSpec(agent ContainerAgent, gracefulStop time.Duration) ProbeSpec {
	// Denominator 6: ensures ≥3 probe opportunities within grace period
	// before the container would be force-killed.
	period := gracefulStop / 6
	// Denominator 3: startup allowance is 2× the probe period.
	initialDelay := gracefulStop / 3

	return ProbeSpec{
		Type:             ProbeLiveness,
		Handler:          NewAgentLivenessProbe(agent),
		InitialDelay:     initialDelay,
		Period:           period,
		Timeout:          period,
		SuccessThreshold: 1,
		FailureThreshold: 3,
	}
}

// busCloseTimeout is the ChannelBus close timeout used as the readiness
// probe period baseline. Matches guide.DefaultChannelBusConfig().CloseTimeout.
const busCloseTimeout = 5 * time.Second

// readinessProbeSpec builds a ProbeSpec for bus readiness checking.
// Period matches the bus close timeout (5s) since that is the maximum
// time the bus waits for handlers to drain on shutdown.
func readinessProbeSpec(agentID string, isReady func(string) bool) ProbeSpec {
	return ProbeSpec{
		Type:             ProbeReadiness,
		Handler:          NewBusReadinessProbe(agentID, isReady),
		InitialDelay:     2 * time.Second,
		Period:           busCloseTimeout,
		Timeout:          busCloseTimeout,
		SuccessThreshold: 1,
		FailureThreshold: 3,
	}
}
