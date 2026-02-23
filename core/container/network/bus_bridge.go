package network

import (
	"context"
	"encoding/json"
	"fmt"
)

// PublishFunc delivers a message envelope to the bus layer. The bridge
// calls this function for every envelope that passes the security pipeline.
// Callers provide a closure that converts the envelope fields into the
// bus-native message type (e.g. guide.Message) and publishes it.
type PublishFunc func(topic string, sourceAgent, targetAgent string, payload []byte) error

// BusBridge implements MessageSink. It translates NetworkNamespace
// delivery into EventBus publishes, completing the network pipeline.
type BusBridge struct {
	publish PublishFunc
}

// NewBusBridge creates a bridge that delivers network messages to the bus.
func NewBusBridge(publish PublishFunc) *BusBridge {
	return &BusBridge{publish: publish}
}

// Deliver extracts routing fields from the envelope and delegates to
// the publish function, which converts them to bus-native messages.
func (b *BusBridge) Deliver(_ context.Context, env *MessageEnvelope) error {
	return b.publish(env.Topic, env.SourceAgentType, env.TargetAgentID, env.Payload)
}

// SecuredPublisher wraps an EventBus publish path to route messages
// through the NetworkNamespace security pipeline before delivery.
// Agents use SecuredPublisher instead of raw bus.Publish to enforce
// rate limiting, policy evaluation, circuit breaking, and HMAC signing.
type SecuredPublisher struct {
	namespace   *NetworkNamespace
	containerID string
	agentType   string
	agentRole   string
	labels      map[string]string
}

// NewSecuredPublisher creates a publisher that enforces network policy.
func NewSecuredPublisher(
	namespace *NetworkNamespace,
	containerID string,
	agentType string,
	agentRole string,
	labels map[string]string,
) *SecuredPublisher {
	return &SecuredPublisher{
		namespace:   namespace,
		containerID: containerID,
		agentType:   agentType,
		agentRole:   agentRole,
		labels:      labels,
	}
}

// Publish constructs a MessageEnvelope and sends it through the
// NetworkNamespace security pipeline. The namespace applies rate
// limiting, policy evaluation, circuit breaking, and HMAC signing
// before delivering to the BusBridge sink.
func (sp *SecuredPublisher) Publish(ctx context.Context, topic string, payload any, targetAgentType string) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	env := &MessageEnvelope{
		SourceContainerID: sp.containerID,
		SourceAgentType:   sp.agentType,
		SourceAgentRole:   sp.agentRole,
		SourceLabels:      sp.labels,
		TargetAgentType:   targetAgentType,
		Topic:             topic,
		Payload:           data,
	}

	return sp.namespace.Send(ctx, env)
}
