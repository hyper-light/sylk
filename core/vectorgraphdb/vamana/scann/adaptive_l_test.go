package scann

import (
	"math/bits"
	"path/filepath"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

func TestDeriveL_ScalesWithDifficulty(t *testing.T) {
	R := 64

	easyGraph := GraphDifficulty{AvgExpansion: 0.5, AvgPathLength: 3, ConvergenceRate: 1.0}
	hardGraph := GraphDifficulty{AvgExpansion: 2.0, AvgPathLength: 10, ConvergenceRate: 1.5}

	for _, target := range []float64{0.90, 0.95, 0.98} {
		easyL := DeriveL(R, target, easyGraph)
		hardL := DeriveL(R, target, hardGraph)

		t.Logf("Target %.0f%%: Easy graph L=%d (%.1fx R), Hard graph L=%d (%.1fx R)",
			target*100, easyL, float64(easyL)/float64(R), hardL, float64(hardL)/float64(R))

		if hardL <= easyL {
			t.Errorf("Hard graph should need higher L than easy graph: hard=%d, easy=%d", hardL, easyL)
		}
	}
}

func TestDeriveL_BoundsAreDataDerived(t *testing.T) {
	R := 64
	difficulty := GraphDifficulty{AvgExpansion: 1.0, AvgPathLength: 5, ConvergenceRate: 1.0}

	L := DeriveL(R, 0.98, difficulty)

	expectedMaxL := R * bits.Len(uint(R)) * bits.Len(uint(R))
	if L > expectedMaxL {
		t.Errorf("L=%d exceeds data-derived max %d", L, expectedMaxL)
	}
	if L < R {
		t.Errorf("L=%d below minimum R=%d", L, R)
	}

	t.Logf("R=%d, L=%d, maxL=%d (bits.Len(R)=%d)", R, L, expectedMaxL, bits.Len(uint(R)))
}

func TestAdaptiveL_ParametersAreDerived(t *testing.T) {
	R := 64
	target := 0.98
	difficulty := GraphDifficulty{AvgExpansion: 1.0, AvgPathLength: 5, ConvergenceRate: 1.0}

	adaptive := NewAdaptiveL(R, target, difficulty)

	expectedWindowSize := bits.Len(uint(R)) * bits.Len(uint(R))
	expectedMaxL := R * bits.Len(uint(R)) * bits.Len(uint(R))
	expectedAdjustStep := max(1, R/bits.Len(uint(R)))

	if adaptive.windowSize != expectedWindowSize {
		t.Errorf("windowSize=%d, want %d (derived from bits.Len(R)^2)", adaptive.windowSize, expectedWindowSize)
	}
	if adaptive.maxL != expectedMaxL {
		t.Errorf("maxL=%d, want %d", adaptive.maxL, expectedMaxL)
	}
	if adaptive.adjustStep != expectedAdjustStep {
		t.Errorf("adjustStep=%d, want %d", adaptive.adjustStep, expectedAdjustStep)
	}

	t.Logf("R=%d: windowSize=%d, maxL=%d, adjustStep=%d", R, adaptive.windowSize, adaptive.maxL, adaptive.adjustStep)
}

func TestAdaptiveL_AdjustsUp(t *testing.T) {
	R := 64
	target := 0.98
	difficulty := GraphDifficulty{AvgExpansion: 1.0, AvgPathLength: 5, ConvergenceRate: 1.0}

	adaptive := NewAdaptiveL(R, target, difficulty)
	initialL := adaptive.L()

	minObs := bits.Len(uint(R))
	for i := 0; i < minObs+2; i++ {
		adaptive.RecordObservation(initialL, 0.85)
	}

	newL := adaptive.L()
	if newL <= initialL {
		t.Errorf("Expected L to increase from %d when recall below target, got %d", initialL, newL)
	}
	t.Logf("Adjusted up: %d -> %d after low recall observations", initialL, newL)
}

func TestAdaptiveL_AdjustsDown(t *testing.T) {
	R := 64
	target := 0.80
	difficulty := GraphDifficulty{AvgExpansion: 1.0, AvgPathLength: 5, ConvergenceRate: 1.0}

	adaptive := NewAdaptiveL(R, target, difficulty)
	initialL := adaptive.L()

	gapThresholdDown := float64(bits.Len(uint(R))) / float64(R)
	requiredRecall := target + gapThresholdDown + 0.05

	minObs := bits.Len(uint(R))
	for i := 0; i < minObs+2; i++ {
		adaptive.RecordObservation(initialL, requiredRecall)
	}

	newL := adaptive.L()
	if newL >= initialL {
		t.Errorf("Expected L to decrease from %d when recall %.2f above target %.2f (threshold %.3f), got %d",
			initialL, requiredRecall, target, gapThresholdDown, newL)
	}
	t.Logf("Adjusted down: %d -> %d after recall=%.2f (target=%.2f, threshold=%.3f)",
		initialL, newL, requiredRecall, target, gapThresholdDown)
}

func TestProbeGraphDifficulty_Sylk(t *testing.T) {
	projectRoot := findProjectRoot(t)
	symbols := collectSymbols(t, projectRoot)
	if len(symbols) < 1000 {
		t.Skipf("Not enough symbols: %d", len(symbols))
	}
	t.Logf("Collected %d symbols", len(symbols))

	tmpDir := t.TempDir()
	config := vamana.DefaultConfig()

	vectorStore, labelStore, embeddings := buildStorageLayer(t, tmpDir, symbols)
	defer vectorStore.Close()
	defer labelStore.Close()

	graphStore, err := storage.CreateGraphStore(filepath.Join(tmpDir, "graph.bin"), config.R, len(symbols))
	if err != nil {
		t.Fatalf("CreateGraphStore failed: %v", err)
	}
	defer graphStore.Close()

	magCache := vamana.NewMagnitudeCache(len(symbols))
	for i := range len(symbols) {
		magCache.GetOrCompute(uint32(i), embeddings[i])
	}

	buildResult := buildWithScaNN(t, embeddings, vectorStore, graphStore, magCache, config)
	snapshot := vectorStore.Snapshot()

	probeStart := time.Now()
	difficulty := ProbeGraphDifficulty(embeddings, snapshot, graphStore, magCache, buildResult.Medoid, config.R)
	probeTime := time.Since(probeStart)

	t.Logf("=== Graph Difficulty Probe ===")
	t.Logf("  Probe time: %v", probeTime)
	t.Logf("  Probe count: %d (expected: bits.Len(%d) = %d)", difficulty.ProbeCount, len(embeddings), bits.Len(uint(len(embeddings))))
	t.Logf("  Avg Expansion: %.2f", difficulty.AvgExpansion)
	t.Logf("  Avg Path Length: %.1f", difficulty.AvgPathLength)
	t.Logf("  Convergence Rate: %.2f", difficulty.ConvergenceRate)

	derivedL := DeriveL(config.R, 0.98, difficulty)
	t.Logf("  Derived L for 98%%: %d (%.1fx R)", derivedL, float64(derivedL)/float64(config.R))
}

func TestDeriveL_Validation_Sylk(t *testing.T) {
	projectRoot := findProjectRoot(t)
	symbols := collectSymbols(t, projectRoot)
	if len(symbols) < 1000 {
		t.Skipf("Not enough symbols: %d", len(symbols))
	}

	tmpDir := t.TempDir()
	config := vamana.DefaultConfig()

	vectorStore, labelStore, embeddings := buildStorageLayer(t, tmpDir, symbols)
	defer vectorStore.Close()
	defer labelStore.Close()

	graphStore, err := storage.CreateGraphStore(filepath.Join(tmpDir, "graph.bin"), config.R, len(symbols))
	if err != nil {
		t.Fatalf("CreateGraphStore failed: %v", err)
	}
	defer graphStore.Close()

	magCache := vamana.NewMagnitudeCache(len(symbols))
	for i := range len(symbols) {
		magCache.GetOrCompute(uint32(i), embeddings[i])
	}

	buildResult := buildWithScaNN(t, embeddings, vectorStore, graphStore, magCache, config)
	snapshot := vectorStore.Snapshot()

	difficulty := ProbeGraphDifficulty(embeddings, snapshot, graphStore, magCache, buildResult.Medoid, config.R)
	t.Logf("Graph difficulty: expansion=%.2f, pathLen=%.1f, probes=%d",
		difficulty.AvgExpansion, difficulty.AvgPathLength, difficulty.ProbeCount)

	numSamples := bits.Len(uint(len(embeddings))) * bits.Len(uint(len(embeddings)))
	sampleIndices := sampleRandomIndices(len(embeddings), numSamples)
	groundTruth := computeGroundTruth(embeddings, sampleIndices, 10, magCache)

	t.Log("\n=== Data-Derived L Validation ===")
	for _, target := range []float64{0.90, 0.95, 0.98} {
		L := DeriveL(config.R, target, difficulty)
		recall := measureRecallAtL(snapshot, graphStore, magCache, embeddings, sampleIndices, groundTruth, buildResult.Medoid, L, 10)

		status := "PASS"
		if recall < target*0.95 {
			status = "WARN"
		}
		t.Logf("%s: Target %.0f%% -> L=%d (%.1fx R) -> Actual %.2f%%",
			status, target*100, L, float64(L)/float64(config.R), recall*100)
	}
}
