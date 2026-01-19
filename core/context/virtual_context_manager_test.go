package context

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

func setupVCMTestEnv(t *testing.T) (*VirtualContextManager, *UniversalContentStore, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "vcm_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	vdb, err := vectorgraphdb.Open(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create vectordb: %v", err)
	}

	storeCfg := ContentStoreConfig{
		BlevePath:      filepath.Join(tmpDir, "test.bleve"),
		VectorDB:       vdb,
		IndexQueueSize: 100,
		IndexWorkers:   2,
	}

	store, err := NewUniversalContentStore(storeCfg)
	if err != nil {
		vdb.Close()
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create content store: %v", err)
	}

	registry := NewAgentConfigRegistry()
	refGen := NewReferenceGenerator(store)

	vcm, err := NewVirtualContextManager(VirtualContextManagerConfig{
		ContentStore:   store,
		ConfigRegistry: registry,
		RefGenerator:   refGen,
	})
	if err != nil {
		store.Close()
		vdb.Close()
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create VCM: %v", err)
	}

	cleanup := func() {
		store.Close()
		vdb.Close()
		os.RemoveAll(tmpDir)
	}

	return vcm, store, cleanup
}

func TestNewVirtualContextManager_NilContentStore(t *testing.T) {
	_, err := NewVirtualContextManager(VirtualContextManagerConfig{
		ContentStore:   nil,
		ConfigRegistry: NewAgentConfigRegistry(),
	})

	if err != ErrNilContentStore {
		t.Errorf("expected ErrNilContentStore, got: %v", err)
	}
}

func TestNewVirtualContextManager_NilConfigRegistry(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "vcm_test_*")
	defer os.RemoveAll(tmpDir)

	vdb, _ := vectorgraphdb.Open(filepath.Join(tmpDir, "test.db"))
	defer vdb.Close()

	store, _ := NewUniversalContentStore(ContentStoreConfig{
		BlevePath: filepath.Join(tmpDir, "test.bleve"),
		VectorDB:  vdb,
	})
	defer store.Close()

	_, err := NewVirtualContextManager(VirtualContextManagerConfig{
		ContentStore:   store,
		ConfigRegistry: nil,
	})

	if err != ErrNilConfigRegistry {
		t.Errorf("expected ErrNilConfigRegistry, got: %v", err)
	}
}

func TestVirtualContextManager_RegisterAgent(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	err := vcm.RegisterAgent("agent-1", "librarian", "session-1", 100000)
	if err != nil {
		t.Fatalf("RegisterAgent failed: %v", err)
	}

	state, err := vcm.GetContextState("agent-1")
	if err != nil {
		t.Fatalf("GetContextState failed: %v", err)
	}

	if state.AgentID != "agent-1" {
		t.Errorf("expected AgentID 'agent-1', got '%s'", state.AgentID)
	}
	if state.AgentType != "librarian" {
		t.Errorf("expected AgentType 'librarian', got '%s'", state.AgentType)
	}
	if state.Category != AgentCategoryKnowledge {
		t.Errorf("expected Category Knowledge, got '%s'", state.Category)
	}
	if state.MaxTokens != 100000 {
		t.Errorf("expected MaxTokens 100000, got %d", state.MaxTokens)
	}
}

func TestVirtualContextManager_UnregisterAgent(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	vcm.RegisterAgent("agent-1", "engineer", "session-1", 50000)
	vcm.UnregisterAgent("agent-1")

	_, err := vcm.GetContextState("agent-1")
	if err == nil {
		t.Error("expected error after unregistering agent")
	}
}

func TestVirtualContextManager_TrackContent(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	vcm.RegisterAgent("agent-1", "architect", "session-1", 100000)

	entry := &ContentEntry{
		ID:         "entry-1",
		TokenCount: 500,
		TurnNumber: 1,
	}

	err := vcm.TrackContent("agent-1", entry)
	if err != nil {
		t.Fatalf("TrackContent failed: %v", err)
	}

	count, err := vcm.GetTokenCount("agent-1")
	if err != nil {
		t.Fatalf("GetTokenCount failed: %v", err)
	}

	if count != 500 {
		t.Errorf("expected token count 500, got %d", count)
	}
}

func TestVirtualContextManager_TrackContent_UnknownAgent(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	entry := &ContentEntry{ID: "entry-1", TokenCount: 100}
	err := vcm.TrackContent("unknown-agent", entry)

	if err == nil {
		t.Error("expected error for unknown agent")
	}
}

func TestVirtualContextManager_GetContextUsage(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	vcm.RegisterAgent("agent-1", "librarian", "session-1", 1000)
	vcm.TrackContent("agent-1", &ContentEntry{ID: "e1", TokenCount: 250})
	vcm.TrackContent("agent-1", &ContentEntry{ID: "e2", TokenCount: 250})

	usage, err := vcm.GetContextUsage("agent-1")
	if err != nil {
		t.Fatalf("GetContextUsage failed: %v", err)
	}

	if usage != 0.5 {
		t.Errorf("expected usage 0.5, got %f", usage)
	}
}

func TestVirtualContextManager_GetContextUsage_ZeroMax(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	vcm.RegisterAgent("agent-1", "librarian", "session-1", 0)

	usage, err := vcm.GetContextUsage("agent-1")
	if err != nil {
		t.Fatalf("GetContextUsage failed: %v", err)
	}

	if usage != 0 {
		t.Errorf("expected usage 0 for zero max, got %f", usage)
	}
}

func TestVirtualContextManager_AgentCategories(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	tests := []struct {
		agentType string
		expected  AgentCategory
	}{
		{"librarian", AgentCategoryKnowledge},
		{"archivalist", AgentCategoryKnowledge},
		{"academic", AgentCategoryKnowledge},
		{"architect", AgentCategoryKnowledge},
		{"engineer", AgentCategoryPipeline},
		{"designer", AgentCategoryPipeline},
		{"inspector", AgentCategoryPipeline},
		{"tester", AgentCategoryPipeline},
		{"guide", AgentCategoryStandalone},
		{"orchestrator", AgentCategoryStandalone},
		{"unknown", AgentCategoryStandalone}, // Default
	}

	for _, tc := range tests {
		vcm.RegisterAgent("agent-"+tc.agentType, tc.agentType, "session-1", 100000)
		state, _ := vcm.GetContextState("agent-" + tc.agentType)

		if state.Category != tc.expected {
			t.Errorf("agent type %s: expected category %s, got %s",
				tc.agentType, tc.expected, state.Category)
		}
	}
}

func TestVirtualContextManager_ShouldEvict(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	// Engineer has 75% eviction trigger by default (128000 * 0.75 = 96000)
	vcm.RegisterAgent("agent-1", "engineer", "session-1", 128000)

	// Add tokens at 78% (above 75% trigger)
	vcm.TrackContent("agent-1", &ContentEntry{ID: "e1", TokenCount: 100000})

	should, err := vcm.ShouldEvict("agent-1")
	if err != nil {
		t.Fatalf("ShouldEvict failed: %v", err)
	}

	if !should {
		t.Error("expected ShouldEvict to return true at 78% usage with 75% trigger")
	}
}

func TestVirtualContextManager_ShouldEvict_BelowThreshold(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	vcm.RegisterAgent("agent-1", "engineer", "session-1", 128000)
	vcm.TrackContent("agent-1", &ContentEntry{ID: "e1", TokenCount: 50000})

	should, _ := vcm.ShouldEvict("agent-1")

	if should {
		t.Error("expected ShouldEvict to return false below threshold")
	}
}

func TestVirtualContextManager_GetPressureAction(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	vcm.RegisterAgent("knowledge-agent", "librarian", "session-1", 100000)
	vcm.RegisterAgent("pipeline-agent", "engineer", "session-1", 100000)

	knowledgeAction, _ := vcm.GetPressureAction("knowledge-agent")
	if knowledgeAction != AgentCategoryKnowledge {
		t.Errorf("expected Knowledge action, got %s", knowledgeAction)
	}

	pipelineAction, _ := vcm.GetPressureAction("pipeline-agent")
	if pipelineAction != AgentCategoryPipeline {
		t.Errorf("expected Pipeline action, got %s", pipelineAction)
	}
}

func TestVirtualContextManager_ForceEvict_KnowledgeAgent(t *testing.T) {
	vcm, store, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	vcm.RegisterAgent("agent-1", "librarian", "session-1", 100000)

	// Store content entries
	entries := []*ContentEntry{
		{ID: "e1", SessionID: "session-1", AgentID: "agent-1", AgentType: "librarian",
			Content: "test content 1", TokenCount: 1000, TurnNumber: 1, Timestamp: time.Now().Add(-10 * time.Minute)},
		{ID: "e2", SessionID: "session-1", AgentID: "agent-1", AgentType: "librarian",
			Content: "test content 2", TokenCount: 1000, TurnNumber: 2, Timestamp: time.Now().Add(-5 * time.Minute)},
		{ID: "e3", SessionID: "session-1", AgentID: "agent-1", AgentType: "librarian",
			Content: "test content 3", TokenCount: 1000, TurnNumber: 3, Timestamp: time.Now()},
	}

	for _, entry := range entries {
		store.IndexContent(entry)
		vcm.TrackContent("agent-1", entry)
	}

	ctx := context.Background()
	resp, err := vcm.ForceEvict(ctx, "agent-1", 0.5)
	if err != nil {
		t.Fatalf("ForceEvict failed: %v", err)
	}

	if resp.AgentID != "agent-1" {
		t.Errorf("expected AgentID 'agent-1', got '%s'", resp.AgentID)
	}
	if resp.TokensFreed <= 0 {
		t.Error("expected some tokens to be freed")
	}
}

func TestVirtualContextManager_ForceEvict_NonKnowledgeAgent(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	vcm.RegisterAgent("agent-1", "engineer", "session-1", 100000)

	ctx := context.Background()
	_, err := vcm.ForceEvict(ctx, "agent-1", 0.5)

	if err == nil {
		t.Error("expected error when ForceEvict called on pipeline agent")
	}
}

func TestVirtualContextManager_ForceEvict_InvalidPercent(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	vcm.RegisterAgent("agent-1", "librarian", "session-1", 100000)

	tests := []float64{0, -0.5, 1.5}
	ctx := context.Background()

	for _, pct := range tests {
		_, err := vcm.ForceEvict(ctx, "agent-1", pct)
		if err != ErrInvalidPercent {
			t.Errorf("percent %f: expected ErrInvalidPercent, got %v", pct, err)
		}
	}
}

func TestVirtualContextManager_ForceEvict_UnknownAgent(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	ctx := context.Background()
	_, err := vcm.ForceEvict(ctx, "unknown-agent", 0.5)

	if err == nil {
		t.Error("expected error for unknown agent")
	}
}

func TestVirtualContextManager_GetHandoffTrigger_PipelineAgent(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	vcm.RegisterAgent("agent-1", "engineer", "session-1", 1000)
	vcm.TrackContent("agent-1", &ContentEntry{ID: "e1", TokenCount: 800})

	trigger, err := vcm.GetHandoffTrigger("agent-1")
	if err != nil {
		t.Fatalf("GetHandoffTrigger failed: %v", err)
	}

	if trigger.AgentID != "agent-1" {
		t.Errorf("expected AgentID 'agent-1', got '%s'", trigger.AgentID)
	}
	if trigger.Category != AgentCategoryPipeline {
		t.Errorf("expected Pipeline category, got %s", trigger.Category)
	}
	if trigger.ContextUsage != 0.8 {
		t.Errorf("expected ContextUsage 0.8, got %f", trigger.ContextUsage)
	}
}

func TestVirtualContextManager_GetHandoffTrigger_KnowledgeAgent(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	vcm.RegisterAgent("agent-1", "librarian", "session-1", 1000)

	_, err := vcm.GetHandoffTrigger("agent-1")
	if err == nil {
		t.Error("expected error when GetHandoffTrigger called on knowledge agent")
	}
}

func TestVirtualContextManager_ListAgents(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	vcm.RegisterAgent("agent-1", "librarian", "session-1", 100000)
	vcm.RegisterAgent("agent-2", "engineer", "session-1", 100000)
	vcm.RegisterAgent("agent-3", "guide", "session-1", 100000)

	agents := vcm.ListAgents()
	if len(agents) != 3 {
		t.Errorf("expected 3 agents, got %d", len(agents))
	}
}

func TestVirtualContextManager_GetMetrics(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	metrics := vcm.GetMetrics()
	if metrics == nil {
		t.Error("expected non-nil metrics")
	}
}

func TestVirtualContextManager_ConcurrentAccess(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	// Register multiple agents
	for i := 0; i < 10; i++ {
		vcm.RegisterAgent(
			"agent-"+string(rune('a'+i)),
			"librarian",
			"session-1",
			100000,
		)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 100)

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agentID := "agent-" + string(rune('a'+idx%10))

			_, err := vcm.GetContextState(agentID)
			if err != nil {
				errCh <- err
			}

			_, _ = vcm.GetTokenCount(agentID)
			_, _ = vcm.GetContextUsage(agentID)
			_, _ = vcm.ShouldEvict(agentID)
		}(i)
	}

	// Concurrent writes
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agentID := "agent-" + string(rune('a'+idx%10))

			entry := &ContentEntry{
				ID:         "entry-" + string(rune('0'+idx)),
				TokenCount: 100,
				TurnNumber: idx,
			}
			_ = vcm.TrackContent(agentID, entry)
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent access error: %v", err)
	}
}

func TestGetAgentCategory_AllKnownTypes(t *testing.T) {
	knowledgeTypes := []string{"librarian", "archivalist", "academic", "architect"}
	for _, at := range knowledgeTypes {
		if cat := getAgentCategory(at); cat != AgentCategoryKnowledge {
			t.Errorf("agent type %s: expected Knowledge, got %s", at, cat)
		}
	}

	pipelineTypes := []string{"engineer", "designer", "inspector", "tester"}
	for _, at := range pipelineTypes {
		if cat := getAgentCategory(at); cat != AgentCategoryPipeline {
			t.Errorf("agent type %s: expected Pipeline, got %s", at, cat)
		}
	}

	standaloneTypes := []string{"guide", "orchestrator"}
	for _, at := range standaloneTypes {
		if cat := getAgentCategory(at); cat != AgentCategoryStandalone {
			t.Errorf("agent type %s: expected Standalone, got %s", at, cat)
		}
	}
}

func TestCalculateContextUsage_EdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		tokens    int
		maxTokens int
		expected  float64
	}{
		{"zero max", 100, 0, 0},
		{"zero tokens", 0, 100, 0},
		{"full", 100, 100, 1.0},
		{"half", 50, 100, 0.5},
	}

	for _, tc := range tests {
		state := &AgentContextState{
			TokenCount: tc.tokens,
			MaxTokens:  tc.maxTokens,
		}
		result := calculateContextUsage(state)
		if result != tc.expected {
			t.Errorf("%s: expected %f, got %f", tc.name, tc.expected, result)
		}
	}
}

func TestVirtualContextManager_TrackMultipleEntries(t *testing.T) {
	vcm, _, cleanup := setupVCMTestEnv(t)
	defer cleanup()

	vcm.RegisterAgent("agent-1", "librarian", "session-1", 100000)

	entries := []*ContentEntry{
		{ID: "e1", TokenCount: 100, TurnNumber: 1},
		{ID: "e2", TokenCount: 200, TurnNumber: 2},
		{ID: "e3", TokenCount: 300, TurnNumber: 3},
	}

	for _, e := range entries {
		vcm.TrackContent("agent-1", e)
	}

	state, _ := vcm.GetContextState("agent-1")

	if state.TokenCount != 600 {
		t.Errorf("expected total tokens 600, got %d", state.TokenCount)
	}
	if state.TurnNumber != 3 {
		t.Errorf("expected turn number 3, got %d", state.TurnNumber)
	}
	if len(state.EntryIDs) != 3 {
		t.Errorf("expected 3 entry IDs, got %d", len(state.EntryIDs))
	}
}

func TestCloneAgentContextState(t *testing.T) {
	original := &AgentContextState{
		AgentID:    "agent-1",
		AgentType:  "librarian",
		SessionID:  "session-1",
		Category:   AgentCategoryKnowledge,
		TokenCount: 1000,
		MaxTokens:  100000,
		TurnNumber: 5,
		EntryIDs:   []string{"e1", "e2", "e3"},
	}

	clone := cloneAgentContextState(original)

	// Modify original
	original.TokenCount = 2000
	original.EntryIDs[0] = "modified"

	// Clone should be unaffected
	if clone.TokenCount != 1000 {
		t.Errorf("clone TokenCount should be 1000, got %d", clone.TokenCount)
	}
	if clone.EntryIDs[0] != "e1" {
		t.Errorf("clone EntryIDs[0] should be 'e1', got '%s'", clone.EntryIDs[0])
	}
}
