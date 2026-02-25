package escalation

import (
	"math"
	"time"
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
