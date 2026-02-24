package network

import "testing"

func TestServiceRegistry_RegisterDefaultsWeight(t *testing.T) {
	sr := NewServiceRegistry()
	sr.Register(ServiceEndpoint{
		AgentID:   "a1",
		AgentType: "engineer",
		Healthy:   true,
		Ready:     true,
	})

	ep, ok := sr.GetEndpoint("a1")
	if !ok {
		t.Fatal("expected to find a1")
	}
	if ep.Weight != 1.0 {
		t.Fatalf("expected default weight 1.0, got %f", ep.Weight)
	}
}

func TestServiceRegistry_RegisterPreservesExplicitWeight(t *testing.T) {
	sr := NewServiceRegistry()
	sr.Register(ServiceEndpoint{
		AgentID:   "a1",
		AgentType: "engineer",
		Healthy:   true,
		Ready:     true,
		Weight:    0.5,
	})

	ep, ok := sr.GetEndpoint("a1")
	if !ok {
		t.Fatal("expected to find a1")
	}
	if ep.Weight != 0.5 {
		t.Fatalf("expected weight 0.5, got %f", ep.Weight)
	}
}

func TestServiceRegistry_UpdateQuality(t *testing.T) {
	sr := NewServiceRegistry()
	sr.Register(ServiceEndpoint{
		AgentID:   "a1",
		AgentType: "engineer",
		Healthy:   true,
		Ready:     true,
	})

	sr.UpdateQuality("a1", 0.85, 0.12)

	ep, ok := sr.GetEndpoint("a1")
	if !ok {
		t.Fatal("expected to find a1")
	}
	if ep.Quality != 0.85 {
		t.Fatalf("expected quality 0.85, got %f", ep.Quality)
	}
	if ep.StdDev != 0.12 {
		t.Fatalf("expected stddev 0.12, got %f", ep.StdDev)
	}
}

func TestServiceRegistry_UpdateQualityMissing(t *testing.T) {
	sr := NewServiceRegistry()
	// Should not panic on missing endpoint.
	sr.UpdateQuality("nonexistent", 0.9, 0.1)
}

func TestServiceRegistry_UpdateWeight(t *testing.T) {
	sr := NewServiceRegistry()
	sr.Register(ServiceEndpoint{
		AgentID:   "a1",
		AgentType: "engineer",
		Healthy:   true,
		Ready:     true,
	})

	sr.UpdateWeight("a1", 0.3)

	ep, ok := sr.GetEndpoint("a1")
	if !ok {
		t.Fatal("expected to find a1")
	}
	if ep.Weight != 0.3 {
		t.Fatalf("expected weight 0.3, got %f", ep.Weight)
	}
}

func TestServiceRegistry_UpdateWeightMissing(t *testing.T) {
	sr := NewServiceRegistry()
	// Should not panic on missing endpoint.
	sr.UpdateWeight("nonexistent", 0.5)
}

func TestServiceRegistry_GetWeightedEndpoints(t *testing.T) {
	sr := NewServiceRegistry()
	sr.Register(ServiceEndpoint{AgentID: "a1", AgentType: "engineer", Healthy: true, Ready: true, Weight: 0.3})
	sr.Register(ServiceEndpoint{AgentID: "a2", AgentType: "engineer", Healthy: true, Ready: true, Weight: 0.7})
	sr.Register(ServiceEndpoint{AgentID: "a3", AgentType: "engineer", Healthy: false, Ready: true, Weight: 1.0})
	sr.Register(ServiceEndpoint{AgentID: "a4", AgentType: "inspector", Healthy: true, Ready: true, Weight: 1.0})

	endpoints := sr.GetWeightedEndpoints("engineer")
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 healthy+ready engineers, got %d", len(endpoints))
	}

	// Should be sorted by weight descending.
	if endpoints[0].Weight < endpoints[1].Weight {
		t.Fatal("expected descending weight order")
	}
	if endpoints[0].AgentID != "a2" {
		t.Fatalf("expected a2 first (weight 0.7), got %s", endpoints[0].AgentID)
	}
}

func TestServiceRegistry_GetWeightedEndpointsEmpty(t *testing.T) {
	sr := NewServiceRegistry()
	endpoints := sr.GetWeightedEndpoints("nonexistent")
	if len(endpoints) != 0 {
		t.Fatalf("expected 0, got %d", len(endpoints))
	}
}
