package handoff

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/providers"
)

func TestStopQualityFromReason_AllVariants(t *testing.T) {
	tests := []struct {
		reason providers.StopReason
		want   StopQuality
	}{
		{providers.StopReasonEndTurn, 1.0},
		{providers.StopReasonToolUse, 1.0},
		{providers.StopReasonStopSequence, 0.9},
		{providers.StopReasonMaxTokens, 0.3},
		{providers.StopReasonError, 0.0},
		{"", 0.5},
		{"unknown_value", 0.5},
	}

	for _, tt := range tests {
		got := StopQualityFromReason(tt.reason)
		if got != tt.want {
			t.Errorf("StopQualityFromReason(%q) = %v, want %v", tt.reason, got, tt.want)
		}
	}
}

func TestExtractProviderSignals_NilResponse(t *testing.T) {
	p := ExtractProviderSignals(nil)
	if p.StopQual != StopQualityFromReason("") {
		t.Errorf("nil response: StopQual = %v, want neutral", p.StopQual)
	}
}

func TestExtractProviderSignals_Normal(t *testing.T) {
	resp := &providers.Response{
		StopReason: providers.StopReasonEndTurn,
		Usage: providers.Usage{
			InputTokens:     1000,
			OutputTokens:    200,
			CacheReadTokens: 500,
		},
	}
	p := ExtractProviderSignals(resp)
	if p.StopQual != 1.0 {
		t.Errorf("StopQual = %v, want 1.0", p.StopQual)
	}
	if p.OutputTokens != 200 {
		t.Errorf("OutputTokens = %d, want 200", p.OutputTokens)
	}
}

func TestProviderSignals_CacheEfficiency(t *testing.T) {
	tests := []struct {
		name   string
		input  int
		cached int
		want   float64
	}{
		{"zero_input", 0, 0, 0},
		{"no_cache", 1000, 0, 0},
		{"half_cached", 1000, 500, 0.5},
		{"fully_cached", 1000, 1000, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &ProviderSignals{InputTokens: tt.input, CacheReadTokens: tt.cached}
			got := p.CacheEfficiency()
			if got != tt.want {
				t.Errorf("CacheEfficiency() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompositeQuality_MinAggregation(t *testing.T) {
	tests := []struct {
		name          string
		stopQ         StopQuality
		toolCalls     int
		toolSuccesses int
		behaviorRating float64
		hasBehavior   bool
		want          float64
	}{
		{"perfect", 1.0, 5, 5, 0, false, 1.0},
		{"stop_tanks", 0.3, 5, 5, 0, false, 0.3},
		{"tools_tank", 1.0, 10, 2, 0, false, 0.2},
		{"behavior_tanks", 1.0, 5, 5, 0.1, true, 0.1},
		{"no_tools", 1.0, 0, 0, 0, false, 1.0},
		{"all_bad", 0.3, 10, 2, 0.1, true, 0.1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompositeQuality(tt.stopQ, tt.toolCalls, tt.toolSuccesses, tt.behaviorRating, tt.hasBehavior)
			if got != tt.want {
				t.Errorf("CompositeQuality() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComputeStressSignal_TTFTDeviation(t *testing.T) {
	baseline := &SignalBaseline{
		ttft: ewmaStat{mean: float64(100 * time.Millisecond), initialized: true},
	}

	// 2x baseline TTFT → severity = (200-100)/100 = 1.0
	stream := StreamSignals{TimeToFirstToken: 200 * time.Millisecond}
	stress := ComputeStressSignal(stream, baseline)
	if stress.Source != "ttft" {
		t.Errorf("source = %q, want ttft", stress.Source)
	}
	if stress.Severity != 1.0 {
		t.Errorf("severity = %v, want 1.0", stress.Severity)
	}
}

func TestComputeStressSignal_Backpressure(t *testing.T) {
	stream := StreamSignals{BackpressureEvents: 3}
	stress := ComputeStressSignal(stream, nil)
	if stress.Source != "backpressure" {
		t.Errorf("source = %q, want backpressure", stress.Source)
	}
	// Severity derived from 1 - StopQualityFromReason(stop_sequence) = 1 - 0.9 = 0.1
	expected := 1.0 - float64(StopQualityFromReason(providers.StopReasonStopSequence))
	if stress.Severity != expected {
		t.Errorf("severity = %v, want %v", stress.Severity, expected)
	}
}

func TestComputeStressSignal_EarlyAbort(t *testing.T) {
	stream := StreamSignals{EarlyAbort: true}
	stress := ComputeStressSignal(stream, nil)
	if stress.Source != "early_abort" {
		t.Errorf("source = %q, want early_abort", stress.Source)
	}
	expected := 1.0 - float64(StopQualityFromReason(providers.StopReasonMaxTokens))
	if stress.Severity != expected {
		t.Errorf("severity = %v, want %v", stress.Severity, expected)
	}
}

func TestComputeStressSignal_ThroughputDrop(t *testing.T) {
	baseline := &SignalBaseline{
		tokensPerSecond: ewmaStat{mean: 100, initialized: true},
	}
	// 50 tps vs 100 baseline → severity = (100-50)/100 = 0.5
	stream := StreamSignals{TokensPerSecond: 50}
	stress := ComputeStressSignal(stream, baseline)
	if stress.Source != "throughput" {
		t.Errorf("source = %q, want throughput", stress.Source)
	}
	if stress.Severity != 0.5 {
		t.Errorf("severity = %v, want 0.5", stress.Severity)
	}
}

func TestComputeStressSignal_MaxSelection(t *testing.T) {
	// Early abort (0.7) beats backpressure (0.1) and throughput
	baseline := &SignalBaseline{
		tokensPerSecond: ewmaStat{mean: 100, initialized: true},
	}
	stream := StreamSignals{
		EarlyAbort:         true,
		BackpressureEvents: 1,
		TokensPerSecond:    90, // 10% drop
	}
	stress := ComputeStressSignal(stream, baseline)
	if stress.Source != "early_abort" {
		t.Errorf("max source = %q, want early_abort", stress.Source)
	}
}

func TestComputeStressSignal_NoData(t *testing.T) {
	stress := ComputeStressSignal(StreamSignals{}, nil)
	if stress.Severity != 0 {
		t.Errorf("empty stream → severity = %v, want 0", stress.Severity)
	}
}

func TestExtractBehaviorSignals(t *testing.T) {
	sig := QualitySignal{
		FeedbackType: FeedbackRetry,
		Score:        0.2,
	}
	b := ExtractBehaviorSignals(sig)
	if !b.IsRetry {
		t.Error("expected IsRetry = true")
	}
	if b.IsCorrection {
		t.Error("expected IsCorrection = false")
	}
	if b.Rating != 0.2 {
		t.Errorf("Rating = %v, want 0.2", b.Rating)
	}
}
