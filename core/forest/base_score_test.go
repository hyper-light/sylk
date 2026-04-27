package forest

import (
	"context"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #8 — base score: resolvers, store, serving, training
// ────────────────────────────────────────────────────────────────────

func TestBaseScore_ResolverDefaults(t *testing.T) {
	cases := []struct {
		name string
		got  any
		want any
	}{
		{"train interval default", resolveBaseScoreTrainInterval(0), defaultBaseScoreTrainInterval},
		{"min training default", resolveBaseScoreMinTrainingExamples(0), defaultBaseScoreMinTrainingExamples},
		{"improve threshold default", resolveBaseScoreImprovementThreshold(-0.1), defaultBaseScoreImprovementThreshold},
		{"improve threshold clamp >1", resolveBaseScoreImprovementThreshold(2.0), 1.0},
		{"learning rate default", resolveBaseScoreLearningRate(0), defaultBaseScoreLearningRate},
		{"learning rate clamp >1", resolveBaseScoreLearningRate(5), 1.0},
		{"l1 reg default", resolveBaseScoreL1Reg(-1), defaultBaseScoreL1Reg},
		{"l2 reg default", resolveBaseScoreL2Reg(-1), defaultBaseScoreL2Reg},
		{"epochs default", resolveBaseScoreEpochs(0), defaultBaseScoreEpochs},
		{"train batch default", resolveBaseScoreTrainBatch(0), defaultBaseScoreTrainBatch},
		{"ab rate default", resolveBaseScoreABRate(-1), defaultBaseScoreABRate},
		{"ab rate clamp >1", resolveBaseScoreABRate(2), 1.0},
		{"max weight default", resolveBaseScoreMaxWeight(0), defaultBaseScoreMaxWeight},
		{"pruning threshold default", resolveBaseScorePruningThreshold(-1), defaultBaseScorePruningThreshold},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %v want %v", c.got, c.want)
			}
		})
	}
}

func TestBaseScore_StoreRoundTrip(t *testing.T) {
	forest, _ := newTestForest(t)
	ctx := context.Background()

	model := &BaseScoreModel{
		ID:           "test-model-1",
		Version:      1,
		Weights:      [BaseScoreComponentCount]float64{0.16, 0.11, 0.07, 0.08, 0.05, 0.08, 0.07, 0.10, 0.10, 0.07, 0.05, 0.03, 0.03},
		Bias:         0.05,
		L1Norm:       1.0,
		Accuracy:     0.78,
		TrainingSize: 1500,
		TrainedAt:    time.Now().UTC(),
		Role:         BaseScoreRoleChampion,
	}
	if err := forest.baseScoreStore.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	loaded, err := forest.baseScoreStore.LoadByVersion(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("LoadByVersion returned nil")
	}
	if loaded.ID != model.ID {
		t.Errorf("id: got %s want %s", loaded.ID, model.ID)
	}
	if loaded.Weights != model.Weights {
		t.Errorf("weights: got %v want %v", loaded.Weights, model.Weights)
	}
	if loaded.Role != BaseScoreRoleChampion {
		t.Errorf("role: got %s want champion", loaded.Role)
	}
}

func TestBaseScore_StorePromotionAtomic(t *testing.T) {
	forest, _ := newTestForest(t)
	ctx := context.Background()

	insert := func(id string, version int64, role BaseScoreRole) {
		t.Helper()
		err := forest.baseScoreStore.Save(ctx, &BaseScoreModel{
			ID:        id,
			Version:   version,
			TrainedAt: time.Now().UTC(),
			Role:      role,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("old-champion", 1, BaseScoreRoleChampion)
	insert("new-candidate", 2, "")

	if err := forest.baseScoreStore.PromoteChampion(ctx, "new-candidate"); err != nil {
		t.Fatal(err)
	}
	champion, _, err := forest.baseScoreStore.LoadActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if champion == nil || champion.ID != "new-candidate" {
		t.Errorf("expected new-candidate as champion; got %+v", champion)
	}

	// The old champion should now have empty role.
	old, err := forest.baseScoreStore.LoadByVersion(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if old.Role != "" {
		t.Errorf("old champion role: got %s want empty", old.Role)
	}
}

func TestBaseScore_StoreInvalidWeightsRejected(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()

	// Bypass the store API to insert a row with malformed weights.
	if _, err := db.Exec(`
		INSERT INTO forest_base_score_models
		(id, version, weights_blob, bias, l1_norm, accuracy, training_size, trained_at, role)
		VALUES ('bad', 99, '[1,2,3]', 0, 0, 0, 0, ?, '')
	`, time.Now().UTC().Unix()); err != nil {
		t.Fatal(err)
	}
	loaded, err := forest.baseScoreStore.LoadByVersion(ctx, 99)
	if err == nil {
		t.Fatalf("expected error on malformed weights; got loaded=%v", loaded)
	}
}

func TestBaseScore_CacheChampionChallenger(t *testing.T) {
	cache := newBaseScoreCache()
	if cache.Champion() != nil {
		t.Error("empty cache should return nil champion")
	}
	champion := &BaseScoreModel{ID: "c1", Version: 1}
	cache.SetChampion(champion)
	if got := cache.Champion(); got != champion {
		t.Errorf("set/get champion mismatch: got %v", got)
	}
	cache.SetChampion(nil)
	if cache.Champion() != nil {
		t.Error("nil set should clear")
	}
}

func TestBaseScore_VariantResolutionColdStart(t *testing.T) {
	forest, _ := newTestForestWithConfig(t, Config{})
	variant, model := forest.resolveRetrieveBaseScoreVariant()
	if variant != BaseScoreVariantHardcoded {
		t.Errorf("cold start: got %s want hardcoded", variant)
	}
	if model != nil {
		t.Errorf("cold start: model should be nil; got %v", model)
	}
}

func TestBaseScore_VariantResolutionABSampling(t *testing.T) {
	forest, _ := newTestForestWithConfig(t, Config{
		BaseScoreABRate: 1.0,
		BaseScoreSeed:   42,
	})
	champion := &BaseScoreModel{ID: "champ", Version: 1, Role: BaseScoreRoleChampion}
	challenger := &BaseScoreModel{ID: "chal", Version: 2, Role: BaseScoreRoleChallenger}
	forest.baseScoreCache.SetChampion(champion)
	forest.baseScoreCache.SetChallenger(challenger)

	for i := 0; i < 20; i++ {
		variant, model := forest.resolveRetrieveBaseScoreVariant()
		if variant != BaseScoreVariantChallenger {
			t.Errorf("AB rate=1.0 iter %d: got %s, want challenger", i, variant)
		}
		if model != challenger {
			t.Errorf("iter %d: got %v want challenger model", i, model)
		}
	}
}

func TestBaseScore_VariantResolutionDominantWithoutChallenger(t *testing.T) {
	forest, _ := newTestForestWithConfig(t, Config{
		BaseScoreABRate: 0.5,
		BaseScoreSeed:   1,
	})
	champion := &BaseScoreModel{ID: "champ", Version: 1, Role: BaseScoreRoleChampion}
	forest.baseScoreCache.SetChampion(champion)
	// No challenger set; resolver should always pick champion.
	for i := 0; i < 20; i++ {
		variant, _ := forest.resolveRetrieveBaseScoreVariant()
		if variant != BaseScoreVariantChampion {
			t.Errorf("no challenger iter %d: got %s want champion", i, variant)
		}
	}
}

func TestBaseScore_WeightsForModelHardcodedFallback(t *testing.T) {
	weights, bias := baseScoreWeightsForModel(nil)
	if len(weights) != len(defaultScoreWeights) {
		t.Errorf("nil model: weights len got %d want %d", len(weights), len(defaultScoreWeights))
	}
	if bias != 0 {
		t.Errorf("nil model: bias got %v want 0", bias)
	}
	for i, want := range defaultScoreWeights {
		if weights[i] != want {
			t.Errorf("weights[%d]: got %v want %v", i, weights[i], want)
		}
	}
}

func TestBaseScore_SGDRecoversGroundTruth(t *testing.T) {
	// Generate a "ground truth" linear model and produce labels by
	// thresholding sigmoid(w·x + b). SGD should recover weights
	// close to the truth (up to scaling) and produce high training
	// accuracy. This tests that SGD is actually learning, not just
	// that some specific test case happens to pass.
	rng := newDeterministicRand(7)
	trueWeights := [BaseScoreComponentCount]float64{
		2.0, -1.5, 0, 0, 0.8, 0, 0, -0.5, 0, 0, 1.2, 0, 0,
	}
	trueBias := -0.3

	const total = 800
	examples := make([]baseScoreTrainingExample, 0, total)
	for i := 0; i < total; i++ {
		var c [BaseScoreComponentCount]float64
		for j := range c {
			c[j] = rng.Float64()
		}
		var dot float64
		for j := range c {
			dot += trueWeights[j] * c[j]
		}
		prob := baseScoreSigmoid(dot + trueBias)
		label := 0.0
		if prob > 0.5 {
			label = 1.0
		}
		examples = append(examples, baseScoreTrainingExample{
			components: c,
			label:      label,
			weight:     1.0,
		})
	}

	var initWeights [BaseScoreComponentCount]float64
	trained := trainBaseScoreSGD(examples, initWeights, 0, 100, 0.1, 0, 0.0001, 5.0)
	accuracy := evaluateBaseScoreAccuracy(examples, trained.weights, trained.bias)
	t.Logf("trained accuracy=%v weights=%v bias=%v",
		accuracy, trained.weights, trained.bias)
	if accuracy < 0.92 {
		t.Errorf("accuracy: got %v want >= 0.92", accuracy)
	}
	// Sign correctness: weights with non-zero ground truth should
	// have matching sign in the trained model.
	for j := range trueWeights {
		if trueWeights[j] == 0 {
			continue
		}
		if signFloat64(trueWeights[j]) != signFloat64(trained.weights[j]) {
			t.Errorf("weights[%d] sign mismatch: true=%v trained=%v",
				j, trueWeights[j], trained.weights[j])
		}
	}
}

// newDeterministicRand returns a math/rand.Rand seeded for repeatable
// tests. Wrapped here so SGD tests don't pull in the project's
// crypto/rand-backed exploration RNG.
func newDeterministicRand(seed int64) interface {
	Float64() float64
} {
	return &deterministicRand{state: uint64(seed) | 1}
}

type deterministicRand struct {
	state uint64
}

// Float64 produces a uniform [0,1) sample via splitmix64 — fast,
// deterministic, no external dependencies. Test-only.
func (d *deterministicRand) Float64() float64 {
	d.state += 0x9e3779b97f4a7c15
	z := d.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	z = z ^ (z >> 31)
	// Keep 53 bits and divide by 2^53 to land in [0,1).
	const denom = float64(1 << 53)
	return float64(z>>11) / denom
}

func TestBaseScore_SGDClampsWeights(t *testing.T) {
	examples := []baseScoreTrainingExample{
		{components: [BaseScoreComponentCount]float64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, label: 1, weight: 1},
		{components: [BaseScoreComponentCount]float64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, label: 1, weight: 1},
		{components: [BaseScoreComponentCount]float64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, label: 1, weight: 1},
	}
	var initWeights [BaseScoreComponentCount]float64
	maxWeight := 2.0
	trained := trainBaseScoreSGD(examples, initWeights, 0, 1000, 1.0, 0, 0, maxWeight)
	for i, w := range trained.weights {
		if w > maxWeight || w < -maxWeight {
			t.Errorf("weights[%d] = %v exceeds max %v", i, w, maxWeight)
		}
	}
}

func TestBaseScore_SGDEmptyExamplesNoOp(t *testing.T) {
	var initWeights [BaseScoreComponentCount]float64
	for i := range initWeights {
		initWeights[i] = float64(i) * 0.1
	}
	trained := trainBaseScoreSGD(nil, initWeights, 0.5, 10, 0.01, 0, 0, 5.0)
	if trained.weights != initWeights {
		t.Errorf("empty examples should preserve init weights; got %v", trained.weights)
	}
}

func TestBaseScore_SigmoidExtremes(t *testing.T) {
	cases := []struct {
		z, want float64
	}{
		{0, 0.5},
		{-1000, 0},
		{1000, 1},
	}
	for _, c := range cases {
		got := baseScoreSigmoid(c.z)
		if math.Abs(got-c.want) > 1e-3 {
			t.Errorf("sigmoid(%v) = %v want ~%v", c.z, got, c.want)
		}
	}
}

func TestBaseScore_DecodeFirstNFloat32sBoundsCheck(t *testing.T) {
	if _, ok := decodeFirstNFloat32sToFloat64(nil, 13); ok {
		t.Error("nil blob should not decode successfully")
	}
	if _, ok := decodeFirstNFloat32sToFloat64([]byte{1, 2, 3}, 13); ok {
		t.Error("short blob should not decode successfully")
	}
	// Pack 13 known floats and verify round-trip.
	blob := make([]byte, 13*4)
	values := make([]float32, 13)
	for i := range values {
		values[i] = float32(i) * 0.1
		binary.LittleEndian.PutUint32(blob[i*4:], math.Float32bits(values[i]))
	}
	got, ok := decodeFirstNFloat32sToFloat64(blob, 13)
	if !ok {
		t.Fatal("valid blob should decode")
	}
	for i, want := range values {
		if math.Abs(got[i]-float64(want)) > 1e-6 {
			t.Errorf("[%d] got %v want %v", i, got[i], want)
		}
	}
}

func TestBaseScore_EncodeDecodeWeightsRoundTrip(t *testing.T) {
	original := [BaseScoreComponentCount]float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0, 1.1, 1.2, 1.3}
	encoded, err := encodeBaseScoreWeights(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeBaseScoreWeights(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != original {
		t.Errorf("round-trip mismatch: got %v want %v", decoded, original)
	}
}

func TestBaseScore_DecodeWeightsLengthMismatch(t *testing.T) {
	if _, err := decodeBaseScoreWeights("[1,2,3]"); err == nil {
		t.Error("3-element payload should reject")
	}
	if _, err := decodeBaseScoreWeights(""); err == nil {
		t.Error("empty payload should reject")
	}
	if _, err := decodeBaseScoreWeights("not-json"); err == nil {
		t.Error("malformed json should reject")
	}
}

func TestBaseScore_ScoreBatchUsesPassedWeights(t *testing.T) {
	inputs := []scoreInput{
		{QueryMatch: 1.0, Evidence: 0.0, Canopy: 0.0, Substrate: 0.0, Frontier: 0.0,
			Confidence: 0.0, Recency: 0.0, Warmth: 0.0, Utility: 0.0, Salience: 0.0,
			ConflictSafety: 0.0, ScopeSafety: 0.0, InhibitionSafety: 0.0},
	}
	// All-zero weights except [0]=1.0; bias=0.5. Expect base = 1.0 * 1.0 + 0.5 = 1.5.
	weights := make([]float32, 13)
	weights[0] = 1.0
	scores := scoreBatch(inputs, time.Now().UTC(), nil, weights, 0.5)
	if len(scores) != 1 {
		t.Fatalf("got %d scores want 1", len(scores))
	}
	got := scores[0].Base
	if math.Abs(got-1.5) > 1e-6 {
		t.Errorf("base: got %v want 1.5", got)
	}
}

func TestBaseScore_ScoreBatchFallsBackOnLengthMismatch(t *testing.T) {
	inputs := []scoreInput{
		{QueryMatch: 1.0},
	}
	// Wrong-length weights → falls back to defaults.
	scores := scoreBatch(inputs, time.Now().UTC(), nil, []float32{1.0, 2.0}, 100.0)
	if len(scores) != 1 {
		t.Fatalf("got %d scores want 1", len(scores))
	}
	// With defaults (no bias since length mismatch zeros it), the
	// base should match defaultScoreWeights[0] * 1.0 = 0.16.
	want := float64(defaultScoreWeights[0])
	if math.Abs(scores[0].Base-want) > 1e-6 {
		t.Errorf("fallback base: got %v want %v", scores[0].Base, want)
	}
}

func TestBaseScore_DiagnosticsModelInfoColdStart(t *testing.T) {
	forest, _ := newTestForestWithConfig(t, Config{})
	info := forest.BaseScoreModelInfo()
	if !info.UsingHardcodedDefaults {
		t.Error("cold start: should report hardcoded defaults")
	}
	if info.Champion != nil {
		t.Errorf("cold start: champion should be nil; got %v", info.Champion)
	}
}

func TestBaseScore_DiagnosticsModelInfoWithChampion(t *testing.T) {
	forest, _ := newTestForestWithConfig(t, Config{})
	champion := &BaseScoreModel{ID: "c1", Version: 5}
	forest.baseScoreCache.SetChampion(champion)

	info := forest.BaseScoreModelInfo()
	if info.UsingHardcodedDefaults {
		t.Error("with champion: should not report hardcoded")
	}
	if info.Champion == nil || info.Champion.ID != "c1" {
		t.Errorf("expected champion c1; got %+v", info.Champion)
	}
}

func TestBaseScore_DiagnosticsModelInfoNilForest(t *testing.T) {
	var f *MemoryForest
	info := f.BaseScoreModelInfo()
	if !info.UsingHardcodedDefaults {
		t.Error("nil forest: should report hardcoded defaults")
	}
}

func TestBaseScore_ComponentStatsAccumulatorWelford(t *testing.T) {
	a := newComponentContributionAccumulator()
	weights := [BaseScoreComponentCount]float64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	// Simulate three retrievals each with one candidate of varying QueryMatch.
	values := []float64{0.2, 0.4, 0.6}
	for _, v := range values {
		a.foldOne(0, weights[0]*v)
	}
	out := a.snapshot(weights, 0.005)
	if out[0].SampleCount != 3 {
		t.Errorf("count: got %d want 3", out[0].SampleCount)
	}
	if math.Abs(out[0].MeanContribution-0.4) > 1e-9 {
		t.Errorf("mean: got %v want 0.4", out[0].MeanContribution)
	}
	// stddev of [0.2, 0.4, 0.6] ≈ 0.2 (sample sd with n-1)
	if math.Abs(out[0].StddevContribution-0.2) > 1e-6 {
		t.Errorf("stddev: got %v want ~0.2", out[0].StddevContribution)
	}
}

func TestBaseScore_ComponentStatsPruningCandidate(t *testing.T) {
	a := newComponentContributionAccumulator()
	a.foldOne(0, 0.001)
	weights := [BaseScoreComponentCount]float64{0.001, 0.5, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	out := a.snapshot(weights, 0.01)
	if !out[0].PruningCandidate {
		t.Errorf("weight 0.001 below threshold 0.01 should be pruning candidate")
	}
	if out[1].PruningCandidate {
		t.Errorf("weight 0.5 should NOT be pruning candidate")
	}
}

// TestBaseScore_AuditCapturesVariant exercises the end-to-end path:
// Retrieve writes the variant + version into the audit row.
func TestBaseScore_AuditCapturesVariant(t *testing.T) {
	forest, _ := newAsyncTestForestWithConfig(t, Config{})
	ctx := context.Background()

	event := &Event{
		SessionID: "sess-bs",
		BranchID:  "branch-x",
		AgentID:   "engineer", AgentType: "engineer",
		EventType: EventTypeDecisionRecorded,
		Family:    TreeFamilyDecision,
		Title:     "x",
	}
	if err := forest.AppendEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := forest.WaitForBranchSeq(ctx, event.Seq, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := forest.Retrieve(ctx, Query{
		SessionID: "sess-bs",
		Query:     "x",
		Limit:     1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := forest.WaitForRetrievalAuditDrain(ctx, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	results, err := forest.QueryRetrievalAudits(ctx, RetrievalAuditFilter{SessionID: "sess-bs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 audit; got %d", len(results))
	}
	// Cold-start: no model, expect Hardcoded variant + version 0.
	if results[0].BaseScoreVariant != BaseScoreVariantHardcoded {
		t.Errorf("variant: got %s want hardcoded", results[0].BaseScoreVariant)
	}
	if results[0].BaseScoreVersion != 0 {
		t.Errorf("version: got %d want 0", results[0].BaseScoreVersion)
	}
}
