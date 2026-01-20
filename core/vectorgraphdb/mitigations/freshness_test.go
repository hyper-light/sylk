package mitigations

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFreshnessTracker_GetFreshness(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ns := vectorgraphdb.NewNodeStore(db, nil)
	node := &vectorgraphdb.GraphNode{
		ID:       "test-fresh-node",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{"path": "/test.go"},
	}
	require.NoError(t, ns.InsertNode(node, makeTestEmbedding()))

	tracker := NewFreshnessTracker(db, DefaultDecayConfig())

	info, err := tracker.GetFreshness("test-fresh-node")

	require.NoError(t, err)
	assert.Equal(t, "test-fresh-node", info.NodeID)
	assert.False(t, info.IsStale)
	assert.Greater(t, info.FreshnessScore, 0.0)
	assert.Equal(t, 0.01, info.DecayRate)
}

func TestFreshnessTracker_MarkStale(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ns := vectorgraphdb.NewNodeStore(db, nil)
	node := &vectorgraphdb.GraphNode{
		ID:       "test-stale-node",
		Domain:   vectorgraphdb.DomainHistory,
		NodeType: vectorgraphdb.NodeTypeDecision,
		Metadata: map[string]any{},
	}
	require.NoError(t, ns.InsertNode(node, makeTestEmbedding()))

	tracker := NewFreshnessTracker(db, DefaultDecayConfig())

	err := tracker.MarkStale("test-stale-node")
	require.NoError(t, err)

	info, err := tracker.GetFreshness("test-stale-node")
	require.NoError(t, err)
	assert.True(t, info.IsStale)
	assert.Equal(t, 0.3, info.FreshnessScore)
}

func TestFreshnessTracker_ApplyDecay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ns := vectorgraphdb.NewNodeStore(db, nil)
	node := &vectorgraphdb.GraphNode{
		ID:       "test-decay-node",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{},
	}
	require.NoError(t, ns.InsertNode(node, makeTestEmbedding()))

	tracker := NewFreshnessTracker(db, DefaultDecayConfig())

	results := []vectorgraphdb.SearchResult{
		{Node: node, Similarity: 0.9},
	}

	decayed := tracker.ApplyDecay(results)

	assert.Len(t, decayed, 1)
	assert.Greater(t, decayed[0].Similarity, 0.0)
}

func TestFreshnessTracker_GetStaleCount(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := NewFreshnessTracker(db, DefaultDecayConfig())

	assert.Equal(t, 0, tracker.GetStaleCount())
}

func TestFreshnessTracker_DecayRates(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	config := DefaultDecayConfig()
	tracker := NewFreshnessTracker(db, config)

	ns := vectorgraphdb.NewNodeStore(db, nil)
	emb := makeTestEmbedding()

	codeNode := &vectorgraphdb.GraphNode{
		ID:       "code-node",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{},
	}
	histNode := &vectorgraphdb.GraphNode{
		ID:       "hist-node",
		Domain:   vectorgraphdb.DomainHistory,
		NodeType: vectorgraphdb.NodeTypeDecision,
		Metadata: map[string]any{},
	}
	acadNode := &vectorgraphdb.GraphNode{
		ID:       "acad-node",
		Domain:   vectorgraphdb.DomainAcademic,
		NodeType: vectorgraphdb.NodeTypeDocumentation,
		Metadata: map[string]any{},
	}

	require.NoError(t, ns.InsertNode(codeNode, emb))
	require.NoError(t, ns.InsertNode(histNode, emb))
	require.NoError(t, ns.InsertNode(acadNode, emb))

	codeInfo, _ := tracker.GetFreshness("code-node")
	histInfo, _ := tracker.GetFreshness("hist-node")
	acadInfo, _ := tracker.GetFreshness("acad-node")

	assert.Equal(t, config.CodeDecayRate, codeInfo.DecayRate)
	assert.Equal(t, config.HistoryDecayRate, histInfo.DecayRate)
	assert.Equal(t, config.AcademicDecayRate, acadInfo.DecayRate)
}

func TestComputeAccessBoost(t *testing.T) {
	tests := []struct {
		name          string
		recencyHours  float64
		expectedBoost float64
	}{
		{"very_recent", 0.5, 1.1},
		{"recent_day", 12, 1.05},
		{"recent_week", 100, 1.0},
		{"old", 200, 0.95},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			boost := computeAccessBoost(tt.recencyHours)
			assert.Equal(t, tt.expectedBoost, boost)
		})
	}
}

func TestComputeScanInterval(t *testing.T) {
	tests := []struct {
		name             string
		freshnessScore   float64
		expectedInterval time.Duration
	}{
		{"high_freshness", 0.9, 24 * time.Hour},
		{"medium_freshness", 0.6, 6 * time.Hour},
		{"low_freshness", 0.3, 1 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interval := computeScanInterval(tt.freshnessScore)
			assert.Equal(t, tt.expectedInterval, interval)
		})
	}
}

func TestFreshnessTracker_Close(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := NewFreshnessTracker(db, DefaultDecayConfig())

	// Add some stale nodes
	ns := vectorgraphdb.NewNodeStore(db, nil)
	node := &vectorgraphdb.GraphNode{
		ID:       "close-test-node",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{},
	}
	require.NoError(t, ns.InsertNode(node, makeTestEmbedding()))
	require.NoError(t, tracker.MarkStale("close-test-node"))

	assert.Equal(t, 1, tracker.GetStaleCount())

	// Close should not block and should clear state
	tracker.Close()

	assert.Equal(t, 0, tracker.GetStaleCount())
}

func TestFreshnessTracker_StopScanner_MultipleCalls(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := NewFreshnessTracker(db, DefaultDecayConfig())

	// Multiple calls to StopScanner should be safe
	tracker.StopScanner()
	tracker.StopScanner()
	tracker.StopScanner()

	// Should not panic
	tracker.Close()
}
