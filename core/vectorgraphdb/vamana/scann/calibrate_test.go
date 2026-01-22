package scann

import (
	"math/rand/v2"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

func TestCalibrateL(t *testing.T) {
	n := 5000
	dim := 128
	vectors := generateRandomVectors(n, dim)

	tmpDir := t.TempDir()
	vectorPath := filepath.Join(tmpDir, "vectors.bin")
	graphPath := filepath.Join(tmpDir, "graph.bin")

	vectorStore, err := storage.CreateVectorStore(vectorPath, dim, n)
	if err != nil {
		t.Fatalf("failed to create vector store: %v", err)
	}
	defer vectorStore.Close()

	for _, vec := range vectors {
		if _, err := vectorStore.Append(vec); err != nil {
			t.Fatalf("failed to append vector: %v", err)
		}
	}

	R := 32
	graphStore, err := storage.CreateGraphStore(graphPath, R, n)
	if err != nil {
		t.Fatalf("failed to create graph store: %v", err)
	}
	defer graphStore.Close()

	magCache := vamana.NewMagnitudeCache(n)

	vamanaConf := vamana.VamanaConfig{R: R, L: R, Alpha: 1.2}
	batchConf := BatchBuildConfig{NumWorkers: 4}
	avqConf := AVQConfig{NumPartitions: 8}

	builder := NewBatchBuilder(batchConf, avqConf, vamanaConf)
	result, err := builder.Build(vectors, vectorStore, graphStore, magCache)
	if err != nil {
		t.Fatalf("failed to build index: %v", err)
	}

	cfg := CalibrationConfig{
		TargetRecall:      0.90,
		ConfidenceLevel:   0.90,
		NumSamplesPerStep: 20,
		K:                 10,
		MaxL:              200,
		MaxIterations:     20,
	}

	calResult := CalibrateL(cfg, vectors, vectorStore, graphStore, magCache, result.Medoid, R)

	t.Logf("Calibration: L=%d, Recall=%.2f%%, Iterations=%d, Converged=%v, Interval=[%d,%d]",
		calResult.L, calResult.Recall*100, calResult.Iterations, calResult.Converged, calResult.LowerBound, calResult.UpperBound)

	if calResult.L < R {
		t.Errorf("calibrated L=%d should be >= R=%d", calResult.L, R)
	}

	if calResult.Recall < cfg.TargetRecall*0.95 {
		t.Errorf("calibrated recall %.2f%% significantly below target %.2f%%", calResult.Recall*100, cfg.TargetRecall*100)
	}
}

func TestCalibrateL_SmallDataset(t *testing.T) {
	n := 100
	dim := 32
	vectors := generateRandomVectors(n, dim)

	tmpDir := t.TempDir()
	vectorPath := filepath.Join(tmpDir, "vectors.bin")
	graphPath := filepath.Join(tmpDir, "graph.bin")

	vectorStore, err := storage.CreateVectorStore(vectorPath, dim, n)
	if err != nil {
		t.Fatalf("failed to create vector store: %v", err)
	}
	defer vectorStore.Close()

	for _, vec := range vectors {
		vectorStore.Append(vec)
	}

	R := 16
	graphStore, err := storage.CreateGraphStore(graphPath, R, n)
	if err != nil {
		t.Fatalf("failed to create graph store: %v", err)
	}
	defer graphStore.Close()

	magCache := vamana.NewMagnitudeCache(n)

	vamanaConf := vamana.VamanaConfig{R: R, L: R, Alpha: 1.2}
	batchConf := BatchBuildConfig{NumWorkers: 2}
	avqConf := AVQConfig{NumPartitions: 4}

	builder := NewBatchBuilder(batchConf, avqConf, vamanaConf)
	result, _ := builder.Build(vectors, vectorStore, graphStore, magCache)

	cfg := DefaultCalibrationConfig()
	cfg.MaxL = 100
	cfg.MaxIterations = 15

	calResult := CalibrateL(cfg, vectors, vectorStore, graphStore, magCache, result.Medoid, R)

	t.Logf("Small dataset: L=%d, Recall=%.2f%%, Iterations=%d, Converged=%v",
		calResult.L, calResult.Recall*100, calResult.Iterations, calResult.Converged)

	if calResult.L <= 0 {
		t.Errorf("calibrated L should be positive, got %d", calResult.L)
	}
}

func TestCalibrateL_EmptyDataset(t *testing.T) {
	cfg := DefaultCalibrationConfig()
	R := 32

	result := CalibrateL(cfg, nil, nil, nil, nil, 0, R)

	if result.L != R {
		t.Errorf("expected L=%d for empty dataset, got %d", R, result.L)
	}
	if result.Converged {
		t.Errorf("expected Converged=false for empty dataset")
	}
}

func generateRandomVectors(n, dim int) [][]float32 {
	vectors := make([][]float32, n)
	for i := range n {
		vec := make([]float32, dim)
		for j := range dim {
			vec[j] = rand.Float32()*2 - 1
		}
		vectors[i] = vec
	}
	return vectors
}

func TestCalibrateL_Sylk(t *testing.T) {
	projectRoot := findProjectRoot(t)
	t.Logf("Project root: %s", projectRoot)

	symbols := collectSymbols(t, projectRoot)
	if len(symbols) < 1000 {
		t.Skipf("Not enough symbols for calibration test: %d", len(symbols))
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

	cfg := CalibrationConfig{
		TargetRecall:      0.95,
		ConfidenceLevel:   0.95,
		NumSamplesPerStep: 30,
		K:                 10,
		MaxL:              500,
		MaxIterations:     25,
	}

	calResult := CalibrateL(cfg, embeddings, vectorStore, graphStore, magCache, buildResult.Medoid, config.R)

	t.Logf("=== PBA Calibration Result ===")
	t.Logf("  Target Recall: %.0f%%", cfg.TargetRecall*100)
	t.Logf("  Calibrated L: %d", calResult.L)
	t.Logf("  Achieved Recall: %.2f%%", calResult.Recall*100)
	t.Logf("  Confidence: %.2f%%", calResult.Confidence*100)
	t.Logf("  Iterations: %d", calResult.Iterations)
	t.Logf("  Converged: %v", calResult.Converged)
	t.Logf("  95%% CI: [%d, %d]", calResult.LowerBound, calResult.UpperBound)

	if calResult.Recall < cfg.TargetRecall*0.95 {
		t.Errorf("FAIL: Calibrated recall %.2f%% significantly below target %.0f%%", calResult.Recall*100, cfg.TargetRecall*100)
	} else {
		t.Logf("PASS: Calibrated L=%d achieves %.2f%% recall (target %.0f%%)", calResult.L, calResult.Recall*100, cfg.TargetRecall*100)
	}
}
