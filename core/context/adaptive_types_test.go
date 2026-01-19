package context

import (
	"testing"
)

func TestAdaptiveStateSerialization(t *testing.T) {
	state := NewAdaptiveState()

	data, err := state.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	// Verify size is under 2KB
	if len(data) >= 2048 {
		t.Errorf("Serialized size %d exceeds 2KB limit", len(data))
	}

	t.Logf("Serialized size: %d bytes", len(data))

	// Verify round-trip
	state2 := &AdaptiveState{}
	if err := state2.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary failed: %v", err)
	}

	if state2.Version != state.Version {
		t.Errorf("Version mismatch: got %d, want %d", state2.Version, state.Version)
	}
}

func TestRobustWeightDistribution(t *testing.T) {
	dist := newRobustDist(8, 2)

	// Test Mean
	mean := dist.Mean()
	if mean < 0.79 || mean > 0.81 {
		t.Errorf("Mean = %v, want ~0.8", mean)
	}

	// Test Variance
	variance := dist.Variance()
	if variance <= 0 {
		t.Errorf("Variance = %v, want > 0", variance)
	}

	// Test Sample produces values in [0, 1]
	for i := 0; i < 100; i++ {
		sample := dist.Sample()
		if sample < 0 || sample > 1 {
			t.Errorf("Sample = %v, want in [0, 1]", sample)
		}
	}

	// Test Confidence
	conf := dist.Confidence()
	if conf < 0 || conf > 1 {
		t.Errorf("Confidence = %v, want in [0, 1]", conf)
	}

	// Test CredibleInterval
	low, high := dist.CredibleInterval(0.95)
	if low >= high {
		t.Errorf("CredibleInterval: low=%v >= high=%v", low, high)
	}
}

func TestEpisodeObservationInferSatisfaction(t *testing.T) {
	tests := []struct {
		name string
		obs  EpisodeObservation
		want float64
	}{
		{
			name: "clean completion",
			obs: EpisodeObservation{
				TaskCompleted: true,
				FollowUpCount: 0,
			},
			want: 0.5,
		},
		{
			name: "completion with followups",
			obs: EpisodeObservation{
				TaskCompleted: true,
				FollowUpCount: 2,
			},
			want: 0.0, // 0.2 + 2*(-0.1)
		},
		{
			name: "failed task",
			obs: EpisodeObservation{
				TaskCompleted: false,
				FollowUpCount: 0,
			},
			want: -0.4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.obs.InferSatisfaction()
			if got != tt.want {
				t.Errorf("InferSatisfaction() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAdaptiveStateSampleWeights(t *testing.T) {
	state := NewAdaptiveState()
	ctx := TaskContext("test")

	weights := state.SampleWeights(ctx)

	// All weights should be in valid range
	if weights.TaskSuccess < 0 || weights.TaskSuccess > 1 {
		t.Errorf("TaskSuccess = %v, want in [0, 1]", weights.TaskSuccess)
	}
	if weights.RelevanceBonus < 0 || weights.RelevanceBonus > 1 {
		t.Errorf("RelevanceBonus = %v, want in [0, 1]", weights.RelevanceBonus)
	}
	if weights.StrugglePenalty < 0 || weights.StrugglePenalty > 1 {
		t.Errorf("StrugglePenalty = %v, want in [0, 1]", weights.StrugglePenalty)
	}
	if weights.WastePenalty < 0 || weights.WastePenalty > 1 {
		t.Errorf("WastePenalty = %v, want in [0, 1]", weights.WastePenalty)
	}
}

func TestUpdateConfig(t *testing.T) {
	cfg := DefaultUpdateConfig()

	if cfg.DecayFactor != 0.999 {
		t.Errorf("DecayFactor = %v, want 0.999", cfg.DecayFactor)
	}
	if cfg.OutlierSigma != 3.0 {
		t.Errorf("OutlierSigma = %v, want 3.0", cfg.OutlierSigma)
	}
	if cfg.MinEffectiveSamples != 10.0 {
		t.Errorf("MinEffectiveSamples = %v, want 10.0", cfg.MinEffectiveSamples)
	}
	if cfg.DriftRate != 0.001 {
		t.Errorf("DriftRate = %v, want 0.001", cfg.DriftRate)
	}
}
