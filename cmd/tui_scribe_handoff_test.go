package cmd

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/scribe"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
)

type testScribeProvider struct{}

func (p *testScribeProvider) Complete(_ context.Context, _ *providers.Request) (*providers.Response, error) {
	return &providers.Response{}, nil
}

type testScribeBus struct{}

func (b *testScribeBus) Publish(string, *guide.Message) error { return nil }

func (b *testScribeBus) Subscribe(topic string, _ guide.MessageHandler) (guide.Subscription, error) {
	return &testScribeSub{topic: topic}, nil
}

func (b *testScribeBus) SubscribeAsync(topic string, handler guide.MessageHandler) (guide.Subscription, error) {
	return b.Subscribe(topic, handler)
}

func (b *testScribeBus) Close() error                         { return nil }
func (b *testScribeBus) CloseWithContext(_ context.Context) error { return nil }

type testScribeSub struct {
	topic string
}

func (s *testScribeSub) Topic() string      { return s.topic }
func (s *testScribeSub) Unsubscribe() error { return nil }
func (s *testScribeSub) IsActive() bool     { return true }

func TestHandoffManagedScribe_CreatesReplacementFromSourceAgentID(t *testing.T) {
	supervisor := handoff.NewHandoffSupervisor(nil)
	if err := supervisor.Start(); err != nil {
		t.Fatalf("Start supervisor: %v", err)
	}
	t.Cleanup(func() { _ = supervisor.Stop() })
	var supervisorRef atomic.Pointer[handoff.HandoffSupervisor]
	supervisorRef.Store(supervisor)

	registry := newHandoffManagedScribeRegistry()
	var newManaged func(parentAgentType string, logger *slog.Logger, autoRegister bool) (*handoffManagedScribe, error)
	newManaged = func(parentAgentType string, logger *slog.Logger, autoRegister bool) (*handoffManagedScribe, error) {
		managed := &handoffManagedScribe{
			inner: scribe.New(scribe.Config{
				ParentAgentType: parentAgentType,
				Provider:        &testScribeProvider{},
				Model:           "test-model",
				Bus:             &testScribeBus{},
				Logger:          logger,
			}),
			supervisorRef:   &supervisorRef,
			logger:          logger,
			parentAgentType: parentAgentType,
			registry:        registry,
			autoRegister:    autoRegister,
		}
		managed.newManaged = newManaged
		return managed, nil
	}

	managed, err := newManaged("engineer", slog.Default(), true)
	if err != nil {
		t.Fatalf("newManaged: %v", err)
	}
	pod := agentShared.NewAgentPod(agentShared.AgentPodConfig{PodID: "pod-1"})
	managed.SetAgentPod(pod)
	if err := managed.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = managed.Stop() })

	if !supervisor.Factory().HasCreator(managed.AgentType()) {
		t.Fatalf("expected handoff creator for %q", managed.AgentType())
	}
	if got := supervisor.BridgeCount(); got != 1 {
		t.Fatalf("bridge count after initial registration = %d, want 1", got)
	}

	replacementAgent, err := supervisor.Factory().CreateAgent(
		handoff.WithFactoryCreationMetadata(context.Background(), handoff.FactoryCreationMetadata{
			AgentType: managed.AgentType(),
			AgentID:   managed.AgentID(),
		}),
		managed.AgentType(),
	)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	t.Cleanup(func() { _ = replacementAgent.Terminate(context.Background()) })

	replacement, ok := replacementAgent.(*handoffManagedScribe)
	if !ok {
		t.Fatalf("replacement type = %T, want *handoffManagedScribe", replacementAgent)
	}
	if replacement.ParentAgentType() != "engineer" {
		t.Fatalf("replacement parent agent type = %q, want engineer", replacement.ParentAgentType())
	}
	if replacement.AgentPod() != pod {
		t.Fatalf("replacement pod = %#v, want %#v", replacement.AgentPod(), pod)
	}
	if got := supervisor.BridgeCount(); got != 1 {
		t.Fatalf("bridge count after replacement creation = %d, want 1", got)
	}
}
