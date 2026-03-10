package shared

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
)

type coordinationTestBus struct {
	mu      sync.Mutex
	pending map[string]chan *guide.Message
}

func (b *coordinationTestBus) Publish(topic string, msg *guide.Message) error {
	if topic != guide.TopicGuideRequests {
		return fmt.Errorf("unexpected topic %q", topic)
	}
	req, ok := msg.GetActionRequest()
	if !ok || req == nil {
		return fmt.Errorf("expected action request message")
	}
	b.mu.Lock()
	ch := b.pending[req.CorrelationID]
	b.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("no pending waiter for correlation %q", req.CorrelationID)
	}
	resp := guide.NewResponseMessage("", &guide.RouteResponse{
		CorrelationID: req.CorrelationID,
		Success:       true,
		Data: map[string]any{
			"ok": true,
		},
	})
	ch <- resp
	return nil
}

func (b *coordinationTestBus) Subscribe(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, fmt.Errorf("not implemented")
}

func (b *coordinationTestBus) SubscribeAsync(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, fmt.Errorf("not implemented")
}

func (b *coordinationTestBus) Close() error { return nil }

func TestCoordinationClient_UsesBusProviderAtRequestTime(t *testing.T) {
	bus := &coordinationTestBus{pending: make(map[string]chan *guide.Message)}

	client := CoordinationClient{
		BusProvider:     func() guide.EventBus { return bus },
		SourceAgentID:   func() string { return "worker-1234" },
		SourceAgentType: func() string { return "inspector-pipeline" },
		SessionID:       func() string { return "session-1" },
		RegisterPending: func(correlationID string) <-chan *guide.Message {
			ch := make(chan *guide.Message, 1)
			bus.mu.Lock()
			bus.pending[correlationID] = ch
			bus.mu.Unlock()
			return ch
		},
		ClearPending: func(correlationID string) {
			bus.mu.Lock()
			delete(bus.pending, correlationID)
			bus.mu.Unlock()
		},
		Timeout: time.Second,
	}

	var out map[string]any
	if err := client.requestWithTimeout(context.Background(), "coord_test", map[string]any{"task_id": "task-1"}, &out, 0); err != nil {
		t.Fatalf("requestWithTimeout() error = %v", err)
	}
	if got, ok := out["ok"].(bool); !ok || !got {
		t.Fatalf("expected decoded response payload, got %#v", out)
	}
}
