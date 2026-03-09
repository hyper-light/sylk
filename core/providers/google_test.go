package providers

import "testing"

func TestGoogleConvertTools_IncludesNativeWebSearch(t *testing.T) {
	g := &GoogleProvider{}

	tools := g.convertTools([]Tool{
		{
			Kind: ToolKindNativeWebSearch,
			Name: "web_search",
			WebSearch: &WebSearchOptions{
				EnableURLContext: true,
			},
		},
		{
			Name:        "web_fetch",
			Description: "Fetch a URL",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{"type": "string"},
				},
			},
		},
	})

	if len(tools) != 1 {
		t.Fatalf("expected single aggregated tool entry, got %d", len(tools))
	}
	if tools[0].GoogleSearch == nil {
		t.Fatal("expected google search tool to be enabled")
	}
	if tools[0].URLContext == nil {
		t.Fatal("expected URL context tool to be enabled")
	}
	if len(tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("expected one function declaration, got %d", len(tools[0].FunctionDeclarations))
	}
	if tools[0].FunctionDeclarations[0].Name != "web_fetch" {
		t.Fatalf("expected function declaration web_fetch, got %q", tools[0].FunctionDeclarations[0].Name)
	}
}
