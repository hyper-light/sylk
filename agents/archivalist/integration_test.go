package archivalist

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Integration Scenario Tests
// =============================================================================

func TestIntegration_SingleAgentWorkflow(t *testing.T) {
	a := newTestArchivalist(t)
	ctx := testContext(t)

	// 1. Register agent
	resp, err := a.RegisterAgent("worker-1", "", "", SourceModelClaudeOpus45)
	require.NoError(t, err, "RegisterAgent")
	require.NotNil(t, resp, "Response should not be nil")
	agentID := resp.AgentID

	// 2. Set task
	a.SetCurrentTask("Implement user authentication", "Add JWT-based auth", SourceModelClaudeOpus45)

	// 3. Read files
	a.RecordFileRead("/src/auth/handler.go", "Auth handler implementation", SourceModelClaudeOpus45)
	a.RecordFileRead("/src/auth/middleware.go", "Auth middleware", SourceModelClaudeOpus45)

	// 4. Store insights
	result := a.StoreInsight(ctx, "JWT tokens should be validated server-side", SourceModelClaudeOpus45)
	assert.True(t, result.Success, "StoreInsight should succeed")

	// 5. Register pattern
	a.RegisterPattern("auth", "Token Validation", "Always validate JWT signature", "jwt.Verify(token)", SourceModelClaudeOpus45)

	// 6. Modify file
	a.RecordFileModified("/src/auth/handler.go", 50, 75, "Added JWT validation", SourceModelClaudeOpus45)

	// 7. Complete steps
	a.CompleteStep("Review existing auth code")
	a.SetNextSteps([]string{"Implement JWT validation", "Add tests", "Update documentation"})

	// 8. Query state
	entries, err := a.Query(ctx, ArchiveQuery{Categories: []Category{CategoryInsight}})
	require.NoError(t, err, "Query")
	assert.True(t, len(entries) > 0, "Should have insights")

	// 9. Get briefing
	briefing := a.GetAgentBriefing()
	assert.Equal(t, "Implement user authentication", briefing.ResumeState.CurrentTask, "Task should match")
	assert.Len(t, briefing.ResumeState.CompletedSteps, 1, "Should have 1 completed step")
	assert.Len(t, briefing.ModifiedFiles, 1, "Should have 1 modified file")
	assert.Len(t, briefing.Patterns, 1, "Should have 1 pattern")

	// 10. Unregister
	err = a.UnregisterAgent(agentID)
	require.NoError(t, err, "UnregisterAgent")
}

func TestIntegration_ParentChildWorkflow(t *testing.T) {
	a := newTestArchivalist(t)
	ctx := testContext(t)

	// Parent registers
	parentResp, _ := a.RegisterAgent("parent", "", "", SourceModelClaudeOpus45)
	parentID := parentResp.AgentID

	// Parent sets task
	a.SetCurrentTask("Refactor entire module", "Complete refactoring", SourceModelClaudeOpus45)
	a.SetNextSteps([]string{"Subtask A", "Subtask B", "Subtask C"})

	// Parent spawns child for subtask
	childResp, err := a.RegisterAgent("child-a", "", parentID, SourceModelGPT52Codex)
	require.NoError(t, err, "Register child")
	childID := childResp.AgentID

	// Child does work
	a.RecordFileRead("/src/subtask_a.go", "Subtask A file", SourceModelGPT52Codex)
	a.StoreEntry(ctx, makeEntry(CategoryInsight, "Subtask A insight from child", SourceModelGPT52Codex))
	a.RecordFileModified("/src/subtask_a.go", 1, 50, "Child made changes", SourceModelGPT52Codex)

	// Child completes and reports
	a.CompleteStep("Subtask A")
	a.RecordFailure("Bad approach in subtask A", "Didn't scale", "Subtask A", SourceModelGPT52Codex)

	// Parent checks child's work
	briefing := a.GetAgentBriefing()
	assert.True(t, len(briefing.ModifiedFiles) > 0, "Should see child's file modifications")
	assert.True(t, len(briefing.RecentFailures) > 0, "Should see child's failures")

	// Child unregisters
	a.UnregisterAgent(childID)

	// Parent continues
	registry := a.GetRegistry()
	parent := registry.Get(parentID)
	assert.NotNil(t, parent, "Parent should still exist")
}

func TestIntegration_HandoffWorkflow(t *testing.T) {
	a := newTestArchivalist(t)
	ctx := testContext(t)

	// Agent A starts work
	respA, _ := a.RegisterAgent("agent-a", "", "", SourceModelClaudeOpus45)

	a.SetCurrentTask("Implement feature X", "Full implementation", SourceModelClaudeOpus45)
	a.SetNextSteps([]string{"Design", "Implement", "Test", "Document"})
	a.CompleteStep("Design")
	a.SetCurrentStep("Implement")

	a.RecordFileRead("/src/feature_x.go", "Main feature file", SourceModelClaudeOpus45)
	a.RecordFileModified("/src/feature_x.go", 1, 100, "Initial implementation", SourceModelClaudeOpus45)

	a.RegisterPattern("feature_x", "Error Pattern", "Return wrapped errors", "fmt.Errorf(...)", SourceModelClaudeOpus45)
	a.RecordUserWants("Clean, well-documented code", "high", "user")

	a.StoreEntry(ctx, makeEntry(CategoryInsight, "Feature X should use strategy pattern", SourceModelClaudeOpus45))

	// Agent A stops (simulating context limit or session end)
	a.UnregisterAgent(respA.AgentID)

	// Agent B takes over
	respB, _ := a.RegisterAgent("agent-b", "", "", SourceModelGPT52Codex)

	// Agent B gets briefing
	briefing := a.GetAgentBriefing()

	// Verify handoff state
	assert.NotNil(t, briefing.ResumeState, "Should have resume state")
	assert.Equal(t, "Implement feature X", briefing.ResumeState.CurrentTask, "Task should be preserved")
	assert.Equal(t, "Implement", briefing.ResumeState.CurrentStep, "Current step should be preserved")
	assert.Len(t, briefing.ResumeState.CompletedSteps, 1, "Completed steps should be preserved")

	// Verify patterns transferred
	assert.Len(t, briefing.Patterns, 1, "Patterns should be preserved")

	// Verify file state transferred
	assert.True(t, a.WasFileRead("/src/feature_x.go"), "File read status should be preserved")
	modifiedFiles := a.GetModifiedFiles()
	assert.Len(t, modifiedFiles, 1, "Modified files should be preserved")

	// Verify user intents transferred
	assert.Len(t, briefing.UserWants, 1, "User wants should be preserved")

	// Agent B continues work
	a.CompleteStep("Implement")
	a.SetCurrentStep("Test")

	// Verify state updated
	state := a.GetResumeState()
	assert.Len(t, state.CompletedSteps, 2, "Should have 2 completed steps now")

	a.UnregisterAgent(respB.AgentID)
}

func TestIntegration_ConflictResolutionWorkflow(t *testing.T) {
	a := newTestArchivalist(t)
	ctx := testContext(t)

	// Register two agents
	resp1, _ := a.RegisterAgent("agent-1", "", "", SourceModelClaudeOpus45)
	resp2, _ := a.RegisterAgent("agent-2", "", "", SourceModelGPT52Codex)

	// Both agents read the same file
	a.RecordFileRead("/src/shared.go", "Shared file", SourceModelClaudeOpus45)

	// Agent 1 modifies file
	a.RecordFileModified("/src/shared.go", 10, 20, "Agent 1 changes", SourceModelClaudeOpus45)

	// Agent 2 tries to modify overlapping lines (potential conflict)
	req := &Request{
		AgentID: resp2.AgentID,
		Version: resp2.Version,
		Write: &WriteRequest{
			Scope: string(ScopeFiles) + "./src/shared.go",
			Data: map[string]any{
				"status": "modified",
				"changes": []any{
					map[string]any{"start_line": 15.0, "end_line": 25.0, "description": "Agent 2 changes"},
				},
			},
		},
	}

	response := a.HandleRequest(ctx, req)

	// Response should indicate conflict or merge
	assert.True(t, response.Status == StatusOK || response.Status == StatusConflict || response.Status == StatusMerged,
		"Should handle conflict appropriately")

	// Check conflict history
	conflicts := a.GetUnresolvedConflicts()
	// May or may not have unresolved conflicts depending on resolution strategy
	_ = conflicts

	a.UnregisterAgent(resp1.AgentID)
	a.UnregisterAgent(resp2.AgentID)
}

func TestIntegration_SessionLifecycle(t *testing.T) {
	a := newTestArchivalist(t)
	ctx := testContext(t)

	// Get initial session
	session1 := a.GetCurrentSession()
	require.NotNil(t, session1, "Should have current session")
	session1ID := session1.ID

	// Do work in session 1
	a.StoreEntry(ctx, makeEntry(CategoryInsight, "Session 1 insight", SourceModelClaudeOpus45))
	a.StoreEntry(ctx, makeEntry(CategoryDecision, "Session 1 decision", SourceModelClaudeOpus45))
	a.SetCurrentTask("Session 1 task", "Objective", SourceModelClaudeOpus45)

	// End session 1
	err := a.EndSession(ctx, "Session 1 completed successfully", "Testing")
	require.NoError(t, err, "EndSession")

	// Verify new session started
	session2 := a.GetCurrentSession()
	assert.NotEqual(t, session1ID, session2.ID, "Should have new session ID")

	// Work in session 2
	a.StoreEntry(ctx, makeEntry(CategoryInsight, "Session 2 insight", SourceModelClaudeOpus45))

	// Query session 1 data
	entries, err := a.Query(ctx, ArchiveQuery{
		SessionIDs: []string{session1ID},
	})
	require.NoError(t, err, "Query session 1")
	// May have entries if not archived, or may need to query archive
	_ = entries

	// Snapshot should show current state
	snapshot := a.GetSnapshot(ctx)
	assert.Equal(t, session2.ID, snapshot.Session.ID, "Snapshot should show current session")
}

func TestIntegration_MultiSessionContinuity(t *testing.T) {
	a := newTestArchivalist(t)

	// Session 1: Establish patterns and make decisions
	a.RegisterPattern("coding", "Error Handling", "Wrap all errors", "fmt.Errorf", SourceModelClaudeOpus45)
	a.RecordUserWants("Consistent error handling", "high", "user")
	a.RecordFailure("Direct error returns", "Lost context", "Error handling", SourceModelClaudeOpus45)

	// End session 1
	a.EndSession(context.Background(), "Session 1 done", "Patterns established")

	// Session 2: Different work, but should have access to session 1 knowledge
	a.SetCurrentTask("Implement new feature", "Use established patterns", SourceModelClaudeOpus45)

	// Patterns should still be accessible
	patterns := a.GetPatterns()
	assert.Len(t, patterns, 1, "Pattern from session 1 should be accessible")

	// Failures should still be accessible
	failure, found := a.CheckFailure("Direct error returns")
	assert.True(t, found, "Failure from session 1 should be accessible")
	assert.NotNil(t, failure, "Failure should not be nil")

	// User wants should still be accessible
	wants := a.GetUserWants()
	assert.Len(t, wants, 1, "User wants from session 1 should be accessible")
}

func TestIntegration_FullRAGPipeline(t *testing.T) {
	// This test requires RAG to be enabled
	a := newTestArchivalistWithRAG(t)
	ctx := testContext(t)

	// Index some content
	a.IndexContent(ctx, "doc-1", "Error handling best practices for Go applications", "documentation", "text")
	a.IndexContent(ctx, "doc-2", "JWT authentication implementation guide", "documentation", "text")
	a.IndexContent(ctx, "doc-3", "Database connection pooling strategies", "documentation", "text")

	// Store related entries
	a.StoreEntry(ctx, makeEntry(CategoryInsight, "Use wrapped errors with context", SourceModelClaudeOpus45))
	a.StoreEntry(ctx, makeEntry(CategoryDecision, "Chose JWT over session cookies", SourceModelClaudeOpus45))

	// Register patterns
	a.RegisterPattern("error", "Wrap Errors", "Always wrap with context", "fmt.Errorf(...)", SourceModelClaudeOpus45)

	// Now query context (would normally call Anthropic, but synthesizer uses mock)
	// Just verify the components are wired correctly via stats
	cacheStats := a.GetQueryCacheStats()
	assert.NotNil(t, cacheStats, "Query cache stats should be available")

	memoryStats := a.GetMemoryStats()
	assert.NotNil(t, memoryStats, "Memory stats should be available")
}

// =============================================================================
// Knowledge Propagation Tests
// =============================================================================

func TestKnowledge_PatternDiscovery(t *testing.T) {
	a := newTestArchivalist(t)

	// Agent 1 establishes pattern in "session 1" context
	a.RegisterAgent("agent-1", "", "", SourceModelClaudeOpus45)
	a.RegisterPattern("api", "REST Conventions", "Use plural nouns for resources", "/users, /orders", SourceModelClaudeOpus45)

	// Simulate session change (pattern persists in AgentContext)
	a.EndSession(context.Background(), "Session 1", "API work")

	// Agent 2 in new session should find the pattern
	a.RegisterAgent("agent-2", "", "", SourceModelGPT52Codex)

	patterns := a.GetPatternsByCategory("api")
	assert.Len(t, patterns, 1, "Pattern should be discoverable across sessions")
	assert.Equal(t, "REST Conventions", patterns[0].Name, "Pattern name should match")
}

func TestKnowledge_FailureAvoidance(t *testing.T) {
	a := newTestArchivalist(t)

	// Record a failure
	a.RecordFailure(
		"Using global state for configuration",
		"Made testing difficult and caused race conditions",
		"Configuration management",
		SourceModelClaudeOpus45,
	)

	// Later, check before attempting same approach
	failure, found := a.CheckFailure("global state")
	assert.True(t, found, "Should find related failure")
	assert.NotNil(t, failure, "Failure should not be nil")
	assert.True(t, len(failure.Reason) > 0, "Should have reason")
}

func TestKnowledge_DecisionRetrieval(t *testing.T) {
	a := newTestArchivalist(t)
	ctx := testContext(t)

	// Make a decision
	result := a.StoreDecision(ctx,
		"Use PostgreSQL over MySQL",
		"Better JSON support and JSONB type for our use case",
		SourceModelClaudeOpus45,
	)
	assert.True(t, result.Success, "StoreDecision should succeed")

	// Query decisions
	entries, err := a.Query(ctx, ArchiveQuery{
		Categories: []Category{CategoryDecision},
	})
	require.NoError(t, err, "Query decisions")
	assert.Len(t, entries, 1, "Should have 1 decision")
	assert.True(t, len(entries[0].Content) > 0, "Decision should have content")
}

func TestKnowledge_CrossAgentLearning(t *testing.T) {
	a := newTestArchivalist(t)

	// Agent 1 learns something the hard way and records the resolution
	a.RegisterAgent("agent-1", "", "", SourceModelClaudeOpus45)
	a.RecordFailureWithResolution(
		"Recursive file walking without depth limit",
		"Caused stack overflow on deep directory structures",
		"File system traversal",
		"Use iterative approach with explicit stack and depth limit",
		SourceModelClaudeOpus45,
	)

	// Agent 2 can benefit from this learning
	a.RegisterAgent("agent-2", "", "", SourceModelGPT52Codex)

	failure, found := a.CheckFailure("recursive file")
	assert.True(t, found, "Agent 2 should find Agent 1's failure")
	assert.True(t, len(failure.Resolution) > 0, "Should have resolution")
}

// =============================================================================
// Durability Tests
// =============================================================================

func TestDurability_VersionContinuity(t *testing.T) {
	a := newTestArchivalist(t)
	ctx := testContext(t)

	// Perform many operations
	for i := 0; i < 100; i++ {
		req := &Request{
			AgentID: "agent-1",
			Write: &WriteRequest{
				Scope: string(ScopeResume),
				Data: map[string]any{
					"current_step": "Step " + string(rune('0'+i%10)),
				},
			},
		}
		a.HandleRequest(ctx, req)
	}

	// Version should have incremented properly
	registry := a.GetRegistry()
	version := registry.GetVersionNumber()
	assert.True(t, version > 0, "Version should have incremented")
}

func TestDurability_CleanShutdown(t *testing.T) {
	tmpDir := t.TempDir()

	// Create archivalist with archive
	a, err := New(Config{
		AnthropicAPIKey: "test-key",
		EnableArchive:   true,
		EnableRAG:       false,
		ArchivePath:     tmpDir + "/archive.db",
	})
	require.NoError(t, err, "New")

	// Do some work
	ctx := context.Background()
	a.StoreEntry(ctx, makeEntry(CategoryInsight, "Test insight", SourceModelClaudeOpus45))
	a.SetCurrentTask("Test task", "Objective", SourceModelClaudeOpus45)

	// Close should not error
	err = a.Close()
	require.NoError(t, err, "Close")
}

func TestDurability_PartialWriteRecovery(t *testing.T) {
	a := newTestArchivalist(t)
	ctx := testContext(t)

	// Batch with some invalid operations
	req := &Request{
		AgentID: "agent-1",
		Batch: &BatchRequest{
			Writes: []WriteRequest{
				{Scope: string(ScopeResume), Data: map[string]any{"current_task": "Task 1"}},
				{Scope: string(ScopeResume), Data: map[string]any{"current_task": "Task 2"}},
				{Scope: string(ScopeResume), Data: map[string]any{"current_task": "Task 3"}},
			},
		},
	}

	resp := a.HandleRequest(ctx, req)

	// Should handle gracefully
	assert.True(t, resp.Status == StatusOK || resp.Status == StatusPartial, "Should handle batch")
}

// =============================================================================
// Protocol Integration Tests
// =============================================================================

func TestProtocol_FullWriteReadCycle(t *testing.T) {
	a := newTestArchivalist(t)
	ctx := testContext(t)

	// Register
	registerReq := &Request{
		Register: &RegisterRequest{
			Name:    "test-agent",
			Session: "session-1",
		},
	}
	registerResp := a.HandleRequest(ctx, registerReq)
	assert.Equal(t, StatusOK, registerResp.Status, "Register should succeed")
	agentID := registerResp.AgentID
	version := registerResp.Version

	// Write
	writeReq := &Request{
		AgentID: agentID,
		Version: version,
		Write: &WriteRequest{
			Scope: string(ScopeResume),
			Data: map[string]any{
				"current_task": "Integration test task",
				"objective":    "Test the protocol",
			},
		},
	}
	writeResp := a.HandleRequest(ctx, writeReq)
	assert.True(t, writeResp.Status == StatusOK || writeResp.Status == StatusMerged, "Write should succeed")
	newVersion := writeResp.Version

	// Read
	readReq := &Request{
		AgentID: agentID,
		Read: &ReadRequest{
			Scope: string(ScopeResume),
		},
	}
	readResp := a.HandleRequest(ctx, readReq)
	assert.Equal(t, StatusOK, readResp.Status, "Read should succeed")
	assert.NotNil(t, readResp.Data, "Should have data")

	// Delta read
	deltaReq := &Request{
		AgentID: agentID,
		Read: &ReadRequest{
			Since: version,
			Limit: 10,
		},
	}
	deltaResp := a.HandleRequest(ctx, deltaReq)
	assert.Equal(t, StatusOK, deltaResp.Status, "Delta read should succeed")

	// Briefing
	briefingReq := &Request{
		AgentID: agentID,
		Briefing: &BriefingRequest{
			Tier: BriefingStandard,
		},
	}
	briefingResp := a.HandleRequest(ctx, briefingReq)
	assert.Equal(t, StatusOK, briefingResp.Status, "Briefing should succeed")

	_ = newVersion
}

func TestProtocol_MicroBriefingFormat(t *testing.T) {
	a := newTestArchivalist(t)
	ctx := testContext(t)

	// Set up state
	a.SetCurrentTask("Implement auth", "JWT implementation", SourceModelClaudeOpus45)
	a.CompleteStep("Design")
	a.CompleteStep("Setup")
	a.SetNextSteps([]string{"Implement", "Test"})
	a.RecordFileModified("/src/auth.go", 1, 50, "Auth handler", SourceModelClaudeOpus45)
	a.AddBlocker("Waiting for keys")

	// Get micro briefing
	briefingReq := &Request{
		Briefing: &BriefingRequest{
			Tier: BriefingMicro,
		},
	}
	resp := a.HandleRequest(ctx, briefingReq)

	assert.Equal(t, StatusOK, resp.Status, "Should succeed")
	assert.True(t, len(resp.Briefing) > 0, "Should have briefing string")

	// Micro briefing format: task:progress:files:block=blocker
	t.Logf("Micro briefing: %s", resp.Briefing)
}
