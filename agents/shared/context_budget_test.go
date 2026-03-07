package shared

import (
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

func TestNewContextBudget(t *testing.T) {
	b := NewContextBudget("claude-opus-4-6", 4096, 8192)
	if b.ModelLimit() != 200000 {
		t.Fatalf("expected 200000, got %d", b.ModelLimit())
	}
	// Capacity = 200000 - 4096 - 8192
	wantCap := 200000 - 4096 - 8192
	if b.Capacity() != wantCap {
		t.Fatalf("expected capacity %d, got %d", wantCap, b.Capacity())
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
	if b.FixedOverhead() != b.fixedOverhead {
		t.Fatal("FixedOverhead() should match fixedOverhead field")
	}
}

func TestZoneThresholds(t *testing.T) {
	// Use a small model limit for predictable math.
	b := &ContextBudget{
		modelLimit:    1000,
		reserveTokens: 0,
		fixedOverhead: 0,
		counter:       providers.NewProviderTokenCounter(providers.DefaultTokenCounterConfig()),
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

func TestAnchorAndProjectedInput(t *testing.T) {
	b := NewContextBudget("claude-opus-4-6", 4096, 0)
	b.ComputeFixedOverhead("system prompt", nil)

	msgs := []providers.Message{
		{Role: providers.RoleUser, Content: "hello"},
		{Role: providers.RoleAssistant, Content: "hi there"},
	}

	// Before anchor: uses fixedOverhead + estimateRaw.
	preAnchor := b.ProjectedInput(msgs)
	if preAnchor <= 0 {
		t.Fatal("expected positive projected input before anchor")
	}
	if b.Anchored() {
		t.Fatal("should not be anchored before Anchor()")
	}

	// Anchor with provider ground truth.
	b.Anchor(500, len(msgs))
	if !b.Anchored() {
		t.Fatal("should be anchored after Anchor()")
	}
	if b.AnchorTokens() != 500 {
		t.Fatalf("expected anchor 500, got %d", b.AnchorTokens())
	}

	// No new messages → projected = anchor exactly.
	projected := b.ProjectedInput(msgs)
	if projected != 500 {
		t.Fatalf("expected projected=500 (no delta), got %d", projected)
	}

	// Append a message → projected = anchor + delta estimate.
	msgs = append(msgs, providers.Message{Role: providers.RoleUser, Content: makeString(400)})
	projectedWithDelta := b.ProjectedInput(msgs)
	if projectedWithDelta <= 500 {
		t.Fatalf("expected projected > 500 with delta, got %d", projectedWithDelta)
	}
}

func TestAnchorInvalidation(t *testing.T) {
	b := NewContextBudget("claude-opus-4-6", 4096, 0)
	b.ComputeFixedOverhead("system", nil)

	msgs := []providers.Message{{Role: providers.RoleUser, Content: "test"}}
	b.Anchor(300, len(msgs))
	if !b.Anchored() {
		t.Fatal("should be anchored")
	}

	b.InvalidateAnchor()
	if b.Anchored() {
		t.Fatal("should not be anchored after invalidation")
	}

	// After invalidation, falls back to fixedOverhead + estimateRaw.
	projected := b.ProjectedInput(msgs)
	if projected <= 0 {
		t.Fatal("expected positive projected input after invalidation")
	}
}

func TestPerResultBudget(t *testing.T) {
	b := &ContextBudget{
		modelLimit:    10000,
		reserveTokens: 0,
		fixedOverhead: 0,
		counter:       providers.NewProviderTokenCounter(providers.DefaultTokenCounterConfig()),
		compactAt:     0.80,
		evictAt:       0.90,
		criticalAt:    0.95,
	}

	// 10000 capacity, 5000 projected, 5 remaining turns → 1000 per result
	budget := b.PerResultBudget(5000, 5)
	if budget != 1000 {
		t.Fatalf("expected 1000, got %d", budget)
	}

	// 0 remaining turns → divisor clamped to 1
	budget = b.PerResultBudget(5000, 0)
	if budget != 5000 {
		t.Fatalf("expected 5000, got %d", budget)
	}

	// Used exceeds capacity → 0
	budget = b.PerResultBudget(15000, 5)
	if budget != 0 {
		t.Fatalf("expected 0, got %d", budget)
	}
}

func TestUtilizationZeroCapacity(t *testing.T) {
	b := &ContextBudget{
		modelLimit:    1000,
		reserveTokens: 1000,
		fixedOverhead: 0,
		counter:       providers.NewProviderTokenCounter(providers.DefaultTokenCounterConfig()),
		compactAt:     0.80,
		evictAt:       0.90,
		criticalAt:    0.95,
	}
	u := b.Utilization([]providers.Message{{Role: providers.RoleUser, Content: "hello"}})
	if u != 1.0 {
		t.Fatalf("expected 1.0 when capacity=0, got %.2f", u)
	}
}

func TestAvailableWithMessages(t *testing.T) {
	b := &ContextBudget{
		modelLimit:    1000,
		reserveTokens: 0,
		fixedOverhead: 0,
		counter:       providers.NewProviderTokenCounter(providers.DefaultTokenCounterConfig()),
		compactAt:     0.80,
		evictAt:       0.90,
		criticalAt:    0.95,
	}

	msgs := []providers.Message{{Role: providers.RoleUser, Content: makeString(2000)}}
	avail := b.Available(msgs)
	if avail >= 1000 {
		t.Fatalf("expected available < capacity with messages, got %d", avail)
	}

	// With no messages, available should equal capacity.
	empty := b.Available(nil)
	if empty != 1000 {
		t.Fatalf("expected 1000 with no messages, got %d", empty)
	}
}

func TestCapacityNonNegative(t *testing.T) {
	b := &ContextBudget{
		modelLimit:    100,
		reserveTokens: 500,
		counter:       providers.NewProviderTokenCounter(providers.DefaultTokenCounterConfig()),
	}
	if b.Capacity() != 0 {
		t.Fatalf("expected 0 when reserve > limit, got %d", b.Capacity())
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
