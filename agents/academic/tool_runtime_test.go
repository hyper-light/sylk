package academic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

func TestAcademicToolInvocationsUseManifestAgentID(t *testing.T) {
	a, err := New(Config{ID: "academic-custom"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	if got, want := a.toolRuntime().AgentID(), "academic"; got != want {
		t.Fatalf("runtime agent id = %q, want %q", got, want)
	}

	invocations := a.toolInvocations(context.Background(), []providers.ToolCall{{
		ID:        "call-1",
		Name:      "search_skills",
		Arguments: `{"query":"research"}`,
	}})
	if len(invocations) != 1 {
		t.Fatalf("toolInvocations len = %d, want 1", len(invocations))
	}
	if got, want := invocations[0].AgentID, a.toolRuntime().AgentID(); got != want {
		t.Fatalf("invocation agent id = %q, want %q", got, want)
	}
	if invocations[0].AgentID == a.id {
		t.Fatalf("invocation agent id unexpectedly used instance id %q", a.id)
	}
}

func TestAcademicExecuteToolCallAllowsSearchSkillsWithCustomInstanceID(t *testing.T) {
	a, err := New(Config{ID: "academic-custom"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	_, err = a.executeToolCall(context.Background(), providers.ToolCall{
		ID:        "call-1",
		Name:      "search_skills",
		Arguments: `{"query":"research"}`,
	})
	if err != nil {
		t.Fatalf("search_skills should execute for academic runtime: %v", err)
	}
}

func TestAcademicManifestHidesRecursiveResearchWrappers(t *testing.T) {
	a, err := New(Config{ID: "academic-custom"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	tools := a.buildToolDefinitions()
	visible := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		visible[tool.Name] = struct{}{}
	}

	for _, name := range academicRecursiveSkillNames() {
		if _, ok := visible[name]; ok {
			t.Fatalf("recursive skill %q should not be visible in academic tool definitions", name)
		}
		policy, ok := academicToolManifest(a.skills).Policy(name)
		if !ok {
			t.Fatalf("expected policy for %q", name)
		}
		if policy.Searchable {
			t.Fatalf("recursive skill %q should not be searchable", name)
		}
		if policy.VisibleByDefault {
			t.Fatalf("recursive skill %q should not be visible by default", name)
		}
	}
}

func TestAcademicSearchSkillsDoesNotReturnRecursiveResearchWrappers(t *testing.T) {
	a, err := New(Config{ID: "academic-custom"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	result, err := a.executeToolCall(context.Background(), providers.ToolCall{
		ID:        "call-2",
		Name:      "search_skills",
		Arguments: `{"query":"research best practices compare solution","activate":false,"limit":10}`,
	})
	if err != nil {
		t.Fatalf("execute search_skills: %v", err)
	}

	var payload struct {
		Matches []struct {
			Name string `json:"name"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("unmarshal search_skills payload: %v", err)
	}

	blocked := make(map[string]struct{}, len(academicRecursiveSkillNames()))
	for _, name := range academicRecursiveSkillNames() {
		blocked[name] = struct{}{}
	}
	for _, match := range payload.Matches {
		if _, ok := blocked[match.Name]; ok {
			t.Fatalf("search_skills returned recursive academic wrapper %q", match.Name)
		}
	}
}

func TestAcademicBuildToolDefinitionsIncludesNativeWebSearch(t *testing.T) {
	a, err := New(Config{ID: "academic-custom"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	tools := a.buildToolDefinitions()
	for _, tool := range tools {
		if tool.Name != "web_search" {
			continue
		}
		if got, want := tool.ResolvedKind(), providers.ToolKindNativeWebSearch; got != want {
			t.Fatalf("web_search tool kind = %q, want %q", got, want)
		}
		if tool.WebSearch == nil {
			t.Fatal("web_search native config should be present")
		}
		if got, want := tool.WebSearch.MaxUses, 6; got != want {
			t.Fatalf("web_search max uses = %d, want %d", got, want)
		}
		return
	}

	t.Fatal("expected academic tools to include web_search")
}

func TestAcademicBuildToolDefinitionsIncludesGroundSource(t *testing.T) {
	a, err := New(Config{ID: "academic-custom"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	tools := a.buildToolDefinitions()
	for _, tool := range tools {
		if tool.Name != "ground_source" {
			continue
		}
		if got, want := tool.ResolvedKind(), providers.ToolKindFunction; got != want {
			t.Fatalf("ground_source tool kind = %q, want %q", got, want)
		}
		return
	}

	t.Fatal("expected academic tools to include ground_source")
}

func TestAcademicExecuteToolCallRejectsProviderNativeWebSearch(t *testing.T) {
	a, err := New(Config{ID: "academic-custom"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	_, err = a.executeToolCall(context.Background(), providers.ToolCall{
		ID:   "call-native-web-search",
		Name: "web_search",
	})
	if err == nil {
		t.Fatal("expected provider-native web_search to reject local execution")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "web_search") || !strings.Contains(got, "provider-native") {
		t.Fatalf("unexpected error: %v", err)
	}
}
