package ivf

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestShardedNormStore_CreateAndAppend(t *testing.T) {
	dir := t.TempDir()

	store, err := CreateShardedNormStore(dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	for i := range 100 {
		id, err := store.Append(float64(i) * 1.5)
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

	for i := range 100 {
		norm := store.Get(uint32(i))
		expected := float64(i) * 1.5
		if math.Abs(norm-expected) > 1e-10 {
			t.Errorf("get %d: got %v, want %v", i, norm, expected)
		}
	}
}

func TestShardedNormStore_MultiShard(t *testing.T) {
	dir := t.TempDir()

	store, err := CreateShardedNormStore(dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	count := NormsPerShard + 1000
	norms := make([]float64, count)
	for i := range count {
		norms[i] = float64(i) * 0.001
	}

	startID, err := store.AppendBatch(norms)
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

	for i := range count {
		norm := store.Get(uint32(i))
		expected := float64(i) * 0.001
		if math.Abs(norm-expected) > 1e-10 {
			t.Errorf("get %d: got %v, want %v", i, norm, expected)
		}
	}
}

func TestShardedNormStore_OpenExisting(t *testing.T) {
	dir := t.TempDir()

	store1, err := CreateShardedNormStore(dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	norms := make([]float64, 1000)
	for i := range 1000 {
		norms[i] = math.Sqrt(float64(i))
	}
	if _, err := store1.AppendBatch(norms); err != nil {
		t.Fatalf("append batch: %v", err)
	}

	if err := store1.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	store2, corrupted, err := OpenShardedNormStore(dir)
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

	for i := range 1000 {
		norm := store2.Get(uint32(i))
		expected := math.Sqrt(float64(i))
		if math.Abs(norm-expected) > 1e-10 {
			t.Errorf("get %d: got %v, want %v", i, norm, expected)
		}
	}
}

func TestShardedNormStore_Checksum(t *testing.T) {
	dir := t.TempDir()

	store, err := CreateShardedNormStore(dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	norms := make([]float64, NormsPerShard)
	for i := range NormsPerShard {
		norms[i] = float64(i)
	}

	if _, err := store.AppendBatch(norms); err != nil {
		t.Fatalf("append batch: %v", err)
	}

	if _, err := store.Append(99.99); err != nil {
		t.Fatalf("append extra: %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	store2, corrupted, err := OpenShardedNormStore(dir)
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

func TestShardedNormStore_CorruptionDetection(t *testing.T) {
	dir := t.TempDir()

	store, err := CreateShardedNormStore(dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	norms := make([]float64, NormsPerShard)
	for i := range NormsPerShard {
		norms[i] = float64(i)
	}

	if _, err := store.AppendBatch(norms); err != nil {
		t.Fatalf("append batch: %v", err)
	}

	if _, err := store.Append(99.99); err != nil {
		t.Fatalf("append extra: %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	shardPath := filepath.Join(dir, "shard_0000.bin")
	f, err := os.OpenFile(shardPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("open shard: %v", err)
	}

	if _, err := f.WriteAt([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, normHeaderSize+100); err != nil {
		f.Close()
		t.Fatalf("corrupt shard: %v", err)
	}
	f.Close()

	store2, corrupted, err := OpenShardedNormStore(dir)
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

func TestShardedNormStore_RebuildShard(t *testing.T) {
	dir := t.TempDir()

	store, err := CreateShardedNormStore(dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	originalNorms := make([]float64, 1000)
	for i := range 1000 {
		originalNorms[i] = float64(i) * 0.5
	}

	if _, err := store.AppendBatch(originalNorms); err != nil {
		t.Fatalf("append batch: %v", err)
	}

	if err := store.RebuildShard(0, originalNorms); err != nil {
		t.Fatalf("rebuild shard: %v", err)
	}

	for i := range 1000 {
		norm := store.Get(uint32(i))
		expected := float64(i) * 0.5
		if math.Abs(norm-expected) > 1e-10 {
			t.Errorf("get %d after rebuild: got %v, want %v", i, norm, expected)
		}
	}

	store.Close()
}

func TestShardedNormStore_GetSlice(t *testing.T) {
	dir := t.TempDir()

	store, err := CreateShardedNormStore(dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	norms := make([]float64, 100)
	for i := range 100 {
		norms[i] = float64(i) * 2.0
	}

	if _, err := store.AppendBatch(norms); err != nil {
		t.Fatalf("append batch: %v", err)
	}

	slice := store.GetSlice(10, 20)
	if slice == nil {
		t.Fatal("GetSlice returned nil")
	}

	if len(slice) != 10 {
		t.Errorf("slice length: got %d, want 10", len(slice))
	}

	for i := range 10 {
		expected := float64(i+10) * 2.0
		if math.Abs(slice[i]-expected) > 1e-10 {
			t.Errorf("slice[%d]: got %v, want %v", i, slice[i], expected)
		}
	}
}

func BenchmarkShardedNormStore_Append(b *testing.B) {
	dir := b.TempDir()

	store, err := CreateShardedNormStore(dir)
	if err != nil {
		b.Fatalf("create store: %v", err)
	}
	defer store.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.Append(float64(i) * 0.001); err != nil {
			b.Fatalf("append: %v", err)
		}
	}
}

func BenchmarkShardedNormStore_Get(b *testing.B) {
	dir := b.TempDir()

	store, err := CreateShardedNormStore(dir)
	if err != nil {
		b.Fatalf("create store: %v", err)
	}
	defer store.Close()

	norms := make([]float64, 100000)
	for i := range 100000 {
		norms[i] = float64(i)
	}
	if _, err := store.AppendBatch(norms); err != nil {
		b.Fatalf("append batch: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := uint32(i % 100000)
		_ = store.Get(id)
	}
}

func BenchmarkShardedNormStore_Open(b *testing.B) {
	dir := b.TempDir()

	store, err := CreateShardedNormStore(dir)
	if err != nil {
		b.Fatalf("create store: %v", err)
	}

	norms := make([]float64, 300000)
	for i := range 300000 {
		norms[i] = float64(i)
	}
	if _, err := store.AppendBatch(norms); err != nil {
		b.Fatalf("append batch: %v", err)
	}
	store.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, _, err := OpenShardedNormStore(dir)
		if err != nil {
			b.Fatalf("open: %v", err)
		}
		s.Close()
	}
}
