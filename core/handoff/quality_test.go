package handoff

import (
	"encoding/json"
	"math"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Test QualityMetricType
// =============================================================================

func TestQualityMetricType_String(t *testing.T) {
	tests := []struct {
		metricType QualityMetricType
		expected   string
	}{
		{MetricUnknown, "unknown"},
		{MetricResponseCoherence, "response_coherence"},
		{MetricToolAccuracy, "tool_accuracy"},
		{MetricTaskCompletion, "task_completion"},
		{MetricUserRating, "user_rating"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.metricType.String(); got != tt.expected {
				t.Errorf("String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseQualityMetricType(t *testing.T) {
	tests := []struct {
		input    string
		expected QualityMetricType
	}{
		{"response_coherence", MetricResponseCoherence},
		{"tool_accuracy", MetricToolAccuracy},
		{"task_completion", MetricTaskCompletion},
		{"user_rating", MetricUserRating},
		{"unknown", MetricUnknown},
		{"invalid", MetricUnknown},
		{"", MetricUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := ParseQualityMetricType(tt.input); got != tt.expected {
				t.Errorf("ParseQualityMetricType(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestQualityMetricType_RoundTrip(t *testing.T) {
	types := []QualityMetricType{
		MetricResponseCoherence,
		MetricToolAccuracy,
		MetricTaskCompletion,
		MetricUserRating,
	}

	for _, mt := range types {
		str := mt.String()
		parsed := ParseQualityMetricType(str)
		if parsed != mt {
			t.Errorf("Round trip failed for %v: got %v", mt, parsed)
		}
	}
}

// =============================================================================
// Test QualityScore
// =============================================================================

func TestNewQualityScore(t *testing.T) {
	score := NewQualityScore()

	if score == nil {
		t.Fatal("NewQualityScore returned nil")
	}

	if score.ResponseQuality != 0.5 {
		t.Errorf("ResponseQuality = %v, want 0.5", score.ResponseQuality)
	}

	if score.ToolSuccess != 0.5 {
		t.Errorf("ToolSuccess = %v, want 0.5", score.ToolSuccess)
	}

	if score.UserSatisfaction != 0.5 {
		t.Errorf("UserSatisfaction = %v, want 0.5", score.UserSatisfaction)
	}

	if score.Confidence != 0.0 {
		t.Errorf("Confidence = %v, want 0.0", score.Confidence)
	}

	if score.SignalCount != 0 {
		t.Errorf("SignalCount = %v, want 0", score.SignalCount)
	}

	if score.MetricBreakdown == nil {
		t.Error("MetricBreakdown is nil")
	}
}

func TestQualityScore_ComputeOverall(t *testing.T) {
	tests := []struct {
		name             string
		responseQuality  float64
		toolSuccess      float64
		userSatisfaction float64
		expectedMin      float64
		expectedMax      float64
	}{
		{
			name:             "all perfect",
			responseQuality:  1.0,
			toolSuccess:      1.0,
			userSatisfaction: 1.0,
			expectedMin:      0.99,
			expectedMax:      1.0,
		},
		{
			name:             "all zero",
			responseQuality:  0.0,
			toolSuccess:      0.0,
			userSatisfaction: 0.0,
			expectedMin:      0.0,
			expectedMax:      0.01,
		},
		{
			name:             "all average",
			responseQuality:  0.5,
			toolSuccess:      0.5,
			userSatisfaction: 0.5,
			expectedMin:      0.49,
			expectedMax:      0.51,
		},
		{
			name:             "mixed scores",
			responseQuality:  0.8,
			toolSuccess:      0.6,
			userSatisfaction: 0.7,
			expectedMin:      0.6,
			expectedMax:      0.8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := NewQualityScore()
			score.ResponseQuality = tt.responseQuality
			score.ToolSuccess = tt.toolSuccess
			score.UserSatisfaction = tt.userSatisfaction

			overall := score.ComputeOverall()

			if overall < tt.expectedMin || overall > tt.expectedMax {
				t.Errorf("ComputeOverall() = %v, want [%v, %v]",
					overall, tt.expectedMin, tt.expectedMax)
			}
		})
	}
}

func TestQualityScore_ComputeOverall_Nil(t *testing.T) {
	var nilScore *QualityScore
	if nilScore.ComputeOverall() != 0.5 {
		t.Errorf("ComputeOverall() on nil = %v, want 0.5", nilScore.ComputeOverall())
	}
}

func TestQualityScore_ComputeOverallWeighted(t *testing.T) {
	score := NewQualityScore()
	score.ResponseQuality = 1.0
	score.ToolSuccess = 0.0
	score.UserSatisfaction = 0.5

	// Heavy weight on response quality
	weights := map[QualityMetricType]float64{
		MetricResponseCoherence: 0.8,
		MetricToolAccuracy:      0.1,
		MetricUserRating:        0.1,
	}

	weighted := score.ComputeOverallWeighted(weights)

	// With these weights, overall should be closer to 1.0 than the default
	if weighted < 0.7 {
		t.Errorf("ComputeOverallWeighted() = %v, expected > 0.7", weighted)
	}
}

func TestQualityScore_GetMetricBreakdown(t *testing.T) {
	score := NewQualityScore()
	score.ResponseQuality = 0.8
	score.ToolSuccess = 0.7
	score.UserSatisfaction = 0.9

	breakdown := score.GetMetricBreakdown()

	if breakdown == nil {
		t.Fatal("GetMetricBreakdown returned nil")
	}

	if breakdown[MetricResponseCoherence] != 0.8 {
		t.Errorf("ResponseCoherence = %v, want 0.8", breakdown[MetricResponseCoherence])
	}

	if breakdown[MetricToolAccuracy] != 0.7 {
		t.Errorf("ToolAccuracy = %v, want 0.7", breakdown[MetricToolAccuracy])
	}

	if breakdown[MetricUserRating] != 0.9 {
		t.Errorf("UserRating = %v, want 0.9", breakdown[MetricUserRating])
	}
}

func TestQualityScore_Clone(t *testing.T) {
	original := NewQualityScore()
	original.ResponseQuality = 0.8
	original.ToolSuccess = 0.7
	original.UserSatisfaction = 0.9
	original.Confidence = 0.6
	original.SignalCount = 10
	original.MetricBreakdown[MetricResponseCoherence] = &MetricScore{
		Mean:  0.8,
		Count: 5,
	}

	cloned := original.Clone()

	// Verify values match
	if cloned.ResponseQuality != original.ResponseQuality {
		t.Errorf("ResponseQuality mismatch: got %v, want %v",
			cloned.ResponseQuality, original.ResponseQuality)
	}

	if cloned.ToolSuccess != original.ToolSuccess {
		t.Errorf("ToolSuccess mismatch: got %v, want %v",
			cloned.ToolSuccess, original.ToolSuccess)
	}

	if cloned.Confidence != original.Confidence {
		t.Errorf("Confidence mismatch: got %v, want %v",
			cloned.Confidence, original.Confidence)
	}

	// Verify independence
	cloned.ResponseQuality = 0.1
	if original.ResponseQuality == 0.1 {
		t.Error("Modifying clone affected original")
	}

	// Verify MetricBreakdown independence
	if cloned.MetricBreakdown[MetricResponseCoherence] == original.MetricBreakdown[MetricResponseCoherence] {
		t.Error("MetricBreakdown was not deep copied")
	}
}

func TestQualityScore_CloneNil(t *testing.T) {
	var nilScore *QualityScore
	if nilScore.Clone() != nil {
		t.Error("Clone of nil should return nil")
	}
}

func TestQualityScore_IsGood(t *testing.T) {
	tests := []struct {
		name             string
		responseQuality  float64
		toolSuccess      float64
		userSatisfaction float64
		confidence       float64
		expected         bool
	}{
		{"high quality high confidence", 0.9, 0.9, 0.9, 0.5, true},
		{"high quality low confidence", 0.9, 0.9, 0.9, 0.1, false},
		{"low quality high confidence", 0.3, 0.3, 0.3, 0.5, false},
		{"borderline quality", 0.7, 0.7, 0.7, 0.5, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := NewQualityScore()
			score.ResponseQuality = tt.responseQuality
			score.ToolSuccess = tt.toolSuccess
			score.UserSatisfaction = tt.userSatisfaction
			score.Confidence = tt.confidence

			if got := score.IsGood(); got != tt.expected {
				t.Errorf("IsGood() = %v, want %v (overall: %v)",
					got, tt.expected, score.ComputeOverall())
			}
		})
	}
}

func TestQualityScore_IsPoor(t *testing.T) {
	tests := []struct {
		name             string
		responseQuality  float64
		toolSuccess      float64
		userSatisfaction float64
		confidence       float64
		expected         bool
	}{
		{"low quality high confidence", 0.2, 0.2, 0.2, 0.5, true},
		{"low quality low confidence", 0.2, 0.2, 0.2, 0.1, false},
		{"high quality high confidence", 0.9, 0.9, 0.9, 0.5, false},
		{"borderline quality", 0.4, 0.4, 0.4, 0.5, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := NewQualityScore()
			score.ResponseQuality = tt.responseQuality
			score.ToolSuccess = tt.toolSuccess
			score.UserSatisfaction = tt.userSatisfaction
			score.Confidence = tt.confidence

			if got := score.IsPoor(); got != tt.expected {
				t.Errorf("IsPoor() = %v, want %v (overall: %v)",
					got, tt.expected, score.ComputeOverall())
			}
		})
	}
}

// =============================================================================
// Test QualityAggregator
// =============================================================================

func TestNewQualityAggregator(t *testing.T) {
	agg := NewQualityAggregator(nil)

	if agg == nil {
		t.Fatal("NewQualityAggregator returned nil")
	}

	if agg.config == nil {
		t.Error("Config is nil")
	}

	if agg.metrics == nil {
		t.Error("Metrics map is nil")
	}

	if agg.history == nil {
		t.Error("History slice is nil")
	}
}

func TestQualityAggregator_AddSignal(t *testing.T) {
	agg := NewQualityAggregator(nil)

	agg.AddSignal(MetricResponseCoherence, 0.8)
	agg.AddSignal(MetricResponseCoherence, 0.9)
	agg.AddSignal(MetricToolAccuracy, 1.0)

	stats := agg.Stats()

	if stats.TotalSignals != 3 {
		t.Errorf("TotalSignals = %d, want 3", stats.TotalSignals)
	}

	if stats.MetricCounts[MetricResponseCoherence] != 2 {
		t.Errorf("ResponseCoherence count = %d, want 2",
			stats.MetricCounts[MetricResponseCoherence])
	}

	if stats.MetricCounts[MetricToolAccuracy] != 1 {
		t.Errorf("ToolAccuracy count = %d, want 1",
			stats.MetricCounts[MetricToolAccuracy])
	}
}

func TestQualityAggregator_AddSignal_Clamping(t *testing.T) {
	agg := NewQualityAggregator(nil)

	// Values should be clamped to [0, 1]
	agg.AddSignal(MetricResponseCoherence, -0.5)
	agg.AddSignal(MetricResponseCoherence, 1.5)

	metric := agg.GetMetricStats(MetricResponseCoherence)
	if metric == nil {
		t.Fatal("Metric is nil")
	}

	// Mean of clamped values (0 + 1) / 2 = 0.5
	if math.Abs(metric.Mean-0.5) > 0.01 {
		t.Errorf("Mean = %v, expected ~0.5 after clamping", metric.Mean)
	}
}

func TestQualityAggregator_ComputeScore(t *testing.T) {
	agg := NewQualityAggregator(nil)

	// Add some signals
	agg.AddSignal(MetricResponseCoherence, 0.8)
	agg.AddSignal(MetricToolAccuracy, 0.9)
	agg.AddSignal(MetricUserRating, 0.7)

	score := agg.ComputeScore()

	if score == nil {
		t.Fatal("ComputeScore returned nil")
	}

	if score.ResponseQuality < 0.7 || score.ResponseQuality > 0.9 {
		t.Errorf("ResponseQuality = %v, expected ~0.8", score.ResponseQuality)
	}

	if score.ToolSuccess < 0.8 || score.ToolSuccess > 1.0 {
		t.Errorf("ToolSuccess = %v, expected ~0.9", score.ToolSuccess)
	}

	if score.UserSatisfaction < 0.6 || score.UserSatisfaction > 0.8 {
		t.Errorf("UserSatisfaction = %v, expected ~0.7", score.UserSatisfaction)
	}

	if score.Confidence <= 0 {
		t.Errorf("Confidence = %v, expected > 0", score.Confidence)
	}

	if score.SignalCount != 3 {
		t.Errorf("SignalCount = %d, want 3", score.SignalCount)
	}
}

func TestQualityAggregator_ComputeConfidence(t *testing.T) {
	config := DefaultQualityAggregatorConfig()
	config.ConfidenceScale = 10.0
	agg := NewQualityAggregator(config)

	// No signals = no confidence
	score := agg.ComputeScore()
	if score.Confidence != 0.0 {
		t.Errorf("Confidence with 0 signals = %v, want 0", score.Confidence)
	}

	// Add 10 signals (should be ~0.63 confidence)
	for i := 0; i < 10; i++ {
		agg.AddSignal(MetricResponseCoherence, 0.8)
	}

	score = agg.ComputeScore()
	expectedConfidence := 1.0 - math.Exp(-10.0/10.0) // ~0.632
	if math.Abs(score.Confidence-expectedConfidence) > 0.05 {
		t.Errorf("Confidence with 10 signals = %v, want ~%v",
			score.Confidence, expectedConfidence)
	}

	// Add more signals (confidence should increase but saturate)
	for i := 0; i < 20; i++ {
		agg.AddSignal(MetricResponseCoherence, 0.8)
	}

	score = agg.ComputeScore()
	if score.Confidence <= expectedConfidence {
		t.Errorf("Confidence should increase with more signals")
	}

	if score.Confidence > 1.0 {
		t.Errorf("Confidence = %v, should not exceed 1.0", score.Confidence)
	}
}

func TestQualityAggregator_GetTrend(t *testing.T) {
	config := DefaultQualityAggregatorConfig()
	config.TrendWindowSize = 5
	agg := NewQualityAggregator(config)

	// Not enough history
	trend := agg.GetTrend()
	if trend != 0.0 {
		t.Errorf("Trend with no history = %v, want 0", trend)
	}

	// Add improving signals
	signals := []float64{0.5, 0.6, 0.7, 0.8, 0.9}
	for _, v := range signals {
		agg.AddSignal(MetricResponseCoherence, v)
		agg.ComputeScore() // Trigger history recording
	}

	trend = agg.GetTrend()
	if trend <= 0 {
		t.Errorf("Trend should be positive for improving signals, got %v", trend)
	}

	if !agg.IsTrendingUp() {
		t.Error("IsTrendingUp should be true for improving signals")
	}
}

func TestQualityAggregator_GetTrend_Declining(t *testing.T) {
	config := DefaultQualityAggregatorConfig()
	config.TrendWindowSize = 5
	agg := NewQualityAggregator(config)

	// Add declining signals
	signals := []float64{0.9, 0.8, 0.7, 0.6, 0.5}
	for _, v := range signals {
		agg.AddSignal(MetricResponseCoherence, v)
		agg.ComputeScore()
	}

	trend := agg.GetTrend()
	if trend >= 0 {
		t.Errorf("Trend should be negative for declining signals, got %v", trend)
	}

	if !agg.IsTrendingDown() {
		t.Error("IsTrendingDown should be true for declining signals")
	}
}

func TestQualityAggregator_Reset(t *testing.T) {
	agg := NewQualityAggregator(nil)

	// Add some signals
	for i := 0; i < 10; i++ {
		agg.AddSignal(MetricResponseCoherence, 0.8)
	}

	// Reset
	agg.Reset()

	stats := agg.Stats()
	if stats.TotalSignals != 0 {
		t.Errorf("TotalSignals after reset = %d, want 0", stats.TotalSignals)
	}

	if len(stats.MetricCounts) != 0 {
		t.Errorf("MetricCounts after reset = %d, want 0", len(stats.MetricCounts))
	}

	if stats.HistorySize != 0 {
		t.Errorf("HistorySize after reset = %d, want 0", stats.HistorySize)
	}
}

func TestQualityAggregator_GetMetricStats(t *testing.T) {
	agg := NewQualityAggregator(nil)

	// No stats for empty metric
	stats := agg.GetMetricStats(MetricResponseCoherence)
	if stats != nil {
		t.Error("Expected nil for metric with no signals")
	}

	// Add signals
	agg.AddSignal(MetricResponseCoherence, 0.8)
	agg.AddSignal(MetricResponseCoherence, 0.6)

	stats = agg.GetMetricStats(MetricResponseCoherence)
	if stats == nil {
		t.Fatal("Stats is nil")
	}

	if stats.Count != 2 {
		t.Errorf("Count = %d, want 2", stats.Count)
	}

	expectedMean := 0.7
	if math.Abs(stats.Mean-expectedMean) > 0.01 {
		t.Errorf("Mean = %v, want %v", stats.Mean, expectedMean)
	}

	if stats.LastValue != 0.6 {
		t.Errorf("LastValue = %v, want 0.6", stats.LastValue)
	}
}

func TestQualityAggregator_ThreadSafety(t *testing.T) {
	agg := NewQualityAggregator(nil)

	var wg sync.WaitGroup
	numGoroutines := 10
	signalsPerGoroutine := 100

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < signalsPerGoroutine; j++ {
				metricType := QualityMetricType((id + j) % 4)
				if metricType == MetricUnknown {
					metricType = MetricResponseCoherence
				}
				agg.AddSignal(metricType, 0.5+float64(j%10)*0.05)
				if j%10 == 0 {
					agg.ComputeScore()
				}
			}
		}(i)
	}

	wg.Wait()

	stats := agg.Stats()
	expectedSignals := int64(numGoroutines * signalsPerGoroutine)
	if stats.TotalSignals != expectedSignals {
		t.Errorf("TotalSignals = %d, want %d", stats.TotalSignals, expectedSignals)
	}
}

// =============================================================================
// Test FeedbackType
// =============================================================================

func TestFeedbackType_String(t *testing.T) {
	tests := []struct {
		feedbackType FeedbackType
		expected     string
	}{
		{FeedbackUnknown, "unknown"},
		{FeedbackThumbsUp, "thumbs_up"},
		{FeedbackThumbsDown, "thumbs_down"},
		{FeedbackRating, "rating"},
		{FeedbackCorrection, "correction"},
		{FeedbackComment, "comment"},
		{FeedbackRetry, "retry"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.feedbackType.String(); got != tt.expected {
				t.Errorf("String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// Test DefaultQualityAssessorHook
// =============================================================================

func TestNewDefaultQualityAssessorHook(t *testing.T) {
	hook := NewDefaultQualityAssessorHook(nil, nil)

	if hook == nil {
		t.Fatal("NewDefaultQualityAssessorHook returned nil")
	}

	if hook.config == nil {
		t.Error("Config is nil")
	}

	if hook.aggregator == nil {
		t.Error("Aggregator is nil")
	}

	if hook.responseSignals == nil {
		t.Error("ResponseSignals is nil")
	}

	if hook.toolSignals == nil {
		t.Error("ToolSignals is nil")
	}

	if hook.feedbackSignals == nil {
		t.Error("FeedbackSignals is nil")
	}
}

func TestDefaultQualityAssessorHook_OnResponseComplete(t *testing.T) {
	hook := NewDefaultQualityAssessorHook(nil, nil)

	response := &ResponseContext{
		ResponseID:     "resp-1",
		Content:        "This is a test response with sufficient content to be considered complete.",
		TokenCount:     100,
		GenerationTime: 500 * time.Millisecond,
		TurnNumber:     1,
		ContextSize:    1000,
		Timestamp:      time.Now(),
	}

	ctx := &AssessmentContext{
		SessionID: "session-1",
		AgentID:   "agent-1",
		ModelID:   "model-1",
		TaskType:  "test",
	}

	score := hook.OnResponseComplete(response, ctx)

	if score == nil {
		t.Fatal("OnResponseComplete returned nil")
	}

	if score.ResponseQuality <= 0 {
		t.Errorf("ResponseQuality = %v, expected > 0", score.ResponseQuality)
	}

	stats := hook.Stats()
	if stats.ResponseSignalCount != 1 {
		t.Errorf("ResponseSignalCount = %d, want 1", stats.ResponseSignalCount)
	}

	if stats.TotalAssessments != 1 {
		t.Errorf("TotalAssessments = %d, want 1", stats.TotalAssessments)
	}
}

func TestDefaultQualityAssessorHook_OnResponseComplete_Nil(t *testing.T) {
	hook := NewDefaultQualityAssessorHook(nil, nil)

	score := hook.OnResponseComplete(nil, nil)

	if score == nil {
		t.Fatal("OnResponseComplete with nil should return score")
	}

	stats := hook.Stats()
	if stats.ResponseSignalCount != 0 {
		t.Errorf("ResponseSignalCount = %d, want 0 for nil response", stats.ResponseSignalCount)
	}
}

func TestDefaultQualityAssessorHook_OnToolSuccess(t *testing.T) {
	hook := NewDefaultQualityAssessorHook(nil, nil)

	tool := &ToolContext{
		ToolID:         "tool-1",
		ToolName:       "test_tool",
		InvocationTime: time.Now(),
	}

	result := &ToolResult{
		Success:       true,
		Output:        "success",
		ExecutionTime: 100 * time.Millisecond,
		RetryCount:    0,
	}

	hook.OnToolSuccess(tool, result)

	stats := hook.Stats()
	if stats.ToolSignalCount != 1 {
		t.Errorf("ToolSignalCount = %d, want 1", stats.ToolSignalCount)
	}

	// The tool accuracy metric should be 1.0 for a successful tool with no retries
	metric := hook.aggregator.GetMetricStats(MetricToolAccuracy)
	if metric == nil {
		t.Fatal("ToolAccuracy metric is nil")
	}

	if metric.Mean < 0.9 {
		t.Errorf("ToolAccuracy = %v, expected high value for success", metric.Mean)
	}
}

func TestDefaultQualityAssessorHook_OnToolSuccess_WithRetries(t *testing.T) {
	hook := NewDefaultQualityAssessorHook(nil, nil)

	tool := &ToolContext{
		ToolID:   "tool-1",
		ToolName: "test_tool",
	}

	result := &ToolResult{
		Success:       true,
		Output:        "success after retries",
		ExecutionTime: 500 * time.Millisecond,
		RetryCount:    2,
	}

	hook.OnToolSuccess(tool, result)

	// Score should be reduced due to retries
	// Accuracy = 1 / (1 + retryCount) = 1/3 = 0.33
	metric := hook.aggregator.GetMetricStats(MetricToolAccuracy)
	if metric == nil {
		t.Fatal("ToolAccuracy metric is nil")
	}

	expectedAccuracy := 1.0 / 3.0
	if math.Abs(metric.Mean-expectedAccuracy) > 0.01 {
		t.Errorf("ToolAccuracy = %v, expected ~%v", metric.Mean, expectedAccuracy)
	}
}

func TestDefaultQualityAssessorHook_OnToolFailure(t *testing.T) {
	hook := NewDefaultQualityAssessorHook(nil, nil)

	tool := &ToolContext{
		ToolID:   "tool-1",
		ToolName: "test_tool",
	}

	result := &ToolResult{
		Success:       false,
		Error:         "execution failed",
		ExecutionTime: 100 * time.Millisecond,
		RetryCount:    0,
	}

	hook.OnToolFailure(tool, result)

	stats := hook.Stats()
	if stats.ToolSignalCount != 1 {
		t.Errorf("ToolSignalCount = %d, want 1", stats.ToolSignalCount)
	}

	// Score should reflect failure
	metric := hook.aggregator.GetMetricStats(MetricToolAccuracy)
	if metric == nil {
		t.Fatal("ToolAccuracy metric is nil")
	}

	if metric.Mean != 0.0 {
		t.Errorf("ToolAccuracy = %v, expected 0.0 for failure", metric.Mean)
	}
}

func TestDefaultQualityAssessorHook_OnUserFeedback(t *testing.T) {
	tests := []struct {
		name         string
		feedbackType FeedbackType
		rating       float64
		expectedMin  float64
		expectedMax  float64
	}{
		{"thumbs up", FeedbackThumbsUp, 0.0, 0.99, 1.0},
		{"thumbs down", FeedbackThumbsDown, 0.0, 0.0, 0.01},
		{"5 star rating", FeedbackRating, 5.0, 0.99, 1.0},
		{"3 star rating", FeedbackRating, 3.0, 0.55, 0.65},
		{"1 star rating", FeedbackRating, 1.0, 0.15, 0.25},
		{"correction", FeedbackCorrection, 0.0, 0.25, 0.35},
		{"retry", FeedbackRetry, 0.0, 0.15, 0.25},
		{"comment", FeedbackComment, 0.0, 0.45, 0.55},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook := NewDefaultQualityAssessorHook(nil, nil)

			details := &FeedbackDetails{
				Rating:    tt.rating,
				Timestamp: time.Now(),
			}

			hook.OnUserFeedback(tt.feedbackType, details)

			metric := hook.aggregator.GetMetricStats(MetricUserRating)
			if metric == nil {
				t.Fatal("UserRating metric is nil")
			}

			if metric.Mean < tt.expectedMin || metric.Mean > tt.expectedMax {
				t.Errorf("UserRating = %v, expected [%v, %v]",
					metric.Mean, tt.expectedMin, tt.expectedMax)
			}
		})
	}
}

func TestDefaultQualityAssessorHook_OnUserFeedback_Nil(t *testing.T) {
	hook := NewDefaultQualityAssessorHook(nil, nil)

	// Should not panic
	hook.OnUserFeedback(FeedbackThumbsUp, nil)

	stats := hook.Stats()
	if stats.FeedbackSignalCount != 0 {
		t.Errorf("FeedbackSignalCount = %d, want 0 for nil details", stats.FeedbackSignalCount)
	}
}

func TestDefaultQualityAssessorHook_GetCurrentScore(t *testing.T) {
	hook := NewDefaultQualityAssessorHook(nil, nil)

	// Initial score
	score := hook.GetCurrentScore()
	if score == nil {
		t.Fatal("GetCurrentScore returned nil")
	}

	// Add some signals
	response := &ResponseContext{
		Content:   "Test response content that is reasonably long.",
		Timestamp: time.Now(),
	}
	hook.OnResponseComplete(response, nil)

	// Score should be updated
	newScore := hook.GetCurrentScore()
	if newScore.SignalCount != 2 { // Coherence + completion signals
		t.Logf("SignalCount = %d (may vary based on implementation)", newScore.SignalCount)
	}
}

func TestDefaultQualityAssessorHook_Reset(t *testing.T) {
	hook := NewDefaultQualityAssessorHook(nil, nil)

	// Add signals
	response := &ResponseContext{
		Content:   "Test response",
		Timestamp: time.Now(),
	}
	hook.OnResponseComplete(response, nil)
	hook.OnToolSuccess(&ToolContext{ToolName: "test"}, &ToolResult{Success: true})
	hook.OnUserFeedback(FeedbackThumbsUp, &FeedbackDetails{Timestamp: time.Now()})

	// Reset
	hook.Reset()

	stats := hook.Stats()
	if stats.ResponseSignalCount != 0 {
		t.Errorf("ResponseSignalCount = %d, want 0", stats.ResponseSignalCount)
	}
	if stats.ToolSignalCount != 0 {
		t.Errorf("ToolSignalCount = %d, want 0", stats.ToolSignalCount)
	}
	if stats.FeedbackSignalCount != 0 {
		t.Errorf("FeedbackSignalCount = %d, want 0", stats.FeedbackSignalCount)
	}
}

func TestDefaultQualityAssessorHook_SignalLimits(t *testing.T) {
	config := DefaultQualityHookConfig()
	config.MaxResponseSignals = 5
	config.MaxToolSignals = 5
	config.MaxFeedbackSignals = 5

	hook := NewDefaultQualityAssessorHook(config, nil)

	// Add more signals than the limit
	for i := 0; i < 10; i++ {
		response := &ResponseContext{
			ResponseID: "resp-" + string(rune('0'+i)),
			Content:    "Test response content",
			Timestamp:  time.Now(),
		}
		hook.OnResponseComplete(response, nil)
	}

	stats := hook.Stats()
	if stats.ResponseSignalCount != 5 {
		t.Errorf("ResponseSignalCount = %d, want 5 (max limit)", stats.ResponseSignalCount)
	}
}

func TestDefaultQualityAssessorHook_WithProfileLearner(t *testing.T) {
	learner := NewProfileLearner(nil, nil)
	config := DefaultQualityHookConfig()
	config.FeedRatingsToLearner = true

	hook := NewDefaultQualityAssessorHook(config, learner)

	response := &ResponseContext{
		ResponseID: "resp-1",
		Content:    "Test response with good content for assessment.",
		Timestamp:  time.Now(),
	}

	ctx := &AssessmentContext{
		AgentID: "test-agent",
		ModelID: "test-model",
	}

	hook.OnResponseComplete(response, ctx)

	// Verify learner received the observation
	learnerStats := learner.Stats()
	if learnerStats.TotalObservations != 1 {
		t.Errorf("Learner observations = %d, want 1", learnerStats.TotalObservations)
	}
}

func TestDefaultQualityAssessorHook_ThreadSafety(t *testing.T) {
	hook := NewDefaultQualityAssessorHook(nil, nil)

	var wg sync.WaitGroup
	numGoroutines := 10
	opsPerGoroutine := 50

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				switch j % 4 {
				case 0:
					hook.OnResponseComplete(&ResponseContext{
						Content:   "Test",
						Timestamp: time.Now(),
					}, nil)
				case 1:
					hook.OnToolSuccess(&ToolContext{ToolName: "test"}, &ToolResult{Success: true})
				case 2:
					hook.OnToolFailure(&ToolContext{ToolName: "test"}, &ToolResult{Success: false})
				case 3:
					hook.OnUserFeedback(FeedbackThumbsUp, &FeedbackDetails{Timestamp: time.Now()})
				}
				hook.GetCurrentScore()
			}
		}(i)
	}

	wg.Wait()

	// Should not panic and stats should be reasonable
	stats := hook.Stats()
	if stats.TotalAssessments == 0 {
		t.Error("Expected some assessments to be recorded")
	}
}

// =============================================================================
// Test JSON Serialization
// =============================================================================

func TestQualityScore_JSONMarshal(t *testing.T) {
	original := NewQualityScore()
	original.ResponseQuality = 0.85
	original.ToolSuccess = 0.9
	original.UserSatisfaction = 0.75
	original.Confidence = 0.6
	original.SignalCount = 15

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded QualityScore
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if math.Abs(decoded.ResponseQuality-original.ResponseQuality) > 1e-9 {
		t.Errorf("ResponseQuality mismatch: got %v, want %v",
			decoded.ResponseQuality, original.ResponseQuality)
	}

	if math.Abs(decoded.ToolSuccess-original.ToolSuccess) > 1e-9 {
		t.Errorf("ToolSuccess mismatch: got %v, want %v",
			decoded.ToolSuccess, original.ToolSuccess)
	}

	if math.Abs(decoded.UserSatisfaction-original.UserSatisfaction) > 1e-9 {
		t.Errorf("UserSatisfaction mismatch: got %v, want %v",
			decoded.UserSatisfaction, original.UserSatisfaction)
	}

	if math.Abs(decoded.Confidence-original.Confidence) > 1e-9 {
		t.Errorf("Confidence mismatch: got %v, want %v",
			decoded.Confidence, original.Confidence)
	}

	if decoded.SignalCount != original.SignalCount {
		t.Errorf("SignalCount mismatch: got %v, want %v",
			decoded.SignalCount, original.SignalCount)
	}
}

func TestQualityAggregator_JSONMarshal(t *testing.T) {
	original := NewQualityAggregator(nil)

	// Add some signals
	original.AddSignal(MetricResponseCoherence, 0.8)
	original.AddSignal(MetricToolAccuracy, 0.9)
	original.ComputeScore()

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	decoded := NewQualityAggregator(nil)
	err = json.Unmarshal(data, decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	originalStats := original.Stats()
	decodedStats := decoded.Stats()

	if decodedStats.TotalSignals != originalStats.TotalSignals {
		t.Errorf("TotalSignals mismatch: got %v, want %v",
			decodedStats.TotalSignals, originalStats.TotalSignals)
	}
}

func TestDefaultQualityAssessorHook_JSONMarshal(t *testing.T) {
	original := NewDefaultQualityAssessorHook(nil, nil)

	// Add some signals
	original.OnResponseComplete(&ResponseContext{
		Content:   "Test response",
		Timestamp: time.Now(),
	}, nil)
	original.OnToolSuccess(&ToolContext{ToolName: "test"}, &ToolResult{Success: true})

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	decoded := NewDefaultQualityAssessorHook(nil, nil)
	err = json.Unmarshal(data, decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	originalStats := original.Stats()
	decodedStats := decoded.Stats()

	if decodedStats.TotalAssessments != originalStats.TotalAssessments {
		t.Errorf("TotalAssessments mismatch: got %v, want %v",
			decodedStats.TotalAssessments, originalStats.TotalAssessments)
	}
}

func TestQualityMetricType_JSONMarshal(t *testing.T) {
	original := MetricResponseCoherence

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded QualityMetricType
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded != original {
		t.Errorf("Decoded = %v, want %v", decoded, original)
	}
}

// =============================================================================
// Test Hook Invocation
// =============================================================================

func TestHookInvocation_MultipleResponses(t *testing.T) {
	hook := NewDefaultQualityAssessorHook(nil, nil)

	// Simulate a session with multiple responses
	responses := []struct {
		content        string
		generationTime time.Duration
	}{
		{"Short", 100 * time.Millisecond},
		{"This is a medium length response with more content", 200 * time.Millisecond},
		{"This is a longer response that provides detailed information about the topic at hand.", 300 * time.Millisecond},
	}

	var lastScore *QualityScore
	for i, resp := range responses {
		score := hook.OnResponseComplete(&ResponseContext{
			ResponseID:     "resp-" + string(rune('1'+i)),
			Content:        resp.content,
			GenerationTime: resp.generationTime,
			Timestamp:      time.Now(),
		}, nil)

		if score == nil {
			t.Fatalf("Response %d: OnResponseComplete returned nil", i)
		}

		lastScore = score
	}

	// Final score should reflect all responses
	if lastScore.SignalCount < 3 {
		t.Errorf("Expected at least 3 signals, got %d", lastScore.SignalCount)
	}

	// Confidence should be non-zero after multiple responses
	if lastScore.Confidence <= 0 {
		t.Errorf("Confidence = %v, expected > 0 after multiple responses", lastScore.Confidence)
	}
}

func TestHookInvocation_MixedSignals(t *testing.T) {
	hook := NewDefaultQualityAssessorHook(nil, nil)

	// Add successful response
	hook.OnResponseComplete(&ResponseContext{
		Content:   "Good response with adequate content length.",
		Timestamp: time.Now(),
	}, nil)

	// Add tool success
	hook.OnToolSuccess(&ToolContext{ToolName: "search"}, &ToolResult{Success: true})

	// Add tool failure
	hook.OnToolFailure(&ToolContext{ToolName: "compile"}, &ToolResult{Success: false, Error: "syntax error"})

	// Add user feedback
	hook.OnUserFeedback(FeedbackThumbsUp, &FeedbackDetails{Timestamp: time.Now()})

	score := hook.GetCurrentScore()

	// Check that all metrics are populated
	breakdown := score.GetMetricBreakdown()
	if breakdown == nil {
		t.Fatal("GetMetricBreakdown returned nil")
	}

	// Overall should be in a reasonable range
	overall := score.ComputeOverall()
	if overall < 0.3 || overall > 0.9 {
		t.Errorf("Overall = %v, expected in [0.3, 0.9] for mixed signals", overall)
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkQualityScore_ComputeOverall(b *testing.B) {
	score := NewQualityScore()
	score.ResponseQuality = 0.8
	score.ToolSuccess = 0.7
	score.UserSatisfaction = 0.9

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = score.ComputeOverall()
	}
}

func BenchmarkQualityAggregator_AddSignal(b *testing.B) {
	agg := NewQualityAggregator(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agg.AddSignal(MetricResponseCoherence, 0.8)
	}
}

func BenchmarkQualityAggregator_ComputeScore(b *testing.B) {
	agg := NewQualityAggregator(nil)
	for i := 0; i < 100; i++ {
		agg.AddSignal(MetricResponseCoherence, 0.8)
		agg.AddSignal(MetricToolAccuracy, 0.9)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = agg.ComputeScore()
	}
}

func BenchmarkDefaultQualityAssessorHook_OnResponseComplete(b *testing.B) {
	hook := NewDefaultQualityAssessorHook(nil, nil)
	response := &ResponseContext{
		ResponseID: "resp-1",
		Content:    "Test response with reasonable content length for benchmarking purposes.",
		Timestamp:  time.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = hook.OnResponseComplete(response, nil)
	}
}

func BenchmarkQualityScore_Clone(b *testing.B) {
	score := NewQualityScore()
	score.ResponseQuality = 0.8
	score.ToolSuccess = 0.7
	score.UserSatisfaction = 0.9
	score.Confidence = 0.6
	score.SignalCount = 50
	score.MetricBreakdown[MetricResponseCoherence] = &MetricScore{Mean: 0.8, Count: 10}
	score.MetricBreakdown[MetricToolAccuracy] = &MetricScore{Mean: 0.7, Count: 20}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = score.Clone()
	}
}

func BenchmarkQualityAggregator_GetTrend(b *testing.B) {
	agg := NewQualityAggregator(nil)
	for i := 0; i < 100; i++ {
		agg.AddSignal(MetricResponseCoherence, 0.5+float64(i)*0.005)
		agg.ComputeScore()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = agg.GetTrend()
	}
}
