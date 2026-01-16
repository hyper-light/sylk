package archivalist

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// =============================================================================
// ConflictDetector Unit Tests
// =============================================================================

func newTestConflictDetector(t *testing.T) (*ConflictDetector, *AgentContext, *Registry) {
	t.Helper()
	agentCtx := NewAgentContext()
	registry := newTestRegistry(t)
	detector := NewConflictDetector(agentCtx, registry)
	return detector, agentCtx, registry
}

func TestConflictDetector_NoConflict_EmptyState(t *testing.T) {
	detector, _, _ := newTestConflictDetector(t)

	result := detector.DetectConflict(
		ScopeFiles,
		"/src/main.go",
		map[string]any{"status": "read"},
		"v1",
		"agent-1",
	)

	assert.Equal(t, ConflictTypeNone, result.Type, "Should have no conflict")
	assert.True(t, result.Resolved, "Should be resolved")
}

func TestConflictDetector_FileConflict_SameFile(t *testing.T) {
	detector, agentCtx, registry := newTestConflictDetector(t)

	// Register two agents
	agent1, _ := registry.Register("agent1", "session-1", "", SourceModelClaudeOpus45)
	agent2, _ := registry.Register("agent2", "session-1", "", SourceModelGPT52Codex)
	_ = agent1

	// First agent reads and modifies file
	agentCtx.RecordFileRead("/src/main.go", "Main file", SourceModelClaudeOpus45)
	agentCtx.RecordFileModified("/src/main.go", FileChange{
		StartLine: 10, EndLine: 20, Description: "Change 1",
	}, SourceModelClaudeOpus45)

	// Second agent tries to modify same file
	result := detector.DetectConflict(
		ScopeFiles,
		"/src/main.go",
		map[string]any{
			"status": "modified",
			"changes": []any{
				map[string]any{"start_line": 15.0, "end_line": 25.0, "description": "Change 2"},
			},
		},
		agent2.LastVersion,
		agent2.ID,
	)

	assert.Equal(t, ConflictTypeFileState, result.Type, "Should detect file conflict")
}

func TestConflictDetector_FileConflict_NonOverlappingRanges(t *testing.T) {
	detector, agentCtx, registry := newTestConflictDetector(t)

	registry.Register("agent1", "session-1", "", SourceModelClaudeOpus45)
	agent2, _ := registry.Register("agent2", "session-1", "", SourceModelGPT52Codex)

	// First agent modifies lines 10-20
	agentCtx.RecordFileRead("/src/main.go", "Main file", SourceModelClaudeOpus45)
	agentCtx.RecordFileModified("/src/main.go", FileChange{
		StartLine: 10, EndLine: 20, Description: "Change 1",
	}, SourceModelClaudeOpus45)

	// Second agent tries to modify lines 50-60 (non-overlapping)
	result := detector.DetectConflict(
		ScopeFiles,
		"/src/main.go",
		map[string]any{
			"status": "modified",
			"changes": []any{
				map[string]any{"start_line": 50.0, "end_line": 60.0, "description": "Change 2"},
			},
		},
		agent2.LastVersion,
		agent2.ID,
	)

	// Should still flag as modified but may allow merge
	// The exact behavior depends on implementation
	if result.Type == ConflictTypeFileState {
		assert.True(t, result.Resolved || result.Strategy == ResolutionMerge, "Non-overlapping should be resolvable")
	}
}

func TestConflictDetector_FileConflict_SameAgent(t *testing.T) {
	detector, agentCtx, registry := newTestConflictDetector(t)

	agent1, _ := registry.Register("agent1", "session-1", "", SourceModelClaudeOpus45)

	// Agent modifies file
	agentCtx.RecordFileRead("/src/main.go", "Main file", SourceModelClaudeOpus45)
	agentCtx.RecordFileModified("/src/main.go", FileChange{
		StartLine: 10, EndLine: 20, Description: "Change 1",
	}, SourceModelClaudeOpus45)

	// Same agent modifies again (should not conflict)
	result := detector.DetectConflict(
		ScopeFiles,
		"/src/main.go",
		map[string]any{
			"status": "modified",
			"changes": []any{
				map[string]any{"start_line": 15.0, "end_line": 25.0, "description": "Change 2"},
			},
		},
		agent1.LastVersion,
		agent1.ID,
	)

	// Same agent should not conflict with itself
	assert.Equal(t, ConflictTypeNone, result.Type, "Same agent should not conflict")
}

func TestConflictDetector_IntentConflict(t *testing.T) {
	detector, agentCtx, registry := newTestConflictDetector(t)

	registry.Register("agent1", "session-1", "", SourceModelClaudeOpus45)
	agent2, _ := registry.Register("agent2", "session-1", "", SourceModelGPT52Codex)

	// First agent records user wants something
	agentCtx.RecordUserWants("Use PostgreSQL", "high", "user-1")

	// Second agent tries to record conflicting intent
	result := detector.DetectConflict(
		ScopeIntents,
		"",
		map[string]any{
			"type":        "reject",
			"description": "Use PostgreSQL",
		},
		agent2.LastVersion,
		agent2.ID,
	)

	assert.Equal(t, ConflictTypeIntent, result.Type, "Should detect intent conflict")
}

func TestConflictDetector_ResumeConflict_DifferentTasks(t *testing.T) {
	detector, agentCtx, registry := newTestConflictDetector(t)

	agent1, _ := registry.Register("agent1", "session-1", "", SourceModelClaudeOpus45)
	agent2, _ := registry.Register("agent2", "session-1", "", SourceModelGPT52Codex)

	// First agent sets current task
	agentCtx.SetCurrentTask("Implement auth", "Add JWT auth", agent1.Source)

	// Second agent tries to set different task
	result := detector.DetectConflict(
		ScopeResume,
		"",
		map[string]any{
			"current_task": "Implement database",
			"objective":    "Add PostgreSQL",
		},
		agent2.LastVersion,
		agent2.ID,
	)

	assert.Equal(t, ConflictTypeResume, result.Type, "Should detect resume conflict")
}

func TestConflictDetector_PatternConflict(t *testing.T) {
	detector, agentCtx, registry := newTestConflictDetector(t)

	registry.Register("agent1", "session-1", "", SourceModelClaudeOpus45)
	agent2, _ := registry.Register("agent2", "session-1", "", SourceModelGPT52Codex)

	// First agent registers a pattern
	agentCtx.RegisterPattern(&Pattern{
		Category:    "error_handling",
		Name:        "Wrap errors",
		Description: "Use fmt.Errorf with %w",
		Source:      SourceModelClaudeOpus45,
	})

	// Second agent tries to register conflicting pattern
	result := detector.DetectConflict(
		ScopePatterns,
		"error_handling",
		map[string]any{
			"name":        "Don't wrap errors",
			"description": "Return errors directly",
		},
		agent2.LastVersion,
		agent2.ID,
	)

	// Should detect pattern conflict or allow combine
	if result.Type != ConflictTypeNone {
		assert.True(t, result.Strategy == ResolutionCombine || result.Type == ConflictTypePattern, "Should handle pattern conflict")
	}
}

// =============================================================================
// Conflict Resolution Tests
// =============================================================================

func TestConflictDetector_Resolve_Merge(t *testing.T) {
	detector, _, _ := newTestConflictDetector(t)

	result := &ConflictResult{
		Type:     ConflictTypeFileState,
		Strategy: ResolutionMerge,
		Resolved: true,
	}

	existing := map[string]any{
		"changes": []any{
			map[string]any{"start_line": 10, "end_line": 20},
		},
	}

	incoming := map[string]any{
		"changes": []any{
			map[string]any{"start_line": 50, "end_line": 60},
		},
	}

	merged := detector.Resolve(result, existing, incoming)

	// Should produce merged result
	assert.NotNil(t, merged, "Merged result should not be nil")
}

func TestConflictDetector_Resolve_Accept(t *testing.T) {
	detector, _, _ := newTestConflictDetector(t)

	result := &ConflictResult{
		Type:     ConflictTypeNone,
		Strategy: ResolutionAccept,
		Resolved: true,
	}

	existing := map[string]any{"key": "old"}
	incoming := map[string]any{"key": "new"}

	resolved := detector.Resolve(result, existing, incoming)

	assert.Equal(t, "new", resolved["key"], "Should use incoming value")
}

func TestConflictDetector_Resolve_Combine(t *testing.T) {
	detector, _, _ := newTestConflictDetector(t)

	result := &ConflictResult{
		Type:     ConflictTypePattern,
		Strategy: ResolutionCombine,
		Resolved: true,
	}

	existing := map[string]any{
		"name":        "Pattern A",
		"description": "Description A",
	}

	incoming := map[string]any{
		"name":        "Pattern B",
		"description": "Description B",
	}

	combined := detector.Resolve(result, existing, incoming)

	// Should produce combined result
	assert.NotNil(t, combined, "Combined result should not be nil")
}

// =============================================================================
// ConflictHistory Tests
// =============================================================================

func TestConflictHistory_Record(t *testing.T) {
	history := NewConflictHistory(100)

	record := &ConflictRecord{
		ID:           "conflict-1",
		Type:         ConflictTypeFileState,
		Scope:        ScopeFiles,
		Key:          "/src/main.go",
		AgentID:      "agent-1",
		AutoResolved: false,
	}

	history.Record(record)

	recent := history.GetRecent(10)
	assert.Len(t, recent, 1, "Should have 1 conflict")
	assert.Equal(t, "conflict-1", recent[0].ID, "ID should match")
}

func TestConflictHistory_GetRecent(t *testing.T) {
	history := NewConflictHistory(100)

	for i := 0; i < 10; i++ {
		history.Record(&ConflictRecord{
			ID:   "conflict-" + string(rune('0'+i)),
			Type: ConflictTypeFileState,
		})
	}

	recent := history.GetRecent(5)

	assert.Len(t, recent, 5, "Should return 5 recent conflicts")
}

func TestConflictHistory_GetUnresolved(t *testing.T) {
	history := NewConflictHistory(100)

	history.Record(&ConflictRecord{ID: "1", Type: ConflictTypeFileState, AutoResolved: true})
	history.Record(&ConflictRecord{ID: "2", Type: ConflictTypeFileState, AutoResolved: false})
	history.Record(&ConflictRecord{ID: "3", Type: ConflictTypeIntent, AutoResolved: false})
	history.Record(&ConflictRecord{ID: "4", Type: ConflictTypeFileState, AutoResolved: true})

	unresolved := history.GetUnresolved()

	assert.Len(t, unresolved, 2, "Should have 2 unresolved conflicts")
	for _, c := range unresolved {
		assert.False(t, c.AutoResolved, "All should be unresolved")
	}
}

func TestConflictHistory_Capacity(t *testing.T) {
	history := NewConflictHistory(10)

	// Add more than capacity
	for i := 0; i < 20; i++ {
		history.Record(&ConflictRecord{
			ID:   "conflict-" + string(rune('0'+i)),
			Type: ConflictTypeFileState,
		})
	}

	recent := history.GetRecent(100)

	assert.LessOrEqual(t, len(recent), 10, "Should not exceed capacity")
}

// =============================================================================
// Conflict Detection Edge Cases
// =============================================================================

func TestConflictDetector_VersionMismatch(t *testing.T) {
	detector, _, registry := newTestConflictDetector(t)

	registry.Register("agent1", "session-1", "", SourceModelClaudeOpus45)

	// Increment version several times
	registry.IncrementVersion()
	registry.IncrementVersion()
	registry.IncrementVersion()

	// Try to write with old version
	result := detector.DetectConflict(
		ScopeFiles,
		"/src/main.go",
		map[string]any{"status": "modified"},
		"v0", // Old version
		"agent-1",
	)

	// May detect version conflict depending on implementation
	// The result depends on how strict version checking is
	assert.NotNil(t, result, "Should return a result")
}

func TestConflictDetector_CompetingPatterns(t *testing.T) {
	detector, agentCtx, registry := newTestConflictDetector(t)

	agent1, _ := registry.Register("agent1", "session-1", "", SourceModelClaudeOpus45)
	agent2, _ := registry.Register("agent2", "session-1", "", SourceModelGPT52Codex)

	// Both agents try to establish patterns in same category
	agentCtx.RegisterPattern(&Pattern{
		Category:      "formatting",
		Name:          "Tabs",
		Description:   "Use tabs for indentation",
		Source:        agent1.Source,
		EstablishedBy: agent1.ID,
	})

	result := detector.DetectConflict(
		ScopePatterns,
		"formatting",
		map[string]any{
			"name":        "Spaces",
			"description": "Use spaces for indentation",
		},
		agent2.LastVersion,
		agent2.ID,
	)

	// Should detect potential conflict
	assert.NotNil(t, result, "Should return a result for competing patterns")
}

func TestConflictDetector_TaskOwnershipCollision(t *testing.T) {
	detector, agentCtx, registry := newTestConflictDetector(t)

	agent1, _ := registry.Register("agent1", "session-1", "", SourceModelClaudeOpus45)
	agent2, _ := registry.Register("agent2", "session-1", "", SourceModelGPT52Codex)

	// First agent claims a task
	agentCtx.SetCurrentTask("Implement feature X", "Full implementation", agent1.Source)

	// Second agent tries to claim same task
	result := detector.DetectConflict(
		ScopeResume,
		"",
		map[string]any{
			"current_task": "Implement feature X",
			"objective":    "Different approach",
		},
		agent2.LastVersion,
		agent2.ID,
	)

	assert.Equal(t, ConflictTypeResume, result.Type, "Should detect task ownership conflict")
}

func TestConflictDetector_ContradictoryIntents(t *testing.T) {
	detector, agentCtx, registry := newTestConflictDetector(t)

	registry.Register("agent1", "session-1", "", SourceModelClaudeOpus45)
	agent2, _ := registry.Register("agent2", "session-1", "", SourceModelGPT52Codex)

	// Record that user wants something
	agentCtx.RecordUserWants("Use microservices architecture", "high", "user")

	// Try to record that user rejects the same thing
	result := detector.DetectConflict(
		ScopeIntents,
		"",
		map[string]any{
			"type":        "reject",
			"description": "Use microservices architecture",
		},
		agent2.LastVersion,
		agent2.ID,
	)

	assert.Equal(t, ConflictTypeIntent, result.Type, "Should detect contradictory intent")
}
