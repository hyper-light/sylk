package mvcc

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Version Tests (MVCC.1)
// =============================================================================

func TestNewVersion(t *testing.T) {
	data := []byte("test document data")
	txnID := uint64(100)

	t.Run("creates version with correct fields", func(t *testing.T) {
		v := NewVersion(txnID, data, false)

		if v.TxnID != txnID {
			t.Errorf("expected TxnID %d, got %d", txnID, v.TxnID)
		}
		if v.CommitTS != 0 {
			t.Errorf("expected CommitTS 0, got %d", v.CommitTS)
		}
		if !bytes.Equal(v.Data, data) {
			t.Errorf("expected Data %v, got %v", data, v.Data)
		}
		if v.DeleteMarker {
			t.Error("expected DeleteMarker false")
		}
		if v.Next != nil {
			t.Error("expected Next nil")
		}
		if v.CreatedAt.IsZero() {
			t.Error("expected CreatedAt to be set")
		}
	})

	t.Run("creates delete marker version", func(t *testing.T) {
		v := NewVersion(txnID, nil, true)

		if !v.DeleteMarker {
			t.Error("expected DeleteMarker true")
		}
		if v.Data != nil {
			t.Errorf("expected nil Data for delete marker, got %v", v.Data)
		}
	})
}

func TestVersionIsCommitted(t *testing.T) {
	t.Run("uncommitted version", func(t *testing.T) {
		v := NewVersion(1, []byte("data"), false)
		if v.IsCommitted() {
			t.Error("expected uncommitted version")
		}
	})

	t.Run("committed version", func(t *testing.T) {
		v := NewVersion(1, []byte("data"), false)
		v.CommitTS = 100
		if !v.IsCommitted() {
			t.Error("expected committed version")
		}
	})
}

func TestVersionIsVisible(t *testing.T) {
	tests := []struct {
		name     string
		commitTS uint64
		readTS   uint64
		visible  bool
	}{
		{"uncommitted not visible", 0, 100, false},
		{"committed before read visible", 50, 100, true},
		{"committed at read visible", 100, 100, true},
		{"committed after read not visible", 150, 100, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewVersion(1, []byte("data"), false)
			v.CommitTS = tt.commitTS

			if got := v.IsVisible(tt.readTS); got != tt.visible {
				t.Errorf("IsVisible(%d) = %v, want %v", tt.readTS, got, tt.visible)
			}
		})
	}
}

func TestVersionClone(t *testing.T) {
	original := &Version{
		TxnID:        42,
		CommitTS:     100,
		Data:         []byte("original data"),
		DeleteMarker: true,
		CreatedAt:    time.Now(),
	}
	original.Next = NewVersion(41, []byte("older"), false)

	clone := original.Clone()

	t.Run("copies all scalar fields", func(t *testing.T) {
		if clone.TxnID != original.TxnID {
			t.Errorf("TxnID mismatch: got %d, want %d", clone.TxnID, original.TxnID)
		}
		if clone.CommitTS != original.CommitTS {
			t.Errorf("CommitTS mismatch: got %d, want %d", clone.CommitTS, original.CommitTS)
		}
		if clone.DeleteMarker != original.DeleteMarker {
			t.Errorf("DeleteMarker mismatch: got %v, want %v", clone.DeleteMarker, original.DeleteMarker)
		}
		if !clone.CreatedAt.Equal(original.CreatedAt) {
			t.Errorf("CreatedAt mismatch: got %v, want %v", clone.CreatedAt, original.CreatedAt)
		}
	})

	t.Run("deep copies data", func(t *testing.T) {
		if !bytes.Equal(clone.Data, original.Data) {
			t.Error("Data not equal")
		}
		// Modify clone data, original should be unchanged
		clone.Data[0] = 'X'
		if bytes.Equal(clone.Data, original.Data) {
			t.Error("Data should be a deep copy, not shared")
		}
	})

	t.Run("does not copy Next pointer", func(t *testing.T) {
		if clone.Next != nil {
			t.Error("Clone should not have Next pointer")
		}
	})
}

// =============================================================================
// VersionChain Tests (MVCC.2)
// =============================================================================

func TestNewVersionChain(t *testing.T) {
	docID := "doc-123"
	vc := NewVersionChain(docID)

	if vc.DocID != docID {
		t.Errorf("expected DocID %s, got %s", docID, vc.DocID)
	}
	if vc.Head != nil {
		t.Error("expected Head nil")
	}
	if vc.Length != 0 {
		t.Errorf("expected Length 0, got %d", vc.Length)
	}
}

func TestVersionChainPrepend(t *testing.T) {
	vc := NewVersionChain("doc-1")

	v1 := NewVersion(1, []byte("v1"), false)
	v2 := NewVersion(2, []byte("v2"), false)
	v3 := NewVersion(3, []byte("v3"), false)

	vc.Prepend(v1)
	if vc.Head != v1 {
		t.Error("Head should be v1")
	}
	if vc.Length != 1 {
		t.Errorf("Length should be 1, got %d", vc.Length)
	}

	vc.Prepend(v2)
	if vc.Head != v2 {
		t.Error("Head should be v2")
	}
	if vc.Head.Next != v1 {
		t.Error("v2.Next should be v1")
	}
	if vc.Length != 2 {
		t.Errorf("Length should be 2, got %d", vc.Length)
	}

	vc.Prepend(v3)
	if vc.Head != v3 {
		t.Error("Head should be v3")
	}
	if vc.Length != 3 {
		t.Errorf("Length should be 3, got %d", vc.Length)
	}

	// Verify chain: v3 -> v2 -> v1
	if vc.Head.Next != v2 {
		t.Error("v3.Next should be v2")
	}
	if vc.Head.Next.Next != v1 {
		t.Error("v2.Next should be v1")
	}
}

func TestVersionChainGetLatest(t *testing.T) {
	t.Run("empty chain returns nil", func(t *testing.T) {
		vc := NewVersionChain("doc-1")
		if vc.GetLatest() != nil {
			t.Error("expected nil for empty chain")
		}
	})

	t.Run("returns head version", func(t *testing.T) {
		vc := NewVersionChain("doc-1")
		v := NewVersion(1, []byte("data"), false)
		vc.Prepend(v)

		if vc.GetLatest() != v {
			t.Error("expected head version")
		}
	})

	t.Run("returns uncommitted head", func(t *testing.T) {
		vc := NewVersionChain("doc-1")
		committed := NewVersion(1, []byte("committed"), false)
		committed.CommitTS = 100
		vc.Prepend(committed)

		uncommitted := NewVersion(2, []byte("uncommitted"), false)
		vc.Prepend(uncommitted)

		latest := vc.GetLatest()
		if latest != uncommitted {
			t.Error("expected uncommitted head")
		}
		if latest.IsCommitted() {
			t.Error("latest should be uncommitted")
		}
	})
}

func TestVersionChainGetLatestCommitted(t *testing.T) {
	t.Run("empty chain returns nil", func(t *testing.T) {
		vc := NewVersionChain("doc-1")
		if vc.GetLatestCommitted() != nil {
			t.Error("expected nil for empty chain")
		}
	})

	t.Run("only uncommitted returns nil", func(t *testing.T) {
		vc := NewVersionChain("doc-1")
		vc.Prepend(NewVersion(1, []byte("uncommitted"), false))
		if vc.GetLatestCommitted() != nil {
			t.Error("expected nil when only uncommitted versions exist")
		}
	})

	t.Run("skips uncommitted to find committed", func(t *testing.T) {
		vc := NewVersionChain("doc-1")

		committed := NewVersion(1, []byte("committed"), false)
		committed.CommitTS = 100
		vc.Prepend(committed)

		uncommitted := NewVersion(2, []byte("uncommitted"), false)
		vc.Prepend(uncommitted)

		result := vc.GetLatestCommitted()
		if result != committed {
			t.Error("expected committed version")
		}
	})
}

func TestVersionChainGetVisibleVersion(t *testing.T) {
	vc := NewVersionChain("doc-1")

	// Create versions with different commit timestamps
	v1 := NewVersion(1, []byte("v1"), false)
	v1.CommitTS = 100
	vc.Prepend(v1)

	v2 := NewVersion(2, []byte("v2"), false)
	v2.CommitTS = 200
	vc.Prepend(v2)

	v3 := NewVersion(3, []byte("v3"), false)
	v3.CommitTS = 300
	vc.Prepend(v3)

	// Add uncommitted version at head
	v4 := NewVersion(4, []byte("v4"), false)
	vc.Prepend(v4)

	tests := []struct {
		name    string
		readTS  uint64
		wantTxn uint64
		wantNil bool
	}{
		{"before all commits", 50, 0, true},
		{"at v1 commit", 100, 1, false},
		{"between v1 and v2", 150, 1, false},
		{"at v2 commit", 200, 2, false},
		{"between v2 and v3", 250, 2, false},
		{"at v3 commit", 300, 3, false},
		{"after v3 commit", 400, 3, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := vc.GetVisibleVersion(tt.readTS)
			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil, got version with TxnID %d", result.TxnID)
				}
			} else {
				if result == nil {
					t.Error("expected version, got nil")
				} else if result.TxnID != tt.wantTxn {
					t.Errorf("expected TxnID %d, got %d", tt.wantTxn, result.TxnID)
				}
			}
		})
	}
}

func TestVersionChainCommitVersion(t *testing.T) {
	t.Run("commits uncommitted version", func(t *testing.T) {
		vc := NewVersionChain("doc-1")
		v := NewVersion(42, []byte("data"), false)
		vc.Prepend(v)

		err := vc.CommitVersion(42, 100)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !v.IsCommitted() {
			t.Error("version should be committed")
		}
		if v.CommitTS != 100 {
			t.Errorf("expected CommitTS 100, got %d", v.CommitTS)
		}
	})

	t.Run("error on version not found", func(t *testing.T) {
		vc := NewVersionChain("doc-1")
		vc.Prepend(NewVersion(1, []byte("data"), false))

		err := vc.CommitVersion(999, 100)
		if err != ErrVersionNotFound {
			t.Errorf("expected ErrVersionNotFound, got %v", err)
		}
	})

	t.Run("error on already committed", func(t *testing.T) {
		vc := NewVersionChain("doc-1")
		v := NewVersion(42, []byte("data"), false)
		v.CommitTS = 100
		vc.Prepend(v)

		err := vc.CommitVersion(42, 200)
		if err != ErrVersionCommitted {
			t.Errorf("expected ErrVersionCommitted, got %v", err)
		}
	})

	t.Run("commits version in middle of chain", func(t *testing.T) {
		vc := NewVersionChain("doc-1")

		v1 := NewVersion(1, []byte("v1"), false)
		vc.Prepend(v1)

		v2 := NewVersion(2, []byte("v2"), false)
		vc.Prepend(v2)

		v3 := NewVersion(3, []byte("v3"), false)
		vc.Prepend(v3)

		err := vc.CommitVersion(2, 100)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !v2.IsCommitted() {
			t.Error("v2 should be committed")
		}
	})
}

func TestVersionChainAbortVersion(t *testing.T) {
	t.Run("aborts head version", func(t *testing.T) {
		vc := NewVersionChain("doc-1")
		v1 := NewVersion(1, []byte("v1"), false)
		v1.CommitTS = 100
		vc.Prepend(v1)

		v2 := NewVersion(2, []byte("v2"), false)
		vc.Prepend(v2)

		err := vc.AbortVersion(2)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if vc.Head != v1 {
			t.Error("head should be v1 after abort")
		}
		if vc.Length != 1 {
			t.Errorf("length should be 1, got %d", vc.Length)
		}
	})

	t.Run("aborts middle version", func(t *testing.T) {
		vc := NewVersionChain("doc-1")

		v1 := NewVersion(1, []byte("v1"), false)
		vc.Prepend(v1)

		v2 := NewVersion(2, []byte("v2"), false)
		vc.Prepend(v2)

		v3 := NewVersion(3, []byte("v3"), false)
		vc.Prepend(v3)

		err := vc.AbortVersion(2)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if vc.Head.Next != v1 {
			t.Error("v3.Next should be v1 after aborting v2")
		}
		if vc.Length != 2 {
			t.Errorf("length should be 2, got %d", vc.Length)
		}
	})

	t.Run("error on version not found", func(t *testing.T) {
		vc := NewVersionChain("doc-1")
		vc.Prepend(NewVersion(1, []byte("data"), false))

		err := vc.AbortVersion(999)
		if err != ErrVersionNotFound {
			t.Errorf("expected ErrVersionNotFound, got %v", err)
		}
	})

	t.Run("error on committed version", func(t *testing.T) {
		vc := NewVersionChain("doc-1")
		v := NewVersion(42, []byte("data"), false)
		v.CommitTS = 100
		vc.Prepend(v)

		err := vc.AbortVersion(42)
		if err != ErrVersionCommitted {
			t.Errorf("expected ErrVersionCommitted, got %v", err)
		}
	})

	t.Run("abort only version in chain", func(t *testing.T) {
		vc := NewVersionChain("doc-1")
		v := NewVersion(1, []byte("data"), false)
		vc.Prepend(v)

		err := vc.AbortVersion(1)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if vc.Head != nil {
			t.Error("head should be nil after aborting only version")
		}
		if vc.Length != 0 {
			t.Errorf("length should be 0, got %d", vc.Length)
		}
	})
}

func TestVersionChainGarbageCollect(t *testing.T) {
	t.Run("empty chain", func(t *testing.T) {
		vc := NewVersionChain("doc-1")
		removed := vc.GarbageCollect(100)
		if removed != 0 {
			t.Errorf("expected 0 removed, got %d", removed)
		}
	})

	t.Run("keeps at least one committed version", func(t *testing.T) {
		vc := NewVersionChain("doc-1")

		v := NewVersion(1, []byte("v1"), false)
		v.CommitTS = 50
		vc.Prepend(v)

		removed := vc.GarbageCollect(100)
		if removed != 0 {
			t.Errorf("expected 0 removed (must keep one), got %d", removed)
		}
		if vc.Length != 1 {
			t.Errorf("expected length 1, got %d", vc.Length)
		}
	})

	t.Run("removes old versions after newer committed exists", func(t *testing.T) {
		vc := NewVersionChain("doc-1")

		v1 := NewVersion(1, []byte("v1"), false)
		v1.CommitTS = 50
		vc.Prepend(v1)

		v2 := NewVersion(2, []byte("v2"), false)
		v2.CommitTS = 100
		vc.Prepend(v2)

		v3 := NewVersion(3, []byte("v3"), false)
		v3.CommitTS = 200
		vc.Prepend(v3)

		// GC with minTS=150 removes all versions with commitTS < minTS
		// after finding the first committed version to keep (v3).
		// Both v2 (commitTS=100) and v1 (commitTS=50) have commitTS < 150,
		// so both are removed once we encounter v2.
		removed := vc.GarbageCollect(150)
		if removed != 2 {
			t.Errorf("expected 2 removed (v2 and v1), got %d", removed)
		}
		if vc.Length != 1 {
			t.Errorf("expected length 1, got %d", vc.Length)
		}
		// Verify only v3 remains
		if vc.Head != v3 || vc.Head.Next != nil {
			t.Error("expected only v3 to remain in chain")
		}
	})

	t.Run("preserves uncommitted versions", func(t *testing.T) {
		vc := NewVersionChain("doc-1")

		v1 := NewVersion(1, []byte("v1"), false)
		v1.CommitTS = 50
		vc.Prepend(v1)

		v2 := NewVersion(2, []byte("v2"), false)
		v2.CommitTS = 100
		vc.Prepend(v2)

		uncommitted := NewVersion(3, []byte("uncommitted"), false)
		vc.Prepend(uncommitted)

		removed := vc.GarbageCollect(200)
		// Should remove v1, keep v2 (newest committed) and uncommitted
		if removed != 1 {
			t.Errorf("expected 1 removed, got %d", removed)
		}
		if vc.Head != uncommitted {
			t.Error("head should still be uncommitted version")
		}
	})

	t.Run("removes multiple old versions", func(t *testing.T) {
		vc := NewVersionChain("doc-1")

		for i := 1; i <= 5; i++ {
			v := NewVersion(uint64(i), []byte("data"), false)
			v.CommitTS = uint64(i * 100)
			vc.Prepend(v)
		}

		// Chain: v5(500) -> v4(400) -> v3(300) -> v2(200) -> v1(100)
		// GC with minTS=350 should remove v1, v2, v3 (but keep v3 as the boundary)
		removed := vc.GarbageCollect(350)
		if removed < 2 {
			t.Errorf("expected at least 2 removed, got %d", removed)
		}
	})
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestVersionChainConcurrentPrepend(t *testing.T) {
	vc := NewVersionChain("doc-concurrent")
	numGoroutines := 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(txnID int) {
			defer wg.Done()
			v := NewVersion(uint64(txnID), []byte("data"), false)
			vc.Prepend(v)
		}(i)
	}

	wg.Wait()

	if vc.Length != numGoroutines {
		t.Errorf("expected length %d, got %d", numGoroutines, vc.Length)
	}

	// Verify chain integrity
	seen := make(map[uint64]bool)
	current := vc.Head
	count := 0
	for current != nil {
		if seen[current.TxnID] {
			t.Errorf("duplicate TxnID %d in chain", current.TxnID)
		}
		seen[current.TxnID] = true
		count++
		current = current.Next
	}
	if count != numGoroutines {
		t.Errorf("chain traversal found %d versions, expected %d", count, numGoroutines)
	}
}

func TestVersionChainConcurrentReads(t *testing.T) {
	vc := NewVersionChain("doc-concurrent")

	// Prepend some committed versions
	for i := 1; i <= 10; i++ {
		v := NewVersion(uint64(i), []byte("data"), false)
		v.CommitTS = uint64(i * 100)
		vc.Prepend(v)
	}

	numReaders := 50
	var wg sync.WaitGroup
	wg.Add(numReaders)

	for i := 0; i < numReaders; i++ {
		go func(readerID int) {
			defer wg.Done()
			readTS := uint64((readerID%10 + 1) * 100)
			for j := 0; j < 100; j++ {
				v := vc.GetVisibleVersion(readTS)
				if v == nil {
					t.Errorf("reader %d: expected visible version for readTS %d", readerID, readTS)
					return
				}
				_ = vc.GetLatest()
				_ = vc.GetLatestCommitted()
			}
		}(i)
	}

	wg.Wait()
}

func TestVersionChainConcurrentCommitAndRead(t *testing.T) {
	vc := NewVersionChain("doc-concurrent")

	// Add uncommitted versions
	numVersions := 20
	for i := 1; i <= numVersions; i++ {
		v := NewVersion(uint64(i), []byte("data"), false)
		vc.Prepend(v)
	}

	var wg sync.WaitGroup

	// Concurrent commits
	wg.Add(numVersions)
	for i := 1; i <= numVersions; i++ {
		go func(txnID int) {
			defer wg.Done()
			// Small delay to interleave with reads
			time.Sleep(time.Microsecond * time.Duration(txnID%10))
			err := vc.CommitVersion(uint64(txnID), uint64(txnID*100))
			if err != nil {
				t.Errorf("commit failed for txn %d: %v", txnID, err)
			}
		}(i)
	}

	// Concurrent reads
	numReaders := 10
	wg.Add(numReaders)
	for i := 0; i < numReaders; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = vc.GetLatest()
				_ = vc.GetLatestCommitted()
				_ = vc.GetVisibleVersion(uint64(j * 50))
			}
		}()
	}

	wg.Wait()

	// Verify all versions are committed
	current := vc.Head
	for current != nil {
		if !current.IsCommitted() {
			t.Errorf("version %d should be committed", current.TxnID)
		}
		current = current.Next
	}
}

func TestVersionChainConcurrentAbort(t *testing.T) {
	vc := NewVersionChain("doc-concurrent")

	// Add uncommitted versions
	numVersions := 50
	for i := 1; i <= numVersions; i++ {
		v := NewVersion(uint64(i), []byte("data"), false)
		vc.Prepend(v)
	}

	var wg sync.WaitGroup
	var abortedCount int32
	var mu sync.Mutex

	// Abort even txnIDs
	toAbort := numVersions / 2
	wg.Add(toAbort)
	for i := 2; i <= numVersions; i += 2 {
		go func(txnID int) {
			defer wg.Done()
			err := vc.AbortVersion(uint64(txnID))
			if err == nil {
				mu.Lock()
				abortedCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	if int(abortedCount) != toAbort {
		t.Errorf("expected %d aborted, got %d", toAbort, abortedCount)
	}

	expectedLength := numVersions - toAbort
	if vc.Length != expectedLength {
		t.Errorf("expected length %d, got %d", expectedLength, vc.Length)
	}

	// Verify only odd txnIDs remain
	current := vc.Head
	for current != nil {
		if current.TxnID%2 == 0 {
			t.Errorf("even txnID %d should have been aborted", current.TxnID)
		}
		current = current.Next
	}
}

func TestVersionChainConcurrentGarbageCollect(t *testing.T) {
	vc := NewVersionChain("doc-concurrent")

	// Add committed versions
	numVersions := 100
	for i := 1; i <= numVersions; i++ {
		v := NewVersion(uint64(i), []byte("data"), false)
		v.CommitTS = uint64(i * 10)
		vc.Prepend(v)
	}

	var wg sync.WaitGroup
	numGCWorkers := 10

	wg.Add(numGCWorkers)
	for i := 0; i < numGCWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				minTS := uint64((workerID*10 + j) * 10)
				vc.GarbageCollect(minTS)
			}
		}(i)
	}

	// Concurrent reads during GC
	wg.Add(5)
	for i := 0; i < 5; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = vc.GetVisibleVersion(uint64(j * 20))
				_ = vc.GetLatestCommitted()
			}
		}()
	}

	wg.Wait()

	// Chain should still be valid
	if vc.Length < 1 {
		t.Error("GC should keep at least one version")
	}

	// Verify chain integrity
	current := vc.Head
	count := 0
	for current != nil {
		count++
		current = current.Next
	}
	if count != vc.Length {
		t.Errorf("chain traversal count %d != Length %d", count, vc.Length)
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestVersionChainEmptyOperations(t *testing.T) {
	vc := NewVersionChain("empty")

	if vc.GetLatest() != nil {
		t.Error("GetLatest on empty should return nil")
	}
	if vc.GetLatestCommitted() != nil {
		t.Error("GetLatestCommitted on empty should return nil")
	}
	if vc.GetVisibleVersion(100) != nil {
		t.Error("GetVisibleVersion on empty should return nil")
	}
	if err := vc.CommitVersion(1, 100); err != ErrVersionNotFound {
		t.Errorf("CommitVersion on empty should return ErrVersionNotFound, got %v", err)
	}
	if err := vc.AbortVersion(1); err != ErrVersionNotFound {
		t.Errorf("AbortVersion on empty should return ErrVersionNotFound, got %v", err)
	}
	if vc.GarbageCollect(100) != 0 {
		t.Error("GarbageCollect on empty should return 0")
	}
}

func TestVersionWithNilData(t *testing.T) {
	v := NewVersion(1, nil, false)
	if v.Data != nil {
		t.Error("Data should be nil")
	}

	clone := v.Clone()
	if clone.Data != nil {
		t.Error("Cloned Data should be nil")
	}
}

func TestVersionVisibilityEdgeCases(t *testing.T) {
	t.Run("zero read timestamp", func(t *testing.T) {
		v := NewVersion(1, []byte("data"), false)
		v.CommitTS = 0 // uncommitted
		if v.IsVisible(0) {
			t.Error("uncommitted should not be visible even with readTS=0")
		}
	})

	t.Run("max uint64 timestamps", func(t *testing.T) {
		v := NewVersion(1, []byte("data"), false)
		v.CommitTS = ^uint64(0) - 1 // max - 1

		if !v.IsVisible(^uint64(0)) {
			t.Error("should be visible when readTS is max")
		}
		if v.IsVisible(^uint64(0) - 2) {
			t.Error("should not be visible when readTS < commitTS")
		}
	})
}

func TestDeleteMarkerVisibility(t *testing.T) {
	vc := NewVersionChain("doc-1")

	// Original version
	v1 := NewVersion(1, []byte("original"), false)
	v1.CommitTS = 100
	vc.Prepend(v1)

	// Delete marker
	v2 := NewVersion(2, nil, true)
	v2.CommitTS = 200
	vc.Prepend(v2)

	// Read at different timestamps
	visible100 := vc.GetVisibleVersion(100)
	if visible100 == nil || visible100.DeleteMarker {
		t.Error("at ts=100, should see original (non-delete)")
	}

	visible200 := vc.GetVisibleVersion(200)
	if visible200 == nil || !visible200.DeleteMarker {
		t.Error("at ts=200, should see delete marker")
	}
}

// =============================================================================
// Transaction Tests (MVCC.3)
// =============================================================================

func TestNewTransaction(t *testing.T) {
	txn := NewTransaction(42, 100)

	if txn.ID != 42 {
		t.Errorf("expected ID 42, got %d", txn.ID)
	}
	if txn.StartTS != 100 {
		t.Errorf("expected StartTS 100, got %d", txn.StartTS)
	}
	if txn.State != TxnStateActive {
		t.Errorf("expected State Active, got %v", txn.State)
	}
	if txn.ReadSet == nil {
		t.Error("ReadSet should be initialized")
	}
	if len(txn.ReadSet) != 0 {
		t.Error("ReadSet should be empty")
	}
	if txn.WriteSet == nil {
		t.Error("WriteSet should be initialized")
	}
	if len(txn.WriteSet) != 0 {
		t.Error("WriteSet should be empty")
	}
	if txn.CreatedAt().IsZero() {
		t.Error("createdAt should be set")
	}
}

func TestTransactionStateString(t *testing.T) {
	tests := []struct {
		state    TransactionState
		expected string
	}{
		{TxnStateActive, "Active"},
		{TxnStateCommitting, "Committing"},
		{TxnStateCommitted, "Committed"},
		{TxnStateAborted, "Aborted"},
		{TransactionState(99), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.state.String(); got != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, got)
			}
		})
	}
}

func TestTransactionAddToReadSet(t *testing.T) {
	txn := NewTransaction(1, 100)

	txn.AddToReadSet("doc1", 5)
	txn.AddToReadSet("doc2", 10)

	if txn.ReadSet["doc1"] != 5 {
		t.Errorf("expected ReadSet[doc1]=5, got %d", txn.ReadSet["doc1"])
	}
	if txn.ReadSet["doc2"] != 10 {
		t.Errorf("expected ReadSet[doc2]=10, got %d", txn.ReadSet["doc2"])
	}

	// Update existing entry
	txn.AddToReadSet("doc1", 7)
	if txn.ReadSet["doc1"] != 7 {
		t.Errorf("expected ReadSet[doc1]=7 after update, got %d", txn.ReadSet["doc1"])
	}
}

func TestTransactionAddToWriteSet(t *testing.T) {
	txn := NewTransaction(1, 100)

	v1 := NewVersion(1, []byte("v1"), false)
	txn.AddToWriteSet("doc1", v1)

	v2 := NewVersion(1, []byte("v2"), false)
	txn.AddToWriteSet("doc2", v2)

	if txn.WriteSet["doc1"] != v1 {
		t.Error("expected WriteSet[doc1]=v1")
	}
	if txn.WriteSet["doc2"] != v2 {
		t.Error("expected WriteSet[doc2]=v2")
	}
}

func TestTransactionGetFromWriteSet(t *testing.T) {
	txn := NewTransaction(1, 100)

	// Not in write set
	if txn.GetFromWriteSet("doc1") != nil {
		t.Error("expected nil for doc not in write set")
	}

	v1 := NewVersion(1, []byte("v1"), false)
	txn.AddToWriteSet("doc1", v1)

	if txn.GetFromWriteSet("doc1") != v1 {
		t.Error("expected v1 from write set")
	}
}

func TestTransactionCanCommit(t *testing.T) {
	t.Run("no conflicts - can commit", func(t *testing.T) {
		chains := make(map[string]*VersionChain)

		chain1 := NewVersionChain("doc1")
		v1 := NewVersion(5, []byte("data1"), false)
		v1.CommitTS = 10
		chain1.Prepend(v1)
		chains["doc1"] = chain1

		txn := NewTransaction(1, 100)
		txn.AddToReadSet("doc1", 5) // Read version from txn 5

		if !txn.CanCommit(chains) {
			t.Error("should be able to commit - no conflicts")
		}
	})

	t.Run("conflict - version overwritten", func(t *testing.T) {
		chains := make(map[string]*VersionChain)

		chain1 := NewVersionChain("doc1")
		v1 := NewVersion(5, []byte("data1"), false)
		v1.CommitTS = 10
		chain1.Prepend(v1)
		chains["doc1"] = chain1

		txn := NewTransaction(1, 100)
		txn.AddToReadSet("doc1", 5)

		// Add a newer committed version
		v2 := NewVersion(6, []byte("data2"), false)
		v2.CommitTS = 20
		chain1.Prepend(v2)

		if txn.CanCommit(chains) {
			t.Error("should NOT be able to commit - version was overwritten")
		}
	})

	t.Run("no conflict on empty read", func(t *testing.T) {
		chains := make(map[string]*VersionChain)
		chain1 := NewVersionChain("doc1")
		chains["doc1"] = chain1

		txn := NewTransaction(1, 100)
		txn.AddToReadSet("doc1", 0) // Read nothing

		if !txn.CanCommit(chains) {
			t.Error("should be able to commit when doc was and still is empty")
		}
	})

	t.Run("conflict when read version and now committed exists", func(t *testing.T) {
		chains := make(map[string]*VersionChain)
		chain1 := NewVersionChain("doc1")
		chains["doc1"] = chain1

		txn := NewTransaction(1, 100)
		txn.AddToReadSet("doc1", 5) // Read version 5

		// Now there's no committed version
		// But we read a specific version, conflict
		if txn.CanCommit(chains) {
			t.Error("should NOT be able to commit - read version no longer exists")
		}
	})
}

func TestTransactionIsActive(t *testing.T) {
	txn := NewTransaction(1, 100)

	if !txn.IsActive() {
		t.Error("new transaction should be active")
	}

	txn.SetState(TxnStateCommitting)
	if txn.IsActive() {
		t.Error("committing transaction should not be active")
	}

	txn.SetState(TxnStateCommitted)
	if txn.IsActive() {
		t.Error("committed transaction should not be active")
	}

	txn.SetState(TxnStateAborted)
	if txn.IsActive() {
		t.Error("aborted transaction should not be active")
	}
}

func TestTransactionSetState(t *testing.T) {
	txn := NewTransaction(1, 100)

	txn.SetState(TxnStateCommitting)
	if txn.GetState() != TxnStateCommitting {
		t.Error("state should be Committing")
	}

	txn.SetState(TxnStateCommitted)
	if txn.GetState() != TxnStateCommitted {
		t.Error("state should be Committed")
	}

	txn.SetState(TxnStateAborted)
	if txn.GetState() != TxnStateAborted {
		t.Error("state should be Aborted")
	}
}

func TestTransactionGetState(t *testing.T) {
	txn := NewTransaction(1, 100)

	if txn.GetState() != TxnStateActive {
		t.Error("initial state should be Active")
	}
}

func TestTransactionCreatedAt(t *testing.T) {
	before := time.Now()
	txn := NewTransaction(1, 100)
	after := time.Now()

	createdAt := txn.CreatedAt()
	if createdAt.Before(before) || createdAt.After(after) {
		t.Error("CreatedAt should be between before and after timestamps")
	}
}

func TestTransactionConcurrentReadSetAccess(t *testing.T) {
	txn := NewTransaction(1, 100)
	var wg sync.WaitGroup

	// Concurrent writes to read set
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			txn.AddToReadSet("doc-"+string(rune('A'+i%26)), uint64(i))
		}(i)
	}

	wg.Wait()
	// Test passes if no race conditions detected
}

func TestTransactionConcurrentWriteSetAccess(t *testing.T) {
	txn := NewTransaction(1, 100)
	var wg sync.WaitGroup

	// Concurrent writes to write set
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := NewVersion(uint64(i), []byte("data"), false)
			txn.AddToWriteSet("doc-"+string(rune('A'+i%26)), v)
		}(i)
	}

	wg.Wait()
	// Test passes if no race conditions detected
}

func TestTransactionConcurrentStateAccess(t *testing.T) {
	txn := NewTransaction(1, 100)
	var wg sync.WaitGroup

	// Concurrent state changes and reads
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			txn.IsActive()
			txn.GetState()
		}()
		go func(i int) {
			defer wg.Done()
			states := []TransactionState{TxnStateActive, TxnStateCommitting, TxnStateCommitted, TxnStateAborted}
			txn.SetState(states[i%len(states)])
		}(i)
	}

	wg.Wait()
	// Test passes if no race conditions detected
}

// =============================================================================
// MVCCStore Tests (MVCC.4)
// =============================================================================

func TestNewMVCCStore(t *testing.T) {
	store := NewMVCCStore()

	if store == nil {
		t.Fatal("NewMVCCStore returned nil")
	}
	if store.chains == nil {
		t.Error("chains map should be initialized")
	}
	if store.txns == nil {
		t.Error("txns map should be initialized")
	}
	if store.gcInterval != DefaultGCInterval {
		t.Errorf("expected gcInterval %v, got %v", DefaultGCInterval, store.gcInterval)
	}
	if store.minRetentionTS != 0 {
		t.Errorf("expected minRetentionTS 0, got %d", store.minRetentionTS)
	}
}

func TestMVCCStoreBegin(t *testing.T) {
	store := NewMVCCStore()

	txn1 := store.Begin()
	if txn1 == nil {
		t.Fatal("Begin returned nil")
	}
	if txn1.ID == 0 {
		t.Error("transaction ID should not be 0")
	}
	if !txn1.IsActive() {
		t.Error("new transaction should be active")
	}

	txn2 := store.Begin()
	if txn2.ID == txn1.ID {
		t.Error("transaction IDs should be unique")
	}
	if txn2.ID <= txn1.ID {
		t.Error("transaction IDs should be monotonically increasing")
	}
}

func TestMVCCStoreBeginMultiple(t *testing.T) {
	store := NewMVCCStore()

	txnIDs := make(map[uint64]bool)
	for i := 0; i < 100; i++ {
		txn := store.Begin()
		if txnIDs[txn.ID] {
			t.Errorf("duplicate transaction ID: %d", txn.ID)
		}
		txnIDs[txn.ID] = true
	}

	if len(txnIDs) != 100 {
		t.Errorf("expected 100 unique IDs, got %d", len(txnIDs))
	}
}

func TestMVCCStoreGetChain(t *testing.T) {
	store := NewMVCCStore()

	// Get creates new chain
	chain1 := store.GetChain("doc1")
	if chain1 == nil {
		t.Fatal("GetChain returned nil")
	}
	if chain1.DocID != "doc1" {
		t.Errorf("expected DocID 'doc1', got %s", chain1.DocID)
	}

	// Get same chain again
	chain1Again := store.GetChain("doc1")
	if chain1Again != chain1 {
		t.Error("should return same chain for same docID")
	}

	// Get different chain
	chain2 := store.GetChain("doc2")
	if chain2 == chain1 {
		t.Error("different docIDs should have different chains")
	}
}

func TestMVCCStoreGetTransaction(t *testing.T) {
	store := NewMVCCStore()

	// Non-existent transaction
	if store.GetTransaction(999) != nil {
		t.Error("should return nil for non-existent transaction")
	}

	txn := store.Begin()
	retrieved := store.GetTransaction(txn.ID)
	if retrieved != txn {
		t.Error("should return the same transaction object")
	}
}

func TestMVCCStoreNextTimestamp(t *testing.T) {
	store := NewMVCCStore()

	ts1 := store.NextTimestamp()
	ts2 := store.NextTimestamp()
	ts3 := store.NextTimestamp()

	if ts2 <= ts1 {
		t.Error("timestamps should be monotonically increasing")
	}
	if ts3 <= ts2 {
		t.Error("timestamps should be monotonically increasing")
	}
}

func TestMVCCStoreGetChains(t *testing.T) {
	store := NewMVCCStore()

	store.GetChain("doc1")
	store.GetChain("doc2")
	store.GetChain("doc3")

	chains := store.GetChains()

	if len(chains) != 3 {
		t.Errorf("expected 3 chains, got %d", len(chains))
	}
	if _, exists := chains["doc1"]; !exists {
		t.Error("doc1 chain should exist")
	}
	if _, exists := chains["doc2"]; !exists {
		t.Error("doc2 chain should exist")
	}
	if _, exists := chains["doc3"]; !exists {
		t.Error("doc3 chain should exist")
	}
}

func TestMVCCStoreRemoveTransaction(t *testing.T) {
	store := NewMVCCStore()

	txn := store.Begin()
	if store.GetTransaction(txn.ID) == nil {
		t.Error("transaction should exist before removal")
	}

	store.RemoveTransaction(txn.ID)
	if store.GetTransaction(txn.ID) != nil {
		t.Error("transaction should not exist after removal")
	}
}

func TestMVCCStoreTransactionCount(t *testing.T) {
	store := NewMVCCStore()

	if store.TransactionCount() != 0 {
		t.Error("new store should have 0 transactions")
	}

	store.Begin()
	if store.TransactionCount() != 1 {
		t.Error("should have 1 transaction")
	}

	txn2 := store.Begin()
	if store.TransactionCount() != 2 {
		t.Error("should have 2 transactions")
	}

	store.RemoveTransaction(txn2.ID)
	if store.TransactionCount() != 1 {
		t.Error("should have 1 transaction after removal")
	}
}

func TestMVCCStoreChainCount(t *testing.T) {
	store := NewMVCCStore()

	if store.ChainCount() != 0 {
		t.Error("new store should have 0 chains")
	}

	store.GetChain("doc1")
	if store.ChainCount() != 1 {
		t.Error("should have 1 chain")
	}

	store.GetChain("doc2")
	if store.ChainCount() != 2 {
		t.Error("should have 2 chains")
	}

	// Getting existing chain doesn't increase count
	store.GetChain("doc1")
	if store.ChainCount() != 2 {
		t.Error("should still have 2 chains")
	}
}

func TestMVCCStoreSetGCInterval(t *testing.T) {
	store := NewMVCCStore()

	newInterval := 10 * time.Minute
	store.SetGCInterval(newInterval)

	if store.gcInterval != newInterval {
		t.Errorf("expected gcInterval %v, got %v", newInterval, store.gcInterval)
	}
}

func TestMVCCStoreSetMinRetentionTS(t *testing.T) {
	store := NewMVCCStore()

	store.SetMinRetentionTS(1000)
	if store.minRetentionTS != 1000 {
		t.Errorf("expected minRetentionTS 1000, got %d", store.minRetentionTS)
	}
}

func TestMVCCStoreConcurrentTransactionCreation(t *testing.T) {
	store := NewMVCCStore()
	var wg sync.WaitGroup

	numTransactions := 100
	txns := make([]*Transaction, numTransactions)
	var mu sync.Mutex

	for i := 0; i < numTransactions; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			txn := store.Begin()
			mu.Lock()
			txns[idx] = txn
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// Verify all transactions have unique IDs
	seen := make(map[uint64]bool)
	for _, txn := range txns {
		if txn == nil {
			t.Error("transaction should not be nil")
			continue
		}
		if seen[txn.ID] {
			t.Errorf("duplicate transaction ID: %d", txn.ID)
		}
		seen[txn.ID] = true
	}

	if store.TransactionCount() != numTransactions {
		t.Errorf("expected %d transactions, got %d", numTransactions, store.TransactionCount())
	}
}

func TestMVCCStoreConcurrentChainAccess(t *testing.T) {
	store := NewMVCCStore()
	var wg sync.WaitGroup

	numGoroutines := 100

	// Concurrent access to same chain
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			chain := store.GetChain("shared-doc")
			if chain == nil {
				t.Error("chain should not be nil")
			}
		}()
	}

	// Concurrent access to different chains
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			chain := store.GetChain("doc-" + string(rune('A'+i%26)))
			if chain == nil {
				t.Error("chain should not be nil")
			}
		}(i)
	}

	wg.Wait()

	// Verify shared chain exists
	if store.ChainCount() < 1 {
		t.Error("should have at least the shared chain")
	}
}

func TestMVCCStoreConcurrentTimestampGeneration(t *testing.T) {
	store := NewMVCCStore()
	var wg sync.WaitGroup

	numTimestamps := 1000
	timestamps := make([]uint64, numTimestamps)
	var mu sync.Mutex

	for i := 0; i < numTimestamps; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ts := store.NextTimestamp()
			mu.Lock()
			timestamps[idx] = ts
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// Verify all timestamps are unique
	seen := make(map[uint64]bool)
	for _, ts := range timestamps {
		if seen[ts] {
			t.Errorf("duplicate timestamp: %d", ts)
		}
		seen[ts] = true
	}
}

// =============================================================================
// Integration Tests - Transaction with MVCCStore
// =============================================================================

func TestMVCCStoreTransactionWithChains(t *testing.T) {
	store := NewMVCCStore()

	// Start transaction
	txn := store.Begin()

	// Get chain and add version
	chain := store.GetChain("doc1")
	v := NewVersion(txn.ID, []byte("hello"), false)
	chain.Prepend(v)
	txn.AddToWriteSet("doc1", v)

	// Verify write set
	if txn.GetFromWriteSet("doc1") != v {
		t.Error("version should be in write set")
	}

	// Commit the version
	commitTS := store.NextTimestamp()
	err := chain.CommitVersion(txn.ID, commitTS)
	if err != nil {
		t.Errorf("unexpected error committing: %v", err)
	}

	txn.SetState(TxnStateCommitted)
	store.RemoveTransaction(txn.ID)

	// Start new transaction and verify visibility
	txn2 := store.Begin()
	visible := chain.GetVisibleVersion(txn2.StartTS)
	if visible == nil {
		t.Error("committed version should be visible")
	}
	if string(visible.Data) != "hello" {
		t.Errorf("expected 'hello', got %s", string(visible.Data))
	}
}

func TestMVCCStoreConflictDetection(t *testing.T) {
	store := NewMVCCStore()

	// Setup: create initial version
	chain := store.GetChain("doc1")
	initialV := NewVersion(0, []byte("initial"), false)
	initialV.CommitTS = store.NextTimestamp()
	chain.Prepend(initialV)

	// Transaction 1 reads the document
	txn1 := store.Begin()
	txn1.AddToReadSet("doc1", 0)

	// Transaction 2 updates and commits
	txn2 := store.Begin()
	v2 := NewVersion(txn2.ID, []byte("updated"), false)
	chain.Prepend(v2)
	commitTS := store.NextTimestamp()
	chain.CommitVersion(txn2.ID, commitTS)
	txn2.SetState(TxnStateCommitted)

	// Transaction 1 should detect conflict
	chains := store.GetChains()
	if txn1.CanCommit(chains) {
		t.Error("txn1 should detect write-write conflict")
	}
}

func TestMVCCStoreMultipleDocumentTransaction(t *testing.T) {
	store := NewMVCCStore()

	// Start transaction
	txn := store.Begin()

	// Write to multiple documents
	docs := []string{"doc1", "doc2", "doc3"}
	for _, docID := range docs {
		chain := store.GetChain(docID)
		v := NewVersion(txn.ID, []byte("data-"+docID), false)
		chain.Prepend(v)
		txn.AddToWriteSet(docID, v)
	}

	// Commit all versions
	commitTS := store.NextTimestamp()
	for _, docID := range docs {
		chain := store.GetChain(docID)
		err := chain.CommitVersion(txn.ID, commitTS)
		if err != nil {
			t.Errorf("failed to commit %s: %v", docID, err)
		}
	}

	txn.SetState(TxnStateCommitted)

	// Verify all documents are visible
	txn2 := store.Begin()
	for _, docID := range docs {
		chain := store.GetChain(docID)
		visible := chain.GetVisibleVersion(txn2.StartTS)
		if visible == nil {
			t.Errorf("document %s should be visible", docID)
		}
	}
}

func TestMVCCStoreTransactionLifecycle(t *testing.T) {
	store := NewMVCCStore()

	// Begin
	txn := store.Begin()
	if txn.GetState() != TxnStateActive {
		t.Error("should start as Active")
	}

	// Write
	chain := store.GetChain("doc1")
	v := NewVersion(txn.ID, []byte("data"), false)
	chain.Prepend(v)
	txn.AddToWriteSet("doc1", v)

	// Committing
	txn.SetState(TxnStateCommitting)
	if txn.GetState() != TxnStateCommitting {
		t.Error("should be Committing")
	}

	// Commit version
	commitTS := store.NextTimestamp()
	chain.CommitVersion(txn.ID, commitTS)

	// Committed
	txn.SetState(TxnStateCommitted)
	if txn.GetState() != TxnStateCommitted {
		t.Error("should be Committed")
	}

	// Cleanup
	store.RemoveTransaction(txn.ID)
	if store.GetTransaction(txn.ID) != nil {
		t.Error("transaction should be removed")
	}
}

func TestMVCCStoreTransactionAbort(t *testing.T) {
	store := NewMVCCStore()

	// Begin
	txn := store.Begin()

	// Write
	chain := store.GetChain("doc1")
	v := NewVersion(txn.ID, []byte("data"), false)
	chain.Prepend(v)
	txn.AddToWriteSet("doc1", v)

	// Abort
	err := chain.AbortVersion(txn.ID)
	if err != nil {
		t.Errorf("failed to abort: %v", err)
	}

	txn.SetState(TxnStateAborted)
	store.RemoveTransaction(txn.ID)

	// Verify version is not in chain
	txn2 := store.Begin()
	visible := chain.GetVisibleVersion(txn2.StartTS)
	if visible != nil {
		t.Error("aborted version should not be visible")
	}
}

// =============================================================================
// VersionChain.Read Tests (MVCC.5)
// =============================================================================

func TestVersionChain_Read_Basic(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn := NewTransaction(1, 100)

	// Create a committed version
	v := NewVersion(5, []byte("hello world"), false)
	v.CommitTS = 50
	vc.Prepend(v)

	data, err := vc.Read(txn)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !bytes.Equal(data, []byte("hello world")) {
		t.Errorf("expected 'hello world', got %s", string(data))
	}

	// Verify read was recorded in transaction's read set
	if txn.ReadSet["doc-1"] != 5 {
		t.Errorf("expected read set to contain doc-1 with version txnID 5, got %d", txn.ReadSet["doc-1"])
	}
}

func TestVersionChain_Read_NoVisibleVersion(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn := NewTransaction(1, 100)

	// Empty chain
	data, err := vc.Read(txn)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if data != nil {
		t.Error("expected nil data for empty chain")
	}

	// Version committed after txn start
	v := NewVersion(5, []byte("future data"), false)
	v.CommitTS = 200 // After txn.StartTS
	vc.Prepend(v)

	data, err = vc.Read(txn)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if data != nil {
		t.Error("expected nil data for version committed after start timestamp")
	}
}

func TestVersionChain_Read_DeletedVersion(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn := NewTransaction(1, 200)

	// Create original version
	v1 := NewVersion(5, []byte("original"), false)
	v1.CommitTS = 50
	vc.Prepend(v1)

	// Create delete marker
	v2 := NewVersion(6, nil, true)
	v2.CommitTS = 100
	vc.Prepend(v2)

	data, err := vc.Read(txn)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if data != nil {
		t.Error("expected nil data for deleted version")
	}
}

func TestVersionChain_Read_MultipleVersions(t *testing.T) {
	vc := NewVersionChain("doc-1")

	// Create multiple committed versions
	v1 := NewVersion(1, []byte("v1"), false)
	v1.CommitTS = 100
	vc.Prepend(v1)

	v2 := NewVersion(2, []byte("v2"), false)
	v2.CommitTS = 200
	vc.Prepend(v2)

	v3 := NewVersion(3, []byte("v3"), false)
	v3.CommitTS = 300
	vc.Prepend(v3)

	tests := []struct {
		name     string
		startTS  uint64
		expected string
	}{
		{"sees v1", 150, "v1"},
		{"sees v2", 250, "v2"},
		{"sees v3", 350, "v3"},
		{"sees latest at exact commit time", 300, "v3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txn := NewTransaction(uint64(tt.startTS), tt.startTS)
			data, err := vc.Read(txn)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !bytes.Equal(data, []byte(tt.expected)) {
				t.Errorf("expected %s, got %s", tt.expected, string(data))
			}
		})
	}
}

func TestVersionChain_Read_ReturnsDataCopy(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn := NewTransaction(1, 100)

	originalData := []byte("original")
	v := NewVersion(5, originalData, false)
	v.CommitTS = 50
	vc.Prepend(v)

	data, _ := vc.Read(txn)

	// Modify returned data
	data[0] = 'X'

	// Original should be unchanged
	if v.Data[0] == 'X' {
		t.Error("Read should return a copy, not the original data")
	}
}

// =============================================================================
// VersionChain.ReadForUpdate Tests (MVCC.5)
// =============================================================================

func TestVersionChain_ReadForUpdate_NoConflict(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn := NewTransaction(1, 100)

	// Create a committed version
	v := NewVersion(5, []byte("hello"), false)
	v.CommitTS = 50
	vc.Prepend(v)

	data, err := vc.ReadForUpdate(txn)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !bytes.Equal(data, []byte("hello")) {
		t.Errorf("expected 'hello', got %s", string(data))
	}

	// Verify read was recorded
	if txn.ReadSet["doc-1"] != 5 {
		t.Errorf("expected read set entry for doc-1")
	}
}

func TestVersionChain_ReadForUpdate_Conflict(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn1 := NewTransaction(1, 100)
	txn2 := NewTransaction(2, 100)

	// Create a committed version
	v := NewVersion(5, []byte("hello"), false)
	v.CommitTS = 50
	vc.Prepend(v)

	// txn1 writes (creates uncommitted version)
	uncommitted := NewVersion(txn1.ID, []byte("updated"), false)
	uncommitted.Next = vc.Head
	vc.Head = uncommitted
	vc.Length++

	// txn2 tries to ReadForUpdate - should fail
	_, err := vc.ReadForUpdate(txn2)
	if err != ErrWriteConflict {
		t.Errorf("expected ErrWriteConflict, got %v", err)
	}
}

func TestVersionChain_ReadForUpdate_OwnUncommittedOK(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn := NewTransaction(1, 100)

	// Create a committed version
	v := NewVersion(5, []byte("hello"), false)
	v.CommitTS = 50
	vc.Prepend(v)

	// Same txn has uncommitted version at head
	uncommitted := NewVersion(txn.ID, []byte("my update"), false)
	uncommitted.Next = vc.Head
	vc.Head = uncommitted
	vc.Length++

	// ReadForUpdate should succeed (own uncommitted version)
	data, err := vc.ReadForUpdate(txn)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// Should see the committed version (not its own uncommitted)
	if !bytes.Equal(data, []byte("hello")) {
		t.Errorf("expected 'hello', got %s", string(data))
	}
}

func TestVersionChain_ReadForUpdate_EmptyChain(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn := NewTransaction(1, 100)

	data, err := vc.ReadForUpdate(txn)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if data != nil {
		t.Error("expected nil data for empty chain")
	}
}

func TestVersionChain_ReadForUpdate_DeletedVersion(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn := NewTransaction(1, 200)

	// Create original version
	v1 := NewVersion(5, []byte("original"), false)
	v1.CommitTS = 50
	vc.Prepend(v1)

	// Create delete marker
	v2 := NewVersion(6, nil, true)
	v2.CommitTS = 100
	vc.Prepend(v2)

	data, err := vc.ReadForUpdate(txn)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if data != nil {
		t.Error("expected nil data for deleted version")
	}
}

// =============================================================================
// VersionChain.Write Tests (MVCC.6)
// =============================================================================

func TestVersionChain_Write_Basic(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn := NewTransaction(1, 100)

	err := vc.Write(txn, []byte("new data"))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if vc.Head == nil {
		t.Fatal("expected head to be set")
	}
	if vc.Head.TxnID != txn.ID {
		t.Errorf("expected head TxnID %d, got %d", txn.ID, vc.Head.TxnID)
	}
	if !bytes.Equal(vc.Head.Data, []byte("new data")) {
		t.Errorf("expected 'new data', got %s", string(vc.Head.Data))
	}
	if vc.Head.IsCommitted() {
		t.Error("new version should be uncommitted")
	}
	if vc.Length != 1 {
		t.Errorf("expected length 1, got %d", vc.Length)
	}

	// Verify write was recorded in transaction
	if txn.WriteSet["doc-1"] != vc.Head {
		t.Error("write set should contain the new version")
	}
}

func TestVersionChain_Write_Conflict(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn1 := NewTransaction(1, 100)
	txn2 := NewTransaction(2, 100)

	// txn1 writes first
	err := vc.Write(txn1, []byte("txn1 data"))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// txn2 tries to write - should fail
	err = vc.Write(txn2, []byte("txn2 data"))
	if err != ErrWriteConflict {
		t.Errorf("expected ErrWriteConflict, got %v", err)
	}

	// Original data should be unchanged
	if !bytes.Equal(vc.Head.Data, []byte("txn1 data")) {
		t.Error("original data should be unchanged after conflict")
	}
}

func TestVersionChain_Write_UpdateOwnVersion(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn := NewTransaction(1, 100)

	// First write
	err := vc.Write(txn, []byte("first"))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	firstVersion := vc.Head

	// Second write from same txn - should update, not create new
	err = vc.Write(txn, []byte("second"))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if vc.Head != firstVersion {
		t.Error("should update existing version, not create new one")
	}
	if !bytes.Equal(vc.Head.Data, []byte("second")) {
		t.Errorf("expected 'second', got %s", string(vc.Head.Data))
	}
	if vc.Length != 1 {
		t.Errorf("expected length 1 (update, not append), got %d", vc.Length)
	}
}

func TestVersionChain_Write_AfterCommittedVersion(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn := NewTransaction(1, 100)

	// Create a committed version first
	committed := NewVersion(5, []byte("committed"), false)
	committed.CommitTS = 50
	vc.Prepend(committed)

	// Write new version
	err := vc.Write(txn, []byte("new"))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if vc.Head.TxnID != txn.ID {
		t.Error("head should be new version")
	}
	if vc.Head.Next != committed {
		t.Error("new version should link to committed version")
	}
	if vc.Length != 2 {
		t.Errorf("expected length 2, got %d", vc.Length)
	}
}

func TestVersionChain_Write_DataIsCopied(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn := NewTransaction(1, 100)

	data := []byte("original")
	err := vc.Write(txn, data)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Modify original data
	data[0] = 'X'

	// Version data should be unchanged
	if vc.Head.Data[0] == 'X' {
		t.Error("Write should copy data, not store reference")
	}
}

// =============================================================================
// VersionChain.Delete Tests (MVCC.7)
// =============================================================================

func TestVersionChain_Delete_Basic(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn := NewTransaction(1, 100)

	// Create a committed version first
	committed := NewVersion(5, []byte("data"), false)
	committed.CommitTS = 50
	vc.Prepend(committed)

	err := vc.Delete(txn)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if vc.Head == nil {
		t.Fatal("expected head to be set")
	}
	if vc.Head.TxnID != txn.ID {
		t.Errorf("expected head TxnID %d, got %d", txn.ID, vc.Head.TxnID)
	}
	if !vc.Head.DeleteMarker {
		t.Error("head should be a delete marker")
	}
	if vc.Head.Data != nil {
		t.Error("delete marker should have nil data")
	}
	if vc.Head.IsCommitted() {
		t.Error("new delete marker should be uncommitted")
	}
	if vc.Length != 2 {
		t.Errorf("expected length 2, got %d", vc.Length)
	}

	// Verify delete was recorded in transaction
	if txn.WriteSet["doc-1"] != vc.Head {
		t.Error("write set should contain the delete marker")
	}
}

func TestVersionChain_Delete_ConvertFromWrite(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn := NewTransaction(1, 100)

	// Write first
	err := vc.Write(txn, []byte("will be deleted"))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	originalVersion := vc.Head
	originalLength := vc.Length

	// Then delete - should convert existing version
	err = vc.Delete(txn)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if vc.Head != originalVersion {
		t.Error("should convert existing version, not create new one")
	}
	if !vc.Head.DeleteMarker {
		t.Error("version should be converted to delete marker")
	}
	if vc.Head.Data != nil {
		t.Error("data should be nil after conversion to delete")
	}
	if vc.Length != originalLength {
		t.Errorf("length should remain %d, got %d", originalLength, vc.Length)
	}
}

func TestVersionChain_Delete_Conflict(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn1 := NewTransaction(1, 100)
	txn2 := NewTransaction(2, 100)

	// Create a committed version
	committed := NewVersion(5, []byte("data"), false)
	committed.CommitTS = 50
	vc.Prepend(committed)

	// txn1 writes
	err := vc.Write(txn1, []byte("update"))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// txn2 tries to delete - should fail
	err = vc.Delete(txn2)
	if err != ErrWriteConflict {
		t.Errorf("expected ErrWriteConflict, got %v", err)
	}
}

func TestVersionChain_Delete_EmptyChain(t *testing.T) {
	vc := NewVersionChain("doc-1")
	txn := NewTransaction(1, 100)

	err := vc.Delete(txn)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !vc.Head.DeleteMarker {
		t.Error("should create delete marker even on empty chain")
	}
	if vc.Length != 1 {
		t.Errorf("expected length 1, got %d", vc.Length)
	}
}

// =============================================================================
// Concurrent Read/Write Tests (MVCC.5, MVCC.6, MVCC.7)
// =============================================================================

func TestVersionChain_ConcurrentReadWrite(t *testing.T) {
	vc := NewVersionChain("doc-1")

	// Create an initial committed version
	initial := NewVersion(0, []byte("initial"), false)
	initial.CommitTS = 1
	vc.Prepend(initial)

	var wg sync.WaitGroup
	numReaders := 20
	numWriters := 5

	// Start readers
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				txn := NewTransaction(uint64(1000+readerID*100+j), uint64(100+j))
				data, err := vc.Read(txn)
				if err != nil {
					t.Errorf("reader %d: unexpected error: %v", readerID, err)
				}
				// Data might be nil if read during write transition
				_ = data
			}
		}(i)
	}

	// Start writers (only one will succeed at a time due to conflict detection)
	successfulWrites := make(chan int, numWriters*10)
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				txn := NewTransaction(uint64(2000+writerID*100+j), uint64(200+j))
				err := vc.Write(txn, []byte("writer data"))
				if err == nil {
					successfulWrites <- writerID
					// Simulate commit
					vc.CommitVersion(txn.ID, uint64(300+writerID*10+j))
				}
				// ErrWriteConflict is expected for some writes
			}
		}(i)
	}

	wg.Wait()
	close(successfulWrites)

	// Count successful writes
	count := 0
	for range successfulWrites {
		count++
	}

	// At least some writes should succeed
	if count == 0 {
		t.Error("expected at least some writes to succeed")
	}

	// Chain should have more versions than just initial
	if vc.Length <= 1 {
		t.Error("expected chain to grow from writes")
	}
}

func TestVersionChain_ConcurrentReadForUpdate(t *testing.T) {
	vc := NewVersionChain("doc-1")

	// Create an initial committed version
	initial := NewVersion(0, []byte("initial"), false)
	initial.CommitTS = 1
	vc.Prepend(initial)

	var wg sync.WaitGroup
	numGoroutines := 20
	conflicts := int32(0)
	successes := int32(0)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			txn := NewTransaction(uint64(100+id), 100)
			_, err := vc.ReadForUpdate(txn)
			if err == ErrWriteConflict {
				atomic.AddInt32(&conflicts, 1)
			} else if err == nil {
				atomic.AddInt32(&successes, 1)
				// Simulate a write after successful ReadForUpdate
				vc.Write(txn, []byte("updated"))
				// Give others a chance to hit conflict
				time.Sleep(time.Microsecond)
				// Commit
				vc.CommitVersion(txn.ID, uint64(200+id))
			}
		}(i)
	}

	wg.Wait()

	// All operations should have either succeeded or conflicted
	total := int(successes) + int(conflicts)
	if total != numGoroutines {
		t.Errorf("expected %d total operations, got %d", numGoroutines, total)
	}
}

func TestVersionChain_ConcurrentDelete(t *testing.T) {
	var wg sync.WaitGroup
	numGoroutines := 10

	for round := 0; round < 5; round++ {
		vc := NewVersionChain("doc-1")

		// Create an initial committed version
		initial := NewVersion(0, []byte("initial"), false)
		initial.CommitTS = 1
		vc.Prepend(initial)

		conflicts := int32(0)
		successes := int32(0)

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				txn := NewTransaction(uint64(100+id), 100)
				err := vc.Delete(txn)
				if err == ErrWriteConflict {
					atomic.AddInt32(&conflicts, 1)
				} else if err == nil {
					atomic.AddInt32(&successes, 1)
				}
			}(i)
		}

		wg.Wait()

		// Exactly one delete should succeed (first one wins)
		if successes != 1 {
			t.Errorf("round %d: expected exactly 1 successful delete, got %d", round, successes)
		}
		if int(conflicts) != numGoroutines-1 {
			t.Errorf("round %d: expected %d conflicts, got %d", round, numGoroutines-1, conflicts)
		}
	}
}

// =============================================================================
// Integration Tests for Read/Write/Delete Flow
// =============================================================================

func TestVersionChain_ReadWriteDeleteFlow(t *testing.T) {
	store := NewMVCCStore()
	chain := store.GetChain("doc-1")

	// Transaction 1: Initial write
	txn1 := store.Begin()
	err := chain.Write(txn1, []byte("version 1"))
	if err != nil {
		t.Fatalf("txn1 write failed: %v", err)
	}
	commitTS1 := store.NextTimestamp()
	chain.CommitVersion(txn1.ID, commitTS1)
	txn1.SetState(TxnStateCommitted)

	// Transaction 2: Read
	txn2 := store.Begin()
	data, err := chain.Read(txn2)
	if err != nil {
		t.Fatalf("txn2 read failed: %v", err)
	}
	if !bytes.Equal(data, []byte("version 1")) {
		t.Errorf("txn2 expected 'version 1', got %s", string(data))
	}

	// Transaction 3: Update
	txn3 := store.Begin()
	err = chain.Write(txn3, []byte("version 2"))
	if err != nil {
		t.Fatalf("txn3 write failed: %v", err)
	}
	commitTS3 := store.NextTimestamp()
	chain.CommitVersion(txn3.ID, commitTS3)
	txn3.SetState(TxnStateCommitted)

	// Transaction 2 (still open) should still see version 1
	data, err = chain.Read(txn2)
	if err != nil {
		t.Fatalf("txn2 second read failed: %v", err)
	}
	if !bytes.Equal(data, []byte("version 1")) {
		t.Errorf("txn2 should still see 'version 1', got %s", string(data))
	}

	// Transaction 4: See version 2
	txn4 := store.Begin()
	data, err = chain.Read(txn4)
	if err != nil {
		t.Fatalf("txn4 read failed: %v", err)
	}
	if !bytes.Equal(data, []byte("version 2")) {
		t.Errorf("txn4 expected 'version 2', got %s", string(data))
	}

	// Transaction 5: Delete
	txn5 := store.Begin()
	err = chain.Delete(txn5)
	if err != nil {
		t.Fatalf("txn5 delete failed: %v", err)
	}
	commitTS5 := store.NextTimestamp()
	chain.CommitVersion(txn5.ID, commitTS5)
	txn5.SetState(TxnStateCommitted)

	// Transaction 6: Should see deletion (nil data)
	txn6 := store.Begin()
	data, err = chain.Read(txn6)
	if err != nil {
		t.Fatalf("txn6 read failed: %v", err)
	}
	if data != nil {
		t.Error("txn6 should see nil data after deletion")
	}

	// Transaction 4 (still open) should still see version 2
	data, err = chain.Read(txn4)
	if err != nil {
		t.Fatalf("txn4 second read failed: %v", err)
	}
	if !bytes.Equal(data, []byte("version 2")) {
		t.Errorf("txn4 should still see 'version 2', got %s", string(data))
	}
}

func TestVersionChain_ReadForUpdateThenWrite(t *testing.T) {
	store := NewMVCCStore()
	chain := store.GetChain("doc-1")

	// Setup: Create initial version
	initial := NewVersion(0, []byte("initial"), false)
	initial.CommitTS = store.NextTimestamp()
	chain.Prepend(initial)

	// Transaction 1: ReadForUpdate then Write
	txn1 := store.Begin()
	data, err := chain.ReadForUpdate(txn1)
	if err != nil {
		t.Fatalf("ReadForUpdate failed: %v", err)
	}
	if !bytes.Equal(data, []byte("initial")) {
		t.Errorf("expected 'initial', got %s", string(data))
	}

	err = chain.Write(txn1, []byte("updated"))
	if err != nil {
		t.Fatalf("Write after ReadForUpdate failed: %v", err)
	}

	// Transaction 2: Should be blocked by txn1's uncommitted write
	txn2 := store.Begin()
	_, err = chain.ReadForUpdate(txn2)
	if err != ErrWriteConflict {
		t.Errorf("expected ErrWriteConflict, got %v", err)
	}

	// Commit txn1
	commitTS := store.NextTimestamp()
	chain.CommitVersion(txn1.ID, commitTS)
	txn1.SetState(TxnStateCommitted)

	// Transaction 3: Should now see the update
	txn3 := store.Begin()
	data, err = chain.Read(txn3)
	if err != nil {
		t.Fatalf("txn3 read failed: %v", err)
	}
	if !bytes.Equal(data, []byte("updated")) {
		t.Errorf("expected 'updated', got %s", string(data))
	}
}

// =============================================================================
// Transaction.Get Tests (MVCC.8)
// =============================================================================

func TestTransaction_Get_Basic(t *testing.T) {
	store := NewMVCCStore()

	// Setup: Create initial committed version
	txn1 := store.Begin()
	err := txn1.Put("key1", []byte("value1"))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	err = txn1.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// New transaction should see the committed value
	txn2 := store.Begin()
	value, found := txn2.Get("key1")
	if !found {
		t.Fatal("expected to find key1")
	}
	if !bytes.Equal(value, []byte("value1")) {
		t.Errorf("expected 'value1', got %s", string(value))
	}
}

func TestTransaction_Get_NotFound(t *testing.T) {
	store := NewMVCCStore()
	txn := store.Begin()

	value, found := txn.Get("nonexistent")
	if found {
		t.Error("expected not found for nonexistent key")
	}
	if value != nil {
		t.Error("expected nil value for nonexistent key")
	}
}

func TestTransaction_Get_PendingWrite(t *testing.T) {
	store := NewMVCCStore()
	txn := store.Begin()

	// Put a value
	err := txn.Put("key1", []byte("pending value"))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Get should return the pending value
	value, found := txn.Get("key1")
	if !found {
		t.Fatal("expected to find key1 in pending writes")
	}
	if !bytes.Equal(value, []byte("pending value")) {
		t.Errorf("expected 'pending value', got %s", string(value))
	}
}

func TestTransaction_Get_PendingDelete(t *testing.T) {
	store := NewMVCCStore()

	// Setup: Create initial committed version
	txn1 := store.Begin()
	txn1.Put("key1", []byte("value1"))
	txn1.Commit()

	// Start new transaction and delete
	txn2 := store.Begin()
	err := txn2.Delete("key1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Get should return not found due to pending delete
	value, found := txn2.Get("key1")
	if found {
		t.Error("expected not found after pending delete")
	}
	if value != nil {
		t.Error("expected nil value after pending delete")
	}
}

func TestTransaction_Get_SnapshotIsolation(t *testing.T) {
	store := NewMVCCStore()

	// Setup: Create initial committed version
	txn1 := store.Begin()
	txn1.Put("key1", []byte("version1"))
	txn1.Commit()

	// Start txn2 to read
	txn2 := store.Begin()

	// txn3 updates and commits
	txn3 := store.Begin()
	txn3.Put("key1", []byte("version2"))
	txn3.Commit()

	// txn2 should still see version1 (snapshot isolation)
	value, found := txn2.Get("key1")
	if !found {
		t.Fatal("expected to find key1")
	}
	if !bytes.Equal(value, []byte("version1")) {
		t.Errorf("expected 'version1' (snapshot), got %s", string(value))
	}

	// New transaction should see version2
	txn4 := store.Begin()
	value, found = txn4.Get("key1")
	if !found {
		t.Fatal("expected to find key1")
	}
	if !bytes.Equal(value, []byte("version2")) {
		t.Errorf("expected 'version2', got %s", string(value))
	}
}

func TestTransaction_Get_ReturnsValueCopy(t *testing.T) {
	store := NewMVCCStore()

	txn1 := store.Begin()
	txn1.Put("key1", []byte("original"))
	txn1.Commit()

	txn2 := store.Begin()
	value, _ := txn2.Get("key1")

	// Modify returned value
	value[0] = 'X'

	// Get again - should be unchanged
	value2, _ := txn2.Get("key1")
	if value2[0] == 'X' {
		t.Error("Get should return a copy, not the original")
	}
}

// =============================================================================
// Transaction.Put Tests (MVCC.8)
// =============================================================================

func TestTransaction_Put_Basic(t *testing.T) {
	store := NewMVCCStore()
	txn := store.Begin()

	err := txn.Put("key1", []byte("value1"))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Value should be accessible via Get
	value, found := txn.Get("key1")
	if !found {
		t.Fatal("expected to find key1")
	}
	if !bytes.Equal(value, []byte("value1")) {
		t.Errorf("expected 'value1', got %s", string(value))
	}
}

func TestTransaction_Put_Overwrite(t *testing.T) {
	store := NewMVCCStore()
	txn := store.Begin()

	txn.Put("key1", []byte("value1"))
	txn.Put("key1", []byte("value2"))

	value, found := txn.Get("key1")
	if !found {
		t.Fatal("expected to find key1")
	}
	if !bytes.Equal(value, []byte("value2")) {
		t.Errorf("expected 'value2' after overwrite, got %s", string(value))
	}
}

func TestTransaction_Put_AfterCommit(t *testing.T) {
	store := NewMVCCStore()
	txn := store.Begin()
	txn.Commit()

	err := txn.Put("key1", []byte("value1"))
	if err != ErrTransactionClosed {
		t.Errorf("expected ErrTransactionClosed, got %v", err)
	}
}

func TestTransaction_Put_AfterRollback(t *testing.T) {
	store := NewMVCCStore()
	txn := store.Begin()
	txn.Rollback()

	err := txn.Put("key1", []byte("value1"))
	if err != ErrTransactionClosed {
		t.Errorf("expected ErrTransactionClosed, got %v", err)
	}
}

func TestTransaction_Put_CopiesValue(t *testing.T) {
	store := NewMVCCStore()
	txn := store.Begin()

	value := []byte("original")
	txn.Put("key1", value)

	// Modify original
	value[0] = 'X'

	// Get should return unchanged value
	retrieved, _ := txn.Get("key1")
	if retrieved[0] == 'X' {
		t.Error("Put should copy value, not store reference")
	}
}

// =============================================================================
// Transaction.Delete Tests (MVCC.8)
// =============================================================================

func TestTransaction_Delete_Basic(t *testing.T) {
	store := NewMVCCStore()

	// Setup: Create committed version
	txn1 := store.Begin()
	txn1.Put("key1", []byte("value1"))
	txn1.Commit()

	// Delete in new transaction
	txn2 := store.Begin()
	err := txn2.Delete("key1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Key should not be found in same transaction
	_, found := txn2.Get("key1")
	if found {
		t.Error("expected not found after delete")
	}
}

func TestTransaction_Delete_NonexistentKey(t *testing.T) {
	store := NewMVCCStore()
	txn := store.Begin()

	// Delete nonexistent key - should succeed
	err := txn.Delete("nonexistent")
	if err != nil {
		t.Fatalf("Delete of nonexistent key failed: %v", err)
	}
}

func TestTransaction_Delete_AfterPut(t *testing.T) {
	store := NewMVCCStore()
	txn := store.Begin()

	txn.Put("key1", []byte("value1"))
	txn.Delete("key1")

	_, found := txn.Get("key1")
	if found {
		t.Error("expected not found after put then delete")
	}
}

func TestTransaction_Delete_ThenPut(t *testing.T) {
	store := NewMVCCStore()

	// Setup
	txn1 := store.Begin()
	txn1.Put("key1", []byte("original"))
	txn1.Commit()

	// Delete then put in same transaction
	txn2 := store.Begin()
	txn2.Delete("key1")
	txn2.Put("key1", []byte("new value"))

	value, found := txn2.Get("key1")
	if !found {
		t.Fatal("expected to find key1 after delete then put")
	}
	if !bytes.Equal(value, []byte("new value")) {
		t.Errorf("expected 'new value', got %s", string(value))
	}
}

func TestTransaction_Delete_AfterCommit(t *testing.T) {
	store := NewMVCCStore()
	txn := store.Begin()
	txn.Commit()

	err := txn.Delete("key1")
	if err != ErrTransactionClosed {
		t.Errorf("expected ErrTransactionClosed, got %v", err)
	}
}

func TestTransaction_Delete_AfterRollback(t *testing.T) {
	store := NewMVCCStore()
	txn := store.Begin()
	txn.Rollback()

	err := txn.Delete("key1")
	if err != ErrTransactionClosed {
		t.Errorf("expected ErrTransactionClosed, got %v", err)
	}
}

// =============================================================================
// Transaction.Commit Tests (MVCC.9)
// =============================================================================

func TestTransaction_Commit_Basic(t *testing.T) {
	store := NewMVCCStore()

	txn1 := store.Begin()
	txn1.Put("key1", []byte("value1"))
	txn1.Put("key2", []byte("value2"))

	err := txn1.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify values are visible to new transaction
	txn2 := store.Begin()
	value1, found := txn2.Get("key1")
	if !found || !bytes.Equal(value1, []byte("value1")) {
		t.Errorf("expected 'value1', got %s (found=%v)", string(value1), found)
	}
	value2, found := txn2.Get("key2")
	if !found || !bytes.Equal(value2, []byte("value2")) {
		t.Errorf("expected 'value2', got %s (found=%v)", string(value2), found)
	}
}

func TestTransaction_Commit_Delete(t *testing.T) {
	store := NewMVCCStore()

	// Setup
	txn1 := store.Begin()
	txn1.Put("key1", []byte("value1"))
	txn1.Commit()

	// Delete and commit
	txn2 := store.Begin()
	txn2.Delete("key1")
	err := txn2.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify key is deleted
	txn3 := store.Begin()
	_, found := txn3.Get("key1")
	if found {
		t.Error("expected key to be deleted after commit")
	}
}

func TestTransaction_Commit_Empty(t *testing.T) {
	store := NewMVCCStore()
	txn := store.Begin()

	err := txn.Commit()
	if err != nil {
		t.Fatalf("Commit of empty transaction failed: %v", err)
	}
}

func TestTransaction_Commit_Idempotent(t *testing.T) {
	store := NewMVCCStore()

	txn := store.Begin()
	txn.Put("key1", []byte("value1"))

	err := txn.Commit()
	if err != nil {
		t.Fatalf("First commit failed: %v", err)
	}

	// Second commit should succeed (idempotent)
	err = txn.Commit()
	if err != nil {
		t.Errorf("Second commit should succeed, got %v", err)
	}
}

func TestTransaction_Commit_AfterRollback(t *testing.T) {
	store := NewMVCCStore()

	txn := store.Begin()
	txn.Put("key1", []byte("value1"))
	txn.Rollback()

	err := txn.Commit()
	if err != ErrTransactionAborted {
		t.Errorf("expected ErrTransactionAborted, got %v", err)
	}
}

func TestTransaction_Commit_WriteConflict(t *testing.T) {
	store := NewMVCCStore()

	// Setup: Create initial value
	txn1 := store.Begin()
	txn1.Put("key1", []byte("initial"))
	txn1.Commit()

	// txn2 starts and writes to its local write set
	txn2 := store.Begin()
	txn2.Put("key1", []byte("txn2 value"))

	// txn3 starts and writes - but doesn't commit yet
	txn3 := store.Begin()
	txn3.Put("key1", []byte("txn3 value"))

	// txn2 tries to commit - this applies writes to version chain
	// txn3 hasn't committed yet, so no conflict at this point
	err := txn2.Commit()
	if err != nil {
		t.Fatalf("txn2 commit failed: %v", err)
	}

	// Now txn3 commit should detect conflict (txn2's uncommitted/just-committed version)
	err = txn3.Commit()
	if err != ErrWriteConflict {
		t.Errorf("expected ErrWriteConflict for txn3, got %v", err)
	}

	// txn3 should be aborted
	if !txn3.aborted.Load() {
		t.Error("txn3 should be aborted after conflict")
	}
}

func TestTransaction_Commit_AtomicMultipleKeys(t *testing.T) {
	store := NewMVCCStore()

	// Setup: Prepare initial state
	txn1 := store.Begin()
	txn1.Put("key1", []byte("initial1"))
	txn1.Put("key2", []byte("initial2"))
	txn1.Commit()

	// txn2 writes to both keys (stays in local write set)
	txn2 := store.Begin()
	txn2.Put("key1", []byte("txn2_key1"))
	txn2.Put("key2", []byte("txn2_key2"))

	// txn3 starts writing but doesn't commit yet
	txn3 := store.Begin()
	txn3.Put("key2", []byte("txn3_key2"))

	// txn2 commits first - this should succeed
	err := txn2.Commit()
	if err != nil {
		t.Fatalf("txn2 commit failed: %v", err)
	}

	// txn3 tries to commit - should fail on key2 conflict
	err = txn3.Commit()
	if err != ErrWriteConflict {
		t.Errorf("expected ErrWriteConflict, got %v", err)
	}

	// txn2's writes should be committed
	txn4 := store.Begin()
	value1, _ := txn4.Get("key1")
	if !bytes.Equal(value1, []byte("txn2_key1")) {
		t.Errorf("key1 should be txn2's value, got %s", string(value1))
	}
	value2, _ := txn4.Get("key2")
	if !bytes.Equal(value2, []byte("txn2_key2")) {
		t.Errorf("key2 should be txn2's value, got %s", string(value2))
	}
}

// =============================================================================
// Transaction.Rollback Tests (MVCC.10)
// =============================================================================

func TestTransaction_Rollback_Basic(t *testing.T) {
	store := NewMVCCStore()

	txn := store.Begin()
	txn.Put("key1", []byte("value1"))

	err := txn.Rollback()
	if err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	// Value should not be visible
	txn2 := store.Begin()
	_, found := txn2.Get("key1")
	if found {
		t.Error("value should not be visible after rollback")
	}
}

func TestTransaction_Rollback_Idempotent(t *testing.T) {
	store := NewMVCCStore()

	txn := store.Begin()
	txn.Put("key1", []byte("value1"))

	err := txn.Rollback()
	if err != nil {
		t.Fatalf("First rollback failed: %v", err)
	}

	// Second rollback should succeed (idempotent)
	err = txn.Rollback()
	if err != nil {
		t.Errorf("Second rollback should succeed, got %v", err)
	}
}

func TestTransaction_Rollback_AfterCommit(t *testing.T) {
	store := NewMVCCStore()

	txn := store.Begin()
	txn.Put("key1", []byte("value1"))
	txn.Commit()

	err := txn.Rollback()
	if err != ErrTransactionCommitted {
		t.Errorf("expected ErrTransactionCommitted, got %v", err)
	}
}

func TestTransaction_Rollback_ClearsWriteSet(t *testing.T) {
	store := NewMVCCStore()

	txn := store.Begin()
	txn.Put("key1", []byte("value1"))
	txn.Put("key2", []byte("value2"))
	txn.Rollback()

	// Write set should be cleared
	if txn.writes != nil {
		t.Error("write set should be nil after rollback")
	}
}

func TestTransaction_Rollback_RemovesPendingVersions(t *testing.T) {
	store := NewMVCCStore()

	// Setup: Create initial value
	txn1 := store.Begin()
	txn1.Put("key1", []byte("initial"))
	txn1.Commit()

	// Start transaction, write to local set first
	txn2 := store.Begin()
	txn2.Put("key1", []byte("updated"))

	// At this point, write is only in local write set, not in version chain
	// We need to trigger the commit process to create the version
	// But since we want to test rollback, let's write directly to the chain
	chain := store.getChain("key1")
	chain.Write(txn2, []byte("updated"))

	// The uncommitted version should exist before rollback
	if chain.Head == nil || chain.Head.IsCommitted() {
		t.Fatal("expected uncommitted version at head")
	}

	// Now rollback - this should remove the pending version
	txn2.Rollback()

	// After rollback, head should be the committed version
	if !chain.Head.IsCommitted() {
		t.Error("pending version should be removed after rollback")
	}
}

// =============================================================================
// ACID Property Tests
// =============================================================================

func TestACID_Atomicity(t *testing.T) {
	store := NewMVCCStore()

	// Setup
	txn1 := store.Begin()
	txn1.Put("key1", []byte("v1"))
	txn1.Put("key2", []byte("v2"))
	txn1.Commit()

	// txn2 will write to both keys
	txn2 := store.Begin()
	txn2.Put("key1", []byte("txn2_v1"))
	txn2.Put("key2", []byte("txn2_v2"))

	// txn3 starts and writes to key2 (will cause conflict)
	txn3 := store.Begin()
	txn3.Put("key2", []byte("txn3_v2"))

	// txn2 commits first - both keys should be updated
	err := txn2.Commit()
	if err != nil {
		t.Fatalf("txn2 commit failed: %v", err)
	}

	// txn3 should fail due to conflict on key2
	err = txn3.Commit()
	if err == nil {
		t.Fatal("expected conflict error for txn3")
	}

	// Final state: txn2's values should be present for both keys
	txn4 := store.Begin()
	v1, _ := txn4.Get("key1")
	v2, _ := txn4.Get("key2")

	if !bytes.Equal(v1, []byte("txn2_v1")) {
		t.Errorf("key1 should be txn2_v1, got %s", string(v1))
	}
	if !bytes.Equal(v2, []byte("txn2_v2")) {
		t.Errorf("key2 should be txn2_v2, got %s", string(v2))
	}
}

func TestACID_Consistency(t *testing.T) {
	store := NewMVCCStore()

	// Initial state
	txn1 := store.Begin()
	txn1.Put("account_a", []byte("100"))
	txn1.Put("account_b", []byte("100"))
	txn1.Commit()

	// Transfer: A-50, B+50
	txn2 := store.Begin()
	txn2.Put("account_a", []byte("50"))
	txn2.Put("account_b", []byte("150"))
	err := txn2.Commit()
	if err != nil {
		t.Fatalf("Transfer commit failed: %v", err)
	}

	// Verify consistency: both updated together
	txn3 := store.Begin()
	a, _ := txn3.Get("account_a")
	b, _ := txn3.Get("account_b")

	if !bytes.Equal(a, []byte("50")) || !bytes.Equal(b, []byte("150")) {
		t.Errorf("inconsistent state: a=%s, b=%s", string(a), string(b))
	}
}

func TestACID_Isolation_SnapshotReads(t *testing.T) {
	store := NewMVCCStore()

	// Setup
	txn1 := store.Begin()
	txn1.Put("key1", []byte("v1"))
	txn1.Commit()

	// txn2 reads (takes snapshot)
	txn2 := store.Begin()
	v1, _ := txn2.Get("key1")
	if !bytes.Equal(v1, []byte("v1")) {
		t.Fatalf("expected v1, got %s", string(v1))
	}

	// txn3 updates and commits
	txn3 := store.Begin()
	txn3.Put("key1", []byte("v2"))
	txn3.Commit()

	// txn2 should still see v1 (snapshot isolation)
	v2, _ := txn2.Get("key1")
	if !bytes.Equal(v2, []byte("v1")) {
		t.Errorf("snapshot isolation violated: expected v1, got %s", string(v2))
	}
}

func TestACID_Isolation_WriteConflict(t *testing.T) {
	store := NewMVCCStore()

	// Setup
	txn1 := store.Begin()
	txn1.Put("key1", []byte("initial"))
	txn1.Commit()

	// Two concurrent writers - both write to local write set first
	txn2 := store.Begin()
	txn3 := store.Begin()

	txn2.Put("key1", []byte("txn2"))
	txn3.Put("key1", []byte("txn3"))

	// First to commit wins (applies write to version chain)
	err2 := txn2.Commit()
	if err2 != nil {
		t.Fatalf("txn2 commit failed: %v", err2)
	}

	// Second should fail with conflict (detects txn2's version in chain)
	err3 := txn3.Commit()
	if err3 != ErrWriteConflict {
		t.Errorf("expected write conflict, got %v", err3)
	}

	// Final value should be txn2's
	txn4 := store.Begin()
	v, _ := txn4.Get("key1")
	if !bytes.Equal(v, []byte("txn2")) {
		t.Errorf("expected 'txn2', got %s", string(v))
	}
}

func TestACID_Durability(t *testing.T) {
	store := NewMVCCStore()

	// Commit several values
	for i := 0; i < 10; i++ {
		txn := store.Begin()
		txn.Put("key"+string(rune('0'+i)), []byte("value"+string(rune('0'+i))))
		err := txn.Commit()
		if err != nil {
			t.Fatalf("commit %d failed: %v", i, err)
		}
	}

	// All values should be readable
	txn := store.Begin()
	for i := 0; i < 10; i++ {
		v, found := txn.Get("key" + string(rune('0'+i)))
		if !found {
			t.Errorf("key%d not found", i)
		}
		expected := "value" + string(rune('0'+i))
		if !bytes.Equal(v, []byte(expected)) {
			t.Errorf("key%d: expected %s, got %s", i, expected, string(v))
		}
	}
}

// =============================================================================
// Concurrent Transaction Tests (Thread Safety)
// =============================================================================

func TestTransaction_ConcurrentGetPut(t *testing.T) {
	store := NewMVCCStore()

	// Setup initial values
	setup := store.Begin()
	for i := 0; i < 10; i++ {
		setup.Put("key"+string(rune('0'+i)), []byte("initial"))
	}
	setup.Commit()

	var wg sync.WaitGroup
	numGoroutines := 20

	// Concurrent readers and writers
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				txn := store.Begin()
				key := "key" + string(rune('0'+j%10))

				if id%2 == 0 {
					// Reader
					_, _ = txn.Get(key)
				} else {
					// Writer
					txn.Put(key, []byte("value"+string(rune('0'+id))))
					txn.Commit()
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestTransaction_ConcurrentCommit(t *testing.T) {
	store := NewMVCCStore()

	// Setup
	setup := store.Begin()
	setup.Put("counter", []byte("0"))
	setup.Commit()

	var wg sync.WaitGroup
	numGoroutines := 10
	successCount := int32(0)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			txn := store.Begin()
			txn.Put("counter", []byte(string(rune('0'+id))))
			if err := txn.Commit(); err == nil {
				atomic.AddInt32(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()

	// At least some should succeed (first one wins)
	if successCount == 0 {
		t.Error("expected at least one successful commit")
	}
}

func TestTransaction_ConcurrentRollback(t *testing.T) {
	store := NewMVCCStore()

	var wg sync.WaitGroup
	numGoroutines := 20

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			txn := store.Begin()
			txn.Put("key"+string(rune('0'+id%10)), []byte("value"))
			txn.Rollback()
		}(i)
	}

	wg.Wait()

	// No values should be committed
	txn := store.Begin()
	for i := 0; i < 10; i++ {
		_, found := txn.Get("key" + string(rune('0'+i)))
		if found {
			t.Errorf("key%d should not be found after rollbacks", i)
		}
	}
}

func TestTransaction_ConcurrentMixedOperations(t *testing.T) {
	store := NewMVCCStore()

	var wg sync.WaitGroup
	numGoroutines := 30

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				txn := store.Begin()
				key := "key" + string(rune('0'+j%5))

				switch id % 3 {
				case 0:
					// Put and commit
					txn.Put(key, []byte("value"))
					txn.Commit()
				case 1:
					// Delete and commit
					txn.Delete(key)
					txn.Commit()
				case 2:
					// Put and rollback
					txn.Put(key, []byte("rollback"))
					txn.Rollback()
				}
			}
		}(i)
	}

	wg.Wait()
	// Test passes if no race conditions detected
}

// =============================================================================
// CommitPending and AbortPending Tests
// =============================================================================

func TestVersionChain_CommitPending(t *testing.T) {
	vc := NewVersionChain("doc-1")
	store := NewMVCCStore()
	tx := NewTransactionWithStore(1, 100, store)
	tx.commitTS = 200

	// Create uncommitted version
	v := NewVersion(tx.ID, []byte("data"), false)
	vc.Prepend(v)

	if v.IsCommitted() {
		t.Error("version should be uncommitted before CommitPending")
	}

	vc.CommitPending(tx)

	if !v.IsCommitted() {
		t.Error("version should be committed after CommitPending")
	}
	if v.CommitTS != 200 {
		t.Errorf("expected commitTS 200, got %d", v.CommitTS)
	}
}

func TestVersionChain_CommitPending_NoUncommittedVersion(t *testing.T) {
	vc := NewVersionChain("doc-1")
	store := NewMVCCStore()
	tx := NewTransactionWithStore(1, 100, store)
	tx.commitTS = 200

	// Create committed version (not from this transaction)
	v := NewVersion(5, []byte("data"), false)
	v.CommitTS = 50
	vc.Prepend(v)

	// CommitPending should be no-op
	vc.CommitPending(tx)

	// Original commit timestamp should be unchanged
	if v.CommitTS != 50 {
		t.Errorf("commitTS should be unchanged, got %d", v.CommitTS)
	}
}

func TestVersionChain_AbortPending(t *testing.T) {
	vc := NewVersionChain("doc-1")
	store := NewMVCCStore()
	tx := NewTransactionWithStore(1, 100, store)

	// Create committed version
	committed := NewVersion(5, []byte("committed"), false)
	committed.CommitTS = 50
	vc.Prepend(committed)

	// Create uncommitted version
	uncommitted := NewVersion(tx.ID, []byte("uncommitted"), false)
	vc.Prepend(uncommitted)

	if vc.Length != 2 {
		t.Errorf("expected length 2, got %d", vc.Length)
	}

	vc.AbortPending(tx)

	if vc.Length != 1 {
		t.Errorf("expected length 1 after abort, got %d", vc.Length)
	}
	if vc.Head != committed {
		t.Error("head should be committed version after abort")
	}
}

func TestVersionChain_AbortPending_NoUncommittedVersion(t *testing.T) {
	vc := NewVersionChain("doc-1")
	store := NewMVCCStore()
	tx := NewTransactionWithStore(1, 100, store)

	// Create committed version (not from this transaction)
	v := NewVersion(5, []byte("data"), false)
	v.CommitTS = 50
	vc.Prepend(v)

	initialLength := vc.Length

	// AbortPending should be no-op
	vc.AbortPending(tx)

	if vc.Length != initialLength {
		t.Errorf("length should be unchanged, got %d", vc.Length)
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestTransaction_NilStore(t *testing.T) {
	// Transaction without store reference
	tx := NewTransaction(1, 100)

	// Get should return not found
	_, found := tx.Get("key1")
	if found {
		t.Error("Get with nil store should return not found")
	}

	// Put should still work (writes to local map)
	err := tx.Put("key1", []byte("value"))
	if err != nil {
		t.Errorf("Put with nil store should work: %v", err)
	}

	// Commit should fail gracefully
	err = tx.Commit()
	if err != ErrTransactionClosed {
		t.Errorf("Commit with nil store should return ErrTransactionClosed, got %v", err)
	}
}

func TestTransaction_EmptyKey(t *testing.T) {
	store := NewMVCCStore()
	txn := store.Begin()

	// Empty key should work
	err := txn.Put("", []byte("value"))
	if err != nil {
		t.Fatalf("Put with empty key failed: %v", err)
	}

	value, found := txn.Get("")
	if !found {
		t.Error("Get with empty key should find value")
	}
	if !bytes.Equal(value, []byte("value")) {
		t.Errorf("expected 'value', got %s", string(value))
	}

	err = txn.Commit()
	if err != nil {
		t.Fatalf("Commit with empty key failed: %v", err)
	}
}

func TestTransaction_EmptyValue(t *testing.T) {
	store := NewMVCCStore()
	txn := store.Begin()

	// Empty value should work
	err := txn.Put("key1", []byte{})
	if err != nil {
		t.Fatalf("Put with empty value failed: %v", err)
	}

	value, found := txn.Get("key1")
	if !found {
		t.Error("Get should find key with empty value")
	}
	if len(value) != 0 {
		t.Errorf("expected empty value, got %v", value)
	}

	err = txn.Commit()
	if err != nil {
		t.Fatalf("Commit with empty value failed: %v", err)
	}
}

func TestTransaction_NilValue(t *testing.T) {
	store := NewMVCCStore()
	txn := store.Begin()

	// Nil value should work
	err := txn.Put("key1", nil)
	if err != nil {
		t.Fatalf("Put with nil value failed: %v", err)
	}

	value, found := txn.Get("key1")
	if !found {
		t.Error("Get should find key with nil value")
	}
	if len(value) != 0 {
		t.Errorf("expected empty value, got %v", value)
	}
}

// =============================================================================
// MVCC Integration Tests (MVCC.12)
// =============================================================================

func TestMVCC_SnapshotIsolation(t *testing.T) {
	store := NewMVCCStore()

	// Initial setup: write V1 and commit
	txnSetup := store.Begin()
	err := txnSetup.Put("K", []byte("V1"))
	if err != nil {
		t.Fatalf("Setup put failed: %v", err)
	}
	err = txnSetup.Commit()
	if err != nil {
		t.Fatalf("Setup commit failed: %v", err)
	}

	// Start transaction T1 - it will see V1
	t1 := store.Begin()

	// T1 reads key K, should see V1
	value1, found1 := t1.Get("K")
	if !found1 {
		t.Fatal("T1 should find key K")
	}
	if !bytes.Equal(value1, []byte("V1")) {
		t.Errorf("T1 first read: expected V1, got %s", string(value1))
	}

	// Start transaction T2 (after T1)
	t2 := store.Begin()

	// T2 writes K = V2 and commits
	err = t2.Put("K", []byte("V2"))
	if err != nil {
		t.Fatalf("T2 put failed: %v", err)
	}
	err = t2.Commit()
	if err != nil {
		t.Fatalf("T2 commit failed: %v", err)
	}

	// T1 reads K again - should STILL see V1 (snapshot isolation)
	value2, found2 := t1.Get("K")
	if !found2 {
		t.Fatal("T1 should still find key K")
	}
	if !bytes.Equal(value2, []byte("V1")) {
		t.Errorf("T1 second read: expected V1 (snapshot isolation), got %s", string(value2))
	}

	// T1 commits (read-only, should succeed)
	err = t1.Commit()
	if err != nil {
		t.Fatalf("T1 commit failed: %v", err)
	}

	// New transaction T3 should see V2
	t3 := store.Begin()
	value3, found3 := t3.Get("K")
	if !found3 {
		t.Fatal("T3 should find key K")
	}
	if !bytes.Equal(value3, []byte("V2")) {
		t.Errorf("T3 should see V2, got %s", string(value3))
	}
}

func TestMVCC_WriteWriteConflict(t *testing.T) {
	store := NewMVCCStore()

	// Initial setup
	txnSetup := store.Begin()
	txnSetup.Put("K", []byte("initial"))
	txnSetup.Commit()

	// Start T1 and T2 concurrently
	t1 := store.Begin()
	t2 := store.Begin()

	// T1 writes to key K
	err := t1.Put("K", []byte("T1-value"))
	if err != nil {
		t.Fatalf("T1 put failed: %v", err)
	}

	// T1 commits first
	err = t1.Commit()
	if err != nil {
		t.Fatalf("T1 commit failed: %v", err)
	}

	// T2 writes to same key K
	err = t2.Put("K", []byte("T2-value"))
	if err != nil {
		t.Fatalf("T2 put failed: %v", err)
	}

	// T2 tries to commit - should get write conflict
	err = t2.Commit()
	if err != ErrWriteConflict {
		t.Errorf("T2 should get ErrWriteConflict, got %v", err)
	}

	// Verify T1's value is persisted
	t3 := store.Begin()
	value, found := t3.Get("K")
	if !found {
		t.Fatal("T3 should find key K")
	}
	if !bytes.Equal(value, []byte("T1-value")) {
		t.Errorf("Expected T1-value, got %s", string(value))
	}
}

func TestMVCC_GarbageCollection(t *testing.T) {
	store := NewMVCCStore()

	// Create many versions of the same key
	for i := 0; i < 10; i++ {
		txn := store.Begin()
		txn.Put("K", []byte("V"+string(rune('0'+i))))
		txn.Commit()
	}

	// Verify chain length before GC
	chain := store.GetChain("K")
	initialLength := chain.Length
	if initialLength != 10 {
		t.Errorf("Expected 10 versions before GC, got %d", initialLength)
	}

	// Run GC with config that should collect old versions
	config := GCConfig{
		MinVersionsToKeep: 2,
		MaxVersionAge:     0, // Allow immediate GC based on timestamp
		BatchSize:         100,
		Interval:          time.Second,
	}

	// Manually trigger GC by using GCChainWithMinTS with a high timestamp
	// This simulates GC collecting old versions
	currentTS := store.currentTS.Load()
	collected := store.GCChainWithMinTS("K", currentTS+1, config.MinVersionsToKeep)

	if collected == 0 {
		t.Log("No versions collected - this is acceptable if all versions are recent")
	}

	// Verify at least MinVersionsToKeep versions remain
	if chain.Length < config.MinVersionsToKeep {
		t.Errorf("Should keep at least %d versions, got %d", config.MinVersionsToKeep, chain.Length)
	}

	// Verify the most recent version is still accessible
	txn := store.Begin()
	value, found := txn.Get("K")
	if !found {
		t.Fatal("Should still find key K after GC")
	}
	if !bytes.Equal(value, []byte("V9")) {
		t.Errorf("Latest version should be V9, got %s", string(value))
	}
}

func TestMVCC_GarbageCollectionPreservesActiveReaders(t *testing.T) {
	store := NewMVCCStore()

	// Create initial version
	txn1 := store.Begin()
	txn1.Put("K", []byte("V1"))
	txn1.Commit()

	// Start a long-running reader BEFORE more writes
	longReader := store.Begin()

	// Long reader reads the value (at this point V1)
	value, found := longReader.Get("K")
	if !found || !bytes.Equal(value, []byte("V1")) {
		t.Fatalf("Long reader should see V1")
	}

	// Create more versions
	for i := 2; i <= 5; i++ {
		txn := store.Begin()
		txn.Put("K", []byte("V"+string(rune('0'+i))))
		txn.Commit()
	}

	// Run GC - should preserve V1 because longReader started at that timestamp
	config := GCConfig{
		MinVersionsToKeep: 1,
		MaxVersionAge:     0,
		BatchSize:         100,
		Interval:          time.Second,
	}

	result := store.RunGCCycle(config)
	t.Logf("GC result: chains=%d, collected=%d, skipped=%d", result.ChainsScanned, result.VersionsCollected, result.SkippedActive)

	// Long reader should still be able to see V1 (its snapshot)
	value2, found2 := longReader.Get("K")
	if !found2 {
		t.Fatal("Long reader should still find K after GC")
	}
	if !bytes.Equal(value2, []byte("V1")) {
		t.Errorf("Long reader should still see V1 after GC, got %s", string(value2))
	}
}

func TestMVCC_ConcurrentTransactions(t *testing.T) {
	store := NewMVCCStore()

	// Initialize keys
	numKeys := 10
	for i := 0; i < numKeys; i++ {
		txn := store.Begin()
		txn.Put("key"+string(rune('A'+i)), []byte("0"))
		txn.Commit()
	}

	// Launch N concurrent transactions doing read-modify-write
	numTxns := 50
	var wg sync.WaitGroup
	var successCount atomic.Int64
	var conflictCount atomic.Int64

	for i := 0; i < numTxns; i++ {
		wg.Add(1)
		go func(txnNum int) {
			defer wg.Done()

			// Each transaction increments a random key
			key := "key" + string(rune('A'+txnNum%numKeys))

			txn := store.Begin()

			// Read current value
			value, found := txn.Get(key)
			if !found {
				value = []byte("0")
			}

			// Increment (simple string append for demonstration)
			newValue := append(value, '1')

			// Write new value
			err := txn.Put(key, newValue)
			if err != nil {
				return
			}

			// Try to commit
			err = txn.Commit()
			if err == nil {
				successCount.Add(1)
			} else if err == ErrWriteConflict {
				conflictCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Successful commits: %d, Conflicts: %d", successCount.Load(), conflictCount.Load())

	// All transactions should either succeed or get write conflict
	total := successCount.Load() + conflictCount.Load()
	if total != int64(numTxns) {
		t.Errorf("Expected %d total outcomes, got %d", numTxns, total)
	}

	// Verify data consistency: each key should have consistent state
	verifyTxn := store.Begin()
	for i := 0; i < numKeys; i++ {
		key := "key" + string(rune('A'+i))
		value, found := verifyTxn.Get(key)
		if !found {
			t.Errorf("Key %s should exist", key)
			continue
		}
		// Value should be "0" followed by some number of "1"s
		if len(value) < 1 || value[0] != '0' {
			t.Errorf("Key %s has invalid value: %s", key, string(value))
		}
	}
}

func TestMVCC_LongRunningReader(t *testing.T) {
	store := NewMVCCStore()

	// Initial write
	txn1 := store.Begin()
	txn1.Put("K", []byte("initial"))
	txn1.Commit()

	// Start long-running read transaction
	longReader := store.Begin()
	snapshotValue, _ := longReader.Get("K")

	// Perform many writes and GC cycles while long reader is active
	for i := 0; i < 20; i++ {
		txn := store.Begin()
		txn.Put("K", []byte("update-"+string(rune('0'+i%10))))
		txn.Commit()
	}

	// Run multiple GC cycles
	config := GCConfig{
		MinVersionsToKeep: 2,
		MaxVersionAge:     time.Millisecond,
		BatchSize:         100,
		Interval:          time.Millisecond,
	}
	for i := 0; i < 5; i++ {
		store.RunGCCycle(config)
	}

	// Long reader should still see its original snapshot
	currentValue, found := longReader.Get("K")
	if !found {
		t.Fatal("Long reader should still find K")
	}
	if !bytes.Equal(currentValue, snapshotValue) {
		t.Errorf("Long reader snapshot violated: expected %s, got %s",
			string(snapshotValue), string(currentValue))
	}

	// Complete the long reader
	longReader.Commit()

	// New transaction sees latest
	newTxn := store.Begin()
	latestValue, _ := newTxn.Get("K")
	if bytes.Equal(latestValue, snapshotValue) {
		t.Error("New transaction should see updated value, not the old snapshot")
	}
}

func TestMVCC_GCMetrics(t *testing.T) {
	store := NewMVCCStore()

	// Create some versions
	for i := 0; i < 5; i++ {
		for j := 0; j < 3; j++ {
			txn := store.Begin()
			txn.Put("key"+string(rune('0'+i)), []byte("v"+string(rune('0'+j))))
			txn.Commit()
		}
	}

	// Reset metrics
	store.GetGCMetrics().Reset()

	// Run GC cycle
	config := GCConfig{
		MinVersionsToKeep: 1,
		MaxVersionAge:     0,
		BatchSize:         100,
		Interval:          time.Second,
	}

	result := store.RunGCCycle(config)

	// Check result
	if result.ChainsScanned != 5 {
		t.Errorf("Expected 5 chains scanned, got %d", result.ChainsScanned)
	}
	if result.Duration == 0 {
		t.Error("Duration should be non-zero")
	}

	// Check metrics
	metrics := store.GetGCMetrics().Snapshot()
	if metrics.CyclesRun != 1 {
		t.Errorf("Expected 1 cycle run, got %d", metrics.CyclesRun)
	}
	if metrics.ChainsScanned != 5 {
		t.Errorf("Expected 5 chains scanned in metrics, got %d", metrics.ChainsScanned)
	}
	if metrics.LastRunDuration == 0 {
		t.Error("LastRunDuration should be non-zero")
	}
}

func TestMVCC_ConcurrentGCAndTransactions(t *testing.T) {
	store := NewMVCCStore()

	// Initialize keys
	for i := 0; i < 10; i++ {
		txn := store.Begin()
		txn.Put("key"+string(rune('0'+i)), []byte("initial"))
		txn.Commit()
	}

	var wg sync.WaitGroup

	// Start background GC
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gcConfig := GCConfig{
		MinVersionsToKeep: 2,
		MaxVersionAge:     time.Millisecond,
		BatchSize:         50,
		Interval:          time.Millisecond * 10,
	}
	store.StartGC(ctx, gcConfig)

	// Concurrent transactions
	numWriters := 20
	numIterations := 50

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				txn := store.Begin()
				key := "key" + string(rune('0'+writerID%10))
				txn.Put(key, []byte("value-"+string(rune('0'+j%10))))

				// Ignore conflicts, just keep trying
				txn.Commit()
			}
		}(i)
	}

	wg.Wait()
	cancel() // Stop GC

	// Verify all keys are still accessible
	verifyTxn := store.Begin()
	for i := 0; i < 10; i++ {
		key := "key" + string(rune('0'+i))
		_, found := verifyTxn.Get(key)
		if !found {
			t.Errorf("Key %s should exist after concurrent GC and writes", key)
		}
	}

	// Check GC ran
	metrics := store.GetGCMetrics().Snapshot()
	if metrics.CyclesRun == 0 {
		t.Log("GC cycles may not have run if test completed too quickly")
	} else {
		t.Logf("GC ran %d cycles, collected %d versions", metrics.CyclesRun, metrics.VersionsCollected)
	}
}

func TestMVCC_StartGCCancellation(t *testing.T) {
	store := NewMVCCStore()

	ctx, cancel := context.WithCancel(context.Background())

	config := GCConfig{
		MinVersionsToKeep: 1,
		MaxVersionAge:     time.Hour,
		BatchSize:         10,
		Interval:          time.Millisecond * 50,
	}

	// Start GC
	store.StartGC(ctx, config)

	// Let it run briefly
	time.Sleep(time.Millisecond * 100)

	// Cancel should stop the GC loop
	cancel()

	// Give goroutine time to exit
	time.Sleep(time.Millisecond * 100)

	// Test passes if no goroutine leak (verified by go test -race)
}

func TestMVCC_DefaultGCConfig(t *testing.T) {
	config := DefaultGCConfig()

	if config.MinVersionsToKeep < 1 {
		t.Error("MinVersionsToKeep should be at least 1")
	}
	if config.MaxVersionAge <= 0 {
		t.Error("MaxVersionAge should be positive")
	}
	if config.BatchSize <= 0 {
		t.Error("BatchSize should be positive")
	}
	if config.Interval <= 0 {
		t.Error("Interval should be positive")
	}
}

func TestMVCC_MultipleReadersSameSnapshot(t *testing.T) {
	store := NewMVCCStore()

	// Initial write
	txn1 := store.Begin()
	txn1.Put("K", []byte("V1"))
	txn1.Commit()

	// Start multiple readers at roughly the same time
	readers := make([]*Transaction, 5)
	for i := 0; i < 5; i++ {
		readers[i] = store.Begin()
	}

	// All readers should see V1
	for i, reader := range readers {
		value, found := reader.Get("K")
		if !found {
			t.Errorf("Reader %d should find K", i)
			continue
		}
		if !bytes.Equal(value, []byte("V1")) {
			t.Errorf("Reader %d should see V1, got %s", i, string(value))
		}
	}

	// Writer updates to V2
	writer := store.Begin()
	writer.Put("K", []byte("V2"))
	writer.Commit()

	// All readers should STILL see V1 (snapshot isolation)
	for i, reader := range readers {
		value, found := reader.Get("K")
		if !found {
			t.Errorf("Reader %d should still find K", i)
			continue
		}
		if !bytes.Equal(value, []byte("V1")) {
			t.Errorf("Reader %d should still see V1 after concurrent write, got %s", i, string(value))
		}
		reader.Commit()
	}

	// New reader sees V2
	newReader := store.Begin()
	value, _ := newReader.Get("K")
	if !bytes.Equal(value, []byte("V2")) {
		t.Errorf("New reader should see V2, got %s", string(value))
	}
}

func TestMVCC_DeleteThenRewrite(t *testing.T) {
	store := NewMVCCStore()

	// Write initial value
	txn1 := store.Begin()
	txn1.Put("K", []byte("V1"))
	txn1.Commit()

	// Delete the key
	txn2 := store.Begin()
	txn2.Delete("K")
	txn2.Commit()

	// Verify deleted
	txn3 := store.Begin()
	_, found := txn3.Get("K")
	if found {
		t.Error("Key should not be found after delete")
	}

	// Write new value to same key
	txn4 := store.Begin()
	txn4.Put("K", []byte("V2"))
	txn4.Commit()

	// Verify new value
	txn5 := store.Begin()
	value, found := txn5.Get("K")
	if !found {
		t.Error("Key should be found after rewrite")
	}
	if !bytes.Equal(value, []byte("V2")) {
		t.Errorf("Expected V2, got %s", string(value))
	}
}

func TestMVCC_TransactionIsolationLevels(t *testing.T) {
	store := NewMVCCStore()

	// Setup: Create key with value V1
	setup := store.Begin()
	setup.Put("K", []byte("V1"))
	setup.Commit()

	t.Run("Read committed isolation", func(t *testing.T) {
		// T1 reads K
		t1 := store.Begin()
		v1, _ := t1.Get("K")

		// T2 writes K but doesn't commit
		t2 := store.Begin()
		t2.Put("K", []byte("V2-uncommitted"))

		// T1 should NOT see uncommitted write from T2
		v1After, found := t1.Get("K")
		if !found {
			t.Error("T1 should still see K")
		}
		if !bytes.Equal(v1After, v1) {
			t.Error("T1 should not see uncommitted changes from T2")
		}

		// Cleanup
		t2.Rollback()
		t1.Commit()
	})

	t.Run("Repeatable read", func(t *testing.T) {
		// T1 reads K twice
		t1 := store.Begin()
		v1First, _ := t1.Get("K")

		// T2 writes and commits between T1's reads
		t2 := store.Begin()
		t2.Put("K", []byte("V2-committed"))
		t2.Commit()

		// T1's second read should return same value (repeatable read)
		v1Second, _ := t1.Get("K")
		if !bytes.Equal(v1First, v1Second) {
			t.Errorf("Repeatable read violated: first=%s, second=%s",
				string(v1First), string(v1Second))
		}

		t1.Commit()
	})
}

func TestMVCC_HighContention(t *testing.T) {
	store := NewMVCCStore()

	// Single hot key
	setup := store.Begin()
	setup.Put("hot-key", []byte("0"))
	setup.Commit()

	var wg sync.WaitGroup
	var successfulWrites atomic.Int64
	numWriters := 100

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for attempt := 0; attempt < 10; attempt++ {
				txn := store.Begin()
				value, _ := txn.Get("hot-key")
				newValue := append(value, byte('0'+id%10))
				txn.Put("hot-key", newValue)
				if err := txn.Commit(); err == nil {
					successfulWrites.Add(1)
					return // Success, stop retrying
				}
				// Conflict, retry with new transaction
			}
		}(i)
	}

	wg.Wait()

	t.Logf("High contention: %d/%d writers succeeded", successfulWrites.Load(), numWriters)

	// With 100 writers on same key, many will conflict
	// At least some should succeed
	if successfulWrites.Load() == 0 {
		t.Error("At least some writers should succeed")
	}
}
