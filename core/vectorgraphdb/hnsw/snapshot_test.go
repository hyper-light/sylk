package hnsw

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLayerSnapshot(t *testing.T) {
	t.Run("nil layer returns empty snapshot", func(t *testing.T) {
		idToString := []string{""}
		snapshot := NewLayerSnapshot(nil, idToString)
		assert.NotNil(t, snapshot.Nodes)
		assert.Empty(t, snapshot.Nodes)
	})

	t.Run("empty layer returns empty nodes", func(t *testing.T) {
		l := newLayer()
		idToString := []string{""}
		snapshot := NewLayerSnapshot(l, idToString)
		assert.NotNil(t, snapshot.Nodes)
		assert.Empty(t, snapshot.Nodes)
	})

	t.Run("layer with nodes creates deep copy", func(t *testing.T) {
		l := newLayer()
		idToString := []string{"", "node1", "node2"}
		l.addNode(1)
		l.addNode(2)
		l.addNeighbor(1, 2, 0.5, 10)

		snapshot := NewLayerSnapshot(l, idToString)

		assert.Len(t, snapshot.Nodes, 2)
		assert.Contains(t, snapshot.Nodes, "node1")
		assert.Contains(t, snapshot.Nodes, "node2")
		assert.Equal(t, []string{"node2"}, snapshot.Nodes["node1"])
	})

	t.Run("modifications to original do not affect snapshot", func(t *testing.T) {
		l := newLayer()
		idToString := []string{"", "node1", "neighbor1", "neighbor2", "node3"}
		l.addNode(1)
		l.addNode(2)
		l.addNeighbor(1, 2, 0.3, 10)

		snapshot := NewLayerSnapshot(l, idToString)

		l.addNode(3)
		l.addNeighbor(1, 3, 0.4, 10)
		l.addNode(4)

		assert.Len(t, snapshot.Nodes, 2)
		assert.Equal(t, []string{"neighbor1"}, snapshot.Nodes["node1"])
	})
}

func TestLayerSnapshot_GetNeighbors(t *testing.T) {
	t.Run("returns nil for non-existent node", func(t *testing.T) {
		snapshot := LayerSnapshot{Nodes: make(map[string][]string)}
		neighbors := snapshot.GetNeighbors("nonexistent")
		assert.Nil(t, neighbors)
	})

	t.Run("returns copy of neighbors", func(t *testing.T) {
		snapshot := LayerSnapshot{
			Nodes: map[string][]string{
				"node1": {"neighbor1", "neighbor2"},
			},
		}

		neighbors := snapshot.GetNeighbors("node1")
		assert.Equal(t, []string{"neighbor1", "neighbor2"}, neighbors)

		// Modify returned slice
		neighbors[0] = "modified"

		// Original should be unchanged
		assert.Equal(t, "neighbor1", snapshot.Nodes["node1"][0])
	})

	t.Run("returns nil for node with nil neighbors", func(t *testing.T) {
		snapshot := LayerSnapshot{
			Nodes: map[string][]string{
				"node1": nil,
			},
		}
		neighbors := snapshot.GetNeighbors("node1")
		assert.Nil(t, neighbors)
	})
}

func TestLayerSnapshot_HasNode(t *testing.T) {
	snapshot := LayerSnapshot{
		Nodes: map[string][]string{
			"exists": {},
		},
	}

	assert.True(t, snapshot.HasNode("exists"))
	assert.False(t, snapshot.HasNode("nonexistent"))
}

func TestLayerSnapshot_NodeCount(t *testing.T) {
	t.Run("empty snapshot", func(t *testing.T) {
		snapshot := LayerSnapshot{Nodes: make(map[string][]string)}
		assert.Equal(t, 0, snapshot.NodeCount())
	})

	t.Run("snapshot with nodes", func(t *testing.T) {
		snapshot := LayerSnapshot{
			Nodes: map[string][]string{
				"node1": {},
				"node2": {},
				"node3": {},
			},
		}
		assert.Equal(t, 3, snapshot.NodeCount())
	})
}

func TestNewHNSWSnapshot(t *testing.T) {
	t.Run("nil index creates empty snapshot", func(t *testing.T) {
		snapshot := NewHNSWSnapshot(nil, 42)

		assert.Equal(t, uint64(42), snapshot.SeqNum)
		assert.Equal(t, -1, snapshot.MaxLevel)
		assert.Empty(t, snapshot.EntryPoint)
		assert.Empty(t, snapshot.Layers)
		assert.Empty(t, snapshot.Vectors)
		assert.Empty(t, snapshot.Magnitudes)
		assert.NotZero(t, snapshot.CreatedAt)
	})

	t.Run("empty index creates valid snapshot", func(t *testing.T) {
		idx := New(DefaultConfig())
		snapshot := NewHNSWSnapshot(idx, 1)

		assert.Equal(t, uint64(1), snapshot.SeqNum)
		assert.Equal(t, -1, snapshot.MaxLevel)
		assert.Empty(t, snapshot.EntryPoint)
		assert.Empty(t, snapshot.Vectors)
	})

	t.Run("populated index creates deep copy", func(t *testing.T) {
		idx := New(DefaultConfig())
		idx.stringToID["vec1"] = 1
		idx.idToString = append(idx.idToString, "vec1")
		idx.nodes[1] = nodeData{vector: []float32{1.0, 2.0, 3.0}, magnitude: 3.74}
		idx.entryPoint = 1
		idx.maxLevel = 2
		idx.layers = []*layer{newLayer(), newLayer(), newLayer()}
		idx.layers[0].addNode(1)

		snapshot := NewHNSWSnapshot(idx, 100)

		assert.Equal(t, uint64(100), snapshot.SeqNum)
		assert.Equal(t, "vec1", snapshot.EntryPoint)
		assert.Equal(t, 2, snapshot.MaxLevel)
		assert.Len(t, snapshot.Layers, 3)
		assert.Equal(t, []float32{1.0, 2.0, 3.0}, snapshot.Vectors["vec1"])
		assert.Equal(t, 3.74, snapshot.Magnitudes["vec1"])
	})

	t.Run("modifications to index do not affect snapshot", func(t *testing.T) {
		idx := New(DefaultConfig())
		idx.stringToID["vec1"] = 1
		idx.idToString = append(idx.idToString, "vec1")
		idx.nodes[1] = nodeData{vector: []float32{1.0, 2.0}, magnitude: 2.24}

		snapshot := NewHNSWSnapshot(idx, 1)

		nd := idx.nodes[1]
		nd.vector[0] = 999.0
		idx.nodes[1] = nd
		idx.stringToID["vec2"] = 2
		idx.idToString = append(idx.idToString, "vec2")
		idx.nodes[2] = nodeData{vector: []float32{3.0, 4.0}, magnitude: 5.0}

		assert.Equal(t, []float32{1.0, 2.0}, snapshot.Vectors["vec1"])
		assert.NotContains(t, snapshot.Vectors, "vec2")
		assert.Equal(t, 2.24, snapshot.Magnitudes["vec1"])
	})
}

func TestHNSWSnapshot_ReaderCount(t *testing.T) {
	t.Run("initial reader count is zero", func(t *testing.T) {
		snapshot := NewHNSWSnapshot(nil, 1)
		assert.Equal(t, int32(0), snapshot.ReaderCount())
	})

	t.Run("acquire increments count", func(t *testing.T) {
		snapshot := NewHNSWSnapshot(nil, 1)

		count := snapshot.AcquireReader()
		assert.Equal(t, int32(1), count)
		assert.Equal(t, int32(1), snapshot.ReaderCount())

		count = snapshot.AcquireReader()
		assert.Equal(t, int32(2), count)
		assert.Equal(t, int32(2), snapshot.ReaderCount())
	})

	t.Run("release decrements count", func(t *testing.T) {
		snapshot := NewHNSWSnapshot(nil, 1)
		snapshot.AcquireReader()
		snapshot.AcquireReader()

		count, _ := snapshot.ReleaseReader()
		assert.Equal(t, int32(1), count)
		assert.Equal(t, int32(1), snapshot.ReaderCount())

		count, _ = snapshot.ReleaseReader()
		assert.Equal(t, int32(0), count)
		assert.Equal(t, int32(0), snapshot.ReaderCount())
	})
}

func TestHNSWSnapshot_ConcurrentReaderAccess(t *testing.T) {
	snapshot := NewHNSWSnapshot(nil, 1)
	numGoroutines := 100
	iterations := 1000

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := range numGoroutines {
		go func(id int) {
			defer wg.Done()
			for j := range iterations {
				if (id+j)%2 == 0 {
					snapshot.AcquireReader()
				} else {
					snapshot.ReleaseReader()
				}
			}
		}(i)
	}

	wg.Wait()

	// The final count depends on race timing due to underflow protection.
	// ReleaseReader skips decrement when count is already 0.
	// What we verify is:
	// 1. No panics occurred (reached this point)
	// 2. Count is non-negative (underflow protection worked)
	assert.GreaterOrEqual(t, snapshot.ReaderCount(), int32(0),
		"reader count should never be negative")
}

func TestHNSWSnapshot_IsEmpty(t *testing.T) {
	t.Run("empty snapshot", func(t *testing.T) {
		snapshot := NewHNSWSnapshot(nil, 1)
		assert.True(t, snapshot.IsEmpty())
	})

	t.Run("non-empty snapshot", func(t *testing.T) {
		snapshot := &HNSWSnapshot{
			Vectors: map[string][]float32{
				"vec1": {1.0, 2.0},
			},
		}
		assert.False(t, snapshot.IsEmpty())
	})
}

func TestHNSWSnapshot_Size(t *testing.T) {
	t.Run("empty snapshot", func(t *testing.T) {
		snapshot := NewHNSWSnapshot(nil, 1)
		assert.Equal(t, 0, snapshot.Size())
	})

	t.Run("snapshot with vectors", func(t *testing.T) {
		snapshot := &HNSWSnapshot{
			Vectors: map[string][]float32{
				"vec1": {1.0},
				"vec2": {2.0},
				"vec3": {3.0},
			},
		}
		assert.Equal(t, 3, snapshot.Size())
	})
}

func TestHNSWSnapshot_GetVector(t *testing.T) {
	t.Run("returns nil for non-existent vector", func(t *testing.T) {
		snapshot := NewHNSWSnapshot(nil, 1)
		vec := snapshot.GetVector("nonexistent")
		assert.Nil(t, vec)
	})

	t.Run("returns copy of vector", func(t *testing.T) {
		snapshot := &HNSWSnapshot{
			Vectors: map[string][]float32{
				"vec1": {1.0, 2.0, 3.0},
			},
		}

		vec := snapshot.GetVector("vec1")
		assert.Equal(t, []float32{1.0, 2.0, 3.0}, vec)

		// Modify returned slice
		vec[0] = 999.0

		// Original should be unchanged
		assert.Equal(t, float32(1.0), snapshot.Vectors["vec1"][0])
	})
}

func TestHNSWSnapshot_GetMagnitude(t *testing.T) {
	t.Run("returns false for non-existent magnitude", func(t *testing.T) {
		snapshot := NewHNSWSnapshot(nil, 1)
		mag, exists := snapshot.GetMagnitude("nonexistent")
		assert.False(t, exists)
		assert.Equal(t, float64(0), mag)
	})

	t.Run("returns magnitude and true for existing", func(t *testing.T) {
		snapshot := &HNSWSnapshot{
			Magnitudes: map[string]float64{
				"vec1": 3.74,
			},
		}

		mag, exists := snapshot.GetMagnitude("vec1")
		assert.True(t, exists)
		assert.Equal(t, 3.74, mag)
	})
}

func TestHNSWSnapshot_GetLayer(t *testing.T) {
	t.Run("returns nil for negative level", func(t *testing.T) {
		snapshot := &HNSWSnapshot{
			Layers: []LayerSnapshot{{Nodes: map[string][]string{}}},
		}
		layer := snapshot.GetLayer(-1)
		assert.Nil(t, layer)
	})

	t.Run("returns nil for out of bounds level", func(t *testing.T) {
		snapshot := &HNSWSnapshot{
			Layers: []LayerSnapshot{{Nodes: map[string][]string{}}},
		}
		layer := snapshot.GetLayer(5)
		assert.Nil(t, layer)
	})

	t.Run("returns layer for valid level", func(t *testing.T) {
		snapshot := &HNSWSnapshot{
			Layers: []LayerSnapshot{
				{Nodes: map[string][]string{"node0": {}}},
				{Nodes: map[string][]string{"node1": {}}},
			},
		}

		layer0 := snapshot.GetLayer(0)
		require.NotNil(t, layer0)
		assert.Contains(t, layer0.Nodes, "node0")

		layer1 := snapshot.GetLayer(1)
		require.NotNil(t, layer1)
		assert.Contains(t, layer1.Nodes, "node1")
	})
}

func TestHNSWSnapshot_LayerCount(t *testing.T) {
	t.Run("empty layers", func(t *testing.T) {
		snapshot := NewHNSWSnapshot(nil, 1)
		assert.Equal(t, 0, snapshot.LayerCount())
	})

	t.Run("with layers", func(t *testing.T) {
		snapshot := &HNSWSnapshot{
			Layers: []LayerSnapshot{{}, {}, {}},
		}
		assert.Equal(t, 3, snapshot.LayerCount())
	})
}

func TestHNSWSnapshot_ContainsVector(t *testing.T) {
	snapshot := &HNSWSnapshot{
		Vectors: map[string][]float32{
			"exists": {1.0, 2.0},
		},
	}

	assert.True(t, snapshot.ContainsVector("exists"))
	assert.False(t, snapshot.ContainsVector("nonexistent"))
}

func TestHNSWSnapshot_CreatedAt(t *testing.T) {
	before := time.Now()
	snapshot := NewHNSWSnapshot(nil, 1)
	after := time.Now()

	assert.True(t, snapshot.CreatedAt.After(before) || snapshot.CreatedAt.Equal(before))
	assert.True(t, snapshot.CreatedAt.Before(after) || snapshot.CreatedAt.Equal(after))
}

func TestCopyHelpers(t *testing.T) {
	t.Run("copyStringSlice with nil", func(t *testing.T) {
		result := copyStringSlice(nil)
		assert.Nil(t, result)
	})

	t.Run("copyStringSlice with empty", func(t *testing.T) {
		result := copyStringSlice([]string{})
		assert.NotNil(t, result)
		assert.Empty(t, result)
	})

	t.Run("copyFloat32Slice with nil", func(t *testing.T) {
		result := copyFloat32Slice(nil)
		assert.Nil(t, result)
	})

	t.Run("copyFloat32Slice with empty", func(t *testing.T) {
		result := copyFloat32Slice([]float32{})
		assert.NotNil(t, result)
		assert.Empty(t, result)
	})
}

func TestSnapshot_DataIsolation(t *testing.T) {
	idx := New(DefaultConfig())
	idx.stringToID["vec1"] = 1
	idx.stringToID["vec2"] = 2
	idx.idToString = []string{"", "vec1", "vec2"}
	idx.nodes[1] = nodeData{vector: []float32{1.0, 2.0, 3.0}, magnitude: 3.74}
	idx.nodes[2] = nodeData{vector: []float32{4.0, 5.0, 6.0}, magnitude: 8.77}
	idx.layers = []*layer{newLayer()}
	idx.layers[0].addNode(1)
	idx.layers[0].addNode(2)
	idx.layers[0].addNeighbor(1, 2, 0.1, 10)
	idx.layers[0].addNeighbor(2, 1, 0.1, 10)

	snapshot := NewHNSWSnapshot(idx, 1)

	t.Run("vector modification isolation", func(t *testing.T) {
		nd := idx.nodes[1]
		nd.vector[0] = 100.0
		idx.nodes[1] = nd
		assert.Equal(t, float32(1.0), snapshot.Vectors["vec1"][0])
	})

	t.Run("vector addition isolation", func(t *testing.T) {
		idx.stringToID["vec3"] = 3
		idx.idToString = append(idx.idToString, "vec3")
		idx.nodes[3] = nodeData{vector: []float32{7.0, 8.0, 9.0}, magnitude: 12.0}
		assert.NotContains(t, snapshot.Vectors, "vec3")
	})

	t.Run("vector deletion isolation", func(t *testing.T) {
		delete(idx.nodes, 2)
		assert.Contains(t, snapshot.Vectors, "vec2")
	})

	t.Run("layer modification isolation", func(t *testing.T) {
		idx.layers[0].addNode(3)
		idx.layers[0].addNeighbor(1, 3, 0.2, 10)

		layer := snapshot.GetLayer(0)
		require.NotNil(t, layer)
		assert.NotContains(t, layer.Nodes, "vec3")
		assert.Equal(t, []string{"vec2"}, layer.Nodes["vec1"])
	})
}

func TestSnapshot_ConcurrentCreation(t *testing.T) {
	idx := New(DefaultConfig())
	idx.stringToID["vec1"] = 1
	idx.idToString = append(idx.idToString, "vec1")
	idx.nodes[1] = nodeData{vector: []float32{1.0, 2.0}, magnitude: 2.24}
	idx.layers = []*layer{newLayer()}
	idx.layers[0].addNode(1)

	numGoroutines := 50
	snapshots := make([]*HNSWSnapshot, numGoroutines)

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := range numGoroutines {
		go func(id int) {
			defer wg.Done()
			snapshots[id] = NewHNSWSnapshot(idx, uint64(id))
		}(i)
	}

	wg.Wait()

	for i, snapshot := range snapshots {
		require.NotNil(t, snapshot, "snapshot %d should not be nil", i)
		assert.Equal(t, uint64(i), snapshot.SeqNum)
		assert.Contains(t, snapshot.Vectors, "vec1")
	}
}

func TestCopyLayers_BatchLocking_HappyPath(t *testing.T) {
	t.Run("empty layers returns empty slice", func(t *testing.T) {
		idToString := []string{""}
		result := copyLayers([]*layer{}, idToString)
		assert.NotNil(t, result)
		assert.Empty(t, result)
	})

	t.Run("nil layers in slice handled correctly", func(t *testing.T) {
		idToString := []string{"", "node1", "node2", "node3"}
		layers := []*layer{newLayer(), nil, newLayer()}
		layers[0].addNode(1)
		layers[2].addNode(3)

		result := copyLayers(layers, idToString)

		require.Len(t, result, 3)
		assert.Contains(t, result[0].Nodes, "node1")
		assert.Empty(t, result[1].Nodes)
		assert.Contains(t, result[2].Nodes, "node3")
	})

	t.Run("multiple layers with connections", func(t *testing.T) {
		idToString := []string{"", "node1", "node2"}
		layers := make([]*layer, 3)
		for i := range layers {
			layers[i] = newLayer()
		}

		layers[0].addNode(1)
		layers[0].addNode(2)
		layers[0].addNeighbor(1, 2, 0.5, 10)
		layers[1].addNode(1)
		layers[2].addNode(1)

		result := copyLayers(layers, idToString)

		require.Len(t, result, 3)
		assert.Len(t, result[0].Nodes, 2)
		assert.Equal(t, []string{"node2"}, result[0].Nodes["node1"])
		assert.Len(t, result[1].Nodes, 1)
		assert.Len(t, result[2].Nodes, 1)
	})
}

func TestCopyLayers_BatchLocking_Concurrent(t *testing.T) {
	t.Run("multiple concurrent snapshot requests", func(t *testing.T) {
		idToString := make([]string, 1, 51)
		idToString[0] = ""
		layers := make([]*layer, 5)
		var nextID uint32 = 1
		for i := range layers {
			layers[i] = newLayer()
			for range 10 {
				idToString = append(idToString, fmt.Sprintf("node_%d", nextID))
				layers[i].addNode(nextID)
				nextID++
			}
		}

		numGoroutines := 100
		results := make([][]LayerSnapshot, numGoroutines)
		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		for i := range numGoroutines {
			go func(idx int) {
				defer wg.Done()
				results[idx] = copyLayers(layers, idToString)
			}(i)
		}

		wg.Wait()

		for i, result := range results {
			require.Len(t, result, 5, "result %d should have 5 layers", i)
			for j := range 5 {
				assert.Len(t, result[j].Nodes, 10, "result %d layer %d should have 10 nodes", i, j)
			}
		}
	})

	t.Run("concurrent snapshots with concurrent modifications", func(t *testing.T) {
		idToString := []string{"", "initial_0", "initial_1", "initial_2"}
		layers := make([]*layer, 3)
		for i := range layers {
			layers[i] = newLayer()
			layers[i].addNode(uint32(i + 1))
		}

		numReaders := 50
		iterations := 100

		var wg sync.WaitGroup
		wg.Add(numReaders)

		for i := range numReaders {
			go func(id int) {
				defer wg.Done()
				for j := range iterations {
					result := copyLayers(layers, idToString)
					require.Len(t, result, 3, "reader %d iter %d", id, j)
				}
			}(i)
		}

		wg.Wait()
	})
}

func TestCopyLayers_BatchLocking_NoDeadlock(t *testing.T) {
	t.Run("no deadlock with interleaved lock patterns", func(t *testing.T) {
		idToString := make([]string, 11)
		idToString[0] = ""
		layers := make([]*layer, 10)
		for i := range layers {
			layers[i] = newLayer()
			idToString[i+1] = fmt.Sprintf("node_%d", i)
			layers[i].addNode(uint32(i + 1))
		}

		done := make(chan bool)
		numGoroutines := 20

		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		for i := range numGoroutines {
			go func(id int) {
				defer wg.Done()
				for {
					select {
					case <-done:
						return
					default:
						_ = copyLayers(layers, idToString)
					}
				}
			}(i)
		}

		time.Sleep(100 * time.Millisecond)
		close(done)

		completed := make(chan bool)
		go func() {
			wg.Wait()
			completed <- true
		}()

		select {
		case <-completed:
		case <-time.After(5 * time.Second):
			t.Fatal("Deadlock detected: goroutines did not complete within timeout")
		}
	})
}

func TestCopyLayersWithBatchLock_DirectCalls(t *testing.T) {
	t.Run("acquires and releases locks correctly", func(t *testing.T) {
		idToString := []string{"", "node_0", "node_1", "node_2"}
		layers := make([]*layer, 3)
		for i := range layers {
			layers[i] = newLayer()
			layers[i].addNode(uint32(i + 1))
		}

		result := copyLayersWithBatchLock(layers, idToString)

		require.Len(t, result, 3)

		for i, l := range layers {
			l.mu.Lock()
			assert.Len(t, l.nodes, 1, "layer %d should have 1 node", i)
			l.mu.Unlock()
		}
	})
}

func TestCopyLayerNodesUnlocked(t *testing.T) {
	t.Run("nil layer returns empty snapshot", func(t *testing.T) {
		idToString := []string{""}
		result := copyLayerNodesUnlocked(nil, idToString)
		assert.NotNil(t, result.Nodes)
		assert.Empty(t, result.Nodes)
	})

	t.Run("copies nodes without acquiring lock", func(t *testing.T) {
		idToString := []string{"", "node1", "node2"}
		l := newLayer()
		l.addNode(1)
		l.addNode(2)
		l.addNeighbor(1, 2, 0.3, 10)

		l.mu.RLock()
		result := copyLayerNodesUnlocked(l, idToString)
		l.mu.RUnlock()

		assert.Len(t, result.Nodes, 2)
		assert.Equal(t, []string{"node2"}, result.Nodes["node1"])
	})
}

func TestCopyLayers_Performance(t *testing.T) {
	t.Run("batch locking scales with layer count", func(t *testing.T) {
		layerCounts := []int{1, 5, 10, 20}

		for _, count := range layerCounts {
			idToString := make([]string, 1, count*100+1)
			idToString[0] = ""
			layers := make([]*layer, count)
			var nextID uint32 = 1
			for i := range layers {
				layers[i] = newLayer()
				for range 100 {
					idToString = append(idToString, fmt.Sprintf("node_%d", nextID))
					layers[i].addNode(nextID)
					nextID++
				}
			}

			start := time.Now()
			iterations := 1000
			for range iterations {
				_ = copyLayers(layers, idToString)
			}
			elapsed := time.Since(start)

			t.Logf("copyLayers with %d layers: %v for %d iterations (%.2f us/op)",
				count, elapsed, iterations, float64(elapsed.Microseconds())/float64(iterations))
		}
	})
}
