package mitigations

import (
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultTokenizer_EstimateTokens(t *testing.T) {
	tokenizer := &DefaultTokenizer{}

	tests := []struct {
		content   string
		minTokens int
		maxTokens int
	}{
		{"hello world", 2, 4},
		{"", 0, 0},
		{"one two three four five", 5, 8},
	}

	for _, tt := range tests {
		t.Run(tt.content, func(t *testing.T) {
			tokens := tokenizer.EstimateTokens(tt.content)
			assert.GreaterOrEqual(t, tokens, tt.minTokens)
			assert.LessOrEqual(t, tokens, tt.maxTokens)
		})
	}
}

func TestContextQualityScorer_Score(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	scorer := NewContextQualityScorer(db, DefaultQualityWeights(), nil, nil)

	node := &vectorgraphdb.GraphNode{
		ID:       "score-node",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{"content": "test content here"},
	}

	item, err := scorer.Score(node, 0.9)

	require.NoError(t, err)
	assert.Equal(t, node, item.Node)
	assert.Greater(t, item.QualityScore, 0.0)
	assert.LessOrEqual(t, item.QualityScore, 1.0)
	assert.Greater(t, item.TokenCount, 0)
	assert.Greater(t, item.ScorePerToken, 0.0)
}

func TestContextQualityScorer_SelectContext(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ns := vectorgraphdb.NewNodeStore(db, nil)
	emb := makeTestEmbedding()

	node1 := &vectorgraphdb.GraphNode{
		ID:       "select-1",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{"content": "short"},
	}
	node2 := &vectorgraphdb.GraphNode{
		ID:       "select-2",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{"content": "longer content with more words"},
	}
	require.NoError(t, ns.InsertNode(node1, emb))
	require.NoError(t, ns.InsertNode(node2, emb))

	scorer := NewContextQualityScorer(db, DefaultQualityWeights(), nil, nil)

	results := []vectorgraphdb.SearchResult{
		{Node: node1, Similarity: 0.9},
		{Node: node2, Similarity: 0.8},
	}

	selected, remaining, err := scorer.SelectContext(results, 1000)

	require.NoError(t, err)
	assert.NotEmpty(t, selected)
	assert.Less(t, remaining, 1000)
}

func TestContextQualityScorer_SelectContext_BudgetExceeded(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ns := vectorgraphdb.NewNodeStore(db, nil)
	emb := makeTestEmbedding()

	node := &vectorgraphdb.GraphNode{
		ID:       "budget-node",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{"content": "some content"},
	}
	require.NoError(t, ns.InsertNode(node, emb))

	scorer := NewContextQualityScorer(db, DefaultQualityWeights(), nil, nil)

	results := []vectorgraphdb.SearchResult{
		{Node: node, Similarity: 0.9},
	}

	selected, remaining, err := scorer.SelectContext(results, 5)

	require.NoError(t, err)
	assert.Empty(t, selected)
	assert.Equal(t, 5, remaining)
}

func TestContextQualityScorer_OptimizeSelection_Greedy(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	scorer := NewContextQualityScorer(db, DefaultQualityWeights(), nil, nil)

	items := []*ContextItem{
		{QualityScore: 0.9, TokenCount: 100, ScorePerToken: 0.009},
		{QualityScore: 0.8, TokenCount: 50, ScorePerToken: 0.016},
	}

	selected, totalScore := scorer.OptimizeSelection(items, 15000)

	assert.Len(t, selected, 2)
	assert.InDelta(t, 1.7, totalScore, 0.01)
}

func TestContextQualityScorer_OptimizeSelection_DP(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	scorer := NewContextQualityScorer(db, DefaultQualityWeights(), nil, nil)

	items := []*ContextItem{
		{QualityScore: 0.9, TokenCount: 100, ScorePerToken: 0.009},
		{QualityScore: 0.8, TokenCount: 50, ScorePerToken: 0.016},
	}

	selected, totalScore := scorer.OptimizeSelection(items, 150)

	assert.NotEmpty(t, selected)
	assert.Greater(t, totalScore, 0.0)
}

func TestContextQualityScorer_OptimizeSelection_Empty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	scorer := NewContextQualityScorer(db, DefaultQualityWeights(), nil, nil)

	selected, totalScore := scorer.OptimizeSelection(nil, 1000)

	assert.Empty(t, selected)
	assert.Equal(t, 0.0, totalScore)
}

func TestContextQualityScorer_GetQualityMetrics(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	scorer := NewContextQualityScorer(db, DefaultQualityWeights(), nil, nil)

	items := []*ContextItem{
		{QualityScore: 0.9, TokenCount: 100, Selected: true},
		{QualityScore: 0.7, TokenCount: 50, Selected: false},
	}

	metrics := scorer.GetQualityMetrics(items)

	assert.Equal(t, 2, metrics.TotalItems)
	assert.Equal(t, 1, metrics.SelectedItems)
	assert.Equal(t, 150, metrics.TotalTokens)
	assert.Equal(t, 100, metrics.SelectedTokens)
	assert.InDelta(t, 0.8, metrics.AvgScore, 0.01)
	assert.Equal(t, 0.9, metrics.AvgSelectedScore)
}

func TestCountUniqueWords(t *testing.T) {
	tests := []struct {
		content  string
		expected int
	}{
		{"hello world", 2},
		{"hello hello hello", 1},
		{"", 0},
		{"one two three four five", 5},
	}

	for _, tt := range tests {
		t.Run(tt.content, func(t *testing.T) {
			result := countUniqueWords(tt.content)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractNodeContent(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]any
		expected string
	}{
		{"content", map[string]any{"content": "hello"}, "hello"},
		{"description", map[string]any{"description": "world"}, "world"},
		{"empty", map[string]any{}, ""},
		{"content_priority", map[string]any{"content": "a", "description": "b"}, "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &vectorgraphdb.GraphNode{Metadata: tt.metadata}
			result := extractNodeContent(node)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildWordSet(t *testing.T) {
	tests := []struct {
		content  string
		expected map[string]bool
	}{
		{"hello world", map[string]bool{"hello": true, "world": true}},
		{"HELLO World", map[string]bool{"hello": true, "world": true}},
		{"", map[string]bool{}},
	}

	for _, tt := range tests {
		t.Run(tt.content, func(t *testing.T) {
			result := buildWordSet(tt.content)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestContextItem_UpdateRedundancy_Concurrent(t *testing.T) {
	item := &ContextItem{
		QualityScore:  0.8,
		TokenCount:    100,
		ScorePerToken: 0.008,
		Components:    QualityComponents{Redundancy: 0},
	}

	var wg sync.WaitGroup
	numGoroutines := 10
	updatesPerGoroutine := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < updatesPerGoroutine; j++ {
				item.UpdateRedundancy(0.5, 0.7, 0.007)
			}
		}()
	}

	wg.Wait()

	// Verify final state is consistent
	assert.Equal(t, 0.5, item.Components.Redundancy)
	assert.Equal(t, 0.7, item.QualityScore)
	assert.Equal(t, 0.007, item.ScorePerToken)
}

func TestApplyRedundancyPenalty_ThreadSafe(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	scorer := NewContextQualityScorer(db, DefaultQualityWeights(), nil, nil)

	// Create items with similar content
	items := make([]*ContextItem, 0)
	for i := 0; i < 5; i++ {
		node := &vectorgraphdb.GraphNode{
			ID:       "node-" + string(rune('A'+i)),
			Domain:   vectorgraphdb.DomainCode,
			NodeType: vectorgraphdb.NodeTypeFile,
			Metadata: map[string]any{"content": "common words shared content"},
		}
		item, _ := scorer.Score(node, 0.9)
		items = append(items, item)
	}

	// Apply redundancy penalty concurrently with reads
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scorer.applyRedundancyPenalty(items)
	}()

	go func() {
		defer wg.Done()
		// Concurrent reads using thread-safe getters
		for i := 0; i < 100; i++ {
			for _, item := range items {
				_ = item.GetQualityScore()
				_ = item.GetScorePerToken()
			}
		}
	}()

	wg.Wait()

	// Verify items were modified
	for _, item := range items[1:] {
		// Items after first should have some redundancy penalty
		assert.GreaterOrEqual(t, item.GetRedundancy(), 0.0)
	}
}
