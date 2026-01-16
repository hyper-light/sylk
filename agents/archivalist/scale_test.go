package archivalist

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Scale Tests
// =============================================================================

func TestScale_10kEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping scale test in short mode")
	}

	store := newTestStore(t)

	// Insert 10,000 entries
	start := time.Now()
	for i := 0; i < 10000; i++ {
		store.InsertEntry(makeEntry(
			Category(AllCategories()[i%len(AllCategories())]),
			fmt.Sprintf("Content for entry %d with some additional text to make it realistic", i),
			SourceModel(ValidSources()[i%len(ValidSources())]),
		))
	}
	insertDuration := time.Since(start)
	t.Logf("Inserted 10,000 entries in %v", insertDuration)

	// Query performance
	start = time.Now()
	results, err := store.Query(ArchiveQuery{
		Categories: []Category{CategoryInsight},
		Limit:      100,
	})
	queryDuration := time.Since(start)
	t.Logf("Query completed in %v, returned %d results", queryDuration, len(results))

	require.NoError(t, err, "Query")
	assert.True(t, queryDuration < 100*time.Millisecond, "Query should be fast")
}

func TestScale_1kFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping scale test in short mode")
	}

	ctx := newTestAgentContext(t)

	// Track 1,000 files
	start := time.Now()
	for i := 0; i < 1000; i++ {
		path := fmt.Sprintf("/src/module%d/file%d.go", i/100, i%100)
		ctx.RecordFileRead(path, fmt.Sprintf("File %d summary", i), SourceModelClaudeOpus45)
		if i%3 == 0 {
			ctx.RecordFileModified(path, FileChange{
				StartLine:   i * 10,
				EndLine:     i*10 + 50,
				Description: "Modified for test",
			}, SourceModelClaudeOpus45)
		}
	}
	trackDuration := time.Since(start)
	t.Logf("Tracked 1,000 files in %v", trackDuration)

	// Lookup performance
	start = time.Now()
	for i := 0; i < 100; i++ {
		path := fmt.Sprintf("/src/module%d/file%d.go", i/10, i%10)
		ctx.WasFileRead(path)
		ctx.GetFileState(path)
	}
	lookupDuration := time.Since(start)
	t.Logf("100 lookups completed in %v", lookupDuration)

	assert.True(t, lookupDuration < 10*time.Millisecond, "File lookups should be fast")

	// Get modified files
	start = time.Now()
	modified := ctx.GetModifiedFiles()
	modifiedDuration := time.Since(start)
	t.Logf("Got %d modified files in %v", len(modified), modifiedDuration)

	assert.True(t, len(modified) > 300, "Should have many modified files")
}

func TestScale_500Patterns(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping scale test in short mode")
	}

	ctx := newTestAgentContext(t)

	categories := []string{"error_handling", "naming", "testing", "logging", "validation", "security", "performance", "documentation", "architecture", "database"}

	// Register 500 patterns
	start := time.Now()
	for i := 0; i < 500; i++ {
		ctx.RegisterPattern(&Pattern{
			Category:    categories[i%len(categories)],
			Name:        fmt.Sprintf("Pattern %d", i),
			Description: fmt.Sprintf("Description for pattern %d with detailed explanation", i),
			Example:     fmt.Sprintf("// Example code for pattern %d\nfunc example() {}", i),
			Source:      SourceModelClaudeOpus45,
		})
	}
	registerDuration := time.Since(start)
	t.Logf("Registered 500 patterns in %v", registerDuration)

	// Query by category
	start = time.Now()
	patterns := ctx.GetPatternsByCategory("error_handling")
	categoryDuration := time.Since(start)
	t.Logf("Got %d patterns for category in %v", len(patterns), categoryDuration)

	assert.Equal(t, 50, len(patterns), "Should have 50 error_handling patterns")
	assert.True(t, categoryDuration < 10*time.Millisecond, "Category lookup should be fast")

	// Get all patterns
	start = time.Now()
	all := ctx.GetAllPatterns()
	allDuration := time.Since(start)
	t.Logf("Got all %d patterns in %v", len(all), allDuration)

	assert.Equal(t, 500, len(all), "Should have 500 total patterns")
}

func TestScale_200Failures(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping scale test in short mode")
	}

	ctx := newTestAgentContext(t)

	// Record 200 failures
	approaches := []string{"regex parsing", "string manipulation", "recursive algorithm", "greedy approach", "brute force", "caching strategy"}

	start := time.Now()
	for i := 0; i < 200; i++ {
		ctx.RecordFailure(
			fmt.Sprintf("%s variant %d", approaches[i%len(approaches)], i),
			fmt.Sprintf("Reason %d: explanation of why it failed", i),
			fmt.Sprintf("Task context %d", i),
			SourceModelClaudeOpus45,
		)
	}
	recordDuration := time.Since(start)
	t.Logf("Recorded 200 failures in %v", recordDuration)

	// Check failure lookup
	start = time.Now()
	for i := 0; i < 100; i++ {
		approach := fmt.Sprintf("regex parsing variant %d", i*2)
		ctx.CheckFailure(approach)
	}
	checkDuration := time.Since(start)
	t.Logf("100 failure checks completed in %v", checkDuration)

	assert.True(t, checkDuration < 50*time.Millisecond, "Failure checks should be reasonably fast")
}

func TestScale_100Agents(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping scale test in short mode")
	}

	registry := newTestRegistry(t)

	// Register 100 agents across 10 sessions
	start := time.Now()
	var agents []*RegisteredAgent
	for i := 0; i < 100; i++ {
		sessionID := fmt.Sprintf("session-%d", i/10)
		var parentID string
		if i > 0 && i%5 != 0 {
			// Create hierarchy
			parentID = agents[len(agents)-1].ID
		}
		agent, err := registry.Register(
			fmt.Sprintf("agent-%d", i),
			sessionID,
			parentID,
			SourceModel(ValidSources()[i%len(ValidSources())]),
		)
		if err == nil {
			agents = append(agents, agent)
		}
	}
	registerDuration := time.Since(start)
	t.Logf("Registered %d agents in %v", len(agents), registerDuration)

	// Stats
	stats := registry.GetStats()
	assert.Equal(t, len(agents), stats.TotalAgents, "All agents should be registered")
	assert.Equal(t, 10, stats.SessionCount, "Should have 10 sessions")

	// Hierarchy queries
	start = time.Now()
	for i := 0; i < 20; i++ {
		agent := agents[len(agents)-1-i]
		registry.GetAgentHierarchy(agent.ID)
		registry.GetRootAgent(agent.ID)
	}
	hierarchyDuration := time.Since(start)
	t.Logf("20 hierarchy queries in %v", hierarchyDuration)
}

func TestScale_50kEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping scale test in short mode")
	}

	el := NewEventLog(EventLogConfig{
		MaxEvents: 50000,
	})
	defer el.Close()

	eventTypes := []EventType{EventTypeFileRead, EventTypeFileModify, EventTypePatternAdd, EventTypeFailureRecord, EventTypeResumeUpdate}

	// Insert 50,000 events
	start := time.Now()
	for i := 0; i < 50000; i++ {
		event := makeEvent(
			eventTypes[i%len(eventTypes)],
			fmt.Sprintf("agent-%d", i%100),
			fmt.Sprintf("session-%d", i%10),
			nil,
		)
		event.Version = fmt.Sprintf("v%d", i)
		el.Append(event)
	}
	insertDuration := time.Since(start)
	t.Logf("Inserted 50,000 events in %v", insertDuration)

	// Delta query
	start = time.Now()
	delta := el.GetDelta("v49000", 1000)
	deltaDuration := time.Since(start)
	t.Logf("Delta query returned %d entries in %v", len(delta), deltaDuration)

	assert.True(t, deltaDuration < 100*time.Millisecond, "Delta query should be fast")

	// Recent events
	start = time.Now()
	recent := el.GetRecent(100)
	recentDuration := time.Since(start)
	t.Logf("Got %d recent events in %v", len(recent), recentDuration)

	assert.Equal(t, 100, len(recent), "Should get 100 recent events")
}

func TestScale_QueryPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping scale test in short mode")
	}

	store := newTestStore(t)

	// Populate with varied data
	for i := 0; i < 5000; i++ {
		entry := makeEntry(
			Category(AllCategories()[i%len(AllCategories())]),
			fmt.Sprintf("Content %d with searchable text keyword-%d", i, i%100),
			SourceModel(ValidSources()[i%len(ValidSources())]),
		)
		store.InsertEntry(entry)
	}

	testCases := []struct {
		name  string
		query ArchiveQuery
	}{
		{"Single category", ArchiveQuery{Categories: []Category{CategoryInsight}, Limit: 50}},
		{"Multiple categories", ArchiveQuery{Categories: []Category{CategoryInsight, CategoryDecision, CategoryIssue}, Limit: 50}},
		{"Single source", ArchiveQuery{Sources: []SourceModel{SourceModelClaudeOpus45}, Limit: 50}},
		{"Combined filters", ArchiveQuery{Categories: []Category{CategoryInsight}, Sources: []SourceModel{SourceModelClaudeOpus45}, Limit: 50}},
		{"Date range", ArchiveQuery{Since: timePtr(time.Now().Add(-1 * time.Hour)), Limit: 50}},
		{"No filters, limit only", ArchiveQuery{Limit: 100}},
	}

	for _, tc := range testCases {
		start := time.Now()
		results, err := store.Query(tc.query)
		duration := time.Since(start)

		require.NoError(t, err, tc.name)
		t.Logf("%s: %d results in %v", tc.name, len(results), duration)
		assert.True(t, duration < 100*time.Millisecond, tc.name+" should be under 100ms")
	}
}

func TestScale_TextSearchPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping scale test in short mode")
	}

	store := newTestStore(t)

	// Populate with searchable content
	keywords := []string{"authentication", "database", "performance", "security", "validation", "caching"}
	for i := 0; i < 5000; i++ {
		keyword := keywords[i%len(keywords)]
		entry := makeEntry(
			CategoryGeneral,
			fmt.Sprintf("Content about %s with details %d and more %s information", keyword, i, keyword),
			SourceModelClaudeOpus45,
		)
		store.InsertEntry(entry)
	}

	// Search tests
	for _, keyword := range keywords {
		start := time.Now()
		results, err := store.SearchText(keyword, false, 50)
		duration := time.Since(start)

		require.NoError(t, err, "SearchText "+keyword)
		t.Logf("Search for '%s': %d results in %v", keyword, len(results), duration)
		assert.True(t, duration < 200*time.Millisecond, "Search should be under 200ms")
	}
}

func TestScale_ConcurrentAgentOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping scale test in short mode")
	}

	a := newTestArchivalist(t)
	ctx := context.Background()

	// Simulate 20 agents working concurrently
	numAgents := 20
	opsPerAgent := 50

	runConcurrent(t, numAgents, func(agentNum int) {
		agentName := fmt.Sprintf("agent-%d", agentNum)

		// Register
		resp, _ := a.RegisterAgent(agentName, "", "", SourceModelClaudeOpus45)
		if resp == nil {
			return
		}
		agentID := resp.AgentID

		for op := 0; op < opsPerAgent; op++ {
			// Store entries
			a.StoreEntry(ctx, makeEntry(
				CategoryGeneral,
				fmt.Sprintf("Work from %s operation %d", agentName, op),
				SourceModelClaudeOpus45,
			))

			// Record files
			a.RecordFileRead(
				fmt.Sprintf("/src/%s/file%d.go", agentName, op),
				"Summary",
				SourceModelClaudeOpus45,
			)

			// Query
			a.Query(ctx, ArchiveQuery{Limit: 10})

			// Update resume
			if op%10 == 0 {
				a.SetCurrentTask(fmt.Sprintf("Task %d", op), "Objective", SourceModelClaudeOpus45)
				a.CompleteStep(fmt.Sprintf("Step %d", op))
			}
		}

		// Unregister
		a.UnregisterAgent(agentID)
	})

	stats := a.Stats()
	t.Logf("Final stats: %d entries", stats.TotalEntries)
	assert.True(t, stats.TotalEntries > 0, "Should have stored entries")
}
