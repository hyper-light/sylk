package handoff

import (
	"math"
	"sort"
	"time"
)

// ElasticState is the sizing state machine position.
type ElasticState int

const (
	// ElasticNominal is the steady state: buffer = nominalFraction of context window.
	ElasticNominal ElasticState = iota

	// ElasticExpanding indicates GP detected degradation: growing toward expandedFraction.
	ElasticExpanding

	// ElasticExpanded indicates the buffer is at expanded size, monitoring for improvement.
	ElasticExpanded

	// ElasticShrinking indicates quality recovered: reducing back toward nominalFraction.
	ElasticShrinking
)

// String returns the human-readable name of the elastic state.
func (s ElasticState) String() string {
	switch s {
	case ElasticNominal:
		return "nominal"
	case ElasticExpanding:
		return "expanding"
	case ElasticExpanded:
		return "expanded"
	case ElasticShrinking:
		return "shrinking"
	default:
		return "unknown"
	}
}

// ElasticSizer manages the buffer's token budget via a state machine
// driven by GP quality predictions. All parameters are derived from
// the agent's context window — no magic numbers.
type ElasticSizer struct {
	contextWindow    int
	nominalFraction  float64
	expandedFraction float64
	currentFraction  float64
	state            ElasticState
	stateEnteredAt   time.Time

	// Baseline learning from initial observations.
	gpStdDevBaseline  float64
	baselineObserved  []float64
	baselineLearned   bool
	baselineTarget    int // Number of observations needed to learn baseline.

	// Hysteresis: consecutive ticks in same direction before transition.
	hysteresisCounter int
	hysteresisDepth   int

	// Derived thresholds (relative to learned baseline).
	expandThreshold  float64
	shrinkThreshold  float64
	expandMultiplier float64
	shrinkMultiplier float64

	// Interpolation step per Evaluate call.
	fractionStep float64

	// Frozen during overlap — prevents resizing while handoff is in flight.
	frozen bool
}

// NewElasticSizer derives all parameters from the agent descriptor.
//
// Fraction derivation:
//   - nominalFraction = 1.0 / (contextWindow / 20_000)
//   - expandedFraction = nominalFraction * 1.5
//   - hysteresisDepth = max(3, contextWindow / 50_000)
//   - baselineTarget = hysteresisDepth * 2
func NewElasticSizer(desc AgentDescriptor) *ElasticSizer {
	cw := desc.ContextWindow
	if cw < 20_000 {
		cw = 20_000
	}

	nomFrac := 1.0 / float64(cw/20_000)
	expFrac := nomFrac * 1.5

	hystDepth := cw / 50_000
	if hystDepth < 3 {
		hystDepth = 3
	}

	// Fraction step: move 1/hysteresisDepth of the gap per Evaluate call.
	step := (expFrac - nomFrac) / float64(hystDepth)

	return &ElasticSizer{
		contextWindow:    cw,
		nominalFraction:  nomFrac,
		expandedFraction: expFrac,
		currentFraction:  nomFrac,
		state:            ElasticNominal,
		stateEnteredAt:   time.Now(),

		baselineObserved: make([]float64, 0, hystDepth*2),
		baselineTarget:   hystDepth * 2,

		hysteresisDepth:  hystDepth,
		expandMultiplier: 2.0,
		shrinkMultiplier: 1.2,
		fractionStep:     step,
	}
}

// TokenBudget returns the current buffer size in tokens.
func (e *ElasticSizer) TokenBudget() int {
	return int(e.currentFraction * float64(e.contextWindow))
}

// State returns the current elastic state.
func (e *ElasticSizer) State() ElasticState {
	return e.state
}

// Frozen returns whether the sizer is frozen (during overlap).
func (e *ElasticSizer) Frozen() bool {
	return e.frozen
}

// Freeze locks the sizer at its current budget. Called when overlap begins.
func (e *ElasticSizer) Freeze() {
	e.frozen = true
}

// Evaluate updates the state machine from a GP prediction.
// Returns (newBudget, stateChanged). If the sizer is frozen or the
// baseline hasn't been learned yet, no state transitions occur.
func (e *ElasticSizer) Evaluate(pred *GPPrediction) (int, bool) {
	if pred == nil || e.frozen {
		return e.TokenBudget(), false
	}

	// Learn baseline from initial observations.
	if !e.baselineLearned {
		return e.learnBaseline(pred)
	}

	return e.transition(pred.StdDev)
}

// learnBaseline collects initial StdDev observations and computes the
// baseline using the rolling median. No state transitions occur during
// the learning phase.
func (e *ElasticSizer) learnBaseline(pred *GPPrediction) (int, bool) {
	e.baselineObserved = append(e.baselineObserved, pred.StdDev)

	if len(e.baselineObserved) < e.baselineTarget {
		return e.TokenBudget(), false
	}

	// Compute median as baseline.
	sorted := make([]float64, len(e.baselineObserved))
	copy(sorted, e.baselineObserved)
	sort.Float64s(sorted)

	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		e.gpStdDevBaseline = (sorted[mid-1] + sorted[mid]) / 2
	} else {
		e.gpStdDevBaseline = sorted[mid]
	}

	// Prevent zero baseline (would make thresholds meaningless).
	if e.gpStdDevBaseline < 1e-10 {
		e.gpStdDevBaseline = 1e-10
	}

	e.expandThreshold = e.gpStdDevBaseline * e.expandMultiplier
	e.shrinkThreshold = e.gpStdDevBaseline * e.shrinkMultiplier
	e.baselineLearned = true

	// Release learning buffer.
	e.baselineObserved = nil

	return e.TokenBudget(), false
}

// transition applies the state machine logic based on current StdDev.
func (e *ElasticSizer) transition(stdDev float64) (int, bool) {
	prevState := e.state

	switch e.state {
	case ElasticNominal:
		e.transitionNominal(stdDev)
	case ElasticExpanding:
		e.transitionExpanding()
	case ElasticExpanded:
		e.transitionExpanded(stdDev)
	case ElasticShrinking:
		e.transitionShrinking()
	}

	return e.TokenBudget(), e.state != prevState
}

// transitionNominal checks if StdDev exceeds the expand threshold for
// enough consecutive ticks to trigger expansion.
func (e *ElasticSizer) transitionNominal(stdDev float64) {
	if stdDev > e.expandThreshold {
		e.hysteresisCounter++
		if e.hysteresisCounter >= e.hysteresisDepth {
			e.enterState(ElasticExpanding)
		}
	} else {
		e.hysteresisCounter = 0
	}
}

// transitionExpanding interpolates currentFraction toward expandedFraction.
func (e *ElasticSizer) transitionExpanding() {
	e.currentFraction = math.Min(
		e.currentFraction+e.fractionStep,
		e.expandedFraction,
	)
	if e.currentFraction >= e.expandedFraction {
		e.enterState(ElasticExpanded)
	}
}

// transitionExpanded checks if StdDev has fallen below the shrink threshold
// for enough consecutive ticks to begin shrinking.
func (e *ElasticSizer) transitionExpanded(stdDev float64) {
	if stdDev < e.shrinkThreshold {
		e.hysteresisCounter++
		if e.hysteresisCounter >= e.hysteresisDepth {
			e.enterState(ElasticShrinking)
		}
	} else {
		e.hysteresisCounter = 0
	}
}

// transitionShrinking interpolates currentFraction back toward nominalFraction.
func (e *ElasticSizer) transitionShrinking() {
	e.currentFraction = math.Max(
		e.currentFraction-e.fractionStep,
		e.nominalFraction,
	)
	if e.currentFraction <= e.nominalFraction {
		e.enterState(ElasticNominal)
	}
}

// enterState transitions to a new state, resetting hysteresis.
func (e *ElasticSizer) enterState(s ElasticState) {
	e.state = s
	e.stateEnteredAt = time.Now()
	e.hysteresisCounter = 0
}

// EvaluateWithStress updates the state machine from a GP prediction and an
// optional stress sideband. Stress can trigger expansion from nominal
// without waiting for hysteresis when severity exceeds 1/expandMultiplier.
func (e *ElasticSizer) EvaluateWithStress(pred *GPPrediction, stress *StressSignal) (int, bool) {
	if pred == nil || e.frozen {
		return e.TokenBudget(), false
	}

	if !e.baselineLearned {
		return e.learnBaseline(pred)
	}

	// Stress threshold derived from expandMultiplier (no magic number).
	stressThreshold := 1.0 / e.expandMultiplier
	if stress != nil && e.state == ElasticNominal && stress.Severity > stressThreshold {
		e.enterState(ElasticExpanding)
		return e.TokenBudget(), true
	}

	return e.transition(pred.StdDev)
}

// Reset returns to nominal state. Called after handoff completes.
func (e *ElasticSizer) Reset() {
	e.currentFraction = e.nominalFraction
	e.frozen = false
	e.enterState(ElasticNominal)
}
