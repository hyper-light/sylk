package scann

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

func TestDeriveL_WithDifficulty(t *testing.T) {
	R := 64

	easyGraph := GraphDifficulty{AvgExpansion: 1.0, AvgPathLength: 5, ConvergenceRate: 1.0}
	hardGraph := GraphDifficulty{AvgExpansion: 2.5, AvgPathLength: 15, ConvergenceRate: 1.5}

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

func TestDeriveL98(t *testing.T) {
	R := 64
	difficulty := GraphDifficulty{AvgExpansion: 1.5, AvgPathLength: 10, ConvergenceRate: 1.0}
	L := DeriveL98(R, difficulty)

	t.Logf("DeriveL98(R=%d, avgExpansion=1.5) = %d (%.1fx R)", R, L, float64(L)/float64(R))

	if L < R*2 || L > R*8 {
		t.Errorf("DeriveL98(R=%d) = %d, want [%d, %d]", R, L, R*2, R*8)
	}
}

func TestAdaptiveL_Initial(t *testing.T) {
	R := 64
	target := 0.98
	difficulty := GraphDifficulty{AvgExpansion: 1.5, AvgPathLength: 10, ConvergenceRate: 1.0}

	adaptive := NewAdaptiveL(R, target, difficulty)
	L := adaptive.L()

	expectedL := DeriveL(R, target, difficulty)
	if L != expectedL {
		t.Errorf("Initial L = %d, want %d", L, expectedL)
	}
	t.Logf("Initial L = %d (%.1fx R)", L, float64(L)/float64(R))
}

func TestAdaptiveL_AdjustsUp(t *testing.T) {
	R := 64
	target := 0.98
	difficulty := GraphDifficulty{AvgExpansion: 1.5, AvgPathLength: 10, ConvergenceRate: 1.0}

	adaptive := NewAdaptiveL(R, target, difficulty)
	initialL := adaptive.L()

	for i := 0; i < 5; i++ {
		adaptive.RecordObservation(initialL, 0.90)
	}

	newL := adaptive.L()
	if newL <= initialL {
		t.Errorf("Expected L to increase from %d when recall below target, got %d", initialL, newL)
	}
	t.Logf("Adjusted up: %d -> %d after low recall observations", initialL, newL)
}

func TestAdaptiveL_AdjustsDown(t *testing.T) {
	R := 64
	target := 0.95
	difficulty := GraphDifficulty{AvgExpansion: 1.5, AvgPathLength: 10, ConvergenceRate: 1.0}

	adaptive := NewAdaptiveL(R, target, difficulty)
	initialL := adaptive.L()

	for i := 0; i < 5; i++ {
		adaptive.RecordObservation(initialL, 1.0)
	}

	newL := adaptive.L()
	if newL >= initialL {
		t.Errorf("Expected L to decrease from %d when recall significantly above target, got %d", initialL, newL)
	}
	t.Logf("Adjusted down: %d -> %d after high recall observations", initialL, newL)
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
	t.Logf("  Avg Expansion: %.2f", difficulty.AvgExpansion)
	t.Logf("  Avg Path Length: %.1f", difficulty.AvgPathLength)
	t.Logf("  Convergence Rate: %.2f", difficulty.ConvergenceRate)

	derivedL := DeriveL(config.R, 0.98, difficulty)
	t.Logf("  Derived L for 98%%: %d (%.1fx R)", derivedL, float64(derivedL)/float64(config.R))

	if probeTime > 50*time.Millisecond {
		t.Errorf("Probe took too long: %v (want < 50ms)", probeTime)
	}
}

func TestQuickCalibrate_Sylk(t *testing.T) {
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

	t.Log("=== QuickCalibrate Results ===")

	for _, target := range []float64{0.95, 0.98} {
		calibStart := time.Now()
		calibratedL := QuickCalibrate(target, embeddings, vectorStore, graphStore, magCache, buildResult.Medoid, config.R)
		calibTime := time.Since(calibStart)

		snapshot := vectorStore.Snapshot()
		sampleIndices := sampleRandomIndices(len(embeddings), 100)
		groundTruth := computeGroundTruth(embeddings, sampleIndices, 10, magCache)
		actualRecall := measureRecallAtL(snapshot, graphStore, magCache, embeddings, sampleIndices, groundTruth, buildResult.Medoid, calibratedL, 10)

		t.Logf("Target %.0f%%: L=%d (%.1fx R) -> %.2f%% recall (calibration took %v)",
			target*100, calibratedL, float64(calibratedL)/float64(config.R), actualRecall*100, calibTime)

		if calibTime > 100*time.Millisecond {
			t.Errorf("Calibration took too long: %v (want < 100ms)", calibTime)
		}
	}
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
	t.Logf("Graph difficulty: expansion=%.2f, pathLen=%.1f", difficulty.AvgExpansion, difficulty.AvgPathLength)

	sampleIndices := sampleRandomIndices(len(embeddings), 100)
	groundTruth := computeGroundTruth(embeddings, sampleIndices, 10, magCache)

	t.Log("\n=== Data-Derived L Validation ===")
	for _, target := range []float64{0.90, 0.95, 0.98, 0.99} {
		L := DeriveL(config.R, target, difficulty)
		recall := measureRecallAtL(snapshot, graphStore, magCache, embeddings, sampleIndices, groundTruth, buildResult.Medoid, L, 10)

		status := "PASS"
		if recall < target*0.97 {
			status = "FAIL"
		}
		t.Logf("%s: Target %.0f%% -> L=%d (%.1fx R) -> Actual %.2f%%",
			status, target*100, L, float64(L)/float64(config.R), recall*100)
	}
}
