package forest

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ────────────────────────────────────────────────────────────────────
// HyperParameter tuner / store / fitter — coverage for the four
// integration layers: persistence, workload-init fits, runtime
// adaptation, and config-override priority.
// ────────────────────────────────────────────────────────────────────

// newRawTunerDB returns a bare *sql.DB with only the
// forest_hyperparameters schema applied. Avoids the full forest
// schema migration so the tuner can be exercised against a focused
// fixture.
func newRawTunerDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", stableID("hp-tuner-test", t.Name()))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestHyperParameterStore_RoundTrip verifies Save → Load preserves
// every primitive + provenance + snapshot id + samples. Catches
// JSON-tag drift, time-zone bugs, and nanosecond truncation.
func TestHyperParameterStore_RoundTrip(t *testing.T) {
	t.Parallel()
	db := newRawTunerDB(t)
	store := NewHyperParameterStore(db)
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	src := PlaceholderHyperParameters()
	src.SubstrateDebounce = 731 * time.Millisecond
	src.TrainingDebounce = 4 * src.SubstrateDebounce
	src.ReplayDelay = 17 * time.Second
	src.CounterfactualWeight = 0.42
	src.CounterfactualWindow = 13 * time.Hour
	src.ImplicitNegativeWeight = 0.27
	src.ImplicitNegativeHorizon = 47 * time.Minute
	src.ExplorationRate = 0.07
	src.ExplorationLabelWeight = 1.9
	src.DiversityLambda = 0.55
	src.RetrievalCooldownPenalty = 0.21
	src.RetrievalCooldownWindow = 19
	src.BaseScoreLearningRate = 0.0123
	src.BaseScoreL1Reg = 0.0024
	src.BaseScoreL2Reg = 0.0345
	src.BaseScoreEpochs = 11
	src.BaseScoreImprovementThreshold = 0.018
	src.BaseScoreMinTrainingExamples = 1234
	src.BaseScoreMaxWeight = 6.5
	src.BaseScorePruningThreshold = 0.0078
	src.SnapshotID = 99
	src.SamplesObservedAtFit = 4321
	src.UpdatedAt = time.Date(2026, 1, 2, 3, 4, 5, 678_910_000, time.UTC)
	src.Provenance["substrate_debounce"] = SourceWorkloadInit
	src.Provenance["counterfactual_weight"] = SourceConfig
	src.Provenance["exploration_rate"] = SourceRuntimeAdapt

	if err := store.Save(context.Background(), src); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got == nil {
		t.Fatal("load returned nil snapshot")
	}

	if got.SubstrateDebounce != src.SubstrateDebounce {
		t.Errorf("SubstrateDebounce: got %v want %v", got.SubstrateDebounce, src.SubstrateDebounce)
	}
	if got.TrainingDebounce != src.TrainingDebounce {
		t.Errorf("TrainingDebounce: got %v want %v", got.TrainingDebounce, src.TrainingDebounce)
	}
	if got.ReplayDelay != src.ReplayDelay {
		t.Errorf("ReplayDelay: got %v want %v", got.ReplayDelay, src.ReplayDelay)
	}
	if got.CounterfactualWeight != src.CounterfactualWeight {
		t.Errorf("CounterfactualWeight: got %v want %v", got.CounterfactualWeight, src.CounterfactualWeight)
	}
	if got.CounterfactualWindow != src.CounterfactualWindow {
		t.Errorf("CounterfactualWindow: got %v want %v", got.CounterfactualWindow, src.CounterfactualWindow)
	}
	if got.ImplicitNegativeWeight != src.ImplicitNegativeWeight {
		t.Errorf("ImplicitNegativeWeight: got %v want %v", got.ImplicitNegativeWeight, src.ImplicitNegativeWeight)
	}
	if got.ImplicitNegativeHorizon != src.ImplicitNegativeHorizon {
		t.Errorf("ImplicitNegativeHorizon: got %v want %v", got.ImplicitNegativeHorizon, src.ImplicitNegativeHorizon)
	}
	if got.ExplorationRate != src.ExplorationRate {
		t.Errorf("ExplorationRate: got %v want %v", got.ExplorationRate, src.ExplorationRate)
	}
	if got.ExplorationLabelWeight != src.ExplorationLabelWeight {
		t.Errorf("ExplorationLabelWeight: got %v want %v", got.ExplorationLabelWeight, src.ExplorationLabelWeight)
	}
	if got.DiversityLambda != src.DiversityLambda {
		t.Errorf("DiversityLambda: got %v want %v", got.DiversityLambda, src.DiversityLambda)
	}
	if got.RetrievalCooldownPenalty != src.RetrievalCooldownPenalty {
		t.Errorf("RetrievalCooldownPenalty: got %v want %v", got.RetrievalCooldownPenalty, src.RetrievalCooldownPenalty)
	}
	if got.RetrievalCooldownWindow != src.RetrievalCooldownWindow {
		t.Errorf("RetrievalCooldownWindow: got %v want %v", got.RetrievalCooldownWindow, src.RetrievalCooldownWindow)
	}
	if got.BaseScoreLearningRate != src.BaseScoreLearningRate {
		t.Errorf("BaseScoreLearningRate: got %v want %v", got.BaseScoreLearningRate, src.BaseScoreLearningRate)
	}
	if got.BaseScoreL1Reg != src.BaseScoreL1Reg {
		t.Errorf("BaseScoreL1Reg: got %v want %v", got.BaseScoreL1Reg, src.BaseScoreL1Reg)
	}
	if got.BaseScoreL2Reg != src.BaseScoreL2Reg {
		t.Errorf("BaseScoreL2Reg: got %v want %v", got.BaseScoreL2Reg, src.BaseScoreL2Reg)
	}
	if got.BaseScoreEpochs != src.BaseScoreEpochs {
		t.Errorf("BaseScoreEpochs: got %v want %v", got.BaseScoreEpochs, src.BaseScoreEpochs)
	}
	if got.BaseScoreImprovementThreshold != src.BaseScoreImprovementThreshold {
		t.Errorf("BaseScoreImprovementThreshold: got %v want %v", got.BaseScoreImprovementThreshold, src.BaseScoreImprovementThreshold)
	}
	if got.BaseScoreMinTrainingExamples != src.BaseScoreMinTrainingExamples {
		t.Errorf("BaseScoreMinTrainingExamples: got %v want %v", got.BaseScoreMinTrainingExamples, src.BaseScoreMinTrainingExamples)
	}
	if got.BaseScoreMaxWeight != src.BaseScoreMaxWeight {
		t.Errorf("BaseScoreMaxWeight: got %v want %v", got.BaseScoreMaxWeight, src.BaseScoreMaxWeight)
	}
	if got.BaseScorePruningThreshold != src.BaseScorePruningThreshold {
		t.Errorf("BaseScorePruningThreshold: got %v want %v", got.BaseScorePruningThreshold, src.BaseScorePruningThreshold)
	}
	if got.SnapshotID != src.SnapshotID {
		t.Errorf("SnapshotID: got %v want %v", got.SnapshotID, src.SnapshotID)
	}
	if got.SamplesObservedAtFit != src.SamplesObservedAtFit {
		t.Errorf("SamplesObservedAtFit: got %v want %v", got.SamplesObservedAtFit, src.SamplesObservedAtFit)
	}
	if !got.UpdatedAt.Equal(src.UpdatedAt) {
		t.Errorf("UpdatedAt: got %v want %v", got.UpdatedAt, src.UpdatedAt)
	}
	for _, field := range allHyperParameterFields {
		if got.Provenance[field] != src.Provenance[field] {
			t.Errorf("Provenance[%s]: got %q want %q", field, got.Provenance[field], src.Provenance[field])
		}
	}
}

// TestHyperParameterFitter_FitFromLedger_SubstrateDebounce verifies
// that synthetic intra-burst event timestamps produce a non-
// placeholder SubstrateDebounce value.
func TestHyperParameterFitter_FitFromLedger_SubstrateDebounce(t *testing.T) {
	t.Parallel()
	db := newRawTunerDB(t)

	if err := ensurePhase123Schema(db); err != nil {
		t.Fatalf("ensure phase 1-3 schema: %v", err)
	}

	// Two bursts of intra-burst gaps: one with median ~150ms, another
	// with ~250ms. Plus one large between-burst jump that must be
	// excluded by the substrateBurstWindow filter (>5s).
	const burstSize = 700
	now := time.Now().UTC()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	stmt, err := tx.Prepare(`
		INSERT INTO forest_ledger
			(id, source_kind, source_id, source_key, event_kind, session_id,
			 subject_type, subject_id, occurred_at, inserted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		t.Fatalf("prepare ledger: %v", err)
	}
	defer stmt.Close()
	payloadStmt, err := tx.Prepare(`
		INSERT INTO forest_ledger_payloads (ledger_id, payload_hash, payload, inserted_at)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		t.Fatalf("prepare payload: %v", err)
	}
	defer payloadStmt.Close()
	insertBurst := func(start time.Time, gap time.Duration, n int, prefix string) {
		ts := start
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("%s-%d", prefix, i)
			event := Event{ID: id, SessionID: "session-fit", BranchID: id, EventType: EventTypeDecisionRecorded, Timestamp: ts}
			payload := marshalJSON(event)
			if _, err := stmt.Exec(
				"ledger-"+id,
				string(LedgerSourceForestEvent),
				id,
				"forest_event:"+id,
				string(EventTypeDecisionRecorded),
				"session-fit",
				"branch",
				id,
				ts.Unix(),
				ts.Unix(),
			); err != nil {
				t.Fatalf("insert ledger: %v", err)
			}
			if _, err := payloadStmt.Exec("ledger-"+id, stableID("payload", id), payload, ts.Unix()); err != nil {
				t.Fatalf("insert payload: %v", err)
			}
			ts = ts.Add(gap)
		}
	}
	insertBurst(now, 150*time.Millisecond, burstSize, "burst-a")
	insertBurst(now.Add(2*time.Hour), 250*time.Millisecond, burstSize, "burst-b")
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	fitter := NewHyperParameterFitter(db)
	hp, anyFitted, err := fitter.FitFromLedger(context.Background(), nil)
	if err != nil {
		t.Fatalf("fit: %v", err)
	}
	if !anyFitted {
		t.Fatal("expected at least one fitted field")
	}
	if hp.Provenance["substrate_debounce"] != SourceWorkloadInit {
		t.Fatalf("substrate_debounce provenance: got %q want %q",
			hp.Provenance["substrate_debounce"], SourceWorkloadInit)
	}
	if hp.SubstrateDebounce < 100*time.Millisecond || hp.SubstrateDebounce > 350*time.Millisecond {
		t.Errorf("SubstrateDebounce out of expected band: %v", hp.SubstrateDebounce)
	}
	// TrainingDebounce should derive 4× SubstrateDebounce when the
	// latter was workload-fitted.
	if hp.TrainingDebounce != 4*hp.SubstrateDebounce {
		t.Errorf("TrainingDebounce derivation: got %v want %v",
			hp.TrainingDebounce, 4*hp.SubstrateDebounce)
	}
}

// TestHyperParameterTuner_OverridePriority verifies that an explicit
// override wins over runtime promotion: a runtime trial that
// would normally promote a different value cannot overwrite a
// SourceConfig field.
func TestHyperParameterTuner_OverridePriority(t *testing.T) {
	t.Parallel()
	db := newRawTunerDB(t)

	logger := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	ctx := context.Background()
	tuner, err := NewHyperParameterTuner(ctx, db, logger)
	if err != nil {
		t.Fatalf("new tuner: %v", err)
	}
	t.Cleanup(tuner.Stop)

	if err := tuner.SetOverride(ctx, "exploration_rate", 0.42); err != nil {
		t.Fatalf("set override: %v", err)
	}
	hp := tuner.Get()
	if hp.ExplorationRate != 0.42 {
		t.Errorf("override not visible on Get: got %v want 0.42", hp.ExplorationRate)
	}
	if hp.Provenance["exploration_rate"] != SourceConfig {
		t.Errorf("override provenance: got %q want %q",
			hp.Provenance["exploration_rate"], SourceConfig)
	}
	// Feed adaptation observations after the override. The policy
	// engine may reject retrieval-only evidence, but regardless of
	// trial outcome exploration_rate stays at the operator override.
	for i := 0; i < int(2*minSamplesForAdaptDecision); i++ {
		obs := AdaptationObservation{CalibrationError: 0.6, RegretFrac: 0.3, SampleCount: 1, ObservedAt: time.Now()}
		tuner.ObserveAndAdapt(ctx, obs, false)
	}
	for i := 0; i < int(2*minSamplesForAdaptDecision); i++ {
		obs := AdaptationObservation{CalibrationError: 0.05, RegretFrac: 0.05, SampleCount: 1, ObservedAt: time.Now()}
		tuner.ObserveAndAdapt(ctx, obs, true)
	}

	hp = tuner.Get()
	if hp.ExplorationRate != 0.42 {
		t.Errorf("override clobbered by runtime promotion: got %v want 0.42",
			hp.ExplorationRate)
	}
	if hp.Provenance["exploration_rate"] != SourceConfig {
		t.Errorf("override provenance demoted: got %q want %q",
			hp.Provenance["exploration_rate"], SourceConfig)
	}
}

// TestHyperParameterTuner_RuntimePromotion drives the adaptive loop
// directly: when proposed observations are clearly better than the
// active arm, the policy engine records a promotion proposal. Runtime
// adaptation does not activate without an accepted claim.
func TestHyperParameterTuner_RuntimePromotion(t *testing.T) {
	t.Parallel()
	db := newRawTunerDB(t)

	logger := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	ctx := context.Background()
	tuner, err := NewHyperParameterTuner(ctx, db, logger)
	if err != nil {
		t.Fatalf("new tuner: %v", err)
	}
	t.Cleanup(tuner.Stop)

	original := tuner.Get()

	// Step 1: drive bad observations against active until shouldPropose
	// fires and the tuner generates a proposed snapshot.
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < int(2*minSamplesForAdaptDecision); i++ {
		obs := AdaptationObservation{
			CalibrationError: 0.30 + rng.NormFloat64()*0.02,
			RegretFrac:       0.20 + rng.NormFloat64()*0.02,
			SampleCount:      1,
			ObservedAt:       time.Now(),
		}
		tuner.ObserveAndAdapt(ctx, obs, false)
	}
	if _, isProposed := tuner.SnapshotForTrial(0); !isProposed {
		// 0 is even; SnapshotForTrial returns the proposed arm when
		// one exists. False means no proposal was generated.
		t.Fatal("expected a proposal after bad observations against active arm")
	}

	// Step 2: feed a window of clearly-better observations against
	// the proposed arm. The active arm keeps the bad distribution.
	for i := 0; i < int(4*minSamplesForAdaptDecision); i++ {
		// Active continues to be bad.
		tuner.ObserveAndAdapt(ctx, AdaptationObservation{
			CalibrationError:   0.30 + rng.NormFloat64()*0.02,
			RegretFrac:         0.20 + rng.NormFloat64()*0.02,
			SampleCount:        1,
			Correctness:        0.45,
			Safety:             0.70,
			Cost:               0.50,
			Ecology:            0.45,
			ValidationPassRate: 0.50,
			ArtifactQuality:    0.50,
			EvidenceRefs:       []string{fmt.Sprintf("validation:champion:%d", i)},
			ObservedAt:         time.Now(),
		}, false)
		// Proposed is clearly better.
		tuner.ObserveAndAdapt(ctx, AdaptationObservation{
			CalibrationError:   0.05 + math.Abs(rng.NormFloat64())*0.01,
			RegretFrac:         0.04 + math.Abs(rng.NormFloat64())*0.01,
			SampleCount:        1,
			Correctness:        0.95,
			Safety:             0.95,
			Cost:               0.90,
			Ecology:            0.92,
			ValidationPassRate: 0.98,
			ArtifactQuality:    0.98,
			EvidenceRefs:       []string{fmt.Sprintf("validation:challenger:%d", i)},
			ObservedAt:         time.Now(),
		}, true)
	}

	final := tuner.Get()
	if final.SnapshotID != original.SnapshotID {
		t.Fatalf("runtime proposal activated without accepted claim: got %d want %d", final.SnapshotID, original.SnapshotID)
	}
	assertPolicyCandidateStatus(t, db, PolicyCandidateStatusProposed)
	var outcomes int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_policy_outcomes`).Scan(&outcomes); err != nil {
		t.Fatalf("count policy outcomes: %v", err)
	}
	if outcomes == 0 {
		t.Fatal("expected persisted policy outcomes")
	}
}

// TestHyperParameterTuner_ConfigOverridePersistsAcrossReopen ensures
// a SetOverride call survives a tuner restart against the same DB —
// the persisted snapshot keeps the SourceConfig stamp so a subsequent
// boot doesn't silently lose the operator's choice.
func TestHyperParameterTuner_ConfigOverridePersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	db := newRawTunerDB(t)
	logger := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	ctx := context.Background()

	first, err := NewHyperParameterTuner(ctx, db, logger)
	if err != nil {
		t.Fatalf("new tuner #1: %v", err)
	}
	if err := first.SetOverride(ctx, "diversity_lambda", 0.91); err != nil {
		t.Fatalf("set override: %v", err)
	}
	first.Stop()

	second, err := NewHyperParameterTuner(ctx, db, logger)
	if err != nil {
		t.Fatalf("new tuner #2: %v", err)
	}
	t.Cleanup(second.Stop)

	hp := second.Get()
	if hp.DiversityLambda != 0.91 {
		t.Errorf("override lost across restart: got %v want 0.91", hp.DiversityLambda)
	}
	if hp.Provenance["diversity_lambda"] != SourceConfig {
		t.Errorf("override provenance lost: got %q want %q",
			hp.Provenance["diversity_lambda"], SourceConfig)
	}
}

// testWriter routes slog output to t.Log so the test runner shows
// tuner messages alongside test output (and only on failure).
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}
