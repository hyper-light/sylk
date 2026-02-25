package escalation

import (
	"time"

	"github.com/adalundhe/sylk/core/handoff"
)

// EscalationReason identifies why an escalation was triggered.
type EscalationReason int

const (
	ReasonLowConfidence    EscalationReason = iota
	ReasonRepeatedFailure
	ReasonScopeExceeded
	ReasonUserQuestion
	ReasonQualityDegrading
	ReasonDimensionCritical
	ReasonConsensusFailed
)

// String returns the string representation of an EscalationReason.
func (r EscalationReason) String() string {
	switch r {
	case ReasonLowConfidence:
		return "low_confidence"
	case ReasonRepeatedFailure:
		return "repeated_failure"
	case ReasonScopeExceeded:
		return "scope_exceeded"
	case ReasonUserQuestion:
		return "user_question"
	case ReasonQualityDegrading:
		return "quality_degrading"
	case ReasonDimensionCritical:
		return "dimension_critical"
	case ReasonConsensusFailed:
		return "consensus_failed"
	default:
		return "unknown"
	}
}

// SuggestedAction identifies the recommended action for an escalation.
type SuggestedAction int

const (
	ActionReassign SuggestedAction = iota
	ActionReplan
	ActionAskUser
	ActionHandoff
	ActionAbort
)

// String returns the string representation of a SuggestedAction.
func (a SuggestedAction) String() string {
	switch a {
	case ActionReassign:
		return "reassign"
	case ActionReplan:
		return "replan"
	case ActionAskUser:
		return "ask_user"
	case ActionHandoff:
		return "handoff"
	case ActionAbort:
		return "abort"
	default:
		return "unknown"
	}
}

// EscalationTarget describes where and why to escalate.
type EscalationTarget struct {
	TargetAgent     string           `json:"target_agent"`
	Reason          EscalationReason `json:"reason"`
	Priority        int              `json:"priority"`
	SuggestedAction SuggestedAction  `json:"suggested_action"`
	Context         map[string]any   `json:"context,omitempty"`
}

// CategoryPolicy defines escalation thresholds for an agent category.
type CategoryPolicy struct {
	CompositeThreshold  float64  `json:"composite_threshold"`
	CriticalDimensions []string `json:"critical_dimensions,omitempty"`
}

// EscalationPolicy configures escalation behavior.
type EscalationPolicy struct {
	CategoryThresholds  map[handoff.AgentCategory]CategoryPolicy `json:"category_thresholds"`
	DimensionThresholds map[string]float64                       `json:"dimension_thresholds,omitempty"`
	MaxEscalationDepth  int                                      `json:"max_escalation_depth"`
	CooldownDuration    time.Duration                            `json:"cooldown_duration"`
}

// DefaultEscalationPolicy returns the default policy with thresholds derived
// from agent categories.
func DefaultEscalationPolicy() *EscalationPolicy {
	return &EscalationPolicy{
		CategoryThresholds: map[handoff.AgentCategory]CategoryPolicy{
			handoff.CategoryPipeline: {
				CompositeThreshold: 0.5,
				CriticalDimensions: []string{"correctness"},
			},
			handoff.CategoryStandalone: {
				CompositeThreshold: 0.6,
			},
			handoff.CategoryKnowledge: {
				CompositeThreshold: 0.4,
			},
		},
		DimensionThresholds: map[string]float64{
			"correctness": 0.3,
		},
		MaxEscalationDepth: 3,
		CooldownDuration:   30 * time.Second,
	}
}

// EscalationEvent records a single escalation occurrence.
type EscalationEvent struct {
	TaskID     string           `json:"task_id"`
	Confidence float64          `json:"confidence"`
	Timestamp  time.Time        `json:"timestamp"`
	Reason     EscalationReason `json:"reason"`
	Depth      int              `json:"depth"`
}

// EscalationHistory is a bounded ring buffer of escalation events.
type EscalationHistory struct {
	entries    []EscalationEvent
	maxEntries int
	head       int
	count      int
}

// NewEscalationHistory creates a history with the given capacity.
func NewEscalationHistory(maxEntries int) *EscalationHistory {
	if maxEntries <= 0 {
		maxEntries = 64
	}
	return &EscalationHistory{
		entries:    make([]EscalationEvent, maxEntries),
		maxEntries: maxEntries,
	}
}

// Record adds an event to the history ring buffer.
func (h *EscalationHistory) Record(event EscalationEvent) {
	h.entries[h.head] = event
	h.head = (h.head + 1) % h.maxEntries
	if h.count < h.maxEntries {
		h.count++
	}
}

// LastEscalationFor returns the most recent escalation for the given task, or nil.
func (h *EscalationHistory) LastEscalationFor(taskID string) *EscalationEvent {
	for i := range h.count {
		idx := (h.head - 1 - i + h.maxEntries) % h.maxEntries
		if h.entries[idx].TaskID == taskID {
			e := h.entries[idx]
			return &e
		}
	}
	return nil
}

// RecentForTask returns the n most recent events for the given task.
func (h *EscalationHistory) RecentForTask(taskID string, n int) []EscalationEvent {
	var result []EscalationEvent
	for i := range h.count {
		if len(result) >= n {
			break
		}
		idx := (h.head - 1 - i + h.maxEntries) % h.maxEntries
		if h.entries[idx].TaskID == taskID {
			result = append(result, h.entries[idx])
		}
	}
	return result
}

// DetermineEscalation evaluates whether an escalation is warranted.
// Pure function — evaluates confidence against policy thresholds.
func DetermineEscalation(
	confidence *ConfidenceLevel,
	weights ConfidenceWeights,
	policy *EscalationPolicy,
	category handoff.AgentCategory,
	history *EscalationHistory,
	depth int,
) (*EscalationTarget, bool) {
	if confidence == nil || policy == nil {
		return nil, false
	}

	// Check 1: depth limit → abort
	if depth >= policy.MaxEscalationDepth {
		return &EscalationTarget{
			TargetAgent:     "orchestrator",
			Reason:          ReasonRepeatedFailure,
			Priority:        100,
			SuggestedAction: ActionAbort,
			Context:         map[string]any{"depth": depth},
		}, true
	}

	// Check 2: cooldown
	if history != nil {
		last := history.LastEscalationFor(confidence.TaskID)
		if last != nil && time.Since(last.Timestamp) < policy.CooldownDuration {
			return nil, false
		}
	}

	// Check 3: per-dimension critical thresholds
	dimValues := map[string]float64{
		"correctness":  confidence.Correctness,
		"completeness": confidence.Completeness,
		"quality":      confidence.Quality,
		"integration":  confidence.Integration,
	}
	for k, v := range confidence.Dimensions {
		dimValues[k] = v
	}
	for dim, threshold := range policy.DimensionThresholds {
		if val, ok := dimValues[dim]; ok && val < threshold {
			return &EscalationTarget{
				TargetAgent:     "orchestrator",
				Reason:          ReasonDimensionCritical,
				Priority:        90,
				SuggestedAction: ActionReplan,
				Context:         map[string]any{"dimension": dim, "value": val, "threshold": threshold},
			}, true
		}
	}

	// Check 4: composite below category threshold
	composite := confidence.Composite(weights)
	if catPolicy, ok := policy.CategoryThresholds[category]; ok {
		if composite < catPolicy.CompositeThreshold {
			return &EscalationTarget{
				TargetAgent:     "orchestrator",
				Reason:          ReasonLowConfidence,
				Priority:        80,
				SuggestedAction: ActionReplan,
				Context:         map[string]any{"composite": composite, "threshold": catPolicy.CompositeThreshold},
			}, true
		}
	}

	// Check 5: quality degrading trend (3+ consecutive drops)
	if history != nil {
		recent := history.RecentForTask(confidence.TaskID, 3)
		if len(recent) >= 3 {
			allDropping := true
			for i := 1; i < len(recent); i++ {
				if recent[i-1].Confidence >= recent[i].Confidence {
					allDropping = false
					break
				}
			}
			if allDropping {
				return &EscalationTarget{
					TargetAgent:     "orchestrator",
					Reason:          ReasonQualityDegrading,
					Priority:        85,
					SuggestedAction: ActionReplan,
				}, true
			}
		}
	}

	return nil, false
}
