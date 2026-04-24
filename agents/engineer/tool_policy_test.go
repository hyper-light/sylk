package engineer

import "testing"

func TestEngineerBuildToolDefinitions_IncludeDiscoveryAndQualityTools(t *testing.T) {
	e, err := New(Config{Factory: newTestFactory(t)}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if e.tools != nil {
			e.tools.Close()
		}
	})

	names := make(map[string]struct{})
	for _, tool := range e.buildToolDefinitions() {
		names[tool.Name] = struct{}{}
	}

	// Phase 1 refactor: `consult` and `signal_orchestrator` removed.
	// `consult_peer` replaces `consult`; the orchestrator consumes
	// fabric activities without a dedicated signal skill.
	for _, want := range []string{
		"search_skills",
		"discover_project_tools",
		"discover_code_patterns",
		// Phase 2.K / GT-4 + GI-5: collapsed into dependency(action=…).
		"dependency",
		"lsp",
		"format",
		"lint",
		"fabric_query_claims_board",
		"audit",
		"report_confidence",
	} {
		if _, ok := names[want]; !ok {
			t.Fatalf("expected tool definitions to include %q; got %v", want, names)
		}
	}
}
