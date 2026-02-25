package architect

import (
	"math"
	"testing"
)

func TestDeriveMaxRounds(t *testing.T) {
	// 200K/16K: theoretical=12, prefillCapacity=(200000/2)/16384=6 → min(12,6)=6
	if got := deriveMaxRounds(200000, 16384); got != 6 {
		t.Fatalf("deriveMaxRounds(200K,16K) = %d, want 6", got)
	}
	// 200K/4K: theoretical=48, prefillCapacity=(100000/4096)=24 → 24
	if got := deriveMaxRounds(200000, 4096); got != 24 {
		t.Fatalf("deriveMaxRounds(200K,4K) = %d, want 24", got)
	}
	// 16K/4K: theoretical=4, prefillCapacity=2 → 2
	if got := deriveMaxRounds(16384, 4096); got != 2 {
		t.Fatalf("deriveMaxRounds(16K,4K) = %d, want 2", got)
	}
	// Edge: maxOutput > context → theoretical=0, prefillCapacity=0 → max(1,0)=1
	if got := deriveMaxRounds(4096, 8192); got != 1 {
		t.Fatalf("deriveMaxRounds(4K,8K) = %d, want 1", got)
	}
}

func TestDeriveContinuationConfig(t *testing.T) {
	cfg := deriveContinuationConfig(200000, 16384, 4)

	// deriveMaxRounds(200000,16384) = min(12, 6) = 6
	if cfg.maxRounds != 6 {
		t.Fatalf("maxRounds = %d, want 6", cfg.maxRounds)
	}
	if cfg.prefillCharLimit != 400000 { // (200000/2) * 4
		t.Fatalf("prefillCharLimit = %d, want 400000", cfg.prefillCharLimit)
	}
	if cfg.overlapSearchLimit != 65536 { // 16384 * 4
		t.Fatalf("overlapSearchLimit = %d, want 65536", cfg.overlapSearchLimit)
	}
	if cfg.maxOutputTokens != 16384 {
		t.Fatalf("maxOutputTokens = %d, want 16384", cfg.maxOutputTokens)
	}

	// windowSize = max(1, 6/3) = 2
	// alpha = 2.0 / (2+1) = 0.6666...
	// threshold = 1.0 / max(2, 2) = 0.5
	wantAlpha := 2.0 / 3.0
	if diff := cfg.progressAlpha - wantAlpha; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("progressAlpha = %f, want %f", cfg.progressAlpha, wantAlpha)
	}
	wantThreshold := 0.5
	if cfg.progressThreshold != wantThreshold {
		t.Fatalf("progressThreshold = %f, want %f", cfg.progressThreshold, wantThreshold)
	}
}

func TestDeriveContinuationConfig_SmallContext(t *testing.T) {
	cfg := deriveContinuationConfig(16384, 4096, 4)

	// deriveMaxRounds(16384,4096) = min(4, 2) = 2
	if cfg.maxRounds != 2 {
		t.Fatalf("maxRounds = %d, want 2", cfg.maxRounds)
	}
	// windowSize = max(1, 2/3) = 1
	// alpha = 2.0 / (1+1) = 1.0
	// threshold = 1.0 / max(2, 1) = 0.5
	wantThreshold := 0.5
	if cfg.progressThreshold != wantThreshold {
		t.Fatalf("progressThreshold = %f, want %f", cfg.progressThreshold, wantThreshold)
	}
}

func TestPrefillTail_Short(t *testing.T) {
	got := prefillTail("short text", 100)
	if got != "short text" {
		t.Fatalf("got %q, want full string", got)
	}
}

func TestPrefillTail_Long(t *testing.T) {
	text := "abcdefghij" // 10 chars
	got := prefillTail(text, 5)
	if got != "fghij" {
		t.Fatalf("got %q, want %q", got, "fghij")
	}
}

func TestPrefillTail_ExactLength(t *testing.T) {
	text := "exact"
	got := prefillTail(text, 5)
	if got != "exact" {
		t.Fatalf("got %q, want %q", got, "exact")
	}
}

func TestContinuationModeFromOnChunk(t *testing.T) {
	if mode := continuationModeFromOnChunk(nil); mode != continuationModeJSON {
		t.Fatalf("nil onChunk = %d, want JSON (%d)", mode, continuationModeJSON)
	}
	if mode := continuationModeFromOnChunk(func(string) {}); mode != continuationModeText {
		t.Fatalf("non-nil onChunk = %d, want Text (%d)", mode, continuationModeText)
	}
}

func TestProgressTracker_Healthy(t *testing.T) {
	p := newProgressTracker(0.4)
	// First sample: 100% new content.
	p.update(100, 100)
	if !p.isHealthy(0.25) {
		t.Fatal("100% new content should be healthy")
	}
}

func TestProgressTracker_Decay(t *testing.T) {
	p := newProgressTracker(0.4)

	// Round 1: 90% new. EMA = 0.9 (first sample).
	p.update(100, 90)
	if !p.isHealthy(0.25) {
		t.Fatal("round 1 should be healthy")
	}

	// Round 2: 20% new. EMA = 0.4*0.2 + 0.6*0.9 = 0.08 + 0.54 = 0.62
	p.update(100, 20)
	if !p.isHealthy(0.25) {
		t.Fatal("round 2 EMA should still be above 0.25")
	}

	// Round 3: 5% new. EMA = 0.4*0.05 + 0.6*0.62 = 0.02 + 0.372 = 0.392
	p.update(100, 5)
	if !p.isHealthy(0.25) {
		t.Fatal("round 3 EMA should still be above 0.25")
	}

	// Round 4: 1% new. EMA = 0.4*0.01 + 0.6*0.392 = 0.004 + 0.2352 = 0.2392
	p.update(100, 1)
	if p.isHealthy(0.25) {
		t.Fatalf("round 4 should be unhealthy (EMA=%.4f < 0.25)", p.ema)
	}
}

func TestProgressTracker_ZeroSamples(t *testing.T) {
	p := newProgressTracker(0.4)
	// No samples yet — should be healthy (benefit of the doubt).
	if !p.isHealthy(0.25) {
		t.Fatal("zero samples should be healthy")
	}
}

func TestProgressEMA_FirstSample(t *testing.T) {
	got := progressEMA(1.0, 0.5, 0.4, 0)
	if got != 0.5 {
		t.Fatalf("first sample EMA = %f, want 0.5 (direct assignment)", got)
	}
}

func TestProgressEMA_Subsequent(t *testing.T) {
	// EMA = alpha*value + (1-alpha)*current = 0.4*0.8 + 0.6*0.5 = 0.32 + 0.30 = 0.62
	got := progressEMA(0.5, 0.8, 0.4, 1)
	if math.Abs(got-0.62) > 1e-9 {
		t.Fatalf("subsequent EMA = %f, want 0.62", got)
	}
}
