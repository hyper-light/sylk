package vectorgraphdb

import (
	"math"
	"sync"
	"time"
)

type TemporalScorer struct {
	mu             sync.RWMutex
	halfLifeDays   float64
	semanticWeight float64
	temporalWeight float64
}

func NewTemporalScorer(halfLifeDays, semanticWeight, temporalWeight float64) *TemporalScorer {
	if halfLifeDays <= 0 {
		halfLifeDays = 7.0
	}
	total := semanticWeight + temporalWeight
	if total <= 0 {
		semanticWeight, temporalWeight = 0.8, 0.2
		total = 1.0
	}
	return &TemporalScorer{
		halfLifeDays:   halfLifeDays,
		semanticWeight: semanticWeight / total,
		temporalWeight: temporalWeight / total,
	}
}

func (t *TemporalScorer) Score(semanticSim float64, nodeTimestamp, queryTime time.Time) float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	ageDays := queryTime.Sub(nodeTimestamp).Hours() / 24.0
	if ageDays < 0 {
		ageDays = 0
	}

	temporalScore := math.Exp(-ageDays * math.Ln2 / t.halfLifeDays)
	return t.semanticWeight*semanticSim + t.temporalWeight*temporalScore
}

func (t *TemporalScorer) ScoreRecencyQuery(semanticSim float64, nodeTimestamp, queryTime time.Time) float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	ageDays := queryTime.Sub(nodeTimestamp).Hours() / 24.0
	if ageDays < 0 {
		ageDays = 0
	}

	recencyBoost := 1.0 / (1.0 + ageDays/t.halfLifeDays)
	return semanticSim * recencyBoost
}

func (t *TemporalScorer) BatchScore(semanticSims []float64, timestamps []time.Time, queryTime time.Time) []float64 {
	scores := make([]float64, len(semanticSims))
	for i := range semanticSims {
		if i < len(timestamps) {
			scores[i] = t.Score(semanticSims[i], timestamps[i], queryTime)
		}
	}
	return scores
}

func (t *TemporalScorer) SetHalfLife(days float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if days > 0 {
		t.halfLifeDays = days
	}
}

func (t *TemporalScorer) SetWeights(semantic, temporal float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	total := semantic + temporal
	if total > 0 {
		t.semanticWeight = semantic / total
		t.temporalWeight = temporal / total
	}
}

func (t *TemporalScorer) GetConfig() (halfLife, semanticW, temporalW float64) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.halfLifeDays, t.semanticWeight, t.temporalWeight
}

func (t *TemporalScorer) DeriveHalfLifeFromData(timestamps []time.Time) float64 {
	if len(timestamps) < 2 {
		return 7.0
	}

	var minT, maxT time.Time
	for i, ts := range timestamps {
		if i == 0 || ts.Before(minT) {
			minT = ts
		}
		if i == 0 || ts.After(maxT) {
			maxT = ts
		}
	}

	rangeHours := maxT.Sub(minT).Hours()
	rangeDays := rangeHours / 24.0

	halfLife := rangeDays / 4.0
	return math.Max(1.0, math.Min(365.0, halfLife))
}
