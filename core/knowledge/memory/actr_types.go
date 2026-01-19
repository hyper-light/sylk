package memory

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"time"
)

// =============================================================================
// MD.2.1 AccessTrace Type
// =============================================================================

// AccessType represents the type of memory access that occurred.
type AccessType int

const (
	AccessRetrieval      AccessType = 0
	AccessReinforcement  AccessType = 1
	AccessCreation       AccessType = 2
	AccessReference      AccessType = 3
)

// ValidAccessTypes returns all valid AccessType values.
func ValidAccessTypes() []AccessType {
	return []AccessType{
		AccessRetrieval,
		AccessReinforcement,
		AccessCreation,
		AccessReference,
	}
}

// IsValid returns true if the access type is a recognized value.
func (at AccessType) IsValid() bool {
	for _, valid := range ValidAccessTypes() {
		if at == valid {
			return true
		}
	}
	return false
}

func (at AccessType) String() string {
	switch at {
	case AccessRetrieval:
		return "retrieval"
	case AccessReinforcement:
		return "reinforcement"
	case AccessCreation:
		return "creation"
	case AccessReference:
		return "reference"
	default:
		return fmt.Sprintf("access_type(%d)", at)
	}
}

func ParseAccessType(value string) (AccessType, bool) {
	switch value {
	case "retrieval":
		return AccessRetrieval, true
	case "reinforcement":
		return AccessReinforcement, true
	case "creation":
		return AccessCreation, true
	case "reference":
		return AccessReference, true
	default:
		return AccessType(0), false
	}
}

func (at AccessType) MarshalJSON() ([]byte, error) {
	return json.Marshal(at.String())
}

func (at *AccessType) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		if parsed, ok := ParseAccessType(asString); ok {
			*at = parsed
			return nil
		}
		return fmt.Errorf("invalid access type: %s", asString)
	}

	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		*at = AccessType(asInt)
		return nil
	}

	return fmt.Errorf("invalid access type")
}

// AccessTrace represents a single access event to a memory node.
type AccessTrace struct {
	AccessedAt time.Time  `json:"accessed_at"`
	AccessType AccessType `json:"access_type"`
	Context    string     `json:"context,omitempty"`
}

// =============================================================================
// MD.2.2 ACTRMemory Type
// =============================================================================

// ACTRMemory represents an ACT-R declarative memory chunk with adaptive
// decay learning. Implements the ACT-R activation equation:
//   B = ln(Σtⱼ^(-d)) + β
// where d is the decay parameter (learned per-domain via Beta distribution).
type ACTRMemory struct {
	// Identity
	NodeID string `json:"node_id"`
	Domain int    `json:"domain"`

	// Access history
	Traces     []AccessTrace `json:"traces"`
	MaxTraces  int           `json:"max_traces"`

	// Decay parameters (Beta distribution for Thompson Sampling)
	DecayAlpha float64 `json:"decay_alpha"`
	DecayBeta  float64 `json:"decay_beta"`

	// Base-level offset parameters
	BaseOffsetMean     float64 `json:"base_offset_mean"`
	BaseOffsetVariance float64 `json:"base_offset_variance"`

	// Metadata
	CreatedAt   time.Time `json:"created_at"`
	AccessCount int       `json:"access_count"`
}

// Activation computes the base-level activation using the ACT-R equation:
//   B = ln(Σtⱼ^(-d)) + β
// where d is the decay parameter (sampled from Beta distribution).
func (m *ACTRMemory) Activation(now time.Time) float64 {
	if len(m.Traces) == 0 {
		return -math.MaxFloat64
	}

	d := m.DecayMean()
	sum := 0.0

	for _, trace := range m.Traces {
		ageSeconds := now.Sub(trace.AccessedAt).Seconds()
		if ageSeconds <= 0 {
			ageSeconds = 1.0
		}
		sum += math.Pow(ageSeconds, -d)
	}

	if sum <= 0 {
		return -math.MaxFloat64
	}

	baseLevel := math.Log(sum)
	offset := m.BaseOffsetMean

	return baseLevel + offset
}

// DecayMean returns the expected value of the decay parameter:
//   E[d] = α/(α+β)
func (m *ACTRMemory) DecayMean() float64 {
	if m.DecayAlpha+m.DecayBeta == 0 {
		return 0.5
	}
	return m.DecayAlpha / (m.DecayAlpha + m.DecayBeta)
}

// DecaySample returns a random sample from the Beta distribution for
// Thompson Sampling exploration of decay parameters.
func (m *ACTRMemory) DecaySample() float64 {
	// Simple Beta sampling using Gamma random variables
	// Beta(α,β) = Gamma(α,1) / (Gamma(α,1) + Gamma(β,1))
	ga := gammaRand(m.DecayAlpha)
	gb := gammaRand(m.DecayBeta)
	if ga+gb == 0 {
		return 0.5
	}
	return ga / (ga + gb)
}

// RetrievalProbability computes the probability of successful retrieval
// using softmax activation with a threshold:
//   P(retrieval) = 1 / (1 + exp(-(A - τ)/s))
// where A is activation, τ is threshold, and s is noise (default 0.25).
func (m *ACTRMemory) RetrievalProbability(now time.Time, threshold float64) float64 {
	activation := m.Activation(now)
	noise := 0.25
	diff := (activation - threshold) / noise
	if diff > 20 {
		return 1.0
	}
	if diff < -20 {
		return 0.0
	}
	return 1.0 / (1.0 + math.Exp(-diff))
}

// Reinforce adds a new access trace to the memory, maintaining the
// maximum trace limit by removing oldest traces when necessary.
func (m *ACTRMemory) Reinforce(now time.Time, accessType AccessType, context string) {
	trace := AccessTrace{
		AccessedAt: now,
		AccessType: accessType,
		Context:    context,
	}

	m.Traces = append(m.Traces, trace)
	m.AccessCount++

	// Enforce max traces limit
	if m.MaxTraces > 0 && len(m.Traces) > m.MaxTraces {
		m.Traces = m.Traces[len(m.Traces)-m.MaxTraces:]
	}
}

// UpdateDecay updates the decay parameters using Bayesian learning.
// When a retrieval succeeds (wasUseful=true) after a given age, we
// update the Beta distribution to reflect the observed decay rate.
func (m *ACTRMemory) UpdateDecay(ageAtRetrieval float64, wasUseful bool) {
	if ageAtRetrieval <= 0 {
		return
	}

	// Learning rate for decay adaptation
	learningRate := 0.1

	if wasUseful {
		// Successful retrieval after this age suggests slower decay
		// Decrease mean by increasing β (mean = α/(α+β))
		m.DecayBeta += learningRate * m.DecayMean()
	} else {
		// Failed retrieval suggests faster decay
		// Increase mean by increasing α
		m.DecayAlpha += learningRate * (1.0 - m.DecayMean())
	}
}

// =============================================================================
// MD.2.3 DomainDecayParams Type
// =============================================================================

// DomainDecayParams contains the decay parameter priors for a specific domain.
// Each domain has different memory decay characteristics based on the type
// of knowledge being stored.
type DomainDecayParams struct {
	DecayAlpha        float64 `json:"decay_alpha"`
	DecayBeta         float64 `json:"decay_beta"`
	BaseOffsetMean    float64 `json:"base_offset_mean"`
	BaseOffsetVar     float64 `json:"base_offset_var"`
	EffectiveSamples  float64 `json:"effective_samples"`
}

// DefaultDomainDecay returns the default decay parameters for each domain.
// These priors are based on cognitive expectations for different knowledge types:
//   - Domain 2 (Academic): Beta(3,7) → E[d]=0.3 (slower decay, longer retention)
//   - Domain 3 (Architect): Beta(4,6) → E[d]=0.4 (moderate decay)
//   - Domain 4 (Engineer): Beta(6,4) → E[d]=0.6 (faster decay, task-specific)
//   - Other domains: Beta(5,5) → E[d]=0.5 (standard ACT-R default)
func DefaultDomainDecay(domain int) DomainDecayParams {
	switch domain {
	case 2: // Academic
		return DomainDecayParams{
			DecayAlpha:       3.0,
			DecayBeta:        7.0,
			BaseOffsetMean:   0.0,
			BaseOffsetVar:    1.0,
			EffectiveSamples: 10.0,
		}
	case 3: // Architect
		return DomainDecayParams{
			DecayAlpha:       4.0,
			DecayBeta:        6.0,
			BaseOffsetMean:   0.0,
			BaseOffsetVar:    1.0,
			EffectiveSamples: 10.0,
		}
	case 4: // Engineer
		return DomainDecayParams{
			DecayAlpha:       6.0,
			DecayBeta:        4.0,
			BaseOffsetMean:   0.0,
			BaseOffsetVar:    1.0,
			EffectiveSamples: 10.0,
		}
	default:
		return DomainDecayParams{
			DecayAlpha:       5.0,
			DecayBeta:        5.0,
			BaseOffsetMean:   0.0,
			BaseOffsetVar:    1.0,
			EffectiveSamples: 10.0,
		}
	}
}

// Mean returns the expected decay value: E[d] = α/(α+β)
func (p DomainDecayParams) Mean() float64 {
	if p.DecayAlpha+p.DecayBeta == 0 {
		return 0.5
	}
	return p.DecayAlpha / (p.DecayAlpha + p.DecayBeta)
}

// =============================================================================
// Helper Functions
// =============================================================================

// gammaRand generates a random sample from a Gamma distribution.
// Uses the Marsaglia and Tsang method for α ≥ 1, and transformation
// for α < 1.
func gammaRand(alpha float64) float64 {
	if alpha <= 0 {
		return 0
	}

	if alpha < 1 {
		// Use transformation: if X ~ Gamma(α+1, 1), then X*U^(1/α) ~ Gamma(α, 1)
		return gammaRand(alpha+1) * math.Pow(rand.Float64(), 1.0/alpha)
	}

	// Marsaglia and Tsang method
	d := alpha - 1.0/3.0
	c := 1.0 / math.Sqrt(9.0*d)

	for {
		x := rand.NormFloat64()
		v := 1.0 + c*x
		if v <= 0 {
			continue
		}

		v = v * v * v
		u := rand.Float64()
		x2 := x * x

		if u < 1.0-0.0331*x2*x2 {
			return d * v
		}

		if math.Log(u) < 0.5*x2+d*(1.0-v+math.Log(v)) {
			return d * v
		}
	}
}
