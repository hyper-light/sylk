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

	for _, want := range []string{
		"search_skills",
		"discover_project_tools",
		"discover_code_patterns",
		"research_dependency_install",
		"install_dependency_tooling",
		"lsp",
		"format",
		"lint",
		"consult",
		"audit",
		"report_confidence",
		"signal_orchestrator",
	} {
		if _, ok := names[want]; !ok {
			t.Fatalf("expected tool definitions to include %q; got %v", want, names)
		}
	}
}
