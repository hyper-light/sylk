package mitigations

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultContentAnalyzer_CompareContent(t *testing.T) {
	analyzer := &DefaultContentAnalyzer{}

	tests := []struct {
		name   string
		a      string
		b      string
		minSim float64
		maxSim float64
	}{
		{"identical", "hello world", "hello world", 1.0, 1.0},
		{"similar", "hello world test", "hello world example", 0.3, 0.7},
		{"different", "apple banana cherry", "dog elephant fox", 0.0, 0.1},
		{"empty", "", "", 0.0, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sim := analyzer.CompareContent(tt.a, tt.b)
			assert.GreaterOrEqual(t, sim, tt.minSim)
			assert.LessOrEqual(t, sim, tt.maxSim)
		})
	}
}

func TestMakeWordSet(t *testing.T) {
	tests := []struct {
		input    string
		expected map[string]bool
	}{
		{"hello world", map[string]bool{"hello": true, "world": true}},
		{"test123 abc", map[string]bool{"test123": true, "abc": true}},
		{"", map[string]bool{}},
		{"a-b_c", map[string]bool{"a": true, "b": true, "c": true}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := makeWordSet(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsAlphanumeric(t *testing.T) {
	tests := []struct {
		r        rune
		expected bool
	}{
		{'a', true},
		{'z', true},
		{'A', true},
		{'Z', true},
		{'0', true},
		{'9', true},
		{' ', false},
		{'-', false},
		{'_', false},
	}

	for _, tt := range tests {
		t.Run(string(tt.r), func(t *testing.T) {
			assert.Equal(t, tt.expected, isAlphanumeric(tt.r))
		})
	}
}

func TestConflictDetector_DetectOnInsert(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	detector := NewConflictDetector(db, nil, DefaultConflictDetectorConfig())

	node := &vectorgraphdb.GraphNode{
		ID:       "new-node",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{"content": "hello world"},
	}
	emb := makeTestEmbedding()

	conflicts, err := detector.DetectOnInsert(context.Background(), node, emb)

	require.NoError(t, err)
	assert.Empty(t, conflicts)
}

func TestConflictDetector_Resolve(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	detector := NewConflictDetector(db, nil, DefaultConflictDetectorConfig())

	conflict := detector.createConflict("node-a", "node-b", ConflictSemantic, 0.95, "test conflict")
	detector.storeConflict(conflict)

	err := detector.Resolve(conflict.ID, ResolutionKeepNewer)

	require.NoError(t, err)

	stored := detector.conflicts[conflict.ID]
	assert.NotNil(t, stored.Resolution)
	assert.Equal(t, ResolutionKeepNewer, *stored.Resolution)
	assert.NotNil(t, stored.ResolvedAt)
}

func TestConflictDetector_Resolve_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	detector := NewConflictDetector(db, nil, DefaultConflictDetectorConfig())

	err := detector.Resolve("nonexistent", ResolutionKeepNewer)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestConflictDetector_GetActiveConflicts(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	detector := NewConflictDetector(db, nil, DefaultConflictDetectorConfig())

	conflict1 := detector.createConflict("a", "b", ConflictSemantic, 0.9, "conflict 1")
	conflict2 := detector.createConflict("c", "d", ConflictTemporal, 0.8, "conflict 2")
	detector.storeConflict(conflict1)
	detector.storeConflict(conflict2)

	active := detector.GetActiveConflicts(10)

	assert.Len(t, active, 2)
}

func TestConflictDetector_GetConflictsForNode(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	detector := NewConflictDetector(db, nil, DefaultConflictDetectorConfig())

	conflict := detector.createConflict("target-node", "other-node", ConflictSemantic, 0.9, "test")
	detector.storeConflict(conflict)

	nodeConflicts := detector.GetConflictsForNode("target-node")

	assert.Len(t, nodeConflicts, 1)
	assert.Equal(t, "target-node", nodeConflicts[0].NodeAID)
}

func TestConflictDetector_GetConflictStats(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	detector := NewConflictDetector(db, nil, DefaultConflictDetectorConfig())

	conflict1 := detector.createConflict("a", "b", ConflictSemantic, 0.9, "c1")
	conflict2 := detector.createConflict("c", "d", ConflictTemporal, 0.8, "c2")
	detector.storeConflict(conflict1)
	detector.storeConflict(conflict2)

	_ = detector.Resolve(conflict1.ID, ResolutionKeepNewer)

	stats := detector.GetConflictStats()

	assert.Equal(t, int64(2), stats.TotalConflicts)
	assert.Equal(t, int64(1), stats.UnresolvedCount)
	assert.Equal(t, int64(1), stats.ByType[ConflictSemantic])
	assert.Equal(t, int64(1), stats.ByType[ConflictTemporal])
	assert.Equal(t, int64(1), stats.ByResolution[ResolutionKeepNewer])
}

func TestConflictDetector_AutoResolve(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	detector := NewConflictDetector(db, nil, DefaultConflictDetectorConfig())

	conflict := detector.createConflict("node-a", "node-b", ConflictTemporal, 0.9, "test")
	detector.storeConflict(conflict)

	resolved, err := detector.AutoResolve(conflict.ID)

	require.NoError(t, err)
	assert.False(t, resolved)
}

func TestConflictDetector_AutoResolve_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	detector := NewConflictDetector(db, nil, DefaultConflictDetectorConfig())

	_, err := detector.AutoResolve("nonexistent")

	assert.Error(t, err)
}

func TestConflictDetector_AnnotateResults(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ns := vectorgraphdb.NewNodeStore(db, nil)
	emb := makeTestEmbedding()

	node := &vectorgraphdb.GraphNode{
		ID:       "annotate-node",
		Domain:   vectorgraphdb.DomainCode,
		NodeType: vectorgraphdb.NodeTypeFile,
		Metadata: map[string]any{},
	}
	require.NoError(t, ns.InsertNode(node, emb))

	detector := NewConflictDetector(db, nil, DefaultConflictDetectorConfig())

	conflict := detector.createConflict("annotate-node", "other", ConflictSemantic, 0.9, "test")
	detector.storeConflict(conflict)

	results := []vectorgraphdb.SearchResult{
		{Node: node, Similarity: 0.9},
	}

	annotated := detector.AnnotateResults(results)

	assert.Len(t, annotated, 1)
	assert.True(t, annotated[0].HasConflict)
	assert.Len(t, annotated[0].Conflicts, 1)
}

func TestAbsTimeDiff(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-1 * time.Hour)
	later := now.Add(1 * time.Hour)

	assert.Equal(t, 1*time.Hour, absTimeDiff(now, earlier))
	assert.Equal(t, 1*time.Hour, absTimeDiff(earlier, now))
	assert.Equal(t, 2*time.Hour, absTimeDiff(earlier, later))
}
