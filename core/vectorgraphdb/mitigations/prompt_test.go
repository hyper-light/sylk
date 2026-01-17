package mitigations

import (
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLLMContextBuilder_Build(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	scorer := NewContextQualityScorer(db, DefaultQualityWeights(), nil, nil)
	builder := NewLLMContextBuilder(scorer, nil, nil, nil)

	codeNode := &vectorgraphdb.GraphNode{
		ID:        "code-1",
		Domain:    vectorgraphdb.DomainCode,
		NodeType:  vectorgraphdb.NodeTypeFile,
		Metadata:  map[string]any{"content": "func main() {}", "path": "/main.go"},
		UpdatedAt: time.Now(),
	}
	histNode := &vectorgraphdb.GraphNode{
		ID:        "hist-1",
		Domain:    vectorgraphdb.DomainHistory,
		NodeType:  vectorgraphdb.NodeTypeDecision,
		Metadata:  map[string]any{"content": "decided to use X"},
		UpdatedAt: time.Now(),
	}

	items := []*ContextItem{
		{Node: codeNode, Selected: true, TokenCount: 50},
		{Node: histNode, Selected: true, TokenCount: 30},
	}

	ctx, err := builder.Build(items)

	require.NoError(t, err)
	assert.NotEmpty(t, ctx.Preamble)
	assert.Len(t, ctx.CodeContext, 1)
	assert.Len(t, ctx.HistContext, 1)
	assert.Equal(t, 80, ctx.TotalTokens)
}

func TestLLMContextBuilder_Build_UnselectedItems(t *testing.T) {
	_, cleanup := setupTestDB(t)
	defer cleanup()

	builder := NewLLMContextBuilder(nil, nil, nil, nil)

	node := &vectorgraphdb.GraphNode{
		ID:       "unselected",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{},
	}

	items := []*ContextItem{
		{Node: node, Selected: false, TokenCount: 50},
	}

	ctx, err := builder.Build(items)

	require.NoError(t, err)
	assert.Empty(t, ctx.CodeContext)
	assert.Equal(t, 0, ctx.TotalTokens)
}

func TestLLMContext_FormatAsMarkdown(t *testing.T) {
	ctx := &LLMContext{
		Preamble: "Test preamble",
		CodeContext: []*AnnotatedItem{
			{
				Content:    "func test() {}",
				TrustLevel: TrustVerifiedCode,
				TrustScore: 1.0,
				Source:     "/test.go",
				Freshness:  time.Now(),
			},
		},
	}

	md := ctx.FormatAsMarkdown()

	assert.Contains(t, md, "## Context Information")
	assert.Contains(t, md, "Test preamble")
	assert.Contains(t, md, "### Code Context")
	assert.Contains(t, md, "verified_code")
	assert.Contains(t, md, "/test.go")
	assert.Contains(t, md, "func test() {}")
}

func TestLLMContext_FormatAsMarkdown_WithConflicts(t *testing.T) {
	ctx := &LLMContext{
		Preamble: "Test",
		Conflicts: []*ConflictSummary{
			{
				ConflictID:  "c1",
				Type:        ConflictSemantic,
				ItemAID:     "a",
				ItemBID:     "b",
				Description: "conflict desc",
			},
		},
	}

	md := ctx.FormatAsMarkdown()

	assert.Contains(t, md, "### Unresolved Conflicts")
	assert.Contains(t, md, "semantic")
	assert.Contains(t, md, "conflict desc")
}

func TestLLMContext_FormatAsJSON(t *testing.T) {
	ctx := &LLMContext{
		Preamble:    "test",
		TotalTokens: 100,
	}

	json := ctx.FormatAsJSON()

	assert.Contains(t, json, `"preamble"`)
	assert.Contains(t, json, `"total_tokens"`)
	assert.Contains(t, json, "100")
}

func TestLLMContext_FormatAsXML(t *testing.T) {
	ctx := &LLMContext{
		Preamble:    "test preamble",
		TotalTokens: 50,
		CodeContext: []*AnnotatedItem{
			{
				Content:    "code content",
				TrustLevel: TrustVerifiedCode,
				TrustScore: 0.95,
				Source:     "/src.go",
				Freshness:  time.Now(),
			},
		},
	}

	xml := ctx.FormatAsXML()

	assert.Contains(t, xml, `<?xml version="1.0" encoding="UTF-8"?>`)
	assert.Contains(t, xml, "<context>")
	assert.Contains(t, xml, "</context>")
	assert.Contains(t, xml, "<preamble>")
	assert.Contains(t, xml, "<code_context>")
	assert.Contains(t, xml, "<total_tokens>50</total_tokens>")
}

func TestLLMContext_FormatAsSystemPrompt(t *testing.T) {
	ctx := &LLMContext{
		Preamble: "instructions here",
		CodeContext: []*AnnotatedItem{
			{
				Content:    "code here",
				TrustLevel: TrustVerifiedCode,
				Source:     "/file.go",
			},
		},
		HistContext: []*AnnotatedItem{
			{
				Content:    "history here",
				TrustLevel: TrustRecentHistory,
				Source:     "session-1",
			},
		},
		Conflicts: []*ConflictSummary{
			{ItemAID: "a", ItemBID: "b", Description: "conflict"},
		},
	}

	prompt := ctx.FormatAsSystemPrompt()

	assert.Contains(t, prompt, "<system_context>")
	assert.Contains(t, prompt, "</system_context>")
	assert.Contains(t, prompt, "<code_reference>")
	assert.Contains(t, prompt, "<history_reference>")
	assert.Contains(t, prompt, "<conflicts>")
	assert.Contains(t, prompt, "WARNING:")
}

func TestLLMContext_EstimateTokens(t *testing.T) {
	ctx := &LLMContext{TotalTokens: 150}

	assert.Equal(t, 150, ctx.EstimateTokens())
}

func TestLLMContext_GetItemCount(t *testing.T) {
	ctx := &LLMContext{
		CodeContext: []*AnnotatedItem{{}, {}},
		HistContext: []*AnnotatedItem{{}},
		AcadContext: []*AnnotatedItem{{}, {}, {}},
	}

	assert.Equal(t, 6, ctx.GetItemCount())
}

func TestLLMContext_HasConflicts(t *testing.T) {
	noConflicts := &LLMContext{}
	assert.False(t, noConflicts.HasConflicts())

	withConflicts := &LLMContext{
		Conflicts: []*ConflictSummary{{}},
	}
	assert.True(t, withConflicts.HasConflicts())
}

func TestGeneratePreamble(t *testing.T) {
	builder := NewLLMContextBuilder(nil, nil, nil, nil)

	preamble := builder.generatePreamble()

	assert.Contains(t, preamble, "CONTEXT TRUST LEVELS")
	assert.Contains(t, preamble, "verified_code")
	assert.Contains(t, preamble, "INSTRUCTIONS")
	assert.True(t, len(preamble) > 100)
}

func TestLLMContextBuilder_AddToDomain(t *testing.T) {
	ctx := &LLMContext{
		CodeContext: make([]*AnnotatedItem, 0),
		HistContext: make([]*AnnotatedItem, 0),
		AcadContext: make([]*AnnotatedItem, 0),
	}
	builder := &LLMContextBuilder{}

	codeItem := &AnnotatedItem{Content: "code"}
	histItem := &AnnotatedItem{Content: "hist"}
	acadItem := &AnnotatedItem{Content: "acad"}

	builder.addToDomain(ctx, codeItem, vectorgraphdb.DomainCode)
	builder.addToDomain(ctx, histItem, vectorgraphdb.DomainHistory)
	builder.addToDomain(ctx, acadItem, vectorgraphdb.DomainAcademic)

	assert.Len(t, ctx.CodeContext, 1)
	assert.Len(t, ctx.HistContext, 1)
	assert.Len(t, ctx.AcadContext, 1)
}

func TestLLMContext_FormatAsXML_WithConflicts(t *testing.T) {
	ctx := &LLMContext{
		Preamble: "test",
		Conflicts: []*ConflictSummary{
			{
				Type:        ConflictSemantic,
				ItemAID:     "a",
				ItemBID:     "b",
				Description: "desc",
			},
		},
	}

	xml := ctx.FormatAsXML()

	assert.Contains(t, xml, "<conflicts>")
	assert.Contains(t, xml, `type="semantic"`)
	assert.Contains(t, xml, `items="a,b"`)
}

func TestLLMContext_EmptyDomains(t *testing.T) {
	ctx := &LLMContext{
		Preamble: "test",
	}

	md := ctx.FormatAsMarkdown()
	xml := ctx.FormatAsXML()
	prompt := ctx.FormatAsSystemPrompt()

	assert.NotContains(t, md, "### Code Context")
	assert.NotContains(t, xml, "<code_context>")
	assert.NotContains(t, prompt, "<code_reference>")
}

func TestWriteReferenceBlock(t *testing.T) {
	ctx := &LLMContext{}
	var sb strings.Builder

	items := []*AnnotatedItem{
		{Content: "test", TrustLevel: TrustVerifiedCode, Source: "/test.go"},
	}

	ctx.writeReferenceBlock(&sb, "test_block", items)

	result := sb.String()
	assert.Contains(t, result, "<test_block>")
	assert.Contains(t, result, "</test_block>")
	assert.Contains(t, result, "verified_code")
}

func TestWriteReferenceBlock_Empty(t *testing.T) {
	ctx := &LLMContext{}
	var sb strings.Builder

	ctx.writeReferenceBlock(&sb, "test_block", nil)

	assert.Empty(t, sb.String())
}
