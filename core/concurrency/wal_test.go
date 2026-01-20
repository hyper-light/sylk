package concurrency_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWALEntry_EncodeDecodeRoundtrip(t *testing.T) {
	entry := &concurrency.WALEntry{
		Sequence:  42,
		Timestamp: time.Now().Truncate(time.Nanosecond),
		Type:      concurrency.EntryStateChange,
		Payload:   []byte(`{"key": "value"}`),
	}

	encoded := entry.Encode()
	decoded, err := concurrency.DecodeWALEntry(encoded)

	require.NoError(t, err)
	assert.Equal(t, entry.Sequence, decoded.Sequence)
	assert.Equal(t, entry.Type, decoded.Type)
	assert.Equal(t, entry.Payload, decoded.Payload)
	assert.Equal(t, entry.Timestamp.UnixNano(), decoded.Timestamp.UnixNano())
}

func TestWALEntry_CRCDetectsCorruption(t *testing.T) {
	entry := &concurrency.WALEntry{
		Sequence:  1,
		Timestamp: time.Now(),
		Type:      concurrency.EntryCheckpoint,
		Payload:   []byte("test payload"),
	}

	encoded := entry.Encode()
	encoded[10] ^= 0xFF

	_, err := concurrency.DecodeWALEntry(encoded)
	assert.ErrorIs(t, err, concurrency.ErrInvalidCRC)
}

func TestWALEntry_ShortReadError(t *testing.T) {
	_, err := concurrency.DecodeWALEntry([]byte{1, 2, 3})
	assert.ErrorIs(t, err, concurrency.ErrShortRead)
}

func TestEntryType_String(t *testing.T) {
	tests := []struct {
		t    concurrency.EntryType
		want string
	}{
		{concurrency.EntryCheckpoint, "checkpoint"},
		{concurrency.EntryStateChange, "state_change"},
		{concurrency.EntryLLMRequest, "llm_request"},
		{concurrency.EntryLLMResponse, "llm_response"},
		{concurrency.EntryFileChange, "file_change"},
		{concurrency.EntryType(99), "unknown"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.t.String())
	}
}

func TestWriteAheadLog_CreateAndClose(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.WALConfig{
		Dir:            dir,
		MaxSegmentSize: 1024 * 1024,
		SyncMode:       concurrency.SyncEveryWrite,
		SyncInterval:   100 * time.Millisecond,
	}

	wal, err := concurrency.NewWriteAheadLog(config)
	require.NoError(t, err)

	err = wal.Close()
	require.NoError(t, err)

	err = wal.Close()
	assert.ErrorIs(t, err, concurrency.ErrWALClosed)
}

func TestWriteAheadLog_AppendAndRead(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.WALConfig{
		Dir:            dir,
		MaxSegmentSize: 1024 * 1024,
		SyncMode:       concurrency.SyncEveryWrite,
		SyncInterval:   100 * time.Millisecond,
	}

	wal, err := concurrency.NewWriteAheadLog(config)
	require.NoError(t, err)
	defer wal.Close()

	entries := []concurrency.WALEntry{
		{Type: concurrency.EntryCheckpoint, Payload: []byte("checkpoint1")},
		{Type: concurrency.EntryStateChange, Payload: []byte("state1")},
		{Type: concurrency.EntryLLMRequest, Payload: []byte("request1")},
	}

	for i := range entries {
		seq, err := wal.Append(&entries[i])
		require.NoError(t, err)
		assert.Equal(t, uint64(i+1), seq)
	}

	all, err := wal.ReadAll()
	require.NoError(t, err)
	assert.Len(t, all, 3)

	for i, entry := range all {
		assert.Equal(t, entries[i].Type, entry.Type)
		assert.Equal(t, entries[i].Payload, entry.Payload)
		assert.Equal(t, uint64(i+1), entry.Sequence)
	}
}

func TestWriteAheadLog_ReadFrom(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.WALConfig{
		Dir:            dir,
		MaxSegmentSize: 1024 * 1024,
		SyncMode:       concurrency.SyncEveryWrite,
		SyncInterval:   100 * time.Millisecond,
	}

	wal, err := concurrency.NewWriteAheadLog(config)
	require.NoError(t, err)
	defer wal.Close()

	for i := 0; i < 10; i++ {
		_, err := wal.Append(&concurrency.WALEntry{
			Type:    concurrency.EntryStateChange,
			Payload: []byte{byte(i)},
		})
		require.NoError(t, err)
	}

	entries, err := wal.ReadFrom(5)
	require.NoError(t, err)
	assert.Len(t, entries, 6)
	assert.Equal(t, uint64(5), entries[0].Sequence)
	assert.Equal(t, uint64(10), entries[5].Sequence)
}

func TestWriteAheadLog_SegmentRotation(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.WALConfig{
		Dir:            dir,
		MaxSegmentSize: 500,
		SyncMode:       concurrency.SyncEveryWrite,
		SyncInterval:   100 * time.Millisecond,
	}

	wal, err := concurrency.NewWriteAheadLog(config)
	require.NoError(t, err)
	defer wal.Close()

	for i := 0; i < 20; i++ {
		_, err := wal.Append(&concurrency.WALEntry{
			Type:    concurrency.EntryStateChange,
			Payload: make([]byte, 50),
		})
		require.NoError(t, err)
	}

	files, err := os.ReadDir(dir)
	require.NoError(t, err)

	segmentCount := 0
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".log" {
			segmentCount++
		}
	}

	assert.Greater(t, segmentCount, 1)

	all, err := wal.ReadAll()
	require.NoError(t, err)
	assert.Len(t, all, 20)
}

func TestWriteAheadLog_Truncate(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.WALConfig{
		Dir:            dir,
		MaxSegmentSize: 200,
		SyncMode:       concurrency.SyncEveryWrite,
		SyncInterval:   100 * time.Millisecond,
	}

	wal, err := concurrency.NewWriteAheadLog(config)
	require.NoError(t, err)
	defer wal.Close()

	for i := 0; i < 30; i++ {
		_, err := wal.Append(&concurrency.WALEntry{
			Type:    concurrency.EntryStateChange,
			Payload: make([]byte, 30),
		})
		require.NoError(t, err)
	}

	initialFiles, _ := os.ReadDir(dir)
	initialCount := countLogFiles(initialFiles)

	err = wal.Truncate(20)
	require.NoError(t, err)

	afterFiles, _ := os.ReadDir(dir)
	afterCount := countLogFiles(afterFiles)

	assert.Less(t, afterCount, initialCount)
}

func countLogFiles(entries []os.DirEntry) int {
	count := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".log" {
			count++
		}
	}
	return count
}

func TestWriteAheadLog_Iterator(t *testing.T) {
	wal := createIteratorTestWAL(t)
	defer wal.Close()

	appendTestEntries(t, wal, 15)

	entries := collectIteratorEntries(t, wal)

	assert.Len(t, entries, 15)
	verifySequentialEntries(t, entries)
}

func createIteratorTestWAL(t *testing.T) *concurrency.WriteAheadLog {
	config := concurrency.WALConfig{
		Dir:            t.TempDir(),
		MaxSegmentSize: 200,
		SyncMode:       concurrency.SyncEveryWrite,
		SyncInterval:   100 * time.Millisecond,
	}
	wal, err := concurrency.NewWriteAheadLog(config)
	require.NoError(t, err)
	return wal
}

func appendTestEntries(t *testing.T, wal *concurrency.WriteAheadLog, n int) {
	for i := 0; i < n; i++ {
		_, err := wal.Append(&concurrency.WALEntry{Type: concurrency.EntryStateChange, Payload: []byte{byte(i)}})
		require.NoError(t, err)
	}
}

func collectIteratorEntries(t *testing.T, wal *concurrency.WriteAheadLog) []*concurrency.WALEntry {
	iter, err := wal.Iterator()
	require.NoError(t, err)

	var entries []*concurrency.WALEntry
	for entry, ok := iter.Next(); ok; entry, ok = iter.Next() {
		entries = append(entries, entry)
	}
	return entries
}

func verifySequentialEntries(t *testing.T, entries []*concurrency.WALEntry) {
	for i, entry := range entries {
		assert.Equal(t, uint64(i+1), entry.Sequence)
	}
}

func TestWriteAheadLog_ConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.WALConfig{
		Dir:            dir,
		MaxSegmentSize: 1024 * 1024,
		SyncMode:       concurrency.SyncBatched,
		SyncInterval:   10 * time.Millisecond,
	}

	wal, err := concurrency.NewWriteAheadLog(config)
	require.NoError(t, err)
	defer wal.Close()

	var wg sync.WaitGroup
	appendCount := 100
	goroutines := 10

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < appendCount; i++ {
				_, err := wal.Append(&concurrency.WALEntry{
					Type:    concurrency.EntryStateChange,
					Payload: []byte("concurrent"),
				})
				assert.NoError(t, err)
			}
		}()
	}

	wg.Wait()

	err = wal.Sync()
	require.NoError(t, err)

	all, err := wal.ReadAll()
	require.NoError(t, err)
	assert.Len(t, all, appendCount*goroutines)

	seenSeqs := make(map[uint64]bool)
	for _, entry := range all {
		assert.False(t, seenSeqs[entry.Sequence])
		seenSeqs[entry.Sequence] = true
	}
}

func TestWriteAheadLog_Persistence(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.WALConfig{
		Dir:            dir,
		MaxSegmentSize: 1024 * 1024,
		SyncMode:       concurrency.SyncEveryWrite,
		SyncInterval:   100 * time.Millisecond,
	}

	wal1, err := concurrency.NewWriteAheadLog(config)
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		_, err := wal1.Append(&concurrency.WALEntry{
			Type:    concurrency.EntryStateChange,
			Payload: []byte{byte(i)},
		})
		require.NoError(t, err)
	}

	lastSeq := wal1.CurrentSequence()
	err = wal1.Close()
	require.NoError(t, err)

	wal2, err := concurrency.NewWriteAheadLog(config)
	require.NoError(t, err)
	defer wal2.Close()

	all, err := wal2.ReadAll()
	require.NoError(t, err)
	assert.Len(t, all, 5)

	seq, err := wal2.Append(&concurrency.WALEntry{
		Type:    concurrency.EntryStateChange,
		Payload: []byte("after restart"),
	})
	require.NoError(t, err)
	assert.Equal(t, lastSeq+1, seq)
}

func TestWriteAheadLog_ClosedOperations(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.WALConfig{
		Dir:            dir,
		MaxSegmentSize: 1024 * 1024,
		SyncMode:       concurrency.SyncEveryWrite,
		SyncInterval:   100 * time.Millisecond,
	}

	wal, err := concurrency.NewWriteAheadLog(config)
	require.NoError(t, err)
	wal.Close()

	_, err = wal.Append(&concurrency.WALEntry{})
	assert.ErrorIs(t, err, concurrency.ErrWALClosed)

	_, err = wal.ReadAll()
	assert.ErrorIs(t, err, concurrency.ErrWALClosed)

	_, err = wal.ReadFrom(0)
	assert.ErrorIs(t, err, concurrency.ErrWALClosed)

	_, err = wal.Iterator()
	assert.ErrorIs(t, err, concurrency.ErrWALClosed)

	err = wal.Truncate(0)
	assert.ErrorIs(t, err, concurrency.ErrWALClosed)

	err = wal.Sync()
	assert.ErrorIs(t, err, concurrency.ErrWALClosed)
}

func TestWriteAheadLog_PeriodicSync(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.WALConfig{
		Dir:            dir,
		MaxSegmentSize: 1024 * 1024,
		SyncMode:       concurrency.SyncPeriodic,
		SyncInterval:   50 * time.Millisecond,
	}

	wal, err := concurrency.NewWriteAheadLog(config)
	require.NoError(t, err)
	defer wal.Close()

	_, err = wal.Append(&concurrency.WALEntry{
		Type:    concurrency.EntryStateChange,
		Payload: []byte("periodic test"),
	})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	all, err := wal.ReadAll()
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestWriteAheadLog_BatchedSync(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.WALConfig{
		Dir:            dir,
		MaxSegmentSize: 1024 * 1024,
		SyncMode:       concurrency.SyncBatched,
		SyncInterval:   50 * time.Millisecond,
	}

	wal, err := concurrency.NewWriteAheadLog(config)
	require.NoError(t, err)
	defer wal.Close()

	for i := 0; i < 10; i++ {
		_, err := wal.Append(&concurrency.WALEntry{
			Type:    concurrency.EntryStateChange,
			Payload: []byte{byte(i)},
		})
		require.NoError(t, err)
	}

	err = wal.Sync()
	require.NoError(t, err)

	all, err := wal.ReadAll()
	require.NoError(t, err)
	assert.Len(t, all, 10)
}

func TestWriteAheadLog_CurrentSequence(t *testing.T) {
	dir := t.TempDir()
	config := concurrency.WALConfig{
		Dir:            dir,
		MaxSegmentSize: 1024 * 1024,
		SyncMode:       concurrency.SyncEveryWrite,
		SyncInterval:   100 * time.Millisecond,
	}

	wal, err := concurrency.NewWriteAheadLog(config)
	require.NoError(t, err)
	defer wal.Close()

	assert.Equal(t, uint64(0), wal.CurrentSequence())

	for i := 1; i <= 5; i++ {
		_, err := wal.Append(&concurrency.WALEntry{
			Type: concurrency.EntryStateChange,
		})
		require.NoError(t, err)
		assert.Equal(t, uint64(i), wal.CurrentSequence())
	}
}

func TestWriteAheadLog_RaceConditions(t *testing.T) {
	wal := createRaceTestWAL(t)

	var wg sync.WaitGroup
	appendErrors := runConcurrentAppends(wal, &wg, 5, 50)
	readErrors := runConcurrentReads(wal, &wg, 3, 20)

	wg.Wait()
	wal.Close()

	assert.Equal(t, int64(0), atomic.LoadInt64(appendErrors))
	assert.Equal(t, int64(0), atomic.LoadInt64(readErrors))
}

func createRaceTestWAL(t *testing.T) *concurrency.WriteAheadLog {
	config := concurrency.WALConfig{
		Dir:            t.TempDir(),
		MaxSegmentSize: 500,
		SyncMode:       concurrency.SyncBatched,
		SyncInterval:   10 * time.Millisecond,
	}
	wal, err := concurrency.NewWriteAheadLog(config)
	require.NoError(t, err)
	return wal
}

func runConcurrentAppends(wal *concurrency.WriteAheadLog, wg *sync.WaitGroup, workers, iterations int) *int64 {
	var errors int64
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			appendEntries(wal, iterations, &errors)
		}()
	}
	return &errors
}

func appendEntries(wal *concurrency.WriteAheadLog, n int, errors *int64) {
	for j := 0; j < n; j++ {
		_, err := wal.Append(&concurrency.WALEntry{Type: concurrency.EntryStateChange, Payload: make([]byte, 20)})
		if err != nil {
			atomic.AddInt64(errors, 1)
		}
	}
}

func runConcurrentReads(wal *concurrency.WriteAheadLog, wg *sync.WaitGroup, workers, iterations int) *int64 {
	var errors int64
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			readEntries(wal, iterations, &errors)
		}()
	}
	return &errors
}

func readEntries(wal *concurrency.WriteAheadLog, n int, errors *int64) {
	for j := 0; j < n; j++ {
		_, err := wal.ReadAll()
		if err != nil && err != concurrency.ErrWALClosed {
			atomic.AddInt64(errors, 1)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDefaultWALConfig(t *testing.T) {
	config := concurrency.DefaultWALConfig()

	assert.Equal(t, ".sylk/wal", config.Dir)
	assert.Equal(t, int64(64*1024*1024), config.MaxSegmentSize)
	assert.Equal(t, concurrency.SyncBatched, config.SyncMode)
	assert.Equal(t, 100*time.Millisecond, config.SyncInterval)
}

func TestWriteAheadLog_ContextCancellation(t *testing.T) {
	t.Run("periodic sync stops on context cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		dir := t.TempDir()
		config := concurrency.WALConfig{
			Dir:            dir,
			MaxSegmentSize: 1024 * 1024,
			SyncMode:       concurrency.SyncPeriodic,
			SyncInterval:   50 * time.Millisecond,
		}

		wal, err := concurrency.NewWriteAheadLogWithContext(ctx, config)
		require.NoError(t, err)

		// Append an entry
		_, err = wal.Append(&concurrency.WALEntry{
			Type:    concurrency.EntryStateChange,
			Payload: []byte("test"),
		})
		require.NoError(t, err)

		// Cancel context - this should trigger the periodic sync to stop
		cancel()

		// Give the goroutine time to respond to cancellation
		time.Sleep(100 * time.Millisecond)

		// Close should still work
		err = wal.Close()
		require.NoError(t, err)

		// Verify data was synced
		wal2, err := concurrency.NewWriteAheadLog(config)
		require.NoError(t, err)
		defer wal2.Close()

		all, err := wal2.ReadAll()
		require.NoError(t, err)
		assert.Len(t, all, 1)
	})

	t.Run("batched sync works with context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		dir := t.TempDir()
		config := concurrency.WALConfig{
			Dir:            dir,
			MaxSegmentSize: 1024 * 1024,
			SyncMode:       concurrency.SyncBatched,
			SyncInterval:   50 * time.Millisecond,
		}

		wal, err := concurrency.NewWriteAheadLogWithContext(ctx, config)
		require.NoError(t, err)

		// Append multiple entries
		for i := 0; i < 5; i++ {
			_, err = wal.Append(&concurrency.WALEntry{
				Type:    concurrency.EntryStateChange,
				Payload: []byte{byte(i)},
			})
			require.NoError(t, err)
		}

		err = wal.Close()
		require.NoError(t, err)

		// Verify all data was written
		wal2, err := concurrency.NewWriteAheadLog(config)
		require.NoError(t, err)
		defer wal2.Close()

		all, err := wal2.ReadAll()
		require.NoError(t, err)
		assert.Len(t, all, 5)
	})

	t.Run("context cancellation during operations", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		dir := t.TempDir()
		config := concurrency.WALConfig{
			Dir:            dir,
			MaxSegmentSize: 1024 * 1024,
			SyncMode:       concurrency.SyncPeriodic,
			SyncInterval:   10 * time.Millisecond,
		}

		wal, err := concurrency.NewWriteAheadLogWithContext(ctx, config)
		require.NoError(t, err)

		// Start concurrent appends
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, err := wal.Append(&concurrency.WALEntry{
					Type:    concurrency.EntryStateChange,
					Payload: []byte{byte(i)},
				})
				if err == concurrency.ErrWALClosed {
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()

		// Cancel after some time
		time.Sleep(50 * time.Millisecond)
		cancel()

		// Close should work cleanly
		err = wal.Close()
		require.NoError(t, err)

		wg.Wait()
	})
}

// TestWriteAheadLog_W12_5_ChannelDoubleClose tests that stopPeriodicSync
// uses sync.Once to prevent panic from double-close (W12.5 fix).
func TestWriteAheadLog_W12_5_ChannelDoubleClose(t *testing.T) {
	t.Run("double close does not panic", func(t *testing.T) {
		dir := t.TempDir()
		config := concurrency.WALConfig{
			Dir:            dir,
			MaxSegmentSize: 1024 * 1024,
			SyncMode:       concurrency.SyncPeriodic,
			SyncInterval:   50 * time.Millisecond,
		}

		wal, err := concurrency.NewWriteAheadLog(config)
		require.NoError(t, err)

		// Append an entry to ensure sync goroutine is active
		_, err = wal.Append(&concurrency.WALEntry{
			Type:    concurrency.EntryStateChange,
			Payload: []byte("test"),
		})
		require.NoError(t, err)

		// First close should succeed
		err = wal.Close()
		require.NoError(t, err)

		// Second close should return ErrWALClosed, not panic
		err = wal.Close()
		assert.ErrorIs(t, err, concurrency.ErrWALClosed)
	})

	t.Run("context cancel before close does not panic", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		dir := t.TempDir()
		config := concurrency.WALConfig{
			Dir:            dir,
			MaxSegmentSize: 1024 * 1024,
			SyncMode:       concurrency.SyncPeriodic,
			SyncInterval:   50 * time.Millisecond,
		}

		wal, err := concurrency.NewWriteAheadLogWithContext(ctx, config)
		require.NoError(t, err)

		// Append an entry
		_, err = wal.Append(&concurrency.WALEntry{
			Type:    concurrency.EntryStateChange,
			Payload: []byte("test"),
		})
		require.NoError(t, err)

		// Cancel context first - this triggers periodicSync to exit
		cancel()

		// Give time for the goroutine to react
		time.Sleep(100 * time.Millisecond)

		// Close should not panic even though context was cancelled
		err = wal.Close()
		require.NoError(t, err)
	})

	t.Run("rapid close calls do not panic", func(t *testing.T) {
		dir := t.TempDir()
		config := concurrency.WALConfig{
			Dir:            dir,
			MaxSegmentSize: 1024 * 1024,
			SyncMode:       concurrency.SyncPeriodic,
			SyncInterval:   10 * time.Millisecond,
		}

		wal, err := concurrency.NewWriteAheadLog(config)
		require.NoError(t, err)

		// Append an entry
		_, err = wal.Append(&concurrency.WALEntry{
			Type:    concurrency.EntryStateChange,
			Payload: []byte("test"),
		})
		require.NoError(t, err)

		// Rapid concurrent close attempts
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				wal.Close() // We don't care about the error, just no panic
			}()
		}

		wg.Wait()
	})

	t.Run("non-periodic mode unaffected", func(t *testing.T) {
		dir := t.TempDir()
		config := concurrency.WALConfig{
			Dir:            dir,
			MaxSegmentSize: 1024 * 1024,
			SyncMode:       concurrency.SyncEveryWrite,
			SyncInterval:   100 * time.Millisecond,
		}

		wal, err := concurrency.NewWriteAheadLog(config)
		require.NoError(t, err)

		_, err = wal.Append(&concurrency.WALEntry{
			Type:    concurrency.EntryStateChange,
			Payload: []byte("test"),
		})
		require.NoError(t, err)

		// Close should work normally without periodic sync
		err = wal.Close()
		require.NoError(t, err)

		// Second close returns error, no panic
		err = wal.Close()
		assert.ErrorIs(t, err, concurrency.ErrWALClosed)
	})
}
