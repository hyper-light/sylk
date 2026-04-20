package scribe

import (
	"testing"
)

// TestStoreArchivalistSkill_DeclaresSummaryRequired locks the schema
// fix for the failure mode where the LLM called store_archivalist
// with a commentary object that omitted summary (only filled
// decisions, etc.) and tripped the runtime "commentary.summary is
// required" check. Pre-fix, the schema only marked the outer
// commentary object as required — the LLM legitimately satisfied the
// schema by providing ANY commentary object, then ran into the
// runtime gate. Post-fix, summary is required at the schema level so
// the provider surfaces it as a tool-call validation failure before
// the LLM even ships the call, and the model fills it in correctly
// on the first try.
func TestStoreArchivalistSkill_DeclaresSummaryRequired(t *testing.T) {
	scribe := &Scribe{}
	skill := scribe.storeArchivalistSkill(&scribeRunState{})
	if skill == nil {
		t.Fatal("storeArchivalistSkill returned nil")
	}
	if skill.InputSchema == nil {
		t.Fatal("store_archivalist InputSchema is nil")
	}
	commentary, ok := skill.InputSchema.Properties["commentary"]
	if !ok || commentary == nil {
		t.Fatal("store_archivalist schema missing commentary property")
	}
	if commentary.Type != "object" {
		t.Fatalf("commentary.Type = %q, want object", commentary.Type)
	}
	if len(commentary.Required) == 0 {
		t.Fatal("commentary.Required is empty; the schema must mark summary as required so the LLM cannot ship a call that omits it")
	}
	found := false
	for _, field := range commentary.Required {
		if field == "summary" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("commentary.Required = %v; want it to include \"summary\"", commentary.Required)
	}
}
