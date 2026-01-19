package bleve

import (
	"testing"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInferDomainFromPath_AgentPaths(t *testing.T) {
	tests := []struct {
		path     string
		expected domain.Domain
	}{
		{"agents/librarian/librarian.go", domain.DomainLibrarian},
		{"agents/guide/guide.go", domain.DomainLibrarian},
		{"core/search/bleve/schema.go", domain.DomainLibrarian},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := InferDomainFromPath(tc.path)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestInferDomainFromPath_DocPaths(t *testing.T) {
	tests := []struct {
		path     string
		expected domain.Domain
	}{
		{"docs/README.md", domain.DomainAcademic},
		{"research/papers/algorithm.md", domain.DomainAcademic},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := InferDomainFromPath(tc.path)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestInferDomainFromPath_HistoryPaths(t *testing.T) {
	tests := []struct {
		path     string
		expected domain.Domain
	}{
		{"sessions/session_123.json", domain.DomainArchivalist},
		{"history/decisions/2024-01-01.md", domain.DomainArchivalist},
		{"decisions/arch/api.md", domain.DomainArchivalist},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := InferDomainFromPath(tc.path)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestInferDomainFromPath_DesignPaths(t *testing.T) {
	tests := []struct {
		path     string
		expected domain.Domain
	}{
		{"design/system.md", domain.DomainArchitect},
		{"architect/plans/v2.md", domain.DomainArchitect},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := InferDomainFromPath(tc.path)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestInferDomainFromPath_TestPaths(t *testing.T) {
	tests := []struct {
		path     string
		expected domain.Domain
	}{
		{"tests/integration_test.go", domain.DomainTester},
		{"test/unit_test.go", domain.DomainTester},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := InferDomainFromPath(tc.path)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestInferDomainFromPath_UIDesignerPaths(t *testing.T) {
	tests := []struct {
		path     string
		expected domain.Domain
	}{
		{"ui/components/button.tsx", domain.DomainDesigner},
		{"frontend/app/page.tsx", domain.DomainDesigner},
		{"styles/main.css", domain.DomainDesigner},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := InferDomainFromPath(tc.path)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestInferDomainFromPath_DefaultsToLibrarian(t *testing.T) {
	tests := []string{
		"unknown/path/file.go",
		"random.txt",
		"",
	}

	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			result := InferDomainFromPath(path)
			assert.Equal(t, domain.DomainLibrarian, result)
		})
	}
}

func TestInferDomainFromPath_CaseInsensitive(t *testing.T) {
	tests := []struct {
		path     string
		expected domain.Domain
	}{
		{"DOCS/README.md", domain.DomainAcademic},
		{"Docs/readme.md", domain.DomainAcademic},
		{"TESTS/unit_test.go", domain.DomainTester},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := InferDomainFromPath(tc.path)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestInferDomainFromType_SourceCodeTypes(t *testing.T) {
	tests := []struct {
		docType  string
		expected domain.Domain
	}{
		{"source_code", domain.DomainLibrarian},
		{"markdown", domain.DomainLibrarian},
		{"config", domain.DomainLibrarian},
	}

	for _, tc := range tests {
		t.Run(tc.docType, func(t *testing.T) {
			result := InferDomainFromType(tc.docType)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestInferDomainFromType_LLMTypes(t *testing.T) {
	tests := []struct {
		docType  string
		expected domain.Domain
	}{
		{"llm_prompt", domain.DomainArchivalist},
		{"llm_response", domain.DomainArchivalist},
	}

	for _, tc := range tests {
		t.Run(tc.docType, func(t *testing.T) {
			result := InferDomainFromType(tc.docType)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestInferDomainFromType_OtherTypes(t *testing.T) {
	tests := []struct {
		docType  string
		expected domain.Domain
	}{
		{"web_fetch", domain.DomainAcademic},
		{"note", domain.DomainArchivalist},
		{"git_commit", domain.DomainArchivalist},
	}

	for _, tc := range tests {
		t.Run(tc.docType, func(t *testing.T) {
			result := InferDomainFromType(tc.docType)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestInferDomainFromType_DefaultsToLibrarian(t *testing.T) {
	tests := []string{
		"unknown_type",
		"",
		"INVALID",
	}

	for _, docType := range tests {
		t.Run(docType, func(t *testing.T) {
			result := InferDomainFromType(docType)
			assert.Equal(t, domain.DomainLibrarian, result)
		})
	}
}

func TestInferDomainFromType_CaseInsensitive(t *testing.T) {
	tests := []struct {
		docType  string
		expected domain.Domain
	}{
		{"SOURCE_CODE", domain.DomainLibrarian},
		{"Source_Code", domain.DomainLibrarian},
		{"WEB_FETCH", domain.DomainAcademic},
	}

	for _, tc := range tests {
		t.Run(tc.docType, func(t *testing.T) {
			result := InferDomainFromType(tc.docType)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestInferDomain_ExplicitDomainTakesPriority(t *testing.T) {
	result := InferDomain("agents/librarian/lib.go", "source_code", domain.DomainTester)
	assert.Equal(t, domain.DomainTester, result)
}

func TestInferDomain_PathTakesPriorityOverType(t *testing.T) {
	result := InferDomain("docs/README.md", "source_code", domain.DomainLibrarian)
	assert.Equal(t, domain.DomainAcademic, result)
}

func TestInferDomain_FallsBackToType(t *testing.T) {
	result := InferDomain("unknown/path/file.txt", "web_fetch", domain.DomainLibrarian)
	assert.Equal(t, domain.DomainAcademic, result)
}

func TestInferDomain_FallsBackToLibrarian(t *testing.T) {
	result := InferDomain("unknown/path/file.txt", "unknown_type", domain.DomainLibrarian)
	assert.Equal(t, domain.DomainLibrarian, result)
}

func TestDomainToString(t *testing.T) {
	tests := []struct {
		domain   domain.Domain
		expected string
	}{
		{domain.DomainLibrarian, "librarian"},
		{domain.DomainAcademic, "academic"},
		{domain.DomainArchivalist, "archivalist"},
		{domain.DomainArchitect, "architect"},
		{domain.DomainEngineer, "engineer"},
		{domain.DomainDesigner, "designer"},
		{domain.DomainInspector, "inspector"},
		{domain.DomainTester, "tester"},
		{domain.DomainOrchestrator, "orchestrator"},
		{domain.DomainGuide, "guide"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			result := DomainToString(tc.domain)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestStringToDomain_ValidDomains(t *testing.T) {
	tests := []struct {
		input    string
		expected domain.Domain
	}{
		{"librarian", domain.DomainLibrarian},
		{"academic", domain.DomainAcademic},
		{"archivalist", domain.DomainArchivalist},
		{"architect", domain.DomainArchitect},
		{"engineer", domain.DomainEngineer},
		{"designer", domain.DomainDesigner},
		{"inspector", domain.DomainInspector},
		{"tester", domain.DomainTester},
		{"orchestrator", domain.DomainOrchestrator},
		{"guide", domain.DomainGuide},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result, ok := StringToDomain(tc.input)
			require.True(t, ok)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestStringToDomain_InvalidDomains(t *testing.T) {
	tests := []string{
		"invalid",
		"",
		"LIBRARIAN",
		"unknown_domain",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			_, ok := StringToDomain(input)
			assert.False(t, ok)
		})
	}
}

func TestValidDomainStrings(t *testing.T) {
	result := ValidDomainStrings()

	require.Len(t, result, 10)
	assert.Contains(t, result, "librarian")
	assert.Contains(t, result, "academic")
	assert.Contains(t, result, "archivalist")
	assert.Contains(t, result, "architect")
	assert.Contains(t, result, "engineer")
	assert.Contains(t, result, "designer")
	assert.Contains(t, result, "inspector")
	assert.Contains(t, result, "tester")
	assert.Contains(t, result, "orchestrator")
	assert.Contains(t, result, "guide")
}

func TestDomainFieldNameConstant(t *testing.T) {
	assert.Equal(t, "domain", DomainFieldName)
}

func TestDomainMappingCoversAllDocTypes(t *testing.T) {
	docTypes := []string{
		"source_code",
		"markdown",
		"config",
		"llm_prompt",
		"llm_response",
		"web_fetch",
		"note",
		"git_commit",
	}

	for _, dt := range docTypes {
		t.Run(dt, func(t *testing.T) {
			_, ok := DomainMapping[dt]
			assert.True(t, ok, "DomainMapping should contain %s", dt)
		})
	}
}

func TestPathPrefixMappingHasExpectedEntries(t *testing.T) {
	expectedPrefixes := []string{
		"agents/",
		"core/",
		"docs/",
		"research/",
		"sessions/",
		"history/",
		"decisions/",
		"design/",
		"architect/",
		"workflows/",
		"pipelines/",
		"tests/",
		"test/",
		"inspection/",
		"review/",
		"ui/",
		"frontend/",
		"styles/",
	}

	for _, prefix := range expectedPrefixes {
		t.Run(prefix, func(t *testing.T) {
			_, ok := PathPrefixMapping[prefix]
			assert.True(t, ok, "PathPrefixMapping should contain %s", prefix)
		})
	}
}
