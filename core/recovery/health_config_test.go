package recovery

import (
	"testing"
	"time"
)

func TestDefaultHealthWeights(t *testing.T) {
	w := DefaultHealthWeights()

	if w.HeartbeatWeight != 0.35 {
		t.Errorf("HeartbeatWeight = %v, want 0.35", w.HeartbeatWeight)
	}
	if w.ProgressWeight != 0.30 {
		t.Errorf("ProgressWeight = %v, want 0.30", w.ProgressWeight)
	}
	if w.RepetitionWeight != 0.20 {
		t.Errorf("RepetitionWeight = %v, want 0.20", w.RepetitionWeight)
	}
	if w.ResourceWeight != 0.15 {
		t.Errorf("ResourceWeight = %v, want 0.15", w.ResourceWeight)
	}
}

func TestDefaultHealthThresholds(t *testing.T) {
	th := DefaultHealthThresholds()

	if th.HeartbeatStaleAfter != 30*time.Second {
		t.Errorf("HeartbeatStaleAfter = %v, want 30s", th.HeartbeatStaleAfter)
	}
	if th.ProgressWindowSize != 20 {
		t.Errorf("ProgressWindowSize = %d, want 20", th.ProgressWindowSize)
	}
	if th.RepetitionMinCycles != 3 {
		t.Errorf("RepetitionMinCycles = %d, want 3", th.RepetitionMinCycles)
	}
	if th.HealthyThreshold != 0.7 {
		t.Errorf("HealthyThreshold = %v, want 0.7", th.HealthyThreshold)
	}
	if th.WarningThreshold != 0.4 {
		t.Errorf("WarningThreshold = %v, want 0.4", th.WarningThreshold)
	}
}

func TestHealthWeights_Validate(t *testing.T) {
	tests := []struct {
		name    string
		weights HealthWeights
		want    bool
	}{
		{
			name:    "default weights are valid",
			weights: DefaultHealthWeights(),
			want:    true,
		},
		{
			name: "sum equals 1.0",
			weights: HealthWeights{
				HeartbeatWeight:  0.25,
				ProgressWeight:   0.25,
				RepetitionWeight: 0.25,
				ResourceWeight:   0.25,
			},
			want: true,
		},
		{
			name: "sum too low",
			weights: HealthWeights{
				HeartbeatWeight:  0.1,
				ProgressWeight:   0.1,
				RepetitionWeight: 0.1,
				ResourceWeight:   0.1,
			},
			want: false,
		},
		{
			name: "sum too high",
			weights: HealthWeights{
				HeartbeatWeight:  0.5,
				ProgressWeight:   0.5,
				RepetitionWeight: 0.5,
				ResourceWeight:   0.5,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.weights.Validate(); got != tt.want {
				t.Errorf("Validate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHealthThresholds_Validate(t *testing.T) {
	tests := []struct {
		name       string
		thresholds HealthThresholds
		want       bool
	}{
		{
			name:       "default thresholds are valid",
			thresholds: DefaultHealthThresholds(),
			want:       true,
		},
		{
			name: "zero heartbeat stale",
			thresholds: HealthThresholds{
				HeartbeatStaleAfter: 0,
				ProgressWindowSize:  20,
				RepetitionMinCycles: 3,
				HealthyThreshold:    0.7,
				WarningThreshold:    0.4,
				StuckThreshold:      0.2,
			},
			want: false,
		},
		{
			name: "healthy below warning",
			thresholds: HealthThresholds{
				HeartbeatStaleAfter: 30 * time.Second,
				ProgressWindowSize:  20,
				RepetitionMinCycles: 3,
				HealthyThreshold:    0.3,
				WarningThreshold:    0.5,
				StuckThreshold:      0.2,
			},
			want: false,
		},
		{
			name: "warning below stuck",
			thresholds: HealthThresholds{
				HeartbeatStaleAfter: 30 * time.Second,
				ProgressWindowSize:  20,
				RepetitionMinCycles: 3,
				HealthyThreshold:    0.7,
				WarningThreshold:    0.1,
				StuckThreshold:      0.2,
			},
			want: false,
		},
		{
			name: "zero progress window",
			thresholds: HealthThresholds{
				HeartbeatStaleAfter: 30 * time.Second,
				ProgressWindowSize:  0,
				RepetitionMinCycles: 3,
				HealthyThreshold:    0.7,
				WarningThreshold:    0.4,
				StuckThreshold:      0.2,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.thresholds.Validate(); got != tt.want {
				t.Errorf("Validate() = %v, want %v", got, tt.want)
			}
		})
	}
}
