package hnsw

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLayerSnapshot(t *testing.T) {
	t.Run("nil layer returns empty snapshot", func(t *testing.T) {
		snapshot := NewLayerSnapshot(nil)
		assert.NotNil(t, snapshot.Nodes)
		assert.Empty(t, snapshot.Nodes)
	})

	t.Run("empty layer returns empty nodes", func(t *testing.T) {
		l := newLayer()
		snapshot := NewLayerSnapshot(l)
		assert.NotNil(t, snapshot.Nodes)
		assert.Empty(t, snapshot.Nodes)
	})

	t.Run("layer with nodes creates deep copy", func(t *testing.T) {
		l := newLayer()
		l.addNode("node1")
		l.addNode("node2")
		l.addNeighbor("node1", "node2", 0.5, 10)

		snapshot := NewLayerSnapshot(l)

		assert.Len(t, snapshot.Nodes, 2)
		assert.Contains(t, snapshot.Nodes, "node1")
		assert.Contains(t, snapshot.Nodes, "node2")
		assert.Equal(t, []string{"node2"}, snapshot.Nodes["node1"])
	})

	t.Run("modifications to original do not affect snapshot", func(t *testing.T) {
		l := newLayer()
		l.addNode("node1")
		l.addNeighbor("node1", "neighbor1", 0.3, 10)

		snapshot := NewLayerSnapshot(l)

		// Modify original layer
		l.addNeighbor("node1", "neighbor2", 0.4, 10)
		l.addNode("node3")

		// Snapshot should be unchanged
		assert.Len(t, snapshot.Nodes, 1)
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
		idx.vectors["vec1"] = []float32{1.0, 2.0, 3.0}
		idx.magnitudes["vec1"] = 3.74
		idx.entryPoint = "vec1"
		idx.maxLevel = 2
		idx.layers = []*layer{newLayer(), newLayer(), newLayer()}
		idx.layers[0].addNode("vec1")

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
		idx.vectors["vec1"] = []float32{1.0, 2.0}
		idx.magnitudes["vec1"] = 2.24

		snapshot := NewHNSWSnapshot(idx, 1)

		// Modify original
		idx.vectors["vec1"][0] = 999.0
		idx.vectors["vec2"] = []float32{3.0, 4.0}
		delete(idx.magnitudes, "vec1")

		// Snapshot should be unchanged
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

		count := snapshot.ReleaseReader()
		assert.Equal(t, int32(1), count)
		assert.Equal(t, int32(1), snapshot.ReaderCount())

		count = snapshot.ReleaseReader()
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

	// Final count should be deterministic based on the pattern
	// Each goroutine does iterations/2 acquires and iterations/2 releases
	// So net change per goroutine is 0, total should be 0
	assert.Equal(t, int32(0), snapshot.ReaderCount())
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
	// Create an index with data
	idx := New(DefaultConfig())
	idx.vectors["vec1"] = []float32{1.0, 2.0, 3.0}
	idx.vectors["vec2"] = []float32{4.0, 5.0, 6.0}
	idx.magnitudes["vec1"] = 3.74
	idx.magnitudes["vec2"] = 8.77
	idx.layers = []*layer{newLayer()}
	idx.layers[0].addNode("vec1")
	idx.layers[0].addNode("vec2")
	idx.layers[0].addNeighbor("vec1", "vec2", 0.1, 10)
	idx.layers[0].addNeighbor("vec2", "vec1", 0.1, 10)

	// Create snapshot
	snapshot := NewHNSWSnapshot(idx, 1)

	// Verify isolation in multiple ways
	t.Run("vector modification isolation", func(t *testing.T) {
		idx.vectors["vec1"][0] = 100.0
		assert.Equal(t, float32(1.0), snapshot.Vectors["vec1"][0])
	})

	t.Run("vector addition isolation", func(t *testing.T) {
		idx.vectors["vec3"] = []float32{7.0, 8.0, 9.0}
		assert.NotContains(t, snapshot.Vectors, "vec3")
	})

	t.Run("vector deletion isolation", func(t *testing.T) {
		delete(idx.vectors, "vec2")
		assert.Contains(t, snapshot.Vectors, "vec2")
	})

	t.Run("layer modification isolation", func(t *testing.T) {
		idx.layers[0].addNode("vec3")
		idx.layers[0].addNeighbor("vec1", "vec3", 0.2, 10)

		layer := snapshot.GetLayer(0)
		require.NotNil(t, layer)
		assert.NotContains(t, layer.Nodes, "vec3")
		assert.Equal(t, []string{"vec2"}, layer.Nodes["vec1"])
	})
}

func TestSnapshot_ConcurrentCreation(t *testing.T) {
	idx := New(DefaultConfig())
	idx.vectors["vec1"] = []float32{1.0, 2.0}
	idx.magnitudes["vec1"] = 2.24
	idx.layers = []*layer{newLayer()}
	idx.layers[0].addNode("vec1")

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

	// Verify all snapshots were created correctly
	for i, snapshot := range snapshots {
		require.NotNil(t, snapshot, "snapshot %d should not be nil", i)
		assert.Equal(t, uint64(i), snapshot.SeqNum)
		assert.Contains(t, snapshot.Vectors, "vec1")
	}
}
