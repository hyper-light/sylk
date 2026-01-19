package resource

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Mock FileHandleBudget
// =============================================================================

type mockFileHandleBudget struct {
	mu          sync.Mutex
	allocated   atomic.Int64
	used        atomic.Int64
	globalLimit int64
	allocateErr error
}

func newMockFileHandleBudget(limit int64) *mockFileHandleBudget {
	return &mockFileHandleBudget{
		globalLimit: limit,
	}
}

func (m *mockFileHandleBudget) TryAllocate(amount int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.allocateErr != nil {
		return false
	}
	if m.allocated.Load()+amount > m.globalLimit {
		return false
	}
	m.allocated.Add(amount)
	return true
}

func (m *mockFileHandleBudget) Deallocate(amount int64) {
	m.allocated.Add(-amount)
}

func (m *mockFileHandleBudget) IncrementUsed() {
	m.used.Add(1)
}

func (m *mockFileHandleBudget) DecrementUsed() {
	m.used.Add(-1)
}

func (m *mockFileHandleBudget) Available() int64 {
	return m.globalLimit - m.allocated.Load()
}

func (m *mockFileHandleBudget) GlobalLimit() int64 {
	return m.globalLimit
}

// =============================================================================
// NewSearchFileHandleBudget Tests
// =============================================================================

func TestNewSearchFileHandleBudget_DefaultConfig(t *testing.T) {
	cfg := DefaultSearchFileHandleBudgetConfig()
	cfg.Budget = newMockFileHandleBudget(100)

	sfb, err := NewSearchFileHandleBudget(cfg)
	require.NoError(t, err)
	require.NotNil(t, sfb)

	assert.Equal(t, int32(4), sfb.bleveHandles)
	assert.Equal(t, int32(2), sfb.sqliteHandles)
	assert.Equal(t, int32(32), sfb.maxRead)
	assert.Equal(t, int32(16), sfb.maxIndex)
}

func TestNewSearchFileHandleBudget_CustomConfig(t *testing.T) {
	cfg := SearchFileHandleBudgetConfig{
		Budget:          newMockFileHandleBudget(200),
		BleveHandles:    8,
		SQLiteHandles:   4,
		MaxReadHandles:  64,
		MaxIndexHandles: 32,
	}

	sfb, err := NewSearchFileHandleBudget(cfg)
	require.NoError(t, err)
	require.NotNil(t, sfb)

	assert.Equal(t, int32(8), sfb.bleveHandles)
	assert.Equal(t, int32(4), sfb.sqliteHandles)
	assert.Equal(t, int32(64), sfb.maxRead)
	assert.Equal(t, int32(32), sfb.maxIndex)
}

func TestNewSearchFileHandleBudget_NilBudget(t *testing.T) {
	cfg := SearchFileHandleBudgetConfig{
		Budget: nil,
	}

	sfb, err := NewSearchFileHandleBudget(cfg)
	require.NoError(t, err)
	require.NotNil(t, sfb)
}

func TestNewSearchFileHandleBudget_ReservesPersistentHandles(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	cfg := SearchFileHandleBudgetConfig{
		Budget:        mock,
		BleveHandles:  4,
		SQLiteHandles: 2,
	}

	sfb, err := NewSearchFileHandleBudget(cfg)
	require.NoError(t, err)

	// 4 + 2 = 6 persistent handles should be reserved
	assert.Equal(t, int64(6), mock.allocated.Load())
	assert.Equal(t, int32(6), sfb.activePersistent.Load())
}

func TestNewSearchFileHandleBudget_InsufficientForPersistent(t *testing.T) {
	mock := newMockFileHandleBudget(3) // Too small
	cfg := SearchFileHandleBudgetConfig{
		Budget:        mock,
		BleveHandles:  4,
		SQLiteHandles: 2,
	}

	_, err := NewSearchFileHandleBudget(cfg)
	assert.ErrorIs(t, err, ErrFileHandleExhausted)
}

// =============================================================================
// AcquireForRead Tests
// =============================================================================

func TestAcquireForRead_Success(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:         mock,
		MaxReadHandles: 10,
	})

	ctx := context.Background()
	release, err := sfb.AcquireForRead(ctx, "/path/to/file.go")

	require.NoError(t, err)
	require.NotNil(t, release)
	assert.Equal(t, int32(1), sfb.ActiveRead())
	assert.True(t, sfb.IsPathTracked("/path/to/file.go"))

	release()
	assert.Equal(t, int32(0), sfb.ActiveRead())
	assert.False(t, sfb.IsPathTracked("/path/to/file.go"))
}

func TestAcquireForRead_ExceedsLimit(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:         mock,
		MaxReadHandles: 2,
	})

	ctx := context.Background()

	r1, _ := sfb.AcquireForRead(ctx, "/file1.go")
	r2, _ := sfb.AcquireForRead(ctx, "/file2.go")

	_, err := sfb.AcquireForRead(ctx, "/file3.go")
	assert.ErrorIs(t, err, ErrFileHandleExhausted)

	r1()
	r2()
}

func TestAcquireForRead_ContextCancellation(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:         mock,
		MaxReadHandles: 10,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := sfb.AcquireForRead(ctx, "/path/to/file.go")
	assert.Error(t, err)
}

func TestAcquireForRead_DoubleRelease(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:         mock,
		MaxReadHandles: 10,
	})

	ctx := context.Background()
	release, _ := sfb.AcquireForRead(ctx, "/file.go")

	release()
	release() // Should be safe

	assert.Equal(t, int32(0), sfb.ActiveRead())
}

func TestAcquireForRead_GlobalBudgetExhausted(t *testing.T) {
	mock := newMockFileHandleBudget(10)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:         mock,
		BleveHandles:   3,
		SQLiteHandles:  2,
		MaxReadHandles: 100,
	})

	ctx := context.Background()

	// Fill up remaining budget (10 - 5 persistent = 5 remaining)
	var releases []func()
	for i := 0; i < 5; i++ {
		r, err := sfb.AcquireForRead(ctx, "/file"+string(rune('a'+i))+".go")
		require.NoError(t, err)
		releases = append(releases, r)
	}

	// Next should fail
	_, err := sfb.AcquireForRead(ctx, "/overflow.go")
	assert.ErrorIs(t, err, ErrFileHandleExhausted)

	for _, r := range releases {
		r()
	}
}

// =============================================================================
// TryAcquireForRead Tests
// =============================================================================

func TestTryAcquireForRead_Success(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:         mock,
		MaxReadHandles: 10,
	})

	release, ok := sfb.TryAcquireForRead("/path/to/file.go")

	assert.True(t, ok)
	require.NotNil(t, release)
	assert.Equal(t, int32(1), sfb.ActiveRead())

	release()
	assert.Equal(t, int32(0), sfb.ActiveRead())
}

func TestTryAcquireForRead_Failure(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:         mock,
		MaxReadHandles: 1,
	})

	r1, ok := sfb.TryAcquireForRead("/file1.go")
	require.True(t, ok)

	_, ok = sfb.TryAcquireForRead("/file2.go")
	assert.False(t, ok)

	r1()
}

func TestTryAcquireForRead_Closed(t *testing.T) {
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget: newMockFileHandleBudget(100),
	})

	sfb.Close()

	_, ok := sfb.TryAcquireForRead("/file.go")
	assert.False(t, ok)
}

// =============================================================================
// AcquireForIndex Tests
// =============================================================================

func TestAcquireForIndex_Success(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:          mock,
		MaxIndexHandles: 10,
	})

	ctx := context.Background()
	release, err := sfb.AcquireForIndex(ctx, "/path/to/file.go")

	require.NoError(t, err)
	require.NotNil(t, release)
	assert.Equal(t, int32(1), sfb.ActiveIndex())
	assert.True(t, sfb.IsPathTracked("/path/to/file.go"))

	handleType, ok := sfb.GetPathType("/path/to/file.go")
	assert.True(t, ok)
	assert.Equal(t, HandleTypeIndex, handleType)

	release()
	assert.Equal(t, int32(0), sfb.ActiveIndex())
}

func TestAcquireForIndex_ExceedsLimit(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:          mock,
		MaxIndexHandles: 2,
	})

	ctx := context.Background()

	r1, _ := sfb.AcquireForIndex(ctx, "/file1.go")
	r2, _ := sfb.AcquireForIndex(ctx, "/file2.go")

	_, err := sfb.AcquireForIndex(ctx, "/file3.go")
	assert.ErrorIs(t, err, ErrFileHandleExhausted)

	r1()
	r2()
}

// =============================================================================
// TryAcquireForIndex Tests
// =============================================================================

func TestTryAcquireForIndex_Success(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:          mock,
		MaxIndexHandles: 10,
	})

	release, ok := sfb.TryAcquireForIndex("/path/to/file.go")

	assert.True(t, ok)
	require.NotNil(t, release)
	assert.Equal(t, int32(1), sfb.ActiveIndex())

	release()
	assert.Equal(t, int32(0), sfb.ActiveIndex())
}

func TestTryAcquireForIndex_Failure(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:          mock,
		MaxIndexHandles: 1,
	})

	r1, ok := sfb.TryAcquireForIndex("/file1.go")
	require.True(t, ok)

	_, ok = sfb.TryAcquireForIndex("/file2.go")
	assert.False(t, ok)

	r1()
}

// =============================================================================
// Persistent Handle Tests
// =============================================================================

func TestRegisterBleveIndex(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget: mock,
	})

	sfb.RegisterBleveIndex("/data/index.bleve")

	assert.True(t, sfb.IsPathTracked("/data/index.bleve"))

	handleType, ok := sfb.GetPathType("/data/index.bleve")
	assert.True(t, ok)
	assert.Equal(t, HandleTypeBleve, handleType)
}

func TestRegisterSQLiteDB(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget: mock,
	})

	sfb.RegisterSQLiteDB("/data/search.db")

	assert.True(t, sfb.IsPathTracked("/data/search.db"))

	handleType, ok := sfb.GetPathType("/data/search.db")
	assert.True(t, ok)
	assert.Equal(t, HandleTypeSQLite, handleType)
}

func TestUnregisterBleveIndex(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget: mock,
	})

	sfb.RegisterBleveIndex("/data/index.bleve")
	sfb.UnregisterBleveIndex("/data/index.bleve")

	assert.False(t, sfb.IsPathTracked("/data/index.bleve"))
}

func TestUnregisterSQLiteDB(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget: mock,
	})

	sfb.RegisterSQLiteDB("/data/search.db")
	sfb.UnregisterSQLiteDB("/data/search.db")

	assert.False(t, sfb.IsPathTracked("/data/search.db"))
}

// =============================================================================
// HandleType Tests
// =============================================================================

func TestHandleType_String(t *testing.T) {
	tests := []struct {
		handleType HandleType
		expected   string
	}{
		{HandleTypeRead, "read"},
		{HandleTypeIndex, "index"},
		{HandleTypeBleve, "bleve"},
		{HandleTypeSQLite, "sqlite"},
		{HandleType(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.handleType.String())
		})
	}
}

// =============================================================================
// Degraded Operation Tests
// =============================================================================

func TestAcquireForIndexDegraded_Success(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:          mock,
		MaxIndexHandles: 10,
	})

	ctx := context.Background()
	release, ok, err := sfb.AcquireForIndexDegraded(ctx, "/file.go")

	require.NoError(t, err)
	assert.True(t, ok)
	require.NotNil(t, release)

	release()
}

func TestAcquireForIndexDegraded_NotAvailable(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:          mock,
		MaxIndexHandles: 1,
	})

	ctx := context.Background()
	r1, _, _ := sfb.AcquireForIndexDegraded(ctx, "/file1.go")

	release, ok, err := sfb.AcquireForIndexDegraded(ctx, "/file2.go")

	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, release)

	r1()
}

func TestAcquireForReadDegraded_Success(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:         mock,
		MaxReadHandles: 10,
	})

	ctx := context.Background()
	release, ok, err := sfb.AcquireForReadDegraded(ctx, "/file.go")

	require.NoError(t, err)
	assert.True(t, ok)
	require.NotNil(t, release)

	release()
}

func TestAcquireForReadDegraded_NotAvailable(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:         mock,
		MaxReadHandles: 1,
	})

	ctx := context.Background()
	r1, _, _ := sfb.AcquireForReadDegraded(ctx, "/file1.go")

	release, ok, err := sfb.AcquireForReadDegraded(ctx, "/file2.go")

	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, release)

	r1()
}

// =============================================================================
// Statistics Tests
// =============================================================================

func TestFileHandleBudget_GetStats(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:          mock,
		BleveHandles:    4,
		SQLiteHandles:   2,
		MaxReadHandles:  32,
		MaxIndexHandles: 16,
	})

	ctx := context.Background()
	rr, _ := sfb.AcquireForRead(ctx, "/file1.go")
	ri, _ := sfb.AcquireForIndex(ctx, "/file2.go")
	sfb.RegisterBleveIndex("/index.bleve")

	stats := sfb.GetStats()

	assert.Equal(t, int32(1), stats.ActiveRead)
	assert.Equal(t, int32(1), stats.ActiveIndex)
	assert.Equal(t, int32(6), stats.ActivePersistent)
	assert.Equal(t, int32(32), stats.MaxRead)
	assert.Equal(t, int32(16), stats.MaxIndex)
	assert.Equal(t, int32(4), stats.BleveHandles)
	assert.Equal(t, int32(2), stats.SQLiteHandles)
	assert.Equal(t, 3, stats.TrackedPaths)

	rr()
	ri()
}

func TestAvailableReadHandles(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:         mock,
		MaxReadHandles: 10,
	})

	assert.Equal(t, 10, sfb.AvailableReadHandles())

	ctx := context.Background()
	r1, _ := sfb.AcquireForRead(ctx, "/file1.go")
	r2, _ := sfb.AcquireForRead(ctx, "/file2.go")

	assert.Equal(t, 8, sfb.AvailableReadHandles())

	r1()
	r2()

	assert.Equal(t, 10, sfb.AvailableReadHandles())
}

func TestAvailableIndexHandles(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:          mock,
		MaxIndexHandles: 5,
	})

	assert.Equal(t, 5, sfb.AvailableIndexHandles())

	ctx := context.Background()
	r1, _ := sfb.AcquireForIndex(ctx, "/file1.go")

	assert.Equal(t, 4, sfb.AvailableIndexHandles())

	r1()

	assert.Equal(t, 5, sfb.AvailableIndexHandles())
}

// =============================================================================
// Lifecycle Tests
// =============================================================================

func TestFileHandleBudget_Close(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:        mock,
		BleveHandles:  4,
		SQLiteHandles: 2,
	})

	sfb.RegisterBleveIndex("/index.bleve")
	sfb.RegisterSQLiteDB("/search.db")

	err := sfb.Close()
	require.NoError(t, err)

	// Persistent handles should be released
	assert.Equal(t, int32(0), sfb.activePersistent.Load())

	// Tracked paths should be cleared
	assert.False(t, sfb.IsPathTracked("/index.bleve"))
	assert.False(t, sfb.IsPathTracked("/search.db"))
}

func TestFileHandleBudget_Close_Idempotent(t *testing.T) {
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget: newMockFileHandleBudget(100),
	})

	err := sfb.Close()
	require.NoError(t, err)

	err = sfb.Close()
	require.NoError(t, err)
}

func TestFileHandleBudget_AcquireAfterClose(t *testing.T) {
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget: newMockFileHandleBudget(100),
	})

	sfb.Close()

	ctx := context.Background()

	_, err := sfb.AcquireForRead(ctx, "/file.go")
	assert.ErrorIs(t, err, ErrFileHandleClosed)

	_, err = sfb.AcquireForIndex(ctx, "/file.go")
	assert.ErrorIs(t, err, ErrFileHandleClosed)
}

// =============================================================================
// Degradation Callback Tests
// =============================================================================

func TestFileHandleBudget_OnDegraded_Called(t *testing.T) {
	var degradedType HandleType
	var degradedReason string
	var mu sync.Mutex

	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:         newMockFileHandleBudget(100),
		MaxReadHandles: 1,
		OnDegraded: func(ht HandleType, reason string) {
			mu.Lock()
			degradedType = ht
			degradedReason = reason
			mu.Unlock()
		},
	})

	ctx := context.Background()
	sfb.AcquireForRead(ctx, "/file1.go")
	sfb.AcquireForRead(ctx, "/file2.go")

	mu.Lock()
	ht := degradedType
	reason := degradedReason
	mu.Unlock()

	assert.Equal(t, HandleTypeRead, ht)
	assert.Contains(t, reason, "limit reached")
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestFileHandleBudget_ConcurrentAcquireRelease(t *testing.T) {
	mock := newMockFileHandleBudget(1000)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:          mock,
		MaxReadHandles:  100,
		MaxIndexHandles: 100,
	})

	var wg sync.WaitGroup
	ctx := context.Background()

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			path := "/file" + string(rune('A'+id%26)) + ".go"
			release, err := sfb.AcquireForRead(ctx, path)
			if err == nil {
				time.Sleep(time.Millisecond)
				release()
			}
		}(i)
	}

	// Concurrent indexes
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			path := "/index" + string(rune('A'+id%26)) + ".go"
			release, err := sfb.AcquireForIndex(ctx, path)
			if err == nil {
				time.Sleep(time.Millisecond)
				release()
			}
		}(i)
	}

	wg.Wait()

	assert.Equal(t, int32(0), sfb.ActiveRead())
	assert.Equal(t, int32(0), sfb.ActiveIndex())
}

func TestFileHandleBudget_ConcurrentTryAcquire(t *testing.T) {
	mock := newMockFileHandleBudget(1000)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:         mock,
		MaxReadHandles: 5,
	})

	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			path := "/file" + string(rune('A'+id%26)) + ".go"
			release, ok := sfb.TryAcquireForRead(path)
			if ok {
				time.Sleep(time.Millisecond)
				release()
			}
		}(i)
	}

	wg.Wait()

	assert.Equal(t, int32(0), sfb.ActiveRead())
}

// =============================================================================
// Path Tracking Tests
// =============================================================================

func TestIsPathTracked(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget: mock,
	})

	assert.False(t, sfb.IsPathTracked("/nonexistent.go"))

	ctx := context.Background()
	release, _ := sfb.AcquireForRead(ctx, "/tracked.go")

	assert.True(t, sfb.IsPathTracked("/tracked.go"))

	release()

	assert.False(t, sfb.IsPathTracked("/tracked.go"))
}

func TestGetPathType_NotTracked(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget: mock,
	})

	_, ok := sfb.GetPathType("/nonexistent.go")
	assert.False(t, ok)
}

func TestGetPathType_DifferentTypes(t *testing.T) {
	mock := newMockFileHandleBudget(100)
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget: mock,
	})

	ctx := context.Background()

	rr, _ := sfb.AcquireForRead(ctx, "/read.go")
	ri, _ := sfb.AcquireForIndex(ctx, "/index.go")
	sfb.RegisterBleveIndex("/bleve.index")
	sfb.RegisterSQLiteDB("/sqlite.db")

	readType, ok := sfb.GetPathType("/read.go")
	assert.True(t, ok)
	assert.Equal(t, HandleTypeRead, readType)

	indexType, ok := sfb.GetPathType("/index.go")
	assert.True(t, ok)
	assert.Equal(t, HandleTypeIndex, indexType)

	bleveType, ok := sfb.GetPathType("/bleve.index")
	assert.True(t, ok)
	assert.Equal(t, HandleTypeBleve, bleveType)

	sqliteType, ok := sfb.GetPathType("/sqlite.db")
	assert.True(t, ok)
	assert.Equal(t, HandleTypeSQLite, sqliteType)

	rr()
	ri()
}

// =============================================================================
// Nil Budget Tests
// =============================================================================

func TestAcquireForRead_NilBudget(t *testing.T) {
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:         nil,
		MaxReadHandles: 10,
	})

	ctx := context.Background()
	release, err := sfb.AcquireForRead(ctx, "/file.go")

	require.NoError(t, err)
	assert.Equal(t, int32(1), sfb.ActiveRead())

	release()
	assert.Equal(t, int32(0), sfb.ActiveRead())
}

func TestAcquireForIndex_NilBudget(t *testing.T) {
	sfb, _ := NewSearchFileHandleBudget(SearchFileHandleBudgetConfig{
		Budget:          nil,
		MaxIndexHandles: 10,
	})

	ctx := context.Background()
	release, err := sfb.AcquireForIndex(ctx, "/file.go")

	require.NoError(t, err)
	assert.Equal(t, int32(1), sfb.ActiveIndex())

	release()
	assert.Equal(t, int32(0), sfb.ActiveIndex())
}
