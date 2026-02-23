package handoff

import (
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

func TestSignalFuser_FlushNoSignals(t *testing.T) {
	reg := NewBaselineRegistry(50)
	f := NewSignalFuser(BaselineKey{AgentType: "a", ModelID: "m"}, reg)

	result := f.Flush(0, 0)
	if result.HasData {
		t.Error("expected HasData=false with no signals")
	}
}

func TestSignalFuser_FlushProviderOnly(t *testing.T) {
	reg := NewBaselineRegistry(50)
	f := NewSignalFuser(BaselineKey{AgentType: "a", ModelID: "m"}, reg)

	f.SetProviderSignals(ProviderSignals{
		StopReason:  providers.StopReasonEndTurn,
		StopQual:    StopQualityFromReason(providers.StopReasonEndTurn),
		InputTokens: 1000,
		OutputTokens: 200,
	})

	result := f.Flush(0, 0)
	if !result.HasData {
		t.Fatal("expected HasData=true")
	}
	if result.Quality.CompositeQuality != 1.0 {
		t.Errorf("quality = %v, want 1.0 (end_turn, no tools)", result.Quality.CompositeQuality)
	}
	if result.Quality.StopReason != providers.StopReasonEndTurn {
		t.Errorf("StopReason = %v, want end_turn", result.Quality.StopReason)
	}
}

func TestSignalFuser_FlushAllThreeSignals(t *testing.T) {
	reg := NewBaselineRegistry(50)
	f := NewSignalFuser(BaselineKey{AgentType: "a", ModelID: "m"}, reg)

	f.SetProviderSignals(ProviderSignals{
		StopReason: providers.StopReasonEndTurn,
		StopQual:   1.0,
	})
	f.SetStreamSignals(StreamSignals{TokensPerSecond: 100})
	f.SetBehaviorSignals(BehaviorSignals{Rating: 0.4})

	// min(1.0, 1.0 (no tools), 0.4 (behavior)) = 0.4
	result := f.Flush(0, 0)
	if !result.HasData {
		t.Fatal("expected HasData=true")
	}
	if result.Quality.CompositeQuality != 0.4 {
		t.Errorf("quality = %v, want 0.4", result.Quality.CompositeQuality)
	}
}

func TestSignalFuser_FlushResetsState(t *testing.T) {
	reg := NewBaselineRegistry(50)
	f := NewSignalFuser(BaselineKey{AgentType: "a", ModelID: "m"}, reg)

	f.SetProviderSignals(ProviderSignals{StopQual: 1.0})
	_ = f.Flush(0, 0)

	// Second flush without new signals → no data.
	result := f.Flush(0, 0)
	if result.HasData {
		t.Error("expected HasData=false after reset")
	}
}

func TestSignalFuser_StressPopulated(t *testing.T) {
	reg := NewBaselineRegistry(50)
	key := BaselineKey{AgentType: "a", ModelID: "m"}
	f := NewSignalFuser(key, reg)

	// Warm baseline.
	reg.Update(key, StreamSignals{TokensPerSecond: 100})

	// Now send a slow stream.
	f.SetStreamSignals(StreamSignals{TokensPerSecond: 10})

	result := f.Flush(0, 0)
	if !result.HasData {
		t.Fatal("expected HasData=true")
	}
	if result.Stress.Severity <= 0 {
		t.Error("expected non-zero stress severity from throughput drop")
	}
}

func TestSignalFuser_MinAggregationWithTools(t *testing.T) {
	reg := NewBaselineRegistry(50)
	f := NewSignalFuser(BaselineKey{AgentType: "a", ModelID: "m"}, reg)

	f.SetProviderSignals(ProviderSignals{
		StopReason: providers.StopReasonEndTurn,
		StopQual:   1.0,
	})

	// 3 of 10 tools succeeded → ratio = 0.3
	result := f.Flush(10, 3)
	if result.Quality.CompositeQuality != 0.3 {
		t.Errorf("quality = %v, want 0.3 (tools tank)", result.Quality.CompositeQuality)
	}
}
