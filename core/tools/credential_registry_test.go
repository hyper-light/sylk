package tools

import (
	"sync"
	"testing"
)

func TestToolCredentialRegistry_GetMetadata(t *testing.T) {
	t.Parallel()

	registry := NewToolCredentialRegistry()

	meta := registry.GetMetadata("generate_embeddings")
	if meta == nil {
		t.Fatal("should find generate_embeddings")
	}
	if meta.Name != "generate_embeddings" {
		t.Error("wrong tool name")
	}
	if len(meta.RequiredCredentials) == 0 {
		t.Error("should have required credentials")
	}
}

func TestToolCredentialRegistry_GetMetadata_NotFound(t *testing.T) {
	t.Parallel()

	registry := NewToolCredentialRegistry()

	meta := registry.GetMetadata("nonexistent_tool")
	if meta != nil {
		t.Error("should return nil for unknown tool")
	}
}

func TestToolCredentialRegistry_GetRequiredCredentials(t *testing.T) {
	t.Parallel()

	registry := NewToolCredentialRegistry()

	creds := registry.GetRequiredCredentials("generate_embeddings")
	if len(creds) == 0 {
		t.Error("should have credentials for generate_embeddings")
	}

	found := false
	for _, c := range creds {
		if c == "openai" {
			found = true
			break
		}
	}
	if !found {
		t.Error("generate_embeddings should require openai")
	}
}

func TestToolCredentialRegistry_GetRequiredCredentials_Empty(t *testing.T) {
	t.Parallel()

	registry := NewToolCredentialRegistry()

	creds := registry.GetRequiredCredentials("read_file")
	if len(creds) != 0 {
		t.Error("read_file should not require credentials")
	}
}

func TestToolCredentialRegistry_RequiresCredentials(t *testing.T) {
	t.Parallel()

	registry := NewToolCredentialRegistry()

	if !registry.RequiresCredentials("generate_embeddings") {
		t.Error("generate_embeddings should require credentials")
	}
	if !registry.RequiresCredentials("create_pr") {
		t.Error("create_pr should require credentials")
	}
	if registry.RequiresCredentials("read_file") {
		t.Error("read_file should not require credentials")
	}
	if registry.RequiresCredentials("write_file") {
		t.Error("write_file should not require credentials")
	}
}

func TestToolCredentialRegistry_Register(t *testing.T) {
	t.Parallel()

	registry := NewToolCredentialRegistry()

	registry.Register(&ToolCredentialMetadata{
		Name:                "custom_tool",
		RequiredCredentials: []string{"custom_provider"},
	})

	meta := registry.GetMetadata("custom_tool")
	if meta == nil {
		t.Fatal("should find custom_tool")
	}
	if len(meta.RequiredCredentials) != 1 {
		t.Error("should have 1 required credential")
	}
}

func TestToolCredentialRegistry_ListTools(t *testing.T) {
	t.Parallel()

	registry := NewToolCredentialRegistry()

	tools := registry.ListTools()
	if len(tools) == 0 {
		t.Error("should list registered tools")
	}

	hasEmbeddings := false
	hasReadFile := false
	for _, tool := range tools {
		if tool == "generate_embeddings" {
			hasEmbeddings = true
		}
		if tool == "read_file" {
			hasReadFile = true
		}
	}

	if !hasEmbeddings {
		t.Error("should include generate_embeddings")
	}
	if !hasReadFile {
		t.Error("should include read_file")
	}
}

func TestToolCredentialRegistry_ListToolsRequiringCredential(t *testing.T) {
	t.Parallel()

	registry := NewToolCredentialRegistry()

	githubTools := registry.ListToolsRequiringCredential("github")
	if len(githubTools) == 0 {
		t.Error("should find tools requiring github")
	}

	hasCreatePR := false
	for _, tool := range githubTools {
		if tool == "create_pr" {
			hasCreatePR = true
			break
		}
	}
	if !hasCreatePR {
		t.Error("create_pr should require github")
	}
}

func TestToolCredentialRegistry_DefaultTools(t *testing.T) {
	t.Parallel()

	registry := NewToolCredentialRegistry()

	expectedWithCredentials := map[string][]string{
		"generate_embeddings": {"openai"},
		"create_pr":           {"github"},
		"web_search":          {"serpapi"},
	}

	for tool, expectedCreds := range expectedWithCredentials {
		creds := registry.GetRequiredCredentials(tool)
		if len(creds) == 0 {
			t.Errorf("%s should have credentials", tool)
			continue
		}
		for _, expected := range expectedCreds {
			found := false
			for _, c := range creds {
				if c == expected {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s should require %s", tool, expected)
			}
		}
	}
}

func TestToolCredentialRegistry_DefaultNoCredentials(t *testing.T) {
	t.Parallel()

	registry := NewToolCredentialRegistry()

	noCredentialTools := []string{
		"read_file", "write_file", "list_directory",
		"execute_command", "grep_search", "find_files",
		"git_status", "git_diff", "git_commit",
	}

	for _, tool := range noCredentialTools {
		if registry.RequiresCredentials(tool) {
			t.Errorf("%s should not require credentials", tool)
		}
	}
}

func TestToolCredentialRegistry_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	registry := NewToolCredentialRegistry()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(3)

		go func() {
			defer wg.Done()
			_ = registry.GetMetadata("generate_embeddings")
		}()

		go func() {
			defer wg.Done()
			_ = registry.GetRequiredCredentials("create_pr")
		}()

		go func(n int) {
			defer wg.Done()
			registry.Register(&ToolCredentialMetadata{
				Name:                "concurrent_tool",
				RequiredCredentials: []string{"test"},
			})
		}(i)
	}

	wg.Wait()
}

func TestGlobalFunctions(t *testing.T) {
	t.Parallel()

	meta := GetToolCredentialMetadata("generate_embeddings")
	if meta == nil {
		t.Error("global GetToolCredentialMetadata should work")
	}

	creds := GetRequiredCredentials("create_pr")
	if len(creds) == 0 {
		t.Error("global GetRequiredCredentials should work")
	}

	if !RequiresCredentials("web_search") {
		t.Error("global RequiresCredentials should work")
	}
}
