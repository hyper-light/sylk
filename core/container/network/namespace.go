package network

import (
	"context"
	"errors"
	"sync"

	"github.com/adalundhe/sylk/core/container/security"
)

var (
	ErrNamespaceClosed = errors.New("network namespace closed")
	ErrPolicyDenied    = errors.New("network policy denied message")
	ErrRateLimited     = errors.New("message rate limited")
	ErrCircuitOpen     = errors.New("circuit breaker open for target agent")
	ErrAuthFailed      = errors.New("message authentication failed")
)

// MessageEnvelope wraps a payload with routing metadata and authentication.
type MessageEnvelope struct {
	SourceContainerID string
	SourceAgentType   string
	SourceAgentRole   string
	SourceLabels      map[string]string
	TargetAgentID     string
	TargetAgentType   string
	TargetLabels      map[string]string
	Topic             string
	Payload           []byte
	Signature         string
}

// MessageSink receives messages from the network namespace.
type MessageSink interface {
	Deliver(ctx context.Context, env *MessageEnvelope) error
}

// NetworkNamespace provides the shared communication space within a pod.
// It enforces policy evaluation, rate limiting, circuit breaking, and
// message authentication on all traffic.
type NetworkNamespace struct {
	mu              sync.RWMutex
	podID           string
	evaluator       *PolicyEvaluator
	rateLimiters    map[string]*RateLimiter      // containerID → rate limiter
	circuitBreakers map[string]*CircuitBreaker    // agentID → circuit breaker
	auth            *MessageAuthenticator
	sink            MessageSink
	closed          bool
}

// NetworkNamespaceConfig provides construction parameters.
type NetworkNamespaceConfig struct {
	PodID           string
	Policies        []*NetworkPolicy
	Auth            *MessageAuthenticator
	Sink            MessageSink
	RateLimiterCfg  RateLimiterConfig
}

// NewNetworkNamespace creates a network namespace for a pod.
func NewNetworkNamespace(cfg NetworkNamespaceConfig) *NetworkNamespace {
	evaluator := NewPolicyEvaluator(cfg.Policies)
	auth := cfg.Auth
	if auth == nil {
		auth = NewMessageAuthenticator()
	}
	return &NetworkNamespace{
		podID:           cfg.PodID,
		evaluator:       evaluator,
		rateLimiters:    make(map[string]*RateLimiter),
		circuitBreakers: make(map[string]*CircuitBreaker),
		auth:            auth,
		sink:            cfg.Sink,
	}
}

// RegisterContainer adds rate limiter and circuit breaker for a container.
func (ns *NetworkNamespace) RegisterContainer(containerID, agentID string, rlCfg RateLimiterConfig, cbCfg CircuitBreakerConfig) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.rateLimiters[containerID] = NewRateLimiter(rlCfg)
	ns.circuitBreakers[agentID] = NewCircuitBreaker(agentID, cbCfg)
}

// UnregisterContainer removes the rate limiter and circuit breaker for a container.
func (ns *NetworkNamespace) UnregisterContainer(containerID, agentID string) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	delete(ns.rateLimiters, containerID)
	delete(ns.circuitBreakers, agentID)
}

// Send processes an outbound message through the full security pipeline:
// 1. Check closed
// 2. Rate limit source container
// 3. Evaluate network policy
// 4. Check circuit breaker for target
// 5. Sign message
// 6. Deliver to sink
func (ns *NetworkNamespace) Send(ctx context.Context, env *MessageEnvelope) error {
	ns.mu.RLock()
	if ns.closed {
		ns.mu.RUnlock()
		return ErrNamespaceClosed
	}
	rl := ns.rateLimiters[env.SourceContainerID]
	cb := ns.circuitBreakers[env.TargetAgentID]
	ns.mu.RUnlock()

	if err := ns.checkRateLimit(rl); err != nil {
		return err
	}
	if err := ns.checkPolicy(env); err != nil {
		return err
	}
	if err := ns.checkCircuit(cb); err != nil {
		return err
	}
	if err := ns.signMessage(env); err != nil {
		return err
	}
	return ns.deliver(ctx, env, cb)
}

func (ns *NetworkNamespace) checkRateLimit(rl *RateLimiter) error {
	if rl != nil && !rl.Allow() {
		return ErrRateLimited
	}
	return nil
}

func (ns *NetworkNamespace) checkPolicy(env *MessageEnvelope) error {
	req := EvaluationRequest{
		SourceAgentType: env.SourceAgentType,
		SourceAgentRole: env.SourceAgentRole,
		SourceLabels:    env.SourceLabels,
		TargetAgentType: env.TargetAgentType,
		TargetLabels:    env.TargetLabels,
		Topic:           env.Topic,
		Direction:       DirectionEgress,
	}
	if ns.evaluator.Evaluate(req) == PolicyDeny {
		return ErrPolicyDenied
	}
	return nil
}

func (ns *NetworkNamespace) checkCircuit(cb *CircuitBreaker) error {
	if cb != nil && !cb.Allow() {
		return ErrCircuitOpen
	}
	return nil
}

func (ns *NetworkNamespace) signMessage(env *MessageEnvelope) error {
	sig, err := ns.auth.Sign(env.SourceContainerID, env.Payload)
	if err != nil {
		return err
	}
	env.Signature = sig
	return nil
}

func (ns *NetworkNamespace) deliver(ctx context.Context, env *MessageEnvelope, cb *CircuitBreaker) error {
	if ns.sink == nil {
		return nil
	}
	err := ns.sink.Deliver(ctx, env)
	ns.recordCircuitResult(cb, err)
	return err
}

func (ns *NetworkNamespace) recordCircuitResult(cb *CircuitBreaker, err error) {
	if cb == nil {
		return
	}
	if err != nil {
		cb.RecordFailure()
	} else {
		cb.RecordSuccess()
	}
}

// VerifyMessage checks the HMAC signature of an inbound message.
func (ns *NetworkNamespace) VerifyMessage(env *MessageEnvelope) error {
	return ns.auth.Verify(env.SourceContainerID, env.Payload, env.Signature)
}

// Close shuts down the namespace. No further messages can be sent.
func (ns *NetworkNamespace) Close() {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.closed = true
}

// IsClosed reports whether the namespace has been closed.
func (ns *NetworkNamespace) IsClosed() bool {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.closed
}

// PodID returns the owning pod's ID.
func (ns *NetworkNamespace) PodID() string {
	return ns.podID
}

// Evaluator returns the policy evaluator for direct query.
func (ns *NetworkNamespace) Evaluator() *PolicyEvaluator {
	return ns.evaluator
}

// CircuitBreaker returns the circuit breaker for a given agent.
func (ns *NetworkNamespace) CircuitBreaker(agentID string) *CircuitBreaker {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.circuitBreakers[agentID]
}

// GetCapabilities returns the security-related capability information.
// This is a helper for interacting with the security subsystem.
func GetSecurityCapabilities(caps *security.CapabilitySet) *security.CapabilitySet {
	return caps
}
