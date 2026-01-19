package handoff

import (
	"encoding/json"
	"sync"
	"time"
)

// =============================================================================
// HO.12.1 QualityAssessorHook - Hook for Quality Assessment
// =============================================================================
//
// QualityAssessorHook provides a hook interface for assessing quality signals
// from various sources: response completion, tool execution, and user feedback.
// It aggregates these signals into a unified QualityScore that can be used
// by the ProfileLearner to improve handoff decisions.
//
// Key features:
//   - OnResponseComplete: Assess quality after response generation
//   - OnToolSuccess/OnToolFailure: Track tool execution outcomes
//   - OnUserFeedback: Incorporate explicit user feedback
//   - Aggregates signals into QualityScore
//   - Feeds into ProfileLearner for adaptive learning
//
// Thread Safety:
//   - All operations are protected by appropriate synchronization
//   - Safe for concurrent use from multiple goroutines

// QualityAssessorHook defines the interface for quality assessment hooks.
// Implementations of this interface can be registered with the system to
// receive quality signals and aggregate them into quality scores.
type QualityAssessorHook interface {
	// OnResponseComplete is called when a response generation completes.
	// It assesses the quality of the response in the given context.
	// Returns a QualityScore representing the assessed quality.
	OnResponseComplete(response *ResponseContext, ctx *AssessmentContext) *QualityScore

	// OnToolSuccess is called when a tool execution succeeds.
	// It updates internal metrics based on the successful tool result.
	OnToolSuccess(tool *ToolContext, result *ToolResult)

	// OnToolFailure is called when a tool execution fails.
	// It updates internal metrics based on the failed tool result.
	OnToolFailure(tool *ToolContext, result *ToolResult)

	// OnUserFeedback is called when explicit user feedback is received.
	// feedbackType indicates the type of feedback (e.g., thumbs up/down, rating).
	// details contains additional feedback information.
	OnUserFeedback(feedbackType FeedbackType, details *FeedbackDetails)

	// GetCurrentScore returns the current aggregated quality score.
	GetCurrentScore() *QualityScore

	// Reset clears all accumulated signals and resets to initial state.
	Reset()
}

// ResponseContext contains context about a generated response.
type ResponseContext struct {
	// ResponseID uniquely identifies this response.
	ResponseID string `json:"response_id"`

	// Content is the actual response content.
	Content string `json:"content"`

	// TokenCount is the number of tokens in the response.
	TokenCount int `json:"token_count"`

	// GenerationTime is how long it took to generate the response.
	GenerationTime time.Duration `json:"generation_time"`

	// TurnNumber is the conversation turn number.
	TurnNumber int `json:"turn_number"`

	// ContextSize is the size of the context used for generation.
	ContextSize int `json:"context_size"`

	// Timestamp is when the response was generated.
	Timestamp time.Time `json:"timestamp"`
}

// AssessmentContext provides additional context for quality assessment.
type AssessmentContext struct {
	// SessionID identifies the current session.
	SessionID string `json:"session_id"`

	// AgentID identifies the agent that generated the response.
	AgentID string `json:"agent_id"`

	// ModelID identifies the model used for generation.
	ModelID string `json:"model_id"`

	// TaskType describes the type of task being performed.
	TaskType string `json:"task_type"`

	// ExpectedOutcome describes what was expected (if known).
	ExpectedOutcome string `json:"expected_outcome,omitempty"`

	// Metadata contains additional assessment metadata.
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// ToolContext contains context about a tool execution.
type ToolContext struct {
	// ToolID uniquely identifies the tool.
	ToolID string `json:"tool_id"`

	// ToolName is the name of the tool.
	ToolName string `json:"tool_name"`

	// InputParams are the parameters passed to the tool.
	InputParams map[string]interface{} `json:"input_params,omitempty"`

	// InvocationTime is when the tool was invoked.
	InvocationTime time.Time `json:"invocation_time"`
}

// ToolResult contains the result of a tool execution.
type ToolResult struct {
	// Success indicates whether the tool execution succeeded.
	Success bool `json:"success"`

	// Output is the tool output (if successful).
	Output interface{} `json:"output,omitempty"`

	// Error is the error message (if failed).
	Error string `json:"error,omitempty"`

	// ExecutionTime is how long the tool took to execute.
	ExecutionTime time.Duration `json:"execution_time"`

	// RetryCount is how many times the tool was retried.
	RetryCount int `json:"retry_count"`
}

// FeedbackType represents the type of user feedback.
type FeedbackType int

const (
	// FeedbackUnknown represents unknown feedback type.
	FeedbackUnknown FeedbackType = iota

	// FeedbackThumbsUp represents positive feedback.
	FeedbackThumbsUp

	// FeedbackThumbsDown represents negative feedback.
	FeedbackThumbsDown

	// FeedbackRating represents a numeric rating (e.g., 1-5 stars).
	FeedbackRating

	// FeedbackCorrection represents a user correction.
	FeedbackCorrection

	// FeedbackComment represents a text comment.
	FeedbackComment

	// FeedbackRetry represents user requesting a retry.
	FeedbackRetry
)

// String returns the string representation of the feedback type.
func (ft FeedbackType) String() string {
	switch ft {
	case FeedbackThumbsUp:
		return "thumbs_up"
	case FeedbackThumbsDown:
		return "thumbs_down"
	case FeedbackRating:
		return "rating"
	case FeedbackCorrection:
		return "correction"
	case FeedbackComment:
		return "comment"
	case FeedbackRetry:
		return "retry"
	default:
		return "unknown"
	}
}

// FeedbackDetails contains details about user feedback.
type FeedbackDetails struct {
	// Rating is the numeric rating (if applicable, typically 1-5).
	Rating float64 `json:"rating,omitempty"`

	// Comment is the text comment (if applicable).
	Comment string `json:"comment,omitempty"`

	// Correction is the corrected content (if applicable).
	Correction string `json:"correction,omitempty"`

	// ResponseID is the response this feedback is about.
	ResponseID string `json:"response_id,omitempty"`

	// Timestamp is when the feedback was provided.
	Timestamp time.Time `json:"timestamp"`
}

// =============================================================================
// DefaultQualityAssessorHook Implementation
// =============================================================================

// DefaultQualityAssessorHook is the default implementation of QualityAssessorHook.
// It aggregates signals from multiple sources and computes quality scores
// using configurable weights.
type DefaultQualityAssessorHook struct {
	mu sync.RWMutex

	// config holds the hook configuration.
	config *QualityHookConfig

	// aggregator combines multiple signals into a score.
	aggregator *QualityAggregator

	// responseSignals stores recent response quality signals.
	responseSignals []responseSignal

	// toolSignals stores recent tool execution signals.
	toolSignals []toolSignal

	// feedbackSignals stores recent user feedback signals.
	feedbackSignals []feedbackSignal

	// profileLearner is the learner to feed quality scores into.
	profileLearner *ProfileLearner

	// currentScore caches the current quality score.
	currentScore *QualityScore

	// lastAssessment is when the last assessment was performed.
	lastAssessment time.Time

	// totalAssessments counts total assessments performed.
	totalAssessments int64
}

// responseSignal captures a response quality signal.
type responseSignal struct {
	ResponseID    string
	Coherence     float64
	Completeness  float64
	Timestamp     time.Time
	TurnNumber    int
	ContextSize   int
	GenerationMs  int64
}

// toolSignal captures a tool execution signal.
type toolSignal struct {
	ToolName      string
	Success       bool
	ExecutionMs   int64
	RetryCount    int
	Timestamp     time.Time
}

// feedbackSignal captures a user feedback signal.
type feedbackSignal struct {
	FeedbackType  FeedbackType
	Rating        float64
	Timestamp     time.Time
}

// QualityHookConfig configures the DefaultQualityAssessorHook.
type QualityHookConfig struct {
	// MaxResponseSignals is the maximum number of response signals to retain.
	MaxResponseSignals int `json:"max_response_signals"`

	// MaxToolSignals is the maximum number of tool signals to retain.
	MaxToolSignals int `json:"max_tool_signals"`

	// MaxFeedbackSignals is the maximum number of feedback signals to retain.
	MaxFeedbackSignals int `json:"max_feedback_signals"`

	// SignalDecayHours is how long signals remain fully weighted (in hours).
	SignalDecayHours float64 `json:"signal_decay_hours"`

	// MinCoherence is the minimum coherence threshold for "good" responses.
	MinCoherence float64 `json:"min_coherence"`

	// FeedRatingsToLearner enables feeding ratings to the profile learner.
	FeedRatingsToLearner bool `json:"feed_ratings_to_learner"`

	// AggregatorConfig configures the quality aggregator.
	AggregatorConfig *QualityAggregatorConfig `json:"aggregator_config,omitempty"`
}

// DefaultQualityHookConfig returns sensible defaults for the quality hook.
func DefaultQualityHookConfig() *QualityHookConfig {
	return &QualityHookConfig{
		MaxResponseSignals:   100,
		MaxToolSignals:       200,
		MaxFeedbackSignals:   50,
		SignalDecayHours:     24.0,
		MinCoherence:         0.6,
		FeedRatingsToLearner: true,
		AggregatorConfig:     DefaultQualityAggregatorConfig(),
	}
}

// NewDefaultQualityAssessorHook creates a new DefaultQualityAssessorHook.
// If config is nil, uses default configuration.
// If profileLearner is nil, quality scores won't be fed to learning.
func NewDefaultQualityAssessorHook(config *QualityHookConfig, profileLearner *ProfileLearner) *DefaultQualityAssessorHook {
	if config == nil {
		config = DefaultQualityHookConfig()
	}

	aggConfig := config.AggregatorConfig
	if aggConfig == nil {
		aggConfig = DefaultQualityAggregatorConfig()
	}

	return &DefaultQualityAssessorHook{
		config:          config,
		aggregator:      NewQualityAggregator(aggConfig),
		responseSignals: make([]responseSignal, 0, config.MaxResponseSignals),
		toolSignals:     make([]toolSignal, 0, config.MaxToolSignals),
		feedbackSignals: make([]feedbackSignal, 0, config.MaxFeedbackSignals),
		profileLearner:  profileLearner,
		currentScore:    NewQualityScore(),
	}
}

// OnResponseComplete implements QualityAssessorHook.
func (h *DefaultQualityAssessorHook) OnResponseComplete(response *ResponseContext, ctx *AssessmentContext) *QualityScore {
	if response == nil {
		return h.GetCurrentScore()
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Assess coherence based on response characteristics
	coherence := h.assessCoherence(response)
	completeness := h.assessCompleteness(response)

	// Create and store signal
	signal := responseSignal{
		ResponseID:   response.ResponseID,
		Coherence:    coherence,
		Completeness: completeness,
		Timestamp:    response.Timestamp,
		TurnNumber:   response.TurnNumber,
		ContextSize:  response.ContextSize,
		GenerationMs: response.GenerationTime.Milliseconds(),
	}

	h.responseSignals = h.appendResponseSignal(h.responseSignals, signal)

	// Update aggregator
	h.aggregator.AddSignal(MetricResponseCoherence, coherence)
	h.aggregator.AddSignal(MetricTaskCompletion, completeness)

	// Recompute score
	h.currentScore = h.aggregator.ComputeScore()
	h.lastAssessment = time.Now()
	h.totalAssessments++

	// Feed to learner if configured
	if h.config.FeedRatingsToLearner && h.profileLearner != nil && ctx != nil {
		h.feedToLearner(ctx.AgentID, ctx.ModelID, h.currentScore)
	}

	return h.currentScore.Clone()
}

// OnToolSuccess implements QualityAssessorHook.
func (h *DefaultQualityAssessorHook) OnToolSuccess(tool *ToolContext, result *ToolResult) {
	if tool == nil || result == nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	signal := toolSignal{
		ToolName:    tool.ToolName,
		Success:     true,
		ExecutionMs: result.ExecutionTime.Milliseconds(),
		RetryCount:  result.RetryCount,
		Timestamp:   time.Now(),
	}

	h.toolSignals = h.appendToolSignal(h.toolSignals, signal)

	// Update aggregator - successful tool with no retries is 1.0
	accuracy := 1.0
	if result.RetryCount > 0 {
		// Reduce accuracy based on retry count
		accuracy = 1.0 / float64(1+result.RetryCount)
	}
	h.aggregator.AddSignal(MetricToolAccuracy, accuracy)
}

// OnToolFailure implements QualityAssessorHook.
func (h *DefaultQualityAssessorHook) OnToolFailure(tool *ToolContext, result *ToolResult) {
	if tool == nil || result == nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	signal := toolSignal{
		ToolName:    tool.ToolName,
		Success:     false,
		ExecutionMs: result.ExecutionTime.Milliseconds(),
		RetryCount:  result.RetryCount,
		Timestamp:   time.Now(),
	}

	h.toolSignals = h.appendToolSignal(h.toolSignals, signal)

	// Tool failure = 0.0 accuracy
	h.aggregator.AddSignal(MetricToolAccuracy, 0.0)
}

// OnUserFeedback implements QualityAssessorHook.
func (h *DefaultQualityAssessorHook) OnUserFeedback(feedbackType FeedbackType, details *FeedbackDetails) {
	if details == nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Convert feedback to a rating score
	rating := h.feedbackToRating(feedbackType, details)

	signal := feedbackSignal{
		FeedbackType: feedbackType,
		Rating:       rating,
		Timestamp:    details.Timestamp,
	}

	h.feedbackSignals = h.appendFeedbackSignal(h.feedbackSignals, signal)

	// Update aggregator with user satisfaction metric
	h.aggregator.AddSignal(MetricUserRating, rating)
}

// GetCurrentScore implements QualityAssessorHook.
func (h *DefaultQualityAssessorHook) GetCurrentScore() *QualityScore {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.currentScore == nil {
		return NewQualityScore()
	}
	return h.currentScore.Clone()
}

// Reset implements QualityAssessorHook.
func (h *DefaultQualityAssessorHook) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.responseSignals = make([]responseSignal, 0, h.config.MaxResponseSignals)
	h.toolSignals = make([]toolSignal, 0, h.config.MaxToolSignals)
	h.feedbackSignals = make([]feedbackSignal, 0, h.config.MaxFeedbackSignals)
	h.aggregator.Reset()
	h.currentScore = NewQualityScore()
}

// =============================================================================
// Internal Methods
// =============================================================================

// assessCoherence evaluates response coherence.
// Returns a value in [0, 1] where 1 indicates high coherence.
func (h *DefaultQualityAssessorHook) assessCoherence(response *ResponseContext) float64 {
	if response.Content == "" {
		return 0.0
	}

	// Heuristic-based coherence assessment
	// In practice, this could use more sophisticated NLP methods
	coherence := 1.0

	// Penalize very short responses (might be incomplete)
	if len(response.Content) < 50 {
		coherence *= 0.8
	}

	// Penalize very long generation times (might indicate struggling)
	if response.GenerationTime > 10*time.Second {
		coherence *= 0.9
	}

	// Consider token efficiency
	if response.TokenCount > 0 && response.ContextSize > 0 {
		efficiency := float64(response.TokenCount) / float64(response.ContextSize)
		if efficiency > 0.5 {
			// Response is too large relative to context
			coherence *= 0.95
		}
	}

	return clamp(coherence, 0.0, 1.0)
}

// assessCompleteness evaluates response completeness.
// Returns a value in [0, 1] where 1 indicates complete response.
func (h *DefaultQualityAssessorHook) assessCompleteness(response *ResponseContext) float64 {
	if response.Content == "" {
		return 0.0
	}

	// Basic completeness heuristics
	completeness := 1.0

	// Very short responses are likely incomplete
	if len(response.Content) < 20 {
		completeness = 0.5
	} else if len(response.Content) < 100 {
		completeness = 0.8
	}

	return clamp(completeness, 0.0, 1.0)
}

// feedbackToRating converts feedback to a normalized rating.
func (h *DefaultQualityAssessorHook) feedbackToRating(feedbackType FeedbackType, details *FeedbackDetails) float64 {
	switch feedbackType {
	case FeedbackThumbsUp:
		return 1.0
	case FeedbackThumbsDown:
		return 0.0
	case FeedbackRating:
		// Assume 5-point scale, normalize to [0, 1]
		return clamp(details.Rating/5.0, 0.0, 1.0)
	case FeedbackCorrection:
		// Correction implies the response was partially wrong
		return 0.3
	case FeedbackRetry:
		// Retry request implies dissatisfaction
		return 0.2
	case FeedbackComment:
		// Comments are neutral unless we analyze sentiment
		return 0.5
	default:
		return 0.5
	}
}

// feedToLearner feeds the quality score to the profile learner.
func (h *DefaultQualityAssessorHook) feedToLearner(agentID, modelID string, score *QualityScore) {
	if h.profileLearner == nil || score == nil {
		return
	}

	// Create an observation from the quality score
	obs := NewHandoffObservation(
		0,                           // Context size not relevant here
		0,                           // Turn number not relevant here
		score.ComputeOverall(),      // Use overall quality as the quality score
		score.Confidence >= 0.5,     // Consider successful if confidence is reasonable
		false,                       // Not a handoff trigger
	)

	h.profileLearner.RecordObservation(agentID, modelID, obs)
}

// appendResponseSignal appends a signal while maintaining max size.
func (h *DefaultQualityAssessorHook) appendResponseSignal(signals []responseSignal, signal responseSignal) []responseSignal {
	if len(signals) >= h.config.MaxResponseSignals {
		// Remove oldest signal
		signals = signals[1:]
	}
	return append(signals, signal)
}

// appendToolSignal appends a signal while maintaining max size.
func (h *DefaultQualityAssessorHook) appendToolSignal(signals []toolSignal, signal toolSignal) []toolSignal {
	if len(signals) >= h.config.MaxToolSignals {
		signals = signals[1:]
	}
	return append(signals, signal)
}

// appendFeedbackSignal appends a signal while maintaining max size.
func (h *DefaultQualityAssessorHook) appendFeedbackSignal(signals []feedbackSignal, signal feedbackSignal) []feedbackSignal {
	if len(signals) >= h.config.MaxFeedbackSignals {
		signals = signals[1:]
	}
	return append(signals, signal)
}

// =============================================================================
// Statistics
// =============================================================================

// QualityHookStats contains statistics about the quality hook.
type QualityHookStats struct {
	// ResponseSignalCount is the number of response signals stored.
	ResponseSignalCount int `json:"response_signal_count"`

	// ToolSignalCount is the number of tool signals stored.
	ToolSignalCount int `json:"tool_signal_count"`

	// FeedbackSignalCount is the number of feedback signals stored.
	FeedbackSignalCount int `json:"feedback_signal_count"`

	// TotalAssessments is the total number of assessments performed.
	TotalAssessments int64 `json:"total_assessments"`

	// LastAssessment is when the last assessment was performed.
	LastAssessment time.Time `json:"last_assessment"`

	// CurrentScore is the current quality score.
	CurrentScore *QualityScore `json:"current_score"`
}

// Stats returns statistics about the quality hook.
func (h *DefaultQualityAssessorHook) Stats() QualityHookStats {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return QualityHookStats{
		ResponseSignalCount: len(h.responseSignals),
		ToolSignalCount:     len(h.toolSignals),
		FeedbackSignalCount: len(h.feedbackSignals),
		TotalAssessments:    h.totalAssessments,
		LastAssessment:      h.lastAssessment,
		CurrentScore:        h.currentScore.Clone(),
	}
}

// =============================================================================
// JSON Serialization
// =============================================================================

// qualityHookJSON is used for JSON marshaling/unmarshaling.
type qualityHookJSON struct {
	Config           *QualityHookConfig `json:"config"`
	ResponseSignals  []responseSignal   `json:"response_signals"`
	ToolSignals      []toolSignal       `json:"tool_signals"`
	FeedbackSignals  []feedbackSignal   `json:"feedback_signals"`
	CurrentScore     *QualityScore      `json:"current_score"`
	LastAssessment   string             `json:"last_assessment"`
	TotalAssessments int64              `json:"total_assessments"`
}

// MarshalJSON implements json.Marshaler.
func (h *DefaultQualityAssessorHook) MarshalJSON() ([]byte, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return json.Marshal(qualityHookJSON{
		Config:           h.config,
		ResponseSignals:  h.responseSignals,
		ToolSignals:      h.toolSignals,
		FeedbackSignals:  h.feedbackSignals,
		CurrentScore:     h.currentScore,
		LastAssessment:   h.lastAssessment.Format(time.RFC3339Nano),
		TotalAssessments: h.totalAssessments,
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (h *DefaultQualityAssessorHook) UnmarshalJSON(data []byte) error {
	var temp qualityHookJSON
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if temp.Config != nil {
		h.config = temp.Config
	} else {
		h.config = DefaultQualityHookConfig()
	}

	h.responseSignals = temp.ResponseSignals
	if h.responseSignals == nil {
		h.responseSignals = make([]responseSignal, 0, h.config.MaxResponseSignals)
	}

	h.toolSignals = temp.ToolSignals
	if h.toolSignals == nil {
		h.toolSignals = make([]toolSignal, 0, h.config.MaxToolSignals)
	}

	h.feedbackSignals = temp.FeedbackSignals
	if h.feedbackSignals == nil {
		h.feedbackSignals = make([]feedbackSignal, 0, h.config.MaxFeedbackSignals)
	}

	h.currentScore = temp.CurrentScore
	if h.currentScore == nil {
		h.currentScore = NewQualityScore()
	}

	if temp.LastAssessment != "" {
		if t, err := time.Parse(time.RFC3339Nano, temp.LastAssessment); err == nil {
			h.lastAssessment = t
		}
	}

	h.totalAssessments = temp.TotalAssessments

	// Recreate aggregator
	aggConfig := h.config.AggregatorConfig
	if aggConfig == nil {
		aggConfig = DefaultQualityAggregatorConfig()
	}
	h.aggregator = NewQualityAggregator(aggConfig)

	return nil
}
