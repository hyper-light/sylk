package mitigations

import (
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrustHierarchy_GetTrustInfo_CodeDomain(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ns := vectorgraphdb.NewNodeStore(db, nil)
	node := &vectorgraphdb.GraphNode{
		ID:       "code-trust-node",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{},
	}
	emb := makeTestEmbedding()
	require.NoError(t, ns.InsertNode(node, emb))

	trust := NewTrustHierarchy(db, nil, nil)

	info, err := trust.GetTrustInfo("code-trust-node")

	require.NoError(t, err)
	assert.Equal(t, TrustVerifiedCode, info.TrustLevel)
	assert.Equal(t, 1.0, info.BaseScore)
}

func TestTrustHierarchy_GetTrustInfo_HistoryDomain_Recent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ns := vectorgraphdb.NewNodeStore(db, nil)
	node := &vectorgraphdb.GraphNode{
		ID:       "hist-trust-node",
		Domain:   vectorgraphdb.DomainHistory,
		NodeType: vectorgraphdb.NodeTypeDecision,
		Metadata: map[string]any{},
	}
	emb := makeTestEmbedding()
	require.NoError(t, ns.InsertNode(node, emb))

	trust := NewTrustHierarchy(db, nil, nil)

	info, err := trust.GetTrustInfo("hist-trust-node")

	require.NoError(t, err)
	assert.Equal(t, TrustRecentHistory, info.TrustLevel)
	assert.Equal(t, 0.9, info.BaseScore)
}

func TestTrustHierarchy_ApplyTrust(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ns := vectorgraphdb.NewNodeStore(db, nil)
	node := &vectorgraphdb.GraphNode{
		ID:       "apply-trust-node",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{},
	}
	emb := makeTestEmbedding()
	require.NoError(t, ns.InsertNode(node, emb))

	trust := NewTrustHierarchy(db, nil, nil)

	results := []vectorgraphdb.SearchResult{
		{Node: node, Similarity: 0.9},
	}

	trusted := trust.ApplyTrust(results)

	assert.Len(t, trusted, 1)
	assert.LessOrEqual(t, trusted[0].Similarity, 0.9*1.1)
}

func TestTrustHierarchy_Promote(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ns := vectorgraphdb.NewNodeStore(db, nil)
	node := &vectorgraphdb.GraphNode{
		ID:       "promote-node",
		Domain:   vectorgraphdb.DomainAcademic,
		NodeType: vectorgraphdb.NodeTypePaper,
		Metadata: map[string]any{},
	}

	emb := makeTestEmbedding()
	require.NoError(t, ns.InsertNode(node, emb))

	trust := NewTrustHierarchy(db, nil, nil)

	err := trust.Promote("promote-node", "verified by human", "test-user")

	assert.NoError(t, err)

	history := trust.GetTrustHistory("promote-node")
	assert.Len(t, history, 1)
	assert.Equal(t, "promote", history[0].Action)
}

func TestTrustHierarchy_Demote(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ns := vectorgraphdb.NewNodeStore(db, nil)
	node := &vectorgraphdb.GraphNode{
		ID:       "demote-node",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{},
	}
	emb := makeTestEmbedding()
	require.NoError(t, ns.InsertNode(node, emb))

	trust := NewTrustHierarchy(db, nil, nil)

	err := trust.Demote("demote-node", "found to be inaccurate", "test-user")

	assert.NoError(t, err)

	history := trust.GetTrustHistory("demote-node")
	assert.Len(t, history, 1)
	assert.Equal(t, "demote", history[0].Action)
}

func TestTrustHierarchy_FilterByMinTrust(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ns := vectorgraphdb.NewNodeStore(db, nil)
	emb := makeTestEmbedding()

	codeNode := &vectorgraphdb.GraphNode{
		ID:       "code-filter",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{},
	}
	require.NoError(t, ns.InsertNode(codeNode, emb))

	acadNode := &vectorgraphdb.GraphNode{
		ID:       "acad-filter",
		Domain:   vectorgraphdb.DomainAcademic,
		NodeType: vectorgraphdb.NodeTypePaper,
		Metadata: map[string]any{},
	}
	require.NoError(t, ns.InsertNode(acadNode, emb))

	trust := NewTrustHierarchy(db, nil, nil)

	results := []vectorgraphdb.SearchResult{
		{Node: codeNode, Similarity: 0.9},
		{Node: acadNode, Similarity: 0.8},
	}

	filtered := trust.FilterByMinTrust(results, TrustOfficialDocs)

	assert.Len(t, filtered, 1)
	assert.Equal(t, "code-filter", filtered[0].Node.ID)
}

func TestTrustHierarchy_ExportReport(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	trust := NewTrustHierarchy(db, nil, nil)

	report, err := trust.ExportTrustReport()

	require.NoError(t, err)
	assert.NotNil(t, report)
	assert.Equal(t, 0, report.TotalNodes)
}

func TestPromoteTrustLevel(t *testing.T) {
	tests := []struct {
		current  TrustLevel
		expected TrustLevel
	}{
		{TrustUnknown, TrustLLMInference},
		{TrustLLMInference, TrustExternalArticle},
		{TrustExternalArticle, TrustOldHistory},
		{TrustOldHistory, TrustOfficialDocs},
		{TrustOfficialDocs, TrustRecentHistory},
		{TrustRecentHistory, TrustVerifiedCode},
		{TrustVerifiedCode, TrustVerifiedCode},
	}

	for _, tt := range tests {
		t.Run(tt.current.String(), func(t *testing.T) {
			result := promoteTrustLevel(tt.current)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDemoteTrustLevel(t *testing.T) {
	tests := []struct {
		current  TrustLevel
		expected TrustLevel
	}{
		{TrustVerifiedCode, TrustRecentHistory},
		{TrustRecentHistory, TrustOfficialDocs},
		{TrustOfficialDocs, TrustOldHistory},
		{TrustOldHistory, TrustExternalArticle},
		{TrustExternalArticle, TrustLLMInference},
		{TrustLLMInference, TrustUnknown},
		{TrustUnknown, TrustUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.current.String(), func(t *testing.T) {
			result := demoteTrustLevel(tt.current)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTrustHierarchy_ComputeAggregateTrust(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ns := vectorgraphdb.NewNodeStore(db, nil)
	emb := makeTestEmbedding()

	node1 := &vectorgraphdb.GraphNode{
		ID: "agg-1", Domain: vectorgraphdb.DomainCode, NodeType: vectorgraphdb.NodeTypeFile, Metadata: map[string]any{},
	}
	node2 := &vectorgraphdb.GraphNode{
		ID: "agg-2", Domain: vectorgraphdb.DomainCode, NodeType: vectorgraphdb.NodeTypeFile, Metadata: map[string]any{},
	}
	require.NoError(t, ns.InsertNode(node1, emb))
	require.NoError(t, ns.InsertNode(node2, emb))

	trust := NewTrustHierarchy(db, nil, nil)

	aggregate, err := trust.ComputeAggregateTrust([]string{"agg-1", "agg-2"})

	require.NoError(t, err)
	assert.Greater(t, aggregate, 0.0)
}

func TestTrustHierarchy_ComputeAggregateTrust_Empty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	trust := NewTrustHierarchy(db, nil, nil)

	_, err := trust.ComputeAggregateTrust([]string{})

	assert.Error(t, err)
}

func TestTrustHierarchy_DetermineHistoryTrustLevel_Old(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ns := vectorgraphdb.NewNodeStore(db, nil)
	emb := makeTestEmbedding()

	recentNode := &vectorgraphdb.GraphNode{
		ID:       "recent-hist-node",
		Domain:   vectorgraphdb.DomainHistory,
		NodeType: vectorgraphdb.NodeTypeDecision,
		Metadata: map[string]any{},
	}
	require.NoError(t, ns.InsertNode(recentNode, emb))

	trust := NewTrustHierarchy(db, nil, nil)
	info, err := trust.GetTrustInfo("recent-hist-node")

	require.NoError(t, err)
	assert.Equal(t, TrustRecentHistory, info.TrustLevel)
}

func TestClampTrustScore(t *testing.T) {
	tests := []struct {
		name     string
		input    float64
		expected float64
	}{
		{"negative_clamped_to_zero", -0.5, 0.0},
		{"zero_unchanged", 0.0, 0.0},
		{"valid_low", 0.3, 0.3},
		{"valid_mid", 0.5, 0.5},
		{"valid_high", 0.9, 0.9},
		{"one_unchanged", 1.0, 1.0},
		{"above_one_clamped", 1.5, 1.0},
		{"large_negative", -10.0, 0.0},
		{"large_positive", 10.0, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := clampTrustScore(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateTrustScore(t *testing.T) {
	tests := []struct {
		name      string
		score     float64
		field     string
		wantError bool
	}{
		{"valid_zero", 0.0, "test", false},
		{"valid_mid", 0.5, "test", false},
		{"valid_one", 1.0, "test", false},
		{"negative_invalid", -0.1, "base_score", true},
		{"above_one_invalid", 1.1, "trust_score", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTrustScore(tt.score, tt.field)
			if tt.wantError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.field)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestTrustError_Unwrap(t *testing.T) {
	innerErr := assert.AnError
	trustErr := &TrustError{Op: "test", Field: "score", Value: 1.5, Err: innerErr}

	assert.Equal(t, innerErr, trustErr.Unwrap())
	assert.Contains(t, trustErr.Error(), "test")
	assert.Contains(t, trustErr.Error(), "score")
	assert.Contains(t, trustErr.Error(), "1.5")
}

func TestTrustError_NoField(t *testing.T) {
	innerErr := assert.AnError
	trustErr := &TrustError{Op: "compute", Err: innerErr}

	msg := trustErr.Error()
	assert.Contains(t, msg, "trust compute")
	assert.NotContains(t, msg, "value")
}

func TestTrustHierarchy_ScoresBounded(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ns := vectorgraphdb.NewNodeStore(db, nil)
	node := &vectorgraphdb.GraphNode{
		ID:       "bounded-trust-node",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{},
	}
	emb := makeTestEmbedding()
	require.NoError(t, ns.InsertNode(node, emb))

	trust := NewTrustHierarchy(db, nil, nil)

	info, err := trust.GetTrustInfo("bounded-trust-node")

	require.NoError(t, err)
	assert.GreaterOrEqual(t, info.TrustScore, 0.0)
	assert.LessOrEqual(t, info.TrustScore, 1.0)
	assert.GreaterOrEqual(t, info.BaseScore, 0.0)
	assert.LessOrEqual(t, info.BaseScore, 1.0)
	assert.GreaterOrEqual(t, info.EffectiveScore, 0.0)
	assert.LessOrEqual(t, info.EffectiveScore, 1.0)
}
