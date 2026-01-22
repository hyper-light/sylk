package stitch

import (
	"math/rand/v2"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/scann"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

func TestStitch_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	dim := 128
	mainSize := 1000
	segmentSize := 200
	totalSize := mainSize + segmentSize

	rng := rand.New(rand.NewPCG(42, 42))
	allVectors := make([][]float32, totalSize)
	for i := range totalSize {
		vec := make([]float32, dim)
		for j := range dim {
			vec[j] = rng.Float32()*2 - 1
		}
		allVectors[i] = vec
	}

	vectorStore, err := storage.CreateVectorStore(filepath.Join(tmpDir, "vectors.bin"), dim, totalSize)
	if err != nil {
		t.Fatalf("CreateVectorStore: %v", err)
	}
	defer vectorStore.Close()

	for _, vec := range allVectors {
		if _, err := vectorStore.Append(vec); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	config := vamana.DefaultConfig()

	mainGraph, err := storage.CreateGraphStore(filepath.Join(tmpDir, "main_graph.bin"), config.R, mainSize)
	if err != nil {
		t.Fatalf("CreateGraphStore main: %v", err)
	}
	defer mainGraph.Close()

	segmentGraph, err := storage.CreateGraphStore(filepath.Join(tmpDir, "segment_graph.bin"), config.R, segmentSize)
	if err != nil {
		t.Fatalf("CreateGraphStore segment: %v", err)
	}
	defer segmentGraph.Close()

	magCache := vamana.NewMagnitudeCache(totalSize)

	mainVectors := allVectors[:mainSize]
	mainMedoid := buildGraph(t, tmpDir, "main", mainVectors, mainGraph, vectorStore, magCache, config, 0)
	t.Logf("Main graph built: %d nodes, medoid=%d", mainGraph.Count(), mainMedoid)

	segmentVectors := allVectors[mainSize:]
	segmentMedoid := buildGraph(t, tmpDir, "segment", segmentVectors, segmentGraph, vectorStore, magCache, config, uint32(mainSize))
	t.Logf("Segment graph built: %d nodes, medoid=%d", segmentGraph.Count(), segmentMedoid)

	stitchConfig := DefaultStitchConfig()
	stitcher := NewStitcher(stitchConfig, magCache)

	result, err := stitcher.Stitch(mainGraph, segmentGraph, vectorStore, mainMedoid, segmentMedoid)
	if err != nil {
		t.Fatalf("Stitch: %v", err)
	}

	t.Logf("Stitch result:")
	t.Logf("  Main boundary nodes: %d", result.MainBoundaryNodes)
	t.Logf("  Segment boundary nodes: %d", result.SegmentBoundaryNodes)
	t.Logf("  Cross edges added: %d", result.CrossEdgesAdded)
	t.Logf("  Searches performed: %d", result.SearchesPerformed)
	t.Logf("  Prune operations: %d", result.PruneOperations)

	if result.CrossEdgesAdded == 0 {
		t.Error("Expected cross edges to be added")
	}

	queryIdx := mainSize + segmentSize/2
	query := allVectors[queryIdx]

	results := vamana.GreedySearch(query, mainMedoid, config.L, 10, vectorStore, mainGraph, magCache)

	foundSegmentNode := false
	for _, r := range results {
		if uint32(r.InternalID) >= uint32(mainSize) {
			foundSegmentNode = true
			break
		}
	}

	if !foundSegmentNode {
		t.Log("WARNING: Search from main graph did not find segment nodes in top 10")
		t.Log("This may indicate stitching quality issues")
	} else {
		t.Log("PASS: Search from main graph successfully found segment nodes")
	}
}

func buildGraph(t *testing.T, tmpDir, name string, vectors [][]float32, graph *storage.GraphStore, vectorStore *storage.VectorStore, magCache *vamana.MagnitudeCache, config vamana.VamanaConfig, idOffset uint32) uint32 {
	t.Helper()

	n := len(vectors)
	if n == 0 {
		return 0
	}

	for i, vec := range vectors {
		globalID := uint32(i) + idOffset
		magCache.GetOrCompute(globalID, vec)
	}

	avqConfig := scann.AVQConfig{NumPartitions: 4, CodebookSize: 256, AnisotropicWeight: 0.2}
	batchConfig := scann.DefaultBatchBuildConfig()
	builder := scann.NewBatchBuilder(batchConfig, avqConfig, config)

	result, err := builder.Build(vectors, vectorStore, graph, magCache)
	if err != nil {
		t.Fatalf("ScaNN build: %v", err)
	}

	return result.Medoid
}

func TestBoundary_Sampling(t *testing.T) {
	tmpDir := t.TempDir()
	n := 1000
	R := 64

	graph, err := storage.CreateGraphStore(filepath.Join(tmpDir, "graph.bin"), R, n)
	if err != nil {
		t.Fatalf("CreateGraphStore: %v", err)
	}
	defer graph.Close()

	rng := rand.New(rand.NewPCG(123, 456))
	for i := range n {
		numNeighbors := rng.IntN(R) + 1
		neighbors := make([]uint32, numNeighbors)
		for j := range numNeighbors {
			neighbors[j] = uint32(rng.IntN(n))
		}
		graph.SetNeighbors(uint32(i), neighbors)
	}

	t.Run("Random", func(t *testing.T) {
		config := BoundaryConfig{Strategy: StrategyRandom, K: 50, Seed: 42}
		boundary := SampleBoundary(graph, config)
		if len(boundary) != 50 {
			t.Errorf("Expected 50 boundary nodes, got %d", len(boundary))
		}
		seen := make(map[uint32]bool)
		for _, id := range boundary {
			if seen[id] {
				t.Errorf("Duplicate node %d in boundary", id)
			}
			seen[id] = true
		}
	})

	t.Run("HighDegree", func(t *testing.T) {
		config := BoundaryConfig{Strategy: StrategyHighDegree, K: 10}
		boundary := SampleBoundary(graph, config)
		if len(boundary) != 10 {
			t.Errorf("Expected 10 boundary nodes, got %d", len(boundary))
		}
		prevDegree := R + 1
		for _, id := range boundary {
			degree := len(graph.GetNeighbors(id))
			if degree > prevDegree {
				t.Errorf("Nodes not sorted by degree: %d > %d", degree, prevDegree)
			}
			prevDegree = degree
		}
	})

	t.Run("Hybrid", func(t *testing.T) {
		config := BoundaryConfig{Strategy: StrategyHybrid, K: 20, Seed: 42}
		boundary := SampleBoundary(graph, config)
		if len(boundary) != 20 {
			t.Errorf("Expected 20 boundary nodes, got %d", len(boundary))
		}
		seen := make(map[uint32]bool)
		for _, id := range boundary {
			if seen[id] {
				t.Errorf("Duplicate node %d in boundary", id)
			}
			seen[id] = true
		}
	})

	t.Run("DefaultK", func(t *testing.T) {
		config := BoundaryConfig{Strategy: StrategyRandom, K: 0, Seed: 42}
		boundary := SampleBoundary(graph, config)
		expectedK := 31 // √1000 ≈ 31
		if len(boundary) != expectedK {
			t.Errorf("Expected %d boundary nodes (√N), got %d", expectedK, len(boundary))
		}
	})
}
