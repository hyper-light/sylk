package network

import "testing"

func TestServiceRegistry_RegisterAndGet(t *testing.T) {
	sr := NewServiceRegistry()
	sr.Register(ServiceEndpoint{
		AgentID:   "agent-1",
		AgentType: "engineer",
		Healthy:   true,
		Ready:     true,
	})

	ep, ok := sr.GetEndpoint("agent-1")
	if !ok {
		t.Fatal("expected to find agent-1")
	}
	if ep.AgentType != "engineer" {
		t.Fatalf("expected engineer, got %s", ep.AgentType)
	}
}

func TestServiceRegistry_Deregister(t *testing.T) {
	sr := NewServiceRegistry()
	sr.Register(ServiceEndpoint{AgentID: "agent-1", AgentType: "engineer"})
	sr.Deregister("agent-1")

	_, ok := sr.GetEndpoint("agent-1")
	if ok {
		t.Fatal("should not find deregistered agent")
	}
	if sr.Count() != 0 {
		t.Fatalf("expected 0, got %d", sr.Count())
	}
}

func TestServiceRegistry_GetHealthyEndpoints(t *testing.T) {
	sr := NewServiceRegistry()
	sr.Register(ServiceEndpoint{AgentID: "a1", AgentType: "engineer", Healthy: true, Ready: true})
	sr.Register(ServiceEndpoint{AgentID: "a2", AgentType: "engineer", Healthy: false, Ready: true})
	sr.Register(ServiceEndpoint{AgentID: "a3", AgentType: "engineer", Healthy: true, Ready: false})
	sr.Register(ServiceEndpoint{AgentID: "a4", AgentType: "inspector", Healthy: true, Ready: true})

	healthy := sr.GetHealthyEndpoints("engineer")
	if len(healthy) != 1 {
		t.Fatalf("expected 1 healthy engineer, got %d", len(healthy))
	}
	if healthy[0].AgentID != "a1" {
		t.Fatalf("expected a1, got %s", healthy[0].AgentID)
	}
}

func TestServiceRegistry_GetEndpointsByType(t *testing.T) {
	sr := NewServiceRegistry()
	sr.Register(ServiceEndpoint{AgentID: "a1", AgentType: "engineer"})
	sr.Register(ServiceEndpoint{AgentID: "a2", AgentType: "engineer"})
	sr.Register(ServiceEndpoint{AgentID: "a3", AgentType: "inspector"})

	engineers := sr.GetEndpointsByType("engineer")
	if len(engineers) != 2 {
		t.Fatalf("expected 2 engineers, got %d", len(engineers))
	}
}

func TestServiceRegistry_UpdateHealth(t *testing.T) {
	sr := NewServiceRegistry()
	sr.Register(ServiceEndpoint{AgentID: "a1", AgentType: "engineer", Healthy: false, Ready: false})

	sr.UpdateHealth("a1", true, true)

	ep, _ := sr.GetEndpoint("a1")
	if !ep.Healthy || !ep.Ready {
		t.Fatal("health should be updated")
	}

	healthy := sr.GetHealthyEndpoints("engineer")
	if len(healthy) != 1 {
		t.Fatal("should be healthy after update")
	}
}

func TestServiceRegistry_HealthyCount(t *testing.T) {
	sr := NewServiceRegistry()
	sr.Register(ServiceEndpoint{AgentID: "a1", AgentType: "e", Healthy: true, Ready: true})
	sr.Register(ServiceEndpoint{AgentID: "a2", AgentType: "e", Healthy: false, Ready: false})
	sr.Register(ServiceEndpoint{AgentID: "a3", AgentType: "i", Healthy: true, Ready: true})

	if sr.HealthyCount() != 2 {
		t.Fatalf("expected 2 healthy, got %d", sr.HealthyCount())
	}
}
