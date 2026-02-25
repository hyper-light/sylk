package shared

import (
	"math"
	"time"
)

// =============================================================================
// Multi-Dimensional Quality Scoring
// =============================================================================

// QualityDimension enumerates the axes along which pipeline agents score work.
// The pipeline operates as an RL loop maximizing a composite reward signal
// across these dimensions.
type QualityDimension string

const (
	DimTaskAdherence  QualityDimension = "task_adherence"  // Fidelity to task definition
	DimCorrectness    QualityDimension = "correctness"     // Functional correctness
	DimReadability    QualityDimension = "readability"      // Code clarity and style
	DimTestComplete   QualityDimension = "test_completeness" // Test coverage and quality
	DimRobustness     QualityDimension = "robustness"      // Error handling, edge cases
	DimPerformance    QualityDimension = "performance"     // Computational efficiency
	DimInnovativeness QualityDimension = "innovativeness"  // Novel approaches, elegance
)

// AllQualityDimensions returns the canonical ordered list of dimensions.
func AllQualityDimensions() []QualityDimension {
	return []QualityDimension{
		DimTaskAdherence,
		DimCorrectness,
		DimReadability,
		DimTestComplete,
		DimRobustness,
		DimPerformance,
		DimInnovativeness,
	}
}

// QualityScores holds per-dimension scores in [0, 1] along with actionable
// issues per dimension. This is the structured signal that drives the RL loop.
type QualityScores struct {
	Scores    map[QualityDimension]float64        `json:"scores"`
	Issues    map[QualityDimension][]QualityIssue `json:"issues,omitempty"`
	Composite float64                             `json:"composite"`
	Reasoning string                              `json:"reasoning"`
}

// QualityIssue is an actionable finding for a specific dimension.
type QualityIssue struct {
	Description string  `json:"description"`
	Severity    string  `json:"severity"` // "low" | "medium" | "high" | "critical"
	File        string  `json:"file,omitempty"`
	Line        int     `json:"line,omitempty"`
	SuggestFix  string  `json:"suggest_fix,omitempty"`
	Impact      float64 `json:"impact"` // How much this issue drags down the dimension score [0,1]
}

// DefaultQualityWeights returns the default weight vector for composite
// computation. Correctness and task adherence dominate; innovativeness
// contributes but doesn't gate success.
func DefaultQualityWeights() map[QualityDimension]float64 {
	return map[QualityDimension]float64{
		DimTaskAdherence:  1.5,
		DimCorrectness:    2.0, // Highest weight — incorrect code is worthless
		DimReadability:    1.0,
		DimTestComplete:   1.2,
		DimRobustness:     1.3,
		DimPerformance:    0.8,
		DimInnovativeness: 0.5,
	}
}

// ComputeComposite calculates the weighted geometric mean across all scored
// dimensions. Geometric mean penalizes low outliers — a single 0.1 dimension
// tanks the composite even if others are 0.9. This ensures the RL loop cannot
// "game" the reward by excelling on easy dimensions while ignoring hard ones.
func ComputeComposite(scores map[QualityDimension]float64, weights map[QualityDimension]float64) float64 {
	var sumWeightedLog, sumWeights float64
	for dim, score := range scores {
		w := 1.0
		if wt, ok := weights[dim]; ok {
			w = wt
		}
		if w <= 0 {
			continue
		}
		if score <= 0 {
			return 0
		}
		sumWeightedLog += w * math.Log(score)
		sumWeights += w
	}
	if sumWeights <= 0 {
		return 0
	}
	return math.Exp(sumWeightedLog / sumWeights)
}

// NewQualityScores creates a QualityScores with zero scores and the given reasoning.
func NewQualityScores(reasoning string) *QualityScores {
	return &QualityScores{
		Scores:  make(map[QualityDimension]float64),
		Issues:  make(map[QualityDimension][]QualityIssue),
		Reasoning: reasoning,
	}
}

// ComputeAndSet calculates the composite from current scores using the given weights.
func (qs *QualityScores) ComputeAndSet(weights map[QualityDimension]float64) {
	qs.Composite = ComputeComposite(qs.Scores, weights)
}

// =============================================================================
// Pipeline Feedback Types
// =============================================================================

// TestFeedback carries structured test results from the Tester back to the
// Engineer during the RL refactor loop. Includes both boolean pass/fail
// (backward compat) and multi-dimensional quality scores for reward shaping.
type TestFeedback struct {
	TaskID        string              `json:"task_id"`
	CorrelationID string              `json:"correlation_id"`
	Iteration     int                 `json:"iteration"`
	TestsPassed   bool                `json:"tests_passed"`
	PassCount     int                 `json:"pass_count"`
	FailCount     int                 `json:"fail_count"`
	Failures      []TestFailureDetail `json:"failures,omitempty"`
	Diagnosis     *DiagnosisSummary   `json:"diagnosis,omitempty"`
	Confidence    float64             `json:"confidence"`
	Quality       *QualityScores      `json:"quality,omitempty"` // Multi-dimensional RL reward signal
	Timestamp     time.Time           `json:"timestamp"`
}

// ShouldContinueLoop determines whether the RL loop should iterate again.
// Uses the multi-dimensional quality composite when available, falling back
// to boolean TestsPassed. The threshold is the minimum composite score that
// signals "good enough" — derived from the escalation policy's pipeline
// threshold (0.5) but higher here because we want to push quality up.
func (tf *TestFeedback) ShouldContinueLoop(minComposite float64) bool {
	if tf.Quality != nil && len(tf.Quality.Scores) > 0 {
		return tf.Quality.Composite < minComposite
	}
	return !tf.TestsPassed
}

// TestFailureDetail describes a single test failure with optional fix suggestion.
type TestFailureDetail struct {
	TestName   string `json:"test_name"`
	ErrorMsg   string `json:"error_msg"`
	File       string `json:"file,omitempty"`
	Line       int    `json:"line,omitempty"`
	SuggestFix string `json:"suggest_fix,omitempty"`
	Category   string `json:"category,omitempty"`
}

// DiagnosisSummary is the tester's root-cause analysis of test failures.
type DiagnosisSummary struct {
	RootCause          string   `json:"root_cause"`
	Category           string   `json:"category"`
	IsTestBug          bool     `json:"is_test_bug"`
	Suggestion         string   `json:"suggestion"`
	InvestigationTrail []string `json:"investigation_trail,omitempty"`
}

// RefactorLoopConfig configures the bounded RL refactor loop
// between Engineer and Tester/Inspector agents.
type RefactorLoopConfig struct {
	MaxIterations        int           `json:"max_iterations"`
	TesterTimeout        time.Duration `json:"tester_timeout"`
	EscalateOnExhaustion bool          `json:"escalate_on_exhaustion"`
	// MinQualityComposite is the minimum composite quality score to accept.
	// Derived from the escalation pipeline threshold (0.5) + quality headroom.
	MinQualityComposite float64 `json:"min_quality_composite"`
}

// DefaultRefactorLoopConfig returns the default refactor loop configuration.
// MaxIterations derives from DefaultAuditConfig().MaxAuditIterations.
// TesterTimeout derives from DefaultConsultationTimeout × 2 (tester needs
// more time than a knowledge agent consultation).
func DefaultRefactorLoopConfig() RefactorLoopConfig {
	return RefactorLoopConfig{
		MaxIterations:        3,
		TesterTimeout:        DefaultConsultationTimeout * 2,
		EscalateOnExhaustion: true,
		MinQualityComposite:  0.7,
	}
}
