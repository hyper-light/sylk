package container

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container/network"
)

func TestHealthSyncer_SyncsProbeState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	containerReg := NewContainerRegistry()
	serviceReg := network.NewServiceRegistry()
	scope := concurrency.NewGoroutineScope(ctx, "test", nil)

	// Register a service endpoint.
	serviceReg.Register(network.ServiceEndpoint{
		AgentID:   "agent-1",
		AgentType: "test",
		Healthy:   false,
		Ready:     false,
	})

	syncer := NewHealthSyncer(HealthSyncerConfig{
		ContainerRegistry: containerReg,
		ServiceRegistry:   serviceReg,
		Scope:             scope,
		Period:            50 * time.Millisecond,
	})

	if err := syncer.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait for at least one sync cycle.
	time.Sleep(100 * time.Millisecond)

	// With no containers registered, the service registry should remain unchanged.
	ep, ok := serviceReg.GetEndpoint("agent-1")
	if !ok {
		t.Fatal("endpoint should still exist")
	}
	// Since no container matches agent-1, UpdateHealth was never called,
	// so it stays as registered.
	if ep.Healthy {
		t.Fatal("expected healthy=false since no container updates it")
	}
}

func TestHealthSyncer_DefaultPeriod(t *testing.T) {
	syncer := NewHealthSyncer(HealthSyncerConfig{
		ContainerRegistry: NewContainerRegistry(),
		ServiceRegistry:   network.NewServiceRegistry(),
		Scope:             concurrency.NewGoroutineScope(context.Background(), "test", nil),
	})

	if syncer.period != defaultHealthSyncPeriod {
		t.Fatalf("expected default period %v, got %v",
			defaultHealthSyncPeriod, syncer.period)
	}
}
