package sylkdir

import (
	"math/rand"
	"testing"
	"time"
)

func BenchmarkEdgeMarshalBinary(b *testing.B) {
	edge := makeTestEdge(100, 200, 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = edge.MarshalBinary()
	}
}

func BenchmarkEdgeUnmarshalBinary(b *testing.B) {
	edge := makeTestEdge(100, 200, 1)
	data := edge.MarshalBinary()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := &Edge{}
		_ = e.UnmarshalBinary(data)
	}
}

func BenchmarkEdgeShardStoreWrite(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewEdgeShardStoreFromSylkDir(sd)
	store.Init()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		edge := makeTestEdge(uint32(i), uint32(i+1000), 1)
		store.Write(edge)
	}
}

func BenchmarkEdgeShardStoreGetEdge(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewEdgeShardStoreFromSylkDir(sd)
	store.Init()

	// Pre-populate
	numEdges := 10000
	for i := 0; i < numEdges; i++ {
		store.Write(makeTestEdge(uint32(i), uint32(i+1000), 1))
	}

	// Pre-generate random indices
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	indices := make([]int, b.N)
	for i := range indices {
		indices[i] = rng.Intn(numEdges)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := indices[i]
		_, _ = store.GetEdge(uint32(idx), uint32(idx+1000), 1)
	}
}

func BenchmarkEdgeShardStoreGetOutgoing(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewEdgeShardStoreFromSylkDir(sd)
	store.Init()

	// Pre-populate: 100 nodes, 50 outgoing edges each = 5000 edges
	numNodes := 100
	edgesPerNode := 50
	for src := 0; src < numNodes; src++ {
		for j := 0; j < edgesPerNode; j++ {
			store.Write(makeTestEdge(uint32(src), uint32(src*1000+j), uint8(j%256)))
		}
	}

	// Pre-generate random node IDs
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	nodeIDs := make([]uint32, b.N)
	for i := range nodeIDs {
		nodeIDs[i] = uint32(rng.Intn(numNodes))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.GetOutgoing(nodeIDs[i])
	}
}

func BenchmarkEdgeShardStoreGetIncoming(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewEdgeShardStoreFromSylkDir(sd)
	store.Init()

	// Pre-populate: edges from many sources to 100 target nodes
	numTargets := 100
	sourcesPerTarget := 50
	for tgt := 0; tgt < numTargets; tgt++ {
		for j := 0; j < sourcesPerTarget; j++ {
			store.Write(makeTestEdge(uint32(tgt*1000+j), uint32(tgt), uint8(j%256)))
		}
	}

	// Pre-generate random target IDs
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	targetIDs := make([]uint32, b.N)
	for i := range targetIDs {
		targetIDs[i] = uint32(rng.Intn(numTargets))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.GetIncoming(targetIDs[i])
	}
}

// BenchmarkGetAllEdgesForNode tests the acceptance criteria:
// "Load all edges for a node < 1ms"
func BenchmarkGetAllEdgesForNode(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewEdgeShardStoreFromSylkDir(sd)
	store.Init()

	// Setup: node 50 has 100 outgoing and 100 incoming edges
	targetNode := uint32(50)

	// Outgoing edges
	for i := 0; i < 100; i++ {
		store.Write(makeTestEdge(targetNode, uint32(1000+i), uint8(i%10)))
	}

	// Incoming edges
	for i := 0; i < 100; i++ {
		store.Write(makeTestEdge(uint32(2000+i), targetNode, uint8(i%10)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Get both outgoing and incoming (total of 200 edges)
		_, _ = store.GetOutgoing(targetNode)
		_, _ = store.GetIncoming(targetNode)
	}
}

func BenchmarkEdgeShardStoreSaveIndexes(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewEdgeShardStoreFromSylkDir(sd)
	store.Init()

	// Pre-populate
	for i := 0; i < 5000; i++ {
		store.Write(makeTestEdge(uint32(i), uint32(i+1000), 1))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.SaveIndexes()
	}
}

func BenchmarkEdgeShardStoreLoad(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewEdgeShardStoreFromSylkDir(sd)
	store.Init()

	// Pre-populate and save
	for i := 0; i < 5000; i++ {
		store.Write(makeTestEdge(uint32(i), uint32(i+1000), 1))
	}
	store.SaveIndexes()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		newStore := NewEdgeShardStoreFromSylkDir(sd)
		newStore.Init()
	}
}

func BenchmarkEdgeShardStoreUpdate(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	sd.Init()

	store := NewEdgeShardStoreFromSylkDir(sd)
	store.Init()

	// Pre-populate
	numEdges := 1000
	for i := 0; i < numEdges; i++ {
		store.Write(makeTestEdge(uint32(i), uint32(i+1000), 1))
	}

	// Pre-generate random indices
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	indices := make([]int, b.N)
	for i := range indices {
		indices[i] = rng.Intn(numEdges)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := indices[i]
		// Update existing edge with new weight
		edge := makeTestEdge(uint32(idx), uint32(idx+1000), 1)
		edge.Weight = 0.99
		store.Write(edge)
	}
}
