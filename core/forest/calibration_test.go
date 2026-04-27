package forest

import (
	"context"
	"math"
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #9 Phase B — Calibration tests
// ────────────────────────────────────────────────────────────────────

func TestCalibration_PerfectlyCalibratedHasZeroECE(t *testing.T) {
	// Perfect calibration: in each bin, the predicted_mean == observed_rate.
	// We construct samples where prediction p produces label 1 with prob p
	// (deterministic split). For 10 samples per bin centered at 0.05,
	// 0.15, ..., 0.95, the binner should report ECE near 0.
	rng := newDeterministicRand(13)
	var samples []calibrationSample
	for centerBin := 0; centerBin < 10; centerBin++ {
		center := (float64(centerBin) + 0.5) / 10.0
		// 100 samples per bucket, label distribution matches center.
		for i := 0; i < 100; i++ {
			label := 0.0
			if rng.Float64() < center {
				label = 1.0
			}
			samples = append(samples, calibrationSample{
				prediction: center,
				label:      label,
			})
		}
	}
	snap := buildCalibrationSnapshot("test", 0, time.Now().Add(-time.Hour), samples)
	if snap.ECE > 0.05 {
		t.Errorf("ECE on perfectly-calibrated input should be near 0; got %v", snap.ECE)
	}
	if snap.SampleCount != int64(len(samples)) {
		t.Errorf("sample count: got %d want %d", snap.SampleCount, len(samples))
	}
}

func TestCalibration_OverconfidentModelHasHighECE(t *testing.T) {
	// Model predicts 0.9 for half the samples that have label 0 (very wrong)
	// and 0.9 for half with label 1 (correct). Overall observed rate at
	// prediction=0.9 is 50%, but predicted mean is 90% → gap of 0.4.
	var samples []calibrationSample
	for i := 0; i < 50; i++ {
		samples = append(samples, calibrationSample{prediction: 0.9, label: 0})
		samples = append(samples, calibrationSample{prediction: 0.9, label: 1})
	}
	snap := buildCalibrationSnapshot("test", 0, time.Now().Add(-time.Hour), samples)
	if snap.ECE < 0.3 {
		t.Errorf("overconfident model: ECE got %v want >= 0.3", snap.ECE)
	}
	if snap.MCE < 0.3 {
		t.Errorf("overconfident model: MCE got %v want >= 0.3", snap.MCE)
	}
}

func TestCalibration_BucketIndexClamps(t *testing.T) {
	cases := []struct {
		prediction float64
		n          int
		want       int
	}{
		{0.0, 10, 0},
		{0.05, 10, 0},
		{0.5, 10, 5},
		{0.95, 10, 9},
		{1.0, 10, 9},  // clamps to last bin
		{-0.1, 10, 0}, // clamps to 0
		{1.5, 10, 9},  // clamps to last
		{0.5, 0, 0},   // n<=0 returns 0
	}
	for _, c := range cases {
		got := bucketIndex(c.prediction, c.n)
		if got != c.want {
			t.Errorf("bucketIndex(%v, %d): got %d want %d", c.prediction, c.n, got, c.want)
		}
	}
}

func TestCalibration_AUCMonotoneInputIsOne(t *testing.T) {
	// All positives have higher predictions than all negatives → AUC = 1.
	samples := []calibrationSample{
		{prediction: 0.1, label: 0},
		{prediction: 0.2, label: 0},
		{prediction: 0.3, label: 0},
		{prediction: 0.7, label: 1},
		{prediction: 0.8, label: 1},
		{prediction: 0.9, label: 1},
	}
	got := areaUnderROC(samples)
	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("monotone AUC: got %v want 1.0", got)
	}
}

func TestCalibration_AUCInvertedInputIsZero(t *testing.T) {
	// Inverted ranking: positives have lower predictions than negatives → AUC = 0.
	samples := []calibrationSample{
		{prediction: 0.9, label: 0},
		{prediction: 0.8, label: 0},
		{prediction: 0.7, label: 0},
		{prediction: 0.3, label: 1},
		{prediction: 0.2, label: 1},
		{prediction: 0.1, label: 1},
	}
	got := areaUnderROC(samples)
	if math.Abs(got) > 1e-9 {
		t.Errorf("inverted AUC: got %v want 0", got)
	}
}

func TestCalibration_AUCSingleClassReturnsHalf(t *testing.T) {
	samples := []calibrationSample{
		{prediction: 0.5, label: 1},
		{prediction: 0.6, label: 1},
	}
	if got := areaUnderROC(samples); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("single-class AUC: got %v want 0.5", got)
	}
}

func TestCalibration_AUCEmptyReturnsZero(t *testing.T) {
	if got := areaUnderROC(nil); got != 0 {
		t.Errorf("empty AUC: got %v want 0", got)
	}
}

func TestCalibration_BrierScoreBounds(t *testing.T) {
	// Perfect prediction (matches label exactly) → Brier=0.
	samples := []calibrationSample{
		{prediction: 1, label: 1},
		{prediction: 0, label: 0},
	}
	if got := brierScore(samples); got != 0 {
		t.Errorf("perfect: got %v want 0", got)
	}
	// Worst prediction → Brier=1.
	worst := []calibrationSample{
		{prediction: 1, label: 0},
		{prediction: 0, label: 1},
	}
	if got := brierScore(worst); got != 1 {
		t.Errorf("worst: got %v want 1", got)
	}
	if got := brierScore(nil); got != 0 {
		t.Errorf("empty: got %v want 0", got)
	}
}

func TestCalibration_ECEZeroOnEmpty(t *testing.T) {
	if got := expectedCalibrationError(nil, 0); got != 0 {
		t.Errorf("empty ECE: got %v want 0", got)
	}
	if got := expectedCalibrationError([]ReliabilityBucket{}, 0); got != 0 {
		t.Errorf("empty buckets: got %v want 0", got)
	}
}

// ────────────────────────────────────────────────────────────────────
// Retrieval quality tests
// ────────────────────────────────────────────────────────────────────

func TestRetrievalQuality_PrecisionAtKHandChecks(t *testing.T) {
	cases := []struct {
		name string
		hits []bool
		k    int
		want float64
	}{
		{"all hits at top-3", []bool{true, true, true, false, false}, 3, 1.0},
		{"no hits", []bool{false, false, false, false, false}, 5, 0.0},
		{"one hit at rank 1", []bool{true, false, false, false, false}, 1, 1.0},
		{"one hit at rank 3, K=5", []bool{false, false, true, false, false}, 5, 0.2},
		{"k > len → uses available", []bool{true, false}, 10, 0.5},
		{"empty hits", []bool{}, 5, 0},
		{"k=0", []bool{true, true}, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := precisionAtK(c.hits, c.k)
			if math.Abs(got-c.want) > 1e-9 {
				t.Errorf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestRetrievalQuality_NDCGCorrect(t *testing.T) {
	// Perfect ranking (hit at position 1): NDCG = 1.0
	if got := ndcgAtK([]bool{true, false, false, false, false}, 5); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("perfect: got %v want 1.0", got)
	}
	// No hits: NDCG = 0.
	if got := ndcgAtK([]bool{false, false, false, false, false}, 5); got != 0 {
		t.Errorf("no hits: got %v want 0", got)
	}
	// Hit at position 2 with one total hit: DCG = 1/log2(3) ≈ 0.6309.
	// IDCG = 1/log2(2) = 1. NDCG ≈ 0.63.
	got := ndcgAtK([]bool{false, true, false, false, false}, 5)
	if got < 0.6 || got > 0.7 {
		t.Errorf("rank-2 single hit: got %v want ≈ 0.63", got)
	}
}

func TestRetrievalQuality_GroupRankingsByRetrieval(t *testing.T) {
	rankings := []candidateRanking{
		{retrievalEventID: "r1", branchID: "b1", rankPosition: 1},
		{retrievalEventID: "r1", branchID: "b2", rankPosition: 2},
		{retrievalEventID: "r2", branchID: "b3", rankPosition: 1},
		{retrievalEventID: "r3", branchID: "b4", rankPosition: 1},
	}
	groups := groupRankingsByRetrieval(rankings)
	if len(groups) != 3 {
		t.Fatalf("group count: got %d want 3", len(groups))
	}
	if len(groups[0]) != 2 || groups[0][0].retrievalEventID != "r1" {
		t.Errorf("group 0: got %+v", groups[0])
	}
	if len(groups[1]) != 1 || groups[1][0].retrievalEventID != "r2" {
		t.Errorf("group 1: got %+v", groups[1])
	}
	if len(groups[2]) != 1 || groups[2][0].retrievalEventID != "r3" {
		t.Errorf("group 2: got %+v", groups[2])
	}
}

func TestRetrievalQuality_GroupRankingsEmpty(t *testing.T) {
	if got := groupRankingsByRetrieval(nil); got != nil {
		t.Errorf("nil input should return nil; got %v", got)
	}
}

// ────────────────────────────────────────────────────────────────────
// ModelCalibration cache tests
// ────────────────────────────────────────────────────────────────────

func TestCalibrationCache_LoadEmptyReturnsNotFresh(t *testing.T) {
	c := newCalibrationCache()
	value, version, fresh := c.load(time.Now())
	if fresh {
		t.Error("empty cache: fresh should be false")
	}
	if value != 0 || version != 0 {
		t.Errorf("empty cache: got value=%v version=%d, want 0/0", value, version)
	}
}

func TestCalibrationCache_StoreAndLoadFresh(t *testing.T) {
	c := newCalibrationCache()
	now := time.Now()
	c.store(0.85, 7, now)
	value, version, fresh := c.load(now.Add(time.Minute))
	if !fresh {
		t.Error("fresh value should be reported fresh")
	}
	if value != 0.85 {
		t.Errorf("value: got %v want 0.85", value)
	}
	if version != 7 {
		t.Errorf("version: got %d want 7", version)
	}
}

func TestCalibrationCache_StaleAfterWindow(t *testing.T) {
	c := newCalibrationCache()
	now := time.Now()
	c.store(0.85, 7, now)
	// Past the staleness window — cache reports value but not fresh.
	value, version, fresh := c.load(now.Add(2 * calibrationCacheStaleWindow))
	if fresh {
		t.Error("past stale window: fresh should be false")
	}
	if value != 0.85 {
		t.Errorf("stale value should still be returned; got %v", value)
	}
	if version != 7 {
		t.Errorf("stale version should still be returned; got %d", version)
	}
}

func TestCalibrationCache_NilSafe(t *testing.T) {
	var c *calibrationCache
	c.store(1.0, 1, time.Now()) // must not panic
	value, version, fresh := c.load(time.Now())
	if fresh || value != 0 || version != 0 {
		t.Errorf("nil cache: got value=%v version=%d fresh=%v", value, version, fresh)
	}
}

func TestCalibrationCache_AnnotateHandlesNilPackets(t *testing.T) {
	pkts := []*BranchPacket{
		{Branch: &Branch{ID: "a"}},
		nil,
		{Branch: &Branch{ID: "b"}},
	}
	annotateModelCalibration(pkts, 0.7)
	if pkts[0].Score.ModelCalibration != 0.7 {
		t.Errorf("packet 0: got %v want 0.7", pkts[0].Score.ModelCalibration)
	}
	if pkts[2].Score.ModelCalibration != 0.7 {
		t.Errorf("packet 2: got %v want 0.7", pkts[2].Score.ModelCalibration)
	}
}

func TestCalibration_RetrievalQualityEmptyWindowZero(t *testing.T) {
	forest, _ := newTestForest(t)
	stats, err := forest.RetrievalQualitySince(context.Background(), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if stats.RetrievalCount != 0 {
		t.Errorf("empty window: count got %d want 0", stats.RetrievalCount)
	}
	if stats.MRR != 0 {
		t.Errorf("empty window: MRR got %v want 0", stats.MRR)
	}
}

func TestCalibration_BoosterEmptyWindowZero(t *testing.T) {
	forest, _ := newTestForest(t)
	snap, err := forest.BoosterCalibrationSince(context.Background(), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if snap.SampleCount != 0 {
		t.Errorf("empty window: count got %d want 0", snap.SampleCount)
	}
	if snap.ECE != 0 {
		t.Errorf("empty window: ECE got %v want 0", snap.ECE)
	}
}
