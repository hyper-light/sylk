package archivalist

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/knowledge/memory"
	"github.com/adalundhe/sylk/core/knowledge/query"
	"github.com/adalundhe/sylk/core/skills"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var knowledgeTestDBCounter atomic.Int64

// =============================================================================
// Nil Guard Tests
// =============================================================================

func TestKnowledgeMemory_NilGuards(t *testing.T) {
	a := &Archivalist{skills: skills.NewRegistry()}
	a.skills.Register(knowledgeMemorySkill(a))

	actions := []string{"activation", "record_access", "record_outcome", "review", "spacing", "stats"}
	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			input, _ := json.Marshal(map[string]any{"action": action, "node_id": "n1"})
			result := a.skills.Invoke(context.Background(), "knowledge_memory", input)
			require.NotNil(t, result.Err)
			assert.ErrorIs(t, result.Err, errMemorySubsystemUnavailable)
		})
	}
}

func TestKnowledgeQuery_NilGuards(t *testing.T) {
	a := &Archivalist{skills: skills.NewRegistry()}
	a.skills.Register(knowledgeQuerySkill(a))

	for _, tc := range []struct {
		action string
		err    error
	}{
		{"search", errQuerySubsystemUnavailable},
		{"readiness", errQuerySubsystemUnavailable},
		{"search_feedback", errCrossAgentWeightsUnavailable},
	} {
		t.Run(tc.action, func(t *testing.T) {
			input, _ := json.Marshal(map[string]any{"action": tc.action, "agent_type": "librarian"})
			result := a.skills.Invoke(context.Background(), "knowledge_query", input)
			require.NotNil(t, result.Err)
			assert.ErrorIs(t, result.Err, tc.err)
		})
	}
}

func TestKnowledgeInsights_NilGuards(t *testing.T) {
	a := &Archivalist{skills: skills.NewRegistry()}
	a.skills.Register(knowledgeInsightsSkill(a))

	for _, tc := range []struct {
		action string
		err    error
	}{
		{"agent_trust", errCrossAgentWeightsUnavailable},
		{"query_weights", errQuerySubsystemUnavailable},
		{"decay_params", errMemorySubsystemUnavailable},
		{"blend_phase", errMemorySubsystemUnavailable},
	} {
		t.Run(tc.action, func(t *testing.T) {
			input, _ := json.Marshal(map[string]any{"action": tc.action, "node_id": "n1"})
			result := a.skills.Invoke(context.Background(), "knowledge_insights", input)
			require.NotNil(t, result.Err)
			assert.ErrorIs(t, result.Err, tc.err)
		})
	}
}

// =============================================================================
// knowledge_memory functional tests
// =============================================================================

func TestKnowledgeMemory_Activation(t *testing.T) {
	a := newKnowledgeTestArchivalist(t)

	ctx := context.Background()
	seedNode(t, a, "node-1")

	// Record an access first
	err := a.memoryScorer.Store().RecordAccess(ctx, "node-1", memory.AccessRetrieval, "test")
	require.NoError(t, err)

	input, _ := json.Marshal(map[string]any{
		"action":  "activation",
		"node_id": "node-1",
	})
	result := a.skills.Invoke(ctx, "knowledge_memory", input)
	require.Nil(t, result.Err)

	data, ok := result.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "node-1", data["node_id"])
	activation, ok := data["activation"].(float64)
	require.True(t, ok)
	assert.Greater(t, activation, -math.MaxFloat64)
}

func TestKnowledgeMemory_RecordAccess(t *testing.T) {
	a := newKnowledgeTestArchivalist(t)

	ctx := context.Background()
	seedNode(t, a, "node-2")

	input, _ := json.Marshal(map[string]any{
		"action":      "record_access",
		"node_id":     "node-2",
		"access_type": "retrieval",
		"context":     "test access",
	})
	result := a.skills.Invoke(ctx, "knowledge_memory", input)
	require.Nil(t, result.Err)

	data, ok := result.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, data["recorded"])

	// Verify trace persisted
	mem, err := a.memoryScorer.Store().GetMemory(ctx, "node-2", domain.DomainArchivalist)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(mem.Traces), 1)
}

func TestKnowledgeMemory_RecordOutcome(t *testing.T) {
	a := newKnowledgeTestArchivalist(t)
	ctx := context.Background()
	seedNode(t, a, "node-a")
	seedNode(t, a, "node-b")

	input, _ := json.Marshal(map[string]any{
		"action":     "record_outcome",
		"node_ids":   []string{"node-a", "node-b"},
		"was_useful": true,
	})
	result := a.skills.Invoke(ctx, "knowledge_memory", input)
	require.Nil(t, result.Err)

	data, ok := result.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, data["recorded"])
}

func TestKnowledgeMemory_Review(t *testing.T) {
	a := newKnowledgeTestArchivalist(t)
	ctx := context.Background()

	// Seed nodes with access traces so they appear in review
	for i := range 3 {
		nodeID := fmt.Sprintf("review-node-%d", i)
		seedNodeWithActivation(t, a, nodeID, float64(i))
		err := a.memoryScorer.Store().RecordAccess(ctx, nodeID, memory.AccessRetrieval, "test")
		require.NoError(t, err)
	}

	input, _ := json.Marshal(map[string]any{
		"action": "review",
		"limit":  5,
	})
	result := a.skills.Invoke(ctx, "knowledge_memory", input)
	require.Nil(t, result.Err)

	items, ok := result.Data.([]map[string]any)
	require.True(t, ok)
	assert.LessOrEqual(t, len(items), 5)
}

func TestKnowledgeMemory_Spacing(t *testing.T) {
	a := newKnowledgeTestArchivalist(t)
	ctx := context.Background()

	seedNode(t, a, "spacing-node")
	err := a.memoryScorer.Store().RecordAccess(ctx, "spacing-node", memory.AccessRetrieval, "t1")
	require.NoError(t, err)

	input, _ := json.Marshal(map[string]any{
		"action":  "spacing",
		"node_id": "spacing-node",
	})
	result := a.skills.Invoke(ctx, "knowledge_memory", input)
	require.Nil(t, result.Err)

	report, ok := result.Data.(*memory.SpacingReport)
	require.True(t, ok)
	assert.NotNil(t, report)
}

func TestKnowledgeMemory_Stats(t *testing.T) {
	a := newKnowledgeTestArchivalist(t)
	ctx := context.Background()

	input, _ := json.Marshal(map[string]any{"action": "stats"})
	result := a.skills.Invoke(ctx, "knowledge_memory", input)
	require.Nil(t, result.Err)

	data, ok := result.Data.(map[string]any)
	require.True(t, ok)
	assert.Contains(t, data, "memory_stats")
	assert.Contains(t, data, "weights")
	assert.Contains(t, data, "confidences")
}

// =============================================================================
// knowledge_query functional tests
// =============================================================================

func TestKnowledgeQuery_Readiness_NoSearchers(t *testing.T) {
	a := &Archivalist{
		skills:           skills.NewRegistry(),
		queryCoordinator: query.NewHybridQueryCoordinator(nil, nil, nil),
	}
	a.skills.Register(knowledgeQuerySkill(a))

	input, _ := json.Marshal(map[string]any{"action": "readiness"})
	result := a.skills.Invoke(context.Background(), "knowledge_query", input)
	require.Nil(t, result.Err)

	data, ok := result.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, data["ready"])
}

func TestKnowledgeQuery_SearchFeedback(t *testing.T) {
	a := &Archivalist{
		skills:            skills.NewRegistry(),
		crossAgentWeights: NewCrossAgentWeightManager(),
	}
	a.skills.Register(knowledgeQuerySkill(a))

	input, _ := json.Marshal(map[string]any{
		"action":     "search_feedback",
		"agent_type": "librarian",
		"was_useful": true,
	})
	result := a.skills.Invoke(context.Background(), "knowledge_query", input)
	require.Nil(t, result.Err)

	data, ok := result.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, data["recorded"])
}

// =============================================================================
// knowledge_insights functional tests
// =============================================================================

func TestKnowledgeInsights_AgentTrust_Snapshot(t *testing.T) {
	a := &Archivalist{
		skills:            skills.NewRegistry(),
		crossAgentWeights: NewCrossAgentWeightManager(),
	}
	a.skills.Register(knowledgeInsightsSkill(a))

	// Initialize some weights so snapshot is non-empty
	a.crossAgentWeights.WeighResult("librarian")
	a.crossAgentWeights.WeighResult("designer")

	input, _ := json.Marshal(map[string]any{"action": "agent_trust"})
	result := a.skills.Invoke(context.Background(), "knowledge_insights", input)
	require.Nil(t, result.Err)

	snapshot, ok := result.Data.(map[string]float64)
	require.True(t, ok)
	assert.Contains(t, snapshot, "librarian")
	assert.Contains(t, snapshot, "designer")
	// Librarian has prior (4,2) → mean 0.667; designer has (2,3) → mean 0.4
	assert.Greater(t, snapshot["librarian"], snapshot["designer"])
}

func TestKnowledgeInsights_AgentTrust_SingleAgent(t *testing.T) {
	a := &Archivalist{
		skills:            skills.NewRegistry(),
		crossAgentWeights: NewCrossAgentWeightManager(),
	}
	a.skills.Register(knowledgeInsightsSkill(a))

	input, _ := json.Marshal(map[string]any{
		"action":     "agent_trust",
		"agent_type": "inspector",
	})
	result := a.skills.Invoke(context.Background(), "knowledge_insights", input)
	require.Nil(t, result.Err)

	data, ok := result.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "inspector", data["agent_type"])
	weight, ok := data["weight"].(float64)
	require.True(t, ok)
	assert.Greater(t, weight, 0.0)
}

func TestKnowledgeInsights_DecayParams(t *testing.T) {
	a := newKnowledgeTestArchivalist(t)
	ctx := context.Background()

	input, _ := json.Marshal(map[string]any{
		"action": "decay_params",
		"domain": "academic",
	})
	result := a.skills.Invoke(ctx, "knowledge_insights", input)
	require.Nil(t, result.Err)

	data, ok := result.Data.(map[string]any)
	require.True(t, ok)
	alpha, ok := data["alpha"].(float64)
	require.True(t, ok)
	assert.Greater(t, alpha, 0.0)
}

func TestKnowledgeInsights_BlendPhase(t *testing.T) {
	a := newKnowledgeTestArchivalist(t)
	ctx := context.Background()

	seedNode(t, a, "blend-node")

	input, _ := json.Marshal(map[string]any{
		"action":  "blend_phase",
		"node_id": "blend-node",
	})
	result := a.skills.Invoke(ctx, "knowledge_insights", input)
	require.Nil(t, result.Err)

	data, ok := result.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "cold", data["phase"])
	assert.Equal(t, 0, data["trace_count"])
}

func TestKnowledgeInsights_QueryWeights(t *testing.T) {
	coord := query.NewHybridQueryCoordinator(nil, nil, nil)
	a := &Archivalist{
		skills:           skills.NewRegistry(),
		queryCoordinator: coord,
	}
	a.skills.Register(knowledgeInsightsSkill(a))

	input, _ := json.Marshal(map[string]any{"action": "query_weights"})
	result := a.skills.Invoke(context.Background(), "knowledge_insights", input)
	require.Nil(t, result.Err)

	stats, ok := result.Data.(query.WeightStats)
	require.True(t, ok)
	// Default uniform priors → means ~0.5
	assert.InDelta(t, 0.5, stats.TextMean, 0.01)
}

// =============================================================================
// Shared helper tests
// =============================================================================

func TestClampLimit(t *testing.T) {
	assert.Equal(t, 10, clampLimit(0, 10, 100))
	assert.Equal(t, 10, clampLimit(-1, 10, 100))
	assert.Equal(t, 5, clampLimit(5, 10, 100))
	assert.Equal(t, 100, clampLimit(200, 10, 100))
}

func TestParseDomainOrDefault(t *testing.T) {
	assert.Equal(t, domain.DomainArchivalist, parseDomainOrDefault(""))
	assert.Equal(t, domain.DomainArchivalist, parseDomainOrDefault("invalid"))
	assert.Equal(t, domain.DomainAcademic, parseDomainOrDefault("academic"))
	assert.Equal(t, domain.DomainEngineer, parseDomainOrDefault("engineer"))
}

func TestParseAccessType(t *testing.T) {
	assert.Equal(t, memory.AccessRetrieval, parseAccessType("retrieval"))
	assert.Equal(t, memory.AccessReinforcement, parseAccessType("reinforcement"))
	assert.Equal(t, memory.AccessCreation, parseAccessType("creation"))
	assert.Equal(t, memory.AccessReference, parseAccessType("reference"))
	assert.Equal(t, memory.AccessRetrieval, parseAccessType("unknown"))
}

func TestBuildHybridQuery(t *testing.T) {
	hq := buildHybridQuery("hello world", "librarian", 20)
	assert.Equal(t, "hello world", hq.TextQuery)
	assert.Equal(t, 20, hq.Limit)
	require.Len(t, hq.Filters, 1)
	assert.Equal(t, query.FilterDomain, hq.Filters[0].Type)

	hq2 := buildHybridQuery("test", "", 0)
	assert.Equal(t, 10, hq2.Limit)
	assert.Empty(t, hq2.Filters)
}

// =============================================================================
// Test helpers
// =============================================================================

func newKnowledgeTestArchivalist(t *testing.T) *Archivalist {
	t.Helper()

	db := setupKnowledgeTestDB(t)
	store := memory.NewMemoryStore(db, 0, 0)
	scorer := memory.NewMemoryWeightedScorer(store)
	hybrid := memory.NewHybridQueryWithMemory(scorer)
	spacingAnalyzer := memory.NewSpacingAnalyzer(memory.DefaultTargetRetention)

	a := &Archivalist{
		skills:            skills.NewRegistry(),
		memoryScorer:      scorer,
		hybridMemory:      hybrid,
		spacingAnalyzer:   spacingAnalyzer,
		crossAgentWeights: NewCrossAgentWeightManager(),
	}
	a.skills.Register(knowledgeMemorySkill(a))
	a.skills.Register(knowledgeQuerySkill(a))
	a.skills.Register(knowledgeInsightsSkill(a))

	t.Cleanup(func() {
		scorer.Stop()
		db.Close()
	})
	return a
}

func setupKnowledgeTestDB(t *testing.T) *sql.DB {
	t.Helper()

	n := knowledgeTestDBCounter.Add(1)
	dbURI := fmt.Sprintf("file:knowledgetest%d?mode=memory&cache=shared", n)

	db, err := sql.Open("sqlite3", dbURI)
	require.NoError(t, err)

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	createKnowledgeSchema(t, db)
	return db
}

func createKnowledgeSchema(t *testing.T, db *sql.DB) {
	t.Helper()

	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			domain INTEGER NOT NULL,
			node_type INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL DEFAULT '',
			memory_activation REAL DEFAULT 0.0,
			last_accessed_at INTEGER,
			access_count INTEGER DEFAULT 0,
			base_offset REAL DEFAULT 0.0
		)
	`)
	require.NoError(t, err)

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS node_access_traces (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL,
			accessed_at INTEGER NOT NULL,
			access_type TEXT NOT NULL,
			context TEXT,
			FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
		)
	`)
	require.NoError(t, err)

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS decay_parameters (
			domain INTEGER PRIMARY KEY,
			decay_exponent_alpha REAL NOT NULL,
			decay_exponent_beta REAL NOT NULL,
			base_offset_mean REAL NOT NULL,
			base_offset_variance REAL NOT NULL,
			effective_samples REAL NOT NULL,
			updated_at TEXT NOT NULL
		)
	`)
	require.NoError(t, err)

	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_access_traces_node
		ON node_access_traces(node_id, accessed_at DESC)
	`)
	require.NoError(t, err)
}

func seedNode(t *testing.T, a *Archivalist, nodeID string) {
	t.Helper()
	db := a.memoryScorer.Store().DB()
	_, err := db.Exec(
		`INSERT OR IGNORE INTO nodes (id, domain, name) VALUES (?, ?, ?)`,
		nodeID, int(domain.DomainArchivalist), nodeID,
	)
	require.NoError(t, err)
}

func seedNodeWithActivation(t *testing.T, a *Archivalist, nodeID string, activation float64) {
	t.Helper()
	db := a.memoryScorer.Store().DB()
	_, err := db.Exec(
		`INSERT OR IGNORE INTO nodes (id, domain, name, memory_activation, last_accessed_at, access_count)
		 VALUES (?, ?, ?, ?, ?, 1)`,
		nodeID, int(domain.DomainArchivalist), nodeID, activation, time.Now().Unix(),
	)
	require.NoError(t, err)
}
