package guide

import (
	"testing"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
)

func TestDirectAddressDetector_AtMention_Librarian(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	result := d.Detect("@librarian find the function")

	if !result.IsDirectAddress {
		t.Error("Should detect @librarian as direct address")
	}
	if result.TargetAgent != concurrency.AgentLibrarian {
		t.Errorf("Expected librarian, got %s", result.TargetAgent)
	}
	if result.CleanedQuery != "find the function" {
		t.Errorf("CleanedQuery = %s, expected 'find the function'", result.CleanedQuery)
	}
	if result.Confidence != 1.0 {
		t.Errorf("@ mention should have confidence 1.0, got %f", result.Confidence)
	}
}

func TestDirectAddressDetector_AtMention_Alias(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	result := d.Detect("@lib search for code")

	if !result.IsDirectAddress {
		t.Error("Should detect @lib as alias for librarian")
	}
	if result.TargetAgent != concurrency.AgentLibrarian {
		t.Errorf("Expected librarian, got %s", result.TargetAgent)
	}
}

func TestDirectAddressDetector_AtMention_CaseInsensitive(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	result := d.Detect("@LIBRARIAN help me")

	if !result.IsDirectAddress {
		t.Error("Should be case insensitive by default")
	}
	if result.TargetAgent != concurrency.AgentLibrarian {
		t.Errorf("Expected librarian, got %s", result.TargetAgent)
	}
}

func TestDirectAddressDetector_Greeting_Hey(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	result := d.Detect("hey architect, can you help?")

	if !result.IsDirectAddress {
		t.Error("Should detect 'hey architect' as direct address")
	}
	if result.TargetAgent != concurrency.AgentArchitect {
		t.Errorf("Expected architect, got %s", result.TargetAgent)
	}
	if result.Confidence != 0.9 {
		t.Errorf("Greeting should have confidence 0.9, got %f", result.Confidence)
	}
}

func TestDirectAddressDetector_Greeting_Hello(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	result := d.Detect("hello librarian: I need help")

	if !result.IsDirectAddress {
		t.Error("Should detect 'hello librarian:' as direct address")
	}
	if result.TargetAgent != concurrency.AgentLibrarian {
		t.Errorf("Expected librarian, got %s", result.TargetAgent)
	}
}

func TestDirectAddressDetector_Greeting_Hi(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	result := d.Detect("hi academic can you explain this?")

	if !result.IsDirectAddress {
		t.Error("Should detect 'hi academic' as direct address")
	}
	if result.TargetAgent != concurrency.AgentAcademic {
		t.Errorf("Expected academic, got %s", result.TargetAgent)
	}
}

func TestDirectAddressDetector_NoAddress(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	result := d.Detect("find the function in the codebase")

	if result.IsDirectAddress {
		t.Error("Should not detect direct address")
	}
	if result.CleanedQuery != "find the function in the codebase" {
		t.Error("CleanedQuery should be unchanged")
	}
}

func TestDirectAddressDetector_UnknownAgent(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	result := d.Detect("@unknownagent do something")

	if result.IsDirectAddress {
		t.Error("Should not detect unknown agent as direct address")
	}
}

func TestDirectAddressDetector_MiddleOfQuery(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	result := d.Detect("can you @librarian help me")

	if result.IsDirectAddress {
		t.Error("Should only detect @ at start of query")
	}
}

func TestDirectAddressDetector_StripAddressDisabled(t *testing.T) {
	d := NewDirectAddressDetector(&DirectAddressConfig{
		StripAddressFromQuery: false,
	})

	result := d.Detect("@librarian find the function")

	if result.CleanedQuery != "@librarian find the function" {
		t.Error("Should preserve full query when strip is disabled")
	}
}

func TestDirectAddressDetector_CustomAlias(t *testing.T) {
	d := NewDirectAddressDetector(&DirectAddressConfig{
		CustomAliases: map[string]concurrency.AgentType{
			"bob": concurrency.AgentLibrarian,
		},
	})

	result := d.Detect("@bob find something")

	if !result.IsDirectAddress {
		t.Error("Should detect custom alias @bob")
	}
	if result.TargetAgent != concurrency.AgentLibrarian {
		t.Errorf("Expected librarian, got %s", result.TargetAgent)
	}
}

func TestDirectAddressDetector_IsDirectAddress(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	if !d.IsDirectAddress("@librarian help") {
		t.Error("IsDirectAddress should return true")
	}

	if d.IsDirectAddress("just a normal query") {
		t.Error("IsDirectAddress should return false")
	}
}

func TestDirectAddressDetector_GetTargetAgent(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	agent, ok := d.GetTargetAgent("@architect design this")
	if !ok {
		t.Error("GetTargetAgent should return ok=true")
	}
	if agent != concurrency.AgentArchitect {
		t.Errorf("Expected architect, got %s", agent)
	}

	_, ok = d.GetTargetAgent("normal query")
	if ok {
		t.Error("GetTargetAgent should return ok=false for normal query")
	}
}

func TestDirectAddressDetector_AddAlias(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	d.AddAlias("finder", concurrency.AgentLibrarian)

	if !d.HasAlias("finder") {
		t.Error("Should have alias after adding")
	}

	result := d.Detect("@finder search")
	if !result.IsDirectAddress {
		t.Error("Should detect added alias")
	}
}

func TestDirectAddressDetector_RemoveAlias(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	d.RemoveAlias("lib")

	result := d.Detect("@lib search")
	if result.IsDirectAddress {
		t.Error("Should not detect removed alias")
	}
}

func TestDirectAddressDetector_ListAliases(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	aliases := d.ListAliases()

	if aliases["librarian"] != concurrency.AgentLibrarian {
		t.Error("Should include default aliases")
	}
}

func TestDirectAddressDetector_TargetDomain(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	result := d.Detect("@librarian find code")

	if result.TargetDomain != domain.DomainLibrarian {
		t.Errorf("Expected DomainLibrarian, got %d", result.TargetDomain)
	}
}

func TestDirectAddressDetector_AllAgentAliases(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	tests := []struct {
		alias    string
		expected concurrency.AgentType
	}{
		{"librarian", concurrency.AgentLibrarian},
		{"lib", concurrency.AgentLibrarian},
		{"academic", concurrency.AgentAcademic},
		{"prof", concurrency.AgentAcademic},
		{"professor", concurrency.AgentAcademic},
		{"archivalist", concurrency.AgentArchivalist},
		{"archivist", concurrency.AgentArchivalist},
		{"architect", concurrency.AgentArchitect},
		{"arch", concurrency.AgentArchitect},
		{"engineer", "engineer"},
		{"eng", "engineer"},
		{"designer", "designer"},
		{"tester", "tester"},
		{"orchestrator", concurrency.AgentOrchestrator},
		{"orch", concurrency.AgentOrchestrator},
		{"guide", concurrency.AgentGuide},
	}

	for _, tt := range tests {
		result := d.Detect("@" + tt.alias + " test")
		if !result.IsDirectAddress {
			t.Errorf("Should detect @%s", tt.alias)
		}
		if result.TargetAgent != tt.expected {
			t.Errorf("@%s: expected %s, got %s", tt.alias, tt.expected, result.TargetAgent)
		}
	}
}

func TestDirectAddressDetector_YoGreeting(t *testing.T) {
	d := NewDirectAddressDetector(nil)

	result := d.Detect("yo librarian what's up")

	if !result.IsDirectAddress {
		t.Error("Should detect 'yo librarian' as direct address")
	}
}

func TestDirectAddressResult_Fields(t *testing.T) {
	result := DirectAddressResult{
		IsDirectAddress: true,
		TargetAgent:     concurrency.AgentLibrarian,
		TargetDomain:    domain.DomainLibrarian,
		MatchedPattern:  "@librarian ",
		CleanedQuery:    "find code",
		Confidence:      1.0,
	}

	if !result.IsDirectAddress {
		t.Error("IsDirectAddress mismatch")
	}
	if result.TargetAgent != concurrency.AgentLibrarian {
		t.Error("TargetAgent mismatch")
	}
	if result.TargetDomain != domain.DomainLibrarian {
		t.Error("TargetDomain mismatch")
	}
	if result.MatchedPattern != "@librarian " {
		t.Error("MatchedPattern mismatch")
	}
	if result.CleanedQuery != "find code" {
		t.Error("CleanedQuery mismatch")
	}
	if result.Confidence != 1.0 {
		t.Error("Confidence mismatch")
	}
}
