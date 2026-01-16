package archivalist

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// =============================================================================
// AgentContext Unit Tests
// =============================================================================

func TestAgentContext_RecordFileRead(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.RecordFileRead("/src/main.go", "Main entry point", SourceModelClaudeOpus45)

	state := ctx.GetFileState("/src/main.go")
	assert.NotNil(t, state, "File state should exist")
	assert.Equal(t, "/src/main.go", state.Path, "Path should match")
	assert.Equal(t, "Main entry point", state.Summary, "Summary should match")
	assert.Equal(t, FileStatusRead, state.Status, "Status should be read")
	assert.Equal(t, SourceModelClaudeOpus45, state.Agent, "Agent should match")
}

func TestAgentContext_RecordFileModified(t *testing.T) {
	ctx := newTestAgentContext(t)

	// First read the file
	ctx.RecordFileRead("/src/main.go", "Main entry point", SourceModelClaudeOpus45)

	// Then modify it
	ctx.RecordFileModified("/src/main.go", FileChange{
		StartLine:   10,
		EndLine:     20,
		Description: "Added error handling",
	}, SourceModelGPT52Codex)

	state := ctx.GetFileState("/src/main.go")
	assert.NotNil(t, state, "File state should exist")
	assert.Equal(t, FileStatusModified, state.Status, "Status should be modified")
	assert.Equal(t, SourceModelGPT52Codex, state.Agent, "Last agent should be GPT")
	assert.Len(t, state.Changes, 1, "Should have one change")
	assert.Equal(t, 10, state.Changes[0].StartLine, "StartLine should match")
	assert.Equal(t, 20, state.Changes[0].EndLine, "EndLine should match")
}

func TestAgentContext_RecordFileModified_MultipleChanges(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.RecordFileRead("/src/main.go", "Main entry point", SourceModelClaudeOpus45)
	ctx.RecordFileModified("/src/main.go", FileChange{StartLine: 10, EndLine: 20, Description: "Change 1"}, SourceModelClaudeOpus45)
	ctx.RecordFileModified("/src/main.go", FileChange{StartLine: 30, EndLine: 40, Description: "Change 2"}, SourceModelGPT52Codex)
	ctx.RecordFileModified("/src/main.go", FileChange{StartLine: 50, EndLine: 60, Description: "Change 3"}, SourceModelClaudeOpus45)

	state := ctx.GetFileState("/src/main.go")
	assert.Len(t, state.Changes, 3, "Should have three changes")
}

func TestAgentContext_RecordFileCreated(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.RecordFileCreated("/src/new_file.go", "New utility module", SourceModelClaudeOpus45)

	state := ctx.GetFileState("/src/new_file.go")
	assert.NotNil(t, state, "File state should exist")
	assert.Equal(t, FileStatusCreated, state.Status, "Status should be created")
	assert.Equal(t, "New utility module", state.Summary, "Summary should match")
}

func TestAgentContext_WasFileRead(t *testing.T) {
	ctx := newTestAgentContext(t)

	assert.False(t, ctx.WasFileRead("/src/main.go"), "File should not be marked as read")

	ctx.RecordFileRead("/src/main.go", "Main entry point", SourceModelClaudeOpus45)

	assert.True(t, ctx.WasFileRead("/src/main.go"), "File should be marked as read")
	assert.False(t, ctx.WasFileRead("/src/other.go"), "Other file should not be marked as read")
}

func TestAgentContext_GetModifiedFiles(t *testing.T) {
	ctx := newTestAgentContext(t)

	// Read some files (not modified)
	ctx.RecordFileRead("/src/read_only.go", "Read only", SourceModelClaudeOpus45)

	// Modify some files
	ctx.RecordFileRead("/src/modified1.go", "Modified 1", SourceModelClaudeOpus45)
	ctx.RecordFileModified("/src/modified1.go", FileChange{StartLine: 1, EndLine: 10}, SourceModelClaudeOpus45)

	ctx.RecordFileRead("/src/modified2.go", "Modified 2", SourceModelClaudeOpus45)
	ctx.RecordFileModified("/src/modified2.go", FileChange{StartLine: 1, EndLine: 10}, SourceModelClaudeOpus45)

	// Create a new file
	ctx.RecordFileCreated("/src/new.go", "New file", SourceModelClaudeOpus45)

	modified := ctx.GetModifiedFiles()

	assert.Len(t, modified, 3, "Should have 3 modified/created files")
}

func TestAgentContext_GetAllFiles(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.RecordFileRead("/src/a.go", "A", SourceModelClaudeOpus45)
	ctx.RecordFileRead("/src/b.go", "B", SourceModelClaudeOpus45)
	ctx.RecordFileCreated("/src/c.go", "C", SourceModelClaudeOpus45)

	files := ctx.GetAllFiles()

	assert.Len(t, files, 3, "Should have 3 files")
}

// =============================================================================
// Pattern Tracking Tests
// =============================================================================

func TestAgentContext_RegisterPattern(t *testing.T) {
	ctx := newTestAgentContext(t)

	pattern := &Pattern{
		Category:    "error_handling",
		Name:        "Wrap errors",
		Description: "Always wrap errors with context",
		Example:     "fmt.Errorf(\"failed to do X: %w\", err)",
		Source:      SourceModelClaudeOpus45,
	}

	ctx.RegisterPattern(pattern)

	assert.NotEmpty(t, pattern.ID, "Pattern ID should be set")
	assert.False(t, pattern.CreatedAt.IsZero(), "CreatedAt should be set")

	retrieved, found := ctx.GetPatternByID(pattern.ID)
	assert.True(t, found, "Pattern should be found")
	assert.Equal(t, "Wrap errors", retrieved.Name, "Name should match")
}

func TestAgentContext_GetPatternsByCategory(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.RegisterPattern(&Pattern{Category: "error_handling", Name: "Pattern 1", Source: SourceModelClaudeOpus45})
	ctx.RegisterPattern(&Pattern{Category: "error_handling", Name: "Pattern 2", Source: SourceModelClaudeOpus45})
	ctx.RegisterPattern(&Pattern{Category: "naming", Name: "Pattern 3", Source: SourceModelClaudeOpus45})

	errorPatterns := ctx.GetPatternsByCategory("error_handling")

	assert.Len(t, errorPatterns, 2, "Should have 2 error_handling patterns")
}

func TestAgentContext_GetAllPatterns(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.RegisterPattern(&Pattern{Category: "a", Name: "Pattern 1", Source: SourceModelClaudeOpus45})
	ctx.RegisterPattern(&Pattern{Category: "b", Name: "Pattern 2", Source: SourceModelClaudeOpus45})
	ctx.RegisterPattern(&Pattern{Category: "c", Name: "Pattern 3", Source: SourceModelClaudeOpus45})

	patterns := ctx.GetAllPatterns()

	assert.Len(t, patterns, 3, "Should have 3 patterns")
}

func TestAgentContext_GetPattern_ByCategory(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.RegisterPattern(&Pattern{Category: "error_handling", Name: "Wrap errors", Source: SourceModelClaudeOpus45})

	// GetPattern can find by category
	pattern := ctx.GetPattern("error_handling")

	assert.NotNil(t, pattern, "Should find pattern by category")
	assert.Equal(t, "Wrap errors", pattern.Name, "Name should match")
}

// =============================================================================
// Failure Tracking Tests
// =============================================================================

func TestAgentContext_RecordFailure(t *testing.T) {
	ctx := newTestAgentContext(t)

	failure := ctx.RecordFailure(
		"Using regex for HTML parsing",
		"HTML is not a regular language",
		"Trying to extract data from web page",
		SourceModelClaudeOpus45,
	)

	assert.NotEmpty(t, failure.ID, "Failure ID should be set")
	assert.Equal(t, "Using regex for HTML parsing", failure.Approach, "Approach should match")
	assert.Equal(t, "HTML is not a regular language", failure.Reason, "Reason should match")
	assert.Equal(t, "Trying to extract data from web page", failure.Context, "Context should match")
	assert.False(t, failure.Timestamp.IsZero(), "Timestamp should be set")
}

func TestAgentContext_RecordFailureWithResolution(t *testing.T) {
	ctx := newTestAgentContext(t)

	failure := ctx.RecordFailureWithResolution(
		"Using regex for HTML parsing",
		"HTML is not a regular language",
		"Trying to extract data from web page",
		"Use goquery or htmlparser instead",
		SourceModelClaudeOpus45,
	)

	assert.Equal(t, "Use goquery or htmlparser instead", failure.Resolution, "Resolution should be set")
}

func TestAgentContext_CheckFailure(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.RecordFailure("Bad approach", "It didn't work", "Testing", SourceModelClaudeOpus45)

	// Check for exact match
	failure, found := ctx.CheckFailure("Bad approach")
	assert.True(t, found, "Should find failure")
	assert.NotNil(t, failure, "Failure should not be nil")

	// Check for non-existent
	_, found = ctx.CheckFailure("Different approach")
	assert.False(t, found, "Should not find non-existent failure")
}

func TestAgentContext_CheckFailure_PartialMatch(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.RecordFailure("Using regex for HTML parsing", "Doesn't work", "Testing", SourceModelClaudeOpus45)

	// Check for partial match
	failure, found := ctx.CheckFailure("regex for HTML")
	assert.True(t, found, "Should find failure with partial match")
	assert.NotNil(t, failure, "Failure should not be nil")
}

func TestAgentContext_GetRecentFailures(t *testing.T) {
	ctx := newTestAgentContext(t)

	for i := 0; i < 10; i++ {
		ctx.RecordFailure("Approach "+string(rune('0'+i)), "Reason", "Context", SourceModelClaudeOpus45)
	}

	recent := ctx.GetRecentFailures(5)

	assert.Len(t, recent, 5, "Should return 5 recent failures")
}

func TestAgentContext_GetAllFailures(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.RecordFailure("Approach 1", "Reason 1", "Context", SourceModelClaudeOpus45)
	ctx.RecordFailure("Approach 2", "Reason 2", "Context", SourceModelGPT52Codex)

	failures := ctx.GetAllFailures()

	assert.Len(t, failures, 2, "Should have 2 failures")
}

// =============================================================================
// Intent Tracking Tests
// =============================================================================

func TestAgentContext_RecordUserWants(t *testing.T) {
	ctx := newTestAgentContext(t)

	intent := ctx.RecordUserWants("Clean code with no magic numbers", "high", "user conversation")

	assert.NotEmpty(t, intent.ID, "Intent ID should be set")
	assert.Equal(t, IntentTypeWant, intent.Type, "Type should be want")
	assert.Equal(t, "Clean code with no magic numbers", intent.Content, "Content should match")
	assert.Equal(t, "high", intent.Priority, "Priority should match")
}

func TestAgentContext_RecordUserRejects(t *testing.T) {
	ctx := newTestAgentContext(t)

	intent := ctx.RecordUserRejects("No inline styles in React", "code review")

	assert.Equal(t, IntentTypeReject, intent.Type, "Type should be reject")
	assert.Equal(t, "No inline styles in React", intent.Content, "Content should match")
}

func TestAgentContext_GetUserWants(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.RecordUserWants("Want 1", "high", "source")
	ctx.RecordUserRejects("Reject 1", "source")
	ctx.RecordUserWants("Want 2", "medium", "source")

	wants := ctx.GetUserWants()

	assert.Len(t, wants, 2, "Should have 2 wants")
	for _, w := range wants {
		assert.Equal(t, IntentTypeWant, w.Type, "All should be wants")
	}
}

func TestAgentContext_GetUserRejects(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.RecordUserWants("Want 1", "high", "source")
	ctx.RecordUserRejects("Reject 1", "source")
	ctx.RecordUserRejects("Reject 2", "source")

	rejects := ctx.GetUserRejects()

	assert.Len(t, rejects, 2, "Should have 2 rejects")
	for _, r := range rejects {
		assert.Equal(t, IntentTypeReject, r.Type, "All should be rejects")
	}
}

func TestAgentContext_GetAllIntents(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.RecordUserWants("Want 1", "high", "source")
	ctx.RecordUserRejects("Reject 1", "source")
	ctx.RecordUserWants("Want 2", "medium", "source")

	intents := ctx.GetAllIntents()

	assert.Len(t, intents, 3, "Should have 3 intents total")
}

// =============================================================================
// Resume State Tests
// =============================================================================

func TestAgentContext_SetCurrentTask(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.SetCurrentTask("Implement authentication", "Add JWT-based auth to API", SourceModelClaudeOpus45)

	state := ctx.GetResumeState()
	assert.Equal(t, "Implement authentication", state.CurrentTask, "Task should match")
	assert.Equal(t, "Add JWT-based auth to API", state.TaskObjective, "Objective should match")
	assert.Equal(t, SourceModelClaudeOpus45, state.LastAgent, "Agent should match")
}

func TestAgentContext_CompleteStep(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.SetCurrentTask("Task", "Objective", SourceModelClaudeOpus45)
	ctx.CompleteStep("Step 1")
	ctx.CompleteStep("Step 2")
	ctx.CompleteStep("Step 3")

	state := ctx.GetResumeState()
	assert.Len(t, state.CompletedSteps, 3, "Should have 3 completed steps")
	assert.Equal(t, "Step 1", state.CompletedSteps[0], "First step should match")
}

func TestAgentContext_SetCurrentStep(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.SetCurrentTask("Task", "Objective", SourceModelClaudeOpus45)
	ctx.SetCurrentStep("Working on database schema")

	state := ctx.GetResumeState()
	assert.Equal(t, "Working on database schema", state.CurrentStep, "Current step should match")
}

func TestAgentContext_SetNextSteps(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.SetCurrentTask("Task", "Objective", SourceModelClaudeOpus45)
	ctx.SetNextSteps([]string{"Step A", "Step B", "Step C"})

	state := ctx.GetResumeState()
	assert.Len(t, state.NextSteps, 3, "Should have 3 next steps")
	assert.Equal(t, "Step A", state.NextSteps[0], "First next step should match")
}

func TestAgentContext_AddBlocker(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.SetCurrentTask("Task", "Objective", SourceModelClaudeOpus45)
	ctx.AddBlocker("Waiting for API credentials")
	ctx.AddBlocker("Database not responding")

	state := ctx.GetResumeState()
	assert.Len(t, state.Blockers, 2, "Should have 2 blockers")
}

func TestAgentContext_RemoveBlocker(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.SetCurrentTask("Task", "Objective", SourceModelClaudeOpus45)
	ctx.AddBlocker("Blocker 1")
	ctx.AddBlocker("Blocker 2")
	ctx.AddBlocker("Blocker 3")

	ctx.RemoveBlocker("Blocker 2")

	state := ctx.GetResumeState()
	assert.Len(t, state.Blockers, 2, "Should have 2 blockers after removal")
}

func TestAgentContext_SetFilesToRead(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.SetCurrentTask("Task", "Objective", SourceModelClaudeOpus45)
	ctx.SetFilesToRead([]string{"/src/main.go", "/src/config.go"})

	state := ctx.GetResumeState()
	assert.Len(t, state.FilesToRead, 2, "Should have 2 files to read")
}

func TestAgentContext_UpdateResumeState(t *testing.T) {
	ctx := newTestAgentContext(t)

	ctx.SetCurrentTask("Task", "Objective", SourceModelClaudeOpus45)

	ctx.UpdateResumeState(func(rs *ResumeState) {
		rs.CurrentStep = "Custom step"
		rs.NextSteps = []string{"A", "B"}
		rs.Blockers = []string{"Blocker"}
	})

	state := ctx.GetResumeState()
	assert.Equal(t, "Custom step", state.CurrentStep, "Current step should be updated")
	assert.Len(t, state.NextSteps, 2, "Next steps should be updated")
	assert.Len(t, state.Blockers, 1, "Blockers should be updated")
}

// =============================================================================
// Agent Briefing Tests
// =============================================================================

func TestAgentContext_GetAgentBriefing(t *testing.T) {
	ctx := newTestAgentContext(t)

	// Set up some state
	ctx.SetCurrentTask("Implement feature X", "Full implementation", SourceModelClaudeOpus45)
	ctx.CompleteStep("Design")
	ctx.SetNextSteps([]string{"Implement", "Test"})
	ctx.AddBlocker("Waiting for spec")

	ctx.RecordFileRead("/src/main.go", "Main file", SourceModelClaudeOpus45)
	ctx.RecordFileModified("/src/main.go", FileChange{StartLine: 1, EndLine: 10}, SourceModelClaudeOpus45)

	ctx.RegisterPattern(&Pattern{Category: "testing", Name: "Table tests", Source: SourceModelClaudeOpus45})

	ctx.RecordFailure("Bad approach", "Didn't work", "Testing", SourceModelClaudeOpus45)

	ctx.RecordUserWants("Clean code", "high", "user")

	briefing := ctx.GetAgentBriefing()

	assert.NotNil(t, briefing.ResumeState, "Resume state should be included")
	assert.Equal(t, "Implement feature X", briefing.ResumeState.CurrentTask, "Task should match")
	assert.Len(t, briefing.ModifiedFiles, 1, "Should have 1 modified file")
	assert.Len(t, briefing.Patterns, 1, "Should have 1 pattern")
	assert.Len(t, briefing.RecentFailures, 1, "Should have 1 failure")
	assert.Len(t, briefing.UserWants, 1, "Should have 1 user want")
}

func TestAgentContext_GetAgentBriefing_Empty(t *testing.T) {
	ctx := newTestAgentContext(t)

	briefing := ctx.GetAgentBriefing()

	// Should return empty but not nil
	assert.NotNil(t, briefing, "Briefing should not be nil")
	assert.Empty(t, briefing.ModifiedFiles, "Should have no modified files")
	assert.Empty(t, briefing.Patterns, "Should have no patterns")
}

// =============================================================================
// State Tracking Integration Tests
// =============================================================================

func TestStateTracking_FullWorkflow(t *testing.T) {
	ctx := newTestAgentContext(t)

	// Start a task
	ctx.SetCurrentTask("Refactor authentication", "Improve auth security", SourceModelClaudeOpus45)
	ctx.SetNextSteps([]string{"Review current impl", "Identify vulnerabilities", "Implement fixes", "Add tests"})

	// Read files
	ctx.RecordFileRead("/src/auth/handler.go", "Auth handler", SourceModelClaudeOpus45)
	ctx.RecordFileRead("/src/auth/middleware.go", "Auth middleware", SourceModelClaudeOpus45)

	// Record a pattern discovered
	ctx.RegisterPattern(&Pattern{
		Category:    "auth",
		Name:        "Token validation",
		Description: "Always validate token expiry",
		Source:      SourceModelClaudeOpus45,
	})

	// Complete first step
	ctx.CompleteStep("Review current impl")
	ctx.SetCurrentStep("Identify vulnerabilities")

	// Record a failure
	ctx.RecordFailure("Using basic string comparison for tokens", "Timing attack vulnerability", "Token validation", SourceModelClaudeOpus45)

	// Modify a file
	ctx.RecordFileModified("/src/auth/handler.go", FileChange{
		StartLine:   50,
		EndLine:     70,
		Description: "Fixed timing attack vulnerability",
	}, SourceModelClaudeOpus45)

	// Complete another step
	ctx.CompleteStep("Identify vulnerabilities")
	ctx.SetCurrentStep("Implement fixes")

	// Add a blocker
	ctx.AddBlocker("Need security review from team lead")

	// Get full briefing
	briefing := ctx.GetAgentBriefing()

	assert.Equal(t, "Refactor authentication", briefing.ResumeState.CurrentTask, "Task should match")
	assert.Len(t, briefing.ResumeState.CompletedSteps, 2, "Should have 2 completed steps")
	assert.Equal(t, "Implement fixes", briefing.ResumeState.CurrentStep, "Current step should match")
	assert.Len(t, briefing.ResumeState.Blockers, 1, "Should have 1 blocker")
	assert.Len(t, briefing.ModifiedFiles, 1, "Should have 1 modified file")
	assert.Len(t, briefing.Patterns, 1, "Should have 1 pattern")
	assert.Len(t, briefing.RecentFailures, 1, "Should have 1 failure")
}
