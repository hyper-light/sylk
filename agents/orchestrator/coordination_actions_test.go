package orchestrator

import (
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
)

type coordinationActionTestBus struct {
	topic string
	msg   *guide.Message
}

func (b *coordinationActionTestBus) Publish(topic string, msg *guide.Message) error {
	b.topic = topic
	b.msg = msg
	return nil
}

func (b *coordinationActionTestBus) Subscribe(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, nil
}

func (b *coordinationActionTestBus) SubscribeAsync(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, nil
}

func (b *coordinationActionTestBus) Close() error { return nil }

func TestPublishCoordinationSuccess_PublishesToSourceAgentResponseTopic(t *testing.T) {
	bus := &coordinationActionTestBus{}
	o := &Orchestrator{
		bus:         bus,
		knownAgents: map[string]*guide.AgentAnnouncement{},
		config:      Config{AgentID: "orchestrator"},
	}
	req := &guide.ActionRequest{
		CorrelationID:   "corr-1",
		SourceAgentID:   "worker-1234",
		SourceAgentName: "inspector-pipeline",
	}

	if err := o.publishCoordinationSuccess(req, map[string]any{"ok": true}); err != nil {
		t.Fatalf("publishCoordinationSuccess() error = %v", err)
	}

	wantTopic := guide.TopicResponses("inspector-pipeline", "worker-1234")
	if bus.topic != wantTopic {
		t.Fatalf("published topic = %q, want %q", bus.topic, wantTopic)
	}
	if bus.msg == nil {
		t.Fatal("expected response message to be published")
	}
}
