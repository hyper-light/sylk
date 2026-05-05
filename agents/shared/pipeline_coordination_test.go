package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/pipeline/coordination"
)

type coordinationTestBus struct {
	mu      sync.Mutex
	pending map[string]chan *guide.Message
	handle  func(*guide.ActionRequest) (map[string]any, error)
}

func (b *coordinationTestBus) Publish(topic string, msg *guide.Message) error {
	if topic != guide.TopicGuideRequests {
		return fmt.Errorf("unexpected topic %q", topic)
	}
	req, ok := msg.GetActionRequest()
	if !ok || req == nil {
		return fmt.Errorf("expected action request message")
	}
	payload := map[string]any{"ok": true}
	if b.handle != nil {
		data, err := b.handle(req)
		if err != nil {
			resp := guide.NewResponseMessage("", &guide.RouteResponse{
				CorrelationID: req.CorrelationID,
				Success:       false,
				Error:         err.Error(),
			})
			ch := b.pending[req.CorrelationID]
			if ch != nil {
				ch <- resp
				return nil
			}
			return err
		}
		if data != nil {
			payload = data
		}
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
		Data:          payload,
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

func (b *coordinationTestBus) Close() error                            { return nil }
func (b *coordinationTestBus) CloseWithContext(_ context.Context) error { return nil }

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

func TestCoordinationClient_ResolveArtifactAutoBindsUniquePendingReview(t *testing.T) {
	bus := &coordinationTestBus{
		pending: make(map[string]chan *guide.Message),
		handle: func(req *guide.ActionRequest) (map[string]any, error) {
			switch req.Action {
			case coordination.ActionQueryView:
				return map[string]any{
					"view": map[string]any{
						"task_id": "task-1",
						"reviews": []map[string]any{
							{
								"id":          "rev-1",
								"artifact_id": "artifact-1",
								"status":      string(coordination.ReviewStatusPending),
							},
						},
					},
				}, nil
			case coordination.ActionResolveArtifact:
				raw, err := json.Marshal(req.Data)
				if err != nil {
					return nil, err
				}
				var input coordination.ResolveArtifactInput
				if err := json.Unmarshal(raw, &input); err != nil {
					return nil, err
				}
				if input.ReviewID != "rev-1" {
					t.Fatalf("ResolveArtifactInput.ReviewID = %q, want rev-1", input.ReviewID)
				}
				return map[string]any{"review_id": input.ReviewID}, nil
			default:
				return nil, fmt.Errorf("unexpected action %q", req.Action)
			}
		},
	}

	client := CoordinationClient{
		BusProvider:     func() guide.EventBus { return bus },
		SourceAgentID:   func() string { return "worker-1234" },
		SourceAgentType: func() string { return "engineer" },
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

	result, err := client.ResolveArtifact(context.Background(), coordination.ResolveArtifactInput{
		TaskID:     "task-1",
		ArtifactID: "artifact-1",
	})
	if err != nil {
		t.Fatalf("ResolveArtifact() error = %v", err)
	}
	if got := result["review_id"]; got != "rev-1" {
		t.Fatalf("result review_id = %#v, want rev-1", got)
	}
}

func TestCoordinationClient_ResolveArtifactRejectsAmbiguousPendingReviews(t *testing.T) {
	bus := &coordinationTestBus{
		pending: make(map[string]chan *guide.Message),
		handle: func(req *guide.ActionRequest) (map[string]any, error) {
			switch req.Action {
			case coordination.ActionQueryView:
				return map[string]any{
					"view": map[string]any{
						"task_id": "task-1",
						"reviews": []map[string]any{
							{"id": "rev-1", "artifact_id": "artifact-1", "status": string(coordination.ReviewStatusPending)},
							{"id": "rev-2", "artifact_id": "artifact-1", "status": string(coordination.ReviewStatusPending)},
						},
					},
				}, nil
			default:
				return nil, fmt.Errorf("unexpected action %q", req.Action)
			}
		},
	}

	client := CoordinationClient{
		BusProvider:     func() guide.EventBus { return bus },
		SourceAgentID:   func() string { return "worker-1234" },
		SourceAgentType: func() string { return "engineer" },
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

	_, err := client.ResolveArtifact(context.Background(), coordination.ResolveArtifactInput{
		TaskID:     "task-1",
		ArtifactID: "artifact-1",
	})
	if err == nil {
		t.Fatal("expected ambiguous pending reviews to fail")
	}
	if got := err.Error(); got != `multiple pending reviews exist for artifact "artifact-1"; specify review_id explicitly` {
		t.Fatalf("ResolveArtifact() error = %q", got)
	}
}
