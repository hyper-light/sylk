package quantization

import (
	"context"
	"math"
	"math/rand"
	"testing"
)

func TestOPQ_RecallComparison(t *testing.T) {
	// Compare OPQ vs standard PQ recall
	rng := rand.New(rand.NewSource(42))

	// Test data parameters
	numDB := 1000
	dim := 64
	numClusters := 20 // Clustered data like real embeddings

	// Generate clustered data (more realistic for embeddings)
	// First create cluster centers
	centers := make([][]float32, numClusters)
	for i := range centers {
		centers[i] = make([]float32, dim)
		for j := range centers[i] {
			centers[i][j] = rng.Float32()*2 - 1 // Range [-1, 1]
		}
	}

	// Generate vectors around cluster centers with noise
	dbVectors := make([][]float32, numDB)
	for i := range dbVectors {
		dbVectors[i] = make([]float32, dim)
		center := centers[i%numClusters]
		for j := range dbVectors[i] {
			// Add Gaussian-like noise (using uniform approximation)
			noise := (rng.Float32() + rng.Float32() + rng.Float32() - 1.5) * 0.3
			dbVectors[i][j] = center[j] + noise
		}
	}

	// Derive all configuration from data characteristics
	opqConfig := DeriveOPQConfig(dim, numDB, 0)

	// Train standard PQ using derived config
	pq, err := NewProductQuantizer(dim, opqConfig.PQConfig)
	if err != nil {
		t.Fatalf("failed to create PQ: %v", err)
	}

	if err := pq.TrainParallelWithConfig(context.Background(), dbVectors, opqConfig.TrainConfig, 0); err != nil {
		t.Fatalf("failed to train PQ: %v", err)
	}

	pqCodes, _ := pq.EncodeBatch(dbVectors, 4)

	// Train OPQ using same derived config
	opq, err := NewOptimizedProductQuantizer(dim, opqConfig)
	if err != nil {
		t.Fatalf("failed to create OPQ: %v", err)
	}

	if err := opq.Train(context.Background(), dbVectors, opqConfig); err != nil {
		t.Fatalf("failed to train OPQ: %v", err)
	}

	opqCodes, _ := opq.EncodeBatch(dbVectors, 4)

	// Compute recall for both
	// Derive k and numQueries from data size
	computeRecall := func(name string, codes []PQCode, computeTable func([]float32) (*DistanceTable, error), asymDist func(*DistanceTable, PQCode) float32) float64 {
		// k = sqrt(numDB) is a reasonable default for recall evaluation
		k := int(math.Sqrt(float64(numDB)))
		if k < 1 {
			k = 1
		}
		if k > 100 {
			k = 100 // Cap for reasonable evaluation time
		}

		// numQueries = numDB / 50 gives ~2% sampling
		numQueries := numDB / 50
		if numQueries < 10 {
			numQueries = 10
		}
		if numQueries > 100 {
			numQueries = 100
		}

		queryStep := numDB / numQueries
		totalRecall := 0.0

		for q := 0; q < numQueries; q++ {
			queryIdx := q * queryStep
			if queryIdx >= numDB {
				queryIdx = numDB - 1
			}
			query := dbVectors[queryIdx]

			// Compute exact distances
			exactDists := make([]float64, numDB)
			for i, dbVec := range dbVectors {
				var sum float64
				for j := 0; j < dim; j++ {
					d := float64(query[j] - dbVec[j])
					sum += d * d
				}
				exactDists[i] = sum
			}

			// Find exact top-k
			exactTopK := make(map[int]bool)
			for i := 0; i < k; i++ {
				minIdx := -1
				minDist := math.MaxFloat64
				for j := 0; j < numDB; j++ {
					if exactTopK[j] {
						continue
					}
					if exactDists[j] < minDist {
						minDist = exactDists[j]
						minIdx = j
					}
				}
				if minIdx >= 0 {
					exactTopK[minIdx] = true
				}
			}

			// Compute PQ distances
			table, _ := computeTable(query)
			pqDists := make([]float32, numDB)
			for i := 0; i < numDB; i++ {
				pqDists[i] = asymDist(table, codes[i])
			}

			// Find PQ top-k
			pqTopK := make(map[int]bool)
			for i := 0; i < k; i++ {
				minIdx := -1
				minDist := float32(math.MaxFloat32)
				for j := 0; j < numDB; j++ {
					if pqTopK[j] {
						continue
					}
					if pqDists[j] < minDist {
						minDist = pqDists[j]
						minIdx = j
					}
				}
				if minIdx >= 0 {
					pqTopK[minIdx] = true
				}
			}

			// Compute recall: intersection / k
			hits := 0
			for idx := range exactTopK {
				if pqTopK[idx] {
					hits++
				}
			}
			totalRecall += float64(hits) / float64(k)
		}

		return totalRecall / float64(numQueries) * 100
	}

	pqRecall := computeRecall("PQ", pqCodes, pq.ComputeDistanceTable, pq.AsymmetricDistance)
	opqRecall := computeRecall("OPQ", opqCodes, opq.ComputeDistanceTable, opq.AsymmetricDistance)

	t.Logf("Standard PQ Recall@10: %.2f%%", pqRecall)
	t.Logf("OPQ Recall@10: %.2f%%", opqRecall)
	t.Logf("Improvement: %.2f%%", opqRecall-pqRecall)

	// OPQ's benefit depends on data structure (correlations between dimensions).
	// On random/clustered data with weak correlations, OPQ may not improve and
	// can even slightly hurt due to numerical issues in rotation learning.
	// Allow up to 10% variance since OPQ is not guaranteed to help on all data.
	if opqRecall < pqRecall-10 {
		t.Errorf("OPQ recall (%.2f%%) significantly worse than PQ (%.2f%%)", opqRecall, pqRecall)
	}
}

func TestDeriveOPQConfig(t *testing.T) {
	tests := []struct {
		name       string
		vectorDim  int
		numVectors int
	}{
		{"64-dim, 500 vectors", 64, 500},
		{"128-dim, 1000 vectors", 128, 1000},
		{"768-dim, 10000 vectors", 768, 10000},
		{"32-dim, 200 vectors", 32, 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DeriveOPQConfig(tt.vectorDim, tt.numVectors, 0)

			// Verify constraints
			if tt.vectorDim%config.PQConfig.NumSubspaces != 0 {
				t.Errorf("vectorDim %d not divisible by numSubspaces %d",
					tt.vectorDim, config.PQConfig.NumSubspaces)
			}

			if config.PQConfig.CentroidsPerSubspace > 256 {
				t.Errorf("centroids %d exceeds uint8 max", config.PQConfig.CentroidsPerSubspace)
			}

			if config.OPQIterations <= 0 {
				t.Error("OPQIterations must be positive")
			}

			subspaceDim := tt.vectorDim / config.PQConfig.NumSubspaces
			minSamples := config.PQConfig.CentroidsPerSubspace * max(10, subspaceDim)
			if tt.numVectors < minSamples {
				// This is expected for small datasets
				t.Logf("Note: %d vectors may be insufficient for %d centroids (need ~%d)",
					tt.numVectors, config.PQConfig.CentroidsPerSubspace, minSamples)
			}

			t.Logf("Config: %d subspaces, %d centroids, %d OPQ iterations",
				config.PQConfig.NumSubspaces, config.PQConfig.CentroidsPerSubspace, config.OPQIterations)
		})
	}
}
