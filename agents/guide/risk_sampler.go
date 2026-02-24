package guide

import (
	"math"
	"sync"
)

// Utilities and Penalties
const (
	UtilitySuccess = 1.0
	UtilityClarify = -0.2 // Small penalty for needing to chat
)

// getFailurePenalty returns the risk penalty for a given intent.
// Destructive actions carry a much higher penalty than read-only queries.
func getFailurePenalty(intent Intent) float64 {
	switch intent {
	case IntentFind, IntentSearch, IntentLocate, IntentHelp, IntentStatus, IntentRecall:
		return -1.0 // Read-only / safe actions
	case IntentPlan, IntentDesign:
		return -1.0 // Non-destructive coordination — misrouting is easily recoverable
	case IntentChat:
		return -0.5 // Lowest risk — wrong conversational agent is trivially redirected
	case IntentDeclare, IntentCheck:
		return -2.5 // Verification / declaration actions
	case IntentStore, IntentComplete:
		return -5.0 // Destructive / State-mutating actions
	default:
		return -2.0
	}
}

// RouteStateKey uniquely identifies the semantic context of a route
type RouteStateKey struct {
	Intent      Intent
	Domain      Domain
	TargetAgent string
}

type BetaDist struct {
	Alpha float64
	Beta  float64
}

// RiskSampler replaces the basic adaptive threshold with a Risk-Averse Bayesian Engine.
// It uses the Lower Confidence Bound of a Beta distribution to ensure the Guide
// falls back to chatting with the user when faced with novel or high-risk queries.
type RiskSampler struct {
	mu            sync.RWMutex
	distributions map[RouteStateKey]*BetaDist
}

func NewRiskSampler() *RiskSampler {
	return &RiskSampler{
		distributions: make(map[RouteStateKey]*BetaDist),
	}
}

// Evaluate safely decides whether to route or fall back to chatting.
func (r *RiskSampler) Evaluate(intent Intent, domain Domain, target string, llmConfidence float64) RouteAction {
	r.mu.RLock()
	key := RouteStateKey{Intent: intent, Domain: domain, TargetAgent: target}
	dist, exists := r.distributions[key]

	var alpha, beta float64
	if exists {
		alpha, beta = dist.Alpha, dist.Beta
	} else {
		// Cold Start: Use LLM confidence as a weak prior (weight of 2.0 total)
		// e.g. 0.9 confidence -> Alpha 1.8, Beta 0.2
		alpha = llmConfidence * 2.0
		beta = (1.0 - llmConfidence) * 2.0
	}
	r.mu.RUnlock()

	// 1. Calculate Mean (Expected Success Rate)
	total := alpha + beta
	mean := alpha / total

	// 2. Calculate Variance (Uncertainty)
	// Higher total data = lower variance. Novel "weird" questions have high variance.
	variance := (alpha * beta) / ((total * total) * (total + 1.0))
	stdDev := math.Sqrt(variance)

	// 3. Lower Confidence Bound (Safety Catch)
	// We subtract 1.5 standard deviations (approx 93% confidence interval lower bound)
	// If we have no data, stdDev is huge, driving worstCaseSuccess way down.
	worstCaseSuccess := mean - (1.5 * stdDev)
	if worstCaseSuccess < 0 {
		worstCaseSuccess = 0
	}

	// 4. Expected Utility Evaluation
	failPenalty := getFailurePenalty(intent)

	// We calculate utility using the WORST CASE success rate to guarantee safety
	euRoute := (worstCaseSuccess * UtilitySuccess) + ((1.0 - worstCaseSuccess) * failPenalty)
	euClarify := UtilityClarify

	// 5. Decision
	if euRoute > euClarify {
		if mean >= 0.85 {
			return RouteActionExecute
		}
		return RouteActionLog
	}

	// SAFETY CATCH TRIPPED: Uncertainty or risk is too high. Chat with the user.
	return RouteActionSuggest
}

// RecordOutcome updates our belief after a route executes or is rejected.
func (r *RiskSampler) RecordOutcome(intent Intent, domain Domain, target string, success bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := RouteStateKey{Intent: intent, Domain: domain, TargetAgent: target}
	dist, exists := r.distributions[key]
	if !exists {
		dist = &BetaDist{Alpha: 1.0, Beta: 1.0} // Neutral uniform prior once we have real data
		r.distributions[key] = dist
	}

	// Decay old data slightly (gamma = 0.98) so the system can adapt over time
	dist.Alpha *= 0.98
	dist.Beta *= 0.98

	if success {
		dist.Alpha += 1.0
	} else {
		dist.Beta += 1.0
	}
}
