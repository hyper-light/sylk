package ivf

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
	"github.com/viterin/vek/vek32"
)

const (
	testDim           = 128
	testCodeLen       = 8
	testGraphR        = 64
	testShardCapacity = 0
)

func setupConsistentStores(t *testing.T, baseDir string, count int) {
	t.Helper()

	vectorsDir := filepath.Join(baseDir, "vectors")
	graphDir := filepath.Join(baseDir, "graph")
	bbqDir := filepath.Join(baseDir, "bbq")
	normsDir := filepath.Join(baseDir, "norms")

	vectors, err := storage.CreateShardedVectorStore(vectorsDir, testDim, testShardCapacity)
	if err != nil {
		t.Fatalf("create vectors: %v", err)
	}

	graph, err := storage.CreateShardedGraphStore(graphDir, testGraphR, testShardCapacity)
	if err != nil {
		vectors.Close()
		t.Fatalf("create graph: %v", err)
	}

	bbq, err := CreateShardedBBQStore(bbqDir, testCodeLen)
	if err != nil {
		vectors.Close()
		graph.Close()
		t.Fatalf("create bbq: %v", err)
	}

	norms, err := CreateShardedNormStore(normsDir)
	if err != nil {
		vectors.Close()
		graph.Close()
		bbq.Close()
		t.Fatalf("create norms: %v", err)
	}

	for i := range count {
		vec := make([]float32, testDim)
		for j := range testDim {
			vec[j] = float32(i+j) * 0.01
		}
		if _, err := vectors.Append(vec); err != nil {
			t.Fatalf("append vector %d: %v", i, err)
		}

		if err := graph.SetNeighbors(uint32(i), nil); err != nil {
			t.Fatalf("set graph neighbors %d: %v", i, err)
		}

		code := make([]byte, testCodeLen)
		code[0] = byte(i & 0xFF)
		code[1] = byte((i >> 8) & 0xFF)
		if _, err := bbq.Append(code); err != nil {
			t.Fatalf("append bbq %d: %v", i, err)
		}

		norm := math.Sqrt(float64(vek32.Dot(vec, vec)))
		if _, err := norms.Append(norm); err != nil {
			t.Fatalf("append norm %d: %v", i, err)
		}
	}

	vectors.Sync()
	graph.Sync()
	bbq.Sync()
	norms.Sync()
	vectors.Close()
	graph.Close()
	bbq.Close()
	norms.Close()
}

func TestConsistencyChecker_HealthyStores(t *testing.T) {
	baseDir := t.TempDir()
	count := 1000

	setupConsistentStores(t, baseDir, count)

	checker := NewConsistencyChecker(baseDir)
	report, err := checker.Check()
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if !report.IsHealthy() {
		t.Errorf("expected healthy report, got: corrupt=%v, mismatch=%v",
			report.CorruptShards, report.CountMismatch)
	}

	if report.VectorCount != uint64(count) {
		t.Errorf("vector count: got %d, want %d", report.VectorCount, count)
	}
	if report.GraphCount != uint64(count) {
		t.Errorf("graph count: got %d, want %d", report.GraphCount, count)
	}
	if report.BBQCount != uint64(count) {
		t.Errorf("bbq count: got %d, want %d", report.BBQCount, count)
	}
	if report.NormCount != uint64(count) {
		t.Errorf("norm count: got %d, want %d", report.NormCount, count)
	}

	if report.NeedsRepair() {
		t.Error("healthy stores should not need repair")
	}
	if report.NeedsTruncation() {
		t.Error("healthy stores should not need truncation")
	}
}

func TestConsistencyChecker_CountMismatch(t *testing.T) {
	baseDir := t.TempDir()
	count := 1000

	setupConsistentStores(t, baseDir, count)

	bbqDir := filepath.Join(baseDir, "bbq")
	bbq, _, err := OpenShardedBBQStore(bbqDir)
	if err != nil {
		t.Fatalf("open bbq: %v", err)
	}

	for i := range 100 {
		code := make([]byte, testCodeLen)
		code[0] = byte(i)
		if _, err := bbq.Append(code); err != nil {
			bbq.Close()
			t.Fatalf("append extra bbq %d: %v", i, err)
		}
	}
	bbq.Close()

	checker := NewConsistencyChecker(baseDir)
	report, err := checker.Check()
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if report.IsHealthy() {
		t.Error("expected unhealthy report due to count mismatch")
	}

	if !report.CountMismatch {
		t.Error("expected CountMismatch to be true")
	}

	if !report.NeedsTruncation() {
		t.Error("count mismatch should trigger truncation need")
	}

	if report.MinValidCount != uint64(count) {
		t.Errorf("min valid count: got %d, want %d", report.MinValidCount, count)
	}

	if report.BBQCount != uint64(count+100) {
		t.Errorf("bbq count: got %d, want %d", report.BBQCount, count+100)
	}
}

func TestConsistencyChecker_BBQCorruption(t *testing.T) {
	baseDir := t.TempDir()
	count := BBQPerShard + 100

	vectorsDir := filepath.Join(baseDir, "vectors")
	graphDir := filepath.Join(baseDir, "graph")
	bbqDir := filepath.Join(baseDir, "bbq")
	normsDir := filepath.Join(baseDir, "norms")

	vectors, err := storage.CreateShardedVectorStore(vectorsDir, testDim, testShardCapacity)
	if err != nil {
		t.Fatalf("create vectors: %v", err)
	}

	graph, err := storage.CreateShardedGraphStore(graphDir, testGraphR, testShardCapacity)
	if err != nil {
		vectors.Close()
		t.Fatalf("create graph: %v", err)
	}

	bbq, err := CreateShardedBBQStore(bbqDir, testCodeLen)
	if err != nil {
		vectors.Close()
		graph.Close()
		t.Fatalf("create bbq: %v", err)
	}

	norms, err := CreateShardedNormStore(normsDir)
	if err != nil {
		vectors.Close()
		graph.Close()
		bbq.Close()
		t.Fatalf("create norms: %v", err)
	}

	vecs := make([][]float32, count)
	codes := make([][]byte, count)
	normVals := make([]float64, count)

	for i := range count {
		vecs[i] = make([]float32, testDim)
		for j := range testDim {
			vecs[i][j] = float32(i+j) * 0.01
		}
		codes[i] = make([]byte, testCodeLen)
		codes[i][0] = byte(i & 0xFF)
		normVals[i] = math.Sqrt(float64(vek32.Dot(vecs[i], vecs[i])))
	}

	if _, err := vectors.AppendBatch(vecs); err != nil {
		t.Fatalf("append vectors: %v", err)
	}
	for i := range count {
		if err := graph.SetNeighbors(uint32(i), nil); err != nil {
			t.Fatalf("set graph neighbors %d: %v", i, err)
		}
	}
	if _, err := bbq.AppendBatch(codes); err != nil {
		t.Fatalf("append bbq: %v", err)
	}
	if _, err := norms.AppendBatch(normVals); err != nil {
		t.Fatalf("append norms: %v", err)
	}

	vectors.Sync()
	graph.Sync()
	bbq.Sync()
	norms.Sync()
	vectors.Close()
	graph.Close()
	bbq.Close()
	norms.Close()

	shardPath := filepath.Join(bbqDir, "shard_0000.bin")
	f, err := os.OpenFile(shardPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("open shard: %v", err)
	}
	if _, err := f.WriteAt([]byte{0xFF, 0xFF, 0xFF, 0xFF}, bbqHeaderSize+100); err != nil {
		f.Close()
		t.Fatalf("corrupt shard: %v", err)
	}
	f.Close()

	checker := NewConsistencyChecker(baseDir)
	report, err := checker.Check()
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if report.IsHealthy() {
		t.Error("expected unhealthy report due to BBQ corruption")
	}

	if !report.NeedsRepair() {
		t.Error("corrupted BBQ should need repair")
	}

	found := false
	for _, cs := range report.CorruptShards {
		if cs.StoreType == StoreTypeBBQ && cs.ShardIdx == 0 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected BBQ shard 0 in corrupt list, got: %v", report.CorruptShards)
	}
}

func TestConsistencyChecker_NormCorruption(t *testing.T) {
	baseDir := t.TempDir()
	count := NormsPerShard + 100

	vectorsDir := filepath.Join(baseDir, "vectors")
	graphDir := filepath.Join(baseDir, "graph")
	bbqDir := filepath.Join(baseDir, "bbq")
	normsDir := filepath.Join(baseDir, "norms")

	vectors, err := storage.CreateShardedVectorStore(vectorsDir, testDim, testShardCapacity)
	if err != nil {
		t.Fatalf("create vectors: %v", err)
	}

	graph, err := storage.CreateShardedGraphStore(graphDir, testGraphR, testShardCapacity)
	if err != nil {
		vectors.Close()
		t.Fatalf("create graph: %v", err)
	}

	bbq, err := CreateShardedBBQStore(bbqDir, testCodeLen)
	if err != nil {
		vectors.Close()
		graph.Close()
		t.Fatalf("create bbq: %v", err)
	}

	norms, err := CreateShardedNormStore(normsDir)
	if err != nil {
		vectors.Close()
		graph.Close()
		bbq.Close()
		t.Fatalf("create norms: %v", err)
	}

	vecs := make([][]float32, count)
	codes := make([][]byte, count)
	normVals := make([]float64, count)

	for i := range count {
		vecs[i] = make([]float32, testDim)
		for j := range testDim {
			vecs[i][j] = float32(i+j) * 0.01
		}
		codes[i] = make([]byte, testCodeLen)
		codes[i][0] = byte(i & 0xFF)
		normVals[i] = math.Sqrt(float64(vek32.Dot(vecs[i], vecs[i])))
	}

	if _, err := vectors.AppendBatch(vecs); err != nil {
		t.Fatalf("append vectors: %v", err)
	}
	for i := range count {
		if err := graph.SetNeighbors(uint32(i), nil); err != nil {
			t.Fatalf("set graph neighbors %d: %v", i, err)
		}
	}
	if _, err := bbq.AppendBatch(codes); err != nil {
		t.Fatalf("append bbq: %v", err)
	}
	if _, err := norms.AppendBatch(normVals); err != nil {
		t.Fatalf("append norms: %v", err)
	}

	vectors.Sync()
	graph.Sync()
	bbq.Sync()
	norms.Sync()
	vectors.Close()
	graph.Close()
	bbq.Close()
	norms.Close()

	shardPath := filepath.Join(normsDir, "shard_0000.bin")
	f, err := os.OpenFile(shardPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("open shard: %v", err)
	}
	if _, err := f.WriteAt([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, normHeaderSize+100); err != nil {
		f.Close()
		t.Fatalf("corrupt shard: %v", err)
	}
	f.Close()

	checker := NewConsistencyChecker(baseDir)
	report, err := checker.Check()
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if report.IsHealthy() {
		t.Error("expected unhealthy report due to Norm corruption")
	}

	if !report.NeedsRepair() {
		t.Error("corrupted Norms should need repair")
	}

	found := false
	for _, cs := range report.CorruptShards {
		if cs.StoreType == StoreTypeNorms && cs.ShardIdx == 0 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Norm shard 0 in corrupt list, got: %v", report.CorruptShards)
	}
}

func TestRepairFromSQLite_BBQ(t *testing.T) {
	baseDir := t.TempDir()
	count := BBQPerShard + 100

	vectorsDir := filepath.Join(baseDir, "vectors")
	graphDir := filepath.Join(baseDir, "graph")
	bbqDir := filepath.Join(baseDir, "bbq")
	normsDir := filepath.Join(baseDir, "norms")

	vectors, err := storage.CreateShardedVectorStore(vectorsDir, testDim, testShardCapacity)
	if err != nil {
		t.Fatalf("create vectors: %v", err)
	}

	graph, err := storage.CreateShardedGraphStore(graphDir, testGraphR, testShardCapacity)
	if err != nil {
		vectors.Close()
		t.Fatalf("create graph: %v", err)
	}

	bbq, err := CreateShardedBBQStore(bbqDir, testCodeLen)
	if err != nil {
		vectors.Close()
		graph.Close()
		t.Fatalf("create bbq: %v", err)
	}

	norms, err := CreateShardedNormStore(normsDir)
	if err != nil {
		vectors.Close()
		graph.Close()
		bbq.Close()
		t.Fatalf("create norms: %v", err)
	}

	originalVecs := make([][]float32, count)

	bbqEncoder := func(vec []float32) []byte {
		code := make([]byte, testCodeLen)
		for i := range testCodeLen {
			if i < len(vec) {
				code[i] = byte(int(vec[i]*100) & 0xFF)
			}
		}
		return code
	}

	for i := range count {
		originalVecs[i] = make([]float32, testDim)
		for j := range testDim {
			originalVecs[i][j] = float32(i+j) * 0.01
		}
		if _, err := vectors.Append(originalVecs[i]); err != nil {
			t.Fatalf("append vector %d: %v", i, err)
		}
		if err := graph.SetNeighbors(uint32(i), nil); err != nil {
			t.Fatalf("set graph neighbors %d: %v", i, err)
		}

		code := bbqEncoder(originalVecs[i])
		if _, err := bbq.Append(code); err != nil {
			t.Fatalf("append bbq %d: %v", i, err)
		}

		norm := math.Sqrt(float64(vek32.Dot(originalVecs[i], originalVecs[i])))
		if _, err := norms.Append(norm); err != nil {
			t.Fatalf("append norm %d: %v", i, err)
		}
	}

	vectors.Sync()
	graph.Sync()
	bbq.Sync()
	norms.Sync()
	vectors.Close()
	graph.Close()
	bbq.Close()
	norms.Close()

	shardPath := filepath.Join(bbqDir, "shard_0000.bin")
	f, err := os.OpenFile(shardPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("open shard: %v", err)
	}
	if _, err := f.WriteAt([]byte{0xFF, 0xFF, 0xFF, 0xFF}, bbqHeaderSize+100); err != nil {
		f.Close()
		t.Fatalf("corrupt shard: %v", err)
	}
	f.Close()

	checker := NewConsistencyChecker(baseDir)
	report, err := checker.Check()
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if !report.NeedsRepair() {
		t.Fatal("expected repair needed")
	}

	cfg := RepairConfig{
		BBQEncoder: bbqEncoder,
		Dim:        testDim,
	}

	if err := RepairFromSQLite(baseDir, report, nil, cfg); err != nil {
		t.Fatalf("repair: %v", err)
	}

	report2, err := checker.Check()
	if err != nil {
		t.Fatalf("check after repair: %v", err)
	}

	if report2.NeedsRepair() {
		t.Errorf("still needs repair after repair: %v", report2.CorruptShards)
	}

	bbq2, _, err := OpenShardedBBQStore(bbqDir)
	if err != nil {
		t.Fatalf("open bbq after repair: %v", err)
	}
	defer bbq2.Close()

	for i := range 10 {
		code := bbq2.Get(uint32(i))
		if code == nil {
			t.Errorf("get %d: nil after repair", i)
			continue
		}
		expected := bbqEncoder(originalVecs[i])
		for j := range testCodeLen {
			if code[j] != expected[j] {
				t.Errorf("code[%d][%d]: got %d, want %d", i, j, code[j], expected[j])
				break
			}
		}
	}
}

func TestRepairFromSQLite_Norms(t *testing.T) {
	baseDir := t.TempDir()
	count := NormsPerShard + 100

	vectorsDir := filepath.Join(baseDir, "vectors")
	graphDir := filepath.Join(baseDir, "graph")
	bbqDir := filepath.Join(baseDir, "bbq")
	normsDir := filepath.Join(baseDir, "norms")

	vectors, err := storage.CreateShardedVectorStore(vectorsDir, testDim, testShardCapacity)
	if err != nil {
		t.Fatalf("create vectors: %v", err)
	}

	graph, err := storage.CreateShardedGraphStore(graphDir, testGraphR, testShardCapacity)
	if err != nil {
		vectors.Close()
		t.Fatalf("create graph: %v", err)
	}

	bbq, err := CreateShardedBBQStore(bbqDir, testCodeLen)
	if err != nil {
		vectors.Close()
		graph.Close()
		t.Fatalf("create bbq: %v", err)
	}

	norms, err := CreateShardedNormStore(normsDir)
	if err != nil {
		vectors.Close()
		graph.Close()
		bbq.Close()
		t.Fatalf("create norms: %v", err)
	}

	originalVecs := make([][]float32, count)
	expectedNorms := make([]float64, count)

	for i := range count {
		originalVecs[i] = make([]float32, testDim)
		for j := range testDim {
			originalVecs[i][j] = float32(i+j) * 0.01
		}
		if _, err := vectors.Append(originalVecs[i]); err != nil {
			t.Fatalf("append vector %d: %v", i, err)
		}
		if err := graph.SetNeighbors(uint32(i), nil); err != nil {
			t.Fatalf("set graph neighbors %d: %v", i, err)
		}

		code := make([]byte, testCodeLen)
		code[0] = byte(i & 0xFF)
		if _, err := bbq.Append(code); err != nil {
			t.Fatalf("append bbq %d: %v", i, err)
		}

		expectedNorms[i] = math.Sqrt(float64(vek32.Dot(originalVecs[i], originalVecs[i])))
		if _, err := norms.Append(expectedNorms[i]); err != nil {
			t.Fatalf("append norm %d: %v", i, err)
		}
	}

	vectors.Sync()
	graph.Sync()
	bbq.Sync()
	norms.Sync()
	vectors.Close()
	graph.Close()
	bbq.Close()
	norms.Close()

	shardPath := filepath.Join(normsDir, "shard_0000.bin")
	f, err := os.OpenFile(shardPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("open shard: %v", err)
	}
	if _, err := f.WriteAt([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, normHeaderSize+100); err != nil {
		f.Close()
		t.Fatalf("corrupt shard: %v", err)
	}
	f.Close()

	checker := NewConsistencyChecker(baseDir)
	report, err := checker.Check()
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if !report.NeedsRepair() {
		t.Fatal("expected repair needed")
	}

	cfg := RepairConfig{
		Dim: testDim,
	}

	if err := RepairFromSQLite(baseDir, report, nil, cfg); err != nil {
		t.Fatalf("repair: %v", err)
	}

	report2, err := checker.Check()
	if err != nil {
		t.Fatalf("check after repair: %v", err)
	}

	if report2.NeedsRepair() {
		t.Errorf("still needs repair after repair: %v", report2.CorruptShards)
	}

	norms2, _, err := OpenShardedNormStore(normsDir)
	if err != nil {
		t.Fatalf("open norms after repair: %v", err)
	}
	defer norms2.Close()

	for i := range 10 {
		norm := norms2.Get(uint32(i))
		if math.Abs(norm-expectedNorms[i]) > 1e-10 {
			t.Errorf("norm[%d]: got %v, want %v", i, norm, expectedNorms[i])
		}
	}
}

func TestQuickHealthCheck(t *testing.T) {
	baseDir := t.TempDir()
	count := 100

	setupConsistentStores(t, baseDir, count)

	healthy, err := QuickHealthCheck(baseDir)
	if err != nil {
		t.Fatalf("quick check: %v", err)
	}

	if !healthy {
		t.Error("expected healthy result for consistent stores")
	}
}

func TestConsistencyChecker_CheckBBQOnly(t *testing.T) {
	baseDir := t.TempDir()
	count := BBQPerShard + 100

	bbqDir := filepath.Join(baseDir, "bbq")
	bbq, err := CreateShardedBBQStore(bbqDir, testCodeLen)
	if err != nil {
		t.Fatalf("create bbq: %v", err)
	}

	codes := make([][]byte, count)
	for i := range count {
		codes[i] = make([]byte, testCodeLen)
		codes[i][0] = byte(i & 0xFF)
	}

	if _, err := bbq.AppendBatch(codes); err != nil {
		t.Fatalf("append batch: %v", err)
	}
	bbq.Close()

	shardPath := filepath.Join(bbqDir, "shard_0000.bin")
	f, err := os.OpenFile(shardPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("open shard: %v", err)
	}
	if _, err := f.WriteAt([]byte{0xFF, 0xFF, 0xFF, 0xFF}, bbqHeaderSize+100); err != nil {
		f.Close()
		t.Fatalf("corrupt shard: %v", err)
	}
	f.Close()

	checker := NewConsistencyChecker(baseDir)
	corrupted, err := checker.CheckBBQOnly()
	if err != nil {
		t.Fatalf("check bbq only: %v", err)
	}

	if len(corrupted) != 1 || corrupted[0] != 0 {
		t.Errorf("expected shard 0 corrupted, got: %v", corrupted)
	}
}

func TestConsistencyChecker_CheckNormsOnly(t *testing.T) {
	baseDir := t.TempDir()
	count := NormsPerShard + 100

	normsDir := filepath.Join(baseDir, "norms")
	norms, err := CreateShardedNormStore(normsDir)
	if err != nil {
		t.Fatalf("create norms: %v", err)
	}

	normVals := make([]float64, count)
	for i := range count {
		normVals[i] = float64(i) * 0.1
	}

	if _, err := norms.AppendBatch(normVals); err != nil {
		t.Fatalf("append batch: %v", err)
	}
	norms.Close()

	shardPath := filepath.Join(normsDir, "shard_0000.bin")
	f, err := os.OpenFile(shardPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("open shard: %v", err)
	}
	if _, err := f.WriteAt([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, normHeaderSize+100); err != nil {
		f.Close()
		t.Fatalf("corrupt shard: %v", err)
	}
	f.Close()

	checker := NewConsistencyChecker(baseDir)
	corrupted, err := checker.CheckNormsOnly()
	if err != nil {
		t.Fatalf("check norms only: %v", err)
	}

	if len(corrupted) != 1 || corrupted[0] != 0 {
		t.Errorf("expected shard 0 corrupted, got: %v", corrupted)
	}
}

func TestStoreType_String(t *testing.T) {
	tests := []struct {
		st   StoreType
		want string
	}{
		{StoreTypeVectors, "vectors"},
		{StoreTypeGraph, "graph"},
		{StoreTypeBBQ, "bbq"},
		{StoreTypeNorms, "norms"},
		{StoreType(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.st.String(); got != tt.want {
			t.Errorf("StoreType(%d).String() = %q, want %q", tt.st, got, tt.want)
		}
	}
}
