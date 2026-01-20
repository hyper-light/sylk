// Package hooks provides concurrent execution tests for hook slice operations.
// These tests verify W3C.6 fix for data race in excerpt slice injection.
package hooks

import (
	"context"
	"sync"
	"testing"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
)

// =============================================================================
// Concurrent Execution Tests for W3C.6 Fix
//
// The slice copy pattern fix prevents corruption when multiple hooks prepend
// to the same slice. These tests verify:
// 1. Individual hook executions with shared base content don't corrupt data
// 2. Multiple hooks can run concurrently without panics
// 3. The copy pattern produces correct results
// =============================================================================

// TestConcurrentFailureHookExecution verifies that multiple concurrent executions
// of the failure hook do not cause data races or corruption.
func TestConcurrentFailureHookExecution(t *testing.T) {
	const numGoroutines = 100

	querier := &MockFailureQuerier{}
	querier.SetFailures([]FailurePattern{
		{
			ApproachSignature: "test pattern",
			ErrorPattern:      "test error",
			RecurrenceCount:   5,
			Similarity:        0.9,
		},
	})

	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier: querier,
	})

	// Base excerpts that will be copied for each goroutine
	baseExcerpts := []ctxpkg.Excerpt{
		{Source: "original", Content: "original content", Confidence: 0.5},
	}

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	results := make(chan *ctxpkg.AugmentedQuery, numGoroutines)
	errors := make(chan error, numGoroutines)

	// Run multiple hooks concurrently, each with its own AugmentedQuery copy
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			// Each goroutine gets its own copy of excerpts
			excerptsCopy := make([]ctxpkg.Excerpt, len(baseExcerpts))
			copy(excerptsCopy, baseExcerpts)

			localQuery := &ctxpkg.AugmentedQuery{
				OriginalQuery: "test query",
				Excerpts:      excerptsCopy,
				Summaries:     make([]ctxpkg.Summary, 0),
			}

			data := &ctxpkg.PromptHookData{
				SessionID:         "test-session",
				AgentID:           "test-agent",
				AgentType:         "engineer",
				TurnNumber:        id,
				Query:             "implement authentication",
				Timestamp:         time.Now(),
				PrefetchedContent: localQuery,
			}

			ctx := context.Background()
			_, err := hook.Execute(ctx, data)
			if err != nil {
				errors <- err
				return
			}

			results <- data.PrefetchedContent
		}(i)
	}

	wg.Wait()
	close(results)
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Hook execution error: %v", err)
	}

	// Verify all results have the expected structure
	resultCount := 0
	for result := range results {
		resultCount++
		if result == nil {
			t.Error("Result should not be nil")
			continue
		}
		// Each result should have warning + original = 2 excerpts
		if len(result.Excerpts) != 2 {
			t.Errorf("Expected 2 excerpts (warning + original), got %d", len(result.Excerpts))
		}
		// First excerpt should be the warning
		if len(result.Excerpts) > 0 && result.Excerpts[0].Source != "failure_memory" {
			t.Errorf("First excerpt should be from failure_memory, got %s", result.Excerpts[0].Source)
		}
	}

	if resultCount != numGoroutines {
		t.Errorf("Expected %d results, got %d", numGoroutines, resultCount)
	}
}

// TestConcurrentGuideRoutingHookExecution verifies that multiple concurrent
// executions of the guide routing hook do not cause data races.
func TestConcurrentGuideRoutingHookExecution(t *testing.T) {
	const numGoroutines = 100

	provider := NewMockRoutingProvider()
	provider.SetRecentRoutings([]RoutingDecision{
		{
			MessageID:   "msg-1",
			TargetAgent: "librarian",
			Confidence:  0.9,
			Reason:      "code query",
			Timestamp:   time.Now(),
		},
	})

	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: provider,
	})

	baseExcerpts := []ctxpkg.Excerpt{
		{Source: "original", Content: "original content", Confidence: 0.5},
	}

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	results := make(chan *ctxpkg.AugmentedQuery, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			excerptsCopy := make([]ctxpkg.Excerpt, len(baseExcerpts))
			copy(excerptsCopy, baseExcerpts)

			localQuery := &ctxpkg.AugmentedQuery{
				OriginalQuery: "test query",
				Excerpts:      excerptsCopy,
				Summaries:     make([]ctxpkg.Summary, 0),
			}

			data := &ctxpkg.PromptHookData{
				SessionID:         "test-session",
				AgentID:           "test-agent",
				AgentType:         "guide",
				TurnNumber:        id,
				Query:             "how do I fix this?",
				Timestamp:         time.Now(),
				PrefetchedContent: localQuery,
			}

			ctx := context.Background()
			_, _ = hook.Execute(ctx, data)
			results <- data.PrefetchedContent
		}(i)
	}

	wg.Wait()
	close(results)

	// Verify all results
	for result := range results {
		if result == nil {
			t.Error("Result should not be nil")
			continue
		}
		if len(result.Excerpts) != 2 {
			t.Errorf("Expected 2 excerpts, got %d", len(result.Excerpts))
		}
		if len(result.Excerpts) > 0 && result.Excerpts[0].Source != "routing_history" {
			t.Errorf("First excerpt should be routing_history, got %s", result.Excerpts[0].Source)
		}
	}
}

// TestConcurrentWorkflowContextHookExecution verifies that multiple concurrent
// executions of the workflow context hook do not cause data races.
func TestConcurrentWorkflowContextHookExecution(t *testing.T) {
	const numGoroutines = 100

	provider := NewMockWorkflowProvider()
	provider.SetWorkflowContext(&WorkflowContext{
		CurrentWorkflow: &WorkflowState{
			ID:        "wf-123",
			Name:      "Test Workflow",
			Phase:     "executing",
			Progress:  0.5,
			StartTime: time.Now().Add(-10 * time.Minute),
		},
		PendingTasks: []OrchestratorTask{
			{ID: "task-1", Description: "Test task", Status: "pending"},
		},
	})

	hook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: provider,
	})

	baseExcerpts := []ctxpkg.Excerpt{
		{Source: "original", Content: "original content", Confidence: 0.5},
	}

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	results := make(chan *ctxpkg.AugmentedQuery, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			excerptsCopy := make([]ctxpkg.Excerpt, len(baseExcerpts))
			copy(excerptsCopy, baseExcerpts)

			localQuery := &ctxpkg.AugmentedQuery{
				OriginalQuery: "test query",
				Excerpts:      excerptsCopy,
				Summaries:     make([]ctxpkg.Summary, 0),
			}

			data := &ctxpkg.PromptHookData{
				SessionID:         "test-session",
				AgentID:           "test-agent",
				AgentType:         "orchestrator",
				TurnNumber:        id,
				Query:             "coordinate agents",
				Timestamp:         time.Now(),
				PrefetchedContent: localQuery,
			}

			ctx := context.Background()
			_, _ = hook.Execute(ctx, data)
			results <- data.PrefetchedContent
		}(i)
	}

	wg.Wait()
	close(results)

	for result := range results {
		if result == nil {
			t.Error("Result should not be nil")
			continue
		}
		if len(result.Excerpts) != 2 {
			t.Errorf("Expected 2 excerpts, got %d", len(result.Excerpts))
		}
		if len(result.Excerpts) > 0 && result.Excerpts[0].Source != "workflow_context" {
			t.Errorf("First excerpt should be workflow_context, got %s", result.Excerpts[0].Source)
		}
	}
}

// TestConcurrentMixedHooksExecution verifies that multiple different hooks
// executing concurrently on their own data do not interfere with each other.
func TestConcurrentMixedHooksExecution(t *testing.T) {
	const numGoroutines = 30 // 30 per hook type = 90 total

	// Setup failure hook
	failureQuerier := &MockFailureQuerier{}
	failureQuerier.SetFailures([]FailurePattern{
		{ApproachSignature: "test", RecurrenceCount: 5, Similarity: 0.9},
	})
	failureHook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier: failureQuerier,
	})

	// Setup routing hook
	routingProvider := NewMockRoutingProvider()
	routingProvider.SetRecentRoutings([]RoutingDecision{
		{TargetAgent: "librarian", Confidence: 0.9},
	})
	routingHook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: routingProvider,
	})

	// Setup workflow hook
	workflowProvider := NewMockWorkflowProvider()
	workflowProvider.SetWorkflowContext(&WorkflowContext{
		CurrentWorkflow: &WorkflowState{ID: "wf-1", Name: "Test", Phase: "executing", Progress: 0.5},
	})
	workflowHook := NewWorkflowContextHook(WorkflowContextHookConfig{
		WorkflowProvider: workflowProvider,
	})

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 3)

	failureResults := make(chan int, numGoroutines)
	routingResults := make(chan int, numGoroutines)
	workflowResults := make(chan int, numGoroutines)

	// Run failure hooks
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			localQuery := &ctxpkg.AugmentedQuery{
				Excerpts:  []ctxpkg.Excerpt{{Source: "base"}},
				Summaries: make([]ctxpkg.Summary, 0),
			}
			data := &ctxpkg.PromptHookData{
				AgentType:         "engineer",
				Query:             "test",
				PrefetchedContent: localQuery,
			}
			ctx := context.Background()
			_, _ = failureHook.Execute(ctx, data)
			failureResults <- len(data.PrefetchedContent.Excerpts)
		}(i)
	}

	// Run routing hooks
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			localQuery := &ctxpkg.AugmentedQuery{
				Excerpts:  []ctxpkg.Excerpt{{Source: "base"}},
				Summaries: make([]ctxpkg.Summary, 0),
			}
			data := &ctxpkg.PromptHookData{
				AgentType:         "guide",
				Query:             "test",
				PrefetchedContent: localQuery,
			}
			ctx := context.Background()
			_, _ = routingHook.Execute(ctx, data)
			routingResults <- len(data.PrefetchedContent.Excerpts)
		}(i)
	}

	// Run workflow hooks
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			localQuery := &ctxpkg.AugmentedQuery{
				Excerpts:  []ctxpkg.Excerpt{{Source: "base"}},
				Summaries: make([]ctxpkg.Summary, 0),
			}
			data := &ctxpkg.PromptHookData{
				AgentType:         "orchestrator",
				Query:             "test",
				PrefetchedContent: localQuery,
			}
			ctx := context.Background()
			_, _ = workflowHook.Execute(ctx, data)
			workflowResults <- len(data.PrefetchedContent.Excerpts)
		}(i)
	}

	wg.Wait()
	close(failureResults)
	close(routingResults)
	close(workflowResults)

	// Verify each hook produced correct results
	for count := range failureResults {
		if count != 2 {
			t.Errorf("Failure hook: expected 2 excerpts, got %d", count)
		}
	}
	for count := range routingResults {
		if count != 2 {
			t.Errorf("Routing hook: expected 2 excerpts, got %d", count)
		}
	}
	for count := range workflowResults {
		if count != 2 {
			t.Errorf("Workflow hook: expected 2 excerpts, got %d", count)
		}
	}
}

// TestSliceCopyPatternIntegrity verifies that the slice copy pattern
// correctly preserves original data and prepends new excerpt.
func TestSliceCopyPatternIntegrity(t *testing.T) {
	const numIterations = 100

	querier := &MockFailureQuerier{}
	querier.SetFailures([]FailurePattern{
		{ApproachSignature: "pattern", RecurrenceCount: 5, Similarity: 0.9},
	})

	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier: querier,
	})

	var wg sync.WaitGroup
	wg.Add(numIterations)

	type result struct {
		id            int
		excerptCount  int
		firstSource   string
		secondSource  string
		secondContent string
	}
	results := make(chan result, numIterations)

	for i := 0; i < numIterations; i++ {
		go func(id int) {
			defer wg.Done()

			// Create unique content for each iteration
			originalContent := "original content"

			localQuery := &ctxpkg.AugmentedQuery{
				OriginalQuery: "test",
				Excerpts: []ctxpkg.Excerpt{
					{Source: "initial", Content: originalContent, Confidence: 0.5},
				},
				Summaries: make([]ctxpkg.Summary, 0),
			}

			data := &ctxpkg.PromptHookData{
				AgentType:         "engineer",
				Query:             "test query",
				TurnNumber:        id,
				PrefetchedContent: localQuery,
			}

			ctx := context.Background()
			_, _ = hook.Execute(ctx, data)

			r := result{
				id:           id,
				excerptCount: len(data.PrefetchedContent.Excerpts),
			}
			if len(data.PrefetchedContent.Excerpts) > 0 {
				r.firstSource = data.PrefetchedContent.Excerpts[0].Source
			}
			if len(data.PrefetchedContent.Excerpts) > 1 {
				r.secondSource = data.PrefetchedContent.Excerpts[1].Source
				r.secondContent = data.PrefetchedContent.Excerpts[1].Content
			}
			results <- r
		}(i)
	}

	wg.Wait()
	close(results)

	// Verify all results maintain data integrity
	for r := range results {
		if r.excerptCount != 2 {
			t.Errorf("Iteration %d: expected 2 excerpts, got %d", r.id, r.excerptCount)
			continue
		}

		// First should be the prepended warning
		if r.firstSource != "failure_memory" {
			t.Errorf("Iteration %d: first source should be failure_memory, got %s", r.id, r.firstSource)
		}

		// Second should be the original
		if r.secondSource != "initial" {
			t.Errorf("Iteration %d: second source should be initial, got %s", r.id, r.secondSource)
		}

		// Original content should be preserved
		if r.secondContent != "original content" {
			t.Errorf("Iteration %d: original content corrupted, got %s", r.id, r.secondContent)
		}
	}
}

// TestHookExecutionWithNilPrefetchedContent verifies that hooks correctly
// create a new AugmentedQuery when PrefetchedContent is nil.
func TestHookExecutionWithNilPrefetchedContent(t *testing.T) {
	const numGoroutines = 50

	querier := &MockFailureQuerier{}
	querier.SetFailures([]FailurePattern{
		{ApproachSignature: "test", RecurrenceCount: 5, Similarity: 0.9},
	})

	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier: querier,
	})

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	results := make(chan *ctxpkg.AugmentedQuery, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			data := &ctxpkg.PromptHookData{
				SessionID:         "test-session",
				AgentID:           "test-agent",
				AgentType:         "engineer",
				TurnNumber:        id,
				Query:             "test",
				Timestamp:         time.Now(),
				PrefetchedContent: nil, // Explicitly nil
			}

			ctx := context.Background()
			_, _ = hook.Execute(ctx, data)
			results <- data.PrefetchedContent
		}(i)
	}

	wg.Wait()
	close(results)

	for result := range results {
		if result == nil {
			t.Error("PrefetchedContent should have been created")
			continue
		}
		if len(result.Excerpts) != 1 {
			t.Errorf("Expected 1 excerpt (warning only), got %d", len(result.Excerpts))
		}
		if len(result.Excerpts) > 0 && result.Excerpts[0].Source != "failure_memory" {
			t.Errorf("Excerpt should be from failure_memory, got %s", result.Excerpts[0].Source)
		}
	}
}
