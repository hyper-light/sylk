package vectorgraphdb

import (
	"math"
	"testing"
	"time"
)

func TestTemporalScorer_Score(t *testing.T) {
	scorer := NewTemporalScorer(7.0, 0.8, 0.2)
	now := time.Now()

	tests := []struct {
		name          string
		semanticSim   float64
		nodeTimestamp time.Time
		wantMin       float64
		wantMax       float64
	}{
		{
			name:          "same time - full temporal score",
			semanticSim:   1.0,
			nodeTimestamp: now,
			wantMin:       0.99,
			wantMax:       1.01,
		},
		{
			name:          "7 days ago - half temporal score",
			semanticSim:   1.0,
			nodeTimestamp: now.Add(-7 * 24 * time.Hour),
			wantMin:       0.89,
			wantMax:       0.91,
		},
		{
			name:          "14 days ago - quarter temporal score",
			semanticSim:   1.0,
			nodeTimestamp: now.Add(-14 * 24 * time.Hour),
			wantMin:       0.84,
			wantMax:       0.86,
		},
		{
			name:          "zero semantic sim",
			semanticSim:   0.0,
			nodeTimestamp: now,
			wantMin:       0.19,
			wantMax:       0.21,
		},
		{
			name:          "future timestamp treated as zero age",
			semanticSim:   1.0,
			nodeTimestamp: now.Add(24 * time.Hour),
			wantMin:       0.99,
			wantMax:       1.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := scorer.Score(tt.semanticSim, tt.nodeTimestamp, now)
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("Score() = %v, want between %v and %v", score, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestTemporalScorer_ScoreRecencyQuery(t *testing.T) {
	scorer := NewTemporalScorer(7.0, 0.8, 0.2)
	now := time.Now()

	tests := []struct {
		name          string
		semanticSim   float64
		nodeTimestamp time.Time
		wantMin       float64
		wantMax       float64
	}{
		{
			name:          "same time - full boost",
			semanticSim:   1.0,
			nodeTimestamp: now,
			wantMin:       0.99,
			wantMax:       1.01,
		},
		{
			name:          "7 days ago - half boost",
			semanticSim:   1.0,
			nodeTimestamp: now.Add(-7 * 24 * time.Hour),
			wantMin:       0.49,
			wantMax:       0.51,
		},
		{
			name:          "14 days ago - third boost",
			semanticSim:   1.0,
			nodeTimestamp: now.Add(-14 * 24 * time.Hour),
			wantMin:       0.32,
			wantMax:       0.34,
		},
		{
			name:          "zero semantic sim stays zero",
			semanticSim:   0.0,
			nodeTimestamp: now,
			wantMin:       -0.01,
			wantMax:       0.01,
		},
		{
			name:          "future timestamp treated as zero age",
			semanticSim:   1.0,
			nodeTimestamp: now.Add(24 * time.Hour),
			wantMin:       0.99,
			wantMax:       1.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := scorer.ScoreRecencyQuery(tt.semanticSim, tt.nodeTimestamp, now)
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("ScoreRecencyQuery() = %v, want between %v and %v", score, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestTemporalScorer_BatchScore(t *testing.T) {
	scorer := NewTemporalScorer(7.0, 0.8, 0.2)
	now := time.Now()

	semanticSims := []float64{1.0, 0.8, 0.5}
	timestamps := []time.Time{
		now,
		now.Add(-7 * 24 * time.Hour),
		now.Add(-14 * 24 * time.Hour),
	}

	scores := scorer.BatchScore(semanticSims, timestamps, now)

	if len(scores) != 3 {
		t.Fatalf("BatchScore() returned %d scores, want 3", len(scores))
	}

	// First should be highest (recent + high semantic)
	if scores[0] < scores[1] || scores[0] < scores[2] {
		t.Errorf("scores[0] = %v should be highest, got %v", scores[0], scores)
	}

	// Verify each score is reasonable
	for i, s := range scores {
		if s < 0 || s > 1.1 {
			t.Errorf("scores[%d] = %v, out of expected range [0, 1.1]", i, s)
		}
	}
}

func TestTemporalScorer_BatchScore_MismatchedLengths(t *testing.T) {
	scorer := NewTemporalScorer(7.0, 0.8, 0.2)
	now := time.Now()

	semanticSims := []float64{1.0, 0.8, 0.5, 0.3}
	timestamps := []time.Time{now, now.Add(-7 * 24 * time.Hour)} // shorter

	scores := scorer.BatchScore(semanticSims, timestamps, now)

	if len(scores) != 4 {
		t.Fatalf("BatchScore() returned %d scores, want 4", len(scores))
	}

	// First two should have scores
	if scores[0] == 0 || scores[1] == 0 {
		t.Errorf("first two scores should be non-zero: %v", scores)
	}

	// Last two should be zero (no timestamp)
	if scores[2] != 0 || scores[3] != 0 {
		t.Errorf("last two scores should be zero without timestamps: %v", scores)
	}
}

func TestTemporalScorer_DecayCurve(t *testing.T) {
	scorer := NewTemporalScorer(7.0, 0.0, 1.0) // pure temporal scoring
	now := time.Now()

	// Verify exponential decay property: score halves every half-life
	score0 := scorer.Score(1.0, now, now)
	score7 := scorer.Score(1.0, now.Add(-7*24*time.Hour), now)
	score14 := scorer.Score(1.0, now.Add(-14*24*time.Hour), now)
	score21 := scorer.Score(1.0, now.Add(-21*24*time.Hour), now)

	// At t=0, score should be 1.0
	if math.Abs(score0-1.0) > 0.01 {
		t.Errorf("score at t=0 = %v, want 1.0", score0)
	}

	// At t=halfLife, score should be 0.5
	if math.Abs(score7-0.5) > 0.01 {
		t.Errorf("score at t=7d = %v, want 0.5", score7)
	}

	// At t=2*halfLife, score should be 0.25
	if math.Abs(score14-0.25) > 0.01 {
		t.Errorf("score at t=14d = %v, want 0.25", score14)
	}

	// At t=3*halfLife, score should be 0.125
	if math.Abs(score21-0.125) > 0.01 {
		t.Errorf("score at t=21d = %v, want 0.125", score21)
	}

	// Verify monotonic decay
	if score0 <= score7 || score7 <= score14 || score14 <= score21 {
		t.Errorf("scores should decay monotonically: %v > %v > %v > %v",
			score0, score7, score14, score21)
	}
}

func TestTemporalScorer_DeriveHalfLifeFromData(t *testing.T) {
	scorer := NewTemporalScorer(7.0, 0.8, 0.2)
	now := time.Now()

	tests := []struct {
		name       string
		timestamps []time.Time
		wantMin    float64
		wantMax    float64
	}{
		{
			name:       "empty timestamps - default",
			timestamps: []time.Time{},
			wantMin:    6.9,
			wantMax:    7.1,
		},
		{
			name:       "single timestamp - default",
			timestamps: []time.Time{now},
			wantMin:    6.9,
			wantMax:    7.1,
		},
		{
			name:       "28 day range - 7 day half-life",
			timestamps: []time.Time{now, now.Add(-28 * 24 * time.Hour)},
			wantMin:    6.9,
			wantMax:    7.1,
		},
		{
			name:       "120 day range - 30 day half-life",
			timestamps: []time.Time{now, now.Add(-120 * 24 * time.Hour)},
			wantMin:    29.9,
			wantMax:    30.1,
		},
		{
			name:       "very short range - minimum 1 day",
			timestamps: []time.Time{now, now.Add(-1 * time.Hour)},
			wantMin:    0.9,
			wantMax:    1.1,
		},
		{
			name: "very long range - maximum 365 days",
			timestamps: []time.Time{
				now,
				now.Add(-2000 * 24 * time.Hour),
			},
			wantMin: 364.9,
			wantMax: 365.1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			halfLife := scorer.DeriveHalfLifeFromData(tt.timestamps)
			if halfLife < tt.wantMin || halfLife > tt.wantMax {
				t.Errorf("DeriveHalfLifeFromData() = %v, want between %v and %v",
					halfLife, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestTemporalScorer_SetHalfLife(t *testing.T) {
	scorer := NewTemporalScorer(7.0, 0.8, 0.2)

	scorer.SetHalfLife(14.0)
	halfLife, _, _ := scorer.GetConfig()
	if halfLife != 14.0 {
		t.Errorf("SetHalfLife(14.0) resulted in %v", halfLife)
	}

	// Invalid value should be ignored
	scorer.SetHalfLife(-5.0)
	halfLife, _, _ = scorer.GetConfig()
	if halfLife != 14.0 {
		t.Errorf("SetHalfLife(-5.0) changed value to %v", halfLife)
	}

	scorer.SetHalfLife(0)
	halfLife, _, _ = scorer.GetConfig()
	if halfLife != 14.0 {
		t.Errorf("SetHalfLife(0) changed value to %v", halfLife)
	}
}

func TestTemporalScorer_SetWeights(t *testing.T) {
	scorer := NewTemporalScorer(7.0, 0.8, 0.2)

	scorer.SetWeights(0.6, 0.4)
	_, semantic, temporal := scorer.GetConfig()
	if math.Abs(semantic-0.6) > 0.01 || math.Abs(temporal-0.4) > 0.01 {
		t.Errorf("SetWeights(0.6, 0.4) resulted in semantic=%v, temporal=%v",
			semantic, temporal)
	}

	// Weights should be normalized
	scorer.SetWeights(3.0, 1.0)
	_, semantic, temporal = scorer.GetConfig()
	if math.Abs(semantic-0.75) > 0.01 || math.Abs(temporal-0.25) > 0.01 {
		t.Errorf("SetWeights(3.0, 1.0) should normalize to 0.75/0.25, got %v/%v",
			semantic, temporal)
	}

	// Invalid weights should be ignored
	scorer.SetWeights(0, 0)
	_, semantic, temporal = scorer.GetConfig()
	if math.Abs(semantic-0.75) > 0.01 || math.Abs(temporal-0.25) > 0.01 {
		t.Errorf("SetWeights(0, 0) changed values to %v/%v", semantic, temporal)
	}
}

func TestTemporalScorer_DefaultValues(t *testing.T) {
	// Test with all invalid inputs - should use defaults
	scorer := NewTemporalScorer(-1.0, 0, 0)

	halfLife, semantic, temporal := scorer.GetConfig()

	if halfLife != 7.0 {
		t.Errorf("default halfLife = %v, want 7.0", halfLife)
	}
	if math.Abs(semantic-0.8) > 0.01 {
		t.Errorf("default semanticWeight = %v, want 0.8", semantic)
	}
	if math.Abs(temporal-0.2) > 0.01 {
		t.Errorf("default temporalWeight = %v, want 0.2", temporal)
	}
}

func TestTemporalScorer_WeightNormalization(t *testing.T) {
	// Weights should always sum to 1.0
	scorer := NewTemporalScorer(7.0, 0.5, 0.5)
	_, semantic, temporal := scorer.GetConfig()

	sum := semantic + temporal
	if math.Abs(sum-1.0) > 0.001 {
		t.Errorf("weights sum = %v, want 1.0", sum)
	}

	scorer2 := NewTemporalScorer(7.0, 100, 300)
	_, semantic2, temporal2 := scorer2.GetConfig()

	sum2 := semantic2 + temporal2
	if math.Abs(sum2-1.0) > 0.001 {
		t.Errorf("weights sum = %v, want 1.0 (after normalization)", sum2)
	}
	if math.Abs(semantic2-0.25) > 0.01 || math.Abs(temporal2-0.75) > 0.01 {
		t.Errorf("expected 0.25/0.75, got %v/%v", semantic2, temporal2)
	}
}
