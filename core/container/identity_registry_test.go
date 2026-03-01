package container

import (
	"strings"
	"testing"
)

func TestNewAgentIdentityRegistry(t *testing.T) {
	types := []string{"engineer", "architect", "designer"}
	reg := NewAgentIdentityRegistry(types)

	for _, agentType := range types {
		id, ok := reg.Get(agentType)
		if !ok {
			t.Fatalf("Get(%q) returned not-ok", agentType)
		}
		if !strings.HasPrefix(id, agentType+"_") {
			t.Errorf("Get(%q) = %q, want prefix %q_", agentType, id, agentType)
		}
		// UUID8 suffix should be 8 hex chars.
		suffix := strings.TrimPrefix(id, agentType+"_")
		if len(suffix) != 8 {
			t.Errorf("UUID8 suffix %q has len %d, want 8", suffix, len(suffix))
		}
	}
}

func TestIdentityRegistryGetUnknown(t *testing.T) {
	reg := NewAgentIdentityRegistry([]string{"engineer"})

	if _, ok := reg.Get("unknown"); ok {
		t.Fatal("Get(unknown) should return false")
	}
}

func TestIdentityRegistrySticky(t *testing.T) {
	reg := NewAgentIdentityRegistry([]string{"engineer"})

	id1, _ := reg.Get("engineer")
	id2, _ := reg.Get("engineer")

	if id1 != id2 {
		t.Errorf("IDs not sticky: %q != %q", id1, id2)
	}
}

func TestIdentityRegistryUniqueness(t *testing.T) {
	types := []string{"engineer", "architect", "designer", "inspector", "tester", "librarian"}
	reg := NewAgentIdentityRegistry(types)

	seen := make(map[string]string, len(types))
	for _, agentType := range types {
		id, _ := reg.Get(agentType)
		if prev, dup := seen[id]; dup {
			t.Fatalf("ID collision: %q and %q both got %q", prev, agentType, id)
		}
		seen[id] = agentType
	}
}
