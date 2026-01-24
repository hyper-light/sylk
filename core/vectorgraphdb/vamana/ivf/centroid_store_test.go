package ivf

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/viterin/vek/vek32"
)

func TestCentroidStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "centroids.bin")

	k := 16
	dim := 128

	centroids := make([][]float32, k)
	norms := make([]float64, k)

	for i := range k {
		centroids[i] = make([]float32, dim)
		for j := range dim {
			centroids[i][j] = float32(i*dim+j) * 0.01
		}
		norms[i] = math.Sqrt(float64(vek32.Dot(centroids[i], centroids[i])))
	}

	if err := SaveCentroids(path, centroids, norms); err != nil {
		t.Fatalf("save: %v", err)
	}

	store, err := LoadCentroids(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer store.Close()

	if store.K != k {
		t.Errorf("K: got %d, want %d", store.K, k)
	}
	if store.Dim != dim {
		t.Errorf("Dim: got %d, want %d", store.Dim, dim)
	}

	for i := range k {
		loaded := store.GetCentroid(i)
		if loaded == nil {
			t.Fatalf("GetCentroid(%d): nil", i)
		}

		for j := range dim {
			expected := float32(i*dim+j) * 0.01
			if math.Abs(float64(loaded[j]-expected)) > 1e-6 {
				t.Errorf("centroid[%d][%d]: got %v, want %v", i, j, loaded[j], expected)
			}
		}

		loadedNorm := store.GetNorm(i)
		if math.Abs(loadedNorm-norms[i]) > 1e-10 {
			t.Errorf("norm[%d]: got %v, want %v", i, loadedNorm, norms[i])
		}
	}
}

func TestCentroidStore_ZeroCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "centroids.bin")

	k := 4
	dim := 8

	centroids := make([][]float32, k)
	norms := make([]float64, k)

	for i := range k {
		centroids[i] = make([]float32, dim)
		for j := range dim {
			centroids[i][j] = float32(i*10 + j)
		}
		norms[i] = float64(i + 1)
	}

	if err := SaveCentroids(path, centroids, norms); err != nil {
		t.Fatalf("save: %v", err)
	}

	store, err := LoadCentroids(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer store.Close()

	c1 := store.GetCentroid(0)
	c2 := store.GetCentroid(0)

	if &c1[0] != &c2[0] {
		t.Error("GetCentroid should return same backing slice (zero-copy)")
	}

	if len(store.Centroids) != k*dim {
		t.Errorf("flat centroids length: got %d, want %d", len(store.Centroids), k*dim)
	}

	if len(store.Norms) != k {
		t.Errorf("norms length: got %d, want %d", len(store.Norms), k)
	}
}

func TestCentroidStore_ChecksumVerification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "centroids.bin")

	k := 4
	dim := 8

	centroids := make([][]float32, k)
	norms := make([]float64, k)

	for i := range k {
		centroids[i] = make([]float32, dim)
		for j := range dim {
			centroids[i][j] = float32(i + j)
		}
		norms[i] = float64(i)
	}

	if err := SaveCentroids(path, centroids, norms); err != nil {
		t.Fatalf("save: %v", err)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("open for corruption: %v", err)
	}
	if _, err := f.WriteAt([]byte{0xFF, 0xFF, 0xFF, 0xFF}, centroidHeaderSize+10); err != nil {
		f.Close()
		t.Fatalf("corrupt: %v", err)
	}
	f.Close()

	_, err = LoadCentroids(path)
	if err == nil {
		t.Error("expected checksum error on corrupted file")
	}
}

func TestCentroidStore_InvalidMagic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "centroids.bin")

	k := 4
	dim := 8

	centroids := make([][]float32, k)
	norms := make([]float64, k)

	for i := range k {
		centroids[i] = make([]float32, dim)
		norms[i] = float64(i)
	}

	if err := SaveCentroids(path, centroids, norms); err != nil {
		t.Fatalf("save: %v", err)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("open for corruption: %v", err)
	}
	if _, err := f.WriteAt([]byte{'X', 'X', 'X', 'X'}, 0); err != nil {
		f.Close()
		t.Fatalf("corrupt magic: %v", err)
	}
	f.Close()

	_, err = LoadCentroids(path)
	if err == nil {
		t.Error("expected magic error on corrupted file")
	}
}

func TestCentroidStore_OutOfBounds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "centroids.bin")

	k := 4
	dim := 8

	centroids := make([][]float32, k)
	norms := make([]float64, k)

	for i := range k {
		centroids[i] = make([]float32, dim)
		norms[i] = float64(i)
	}

	if err := SaveCentroids(path, centroids, norms); err != nil {
		t.Fatalf("save: %v", err)
	}

	store, err := LoadCentroids(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer store.Close()

	if c := store.GetCentroid(-1); c != nil {
		t.Error("GetCentroid(-1) should return nil")
	}

	if c := store.GetCentroid(k); c != nil {
		t.Error("GetCentroid(k) should return nil")
	}

	if n := store.GetNorm(-1); n != 0 {
		t.Error("GetNorm(-1) should return 0")
	}

	if n := store.GetNorm(k); n != 0 {
		t.Error("GetNorm(k) should return 0")
	}
}

func TestCentroidStore_ValidationErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "centroids.bin")

	err := SaveCentroids(path, nil, nil)
	if err == nil {
		t.Error("expected error for empty centroids")
	}

	err = SaveCentroids(path, [][]float32{{1, 2, 3}}, []float64{1, 2})
	if err == nil {
		t.Error("expected error for mismatched norms length")
	}

	err = SaveCentroids(path, [][]float32{{1, 2, 3}, {1, 2}}, []float64{1, 2})
	if err == nil {
		t.Error("expected error for inconsistent centroid dimensions")
	}
}

func TestCentroidStore_LargeK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "centroids.bin")

	k := 512
	dim := 768

	centroids := make([][]float32, k)
	norms := make([]float64, k)

	for i := range k {
		centroids[i] = make([]float32, dim)
		for j := range dim {
			centroids[i][j] = float32(i*1000+j) * 0.0001
		}
		norms[i] = math.Sqrt(float64(vek32.Dot(centroids[i], centroids[i])))
	}

	if err := SaveCentroids(path, centroids, norms); err != nil {
		t.Fatalf("save: %v", err)
	}

	store, err := LoadCentroids(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer store.Close()

	if store.K != k {
		t.Errorf("K: got %d, want %d", store.K, k)
	}
	if store.Dim != dim {
		t.Errorf("Dim: got %d, want %d", store.Dim, dim)
	}

	for i := 0; i < k; i += 100 {
		loaded := store.GetCentroid(i)
		if loaded == nil {
			t.Fatalf("GetCentroid(%d): nil", i)
		}

		expected := float32(i*1000) * 0.0001
		if math.Abs(float64(loaded[0]-expected)) > 1e-6 {
			t.Errorf("centroid[%d][0]: got %v, want %v", i, loaded[0], expected)
		}

		loadedNorm := store.GetNorm(i)
		if math.Abs(loadedNorm-norms[i]) > 1e-6 {
			t.Errorf("norm[%d]: got %v, want %v", i, loadedNorm, norms[i])
		}
	}
}

func BenchmarkCentroidStore_Load(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "centroids.bin")

	k := 512
	dim := 768

	centroids := make([][]float32, k)
	norms := make([]float64, k)

	for i := range k {
		centroids[i] = make([]float32, dim)
		norms[i] = float64(i)
	}

	if err := SaveCentroids(path, centroids, norms); err != nil {
		b.Fatalf("save: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store, err := LoadCentroids(path)
		if err != nil {
			b.Fatalf("load: %v", err)
		}
		store.Close()
	}
}

func BenchmarkCentroidStore_GetCentroid(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "centroids.bin")

	k := 512
	dim := 768

	centroids := make([][]float32, k)
	norms := make([]float64, k)

	for i := range k {
		centroids[i] = make([]float32, dim)
		norms[i] = float64(i)
	}

	if err := SaveCentroids(path, centroids, norms); err != nil {
		b.Fatalf("save: %v", err)
	}

	store, err := LoadCentroids(path)
	if err != nil {
		b.Fatalf("load: %v", err)
	}
	defer store.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := i % k
		_ = store.GetCentroid(idx)
	}
}
