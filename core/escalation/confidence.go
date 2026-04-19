package escalation

import (
	"math"
	"time"

	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/messaging"
)

// ConfidenceLevel is a multi-dimensional confidence assessment.
type ConfidenceLevel struct {
	Correctness  float64            `json:"correctness"`
	Completeness float64            `json:"completeness"`
	Quality      float64            `json:"quality"`
	Integration  float64            `json:"integration"`
	Dimensions   map[string]float64 `json:"dimensions,omitempty"`
	Reasoning    string             `json:"reasoning"`
	AgentID      string             `json:"agent_id"`
	AgentType    string             `json:"agent_type"`
	TaskID       string             `json:"task_id"`
	Timestamp    time.Time          `json:"timestamp"`
}

// NewConfidenceLevel creates a zero-value ConfidenceLevel for the given agent and task.
func NewConfidenceLevel(agentID, agentType, taskID string) *ConfidenceLevel {
	return &ConfidenceLevel{
		AgentID:   agentID,
		AgentType: agentType,
		TaskID:    taskID,
		Timestamp: time.Now(),
	}
}

// ConfidenceWeights controls the relative importance of each dimension.
type ConfidenceWeights struct {
	Correctness  float64            `json:"correctness"`
	Completeness float64            `json:"completeness"`
	Quality      float64            `json:"quality"`
	Integration  float64            `json:"integration"`
	Custom       map[string]float64 `json:"custom,omitempty"`
}

// DefaultWeights returns equal weights for all standard dimensions.
func DefaultWeights() ConfidenceWeights {
	return ConfidenceWeights{
		Correctness:  1.0,
		Completeness: 1.0,
		Quality:      1.0,
		Integration:  1.0,
	}
}

// Composite computes the weighted geometric mean of all dimensions.
// Geometric mean penalizes low outliers: a single 0.1 tanks the composite
// even if others are 0.9. Returns 0 if any dimension is 0.
func (c *ConfidenceLevel) Composite(weights ConfidenceWeights) float64 {
	dims := []struct {
		value  float64
		weight float64
	}{
		{c.Correctness, weights.Correctness},
		{c.Completeness, weights.Completeness},
		{c.Quality, weights.Quality},
		{c.Integration, weights.Integration},
	}

	// Add custom dimensions
	for k, v := range c.Dimensions {
		w := 1.0
		if cw, ok := weights.Custom[k]; ok {
			w = cw
		}
		dims = append(dims, struct {
			value  float64
			weight float64
		}{v, w})
	}

	var sumWeightedLog, sumWeights float64
	for _, d := range dims {
		if d.weight <= 0 {
			continue
		}
		if d.value <= 0 {
			return 0
		}
		sumWeightedLog += d.weight * math.Log(d.value)
		sumWeights += d.weight
	}

	if sumWeights <= 0 {
		return 0
	}
	return math.Exp(sumWeightedLog / sumWeights)
}

// ConfidenceCat categorizes a confidence composite score.
type ConfidenceCat int

const (
	ConfidenceCritical ConfidenceCat = iota // < 0.2
	ConfidenceLow                          // >= 0.2
	ConfidenceMedium                       // >= 0.5
	ConfidenceHigh                         // >= 0.8
)

// String returns the string representation of a ConfidenceCat.
func (c ConfidenceCat) String() string {
	switch c {
	case ConfidenceCritical:
		return "critical"
	case ConfidenceLow:
		return "low"
	case ConfidenceMedium:
		return "medium"
	case ConfidenceHigh:
		return "high"
	default:
		return "unknown"
	}
}

// CategorizeConfidence maps a composite score to a category.
func CategorizeConfidence(score float64) ConfidenceCat {
	switch {
	case score >= 0.8:
		return ConfidenceHigh
	case score >= 0.5:
		return ConfidenceMedium
	case score >= 0.2:
		return ConfidenceLow
	default:
		return ConfidenceCritical
	}
}

// Category returns the confidence category based on composite score.
func (c *ConfidenceLevel) Category(weights ConfidenceWeights) ConfidenceCat {
	return CategorizeConfidence(c.Composite(weights))
}

// FromConfidenceChain distills a message envelope's ConfidenceChain down to
// a single ConfidenceLevel that the escalator can evaluate. Each agent's
// entry contributes one dimension keyed by agent name (so the escalator can
// enforce per-agent dimension thresholds), and the overall Correctness
// score is the minimum confidence on the chain (weakest link wins — a
// single low-confidence hop should dominate the composite). Reasoning
// concatenates the chain's reasons for later display.
//
// The agentType / taskID identifiers come from the caller's context because
// the message envelope doesn't carry them.
func FromConfidenceChain(chain []messaging.ConfidenceEntry, agentType, taskID string) *ConfidenceLevel {
	if len(chain) == 0 {
		return nil
	}
	level := NewConfidenceLevel(lastEntryAgent(chain), agentType, taskID)
	level.Dimensions = make(map[string]float64, len(chain))

	minConfidence := chain[0].Confidence
	var totalConfidence float64
	for _, entry := range chain {
		level.Dimensions[entry.Agent] = entry.Confidence
		if entry.Confidence < minConfidence {
			minConfidence = entry.Confidence
		}
		totalConfidence += entry.Confidence
	}
	avg := totalConfidence / float64(len(chain))

	// Correctness takes the weakest link; Completeness/Quality/Integration
	// take the mean. This matches the spec intent: if any one hop was
	// low-confidence about correctness, the escalator should see that.
	level.Correctness = minConfidence
	level.Completeness = avg
	level.Quality = avg
	level.Integration = avg
	level.Reasoning = summarizeChainReasons(chain)
	return level
}

// lastEntryAgent returns the agent id from the most recent chain entry.
func lastEntryAgent(chain []messaging.ConfidenceEntry) string {
	if len(chain) == 0 {
		return ""
	}
	return chain[len(chain)-1].Agent
}

// summarizeChainReasons joins the per-hop reason strings with arrows so the
// escalation context reads as a causal chain in logs / UI.
func summarizeChainReasons(chain []messaging.ConfidenceEntry) string {
	parts := make([]string, 0, len(chain))
	for _, entry := range chain {
		parts = append(parts, entry.Agent+": "+entry.Reason)
	}
	return joinWithArrow(parts)
}

func joinWithArrow(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " -> " + p
	}
	return out
}

// WorkaroundBudgetExceeded reports whether the chain contains enough
// low-confidence hops to exhaust the Architect workaround budget described
// in ARCHITECTURE.md:12022. A workaround hop is any ConfidenceEntry below
// `workaroundThreshold`; once `maxWorkarounds` of those accumulate the
// escalator treats the chain as exceeded-budget and forces user escalation.
func WorkaroundBudgetExceeded(chain []messaging.ConfidenceEntry, workaroundThreshold float64, maxWorkarounds int) bool {
	if maxWorkarounds <= 0 {
		return false
	}
	n := 0
	for _, entry := range chain {
		if entry.Confidence < workaroundThreshold {
			n++
			if n >= maxWorkarounds {
				return true
			}
		}
	}
	return false
}

// DefaultWorkaroundThreshold is the confidence below which a hop counts as
// a workaround under the ARCHITECTURE.md:12022 budget. Tuned so that a
// "low" (< 0.5) hop contributes, but a normal "medium" hop (0.5+) does not.
const DefaultWorkaroundThreshold = 0.5

// DefaultMaxWorkarounds is the per-request workaround ceiling. Three
// below-threshold hops is the documented ceiling before Architect escalates
// to the user for direction instead of attempting another workaround.
const DefaultMaxWorkarounds = 3

// DetermineEscalationFromChain is the escalator's entry point for callers
// that already carry a messaging.ConfidenceChain on the envelope. It
// derives a ConfidenceLevel via FromConfidenceChain, applies the workaround
// budget, then defers to DetermineEscalation.
//
// Returns an EscalationTarget with SuggestedAction=ActionAskUser when the
// workaround budget is exhausted — the escalator's contract for forcing
// user intervention rather than another retry.
func DetermineEscalationFromChain(
	chain []messaging.ConfidenceEntry,
	agentType, taskID string,
	weights ConfidenceWeights,
	policy *EscalationPolicy,
	category handoff.AgentCategory,
	history *EscalationHistory,
	depth int,
) (*EscalationTarget, bool) {
	if WorkaroundBudgetExceeded(chain, DefaultWorkaroundThreshold, DefaultMaxWorkarounds) {
		return &EscalationTarget{
			TargetAgent:     "user",
			Reason:          ReasonRepeatedFailure,
			Priority:        95,
			SuggestedAction: ActionAskUser,
			Context: map[string]any{
				"workaround_budget_exceeded": true,
				"chain_length":               len(chain),
			},
		}, true
	}
	level := FromConfidenceChain(chain, agentType, taskID)
	if level == nil {
		return nil, false
	}
	return DetermineEscalation(level, weights, policy, category, history, depth)
}
