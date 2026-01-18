package lifecycle

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/resources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "github.com/mattn/go-sqlite3"
)

// mockSessionBudget implements a minimal session budget for testing
type mockSessionBudget struct {
	sessionID string
	allocated atomic.Int64
	used      atomic.Int64
}

func (m *mockSessionBudget) SessionID() string { return m.sessionID }
func (m *mockSessionBudget) Allocated() int64  { return m.allocated.Load() }
func (m *mockSessionBudget) Used() int64       { return m.used.Load() }
func (m *mockSessionBudget) TryExpand(amount int64) bool {
	m.allocated.Add(amount)
	return true
}

// mockBudgetProvider implements FileHandleBudgetProvider for testing
type mockBudgetProvider struct {
	sessions sync.Map
	mu       sync.Mutex
}

func newMockBudgetProvider() *mockBudgetProvider {
	return &mockBudgetProvider{}
}

func (m *mockBudgetProvider) RegisterSession(sessionID string) *resources.SessionFileBudget {
	m.mu.Lock()
	defer m.mu.Unlock()

	// We can't create real SessionFileBudget without a parent, so we use a real budget
	cfg := resources.DefaultFileHandleBudgetConfig()
	budget := resources.NewFileHandleBudget(cfg)
	session := budget.RegisterSession(sessionID)
	m.sessions.Store(sessionID, session)
	return session
}

func (m *mockBudgetProvider) GetSession(sessionID string) *resources.SessionFileBudget {
	if val, ok := m.sessions.Load(sessionID); ok {
		return val.(*resources.SessionFileBudget)
	}
	return nil
}

func (m *mockBudgetProvider) RangeSessions(fn func(sessionID string, session *resources.SessionFileBudget) bool) {
	m.sessions.Range(func(key, value any) bool {
		return fn(key.(string), value.(*resources.SessionFileBudget))
	})
}

func (m *mockBudgetProvider) addSession(sessionID string, allocated, used int64) {
	cfg := resources.FileHandleBudgetConfig{
		GlobalLimit:   10000, // Large enough for test values
		ReservedRatio: 0.1,
	}
	budget := resources.NewFileHandleBudget(cfg)
	session := budget.RegisterSession(sessionID)
	session.TryExpand(allocated)
	// Simulate used handles by incrementing
	for i := int64(0); i < used; i++ {
		session.IncrementUsed()
	}
	m.sessions.Store(sessionID, session)
}

func createTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNewFileHandleBudgetPersistence(t *testing.T) {
	db := createTestDB(t)
	budget := newMockBudgetProvider()

	persistence, err := NewFileHandleBudgetPersistence(db, budget)
	require.NoError(t, err)
	assert.NotNil(t, persistence)

	// Verify table was created
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM file_handle_budget").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestNewFileHandleBudgetPersistence_TableAlreadyExists(t *testing.T) {
	db := createTestDB(t)
	budget := newMockBudgetProvider()

	// Create persistence twice - should not error
	_, err := NewFileHandleBudgetPersistence(db, budget)
	require.NoError(t, err)

	_, err = NewFileHandleBudgetPersistence(db, budget)
	require.NoError(t, err)
}

func TestSave_PersistsAllSessions(t *testing.T) {
	db := createTestDB(t)
	budget := newMockBudgetProvider()

	budget.addSession("session-1", 100, 50)
	budget.addSession("session-2", 200, 75)
	budget.addSession("session-3", 50, 10)

	persistence, err := NewFileHandleBudgetPersistence(db, budget)
	require.NoError(t, err)

	err = persistence.Save()
	require.NoError(t, err)

	// Verify all sessions were saved
	rows, err := persistence.LoadAllRows()
	require.NoError(t, err)
	assert.Len(t, rows, 3)

	// Create a map for easier verification
	rowMap := make(map[string]BudgetRow)
	for _, row := range rows {
		rowMap[row.SessionID] = row
	}

	assert.Equal(t, int64(100), rowMap["session-1"].Allocated)
	assert.Equal(t, int64(50), rowMap["session-1"].Used)
	assert.Equal(t, int64(200), rowMap["session-2"].Allocated)
	assert.Equal(t, int64(75), rowMap["session-2"].Used)
	assert.Equal(t, int64(50), rowMap["session-3"].Allocated)
	assert.Equal(t, int64(10), rowMap["session-3"].Used)
}

func TestSave_ClearsOldData(t *testing.T) {
	db := createTestDB(t)
	budget := newMockBudgetProvider()

	persistence, err := NewFileHandleBudgetPersistence(db, budget)
	require.NoError(t, err)

	// Save first set
	budget.addSession("session-1", 100, 50)
	err = persistence.Save()
	require.NoError(t, err)

	// Clear budget and add different session
	budget.sessions = sync.Map{}
	budget.addSession("session-2", 200, 75)
	err = persistence.Save()
	require.NoError(t, err)

	// Verify only session-2 exists
	rows, err := persistence.LoadAllRows()
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, "session-2", rows[0].SessionID)
}

func TestRestore_RecreatesSessions(t *testing.T) {
	db := createTestDB(t)

	// First, save some sessions with one budget
	budget1 := newMockBudgetProvider()
	budget1.addSession("session-1", 100, 50)
	budget1.addSession("session-2", 200, 75)

	persistence1, err := NewFileHandleBudgetPersistence(db, budget1)
	require.NoError(t, err)
	err = persistence1.Save()
	require.NoError(t, err)

	// Create new budget and restore
	budget2 := newMockBudgetProvider()
	persistence2, err := NewFileHandleBudgetPersistence(db, budget2)
	require.NoError(t, err)

	err = persistence2.Restore()
	require.NoError(t, err)

	// Verify sessions were restored
	session1 := budget2.GetSession("session-1")
	require.NotNil(t, session1)
	assert.Equal(t, int64(100), session1.Allocated())

	session2 := budget2.GetSession("session-2")
	require.NotNil(t, session2)
	assert.Equal(t, int64(200), session2.Allocated())
}

func TestRestore_EmptyDatabase(t *testing.T) {
	db := createTestDB(t)
	budget := newMockBudgetProvider()

	persistence, err := NewFileHandleBudgetPersistence(db, budget)
	require.NoError(t, err)

	err = persistence.Restore()
	require.NoError(t, err)

	// Verify no sessions exist
	var count int
	budget.RangeSessions(func(_ string, _ *resources.SessionFileBudget) bool {
		count++
		return true
	})
	assert.Equal(t, 0, count)
}

func TestPeriodicSave_RunsAtInterval(t *testing.T) {
	db := createTestDB(t)
	budget := newMockBudgetProvider()

	persistence, err := NewFileHandleBudgetPersistence(db, budget)
	require.NoError(t, err)

	budget.addSession("session-1", 100, 50)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		persistence.PeriodicSave(ctx, 50*time.Millisecond)
	}()

	// Wait for at least one save
	time.Sleep(100 * time.Millisecond)

	// Verify data was saved
	rows, err := persistence.LoadAllRows()
	require.NoError(t, err)
	assert.Len(t, rows, 1)

	cancel()
	wg.Wait()
}

func TestPeriodicSave_FinalSaveOnShutdown(t *testing.T) {
	db := createTestDB(t)
	budget := newMockBudgetProvider()

	// Add session BEFORE starting periodic save to avoid race
	budget.addSession("session-final", 999, 123)

	persistence, err := NewFileHandleBudgetPersistence(db, budget)
	require.NoError(t, err)

	// Verify nothing saved initially
	rows, err := persistence.LoadAllRows()
	require.NoError(t, err)
	assert.Empty(t, rows)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		persistence.PeriodicSave(ctx, time.Hour) // Long interval - won't trigger
	}()

	// Give the goroutine time to start
	time.Sleep(10 * time.Millisecond)

	// Cancel context - should trigger final save
	cancel()
	wg.Wait()

	// Verify final save occurred
	rows, err = persistence.LoadAllRows()
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, "session-final", rows[0].SessionID)
	assert.Equal(t, int64(999), rows[0].Allocated)
}

func TestSave_TransactionRollbackOnError(t *testing.T) {
	db := createTestDB(t)
	budget := newMockBudgetProvider()

	persistence, err := NewFileHandleBudgetPersistence(db, budget)
	require.NoError(t, err)

	// Save initial data
	budget.addSession("session-1", 100, 50)
	err = persistence.Save()
	require.NoError(t, err)

	// Close the database to cause an error
	db.Close()

	// Attempt to save - should fail
	budget.addSession("session-2", 200, 75)
	err = persistence.Save()
	assert.Error(t, err)
}

func TestSave_ConcurrentCalls(t *testing.T) {
	db := createTestDB(t)
	budget := newMockBudgetProvider()

	persistence, err := NewFileHandleBudgetPersistence(db, budget)
	require.NoError(t, err)

	budget.addSession("session-1", 100, 50)

	var wg sync.WaitGroup
	numGoroutines := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = persistence.Save()
		}()
	}

	wg.Wait()

	// Verify data is consistent after concurrent saves
	rows, err := persistence.LoadAllRows()
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, "session-1", rows[0].SessionID)
}

func TestSave_UpdatesTimestamp(t *testing.T) {
	db := createTestDB(t)
	budget := newMockBudgetProvider()

	persistence, err := NewFileHandleBudgetPersistence(db, budget)
	require.NoError(t, err)

	budget.addSession("session-1", 100, 50)

	beforeSave := time.Now()
	err = persistence.Save()
	require.NoError(t, err)
	afterSave := time.Now()

	rows, err := persistence.LoadAllRows()
	require.NoError(t, err)
	require.Len(t, rows, 1)

	// Parse the timestamp
	savedTime, err := time.Parse(time.RFC3339, rows[0].UpdatedAt)
	require.NoError(t, err)

	// Verify timestamp is within expected range
	assert.True(t, savedTime.After(beforeSave.Add(-time.Second)))
	assert.True(t, savedTime.Before(afterSave.Add(time.Second)))
}

func TestLoadAllRows_EmptyTable(t *testing.T) {
	db := createTestDB(t)
	budget := newMockBudgetProvider()

	persistence, err := NewFileHandleBudgetPersistence(db, budget)
	require.NoError(t, err)

	rows, err := persistence.LoadAllRows()
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestSave_NoSessions(t *testing.T) {
	db := createTestDB(t)
	budget := newMockBudgetProvider()

	persistence, err := NewFileHandleBudgetPersistence(db, budget)
	require.NoError(t, err)

	err = persistence.Save()
	require.NoError(t, err)

	rows, err := persistence.LoadAllRows()
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestRestore_DoesNotCreateDuplicates(t *testing.T) {
	db := createTestDB(t)

	// Save a session
	budget1 := newMockBudgetProvider()
	budget1.addSession("session-1", 100, 50)

	persistence1, err := NewFileHandleBudgetPersistence(db, budget1)
	require.NoError(t, err)
	err = persistence1.Save()
	require.NoError(t, err)

	// Restore twice on same budget
	budget2 := newMockBudgetProvider()
	persistence2, err := NewFileHandleBudgetPersistence(db, budget2)
	require.NoError(t, err)

	err = persistence2.Restore()
	require.NoError(t, err)

	err = persistence2.Restore()
	require.NoError(t, err)

	// Verify only one session exists
	var count int
	budget2.RangeSessions(func(_ string, _ *resources.SessionFileBudget) bool {
		count++
		return true
	})
	assert.Equal(t, 1, count)
}
