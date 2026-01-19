package handoff

import (
	"encoding/json"
	"math"
	"sync"
	"time"
)

// =============================================================================
// HO.12.2 Quality Assessment Metrics
// =============================================================================
//
// This file defines the metrics and scoring structures for quality assessment.
// It includes:
//   - QualityScore: The main quality score structure
//   - QualityMetricType: Enum for metric types
//   - QualityAggregator: Combines multiple signals
//
// Key features:
//   - Support multiple quality signals
//   - Bayesian confidence based on signal count
//   - Thread-safe aggregation
//   - Historical tracking for trends
//   - JSON serialization

// QualityMetricType represents the type of quality metric.
type QualityMetricType int

const (
	// MetricUnknown represents an unknown metric type.
	MetricUnknown QualityMetricType = iota

	// MetricResponseCoherence measures how coherent and well-structured a response is.
	MetricResponseCoherence

	// MetricToolAccuracy measures the accuracy and success rate of tool executions.
	MetricToolAccuracy

	// MetricTaskCompletion measures how well tasks are completed.
	MetricTaskCompletion

	// MetricUserRating measures explicit user ratings and feedback.
	MetricUserRating
)

// String returns the string representation of the metric type.
func (mt QualityMetricType) String() string {
	switch mt {
	case MetricResponseCoherence:
		return "response_coherence"
	case MetricToolAccuracy:
		return "tool_accuracy"
	case MetricTaskCompletion:
		return "task_completion"
	case MetricUserRating:
		return "user_rating"
	default:
		return "unknown"
	}
}

// ParseQualityMetricType parses a string into a QualityMetricType.
func ParseQualityMetricType(s string) QualityMetricType {
	switch s {
	case "response_coherence":
		return MetricResponseCoherence
	case "tool_accuracy":
		return MetricToolAccuracy
	case "task_completion":
		return MetricTaskCompletion
	case "user_rating":
		return MetricUserRating
	default:
		return MetricUnknown
	}
}

// =============================================================================
// QualityScore
// =============================================================================

// QualityScore represents an aggregated quality assessment score.
// It combines multiple quality signals into component scores and an overall score.
type QualityScore struct {
	// ResponseQuality is the quality of generated responses (coherence, completeness).
	// Value in [0, 1] where 1 indicates excellent response quality.
	ResponseQuality float64 `json:"response_quality"`

	// ToolSuccess is the success rate and accuracy of tool executions.
	// Value in [0, 1] where 1 indicates all tools executed successfully.
	ToolSuccess float64 `json:"tool_success"`

	// UserSatisfaction is the inferred user satisfaction from feedback.
	// Value in [0, 1] where 1 indicates high user satisfaction.
	UserSatisfaction float64 `json:"user_satisfaction"`

	// Confidence is the confidence in the overall score based on signal count.
	// Value in [0, 1] where 1 indicates high confidence (many signals).
	Confidence float64 `json:"confidence"`

	// SignalCount tracks the total number of signals aggregated.
	SignalCount int `json:"signal_count"`

	// LastUpdated is when the score was last updated.
	LastUpdated time.Time `json:"last_updated"`

	// MetricBreakdown contains the breakdown by metric type.
	MetricBreakdown map[QualityMetricType]*MetricScore `json:"-"`
}

// MetricScore holds the score for a single metric type.
type MetricScore struct {
	// Sum is the sum of all signal values.
	Sum float64 `json:"sum"`

	// Count is the number of signals.
	Count int `json:"count"`

	// Mean is the current mean value.
	Mean float64 `json:"mean"`

	// Variance tracks the variance of signals.
	Variance float64 `json:"variance"`

	// M2 is used for Welford's online variance algorithm.
	M2 float64 `json:"m2"`

	// LastValue is the most recent signal value.
	LastValue float64 `json:"last_value"`

	// LastUpdated is when this metric was last updated.
	LastUpdated time.Time `json:"last_updated"`
}

// NewQualityScore creates a new QualityScore with default values.
func NewQualityScore() *QualityScore {
	return &QualityScore{
		ResponseQuality:  0.5, // Neutral starting point
		ToolSuccess:      0.5,
		UserSatisfaction: 0.5,
		Confidence:       0.0, // No confidence until signals arrive
		SignalCount:      0,
		LastUpdated:      time.Now(),
		MetricBreakdown:  make(map[QualityMetricType]*MetricScore),
	}
}

// ComputeOverall computes the overall quality score as a weighted average.
// Returns a value in [0, 1] where 1 indicates excellent overall quality.
func (qs *QualityScore) ComputeOverall() float64 {
	if qs == nil {
		return 0.5
	}

	// Default weights (can be made configurable)
	const (
		responseWeight     = 0.35
		toolWeight         = 0.30
		satisfactionWeight = 0.35
	)

	overall := qs.ResponseQuality*responseWeight +
		qs.ToolSuccess*toolWeight +
		qs.UserSatisfaction*satisfactionWeight

	return clamp(overall, 0.0, 1.0)
}

// ComputeOverallWeighted computes the overall quality score with custom weights.
func (qs *QualityScore) ComputeOverallWeighted(weights map[QualityMetricType]float64) float64 {
	if qs == nil || len(weights) == 0 {
		return qs.ComputeOverall()
	}

	totalWeight := 0.0
	weightedSum := 0.0

	// Response quality
	if w, ok := weights[MetricResponseCoherence]; ok && w > 0 {
		weightedSum += qs.ResponseQuality * w
		totalWeight += w
	}

	// Tool success (tool accuracy maps to tool success)
	if w, ok := weights[MetricToolAccuracy]; ok && w > 0 {
		weightedSum += qs.ToolSuccess * w
		totalWeight += w
	}

	// Task completion contributes to response quality
	if w, ok := weights[MetricTaskCompletion]; ok && w > 0 {
		weightedSum += qs.ResponseQuality * w
		totalWeight += w
	}

	// User rating
	if w, ok := weights[MetricUserRating]; ok && w > 0 {
		weightedSum += qs.UserSatisfaction * w
		totalWeight += w
	}

	if totalWeight == 0 {
		return qs.ComputeOverall()
	}

	return clamp(weightedSum/totalWeight, 0.0, 1.0)
}

// GetMetricBreakdown returns the breakdown of scores by metric type.
func (qs *QualityScore) GetMetricBreakdown() map[QualityMetricType]float64 {
	if qs == nil {
		return nil
	}

	result := make(map[QualityMetricType]float64)
	result[MetricResponseCoherence] = qs.ResponseQuality
	result[MetricToolAccuracy] = qs.ToolSuccess
	result[MetricTaskCompletion] = qs.ResponseQuality // Tied to response quality
	result[MetricUserRating] = qs.UserSatisfaction

	return result
}

// Clone creates a deep copy of the quality score.
func (qs *QualityScore) Clone() *QualityScore {
	if qs == nil {
		return nil
	}

	cloned := &QualityScore{
		ResponseQuality:  qs.ResponseQuality,
		ToolSuccess:      qs.ToolSuccess,
		UserSatisfaction: qs.UserSatisfaction,
		Confidence:       qs.Confidence,
		SignalCount:      qs.SignalCount,
		LastUpdated:      qs.LastUpdated,
		MetricBreakdown:  make(map[QualityMetricType]*MetricScore),
	}

	for k, v := range qs.MetricBreakdown {
		if v != nil {
			cloned.MetricBreakdown[k] = &MetricScore{
				Sum:         v.Sum,
				Count:       v.Count,
				Mean:        v.Mean,
				Variance:    v.Variance,
				M2:          v.M2,
				LastValue:   v.LastValue,
				LastUpdated: v.LastUpdated,
			}
		}
	}

	return cloned
}

// IsGood returns true if the quality score indicates good quality.
// Uses a threshold of 0.7 for the overall score.
func (qs *QualityScore) IsGood() bool {
	if qs == nil {
		return false
	}
	return qs.ComputeOverall() >= 0.7 && qs.Confidence >= 0.3
}

// IsPoor returns true if the quality score indicates poor quality.
// Uses a threshold of 0.4 for the overall score.
func (qs *QualityScore) IsPoor() bool {
	if qs == nil {
		return true
	}
	return qs.ComputeOverall() < 0.4 && qs.Confidence >= 0.3
}

// =============================================================================
// QualityAggregator
// =============================================================================

// QualityAggregator combines multiple quality signals into a unified score.
// It uses Bayesian confidence estimation based on signal count and maintains
// historical tracking for trend detection.
type QualityAggregator struct {
	mu sync.RWMutex

	// config holds the aggregator configuration.
	config *QualityAggregatorConfig

	// metrics stores per-metric score tracking.
	metrics map[QualityMetricType]*MetricScore

	// history stores historical scores for trend detection.
	history []historicalScore

	// totalSignals tracks total signals received.
	totalSignals int64

	// lastComputed is the last computed score.
	lastComputed *QualityScore
}

// historicalScore stores a historical quality score.
type historicalScore struct {
	Score     float64
	Timestamp time.Time
}

// QualityAggregatorConfig configures the QualityAggregator.
type QualityAggregatorConfig struct {
	// Weights defines the weight for each metric type.
	// Values should sum to 1.0 for proper normalization.
	Weights map[QualityMetricType]float64 `json:"weights"`

	// ConfidenceScale controls how quickly confidence grows with signal count.
	// Higher values mean slower confidence growth.
	// Default is 10.0 (confidence ~0.63 at 10 signals, ~0.95 at 30 signals).
	ConfidenceScale float64 `json:"confidence_scale"`

	// MinSignalsForConfidence is the minimum signals needed for non-zero confidence.
	MinSignalsForConfidence int `json:"min_signals_for_confidence"`

	// MaxHistorySize is the maximum number of historical scores to retain.
	MaxHistorySize int `json:"max_history_size"`

	// DecayFactor controls exponential decay of older signals (0-1).
	// 1.0 means no decay, 0.9 means 10% decay per update.
	DecayFactor float64 `json:"decay_factor"`

	// TrendWindowSize is the number of recent scores to consider for trends.
	TrendWindowSize int `json:"trend_window_size"`
}

// DefaultQualityAggregatorConfig returns sensible defaults for the aggregator.
func DefaultQualityAggregatorConfig() *QualityAggregatorConfig {
	return &QualityAggregatorConfig{
		Weights: map[QualityMetricType]float64{
			MetricResponseCoherence: 0.30,
			MetricToolAccuracy:      0.25,
			MetricTaskCompletion:    0.20,
			MetricUserRating:        0.25,
		},
		ConfidenceScale:         10.0,
		MinSignalsForConfidence: 1,
		MaxHistorySize:          100,
		DecayFactor:             0.95,
		TrendWindowSize:         10,
	}
}

// NewQualityAggregator creates a new QualityAggregator.
// If config is nil, uses default configuration.
func NewQualityAggregator(config *QualityAggregatorConfig) *QualityAggregator {
	if config == nil {
		config = DefaultQualityAggregatorConfig()
	}

	return &QualityAggregator{
		config:       config,
		metrics:      make(map[QualityMetricType]*MetricScore),
		history:      make([]historicalScore, 0, config.MaxHistorySize),
		lastComputed: NewQualityScore(),
	}
}

// AddSignal adds a quality signal to the aggregator.
// metricType indicates which metric this signal contributes to.
// value should be in [0, 1] where 1 indicates best quality.
func (qa *QualityAggregator) AddSignal(metricType QualityMetricType, value float64) {
	qa.mu.Lock()
	defer qa.mu.Unlock()

	value = clamp(value, 0.0, 1.0)

	// Get or create metric score
	metric, exists := qa.metrics[metricType]
	if !exists {
		metric = &MetricScore{}
		qa.metrics[metricType] = metric
	}

	// Apply decay to existing metrics
	if qa.config.DecayFactor < 1.0 && metric.Count > 0 {
		metric.Sum *= qa.config.DecayFactor
		metric.Count = int(float64(metric.Count) * qa.config.DecayFactor)
		if metric.Count < 1 {
			metric.Count = 1
		}
	}

	// Update metric using Welford's online algorithm
	metric.Count++
	metric.Sum += value
	delta := value - metric.Mean
	metric.Mean += delta / float64(metric.Count)
	delta2 := value - metric.Mean
	metric.M2 += delta * delta2
	if metric.Count > 1 {
		metric.Variance = metric.M2 / float64(metric.Count-1)
	}
	metric.LastValue = value
	metric.LastUpdated = time.Now()

	qa.totalSignals++
}

// ComputeScore computes the current aggregated quality score.
func (qa *QualityAggregator) ComputeScore() *QualityScore {
	qa.mu.Lock()
	defer qa.mu.Unlock()

	score := NewQualityScore()
	score.SignalCount = int(qa.totalSignals)
	score.LastUpdated = time.Now()

	// Compute each component score from metrics
	if metric, ok := qa.metrics[MetricResponseCoherence]; ok && metric.Count > 0 {
		score.ResponseQuality = metric.Mean
	}

	if metric, ok := qa.metrics[MetricToolAccuracy]; ok && metric.Count > 0 {
		score.ToolSuccess = metric.Mean
	}

	// Task completion contributes to response quality
	if metric, ok := qa.metrics[MetricTaskCompletion]; ok && metric.Count > 0 {
		// Blend task completion into response quality
		responseMetric := qa.metrics[MetricResponseCoherence]
		if responseMetric != nil && responseMetric.Count > 0 {
			totalCount := responseMetric.Count + metric.Count
			score.ResponseQuality = (responseMetric.Mean*float64(responseMetric.Count) +
				metric.Mean*float64(metric.Count)) / float64(totalCount)
		} else {
			score.ResponseQuality = metric.Mean
		}
	}

	if metric, ok := qa.metrics[MetricUserRating]; ok && metric.Count > 0 {
		score.UserSatisfaction = metric.Mean
	}

	// Compute Bayesian confidence based on signal count
	score.Confidence = qa.computeConfidenceLocked()

	// Copy metric breakdown
	for metricType, metric := range qa.metrics {
		score.MetricBreakdown[metricType] = &MetricScore{
			Sum:         metric.Sum,
			Count:       metric.Count,
			Mean:        metric.Mean,
			Variance:    metric.Variance,
			M2:          metric.M2,
			LastValue:   metric.LastValue,
			LastUpdated: metric.LastUpdated,
		}
	}

	// Add to history
	qa.history = qa.appendHistory(qa.history, historicalScore{
		Score:     score.ComputeOverall(),
		Timestamp: score.LastUpdated,
	})

	qa.lastComputed = score.Clone()
	return score
}

// computeConfidenceLocked computes Bayesian confidence from signal count.
// Caller must hold the lock.
func (qa *QualityAggregator) computeConfidenceLocked() float64 {
	if qa.totalSignals < int64(qa.config.MinSignalsForConfidence) {
		return 0.0
	}

	// Exponential saturation function
	// confidence = 1 - exp(-signals / scale)
	// This gives logarithmic growth: ~0.63 at scale, ~0.86 at 2*scale, ~0.95 at 3*scale
	confidence := 1.0 - math.Exp(-float64(qa.totalSignals)/qa.config.ConfidenceScale)

	return clamp(confidence, 0.0, 1.0)
}

// appendHistory appends a score to history while maintaining max size.
func (qa *QualityAggregator) appendHistory(history []historicalScore, score historicalScore) []historicalScore {
	if len(history) >= qa.config.MaxHistorySize {
		history = history[1:]
	}
	return append(history, score)
}

// GetTrend returns the trend direction of quality scores.
// Returns a value in [-1, 1] where:
//   - Positive values indicate improving quality
//   - Negative values indicate declining quality
//   - Zero indicates stable quality
func (qa *QualityAggregator) GetTrend() float64 {
	qa.mu.RLock()
	defer qa.mu.RUnlock()

	if len(qa.history) < 2 {
		return 0.0
	}

	windowSize := qa.config.TrendWindowSize
	if windowSize > len(qa.history) {
		windowSize = len(qa.history)
	}

	recentHistory := qa.history[len(qa.history)-windowSize:]

	// Simple linear regression to detect trend
	n := float64(len(recentHistory))
	if n < 2 {
		return 0.0
	}

	sumX := 0.0
	sumY := 0.0
	sumXY := 0.0
	sumX2 := 0.0

	for i, h := range recentHistory {
		x := float64(i)
		y := h.Score
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	denominator := n*sumX2 - sumX*sumX
	if math.Abs(denominator) < 1e-10 {
		return 0.0
	}

	slope := (n*sumXY - sumX*sumY) / denominator

	// Normalize slope to [-1, 1]
	// Typical slope range is [-0.1, 0.1] for quality scores
	normalizedSlope := slope * 10.0

	return clamp(normalizedSlope, -1.0, 1.0)
}

// IsTrendingUp returns true if quality is trending upward.
func (qa *QualityAggregator) IsTrendingUp() bool {
	return qa.GetTrend() > 0.2
}

// IsTrendingDown returns true if quality is trending downward.
func (qa *QualityAggregator) IsTrendingDown() bool {
	return qa.GetTrend() < -0.2
}

// Reset clears all accumulated signals.
func (qa *QualityAggregator) Reset() {
	qa.mu.Lock()
	defer qa.mu.Unlock()

	qa.metrics = make(map[QualityMetricType]*MetricScore)
	qa.history = make([]historicalScore, 0, qa.config.MaxHistorySize)
	qa.totalSignals = 0
	qa.lastComputed = NewQualityScore()
}

// GetLastComputed returns the last computed score without recomputing.
func (qa *QualityAggregator) GetLastComputed() *QualityScore {
	qa.mu.RLock()
	defer qa.mu.RUnlock()

	if qa.lastComputed == nil {
		return NewQualityScore()
	}
	return qa.lastComputed.Clone()
}

// GetMetricStats returns the statistics for a specific metric type.
func (qa *QualityAggregator) GetMetricStats(metricType QualityMetricType) *MetricScore {
	qa.mu.RLock()
	defer qa.mu.RUnlock()

	metric, ok := qa.metrics[metricType]
	if !ok {
		return nil
	}

	return &MetricScore{
		Sum:         metric.Sum,
		Count:       metric.Count,
		Mean:        metric.Mean,
		Variance:    metric.Variance,
		M2:          metric.M2,
		LastValue:   metric.LastValue,
		LastUpdated: metric.LastUpdated,
	}
}

// =============================================================================
// Statistics
// =============================================================================

// QualityAggregatorStats contains statistics about the aggregator.
type QualityAggregatorStats struct {
	// TotalSignals is the total number of signals received.
	TotalSignals int64 `json:"total_signals"`

	// MetricCounts contains the signal count per metric type.
	MetricCounts map[QualityMetricType]int `json:"metric_counts"`

	// HistorySize is the current size of the history buffer.
	HistorySize int `json:"history_size"`

	// CurrentTrend is the current quality trend.
	CurrentTrend float64 `json:"current_trend"`

	// LastComputed is the last computed quality score.
	LastComputed *QualityScore `json:"last_computed"`
}

// Stats returns statistics about the aggregator.
func (qa *QualityAggregator) Stats() QualityAggregatorStats {
	qa.mu.RLock()
	defer qa.mu.RUnlock()

	metricCounts := make(map[QualityMetricType]int)
	for metricType, metric := range qa.metrics {
		metricCounts[metricType] = metric.Count
	}

	return QualityAggregatorStats{
		TotalSignals: qa.totalSignals,
		MetricCounts: metricCounts,
		HistorySize:  len(qa.history),
		CurrentTrend: qa.GetTrend(),
		LastComputed: qa.lastComputed.Clone(),
	}
}

// =============================================================================
// JSON Serialization
// =============================================================================

// MarshalJSON implements json.Marshaler for QualityScore.
func (qs *QualityScore) MarshalJSON() ([]byte, error) {
	type scoreJSON struct {
		ResponseQuality  float64           `json:"response_quality"`
		ToolSuccess      float64           `json:"tool_success"`
		UserSatisfaction float64           `json:"user_satisfaction"`
		Confidence       float64           `json:"confidence"`
		SignalCount      int               `json:"signal_count"`
		LastUpdated      string            `json:"last_updated"`
		Overall          float64           `json:"overall"`
		MetricBreakdown  map[string]float64 `json:"metric_breakdown"`
	}

	breakdown := make(map[string]float64)
	for k, v := range qs.GetMetricBreakdown() {
		breakdown[k.String()] = v
	}

	return json.Marshal(scoreJSON{
		ResponseQuality:  qs.ResponseQuality,
		ToolSuccess:      qs.ToolSuccess,
		UserSatisfaction: qs.UserSatisfaction,
		Confidence:       qs.Confidence,
		SignalCount:      qs.SignalCount,
		LastUpdated:      qs.LastUpdated.Format(time.RFC3339Nano),
		Overall:          qs.ComputeOverall(),
		MetricBreakdown:  breakdown,
	})
}

// UnmarshalJSON implements json.Unmarshaler for QualityScore.
func (qs *QualityScore) UnmarshalJSON(data []byte) error {
	type scoreJSON struct {
		ResponseQuality  float64           `json:"response_quality"`
		ToolSuccess      float64           `json:"tool_success"`
		UserSatisfaction float64           `json:"user_satisfaction"`
		Confidence       float64           `json:"confidence"`
		SignalCount      int               `json:"signal_count"`
		LastUpdated      string            `json:"last_updated"`
		MetricBreakdown  map[string]float64 `json:"metric_breakdown"`
	}

	var temp scoreJSON
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	qs.ResponseQuality = temp.ResponseQuality
	qs.ToolSuccess = temp.ToolSuccess
	qs.UserSatisfaction = temp.UserSatisfaction
	qs.Confidence = temp.Confidence
	qs.SignalCount = temp.SignalCount

	if temp.LastUpdated != "" {
		if t, err := time.Parse(time.RFC3339Nano, temp.LastUpdated); err == nil {
			qs.LastUpdated = t
		}
	}

	qs.MetricBreakdown = make(map[QualityMetricType]*MetricScore)

	return nil
}

// aggregatorJSON is used for JSON marshaling/unmarshaling of QualityAggregator.
type aggregatorJSON struct {
	Config       *QualityAggregatorConfig             `json:"config"`
	Metrics      map[string]*MetricScore              `json:"metrics"`
	History      []historicalScore                    `json:"history"`
	TotalSignals int64                                `json:"total_signals"`
	LastComputed *QualityScore                        `json:"last_computed"`
}

// MarshalJSON implements json.Marshaler for QualityAggregator.
func (qa *QualityAggregator) MarshalJSON() ([]byte, error) {
	qa.mu.RLock()
	defer qa.mu.RUnlock()

	metricsJSON := make(map[string]*MetricScore)
	for k, v := range qa.metrics {
		metricsJSON[k.String()] = v
	}

	return json.Marshal(aggregatorJSON{
		Config:       qa.config,
		Metrics:      metricsJSON,
		History:      qa.history,
		TotalSignals: qa.totalSignals,
		LastComputed: qa.lastComputed,
	})
}

// UnmarshalJSON implements json.Unmarshaler for QualityAggregator.
func (qa *QualityAggregator) UnmarshalJSON(data []byte) error {
	var temp aggregatorJSON
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	qa.mu.Lock()
	defer qa.mu.Unlock()

	if temp.Config != nil {
		qa.config = temp.Config
	} else {
		qa.config = DefaultQualityAggregatorConfig()
	}

	qa.metrics = make(map[QualityMetricType]*MetricScore)
	for k, v := range temp.Metrics {
		metricType := ParseQualityMetricType(k)
		if metricType != MetricUnknown {
			qa.metrics[metricType] = v
		}
	}

	qa.history = temp.History
	if qa.history == nil {
		qa.history = make([]historicalScore, 0, qa.config.MaxHistorySize)
	}

	qa.totalSignals = temp.TotalSignals
	qa.lastComputed = temp.LastComputed
	if qa.lastComputed == nil {
		qa.lastComputed = NewQualityScore()
	}

	return nil
}

// MarshalJSON implements json.Marshaler for QualityMetricType.
func (mt QualityMetricType) MarshalJSON() ([]byte, error) {
	return json.Marshal(mt.String())
}

// UnmarshalJSON implements json.Unmarshaler for QualityMetricType.
func (mt *QualityMetricType) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*mt = ParseQualityMetricType(s)
	return nil
}
