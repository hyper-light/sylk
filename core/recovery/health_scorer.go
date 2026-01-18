package recovery

import (
	"math"
	"time"
)

type ResourceMonitor interface {
	GetResourceScore(agentID string) float64
}

type HealthScorer struct {
	collector     *ProgressCollector
	repetitionDet *RepetitionDetector
	resourceMon   ResourceMonitor
	weights       HealthWeights
	thresholds    HealthThresholds
}

func NewHealthScorer(
	collector *ProgressCollector,
	repetitionDet *RepetitionDetector,
	resourceMon ResourceMonitor,
	weights HealthWeights,
	thresholds HealthThresholds,
) *HealthScorer {
	return &HealthScorer{
		collector:     collector,
		repetitionDet: repetitionDet,
		resourceMon:   resourceMon,
		weights:       weights,
		thresholds:    thresholds,
	}
}

func (h *HealthScorer) Assess(agentID string) HealthAssessment {
	now := time.Now()

	heartbeat := h.scoreHeartbeat(agentID, now)
	progress := h.scoreProgress(agentID)
	repetition := h.scoreRepetition(agentID)
	resource := h.scoreResource(agentID)

	repetitionConcern := h.isRepetitionConcerning(repetition, heartbeat, progress)
	effectiveRepetition := h.effectiveRepetitionScore(repetition, repetitionConcern)

	overall := h.calculateOverallScore(heartbeat, progress, effectiveRepetition, resource)

	return h.buildAssessment(agentID, now, overall, heartbeat, progress, repetition, resource, repetitionConcern)
}

func (h *HealthScorer) isRepetitionConcerning(repetition, heartbeat, progress float64) bool {
	otherSignalsConcerning := heartbeat < 0.5 || progress < 0.5
	return repetition < 0.5 && otherSignalsConcerning
}

func (h *HealthScorer) effectiveRepetitionScore(repetition float64, concerning bool) float64 {
	if concerning {
		return repetition
	}
	return 1.0
}

func (h *HealthScorer) calculateOverallScore(heartbeat, progress, repetition, resource float64) float64 {
	return h.weights.HeartbeatWeight*heartbeat +
		h.weights.ProgressWeight*progress +
		h.weights.RepetitionWeight*repetition +
		h.weights.ResourceWeight*resource
}

func (h *HealthScorer) buildAssessment(
	agentID string, now time.Time, overall, heartbeat, progress, repetition, resource float64, repetitionConcern bool,
) HealthAssessment {
	assessment := HealthAssessment{
		AgentID:           agentID,
		OverallScore:      overall,
		HeartbeatScore:    heartbeat,
		ProgressScore:     progress,
		RepetitionScore:   repetition,
		ResourceScore:     resource,
		RepetitionConcern: repetitionConcern,
		Status:            h.statusFromScore(overall),
		AssessedAt:        now,
	}

	h.populateTimingInfo(&assessment, agentID, now)
	return assessment
}

func (h *HealthScorer) populateTimingInfo(assessment *HealthAssessment, agentID string, now time.Time) {
	if last, ok := h.collector.LastSignalTime(agentID); ok {
		assessment.LastProgress = last
	}

	if assessment.Status >= StatusStuck {
		stuckTime := now.Add(-h.thresholds.HeartbeatStaleAfter)
		assessment.StuckSince = &stuckTime
	}
}

func (h *HealthScorer) scoreHeartbeat(agentID string, now time.Time) float64 {
	last, ok := h.collector.LastSignalTime(agentID)
	if !ok {
		return 0.0
	}

	elapsed := now.Sub(last)
	return h.heartbeatScoreFromElapsed(elapsed)
}

func (h *HealthScorer) heartbeatScoreFromElapsed(elapsed time.Duration) float64 {
	staleAfter := h.thresholds.HeartbeatStaleAfter

	if elapsed < staleAfter/3 {
		return 1.0
	}
	if elapsed < staleAfter {
		return 1.0 - 0.7*(float64(elapsed)/float64(staleAfter))
	}
	overdue := elapsed - staleAfter
	return 0.3 * math.Exp(-float64(overdue)/float64(staleAfter))
}

func (h *HealthScorer) scoreProgress(agentID string) float64 {
	signals := h.collector.RecentSignals(agentID, h.thresholds.ProgressWindowSize)
	if len(signals) < 3 {
		return 0.5
	}

	typeVariety := h.measureTypeVariety(signals)
	targetVariety := h.measureTargetVariety(signals)

	return 0.5*typeVariety + 0.5*targetVariety
}

func (h *HealthScorer) measureTypeVariety(signals []ProgressSignal) float64 {
	typeSet := make(map[ProgressSignalType]bool)
	for _, sig := range signals {
		typeSet[sig.SignalType] = true
	}
	return float64(len(typeSet)) / 5.0
}

func (h *HealthScorer) measureTargetVariety(signals []ProgressSignal) float64 {
	targetSet := make(map[string]bool)
	for _, sig := range signals {
		targetSet[sig.Operation] = true
	}
	return math.Min(float64(len(targetSet))/10.0, 1.0)
}

func (h *HealthScorer) scoreRepetition(agentID string) float64 {
	return h.repetitionDet.Score(agentID)
}

func (h *HealthScorer) scoreResource(agentID string) float64 {
	if h.resourceMon == nil {
		return 1.0
	}
	return h.resourceMon.GetResourceScore(agentID)
}

func (h *HealthScorer) statusFromScore(score float64) AgentHealthStatus {
	switch {
	case score > h.thresholds.HealthyThreshold:
		return StatusHealthy
	case score > h.thresholds.WarningThreshold:
		return StatusWarning
	case score > h.thresholds.CriticalThreshold:
		return StatusStuck
	default:
		return StatusCritical
	}
}
