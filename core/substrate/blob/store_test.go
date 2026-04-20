package blob

import (
	"errors"
	"io"
	"sync"
	"testing"
)

func TestStore_Put_ReturnsHashOfContent(t *testing.T) {
	s := New()
	data := []byte("hello world")
	hash, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if hash != Sum(data) {
		t.Fatal("Put returned a hash that is not the BLAKE3 of the content")
	}
}

func TestStore_Put_NilDataIsError(t *testing.T) {
	s := New()
	_, err := s.Put(nil)
	if !errors.Is(err, ErrNilData) {
		t.Fatalf("expected ErrNilData for nil data, got %v", err)
	}
}

func TestStore_Put_DeduplicatesContent(t *testing.T) {
	s := New()
	data := []byte("dedupe me")
	h1, _ := s.Put(data)
	h2, _ := s.Put(data)
	if h1 != h2 {
		t.Fatal("identical content produced different hashes")
	}
	if got := s.Refcount(h1); got != 2 {
		t.Fatalf("after two Puts of identical content, refcount = %d, want 2", got)
	}
	if got := s.Stat().BlobCount; got != 1 {
		t.Fatalf("after two Puts of identical content, BlobCount = %d, want 1", got)
	}
}

func TestStore_Get_RoundTrip(t *testing.T) {
	s := New()
	data := []byte("round trip me")
	hash, _ := s.Put(data)
	rc, err := s.Get(hash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("Get returned %q, want %q", got, data)
	}
}

func TestStore_Get_MissingIsNotFound(t *testing.T) {
	s := New()
	_, err := s.Get(Sum([]byte("never put")))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_Bytes_RoundTrip(t *testing.T) {
	s := New()
	data := []byte("bytes accessor")
	hash, _ := s.Put(data)
	got, err := s.Bytes(hash)
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("Bytes returned %q, want %q", got, data)
	}
}

func TestStore_Has_TrueAfterPut(t *testing.T) {
	s := New()
	hash, _ := s.Put([]byte("present"))
	if !s.Has(hash) {
		t.Fatal("Has returned false for a blob just Put")
	}
}

func TestStore_Has_FalseForAbsent(t *testing.T) {
	s := New()
	if s.Has(Sum([]byte("absent"))) {
		t.Fatal("Has returned true for a blob never Put")
	}
}

func TestStore_Acquire_IncrementsRefcount(t *testing.T) {
	s := New()
	hash, _ := s.Put([]byte("acquire me"))
	if err := s.Acquire(hash); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := s.Refcount(hash); got != 2 {
		t.Fatalf("after Put + Acquire, refcount = %d, want 2", got)
	}
}

func TestStore_Acquire_MissingIsNotFound(t *testing.T) {
	s := New()
	err := s.Acquire(Sum([]byte("never present")))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_Release_DecrementsRefcount(t *testing.T) {
	s := New()
	hash, _ := s.Put([]byte("release me"))
	_ = s.Acquire(hash) // refcount now 2
	if err := s.Release(hash); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got := s.Refcount(hash); got != 1 {
		t.Fatalf("after Put + Acquire + Release, refcount = %d, want 1", got)
	}
}

func TestStore_Release_RefusesUnderflow(t *testing.T) {
	s := New()
	hash, _ := s.Put([]byte("underflow")) // refcount 1
	if err := s.Release(hash); err != nil {
		t.Fatalf("first Release should succeed: %v", err)
	}
	err := s.Release(hash)
	if !errors.Is(err, ErrRefcountUnderflow) {
		t.Fatalf("expected ErrRefcountUnderflow on second Release, got %v", err)
	}
}

func TestStore_Release_MissingIsNotFound(t *testing.T) {
	s := New()
	err := s.Release(Sum([]byte("never present")))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_Refcount_ZeroForAbsent(t *testing.T) {
	s := New()
	if got := s.Refcount(Sum([]byte("absent"))); got != 0 {
		t.Fatalf("Refcount of absent blob = %d, want 0", got)
	}
}

func TestStore_Stat_ReportsCorrectAccounting(t *testing.T) {
	s := New()
	a := []byte("aaa")
	b := []byte("bbbb")
	hashA, _ := s.Put(a)
	hashA2, _ := s.Put(a) // dedup, refcount = 2
	_, _ = s.Put(b)       // refcount = 1

	if hashA != hashA2 {
		t.Fatal("Put of identical content produced different hashes")
	}

	stats := s.Stat()
	if stats.BlobCount != 2 {
		t.Errorf("BlobCount = %d, want 2", stats.BlobCount)
	}
	wantUnique := int64(len(a) + len(b))
	if stats.UniqueBytes != wantUnique {
		t.Errorf("UniqueBytes = %d, want %d", stats.UniqueBytes, wantUnique)
	}
	wantReferenced := int64(len(a)*2 + len(b)*1)
	if stats.ReferencedBytes != wantReferenced {
		t.Errorf("ReferencedBytes = %d, want %d (a:×2 + b:×1)", stats.ReferencedBytes, wantReferenced)
	}
}

func TestStore_Compact_RemovesRefcountZero(t *testing.T) {
	s := New()
	hash, _ := s.Put([]byte("evict me"))
	_ = s.Release(hash) // refcount = 0

	removed := s.Compact()
	if removed != 1 {
		t.Errorf("Compact removed %d, want 1", removed)
	}
	if s.Has(hash) {
		t.Error("blob with refcount 0 should be gone after Compact")
	}
}

func TestStore_Compact_PreservesRefcountPositive(t *testing.T) {
	s := New()
	hash, _ := s.Put([]byte("keep me")) // refcount = 1
	removed := s.Compact()
	if removed != 0 {
		t.Errorf("Compact removed %d positive-refcount blobs, want 0", removed)
	}
	if !s.Has(hash) {
		t.Error("blob with refcount > 0 should survive Compact")
	}
}

// Concurrent stress test — must be clean under -race.
func TestStore_Concurrent_PutsAndAcquires(t *testing.T) {
	s := New()
	const goroutines = 64
	const opsPerG = 256

	contents := makeContents(opsPerG)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			runConcurrentOps(s, contents)
		}()
	}
	wg.Wait()

	stats := s.Stat()
	if stats.BlobCount != int64(len(contents)) {
		t.Errorf("BlobCount = %d, want %d (one per unique content)", stats.BlobCount, len(contents))
	}
	verifyConcurrentRefcounts(t, s, contents, goroutines)
}

func makeContents(n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = []byte(byteContent(i))
	}
	return out
}

func byteContent(i int) string {
	return "content-" + intToString(i)
}

func intToString(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

func runConcurrentOps(s *Store, contents [][]byte) {
	for _, c := range contents {
		_, _ = s.Put(c)
	}
}

func verifyConcurrentRefcounts(t *testing.T, s *Store, contents [][]byte, goroutines int) {
	t.Helper()
	for _, c := range contents {
		hash := Sum(c)
		got := s.Refcount(hash)
		want := int64(goroutines)
		if got != want {
			t.Errorf("blob %q refcount = %d, want %d", c, got, want)
			return // one failure is enough, don't flood output
		}
	}
}

func TestStore_WithShardCount_AppliesOption(t *testing.T) {
	s := New(WithShardCount(8))
	if got := s.Stat().ShardCount; got != 8 {
		t.Errorf("WithShardCount(8) yielded ShardCount = %d", got)
	}
}

func TestStore_WithShardCount_RoundsUp(t *testing.T) {
	s := New(WithShardCount(7))
	if got := s.Stat().ShardCount; got != 8 {
		t.Errorf("WithShardCount(7) should round up to 8, got %d", got)
	}
}

func TestStore_DistinctContent_LandsInPotentiallyDifferentShards(t *testing.T) {
	// Sanity check: ensure sharding is actually distributing across
	// shards. With many random contents and 16 shards, we expect to see
	// at least 2 distinct shards used (probability of all landing in
	// one shard with 256 distinct hashes and 16 shards is astronomically
	// small).
	s := New(WithShardCount(16))
	for i := range 256 {
		_, _ = s.Put([]byte(byteContent(i)))
	}
	shardsUsed := countNonEmptyShards(s)
	if shardsUsed < 2 {
		t.Errorf("256 distinct blobs landed in %d shards, expected sharding to distribute", shardsUsed)
	}
}

func countNonEmptyShards(s *Store) int {
	n := 0
	for _, sh := range s.shards {
		sh.mu.RLock()
		if len(sh.entries) > 0 {
			n++
		}
		sh.mu.RUnlock()
	}
	return n
}
