package sylkdir

import (
	"math/rand"
	"testing"
	"time"
)

func BenchmarkNodeMarshalBinary(b *testing.B) {
	node := makeTestNode(1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = node.MarshalBinary()
	}
}

func BenchmarkNodeUnmarshalBinary(b *testing.B) {
	node := makeTestNode(1)
	data, _ := node.MarshalBinary()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := &Node{}
		_ = n.UnmarshalBinary(data)
	}
}

func BenchmarkNodeBlockStoreWrite(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewNodeBlockStoreFromSylkDir(sd)
	store.Init()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		node := makeTestNode(uint32(i))
		store.Write(node)
	}
}

func BenchmarkNodeBlockStoreReadSequential(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewNodeBlockStoreFromSylkDir(sd)
	store.Init()

	// Pre-populate with nodes
	numNodes := 10000
	for i := 0; i < numNodes; i++ {
		store.Write(makeTestNode(uint32(i)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.Read(uint32(i % numNodes))
	}
}

func BenchmarkNodeBlockStoreReadRandom(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewNodeBlockStoreFromSylkDir(sd)
	store.Init()

	// Pre-populate with nodes
	numNodes := 10000
	for i := 0; i < numNodes; i++ {
		store.Write(makeTestNode(uint32(i)))
	}

	// Pre-generate random IDs
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	randomIDs := make([]uint32, b.N)
	for i := range randomIDs {
		randomIDs[i] = uint32(rng.Intn(numNodes))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.Read(randomIDs[i])
	}
}

// BenchmarkRead10KRandomNodes tests the acceptance criteria:
// "Read 10K random nodes < 100ms"
func BenchmarkRead10KRandomNodes(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewNodeBlockStoreFromSylkDir(sd)
	store.Init()

	// Pre-populate with nodes
	numNodes := 10000
	for i := 0; i < numNodes; i++ {
		store.Write(makeTestNode(uint32(i)))
	}

	// Pre-generate random IDs for each benchmark iteration
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Read 10K random nodes per iteration
		for j := 0; j < 10000; j++ {
			id := uint32(rng.Intn(numNodes))
			_, _ = store.Read(id)
		}
	}
}

func BenchmarkNodeBlockStoreReadBatch(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewNodeBlockStoreFromSylkDir(sd)
	store.Init()

	// Pre-populate with nodes
	numNodes := 10000
	for i := 0; i < numNodes; i++ {
		store.Write(makeTestNode(uint32(i)))
	}

	// Create batch of random IDs
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	batchSize := 100
	ids := make([]uint32, batchSize)
	for i := range ids {
		ids[i] = uint32(rng.Intn(numNodes))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.ReadBatch(ids)
	}
}

func BenchmarkNodeBlockStoreSaveIndex(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewNodeBlockStoreFromSylkDir(sd)
	store.Init()

	// Pre-populate with nodes
	for i := 0; i < 10000; i++ {
		store.Write(makeTestNode(uint32(i)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.SaveIndex()
	}
}

func BenchmarkNodeBlockStoreLoadIndex(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewNodeBlockStoreFromSylkDir(sd)
	store.Init()

	// Pre-populate with nodes and save index
	for i := 0; i < 10000; i++ {
		store.Write(makeTestNode(uint32(i)))
	}
	store.SaveIndex()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		newStore := NewNodeBlockStoreFromSylkDir(sd)
		newStore.Init()
	}
}
