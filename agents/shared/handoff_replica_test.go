package shared

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
)

type testReplicaHandoffAgent struct {
	id   string
	kind string
	desc handoff.AgentDescriptor
}

func (a *testReplicaHandoffAgent) AgentID() string { return a.id }

func (a *testReplicaHandoffAgent) AgentType() string { return a.kind }

func (a *testReplicaHandoffAgent) Descriptor() handoff.AgentDescriptor { return a.desc }

func (a *testReplicaHandoffAgent) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   a.id,
		AgentType: a.kind,
		Timestamp: time.Now(),
		State: map[string]string{
			"source": "test",
		},
	}
}

func (a *testReplicaHandoffAgent) Terminate(context.Context) error { return nil }

func (a *testReplicaHandoffAgent) EvictEntries(candidates []handoff.EvictionCandidate) (int, error) {
	total := 0
	for _, candidate := range candidates {
		total += candidate.Entry.GetTokenCount()
	}
	return total, nil
}

func setupReplicaSupervisor(t *testing.T) (*handoff.HandoffSupervisor, *handoff.HandoffBridge, *testReplicaHandoffAgent) {
	t.Helper()

	desc := handoff.AgentDescriptor{
		AgentType:     "academic",
		ModelID:       "opus-4.5-200k",
		ContextWindow: 200_000,
		Category:      handoff.CategoryKnowledge,
	}
	agent := &testReplicaHandoffAgent{
		id:   "academic",
		kind: "academic",
		desc: desc,
	}

	supervisor := handoff.NewHandoffSupervisor(&handoff.SupervisorConfig{
		EnableWAL:      false,
		EnableLearning: true,
	})
	if err := supervisor.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		_ = supervisor.Stop()
	})

	bridge, err := supervisor.RegisterAgent(agent)
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	return supervisor, bridge, agent
}

func TestAttachReplicaHandoffBridge_RegistersDistinctReplica(t *testing.T) {
	supervisor, baseBridge, agent := setupReplicaSupervisor(t)

	ctx, cleanup, replicaBridge, err := AttachReplicaHandoffBridge(context.Background(), agent, baseBridge, KnowledgeReplicaHandoffOptions{
		SessionID:     "sess-1",
		CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("AttachReplicaHandoffBridge() error = %v", err)
	}
	defer cleanup()

	if got := HandoffBridgeFromContext(ctx); got != replicaBridge {
		t.Fatalf("HandoffBridgeFromContext() = %p, want %p", got, replicaBridge)
	}

	replicaID := ReplicaHandoffAgentID(agent.AgentID(), "corr-1")
	if replicaBridge == nil {
		t.Fatal("expected replica bridge")
	}
	if replicaBridge == baseBridge {
		t.Fatal("expected request-scoped replica bridge, got singleton bridge")
	}
	if replicaBridge.Agent().AgentID() != replicaID {
		t.Fatalf("replica bridge agent ID = %q, want %q", replicaBridge.Agent().AgentID(), replicaID)
	}
	if replicaBridge.Profile().InstanceID != replicaID {
		t.Fatalf("replica profile instance ID = %q, want %q", replicaBridge.Profile().InstanceID, replicaID)
	}
	if supervisor.GetBridge(replicaID) != replicaBridge {
		t.Fatal("supervisor did not register replica bridge")
	}

	cleanup()
	if supervisor.GetBridge(replicaID) != nil {
		t.Fatal("expected replica bridge to unregister on cleanup")
	}
}

func TestReplicaHandoffActivityPublisher_CanonicalizesVisibleAgentID(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := &replicaHandoffActivityPublisher{
		base:             collector,
		canonicalAgentID: "academic",
		runtimeAgentID:   "academic#replica-corr-1",
	}

	publisher.PublishActivity(&events.ActivityEvent{
		AgentID:       "academic#replica-corr-1",
		CorrelationID: "corr-1",
		Data: map[string]any{
			"agent_type": "academic",
		},
	})

	emitted := collector.Events()
	if len(emitted) != 1 {
		t.Fatalf("event count = %d, want 1", len(emitted))
	}
	if emitted[0].AgentID != "academic" {
		t.Fatalf("visible AgentID = %q, want %q", emitted[0].AgentID, "academic")
	}
	if got := emitted[0].Data["runtime_agent_id"]; got != "academic#replica-corr-1" {
		t.Fatalf("runtime_agent_id = %v, want runtime replica ID", got)
	}
	if got := emitted[0].Data["handoff_replica_id"]; got != "academic#replica-corr-1" {
		t.Fatalf("handoff_replica_id = %v, want runtime replica ID", got)
	}
}

func TestEffectiveHandoffBridge_PrefersReplicaBridge(t *testing.T) {
	_, baseBridge, agent := setupReplicaSupervisor(t)

	ctx, cleanup, replicaBridge, err := AttachReplicaHandoffBridge(context.Background(), agent, baseBridge, KnowledgeReplicaHandoffOptions{
		SessionID:     "sess-1",
		CorrelationID: "corr-2",
	})
	if err != nil {
		t.Fatalf("AttachReplicaHandoffBridge() error = %v", err)
	}
	defer cleanup()

	if got := EffectiveHandoffBridge(ctx, baseBridge); got != replicaBridge {
		t.Fatalf("EffectiveHandoffBridge() = %p, want replica %p", got, replicaBridge)
	}
	if got := EffectiveHandoffBridge(context.Background(), baseBridge); got != baseBridge {
		t.Fatalf("EffectiveHandoffBridge() without context = %p, want base %p", got, baseBridge)
	}
}
