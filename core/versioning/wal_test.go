package versioning

import (
	"sync"
	"testing"
)

func TestNewMemoryWAL(t *testing.T) {
	wal := NewMemoryWAL()
	if wal == nil {
		t.Fatal("expected non-nil WAL")
	}
	if wal.Count() != 0 {
		t.Errorf("expected 0 entries, got %d", wal.Count())
	}
	if wal.LastSequenceID() != 0 {
		t.Errorf("expected sequence ID 0, got %d", wal.LastSequenceID())
	}
}

func TestMemoryWAL_WithOptions(t *testing.T) {
	t.Run("with max entries", func(t *testing.T) {
		wal := NewMemoryWAL(WithMaxEntries(100))
		if wal.maxEntries != 100 {
			t.Errorf("expected maxEntries 100, got %d", wal.maxEntries)
		}
	})

	t.Run("with checkpoint callback", func(t *testing.T) {
		called := false
		wal := NewMemoryWAL(WithCheckpointCallback(func(id uint64) {
			called = true
		}))

		wal.Append(WALEntry{Type: WALEntryOperation, Data: []byte("test")})
		wal.Checkpoint()

		if !called {
			t.Error("checkpoint callback not called")
		}
	})
}

func TestMemoryWAL_Append(t *testing.T) {
	t.Run("appends entry and returns sequence ID", func(t *testing.T) {
		wal := NewMemoryWAL()
		entry := WALEntry{Type: WALEntryOperation, Data: []byte("test")}

		seqID, err := wal.Append(entry)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if seqID != 1 {
			t.Errorf("expected sequence ID 1, got %d", seqID)
		}
		if wal.Count() != 1 {
			t.Errorf("expected 1 entry, got %d", wal.Count())
		}
	})

	t.Run("increments sequence ID", func(t *testing.T) {
		wal := NewMemoryWAL()
		entry := WALEntry{Type: WALEntryOperation, Data: []byte("test")}

		wal.Append(entry)
		seqID, _ := wal.Append(entry)

		if seqID != 2 {
			t.Errorf("expected sequence ID 2, got %d", seqID)
		}
	})

	t.Run("sets timestamp", func(t *testing.T) {
		wal := NewMemoryWAL()
		entry := WALEntry{Type: WALEntryOperation, Data: []byte("test")}

		seqID, _ := wal.Append(entry)
		retrieved, _ := wal.Get(seqID)

		if retrieved.Timestamp.IsZero() {
			t.Error("timestamp should be set")
		}
	})

	t.Run("computes checksum", func(t *testing.T) {
		wal := NewMemoryWAL()
		entry := WALEntry{Type: WALEntryOperation, Data: []byte("test")}

		seqID, _ := wal.Append(entry)
		retrieved, _ := wal.Get(seqID)

		if retrieved.Checksum == 0 {
			t.Error("checksum should be computed")
		}
		if !ValidateChecksum(retrieved) {
			t.Error("checksum validation failed")
		}
	})

	t.Run("returns error when closed", func(t *testing.T) {
		wal := NewMemoryWAL()
		wal.Close()

		_, err := wal.Append(WALEntry{})
		if err != ErrWALClosed {
			t.Errorf("expected ErrWALClosed, got %v", err)
		}
	})

	t.Run("auto-truncates when over limit", func(t *testing.T) {
		wal := NewMemoryWAL(WithMaxEntries(5))

		for i := 0; i < 15; i++ {
			wal.Append(WALEntry{Type: WALEntryOperation, Data: []byte("test")})
		}

		if wal.Count() > 10 {
			t.Errorf("expected at most 10 entries after auto-truncate, got %d", wal.Count())
		}
	})
}

func TestMemoryWAL_Get(t *testing.T) {
	t.Run("retrieves entry by sequence ID", func(t *testing.T) {
		wal := NewMemoryWAL()
		entry := WALEntry{
			Type:       WALEntryOperation,
			Data:       []byte("test data"),
			PipelineID: "pipeline-1",
		}

		seqID, _ := wal.Append(entry)
		retrieved, err := wal.Get(seqID)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if retrieved.PipelineID != "pipeline-1" {
			t.Error("entry mismatch")
		}
	})

	t.Run("returns nil for missing entry", func(t *testing.T) {
		wal := NewMemoryWAL()

		retrieved, err := wal.Get(999)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if retrieved != nil {
			t.Error("expected nil for missing entry")
		}
	})

	t.Run("returns error when closed", func(t *testing.T) {
		wal := NewMemoryWAL()
		wal.Append(WALEntry{Type: WALEntryOperation})
		wal.Close()

		_, err := wal.Get(1)
		if err != ErrWALClosed {
			t.Errorf("expected ErrWALClosed, got %v", err)
		}
	})

	t.Run("returns clone of data", func(t *testing.T) {
		wal := NewMemoryWAL()
		entry := WALEntry{Type: WALEntryOperation, Data: []byte("test")}
		seqID, _ := wal.Append(entry)

		retrieved, _ := wal.Get(seqID)
		retrieved.Data[0] = 'X'

		retrieved2, _ := wal.Get(seqID)
		if retrieved2.Data[0] == 'X' {
			t.Error("Get should return a clone")
		}
	})
}

func TestMemoryWAL_GetRange(t *testing.T) {
	t.Run("retrieves entries in range", func(t *testing.T) {
		wal := NewMemoryWAL()
		for i := 0; i < 5; i++ {
			wal.Append(WALEntry{Type: WALEntryOperation, Data: []byte("test")})
		}

		entries, err := wal.GetRange(2, 4)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != 3 {
			t.Errorf("expected 3 entries, got %d", len(entries))
		}
	})

	t.Run("returns empty for no matches", func(t *testing.T) {
		wal := NewMemoryWAL()

		entries, _ := wal.GetRange(100, 200)
		if len(entries) != 0 {
			t.Errorf("expected 0 entries, got %d", len(entries))
		}
	})
}

func TestMemoryWAL_GetSince(t *testing.T) {
	t.Run("retrieves entries after sequence ID", func(t *testing.T) {
		wal := NewMemoryWAL()
		for i := 0; i < 5; i++ {
			wal.Append(WALEntry{Type: WALEntryOperation, Data: []byte("test")})
		}

		entries, err := wal.GetSince(2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != 3 {
			t.Errorf("expected 3 entries (3,4,5), got %d", len(entries))
		}
	})
}

func TestMemoryWAL_Checkpoint(t *testing.T) {
	t.Run("returns current sequence ID", func(t *testing.T) {
		wal := NewMemoryWAL()
		wal.Append(WALEntry{Type: WALEntryOperation})
		wal.Append(WALEntry{Type: WALEntryOperation})

		checkpointID, err := wal.Checkpoint()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if checkpointID != 2 {
			t.Errorf("expected checkpoint ID 2, got %d", checkpointID)
		}
	})

	t.Run("returns error when closed", func(t *testing.T) {
		wal := NewMemoryWAL()
		wal.Close()

		_, err := wal.Checkpoint()
		if err != ErrWALClosed {
			t.Errorf("expected ErrWALClosed, got %v", err)
		}
	})
}

func TestMemoryWAL_Truncate(t *testing.T) {
	t.Run("removes entries before sequence ID", func(t *testing.T) {
		wal := NewMemoryWAL()
		for i := 0; i < 5; i++ {
			wal.Append(WALEntry{Type: WALEntryOperation, Data: []byte("test")})
		}

		err := wal.Truncate(3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if wal.Count() != 3 {
			t.Errorf("expected 3 entries (3,4,5), got %d", wal.Count())
		}
	})

	t.Run("returns error when closed", func(t *testing.T) {
		wal := NewMemoryWAL()
		wal.Close()

		err := wal.Truncate(1)
		if err != ErrWALClosed {
			t.Errorf("expected ErrWALClosed, got %v", err)
		}
	})
}

func TestMemoryWAL_Close(t *testing.T) {
	wal := NewMemoryWAL()
	err := wal.Close()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !wal.IsClosed() {
		t.Error("expected IsClosed to return true")
	}
}

func TestMemoryWAL_ConcurrentAccess(t *testing.T) {
	wal := NewMemoryWAL()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			entry := WALEntry{
				Type: WALEntryOperation,
				Data: []byte{byte(n)},
			}
			seqID, _ := wal.Append(entry)
			wal.Get(seqID)
			wal.LastSequenceID()
			wal.Count()
		}(i)
	}

	wg.Wait()

	if wal.Count() != 100 {
		t.Errorf("expected 100 entries, got %d", wal.Count())
	}
}

func TestEncodeOperation(t *testing.T) {
	op := NewOperation(
		ComputeVersionID([]byte("base"), nil),
		"test.go",
		NewOffsetTarget(0, 100),
		OpInsert,
		[]byte("content"),
		nil,
		"pipeline-1",
		"session-1",
		"agent-1",
		VectorClock{},
	)

	data := EncodeOperation(&op)
	if len(data) == 0 {
		t.Error("expected non-empty encoded data")
	}
}

func TestValidateChecksum(t *testing.T) {
	t.Run("valid checksum", func(t *testing.T) {
		entry := &WALEntry{
			Data:     []byte("test data"),
			Checksum: computeChecksum([]byte("test data")),
		}

		if !ValidateChecksum(entry) {
			t.Error("expected valid checksum")
		}
	})

	t.Run("invalid checksum", func(t *testing.T) {
		entry := &WALEntry{
			Data:     []byte("test data"),
			Checksum: 12345,
		}

		if ValidateChecksum(entry) {
			t.Error("expected invalid checksum")
		}
	})
}

func TestWALEntryTypes(t *testing.T) {
	types := []WALEntryType{
		WALEntryOperation,
		WALEntryVersion,
		WALEntryCommit,
		WALEntryRollback,
		WALEntryCheckpoint,
	}

	for i, typ := range types {
		if int(typ) != i {
			t.Errorf("expected type %d to have value %d, got %d", i, i, typ)
		}
	}
}

func TestWriteAheadLogInterface(t *testing.T) {
	var _ WriteAheadLog = (*MemoryWAL)(nil)
}
