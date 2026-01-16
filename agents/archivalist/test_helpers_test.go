package archivalist

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Re-export testify functions for convenience
var (
	// Assert functions (non-fatal)
	Assert = assert.New
	// Require functions (fatal)
	Require = require.New
)

// =============================================================================
// Test Fixtures & Factories
// =============================================================================

// newTestStore creates a Store for testing without archive
func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(StoreConfig{
		TokenThreshold: 100000, // Lower threshold for testing
	})
}

// newTestStoreWithArchive creates a Store with a temp archive for testing
func newTestStoreWithArchive(t *testing.T) (*Store, *Archive) {
	t.Helper()
	tmpDir := t.TempDir()
	archive, err := NewArchive(ArchiveConfig{Path: tmpDir + "/test.db"})
	if err != nil {
		t.Fatalf("failed to create archive: %v", err)
	}
	t.Cleanup(func() { archive.Close() })

	store := NewStore(StoreConfig{
		TokenThreshold: 100000,
		Archive:        archive,
	})
	return store, archive
}

// newTestRegistry creates a Registry for testing
func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	return NewRegistry(RegistryConfig{
		IdleTimeout:     100 * time.Millisecond,
		InactiveTimeout: 500 * time.Millisecond,
	})
}

// newTestEventLog creates an EventLog for testing
func newTestEventLog(t *testing.T) *EventLog {
	t.Helper()
	el := NewEventLog(EventLogConfig{
		MaxEvents: 1000,
	})
	t.Cleanup(func() { el.Close() })
	return el
}

// newTestAgentContext creates an AgentContext for testing
func newTestAgentContext(t *testing.T) *AgentContext {
	t.Helper()
	return NewAgentContext()
}

// newTestArchivalist creates an Archivalist for testing without Anthropic calls
func newTestArchivalist(t *testing.T) *Archivalist {
	t.Helper()
	a, err := New(Config{
		AnthropicAPIKey: "test-key", // Won't be used
		EnableArchive:   false,
		EnableRAG:       false,
	})
	if err != nil {
		t.Fatalf("failed to create archivalist: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

// newTestArchivalistWithRAG creates an Archivalist with RAG enabled
func newTestArchivalistWithRAG(t *testing.T) *Archivalist {
	t.Helper()
	tmpDir := t.TempDir()
	a, err := New(Config{
		AnthropicAPIKey: "test-key",
		EnableArchive:   true,
		EnableRAG:       true,
		ArchivePath:     tmpDir + "/archive.db",
		EmbeddingsPath:  tmpDir + "/embeddings.db",
	})
	if err != nil {
		t.Fatalf("failed to create archivalist with RAG: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

// =============================================================================
// Entry Factories
// =============================================================================

// makeEntry creates a test entry with defaults
func makeEntry(category Category, content string, source SourceModel) *Entry {
	return &Entry{
		Category: category,
		Content:  content,
		Source:   source,
		Metadata: make(map[string]any),
	}
}

// makeEntryWithTitle creates a test entry with title
func makeEntryWithTitle(category Category, title, content string, source SourceModel) *Entry {
	e := makeEntry(category, content, source)
	e.Title = title
	return e
}

// makeTaskStateEntry creates a test task state entry
func makeTaskStateEntry(objective string) *Entry {
	return makeEntry(CategoryTaskState, objective, SourceModelClaudeOpus45)
}

// makeDecisionEntry creates a test decision entry
func makeDecisionEntry(choice, rationale string) *Entry {
	e := makeEntryWithTitle(CategoryDecision, choice, rationale, SourceModelClaudeOpus45)
	e.Metadata["rationale"] = rationale
	return e
}

// makeIssueEntry creates a test issue entry
func makeIssueEntry(problem string, resolved bool) *Entry {
	e := makeEntry(CategoryIssue, problem, SourceModelClaudeOpus45)
	e.Metadata["resolved"] = resolved
	return e
}

// =============================================================================
// Event Factories
// =============================================================================

// makeEvent creates a test event
func makeEvent(eventType EventType, agentID, sessionID string, data map[string]any) *Event {
	return &Event{
		Type:      eventType,
		AgentID:   agentID,
		SessionID: sessionID,
		Data:      data,
	}
}

// =============================================================================
// Concurrency Helpers
// =============================================================================

// runConcurrent runs a function n times concurrently and waits for completion
func runConcurrent(t *testing.T, n int, fn func(i int)) {
	t.Helper()
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			fn(idx)
		}(i)
	}
	wg.Wait()
}

// runConcurrentWithErrors runs a function n times and collects errors
func runConcurrentWithErrors(t *testing.T, n int, fn func(i int) error) []error {
	t.Helper()
	var mu sync.Mutex
	var errors []error
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			if err := fn(idx); err != nil {
				mu.Lock()
				errors = append(errors, err)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	return errors
}

// =============================================================================
// Time Helpers
// =============================================================================

// timePtr returns a pointer to a time.Time
func timePtr(t time.Time) *time.Time {
	return &t
}

// pastTime returns a time in the past
func pastTime(d time.Duration) time.Time {
	return time.Now().Add(-d)
}

// futureTime returns a time in the future
func futureTime(d time.Duration) time.Time {
	return time.Now().Add(d)
}

// =============================================================================
// Scale Test Helpers
// =============================================================================

// generateEntries creates n test entries
func generateEntries(n int, category Category, source SourceModel) []*Entry {
	entries := make([]*Entry, n)
	for i := 0; i < n; i++ {
		entries[i] = makeEntry(category, fmt.Sprintf("Content %d", i), source)
	}
	return entries
}

// generateAgents registers n agents in a registry
func generateAgents(t *testing.T, registry *Registry, n int, sessionID string) []*RegisteredAgent {
	t.Helper()
	agents := make([]*RegisteredAgent, n)
	for i := 0; i < n; i++ {
		agent, err := registry.Register(
			fmt.Sprintf("agent-%d", i),
			sessionID,
			"",
			SourceModelClaudeOpus45,
		)
		if err != nil {
			t.Fatalf("failed to register agent %d: %v", i, err)
		}
		agents[i] = agent
	}
	return agents
}

// generateEvents creates n test events
func generateEvents(n int, eventType EventType, sessionID string) []*Event {
	events := make([]*Event, n)
	for i := 0; i < n; i++ {
		events[i] = makeEvent(eventType, fmt.Sprintf("agent-%d", i%10), sessionID, nil)
	}
	return events
}

// =============================================================================
// Context Helpers
// =============================================================================

// testContext returns a context with a reasonable timeout for tests
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// shortContext returns a context with a short timeout
func shortContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}
