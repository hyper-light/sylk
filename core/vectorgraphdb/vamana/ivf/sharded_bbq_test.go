package ivf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShardedBBQStore_CreateAndAppend(t *testing.T) {
	dir := t.TempDir()
	codeLen := 96

	store, err := CreateShardedBBQStore(dir, codeLen)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	// Append 100 codes
	for i := range 100 {
		code := make([]byte, codeLen)
		for j := range codeLen {
			code[j] = byte(i + j)
		}
		id, err := store.Append(code)
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if id != uint32(i) {
			t.Errorf("append %d: got id %d, want %d", i, id, i)
		}
	}

	if store.Count() != 100 {
		t.Errorf("count: got %d, want 100", store.Count())
	}

	// Verify codes
	for i := range 100 {
		code := store.Get(uint32(i))
		if code == nil {
			t.Fatalf("get %d: nil", i)
		}
		if len(code) != codeLen {
			t.Errorf("get %d: len %d, want %d", i, len(code), codeLen)
		}
		if code[0] != byte(i) {
			t.Errorf("get %d: code[0] = %d, want %d", i, code[0], i)
		}
	}
}

func TestShardedBBQStore_MultiShard(t *testing.T) {
	dir := t.TempDir()
	codeLen := 8 // Small codes for fast test

	store, err := CreateShardedBBQStore(dir, codeLen)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	// Append more than one shard's worth
	count := BBQPerShard + 1000
	codes := make([][]byte, count)
	for i := range count {
		codes[i] = make([]byte, codeLen)
		codes[i][0] = byte(i & 0xFF)
		codes[i][1] = byte((i >> 8) & 0xFF)
	}

	startID, err := store.AppendBatch(codes)
	if err != nil {
		t.Fatalf("append batch: %v", err)
	}
	if startID != 0 {
		t.Errorf("start id: got %d, want 0", startID)
	}

	if store.Count() != uint64(count) {
		t.Errorf("count: got %d, want %d", store.Count(), count)
	}

	if store.ShardCount() != 2 {
		t.Errorf("shard count: got %d, want 2", store.ShardCount())
	}

	// Verify codes across shards
	for i := range count {
		code := store.Get(uint32(i))
		if code == nil {
			t.Fatalf("get %d: nil", i)
		}
		expected0 := byte(i & 0xFF)
		expected1 := byte((i >> 8) & 0xFF)
		if code[0] != expected0 || code[1] != expected1 {
			t.Errorf("get %d: got [%d,%d], want [%d,%d]", i, code[0], code[1], expected0, expected1)
		}
	}
}

func TestShardedBBQStore_OpenExisting(t *testing.T) {
	dir := t.TempDir()
	codeLen := 16

	// Create and populate
	store1, err := CreateShardedBBQStore(dir, codeLen)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	codes := make([][]byte, 1000)
	for i := range 1000 {
		codes[i] = make([]byte, codeLen)
		codes[i][0] = byte(i & 0xFF)
	}
	if _, err := store1.AppendBatch(codes); err != nil {
		t.Fatalf("append batch: %v", err)
	}

	if err := store1.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// Reopen
	store2, corrupted, err := OpenShardedBBQStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store2.Close()

	if len(corrupted) > 0 {
		t.Errorf("corrupted shards: %v", corrupted)
	}

	if store2.Count() != 1000 {
		t.Errorf("count: got %d, want 1000", store2.Count())
	}

	// Verify data
	for i := range 1000 {
		code := store2.Get(uint32(i))
		if code == nil {
			t.Fatalf("get %d: nil", i)
		}
		if code[0] != byte(i&0xFF) {
			t.Errorf("get %d: code[0] = %d, want %d", i, code[0], i&0xFF)
		}
	}
}

func TestShardedBBQStore_Checksum(t *testing.T) {
	dir := t.TempDir()
	codeLen := 8

	store, err := CreateShardedBBQStore(dir, codeLen)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Fill exactly one shard to trigger sealing
	codes := make([][]byte, BBQPerShard)
	for i := range BBQPerShard {
		codes[i] = make([]byte, codeLen)
		codes[i][0] = byte(i & 0xFF)
	}

	if _, err := store.AppendBatch(codes); err != nil {
		t.Fatalf("append batch: %v", err)
	}

	// Add one more to trigger seal of first shard
	extraCode := make([]byte, codeLen)
	extraCode[0] = 0xFF
	if _, err := store.Append(extraCode); err != nil {
		t.Fatalf("append extra: %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// Verify first shard checksum is valid
	store2, corrupted, err := OpenShardedBBQStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store2.Close()

	if len(corrupted) > 0 {
		t.Errorf("unexpected corrupted shards: %v", corrupted)
	}

	if !store2.VerifyChecksum(0) {
		t.Error("shard 0 checksum verification failed")
	}
}

func TestShardedBBQStore_CorruptionDetection(t *testing.T) {
	dir := t.TempDir()
	codeLen := 8

	store, err := CreateShardedBBQStore(dir, codeLen)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Fill and seal one shard
	codes := make([][]byte, BBQPerShard)
	for i := range BBQPerShard {
		codes[i] = make([]byte, codeLen)
		codes[i][0] = byte(i & 0xFF)
	}

	if _, err := store.AppendBatch(codes); err != nil {
		t.Fatalf("append batch: %v", err)
	}

	// Add one more to seal first shard
	extraCode := make([]byte, codeLen)
	if _, err := store.Append(extraCode); err != nil {
		t.Fatalf("append extra: %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// Corrupt the first shard
	shardPath := filepath.Join(dir, "shard_0000.bin")
	f, err := os.OpenFile(shardPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("open shard: %v", err)
	}

	// Write garbage to data section
	if _, err := f.WriteAt([]byte{0xFF, 0xFF, 0xFF, 0xFF}, bbqHeaderSize+100); err != nil {
		f.Close()
		t.Fatalf("corrupt shard: %v", err)
	}
	f.Close()

	// Reopen should detect corruption
	store2, corrupted, err := OpenShardedBBQStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store2.Close()

	if len(corrupted) != 1 || corrupted[0] != 0 {
		t.Errorf("expected shard 0 corrupted, got: %v", corrupted)
	}

	if store2.VerifyChecksum(0) {
		t.Error("corrupted shard 0 passed checksum verification")
	}
}

func TestShardedBBQStore_RebuildShard(t *testing.T) {
	dir := t.TempDir()
	codeLen := 8

	store, err := CreateShardedBBQStore(dir, codeLen)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Create test data
	originalCodes := make([][]byte, 1000)
	for i := range 1000 {
		originalCodes[i] = make([]byte, codeLen)
		originalCodes[i][0] = byte(i & 0xFF)
		originalCodes[i][1] = byte((i >> 8) & 0xFF)
	}

	if _, err := store.AppendBatch(originalCodes); err != nil {
		t.Fatalf("append batch: %v", err)
	}

	// Rebuild shard 0 with same data
	if err := store.RebuildShard(0, originalCodes); err != nil {
		t.Fatalf("rebuild shard: %v", err)
	}

	// Verify data is intact
	for i := range 1000 {
		code := store.Get(uint32(i))
		if code == nil {
			t.Fatalf("get %d: nil after rebuild", i)
		}
		expected0 := byte(i & 0xFF)
		expected1 := byte((i >> 8) & 0xFF)
		if code[0] != expected0 || code[1] != expected1 {
			t.Errorf("get %d after rebuild: got [%d,%d], want [%d,%d]",
				i, code[0], code[1], expected0, expected1)
		}
	}

	store.Close()
}

func TestShardedBBQStore_ZeroCopy(t *testing.T) {
	dir := t.TempDir()
	codeLen := 8

	store, err := CreateShardedBBQStore(dir, codeLen)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	code := make([]byte, codeLen)
	for i := range codeLen {
		code[i] = byte(i + 1)
	}

	if _, err := store.Append(code); err != nil {
		t.Fatalf("append: %v", err)
	}

	// Get should return zero-copy slice
	retrieved := store.Get(0)
	if retrieved == nil {
		t.Fatal("get: nil")
	}

	// Modify original code, retrieved should NOT change (it's a copy in Append)
	code[0] = 0xFF
	if retrieved[0] == 0xFF {
		t.Error("retrieved code changed when original was modified")
	}

	// But modifying retrieved WILL change the mmap (zero-copy)
	// This is expected behavior for mmap-backed stores
	original := retrieved[1]
	retrieved[1] = 0xAA

	retrieved2 := store.Get(0)
	if retrieved2[1] != 0xAA {
		t.Error("zero-copy modification not reflected")
	}

	// Restore for cleanliness
	retrieved[1] = original
}

func BenchmarkShardedBBQStore_Append(b *testing.B) {
	dir := b.TempDir()
	codeLen := 96

	store, err := CreateShardedBBQStore(dir, codeLen)
	if err != nil {
		b.Fatalf("create store: %v", err)
	}
	defer store.Close()

	code := make([]byte, codeLen)
	for i := range codeLen {
		code[i] = byte(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.Append(code); err != nil {
			b.Fatalf("append: %v", err)
		}
	}
}

func BenchmarkShardedBBQStore_Get(b *testing.B) {
	dir := b.TempDir()
	codeLen := 96

	store, err := CreateShardedBBQStore(dir, codeLen)
	if err != nil {
		b.Fatalf("create store: %v", err)
	}
	defer store.Close()

	// Prepopulate
	codes := make([][]byte, 100000)
	for i := range 100000 {
		codes[i] = make([]byte, codeLen)
		codes[i][0] = byte(i & 0xFF)
	}
	if _, err := store.AppendBatch(codes); err != nil {
		b.Fatalf("append batch: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := uint32(i % 100000)
		code := store.Get(id)
		if code == nil {
			b.Fatalf("get %d: nil", id)
		}
	}
}

func BenchmarkShardedBBQStore_Open(b *testing.B) {
	dir := b.TempDir()
	codeLen := 96

	// Create and populate
	store, err := CreateShardedBBQStore(dir, codeLen)
	if err != nil {
		b.Fatalf("create store: %v", err)
	}

	codes := make([][]byte, 300000) // ~5 shards
	for i := range 300000 {
		codes[i] = make([]byte, codeLen)
	}
	if _, err := store.AppendBatch(codes); err != nil {
		b.Fatalf("append batch: %v", err)
	}
	store.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, _, err := OpenShardedBBQStore(dir)
		if err != nil {
			b.Fatalf("open: %v", err)
		}
		s.Close()
	}
}
