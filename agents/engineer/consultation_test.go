package engineer

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
)

type consultationTestBus struct {
	mu        sync.Mutex
	publishes int
	onPublish func(*guide.RouteRequest)
}

func (b *consultationTestBus) Publish(topic string, msg *guide.Message) error {
	if topic != guide.TopicGuideRequests {
		return fmt.Errorf("unexpected topic %q", topic)
	}
	req, ok := msg.GetRouteRequest()
	if !ok || req == nil {
		return fmt.Errorf("expected route request")
	}
	b.mu.Lock()
	b.publishes++
	onPublish := b.onPublish
	b.mu.Unlock()
	if onPublish != nil {
		onPublish(req)
	}
	return nil
}

func (b *consultationTestBus) Subscribe(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, nil
}

func (b *consultationTestBus) SubscribeAsync(string, guide.MessageHandler) (guide.Subscription, error) {
	return nil, nil
}

func (b *consultationTestBus) Close() error { return nil }

func (b *consultationTestBus) publishCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.publishes
}

func TestEngineerRequestConsultSync_UsesStreamActivityWithoutRepublish(t *testing.T) {
	prevTimeout := routeSyncTimeout
	routeSyncTimeout = 20 * time.Millisecond
	defer func() { routeSyncTimeout = prevTimeout }()

	e := &Engineer{
		id:              "eng-1234",
		running:         true,
		pendingConsults: make(map[string]*shared.PendingSyncWait),
	}

	bus := &consultationTestBus{}
	bus.onPublish = func(req *guide.RouteRequest) {
		go func(correlationID string) {
			time.Sleep(10 * time.Millisecond)
			e.deliverConsultResponse(&guide.Message{
				CorrelationID: correlationID,
				Type:          guide.MessageTypeStream,
				Payload: &guide.StreamResponse{
					CorrelationID: correlationID,
					Event:         &guide.StreamEvent{Type: guide.StreamEventProgress},
				},
			})
			time.Sleep(15 * time.Millisecond)
			e.deliverConsultResponse(guide.NewResponseMessage("", &guide.RouteResponse{
				CorrelationID: correlationID,
				Success:       true,
				Data:          map[string]any{"ok": true},
			}))
		}(req.CorrelationID)
	}
	e.bus = bus

	resp, err := e.requestConsultSync(context.Background(), &guide.RouteRequest{
		TargetAgentID: "librarian",
		Input:         "inspect the repository",
	})
	if err != nil {
		t.Fatalf("requestConsultSync() error = %v", err)
	}
	if resp == nil {
		t.Fatal("expected consultation response")
	}
	if got := bus.publishCount(); got != 1 {
		t.Fatalf("publish count = %d, want 1", got)
	}
}
