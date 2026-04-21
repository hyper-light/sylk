package prompts

import (
	"strings"
	"testing"
)

// TestFabricAwarenessPromptIsLoadable verifies the shared/fabric_awareness.md
// prompt is embedded and loadable through the Load API. Every agent's
// system prompt builder must include this so the awareness model is
// uniform across the system.
func TestFabricAwarenessPromptIsLoadable(t *testing.T) {
	got, err := Load("shared", "fabric_awareness")
	if err != nil {
		t.Fatalf("Load shared/fabric_awareness: %v", err)
	}
	for _, want := range []string{
		"Activity Fabric Awareness",
		"query_peer_activity",
		"causal_trace",
		"find_related_activity",
		"inspect_open_conflicts",
		"consult_peer",
		"challenge_peer",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fabric_awareness prompt missing required marker %q", want)
		}
	}
}

// TestFabricInspectorAuditPromptIsLoadable verifies the inspector's
// audit-clause extension is loadable. Inspector prompt builders
// concatenate this onto the standard fabric awareness prompt.
func TestFabricInspectorAuditPromptIsLoadable(t *testing.T) {
	got, err := Load("shared", "fabric_inspector_audit")
	if err != nil {
		t.Fatalf("Load shared/fabric_inspector_audit: %v", err)
	}
	for _, want := range []string{
		"inspect_open_activity",
		"challenge_peer",
		"request_override",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fabric_inspector_audit prompt missing required marker %q", want)
		}
	}
}

// TestFabricKnowledgeAdvisoryPromptIsLoadable verifies the knowledge
// agent's advisory clause is loadable. Librarian/academic/archivalist
// prompt builders include this so they emit advisories on every
// consult and subscribe to the fabric for proactive notifications.
func TestFabricKnowledgeAdvisoryPromptIsLoadable(t *testing.T) {
	got, err := Load("shared", "fabric_knowledge_advisory")
	if err != nil {
		t.Fatalf("Load shared/fabric_knowledge_advisory: %v", err)
	}
	for _, want := range []string{
		"advisory_emitted",
		"proactive_advisory",
		"EmitProactiveAdvisory",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fabric_knowledge_advisory prompt missing required marker %q", want)
		}
	}
}
