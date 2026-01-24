package ivf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPartitionIndex_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partitions.bin")

	k := 8
	partitions := make([][]uint32, k)

	totalIDs := uint32(0)
	for i := range k {
		size := (i + 1) * 10
		partitions[i] = make([]uint32, size)
		for j := range size {
			partitions[i][j] = totalIDs
			totalIDs++
		}
	}

	if err := SavePartitionIndex(path, partitions); err != nil {
		t.Fatalf("save: %v", err)
	}

	pi, err := LoadPartitionIndex(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer pi.Close()

	if pi.K != k {
		t.Errorf("K: got %d, want %d", pi.K, k)
	}

	if pi.N != int(totalIDs) {
		t.Errorf("N: got %d, want %d", pi.N, totalIDs)
	}

	for i := range k {
		loaded := pi.Partition(i)
		if loaded == nil {
			t.Fatalf("Partition(%d): nil", i)
		}

		if len(loaded) != len(partitions[i]) {
			t.Errorf("Partition(%d) len: got %d, want %d", i, len(loaded), len(partitions[i]))
			continue
		}

		for j, id := range loaded {
			if id != partitions[i][j] {
				t.Errorf("Partition(%d)[%d]: got %d, want %d", i, j, id, partitions[i][j])
			}
		}
	}
}

func TestPartitionIndex_ZeroCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partitions.bin")

	partitions := [][]uint32{
		{0, 1, 2, 3, 4},
		{5, 6, 7},
		{8, 9, 10, 11},
	}

	if err := SavePartitionIndex(path, partitions); err != nil {
		t.Fatalf("save: %v", err)
	}

	pi, err := LoadPartitionIndex(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer pi.Close()

	p1 := pi.Partition(0)
	p2 := pi.Partition(0)

	if &p1[0] != &p2[0] {
		t.Error("Partition should return same backing slice (zero-copy)")
	}
}

func TestPartitionIndex_PartitionSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partitions.bin")

	partitions := [][]uint32{
		{0, 1, 2, 3, 4},
		{5, 6, 7},
		{8, 9, 10, 11},
		{},
	}

	if err := SavePartitionIndex(path, partitions); err != nil {
		t.Fatalf("save: %v", err)
	}

	pi, err := LoadPartitionIndex(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer pi.Close()

	expectedSizes := []int{5, 3, 4, 0}
	for i, expected := range expectedSizes {
		size := pi.PartitionSize(i)
		if size != expected {
			t.Errorf("PartitionSize(%d): got %d, want %d", i, size, expected)
		}
	}
}

func TestPartitionIndex_ChecksumVerification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partitions.bin")

	partitions := [][]uint32{
		{0, 1, 2, 3, 4},
		{5, 6, 7, 8, 9},
	}

	if err := SavePartitionIndex(path, partitions); err != nil {
		t.Fatalf("save: %v", err)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("open for corruption: %v", err)
	}
	if _, err := f.WriteAt([]byte{0xFF, 0xFF, 0xFF, 0xFF}, partitionHeaderSize+10); err != nil {
		f.Close()
		t.Fatalf("corrupt: %v", err)
	}
	f.Close()

	_, err = LoadPartitionIndex(path)
	if err == nil {
		t.Error("expected checksum error on corrupted file")
	}
}

func TestPartitionIndex_InvalidMagic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partitions.bin")

	partitions := [][]uint32{
		{0, 1, 2},
	}

	if err := SavePartitionIndex(path, partitions); err != nil {
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

	_, err = LoadPartitionIndex(path)
	if err == nil {
		t.Error("expected magic error on corrupted file")
	}
}

func TestPartitionIndex_OutOfBounds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partitions.bin")

	k := 4
	partitions := make([][]uint32, k)
	for i := range k {
		partitions[i] = []uint32{uint32(i)}
	}

	if err := SavePartitionIndex(path, partitions); err != nil {
		t.Fatalf("save: %v", err)
	}

	pi, err := LoadPartitionIndex(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer pi.Close()

	if p := pi.Partition(-1); p != nil {
		t.Error("Partition(-1) should return nil")
	}

	if p := pi.Partition(k); p != nil {
		t.Error("Partition(k) should return nil")
	}

	if s := pi.PartitionSize(-1); s != 0 {
		t.Error("PartitionSize(-1) should return 0")
	}

	if s := pi.PartitionSize(k); s != 0 {
		t.Error("PartitionSize(k) should return 0")
	}
}

func TestPartitionIndex_EmptyPartitions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partitions.bin")

	partitions := [][]uint32{
		{},
		{0, 1, 2},
		{},
		{3, 4},
		{},
	}

	if err := SavePartitionIndex(path, partitions); err != nil {
		t.Fatalf("save: %v", err)
	}

	pi, err := LoadPartitionIndex(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer pi.Close()

	if pi.K != 5 {
		t.Errorf("K: got %d, want 5", pi.K)
	}

	if pi.N != 5 {
		t.Errorf("N: got %d, want 5", pi.N)
	}

	expectedSizes := []int{0, 3, 0, 2, 0}
	for i, expected := range expectedSizes {
		size := pi.PartitionSize(i)
		if size != expected {
			t.Errorf("PartitionSize(%d): got %d, want %d", i, size, expected)
		}

		p := pi.Partition(i)
		if len(p) != expected {
			t.Errorf("len(Partition(%d)): got %d, want %d", i, len(p), expected)
		}
	}
}

func TestPartitionIndex_ValidationErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partitions.bin")

	err := SavePartitionIndex(path, nil)
	if err == nil {
		t.Error("expected error for nil partitions")
	}

	err = SavePartitionIndex(path, [][]uint32{})
	if err == nil {
		t.Error("expected error for empty partitions")
	}
}

func TestPartitionIndex_LargeScale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partitions.bin")

	k := 512
	avgSize := 1000
	partitions := make([][]uint32, k)

	id := uint32(0)
	for i := range k {
		size := avgSize + (i % 100)
		partitions[i] = make([]uint32, size)
		for j := range size {
			partitions[i][j] = id
			id++
		}
	}

	if err := SavePartitionIndex(path, partitions); err != nil {
		t.Fatalf("save: %v", err)
	}

	pi, err := LoadPartitionIndex(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer pi.Close()

	if pi.K != k {
		t.Errorf("K: got %d, want %d", pi.K, k)
	}

	for i := 0; i < k; i += 50 {
		p := pi.Partition(i)
		if p == nil {
			t.Errorf("Partition(%d): nil", i)
			continue
		}

		expectedSize := avgSize + (i % 100)
		if len(p) != expectedSize {
			t.Errorf("Partition(%d) len: got %d, want %d", i, len(p), expectedSize)
		}
	}
}

func BenchmarkPartitionIndex_Load(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "partitions.bin")

	k := 512
	partitions := make([][]uint32, k)
	for i := range k {
		partitions[i] = make([]uint32, 1000)
		for j := range 1000 {
			partitions[i][j] = uint32(i*1000 + j)
		}
	}

	if err := SavePartitionIndex(path, partitions); err != nil {
		b.Fatalf("save: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pi, err := LoadPartitionIndex(path)
		if err != nil {
			b.Fatalf("load: %v", err)
		}
		pi.Close()
	}
}

func BenchmarkPartitionIndex_Partition(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "partitions.bin")

	k := 512
	partitions := make([][]uint32, k)
	for i := range k {
		partitions[i] = make([]uint32, 1000)
		for j := range 1000 {
			partitions[i][j] = uint32(i*1000 + j)
		}
	}

	if err := SavePartitionIndex(path, partitions); err != nil {
		b.Fatalf("save: %v", err)
	}

	pi, err := LoadPartitionIndex(path)
	if err != nil {
		b.Fatalf("load: %v", err)
	}
	defer pi.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := i % k
		_ = pi.Partition(idx)
	}
}
