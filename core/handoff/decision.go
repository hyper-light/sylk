package handoff

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"
)

// =============================================================================
// HO.5.1 Handoff Decision Types
// =============================================================================
//
// These types represent the output of handoff decision logic, capturing
// whether a handoff should occur, why, and with what confidence.

// HandoffTrigger represents the reason for triggering a handoff.
// Different triggers may require different handling strategies.
type HandoffTrigger int

const (
	// TriggerNone indicates no handoff is triggered.
	TriggerNone HandoffTrigger = iota

	// TriggerContextFull indicates handoff due to context window being full
	// or nearly full. This is a hard limit that must be respected.
	TriggerContextFull

	// TriggerQualityDegrading indicates handoff due to predicted quality
	// falling below acceptable thresholds. This is a soft limit based on
	// learned quality prediction models.
	TriggerQualityDegrading

	// TriggerCostOptimization indicates handoff for cost reasons, such as
	// switching to a cheaper model or reducing token usage.
	TriggerCostOptimization

	// TriggerUserRequest indicates the user explicitly requested a handoff,
	// typically through a command or UI action.
	TriggerUserRequest
)

// String returns a human-readable name for the trigger.
func (ht HandoffTrigger) String() string {
	switch ht {
	case TriggerNone:
		return "None"
	case TriggerContextFull:
		return "ContextFull"
	case TriggerQualityDegrading:
		return "QualityDegrading"
	case TriggerCostOptimization:
		return "CostOptimization"
	case TriggerUserRequest:
		return "UserRequest"
	default:
		return fmt.Sprintf("Unknown(%d)", int(ht))
	}
}

// MarshalJSON implements json.Marshaler for HandoffTrigger.
func (ht HandoffTrigger) MarshalJSON() ([]byte, error) {
	return json.Marshal(ht.String())
}

// UnmarshalJSON implements json.Unmarshaler for HandoffTrigger.
func (ht *HandoffTrigger) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*ht = parseTriggerString(s)
	return nil
}

// parseTriggerString converts a string to HandoffTrigger.
func parseTriggerString(s string) HandoffTrigger {
	switch s {
	case "None":
		return TriggerNone
	case "ContextFull":
		return TriggerContextFull
	case "QualityDegrading":
		return TriggerQualityDegrading
	case "CostOptimization":
		return TriggerCostOptimization
	case "UserRequest":
		return TriggerUserRequest
	default:
		return TriggerNone
	}
}

// DecisionFactors captures the factors that went into a handoff decision.
// These are logged for debugging and used to improve future decisions.
type DecisionFactors struct {
	// ContextUtilization is the fraction of context window currently used.
	// Range: [0, 1] where 1 means fully utilized.
	ContextUtilization float64 `json:"context_utilization"`

	// PredictedQuality is the GP-predicted quality score at current context.
	// Range: [0, 1] where higher is better.
	PredictedQuality float64 `json:"predicted_quality"`

	// QualityUncertainty is the standard deviation of quality prediction.
	// Higher values indicate less confident predictions.
	QualityUncertainty float64 `json:"quality_uncertainty"`

	// CostEstimate is the estimated cost of continuing vs. handing off.
	// Positive values favor handoff, negative favor continuing.
	CostEstimate float64 `json:"cost_estimate"`

	// UCBScore is the Upper Confidence Bound score for exploration.
	// Combines quality prediction with uncertainty bonus.
	UCBScore float64 `json:"ucb_score"`

	// ThresholdUsed is the handoff threshold that was applied.
	ThresholdUsed float64 `json:"threshold_used"`

	// ProfileConfidence is the confidence in the learned profile.
	// Range: [0, 1] where higher means more data/experience.
	ProfileConfidence float64 `json:"profile_confidence"`
}

// HandoffDecision represents the outcome of evaluating whether a handoff
// should occur. It captures the decision, confidence, and reasoning.
type HandoffDecision struct {
	// ShouldHandoff indicates whether a handoff is recommended.
	ShouldHandoff bool `json:"should_handoff"`

	// Confidence indicates how confident we are in the decision.
	// Range: [0, 1] where 1 means very confident.
	Confidence float64 `json:"confidence"`

	// Reason is a human-readable explanation of the decision.
	Reason string `json:"reason"`

	// Trigger indicates what caused the handoff (if ShouldHandoff is true).
	Trigger HandoffTrigger `json:"trigger"`

	// Factors contains the detailed factors that went into the decision.
	Factors DecisionFactors `json:"factors"`

	// Timestamp is when the decision was made.
	Timestamp time.Time `json:"timestamp"`
}

// NewHandoffDecision creates a new handoff decision with the current timestamp.
func NewHandoffDecision(
	shouldHandoff bool,
	confidence float64,
	reason string,
	trigger HandoffTrigger,
	factors DecisionFactors,
) *HandoffDecision {
	return &HandoffDecision{
		ShouldHandoff: shouldHandoff,
		Confidence:    clamp(confidence, 0.0, 1.0),
		Reason:        reason,
		Trigger:       trigger,
		Factors:       factors,
		Timestamp:     time.Now(),
	}
}

// String returns a human-readable representation of the decision.
func (hd *HandoffDecision) String() string {
	if hd.ShouldHandoff {
		return fmt.Sprintf(
			"HandoffDecision{Handoff: YES, Trigger: %s, Confidence: %.2f, Reason: %s}",
			hd.Trigger, hd.Confidence, hd.Reason,
		)
	}
	return fmt.Sprintf(
		"HandoffDecision{Handoff: NO, Confidence: %.2f, Reason: %s}",
		hd.Confidence, hd.Reason,
	)
}

// =============================================================================
// HO.5.1 ContextState - Input for Handoff Evaluation
// =============================================================================

// ContextState represents the current state of context for handoff evaluation.
type ContextState struct {
	// ContextSize is the current context size in tokens.
	ContextSize int `json:"context_size"`

	// MaxContextSize is the maximum allowed context size.
	MaxContextSize int `json:"max_context_size"`

	// TokenCount is the number of tokens in the current turn/response.
	TokenCount int `json:"token_count"`

	// ToolCallCount is the number of tool calls in the current operation.
	ToolCallCount int `json:"tool_call_count"`

	// TurnNumber is the current conversation turn number.
	TurnNumber int `json:"turn_number"`

	// UserRequestedHandoff indicates if the user explicitly requested handoff.
	UserRequestedHandoff bool `json:"user_requested_handoff"`

	// EstimatedCostPerToken is the cost per token for the current model.
	// Used for cost optimization decisions.
	EstimatedCostPerToken float64 `json:"estimated_cost_per_token"`
}

// Utilization returns the context utilization as a fraction [0, 1].
func (cs *ContextState) Utilization() float64 {
	if cs.MaxContextSize <= 0 {
		return 0.0
	}
	return float64(cs.ContextSize) / float64(cs.MaxContextSize)
}

// =============================================================================
// HO.5.2 HandoffController - Main Decision Logic
// =============================================================================
//
// HandoffController uses AgentGaussianProcess for quality prediction and
// AgentHandoffProfile for learned parameters to make handoff decisions.
// It implements UCB-style exploration: quality + beta*sigma for uncertainty bonus.

// ControllerConfig holds configuration for the HandoffController.
type ControllerConfig struct {
	// ContextFullThreshold is the utilization level at which context is
	// considered "full" and handoff is triggered. Default: 0.9
	ContextFullThreshold float64 `json:"context_full_threshold"`

	// QualityThreshold is the predicted quality below which handoff is
	// triggered for quality degradation. Default: 0.5
	QualityThreshold float64 `json:"quality_threshold"`

	// ExplorationBeta controls the exploration bonus in UCB calculation.
	// Higher values encourage exploration (trying handoff) when uncertain.
	// Default: 1.0
	ExplorationBeta float64 `json:"exploration_beta"`

	// MinConfidenceForDecision is the minimum profile confidence needed
	// to use learned parameters vs. defaults. Default: 0.3
	MinConfidenceForDecision float64 `json:"min_confidence_for_decision"`

	// CostWeight is the weight given to cost in the decision.
	// Higher values make cost optimization more important. Default: 0.1
	CostWeight float64 `json:"cost_weight"`

	// QualityWeight is the weight given to quality in the decision.
	// Higher values make quality degradation more important. Default: 0.6
	QualityWeight float64 `json:"quality_weight"`

	// ContextWeight is the weight given to context utilization.
	// Higher values make context fullness more important. Default: 0.3
	ContextWeight float64 `json:"context_weight"`
}

// DefaultControllerConfig returns sensible defaults for the controller.
func DefaultControllerConfig() *ControllerConfig {
	return &ControllerConfig{
		ContextFullThreshold:     0.9,
		QualityThreshold:         0.5,
		ExplorationBeta:          1.0,
		MinConfidenceForDecision: 0.3,
		CostWeight:               0.1,
		QualityWeight:            0.6,
		ContextWeight:            0.3,
	}
}

// Clone creates a deep copy of the config.
func (cc *ControllerConfig) Clone() *ControllerConfig {
	if cc == nil {
		return nil
	}
	return &ControllerConfig{
		ContextFullThreshold:     cc.ContextFullThreshold,
		QualityThreshold:         cc.QualityThreshold,
		ExplorationBeta:          cc.ExplorationBeta,
		MinConfidenceForDecision: cc.MinConfidenceForDecision,
		CostWeight:               cc.CostWeight,
		QualityWeight:            cc.QualityWeight,
		ContextWeight:            cc.ContextWeight,
	}
}

// HandoffController makes handoff decisions using GP quality prediction
// and learned agent profiles.
type HandoffController struct {
	mu sync.RWMutex

	// gp is the Gaussian Process for quality prediction.
	gp *AgentGaussianProcess

	// profile is the learned handoff profile for this agent.
	profile *AgentHandoffProfile

	// config holds the controller configuration.
	config *ControllerConfig

	// decisionHistory stores recent decisions for learning.
	decisionHistory []*HandoffDecision

	// maxDecisionHistory limits history size.
	maxDecisionHistory int

	// lastDecision caches the most recent decision.
	lastDecision *HandoffDecision
}

// NewHandoffController creates a new controller with the given components.
// If gp is nil, creates a new GP with default hyperparameters.
// If profile is nil, creates a new profile with default priors.
// If config is nil, uses default configuration.
func NewHandoffController(
	gp *AgentGaussianProcess,
	profile *AgentHandoffProfile,
	config *ControllerConfig,
) *HandoffController {
	if gp == nil {
		gp = NewAgentGaussianProcess(nil)
	}
	if profile == nil {
		profile = NewAgentHandoffProfile("default", "default", "")
	}
	if config == nil {
		config = DefaultControllerConfig()
	}

	return &HandoffController{
		gp:                 gp,
		profile:            profile,
		config:             config.Clone(),
		decisionHistory:    make([]*HandoffDecision, 0),
		maxDecisionHistory: 100,
		lastDecision:       nil,
	}
}

// EvaluateHandoff evaluates whether a handoff should occur given the
// current context state. Returns a HandoffDecision with the recommendation.
//
// The evaluation considers:
// 1. Hard limits (context full, user request)
// 2. Quality prediction from GP
// 3. UCB exploration bonus
// 4. Cost optimization
// 5. Learned thresholds from profile
func (hc *HandoffController) EvaluateHandoff(ctx *ContextState) *HandoffDecision {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	if ctx == nil {
		return NewHandoffDecision(
			false,
			1.0,
			"No context state provided",
			TriggerNone,
			DecisionFactors{},
		)
	}

	// Check for user-requested handoff (highest priority)
	if ctx.UserRequestedHandoff {
		decision := hc.createUserRequestDecision(ctx)
		hc.recordDecision(decision)
		return decision
	}

	// Check for context full (hard limit)
	utilization := ctx.Utilization()
	if utilization >= hc.config.ContextFullThreshold {
		decision := hc.createContextFullDecision(ctx, utilization)
		hc.recordDecision(decision)
		return decision
	}

	// Get quality prediction from GP
	prediction := hc.gp.Predict(ctx.ContextSize, ctx.TokenCount, ctx.ToolCallCount)

	// Calculate UCB score: quality + beta * sigma
	ucbScore := prediction.Mean + hc.config.ExplorationBeta*prediction.StdDev

	// Get threshold from profile (or use default)
	threshold := hc.getEffectiveThreshold()

	// Check for quality degradation
	if prediction.Mean < threshold {
		decision := hc.createQualityDegradingDecision(ctx, prediction, ucbScore, threshold)
		hc.recordDecision(decision)
		return decision
	}

	// Check for cost optimization opportunity
	costDecision := hc.evaluateCostOptimization(ctx, prediction, ucbScore, threshold)
	if costDecision != nil && costDecision.ShouldHandoff {
		hc.recordDecision(costDecision)
		return costDecision
	}

	// No handoff needed
	decision := hc.createNoHandoffDecision(ctx, prediction, ucbScore, threshold)
	hc.recordDecision(decision)
	return decision
}

// createUserRequestDecision creates a decision for user-requested handoff.
func (hc *HandoffController) createUserRequestDecision(ctx *ContextState) *HandoffDecision {
	factors := DecisionFactors{
		ContextUtilization: ctx.Utilization(),
		ProfileConfidence:  hc.profile.Confidence(),
	}

	return NewHandoffDecision(
		true,
		1.0,
		"User explicitly requested handoff",
		TriggerUserRequest,
		factors,
	)
}

// createContextFullDecision creates a decision for context full trigger.
func (hc *HandoffController) createContextFullDecision(
	ctx *ContextState,
	utilization float64,
) *HandoffDecision {
	factors := DecisionFactors{
		ContextUtilization: utilization,
		ThresholdUsed:      hc.config.ContextFullThreshold,
		ProfileConfidence:  hc.profile.Confidence(),
	}

	reason := fmt.Sprintf(
		"Context utilization (%.1f%%) exceeds threshold (%.1f%%)",
		utilization*100,
		hc.config.ContextFullThreshold*100,
	)

	return NewHandoffDecision(
		true,
		0.95, // High confidence since this is a hard limit
		reason,
		TriggerContextFull,
		factors,
	)
}

// createQualityDegradingDecision creates a decision for quality degradation.
func (hc *HandoffController) createQualityDegradingDecision(
	ctx *ContextState,
	prediction *GPPrediction,
	ucbScore float64,
	threshold float64,
) *HandoffDecision {
	factors := DecisionFactors{
		ContextUtilization: ctx.Utilization(),
		PredictedQuality:   prediction.Mean,
		QualityUncertainty: prediction.StdDev,
		UCBScore:           ucbScore,
		ThresholdUsed:      threshold,
		ProfileConfidence:  hc.profile.Confidence(),
	}

	// Confidence based on how far below threshold and uncertainty
	distanceFromThreshold := threshold - prediction.Mean
	confidenceFromDistance := clamp(distanceFromThreshold*2, 0.0, 1.0)
	confidenceFromCertainty := 1.0 - clamp(prediction.StdDev*2, 0.0, 0.5)
	confidence := (confidenceFromDistance + confidenceFromCertainty) / 2

	reason := fmt.Sprintf(
		"Predicted quality (%.2f) below threshold (%.2f), UCB score: %.2f",
		prediction.Mean,
		threshold,
		ucbScore,
	)

	return NewHandoffDecision(
		true,
		confidence,
		reason,
		TriggerQualityDegrading,
		factors,
	)
}

// evaluateCostOptimization checks if a cost-based handoff is warranted.
func (hc *HandoffController) evaluateCostOptimization(
	ctx *ContextState,
	prediction *GPPrediction,
	ucbScore float64,
	threshold float64,
) *HandoffDecision {
	// Skip cost optimization if no cost data
	if ctx.EstimatedCostPerToken <= 0 {
		return nil
	}

	// Estimate cost of continuing vs. handing off
	// Simple model: cost = tokens * cost_per_token
	remainingContext := ctx.MaxContextSize - ctx.ContextSize
	estimatedCostToContinue := float64(remainingContext) * ctx.EstimatedCostPerToken

	// If quality is borderline and cost is high, consider handoff
	qualityMargin := prediction.Mean - threshold
	if qualityMargin < 0.2 && estimatedCostToContinue > 0.1 {
		factors := DecisionFactors{
			ContextUtilization: ctx.Utilization(),
			PredictedQuality:   prediction.Mean,
			QualityUncertainty: prediction.StdDev,
			CostEstimate:       estimatedCostToContinue,
			UCBScore:           ucbScore,
			ThresholdUsed:      threshold,
			ProfileConfidence:  hc.profile.Confidence(),
		}

		// Low confidence since this is a soft optimization
		confidence := 0.3 + 0.3*(1.0-qualityMargin/0.2)

		reason := fmt.Sprintf(
			"Cost optimization: quality margin (%.2f) low, estimated cost (%.4f) high",
			qualityMargin,
			estimatedCostToContinue,
		)

		return NewHandoffDecision(
			true,
			confidence,
			reason,
			TriggerCostOptimization,
			factors,
		)
	}

	return nil
}

// createNoHandoffDecision creates a decision when no handoff is needed.
func (hc *HandoffController) createNoHandoffDecision(
	ctx *ContextState,
	prediction *GPPrediction,
	ucbScore float64,
	threshold float64,
) *HandoffDecision {
	factors := DecisionFactors{
		ContextUtilization: ctx.Utilization(),
		PredictedQuality:   prediction.Mean,
		QualityUncertainty: prediction.StdDev,
		UCBScore:           ucbScore,
		ThresholdUsed:      threshold,
		ProfileConfidence:  hc.profile.Confidence(),
	}

	// Confidence increases with quality headroom and decreases with uncertainty
	qualityHeadroom := prediction.Mean - threshold
	confidenceFromHeadroom := clamp(qualityHeadroom*2, 0.0, 1.0)
	confidenceFromCertainty := 1.0 - clamp(prediction.StdDev, 0.0, 0.5)
	confidence := (confidenceFromHeadroom + confidenceFromCertainty) / 2

	reason := fmt.Sprintf(
		"Quality (%.2f) above threshold (%.2f), context utilization (%.1f%%)",
		prediction.Mean,
		threshold,
		ctx.Utilization()*100,
	)

	return NewHandoffDecision(
		false,
		confidence,
		reason,
		TriggerNone,
		factors,
	)
}

// getEffectiveThreshold returns the threshold to use for decisions.
// Uses learned profile threshold if confidence is sufficient, otherwise default.
func (hc *HandoffController) getEffectiveThreshold() float64 {
	if hc.profile.Confidence() >= hc.config.MinConfidenceForDecision {
		return hc.profile.GetHandoffThresholdMean()
	}
	return hc.config.QualityThreshold
}

// recordDecision adds a decision to history and updates lastDecision.
func (hc *HandoffController) recordDecision(decision *HandoffDecision) {
	hc.lastDecision = decision
	hc.decisionHistory = append(hc.decisionHistory, decision)

	// Trim history if needed
	for len(hc.decisionHistory) > hc.maxDecisionHistory {
		hc.decisionHistory = hc.decisionHistory[1:]
	}
}

// UpdateAfterHandoff updates the controller after a handoff has completed.
// This allows learning from the outcome to improve future decisions.
//
// Parameters:
//   - ctx: The context state when handoff was triggered
//   - wasSuccessful: Whether the handoff resulted in a successful outcome
//   - observedQuality: The quality score observed after the handoff (0-1)
func (hc *HandoffController) UpdateAfterHandoff(
	ctx *ContextState,
	wasSuccessful bool,
	observedQuality float64,
) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	if ctx == nil {
		return
	}

	// Update GP with new observation
	obs := NewGPObservation(
		ctx.ContextSize,
		ctx.TokenCount,
		ctx.ToolCallCount,
		observedQuality,
	)
	hc.gp.AddObservation(obs)

	// Update profile with handoff observation
	profileObs := NewHandoffObservation(
		ctx.ContextSize,
		ctx.TurnNumber,
		observedQuality,
		wasSuccessful,
		true, // Handoff was triggered
	)
	hc.profile.Update(profileObs)
}

// UpdateWithoutHandoff updates the controller when no handoff occurred.
// This provides negative examples for learning.
//
// Parameters:
//   - ctx: The context state
//   - wasSuccessful: Whether the operation was successful without handoff
//   - observedQuality: The quality score observed
func (hc *HandoffController) UpdateWithoutHandoff(
	ctx *ContextState,
	wasSuccessful bool,
	observedQuality float64,
) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	if ctx == nil {
		return
	}

	// Update GP with observation
	obs := NewGPObservation(
		ctx.ContextSize,
		ctx.TokenCount,
		ctx.ToolCallCount,
		observedQuality,
	)
	hc.gp.AddObservation(obs)

	// Update profile (no handoff triggered)
	profileObs := NewHandoffObservation(
		ctx.ContextSize,
		ctx.TurnNumber,
		observedQuality,
		wasSuccessful,
		false, // No handoff triggered
	)
	hc.profile.Update(profileObs)
}

// =============================================================================
// Getters and Configuration
// =============================================================================

// GetGP returns the Gaussian Process (for testing and advanced usage).
func (hc *HandoffController) GetGP() *AgentGaussianProcess {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	return hc.gp
}

// GetProfile returns the handoff profile (for testing and advanced usage).
func (hc *HandoffController) GetProfile() *AgentHandoffProfile {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	return hc.profile
}

// GetConfig returns a copy of the current configuration.
func (hc *HandoffController) GetConfig() *ControllerConfig {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	return hc.config.Clone()
}

// SetConfig updates the controller configuration.
func (hc *HandoffController) SetConfig(config *ControllerConfig) {
	if config == nil {
		return
	}
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.config = config.Clone()
}

// GetLastDecision returns the most recent decision made.
func (hc *HandoffController) GetLastDecision() *HandoffDecision {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	return hc.lastDecision
}

// GetDecisionHistory returns a copy of the decision history.
func (hc *HandoffController) GetDecisionHistory() []*HandoffDecision {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	history := make([]*HandoffDecision, len(hc.decisionHistory))
	copy(history, hc.decisionHistory)
	return history
}

// ClearHistory clears the decision history.
func (hc *HandoffController) ClearHistory() {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.decisionHistory = make([]*HandoffDecision, 0)
	hc.lastDecision = nil
}

// SetMaxHistory sets the maximum number of decisions to keep in history.
func (hc *HandoffController) SetMaxHistory(max int) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if max < 1 {
		max = 1
	}
	hc.maxDecisionHistory = max

	// Trim if needed
	for len(hc.decisionHistory) > hc.maxDecisionHistory {
		hc.decisionHistory = hc.decisionHistory[1:]
	}
}

// =============================================================================
// Statistics and Debugging
// =============================================================================

// ControllerStats contains statistics about the controller's operation.
type ControllerStats struct {
	// TotalDecisions is the number of decisions made.
	TotalDecisions int `json:"total_decisions"`

	// HandoffDecisions is the number of decisions that recommended handoff.
	HandoffDecisions int `json:"handoff_decisions"`

	// TriggerCounts maps trigger types to their counts.
	TriggerCounts map[string]int `json:"trigger_counts"`

	// AverageConfidence is the average confidence across all decisions.
	AverageConfidence float64 `json:"average_confidence"`

	// GPObservations is the number of observations in the GP.
	GPObservations int `json:"gp_observations"`

	// ProfileConfidence is the current profile confidence.
	ProfileConfidence float64 `json:"profile_confidence"`
}

// GetStats returns statistics about the controller's operation.
func (hc *HandoffController) GetStats() ControllerStats {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	stats := ControllerStats{
		TotalDecisions:    len(hc.decisionHistory),
		HandoffDecisions:  0,
		TriggerCounts:     make(map[string]int),
		AverageConfidence: 0.0,
		GPObservations:    hc.gp.NumObservations(),
		ProfileConfidence: hc.profile.Confidence(),
	}

	if len(hc.decisionHistory) == 0 {
		return stats
	}

	totalConfidence := 0.0
	for _, d := range hc.decisionHistory {
		if d.ShouldHandoff {
			stats.HandoffDecisions++
		}
		stats.TriggerCounts[d.Trigger.String()]++
		totalConfidence += d.Confidence
	}

	stats.AverageConfidence = totalConfidence / float64(len(hc.decisionHistory))

	return stats
}

// =============================================================================
// JSON Serialization
// =============================================================================

// controllerJSON is used for JSON marshaling/unmarshaling.
type controllerJSON struct {
	GP                 *AgentGaussianProcess `json:"gp"`
	Profile            *AgentHandoffProfile  `json:"profile"`
	Config             *ControllerConfig     `json:"config"`
	MaxDecisionHistory int                   `json:"max_decision_history"`
}

// MarshalJSON implements json.Marshaler.
func (hc *HandoffController) MarshalJSON() ([]byte, error) {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	return json.Marshal(controllerJSON{
		GP:                 hc.gp,
		Profile:            hc.profile,
		Config:             hc.config,
		MaxDecisionHistory: hc.maxDecisionHistory,
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (hc *HandoffController) UnmarshalJSON(data []byte) error {
	var temp controllerJSON
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	hc.mu.Lock()
	defer hc.mu.Unlock()

	if temp.GP != nil {
		hc.gp = temp.GP
	} else {
		hc.gp = NewAgentGaussianProcess(nil)
	}

	if temp.Profile != nil {
		hc.profile = temp.Profile
	} else {
		hc.profile = NewAgentHandoffProfile("default", "default", "")
	}

	if temp.Config != nil {
		hc.config = temp.Config
	} else {
		hc.config = DefaultControllerConfig()
	}

	hc.maxDecisionHistory = temp.MaxDecisionHistory
	if hc.maxDecisionHistory < 1 {
		hc.maxDecisionHistory = 100
	}

	hc.decisionHistory = make([]*HandoffDecision, 0)
	hc.lastDecision = nil

	return nil
}

// =============================================================================
// Utility Functions
// =============================================================================

// PredictQuality returns the predicted quality for a given context state.
// This is a convenience method for direct GP prediction.
func (hc *HandoffController) PredictQuality(ctx *ContextState) *GPPrediction {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	if ctx == nil {
		return &GPPrediction{
			Mean:       0.7,
			Variance:   0.25,
			StdDev:     0.5,
			LowerBound: 0.2,
			UpperBound: 1.0,
		}
	}

	return hc.gp.Predict(ctx.ContextSize, ctx.TokenCount, ctx.ToolCallCount)
}

// ComputeUCB computes the Upper Confidence Bound score for a context state.
// UCB = mean + beta * stddev
// Higher UCB indicates more optimistic quality (including uncertainty bonus).
func (hc *HandoffController) ComputeUCB(ctx *ContextState) float64 {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	if ctx == nil {
		return 0.7
	}

	pred := hc.gp.Predict(ctx.ContextSize, ctx.TokenCount, ctx.ToolCallCount)
	return pred.Mean + hc.config.ExplorationBeta*pred.StdDev
}

// ShouldExplore returns true if the controller should explore (try handoff)
// based on uncertainty. High uncertainty means we should explore to learn.
func (hc *HandoffController) ShouldExplore(ctx *ContextState) bool {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	if ctx == nil {
		return false
	}

	pred := hc.gp.Predict(ctx.ContextSize, ctx.TokenCount, ctx.ToolCallCount)

	// If uncertainty is high (stddev > 0.2), consider exploration
	// Also consider profile confidence - low confidence = more exploration
	explorationScore := pred.StdDev + (1.0 - hc.profile.Confidence())*0.5

	return explorationScore > 0.4
}

// WeightedDecisionScore computes a weighted score for handoff decision.
// Positive scores favor handoff, negative favor continuing.
func (hc *HandoffController) WeightedDecisionScore(ctx *ContextState) float64 {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	if ctx == nil {
		return 0.0
	}

	// Get quality prediction
	pred := hc.gp.Predict(ctx.ContextSize, ctx.TokenCount, ctx.ToolCallCount)
	threshold := hc.getEffectiveThreshold()

	// Quality factor: negative when quality above threshold
	qualityFactor := (threshold - pred.Mean) / threshold

	// Context factor: positive when context is full
	contextFactor := ctx.Utilization() - hc.config.ContextFullThreshold

	// Cost factor: positive when cost is high
	costFactor := 0.0
	if ctx.EstimatedCostPerToken > 0 {
		remainingContext := float64(ctx.MaxContextSize - ctx.ContextSize)
		estimatedCost := remainingContext * ctx.EstimatedCostPerToken
		costFactor = math.Min(estimatedCost, 1.0)
	}

	// Weighted combination
	score := hc.config.QualityWeight*qualityFactor +
		hc.config.ContextWeight*contextFactor +
		hc.config.CostWeight*costFactor

	return score
}
