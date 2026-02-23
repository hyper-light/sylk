package handoff

import (
	"sync"
	"testing"
)

func TestDescriptorRegistry_PrePopulated(t *testing.T) {
	r := NewDescriptorRegistry()

	expectedTypes := []string{
		"librarian", "archivalist", "academic", "architect",
		"guide", "orchestrator", "engineer", "designer",
		"inspector", "inspector-pipeline", "tester", "tester-pipeline",
	}

	for _, agentType := range expectedTypes {
		desc, ok := r.Get(agentType)
		if !ok {
			t.Errorf("missing descriptor for %q", agentType)
			continue
		}
		if desc.AgentType != agentType {
			t.Errorf("descriptor.AgentType = %q, want %q", desc.AgentType, agentType)
		}
		if desc.ContextWindow <= 0 {
			t.Errorf("descriptor.ContextWindow for %q = %d, want > 0", agentType, desc.ContextWindow)
		}
		if desc.ModelID == "" {
			t.Errorf("descriptor.ModelID for %q is empty", agentType)
		}
	}

	if r.Len() != 12 {
		t.Errorf("registry.Len() = %d, want 12", r.Len())
	}
}

func TestDescriptorRegistry_Categories(t *testing.T) {
	r := NewDescriptorRegistry()

	tests := []struct {
		agentType string
		category  AgentCategory
	}{
		{"librarian", CategoryKnowledge},
		{"archivalist", CategoryKnowledge},
		{"academic", CategoryKnowledge},
		{"architect", CategoryKnowledge},
		{"guide", CategoryStandalone},
		{"orchestrator", CategoryStandalone},
		{"engineer", CategoryPipeline},
		{"designer", CategoryPipeline},
		{"inspector", CategoryStandalone},
		{"inspector-pipeline", CategoryPipeline},
		{"tester", CategoryStandalone},
		{"tester-pipeline", CategoryPipeline},
	}

	for _, tt := range tests {
		desc, ok := r.Get(tt.agentType)
		if !ok {
			t.Errorf("missing %q", tt.agentType)
			continue
		}
		if desc.Category != tt.category {
			t.Errorf("%q category = %v, want %v", tt.agentType, desc.Category, tt.category)
		}
	}
}

func TestDescriptorRegistry_Register(t *testing.T) {
	r := NewDescriptorRegistry()

	custom := AgentDescriptor{
		AgentType:     "custom-agent",
		ModelID:       "custom-model",
		ContextWindow: 50_000,
		Category:      CategoryStandalone,
	}

	r.Register(custom)

	got, ok := r.Get("custom-agent")
	if !ok {
		t.Fatal("custom-agent not found after Register")
	}
	if got.ModelID != "custom-model" {
		t.Errorf("ModelID = %q, want %q", got.ModelID, "custom-model")
	}
}

func TestDescriptorRegistry_GetMissing(t *testing.T) {
	r := NewDescriptorRegistry()

	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("Get(nonexistent) should return false")
	}
}

func TestDescriptorRegistry_All(t *testing.T) {
	r := NewDescriptorRegistry()
	all := r.All()

	if len(all) != 12 {
		t.Errorf("All() returned %d descriptors, want 12", len(all))
	}

	// Verify all entries have non-empty fields.
	for _, d := range all {
		if d.AgentType == "" || d.ModelID == "" || d.ContextWindow <= 0 {
			t.Errorf("incomplete descriptor: %+v", d)
		}
	}
}

func TestDescriptorRegistry_ThreadSafety(t *testing.T) {
	r := NewDescriptorRegistry()
	var wg sync.WaitGroup

	// Concurrent reads.
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Get("guide")
			r.All()
			r.Len()
		}()
	}

	// Concurrent writes.
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Register(AgentDescriptor{
				AgentType:     "concurrent-" + string(rune('a'+i)),
				ModelID:       "model",
				ContextWindow: 100_000,
				Category:      CategoryStandalone,
			})
		}()
	}

	wg.Wait()
}

func TestAgentCategory_String(t *testing.T) {
	tests := []struct {
		cat  AgentCategory
		want string
	}{
		{CategoryKnowledge, "knowledge"},
		{CategoryStandalone, "standalone"},
		{CategoryPipeline, "pipeline"},
		{AgentCategory(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.cat.String(); got != tt.want {
			t.Errorf("AgentCategory(%d).String() = %q, want %q", tt.cat, got, tt.want)
		}
	}
}
