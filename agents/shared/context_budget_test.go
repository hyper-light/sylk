package shared

import (
	"math"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

func TestNewContextBudget(t *testing.T) {
	b := NewContextBudget("claude-opus-4-6", 4096, 8192)
	if b.ModelLimit() != 200000 {
		t.Fatalf("expected 200000, got %d", b.ModelLimit())
	}
	// Available = 200000 - 4096 - 8192 - 0 (no fixed overhead yet)
	if b.Available() != 200000-4096-8192 {
		t.Fatalf("expected %d, got %d", 200000-4096-8192, b.Available())
	}
}

func TestNewContextBudgetUnknownModel(t *testing.T) {
	b := NewContextBudget("unknown-model", 1000, 0)
	if b.ModelLimit() != 128000 {
		t.Fatalf("expected fallback 128000, got %d", b.ModelLimit())
	}
}

func TestComputeFixedOverhead(t *testing.T) {
	b := NewContextBudget("claude-opus-4-6", 4096, 0)
	tools := []providers.Tool{
		{Name: "read_file", Description: "Reads a file from disk", Parameters: map[string]any{"type": "object"}},
	}
	b.ComputeFixedOverhead("You are a helpful assistant.", tools)
	if b.fixedOverhead <= 0 {
		t.Fatal("expected positive fixed overhead")
	}
	if b.Available() >= 200000-4096 {
		t.Fatal("available should decrease after computing overhead")
	}
}

func TestZoneThresholds(t *testing.T) {
	// Use a small model limit for predictable math.
	b := &ContextBudget{
		modelLimit:    1000,
		reserveTokens: 0,
		fixedOverhead: 0,
		counter:       providers.NewProviderTokenCounter(providers.DefaultTokenCounterConfig()),
		calibRatio:    1.0,
		compactAt:     0.80,
		evictAt:       0.90,
		criticalAt:    0.95,
	}

	// Each char ≈ 1/4 token with FallbackCharsPerToken=4.
	// 3000 chars → 750 tokens → 75% of 1000 → Green
	greenMsg := []providers.Message{{Role: providers.RoleUser, Content: makeString(3000)}}
	if z := b.Zone(greenMsg); z != ZoneGreen {
		t.Fatalf("expected Green, got %s (util=%.2f)", z, b.Utilization(greenMsg))
	}

	// 3400 chars → 850 tokens → 85% → Yellow
	yellowMsg := []providers.Message{{Role: providers.RoleUser, Content: makeString(3400)}}
	if z := b.Zone(yellowMsg); z != ZoneYellow {
		t.Fatalf("expected Yellow, got %s (util=%.2f)", z, b.Utilization(yellowMsg))
	}

	// 3700 chars → 925 tokens → 92.5% → Red
	redMsg := []providers.Message{{Role: providers.RoleUser, Content: makeString(3700)}}
	if z := b.Zone(redMsg); z != ZoneRed {
		t.Fatalf("expected Red, got %s (util=%.2f)", z, b.Utilization(redMsg))
	}

	// 3900 chars → 975 tokens → 97.5% → Critical
	critMsg := []providers.Message{{Role: providers.RoleUser, Content: makeString(3900)}}
	if z := b.Zone(critMsg); z != ZoneCritical {
		t.Fatalf("expected Critical, got %s (util=%.2f)", z, b.Utilization(critMsg))
	}
}

func TestCalibrationEMA(t *testing.T) {
	b := NewContextBudget("claude-opus-4-6", 4096, 0)

	// First sample: estimate=100, actual=150 → ratio = 1.5
	b.Calibrate(100, 150)
	if math.Abs(b.CalibRatio()-1.5) > 0.001 {
		t.Fatalf("expected 1.5 after first sample, got %.3f", b.CalibRatio())
	}

	// Second sample: estimate=100, actual=100 → observed=1.0
	// EMA: 0.3*1.0 + 0.7*1.5 = 1.35
	b.Calibrate(100, 100)
	expected := 0.3*1.0 + 0.7*1.5
	if math.Abs(b.CalibRatio()-expected) > 0.001 {
		t.Fatalf("expected %.3f after second sample, got %.3f", expected, b.CalibRatio())
	}

	// After many 1.0 samples, should converge near 1.0.
	for range 20 {
		b.Calibrate(100, 100)
	}
	if math.Abs(b.CalibRatio()-1.0) > 0.05 {
		t.Fatalf("expected convergence near 1.0, got %.3f", b.CalibRatio())
	}
}

func TestCalibrationIgnoresZero(t *testing.T) {
	b := NewContextBudget("claude-opus-4-6", 4096, 0)
	b.Calibrate(0, 100)
	if b.CalibSamples() != 0 {
		t.Fatal("should not count zero-estimated sample")
	}
	b.Calibrate(100, 0)
	if b.CalibSamples() != 0 {
		t.Fatal("should not count zero-actual sample")
	}
}

func TestPerResultBudget(t *testing.T) {
	b := &ContextBudget{
		modelLimit:    10000,
		reserveTokens: 0,
		fixedOverhead: 0,
		counter:       providers.NewProviderTokenCounter(providers.DefaultTokenCounterConfig()),
		calibRatio:    1.0,
		compactAt:     0.80,
		evictAt:       0.90,
		criticalAt:    0.95,
	}

	// 10000 available, 5000 used, 5 remaining turns → 1000 per result
	budget := b.PerResultBudget(5000, 5)
	if budget != 1000 {
		t.Fatalf("expected 1000, got %d", budget)
	}

	// 0 remaining turns → divisor clamped to 1
	budget = b.PerResultBudget(5000, 0)
	if budget != 5000 {
		t.Fatalf("expected 5000, got %d", budget)
	}

	// Used exceeds available → 0
	budget = b.PerResultBudget(15000, 5)
	if budget != 0 {
		t.Fatalf("expected 0, got %d", budget)
	}
}

func TestUtilizationZeroAvailable(t *testing.T) {
	b := &ContextBudget{
		modelLimit:    1000,
		reserveTokens: 1000,
		fixedOverhead: 0,
		counter:       providers.NewProviderTokenCounter(providers.DefaultTokenCounterConfig()),
		calibRatio:    1.0,
		compactAt:     0.80,
		evictAt:       0.90,
		criticalAt:    0.95,
	}
	u := b.Utilization([]providers.Message{{Role: providers.RoleUser, Content: "hello"}})
	if u != 1.0 {
		t.Fatalf("expected 1.0 when available=0, got %.2f", u)
	}
}

func TestBudgetZoneString(t *testing.T) {
	cases := []struct {
		zone BudgetZone
		want string
	}{
		{ZoneGreen, "green"},
		{ZoneYellow, "yellow"},
		{ZoneRed, "red"},
		{ZoneCritical, "critical"},
		{BudgetZone(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.zone.String(); got != tc.want {
			t.Errorf("zone %d: got %q, want %q", tc.zone, got, tc.want)
		}
	}
}

func makeString(n int) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = 'a'
	}
	return string(buf)
}
