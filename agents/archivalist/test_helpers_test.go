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

var (
	Assert  = assert.New
	Require = require.New
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(StoreConfig{
		TokenThreshold: 100000,
	})
}

func newTestStoreWithArchive(t *testing.T) (*Store, *SessionArchives) {
	t.Helper()
	tmpDir := t.TempDir()
	archives := NewSessionArchives(SessionArchivesConfig{WorkDir: tmpDir})
	t.Cleanup(func() { archives.Close() })

	store := NewStore(StoreConfig{
		TokenThreshold: 100000,
		Archive:        archives,
	})
	return store, archives
}

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	return NewRegistry(RegistryConfig{
		IdleTimeout:     100 * time.Millisecond,
		InactiveTimeout: 500 * time.Millisecond,
	})
}

func newTestEventLog(t *testing.T) *EventLog {
	t.Helper()
	el := NewEventLog(EventLogConfig{
		MaxEvents: 1000,
	})
	t.Cleanup(func() { el.Close() })
	return el
}

func newTestAgentContext(t *testing.T) *AgentContext {
	t.Helper()
	return NewAgentContext()
}

func newTestArchivalist(t *testing.T) *Archivalist {
	t.Helper()
	a, err := New(context.Background(), Config{
		Factory:       newTestFactory(t),
		EnableArchive: false,
		EnableRAG:     false,
	})
	if err != nil {
		t.Fatalf("failed to create archivalist: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func newTestArchivalistWithRAG(t *testing.T) *Archivalist {
	t.Helper()
	tmpDir := t.TempDir()
	a, err := New(context.Background(), Config{
		Factory:        newTestFactory(t),
		EnableArchive:  true,
		EnableRAG:      true,
		ArchivePath:    tmpDir + "/archive.db",
		EmbeddingsPath: tmpDir + "/embeddings.db",
	})
	if err != nil {
		t.Fatalf("failed to create archivalist with RAG: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func makeEntry(category Category, content string, source SourceModel) *Entry {
	return &Entry{
		Category: category,
		Content:  content,
		Source:   source,
		Metadata: make(map[string]any),
	}
}

func makeEntryWithTitle(category Category, title, content string, source SourceModel) *Entry {
	e := makeEntry(category, content, source)
	e.Title = title
	return e
}

func makeTaskStateEntry(objective string) *Entry {
	return makeEntry(CategoryTaskState, objective, SourceModelClaudeOpus)
}

func makeDecisionEntry(choice, rationale string) *Entry {
	e := makeEntryWithTitle(CategoryDecision, choice, rationale, SourceModelClaudeOpus)
	e.Metadata["rationale"] = rationale
	return e
}

func makeIssueEntry(problem string, resolved bool) *Entry {
	e := makeEntry(CategoryIssue, problem, SourceModelClaudeOpus)
	e.Metadata["resolved"] = resolved
	return e
}

func makeEvent(eventType EventType, agentID, sessionID string, data map[string]any) *Event {
	return &Event{
		Type:      eventType,
		AgentID:   agentID,
		SessionID: sessionID,
		Data:      data,
	}
}

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

func runConcurrentWithErrors(t *testing.T, n int, fn func(i int) error) []error {
	t.Helper()
	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			if err := fn(idx); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	return errs
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func pastTime(d time.Duration) time.Time {
	return time.Now().Add(-d)
}

func futureTime(d time.Duration) time.Time {
	return time.Now().Add(d)
}

func generateEntries(n int, category Category, source SourceModel) []*Entry {
	entries := make([]*Entry, n)
	for i := 0; i < n; i++ {
		entries[i] = makeEntry(category, fmt.Sprintf("Content %d", i), source)
	}
	return entries
}

func generateAgents(t *testing.T, registry *Registry, n int, sessionID string) []*RegisteredAgent {
	t.Helper()
	agents := make([]*RegisteredAgent, n)
	for i := 0; i < n; i++ {
		agent, err := registry.Register(
			fmt.Sprintf("agent-%d", i),
			sessionID,
			"",
			SourceModelClaudeOpus,
		)
		if err != nil {
			t.Fatalf("failed to register agent %d: %v", i, err)
		}
		agents[i] = agent
	}
	return agents
}

func generateEvents(n int, eventType EventType, sessionID string) []*Event {
	events := make([]*Event, n)
	for i := 0; i < n; i++ {
		events[i] = makeEvent(eventType, fmt.Sprintf("agent-%d", i%10), sessionID, nil)
	}
	return events
}
