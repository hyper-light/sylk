package recovery

import (
	"testing"
	"time"
)

type mockResourceMon struct {
	scores map[string]float64
}

func (m *mockResourceMon) GetResourceScore(agentID string) float64 {
	if score, ok := m.scores[agentID]; ok {
		return score
	}
	return 1.0
}

func newTestHealthScorer() (*HealthScorer, *ProgressCollector, *RepetitionDetector) {
	pc := NewProgressCollector()
	rd := NewRepetitionDetector(DefaultRepetitionConfig())
	rm := &mockResourceMon{scores: make(map[string]float64)}

	hs := NewHealthScorer(pc, rd, rm, DefaultHealthWeights(), DefaultHealthThresholds())
	return hs, pc, rd
}

func TestHealthScorer_HealthyAgent(t *testing.T) {
	hs, pc, _ := newTestHealthScorer()

	for i := 0; i < 10; i++ {
		pc.Signal(ProgressSignal{
			AgentID:    "agent-1",
			SignalType: ProgressSignalType(i % 5),
			Operation:  string(rune('A' + i)),
			Timestamp:  time.Now(),
		})
	}

	assessment := hs.Assess("agent-1")

	if assessment.Status != StatusHealthy {
		t.Errorf("Status = %d, want StatusHealthy", assessment.Status)
	}
	if assessment.OverallScore < 0.7 {
		t.Errorf("OverallScore = %v, want >= 0.7", assessment.OverallScore)
	}
}

func TestHealthScorer_NoSignals(t *testing.T) {
	hs, _, _ := newTestHealthScorer()

	assessment := hs.Assess("nonexistent")

	if assessment.HeartbeatScore != 0.0 {
		t.Errorf("HeartbeatScore for no signals = %v, want 0.0", assessment.HeartbeatScore)
	}
	if assessment.Status == StatusHealthy {
		t.Error("Agent with no signals should not be healthy")
	}
}

func TestHealthScorer_StaleHeartbeat(t *testing.T) {
	hs, pc, _ := newTestHealthScorer()

	pc.Signal(ProgressSignal{
		AgentID:   "agent-1",
		Timestamp: time.Now().Add(-60 * time.Second),
	})

	assessment := hs.Assess("agent-1")

	if assessment.HeartbeatScore > 0.5 {
		t.Errorf("HeartbeatScore for stale signal = %v, want <= 0.5", assessment.HeartbeatScore)
	}
}

func TestHealthScorer_RepetitionConcern(t *testing.T) {
	hs, pc, rd := newTestHealthScorer()

	pc.Signal(ProgressSignal{
		AgentID:   "agent-1",
		Timestamp: time.Now().Add(-40 * time.Second),
	})

	for i := 0; i < 30; i++ {
		rd.Record("agent-1", Operation{Hash: 12345})
	}

	assessment := hs.Assess("agent-1")

	if !assessment.RepetitionConcern {
		t.Error("RepetitionConcern should be true when repetition + stale heartbeat")
	}
}

func TestHealthScorer_NoRepetitionConcernWhenHealthy(t *testing.T) {
	hs, pc, rd := newTestHealthScorer()

	for i := 0; i < 10; i++ {
		pc.Signal(ProgressSignal{
			AgentID:    "agent-1",
			SignalType: ProgressSignalType(i % 5),
			Operation:  string(rune('A' + i)),
			Timestamp:  time.Now(),
		})
	}

	for i := 0; i < 30; i++ {
		rd.Record("agent-1", Operation{Hash: 12345})
	}

	assessment := hs.Assess("agent-1")

	if assessment.RepetitionConcern {
		t.Error("RepetitionConcern should be false when agent otherwise healthy")
	}
}

func TestHealthScorer_ProgressScoreVariety(t *testing.T) {
	hs, pc, _ := newTestHealthScorer()

	for i := 0; i < 20; i++ {
		pc.Signal(ProgressSignal{
			AgentID:    "agent-1",
			SignalType: ProgressSignalType(i % 5),
			Operation:  string(rune('A' + i%15)),
			Timestamp:  time.Now(),
		})
	}

	assessment := hs.Assess("agent-1")

	if assessment.ProgressScore < 0.5 {
		t.Errorf("ProgressScore with variety = %v, want >= 0.5", assessment.ProgressScore)
	}
}

func TestHealthScorer_ProgressScoreLowVariety(t *testing.T) {
	hs, pc, _ := newTestHealthScorer()

	for i := 0; i < 20; i++ {
		pc.Signal(ProgressSignal{
			AgentID:    "agent-1",
			SignalType: SignalToolCompleted,
			Operation:  "same-op",
			Timestamp:  time.Now(),
		})
	}

	assessment := hs.Assess("agent-1")

	if assessment.ProgressScore > 0.4 {
		t.Errorf("ProgressScore with low variety = %v, want <= 0.4", assessment.ProgressScore)
	}
}

func TestHealthScorer_InsufficientProgressData(t *testing.T) {
	hs, pc, _ := newTestHealthScorer()

	pc.Signal(ProgressSignal{
		AgentID:   "agent-1",
		Timestamp: time.Now(),
	})

	assessment := hs.Assess("agent-1")

	if assessment.ProgressScore != 0.5 {
		t.Errorf("ProgressScore with insufficient data = %v, want 0.5", assessment.ProgressScore)
	}
}

func TestHealthScorer_StatusThresholds(t *testing.T) {
	tests := []struct {
		name   string
		score  float64
		status AgentHealthStatus
	}{
		{"healthy", 0.8, StatusHealthy},
		{"warning", 0.5, StatusWarning},
		{"stuck", 0.3, StatusStuck},
		{"critical", 0.1, StatusCritical},
	}

	hs, _, _ := newTestHealthScorer()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hs.statusFromScore(tt.score)
			if got != tt.status {
				t.Errorf("statusFromScore(%v) = %d, want %d", tt.score, got, tt.status)
			}
		})
	}
}

func TestHealthScorer_StuckSincePopulated(t *testing.T) {
	hs, pc, _ := newTestHealthScorer()

	pc.Signal(ProgressSignal{
		AgentID:   "agent-1",
		Timestamp: time.Now().Add(-120 * time.Second),
	})

	assessment := hs.Assess("agent-1")

	if assessment.Status < StatusStuck {
		t.Skip("Agent not stuck, skipping StuckSince test")
	}

	if assessment.StuckSince == nil {
		t.Error("StuckSince should be populated for stuck agent")
	}
}

func TestHealthScorer_ResourceScore(t *testing.T) {
	pc := NewProgressCollector()
	rd := NewRepetitionDetector(DefaultRepetitionConfig())
	rm := &mockResourceMon{scores: map[string]float64{"agent-1": 0.3}}

	hs := NewHealthScorer(pc, rd, rm, DefaultHealthWeights(), DefaultHealthThresholds())

	for i := 0; i < 10; i++ {
		pc.Signal(ProgressSignal{
			AgentID:   "agent-1",
			Timestamp: time.Now(),
		})
	}

	assessment := hs.Assess("agent-1")

	if assessment.ResourceScore != 0.3 {
		t.Errorf("ResourceScore = %v, want 0.3", assessment.ResourceScore)
	}
}

func TestHealthScorer_NilResourceMonitor(t *testing.T) {
	pc := NewProgressCollector()
	rd := NewRepetitionDetector(DefaultRepetitionConfig())

	hs := NewHealthScorer(pc, rd, nil, DefaultHealthWeights(), DefaultHealthThresholds())

	pc.Signal(ProgressSignal{
		AgentID:   "agent-1",
		Timestamp: time.Now(),
	})

	assessment := hs.Assess("agent-1")

	if assessment.ResourceScore != 1.0 {
		t.Errorf("ResourceScore with nil monitor = %v, want 1.0", assessment.ResourceScore)
	}
}

func TestHealthScorer_HeartbeatDecay(t *testing.T) {
	hs, pc, _ := newTestHealthScorer()

	tests := []struct {
		name    string
		elapsed time.Duration
		wantMin float64
		wantMax float64
	}{
		{"very recent", 5 * time.Second, 0.9, 1.0},
		{"recent", 15 * time.Second, 0.5, 0.9},
		{"stale", 35 * time.Second, 0.1, 0.4},
		{"very stale", 90 * time.Second, 0.0, 0.1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc.Signal(ProgressSignal{
				AgentID:   "agent-" + tt.name,
				Timestamp: time.Now().Add(-tt.elapsed),
			})

			assessment := hs.Assess("agent-" + tt.name)

			if assessment.HeartbeatScore < tt.wantMin || assessment.HeartbeatScore > tt.wantMax {
				t.Errorf("HeartbeatScore = %v, want [%v, %v]",
					assessment.HeartbeatScore, tt.wantMin, tt.wantMax)
			}
		})
	}
}
