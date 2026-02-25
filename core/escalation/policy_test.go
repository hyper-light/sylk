package escalation

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/handoff"
)

func TestDefaultEscalationPolicy(t *testing.T) {
	p := DefaultEscalationPolicy()

	if p.MaxEscalationDepth != 3 {
		t.Errorf("MaxEscalationDepth = %d, want 3", p.MaxEscalationDepth)
	}
	if p.CooldownDuration != 30*time.Second {
		t.Errorf("CooldownDuration = %v, want 30s", p.CooldownDuration)
	}

	pipelinePolicy, ok := p.CategoryThresholds[handoff.CategoryPipeline]
	if !ok {
		t.Fatal("missing CategoryPipeline threshold")
	}
	if pipelinePolicy.CompositeThreshold != 0.5 {
		t.Errorf("Pipeline CompositeThreshold = %v, want 0.5", pipelinePolicy.CompositeThreshold)
	}

	standalonePolicy, ok := p.CategoryThresholds[handoff.CategoryStandalone]
	if !ok {
		t.Fatal("missing CategoryStandalone threshold")
	}
	if standalonePolicy.CompositeThreshold != 0.6 {
		t.Errorf("Standalone CompositeThreshold = %v, want 0.6", standalonePolicy.CompositeThreshold)
	}

	knowledgePolicy, ok := p.CategoryThresholds[handoff.CategoryKnowledge]
	if !ok {
		t.Fatal("missing CategoryKnowledge threshold")
	}
	if knowledgePolicy.CompositeThreshold != 0.4 {
		t.Errorf("Knowledge CompositeThreshold = %v, want 0.4", knowledgePolicy.CompositeThreshold)
	}

	correctnessThreshold, ok := p.DimensionThresholds["correctness"]
	if !ok {
		t.Fatal("missing correctness dimension threshold")
	}
	if correctnessThreshold != 0.3 {
		t.Errorf("correctness DimensionThreshold = %v, want 0.3", correctnessThreshold)
	}
}

func TestEscalationHistory_Record(t *testing.T) {
	h := NewEscalationHistory(4)

	for i := range 4 {
		h.Record(EscalationEvent{
			TaskID:     "task-1",
			Confidence: float64(i) * 0.1,
			Timestamp:  time.Now(),
			Reason:     ReasonLowConfidence,
			Depth:      i,
		})
	}

	if h.count != 4 {
		t.Errorf("count = %d, want 4", h.count)
	}
}

func TestEscalationHistory_RingBufferWraps(t *testing.T) {
	capacity := 4
	h := NewEscalationHistory(capacity)

	// Record more than capacity to force wrap.
	totalEvents := capacity + 2
	for i := range totalEvents {
		h.Record(EscalationEvent{
			TaskID:     "task-1",
			Confidence: float64(i) * 0.1,
			Timestamp:  time.Now(),
			Reason:     ReasonLowConfidence,
			Depth:      i,
		})
	}

	// Count should be clamped at capacity.
	if h.count != capacity {
		t.Errorf("count = %d, want %d after wrap", h.count, capacity)
	}

	// Most recent should have Depth = totalEvents-1.
	recent := h.RecentForTask("task-1", 1)
	if len(recent) != 1 {
		t.Fatalf("RecentForTask returned %d events, want 1", len(recent))
	}
	lastDepth := totalEvents - 1
	if recent[0].Depth != lastDepth {
		t.Errorf("most recent Depth = %d, want %d", recent[0].Depth, lastDepth)
	}
}

func TestEscalationHistory_LastEscalationFor(t *testing.T) {
	h := NewEscalationHistory(16)

	h.Record(EscalationEvent{TaskID: "task-A", Confidence: 0.5, Timestamp: time.Now()})
	h.Record(EscalationEvent{TaskID: "task-B", Confidence: 0.6, Timestamp: time.Now()})
	h.Record(EscalationEvent{TaskID: "task-A", Confidence: 0.3, Timestamp: time.Now()})

	got := h.LastEscalationFor("task-A")
	if got == nil {
		t.Fatal("LastEscalationFor returned nil, want event")
	}
	if got.Confidence != 0.3 {
		t.Errorf("Confidence = %v, want 0.3 (most recent for task-A)", got.Confidence)
	}

	missing := h.LastEscalationFor("task-nonexistent")
	if missing != nil {
		t.Errorf("LastEscalationFor non-existent = %v, want nil", missing)
	}
}

func TestEscalationHistory_RecentForTask(t *testing.T) {
	h := NewEscalationHistory(16)

	h.Record(EscalationEvent{TaskID: "task-X", Confidence: 0.1, Timestamp: time.Now()})
	h.Record(EscalationEvent{TaskID: "task-Y", Confidence: 0.2, Timestamp: time.Now()})
	h.Record(EscalationEvent{TaskID: "task-X", Confidence: 0.3, Timestamp: time.Now()})
	h.Record(EscalationEvent{TaskID: "task-X", Confidence: 0.4, Timestamp: time.Now()})

	got := h.RecentForTask("task-X", 2)
	if len(got) != 2 {
		t.Fatalf("RecentForTask returned %d events, want 2", len(got))
	}
	// Most recent first.
	if got[0].Confidence != 0.4 {
		t.Errorf("got[0].Confidence = %v, want 0.4", got[0].Confidence)
	}
	if got[1].Confidence != 0.3 {
		t.Errorf("got[1].Confidence = %v, want 0.3", got[1].Confidence)
	}
}

func TestEscalationHistory_DefaultCapacity(t *testing.T) {
	h := NewEscalationHistory(0)
	// Should default to 64.
	if h.maxEntries != 64 {
		t.Errorf("maxEntries = %d, want 64 (default)", h.maxEntries)
	}
}

func TestDetermineEscalation_NilConfidence(t *testing.T) {
	policy := DefaultEscalationPolicy()
	target, ok := DetermineEscalation(nil, DefaultWeights(), policy, handoff.CategoryPipeline, nil, 0)
	if ok || target != nil {
		t.Errorf("nil confidence: got (%v, %v), want (nil, false)", target, ok)
	}
}

func TestDetermineEscalation_NilPolicy(t *testing.T) {
	cl := &ConfidenceLevel{Correctness: 0.9, Completeness: 0.9, Quality: 0.9, Integration: 0.9}
	target, ok := DetermineEscalation(cl, DefaultWeights(), nil, handoff.CategoryPipeline, nil, 0)
	if ok || target != nil {
		t.Errorf("nil policy: got (%v, %v), want (nil, false)", target, ok)
	}
}

func TestDetermineEscalation_DepthExceeded(t *testing.T) {
	policy := DefaultEscalationPolicy()
	cl := &ConfidenceLevel{Correctness: 0.9, Completeness: 0.9, Quality: 0.9, Integration: 0.9}

	target, ok := DetermineEscalation(cl, DefaultWeights(), policy, handoff.CategoryPipeline, nil, policy.MaxEscalationDepth)
	if !ok {
		t.Fatal("depth exceeded: want escalation")
	}
	if target.SuggestedAction != ActionAbort {
		t.Errorf("SuggestedAction = %v, want %v", target.SuggestedAction, ActionAbort)
	}
	if target.Reason != ReasonRepeatedFailure {
		t.Errorf("Reason = %v, want %v", target.Reason, ReasonRepeatedFailure)
	}
}

func TestDetermineEscalation_CooldownActive(t *testing.T) {
	policy := DefaultEscalationPolicy()
	cl := &ConfidenceLevel{
		Correctness:  0.1,
		Completeness: 0.1,
		Quality:      0.1,
		Integration:  0.1,
		TaskID:       "task-cool",
	}
	h := NewEscalationHistory(16)
	// Record a recent escalation within the cooldown window.
	h.Record(EscalationEvent{
		TaskID:    "task-cool",
		Timestamp: time.Now(),
		Reason:    ReasonLowConfidence,
	})

	target, ok := DetermineEscalation(cl, DefaultWeights(), policy, handoff.CategoryPipeline, h, 0)
	if ok || target != nil {
		t.Errorf("cooldown active: got (%v, %v), want (nil, false)", target, ok)
	}
}

func TestDetermineEscalation_DimensionCritical(t *testing.T) {
	policy := DefaultEscalationPolicy()
	// correctness dimension threshold is 0.3; set correctness below that.
	cl := &ConfidenceLevel{
		Correctness:  0.2,
		Completeness: 0.9,
		Quality:      0.9,
		Integration:  0.9,
		TaskID:       "task-dim",
	}

	target, ok := DetermineEscalation(cl, DefaultWeights(), policy, handoff.CategoryPipeline, nil, 0)
	if !ok {
		t.Fatal("dimension critical: want escalation")
	}
	if target.Reason != ReasonDimensionCritical {
		t.Errorf("Reason = %v, want %v", target.Reason, ReasonDimensionCritical)
	}
	if target.SuggestedAction != ActionReplan {
		t.Errorf("SuggestedAction = %v, want %v", target.SuggestedAction, ActionReplan)
	}
}

func TestDetermineEscalation_CompositeBelowThreshold(t *testing.T) {
	policy := DefaultEscalationPolicy()
	// Pipeline threshold is 0.5. Set all dimensions to 0.4 so composite = 0.4.
	// correctness = 0.4 is above the 0.3 dimension threshold, so check 3 passes.
	cl := &ConfidenceLevel{
		Correctness:  0.4,
		Completeness: 0.4,
		Quality:      0.4,
		Integration:  0.4,
		TaskID:       "task-comp",
	}

	target, ok := DetermineEscalation(cl, DefaultWeights(), policy, handoff.CategoryPipeline, nil, 0)
	if !ok {
		t.Fatal("composite below threshold: want escalation")
	}
	if target.Reason != ReasonLowConfidence {
		t.Errorf("Reason = %v, want %v", target.Reason, ReasonLowConfidence)
	}
}

func TestDetermineEscalation_HighConfidence_NoEscalation(t *testing.T) {
	policy := DefaultEscalationPolicy()
	cl := &ConfidenceLevel{
		Correctness:  0.9,
		Completeness: 0.9,
		Quality:      0.9,
		Integration:  0.9,
		TaskID:       "task-ok",
	}

	target, ok := DetermineEscalation(cl, DefaultWeights(), policy, handoff.CategoryPipeline, nil, 0)
	if ok || target != nil {
		t.Errorf("high confidence: got (%v, %v), want (nil, false)", target, ok)
	}
}

func TestDetermineEscalation_QualityDegrading(t *testing.T) {
	policy := DefaultEscalationPolicy()
	h := NewEscalationHistory(16)

	// Record 3 events with increasing confidence (most recent first in ring = decreasing over time).
	// RecentForTask returns [newest, ..., oldest].
	// The check is: recent[i-1].Confidence < recent[i].Confidence for all adjacent pairs.
	// This means each newer entry has lower confidence than the previous (degrading quality).
	// So we record events with increasing confidence over time (oldest→newest: 0.8, 0.6, 0.4).
	timestamps := []time.Time{
		time.Now().Add(-3 * time.Minute),
		time.Now().Add(-2 * time.Minute),
		time.Now().Add(-1 * time.Minute),
	}
	confidences := []float64{0.8, 0.6, 0.4}

	for i := range 3 {
		h.Record(EscalationEvent{
			TaskID:     "task-deg",
			Confidence: confidences[i],
			Timestamp:  timestamps[i],
			Reason:     ReasonLowConfidence,
		})
	}

	// The confidence itself must pass dimension and composite checks first.
	// Set all dimensions high enough to pass dimension and composite thresholds.
	cl := &ConfidenceLevel{
		Correctness:  0.9,
		Completeness: 0.9,
		Quality:      0.9,
		Integration:  0.9,
		TaskID:       "task-deg",
	}

	target, ok := DetermineEscalation(cl, DefaultWeights(), policy, handoff.CategoryPipeline, h, 0)
	if !ok {
		t.Fatal("quality degrading: want escalation")
	}
	if target.Reason != ReasonQualityDegrading {
		t.Errorf("Reason = %v, want %v", target.Reason, ReasonQualityDegrading)
	}
	if target.SuggestedAction != ActionReplan {
		t.Errorf("SuggestedAction = %v, want %v", target.SuggestedAction, ActionReplan)
	}
}

func TestEscalationReason_String(t *testing.T) {
	tests := []struct {
		reason EscalationReason
		want   string
	}{
		{ReasonLowConfidence, "low_confidence"},
		{ReasonRepeatedFailure, "repeated_failure"},
		{ReasonScopeExceeded, "scope_exceeded"},
		{ReasonUserQuestion, "user_question"},
		{ReasonQualityDegrading, "quality_degrading"},
		{ReasonDimensionCritical, "dimension_critical"},
		{ReasonConsensusFailed, "consensus_failed"},
		{EscalationReason(99), "unknown"},
	}
	for _, tt := range tests {
		got := tt.reason.String()
		if got != tt.want {
			t.Errorf("EscalationReason(%d).String() = %q, want %q", tt.reason, got, tt.want)
		}
	}
}

func TestSuggestedAction_String(t *testing.T) {
	tests := []struct {
		action SuggestedAction
		want   string
	}{
		{ActionReassign, "reassign"},
		{ActionReplan, "replan"},
		{ActionAskUser, "ask_user"},
		{ActionHandoff, "handoff"},
		{ActionAbort, "abort"},
		{SuggestedAction(99), "unknown"},
	}
	for _, tt := range tests {
		got := tt.action.String()
		if got != tt.want {
			t.Errorf("SuggestedAction(%d).String() = %q, want %q", tt.action, got, tt.want)
		}
	}
}
