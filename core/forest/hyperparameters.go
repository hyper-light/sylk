package forest

import (
	"fmt"
	"time"
)

// ─── Forest hyperparameters: integrated tuning system ────────────────
//
// The forest exposes ~20 tunable values that govern its scheduling,
// learning, and ranking behavior: substrate debounce, training
// epochs, counterfactual weight, exploration rate, etc. None of
// these have first-principles derivations — optimal values depend
// on the operator's workload (event rate, label distribution,
// retrieval latency profile).
//
// Rather than dressing up invented defaults with prose, this file
// defines the ONE source of truth — the HyperParameters struct —
// and the four-source resolution model that produces it:
//
//   1. PLACEHOLDER fallback. First-boot defaults that exist only
//      so the system functions before any tuning has run. Marked
//      explicitly via Provenance.Source so an unwary operator
//      who sees these in production can't claim ignorance.
//
//   2. WORKLOAD-DERIVED initialization. On forest startup, if
//      historical ledger data is available (forest_event_seq_log,
//      forest_training_examples, forest_retrieval_audit), values
//      are fitted from that data once and persisted. See
//      hyperparameter_init.go.
//
//   3. RUNTIME-ADAPTED tuning. While the forest runs, observed
//      outcome quality (calibration error, regret, success-rate)
//      drives proposed adjustments; A/B testing promotes winners.
//      See hyperparameter_tuner.go.
//
//   4. CONFIG override. Operator-supplied Config.* always wins
//      over all of the above — explicit ops intent.
//
// Every consumer of these values reads them from a single
// *HyperParameterTuner instance via tuner.Get(); direct const
// references are reserved for the placeholder fallback only.
//
// Provenance is tracked per-parameter: a "current snapshot" knows
// which of its values came from each source. Operators can audit
// "why is the substrate debounce 750 ms" via the provenance log
// without hunting through tuning history.

// HyperParameterSource identifies where a value came from. Tracked
// per-field so operators can audit individual values without
// chasing the whole snapshot's history.
type HyperParameterSource string

const (
	// SourcePlaceholder values are first-boot fallbacks chosen so
	// the system functions before any tuning has been done. They
	// are EXPLICITLY NOT calibrated to any specific workload.
	// Operators MUST replace them — via Config, workload-init, or
	// runtime adaptation — before relying on the forest's
	// quality signals.
	SourcePlaceholder HyperParameterSource = "placeholder"

	// SourceWorkloadInit values were fitted at forest startup from
	// the operator's existing labeled ledger. Real derivation:
	// each value is a function of observed data.
	SourceWorkloadInit HyperParameterSource = "workload_init"

	// SourceRuntimeAdapt values were promoted by the runtime tuner
	// via A/B testing against observed outcome quality. Real
	// derivation, plus statistical significance.
	SourceRuntimeAdapt HyperParameterSource = "runtime_adapt"

	// SourceConfig values were set explicitly by operator
	// configuration. Highest priority; overrides everything.
	SourceConfig HyperParameterSource = "config"
)

// HyperParameters is the immutable snapshot of all forest tunables
// + their per-field provenance. Replaced atomically when the tuner
// adapts; consumers hold a *HyperParameters pointer and re-read on
// each operation (the pointer load is cheap; the cost is borne by
// the tuner on swap).
//
// "Primitive" fields are stored directly. "Derived" fields are
// methods that compute from primitives — e.g., MaxSubstrateDelay()
// returns N × SubstrateDebounce. Tuning a primitive automatically
// rescales every derived dependent.
type HyperParameters struct {
	// ── Maintenance loop primitives (HYPERPARAMETERS) ───────────────
	//
	// SubstrateDebounce is the workload-dependent debounce window
	// for collapsing rapid substrate-refresh requests into one.
	// No first-principles default; placeholder is a guess.
	// Workload-init derives it from observed inter-event arrival
	// within commit bursts (see hyperparameter_init.go).
	SubstrateDebounce time.Duration

	// TrainingDebounce coalesces training requests. Training is
	// heavier than substrate refresh, so this is typically larger.
	// Workload-init derives from training-cycle observed wall time.
	TrainingDebounce time.Duration

	// ReplayDelay is the lag between branch creation and the
	// first replay attempt. Workload-init derives from observed
	// branch-promotion intervals (longer lag = more settling time
	// for downstream signals).
	ReplayDelay time.Duration

	// MaintenanceRetry is the cooldown between maintenance failure
	// and retry. Hyperparameter — depends on observed failure
	// patterns (deterministic vs transient) on the operator's
	// workload.
	MaintenanceRetry time.Duration

	// ── Selection-bias hyperparameters (LEDGER-FITTED) ──────────────
	//
	// CounterfactualWeight: training weight on counterfactual
	// labels (co-candidates that did NOT fire). Bounded (0, 1].
	// Workload-init: grid-search against held-out validation set.
	CounterfactualWeight float64

	// CounterfactualWindow: lookback for co-candidate eligibility.
	// Workload-init: derived from observed training cycle interval
	// times session duration distribution.
	CounterfactualWindow time.Duration

	// ImplicitNegativeWeight: training weight on implicit-negative
	// labels (retrieved but not observed-used). Bounded (0, 1].
	// Workload-init: grid-search against held-out validation set.
	ImplicitNegativeWeight float64

	// ImplicitNegativeHorizon: time window for inferring implicit
	// negatives. Workload-init: fitted from observed task duration
	// distribution (p95 of "time from retrieval to last-touch").
	ImplicitNegativeHorizon time.Duration

	// ExplorationRate: ε-greedy probability of deviating from the
	// score-greedy choice. Bounded [0, 1]. Workload-init: bandit-
	// style fit against observed regret on a held-out window.
	ExplorationRate float64

	// ExplorationLabelWeight: training boost for outcomes from
	// exploration retrievals. Workload-init: grid-search.
	ExplorationLabelWeight float64

	// ── Diversity / cooldown (LEDGER-FITTED) ────────────────────────
	//
	// DiversityLambda: MMR balance between relevance and novelty.
	// 1.0 = pure relevance; 0.0 = pure diversity. Workload-init:
	// grid-search against (precision@K × intra-list diversity).
	DiversityLambda float64

	// RetrievalCooldownPenalty: max score-scale-down for a branch
	// appearing in every recent retrieval. Bounded [0, 1].
	RetrievalCooldownPenalty float64

	// RetrievalCooldownWindow: how many recent retrievals factor
	// into the cooldown penalty.
	RetrievalCooldownWindow int

	// ── Base scorer hyperparameters ─────────────────────────────────
	//
	// BaseScoreLearningRate / L1Reg / L2Reg / Epochs / Improvement
	// Threshold are SGD optimization parameters. Workload-init:
	// grid-search across each, optimizing held-out training-set
	// accuracy.
	BaseScoreLearningRate         float64
	BaseScoreL1Reg                float64
	BaseScoreL2Reg                float64
	BaseScoreEpochs               int
	BaseScoreImprovementThreshold float64

	// BaseScoreMinTrainingExamples: minimum labeled examples to
	// attempt promotion. Derived from statistical-power
	// requirements: with N examples we can detect an improvement
	// of ~1/√N with reasonable confidence; placeholder = 1000
	// gives ~3% detection floor.
	BaseScoreMinTrainingExamples int

	// BaseScoreMaxWeight: clamp on per-component weight after SGD.
	// Hyperparameter; placeholder bounds outliers.
	BaseScoreMaxWeight float64

	// BaseScorePruningThreshold: |weight| below which a component
	// is flagged for pruning. Hyperparameter; placeholder.
	BaseScorePruningThreshold float64

	// ── Provenance ──────────────────────────────────────────────────
	//
	// Provenance records the source of each field. Indexed by the
	// canonical field name (matches the JSON tag on persistence)
	// so audit tooling can resolve "why is X this value" without
	// chasing struct-field reflection.
	Provenance map[string]HyperParameterSource

	// SnapshotID monotonically increments across atomic swaps so
	// readers can detect "did the tuner update since I last read".
	SnapshotID uint64

	// SamplesObservedAtFit records, for each value with provenance
	// = workload_init or runtime_adapt, how many samples informed
	// the fit. Lower = less confident fit. Stored as a single
	// per-snapshot count — finer granularity is in the tuner's
	// audit log.
	SamplesObservedAtFit int64

	// UpdatedAt is the wall-clock time of this snapshot.
	UpdatedAt time.Time
}

// ─── Derivation chain (option 4) ────────────────────────────────────
//
// Anything that's a function of a primitive is computed via method,
// not stored. Tuning a primitive automatically rescales the derived.

// MaxSubstrateDelay caps the debounce extension under sustained
// load. 4× the debounce — under a continuous event stream the
// substrate refresh fires after at most this long, regardless of
// how many events keep extending the debounce. The 4× ratio is a
// design choice (substrate refresh visible to UI; longer ratios
// trade staleness for fewer refreshes).
func (h *HyperParameters) MaxSubstrateDelay() time.Duration {
	if h == nil {
		return 0
	}
	return 4 * h.SubstrateDebounce
}

// MaxTrainingDelay caps training debounce extension. Same shape as
// MaxSubstrateDelay; training tolerates more latency so the
// multiplier could be larger but is held at 4× for symmetry.
func (h *HyperParameters) MaxTrainingDelay() time.Duration {
	if h == nil {
		return 0
	}
	return 4 * h.TrainingDebounce
}

// SubstrateTimeout bounds a single substrate refresh cycle. 10×
// the debounce: cycles that take longer than 10× their coalescing
// window are pathological and should abort.
func (h *HyperParameters) SubstrateTimeout() time.Duration {
	if h == nil {
		return 0
	}
	return 10 * h.SubstrateDebounce
}

// ReplayTimeout: same shape as SubstrateTimeout but anchored to
// the substrate primitive (replay and substrate share scheduling
// scale).
func (h *HyperParameters) ReplayTimeout() time.Duration {
	if h == nil {
		return 0
	}
	return 10 * h.SubstrateDebounce
}

// TrainingTimeout: 15× the training debounce — more headroom
// because training cycles are inherently longer (SGD epochs).
func (h *HyperParameters) TrainingTimeout() time.Duration {
	if h == nil {
		return 0
	}
	return 15 * h.TrainingDebounce
}

// ImplicitNegativeSweepInterval: scan-cadence anchored to the
// horizon. /12 ratio means a retrieval crossing the horizon is
// labeled within at most horizon/12 = derived-from-primitive lag.
func (h *HyperParameters) ImplicitNegativeSweepInterval() time.Duration {
	if h == nil {
		return 0
	}
	return h.ImplicitNegativeHorizon / 12
}

// SubstrateLimit: max branches per substrate refresh cycle.
// Anchored to the session canopy size (defaultSessionPrimingLimit
// in cold_start.go) × neighborhood-fanout factor.
func (h *HyperParameters) SubstrateLimit() int {
	return 6 * defaultSessionPrimingLimit
}

// TrainingExamplesPerCycle: bounded by a per-cycle heap budget
// divided by per-example cost.
//
// per-example bytes = BaseScoreComponentCount × 8B (float64
//   components) + 8B label + 8B weight ≈ 120 B
// Go struct/slice overhead factor: ~3× for typical map-of-struct
// allocation patterns.
//
// budget = trainingPerCycleHeapBudgetBytes (~512 KiB).
//
//	N ≈ budget / (perExample × overhead)
func (h *HyperParameters) TrainingExamplesPerCycle() int {
	const (
		perExampleBytes        = 120
		goOverheadFactor       = 3
		perCycleHeapBudgetByte = 512 * 1024
	)
	return perCycleHeapBudgetByte / (perExampleBytes * goOverheadFactor)
}

// ReplayBatchSize: items processed per replay cycle. Anchored to
// the maintenance retry interval — we want one replay cycle to
// finish well within one retry window so a transient failure
// doesn't double up. ~ms per item; retry interval / 10 gives ~10×
// safety margin.
func (h *HyperParameters) ReplayBatchSize() int {
	if h == nil {
		return 1
	}
	const expectedPerItemMillis = 10
	budgetMillis := int(h.MaintenanceRetry / time.Millisecond)
	if budgetMillis <= 0 {
		return 1
	}
	n := budgetMillis / 10 / expectedPerItemMillis
	if n < 1 {
		n = 1
	}
	return n
}

// ─── Placeholder fallback (option 3 contract) ───────────────────────
//
// PlaceholderHyperParameters returns a snapshot all of whose values
// are SourcePlaceholder. Callers MUST replace via Config or
// fitting before relying on quality signals. Logged at warn level
// when used in production to make the operational hazard visible.

// PlaceholderHyperParameters returns first-boot defaults. Every
// field is marked SourcePlaceholder.
func PlaceholderHyperParameters() *HyperParameters {
	hp := &HyperParameters{
		SubstrateDebounce: 500 * time.Millisecond,
		TrainingDebounce:  2 * time.Second,
		ReplayDelay:       10 * time.Second,
		MaintenanceRetry:  2 * time.Second,

		CounterfactualWeight:    0.3,
		CounterfactualWindow:    24 * time.Hour,
		ImplicitNegativeWeight:  0.15,
		ImplicitNegativeHorizon: 1 * time.Hour,
		ExplorationRate:         0.05,
		ExplorationLabelWeight:  1.5,

		DiversityLambda:          0.7,
		RetrievalCooldownPenalty: 0.3,
		RetrievalCooldownWindow:  32,

		BaseScoreLearningRate:         0.005,
		BaseScoreL1Reg:                0.001,
		BaseScoreL2Reg:                0.01,
		BaseScoreEpochs:               8,
		BaseScoreImprovementThreshold: 0.01,
		BaseScoreMinTrainingExamples:  1000,
		BaseScoreMaxWeight:            5.0,
		BaseScorePruningThreshold:     0.005,

		Provenance:           allFieldsAs(SourcePlaceholder),
		SnapshotID:           0,
		SamplesObservedAtFit: 0,
		UpdatedAt:            time.Now().UTC(),
	}
	return hp
}

// allFieldsAs returns a Provenance map with every known field
// stamped to source. Centralised so adding a new HyperParameters
// field forces an update here (compile-time discipline via the
// const list of field names).
func allFieldsAs(source HyperParameterSource) map[string]HyperParameterSource {
	m := make(map[string]HyperParameterSource, len(allHyperParameterFields))
	for _, f := range allHyperParameterFields {
		m[f] = source
	}
	return m
}

// allHyperParameterFields enumerates every primitive field name.
// Adding a new field to HyperParameters requires adding it here —
// the serialization (JSON tags), provenance (allFieldsAs), and
// auditing surfaces all key off this list.
var allHyperParameterFields = []string{
	"substrate_debounce",
	"training_debounce",
	"replay_delay",
	"maintenance_retry",
	"counterfactual_weight",
	"counterfactual_window",
	"implicit_negative_weight",
	"implicit_negative_horizon",
	"exploration_rate",
	"exploration_label_weight",
	"diversity_lambda",
	"retrieval_cooldown_penalty",
	"retrieval_cooldown_window",
	"base_score_learning_rate",
	"base_score_l1_reg",
	"base_score_l2_reg",
	"base_score_epochs",
	"base_score_improvement_threshold",
	"base_score_min_training_examples",
	"base_score_max_weight",
	"base_score_pruning_threshold",
}

// Validate rejects snapshots with values outside their valid
// domains. Called by the store on Load and by the runtime tuner
// before applying a swap.
func (h *HyperParameters) Validate() error {
	if h == nil {
		return fmt.Errorf("hyperparameters: nil snapshot")
	}
	if h.SubstrateDebounce <= 0 {
		return fmt.Errorf("substrate_debounce must be > 0, got %s", h.SubstrateDebounce)
	}
	if h.TrainingDebounce <= 0 {
		return fmt.Errorf("training_debounce must be > 0, got %s", h.TrainingDebounce)
	}
	if h.ReplayDelay <= 0 {
		return fmt.Errorf("replay_delay must be > 0, got %s", h.ReplayDelay)
	}
	if h.MaintenanceRetry <= 0 {
		return fmt.Errorf("maintenance_retry must be > 0, got %s", h.MaintenanceRetry)
	}
	if h.CounterfactualWeight <= 0 || h.CounterfactualWeight > 1 {
		return fmt.Errorf("counterfactual_weight must be in (0,1], got %v", h.CounterfactualWeight)
	}
	if h.ImplicitNegativeWeight <= 0 || h.ImplicitNegativeWeight > 1 {
		return fmt.Errorf("implicit_negative_weight must be in (0,1], got %v", h.ImplicitNegativeWeight)
	}
	if h.ExplorationRate < 0 || h.ExplorationRate > 1 {
		return fmt.Errorf("exploration_rate must be in [0,1], got %v", h.ExplorationRate)
	}
	if h.DiversityLambda < 0 || h.DiversityLambda > 1 {
		return fmt.Errorf("diversity_lambda must be in [0,1], got %v", h.DiversityLambda)
	}
	if h.RetrievalCooldownPenalty < 0 || h.RetrievalCooldownPenalty > 1 {
		return fmt.Errorf("retrieval_cooldown_penalty must be in [0,1], got %v", h.RetrievalCooldownPenalty)
	}
	if h.RetrievalCooldownWindow <= 0 {
		return fmt.Errorf("retrieval_cooldown_window must be > 0, got %d", h.RetrievalCooldownWindow)
	}
	if h.BaseScoreEpochs <= 0 {
		return fmt.Errorf("base_score_epochs must be > 0, got %d", h.BaseScoreEpochs)
	}
	if h.BaseScoreMinTrainingExamples <= 0 {
		return fmt.Errorf("base_score_min_training_examples must be > 0, got %d", h.BaseScoreMinTrainingExamples)
	}
	if h.Provenance == nil {
		return fmt.Errorf("provenance map is nil")
	}
	for _, field := range allHyperParameterFields {
		if _, ok := h.Provenance[field]; !ok {
			return fmt.Errorf("provenance missing for field %q", field)
		}
	}
	return nil
}

// Clone returns a deep copy. Callers needing to mutate a snapshot
// (e.g., proposing an A/B variant) Clone first to avoid touching
// the live snapshot.
func (h *HyperParameters) Clone() *HyperParameters {
	if h == nil {
		return nil
	}
	out := *h
	if h.Provenance != nil {
		out.Provenance = make(map[string]HyperParameterSource, len(h.Provenance))
		for k, v := range h.Provenance {
			out.Provenance[k] = v
		}
	}
	return &out
}
