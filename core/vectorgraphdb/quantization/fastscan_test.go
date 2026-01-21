package quantization

import (
	"math"
	"math/rand"
	"testing"
)

func TestFastScanPQ_NewFastScanPQ(t *testing.T) {
	dim := 128
	numSubspaces := 8

	pq := NewFastScanPQ(dim, numSubspaces)

	if pq.numSubspaces != numSubspaces {
		t.Errorf("expected numSubspaces=%d, got %d", numSubspaces, pq.numSubspaces)
	}
	if pq.subspaceDim != dim/numSubspaces {
		t.Errorf("expected subspaceDim=%d, got %d", dim/numSubspaces, pq.subspaceDim)
	}
	if pq.centroidsPerSubspace != 16 {
		t.Errorf("expected centroidsPerSubspace=16, got %d", pq.centroidsPerSubspace)
	}
	if len(pq.centroids) != numSubspaces {
		t.Errorf("expected %d subspaces in centroids, got %d", numSubspaces, len(pq.centroids))
	}
	for m, subCentroids := range pq.centroids {
		if len(subCentroids) != 16 {
			t.Errorf("subspace %d: expected 16 centroids, got %d", m, len(subCentroids))
		}
	}
}

func TestFastScanPQ_Train(t *testing.T) {
	dim := 32
	numSubspaces := 4
	numVectors := 100

	pq := NewFastScanPQ(dim, numSubspaces)

	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, numVectors)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range vectors[i] {
			vectors[i][j] = rng.Float32()
		}
	}

	pq.Train(vectors)

	for m := 0; m < numSubspaces; m++ {
		for c := 0; c < 16; c++ {
			allZero := true
			for d := 0; d < pq.subspaceDim; d++ {
				if pq.centroids[m][c][d] != 0 {
					allZero = false
					break
				}
			}
			if allZero {
				t.Errorf("subspace %d, centroid %d is all zeros after training", m, c)
			}
		}
	}
}

func TestFastScanPQ_Encode(t *testing.T) {
	dim := 16
	numSubspaces := 4

	pq := NewFastScanPQ(dim, numSubspaces)

	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, 50)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range vectors[i] {
			vectors[i][j] = rng.Float32()
		}
	}
	pq.Train(vectors)

	testVec := make([]float32, dim)
	for i := range testVec {
		testVec[i] = rng.Float32()
	}

	code := pq.Encode(testVec)

	expectedLen := (numSubspaces + 1) / 2
	if len(code) != expectedLen {
		t.Errorf("expected code length %d, got %d", expectedLen, len(code))
	}

	for m := 0; m < numSubspaces; m++ {
		byteIdx := m / 2
		var c int
		if m%2 == 0 {
			c = int(code[byteIdx] & 0x0F)
		} else {
			c = int(code[byteIdx] >> 4)
		}
		if c < 0 || c >= 16 {
			t.Errorf("subspace %d: code %d out of range [0,15]", m, c)
		}
	}
}

func TestFastScanPQ_EncodePackedFormat(t *testing.T) {
	dim := 8
	numSubspaces := 4

	pq := NewFastScanPQ(dim, numSubspaces)

	for m := 0; m < numSubspaces; m++ {
		for c := 0; c < 16; c++ {
			for d := 0; d < pq.subspaceDim; d++ {
				pq.centroids[m][c][d] = float32(c + 1)
			}
		}
	}

	testVec := make([]float32, dim)
	for i := range testVec {
		testVec[i] = 5.0
	}
	code := pq.Encode(testVec)

	if len(code) != 2 {
		t.Fatalf("expected 2 bytes for 4 subspaces, got %d", len(code))
	}

	c0 := int(code[0] & 0x0F)
	c1 := int(code[0] >> 4)
	c2 := int(code[1] & 0x0F)
	c3 := int(code[1] >> 4)

	if c0 != 4 || c1 != 4 || c2 != 4 || c3 != 4 {
		t.Errorf("expected codes [4,4,4,4] (nearest to 5.0), got [%d,%d,%d,%d]", c0, c1, c2, c3)
	}

	lowNibble := code[0] & 0x0F
	highNibble := (code[0] >> 4) & 0x0F
	if lowNibble != highNibble {
		t.Logf("byte 0: low nibble=%d, high nibble=%d (packed correctly)", lowNibble, highNibble)
	}
}

func TestFastScanPQ_ComputeDistanceTable(t *testing.T) {
	dim := 32
	numSubspaces := 4

	pq := NewFastScanPQ(dim, numSubspaces)

	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, 50)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range vectors[i] {
			vectors[i][j] = rng.Float32()
		}
	}
	pq.Train(vectors)

	query := make([]float32, dim)
	for i := range query {
		query[i] = rng.Float32()
	}

	tables := pq.ComputeDistanceTable(query)

	if len(tables) != numSubspaces {
		t.Errorf("expected %d tables, got %d", numSubspaces, len(tables))
	}

	for m, table := range tables {
		if len(table) != 16 {
			t.Errorf("table %d: expected 16 entries, got %d", m, len(table))
		}
		for c, dist := range table {
			if dist < 0 {
				t.Errorf("table[%d][%d]: negative distance %f", m, c, dist)
			}
		}
	}
}

func TestFastScanPQ_AsymmetricDistance(t *testing.T) {
	dim := 32
	numSubspaces := 4

	pq := NewFastScanPQ(dim, numSubspaces)

	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, 50)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range vectors[i] {
			vectors[i][j] = rng.Float32()
		}
	}
	pq.Train(vectors)

	query := vectors[0]
	code := pq.Encode(vectors[0])
	tables := pq.ComputeDistanceTable(query)

	dist := pq.AsymmetricDistance(tables, code)

	if dist < 0 {
		t.Errorf("expected non-negative distance, got %f", dist)
	}

	if dist > 1.0 {
		t.Errorf("distance to self should be small, got %f", dist)
	}
}

func TestFastScanPQ_DistanceOrdering(t *testing.T) {
	dim := 64
	numSubspaces := 8

	pq := NewFastScanPQ(dim, numSubspaces)

	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, 100)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range vectors[i] {
			vectors[i][j] = rng.Float32()
		}
	}
	pq.Train(vectors)

	codes := make([][]byte, len(vectors))
	for i, v := range vectors {
		codes[i] = pq.Encode(v)
	}

	query := vectors[0]
	tables := pq.ComputeDistanceTable(query)

	selfDist := pq.AsymmetricDistance(tables, codes[0])

	farVec := make([]float32, dim)
	for i := range farVec {
		farVec[i] = vectors[0][i] + 10.0
	}
	farCode := pq.Encode(farVec)
	farDist := pq.AsymmetricDistance(tables, farCode)

	if selfDist > farDist {
		t.Errorf("self distance (%f) should be less than far distance (%f)", selfDist, farDist)
	}
}

func TestFastScanPQ_OddSubspaces(t *testing.T) {
	dim := 15
	numSubspaces := 5

	pq := NewFastScanPQ(dim, numSubspaces)

	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, 50)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range vectors[i] {
			vectors[i][j] = rng.Float32()
		}
	}
	pq.Train(vectors)

	testVec := make([]float32, dim)
	for i := range testVec {
		testVec[i] = rng.Float32()
	}

	code := pq.Encode(testVec)

	expectedLen := (numSubspaces + 1) / 2
	if len(code) != expectedLen {
		t.Errorf("expected code length %d for %d subspaces, got %d", expectedLen, numSubspaces, len(code))
	}

	tables := pq.ComputeDistanceTable(testVec)
	dist := pq.AsymmetricDistance(tables, code)

	if math.IsNaN(float64(dist)) || math.IsInf(float64(dist), 0) {
		t.Errorf("invalid distance: %f", dist)
	}
}

func TestFastScanPQ_ConcurrentAccess(t *testing.T) {
	dim := 32
	numSubspaces := 4

	pq := NewFastScanPQ(dim, numSubspaces)

	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, 50)
	for i := range vectors {
		vectors[i] = make([]float32, dim)
		for j := range vectors[i] {
			vectors[i][j] = rng.Float32()
		}
	}
	pq.Train(vectors)

	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func(idx int) {
			vec := vectors[idx%len(vectors)]
			code := pq.Encode(vec)
			tables := pq.ComputeDistanceTable(vec)
			_ = pq.AsymmetricDistance(tables, code)
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}
