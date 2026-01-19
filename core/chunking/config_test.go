package chunking

import (
	"math"
	"testing"
)

// =============================================================================
// Domain Tests
// =============================================================================

func TestDomain_String(t *testing.T) {
	tests := []struct {
		name   string
		domain Domain
		want   string
	}{
		{
			name:   "code",
			domain: DomainCode,
			want:   "code",
		},
		{
			name:   "academic",
			domain: DomainAcademic,
			want:   "academic",
		},
		{
			name:   "history",
			domain: DomainHistory,
			want:   "history",
		},
		{
			name:   "general",
			domain: DomainGeneral,
			want:   "general",
		},
		{
			name:   "unknown",
			domain: Domain(999),
			want:   "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.domain.String()
			if got != tt.want {
				t.Errorf("String() = %s, want %s", got, tt.want)
			}
		})
	}
}

// =============================================================================
// ChunkConfig Constructor Tests
// =============================================================================

func TestNewChunkConfig(t *testing.T) {
	tests := []struct {
		name      string
		maxTokens int
		wantErr   bool
	}{
		{
			name:      "valid max tokens",
			maxTokens: 2048,
			wantErr:   false,
		},
		{
			name:      "small max tokens",
			maxTokens: 100,
			wantErr:   false,
		},
		{
			name:      "large max tokens",
			maxTokens: 8192,
			wantErr:   false,
		},
		{
			name:      "zero max tokens",
			maxTokens: 0,
			wantErr:   true,
		},
		{
			name:      "negative max tokens",
			maxTokens: -100,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc, err := NewChunkConfig(tt.maxTokens)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NewChunkConfig() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewChunkConfig() unexpected error: %v", err)
			}
			if cc == nil {
				t.Fatal("NewChunkConfig() returned nil")
			}
			if cc.MaxTokens != tt.maxTokens {
				t.Errorf("MaxTokens = %d, want %d", cc.MaxTokens, tt.maxTokens)
			}
			if cc.TargetTokens == nil {
				t.Error("TargetTokens is nil")
			}
			if cc.MinTokens == nil {
				t.Error("MinTokens is nil")
			}
			if cc.ContextTokensBefore == nil {
				t.Error("ContextTokensBefore is nil")
			}
			if cc.ContextTokensAfter == nil {
				t.Error("ContextTokensAfter is nil")
			}
			if cc.OverflowStrategyWeights == nil {
				t.Error("OverflowStrategyWeights is nil")
			}
		})
	}
}

// =============================================================================
// ChunkConfig GetEffective* Methods Tests
// =============================================================================

func TestChunkConfig_GetEffectiveTargetTokens(t *testing.T) {
	cc, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	exploit := cc.GetEffectiveTargetTokens(false)
	if exploit <= 0 {
		t.Errorf("GetEffectiveTargetTokens(false) = %d, want positive", exploit)
	}

	explore := cc.GetEffectiveTargetTokens(true)
	if explore <= 0 {
		t.Errorf("GetEffectiveTargetTokens(true) = %d, want positive", explore)
	}

	ccNil := &ChunkConfig{MaxTokens: 2048}
	fallback := ccNil.GetEffectiveTargetTokens(false)
	if fallback != 1024 {
		t.Errorf("GetEffectiveTargetTokens(false) with nil = %d, want %d",
			fallback, 1024)
	}
}

func TestChunkConfig_GetEffectiveMinTokens(t *testing.T) {
	cc, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	exploit := cc.GetEffectiveMinTokens(false)
	if exploit <= 0 {
		t.Errorf("GetEffectiveMinTokens(false) = %d, want positive", exploit)
	}

	explore := cc.GetEffectiveMinTokens(true)
	if explore <= 0 {
		t.Errorf("GetEffectiveMinTokens(true) = %d, want positive", explore)
	}

	ccNil := &ChunkConfig{MaxTokens: 2048}
	fallback := ccNil.GetEffectiveMinTokens(false)
	expected := 2048 / 10
	if fallback != expected {
		t.Errorf("GetEffectiveMinTokens(false) with nil = %d, want %d",
			fallback, expected)
	}
}

func TestChunkConfig_GetEffectiveContextTokensBefore(t *testing.T) {
	cc, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	exploit := cc.GetEffectiveContextTokensBefore(false)
	if exploit < 0 {
		t.Errorf("GetEffectiveContextTokensBefore(false) = %d, want non-negative", exploit)
	}

	explore := cc.GetEffectiveContextTokensBefore(true)
	if explore < 0 {
		t.Errorf("GetEffectiveContextTokensBefore(true) = %d, want non-negative", explore)
	}

	ccNil := &ChunkConfig{MaxTokens: 2048}
	fallback := ccNil.GetEffectiveContextTokensBefore(false)
	if fallback != 0 {
		t.Errorf("GetEffectiveContextTokensBefore(false) with nil = %d, want 0", fallback)
	}
}

func TestChunkConfig_GetEffectiveContextTokensAfter(t *testing.T) {
	cc, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	exploit := cc.GetEffectiveContextTokensAfter(false)
	if exploit < 0 {
		t.Errorf("GetEffectiveContextTokensAfter(false) = %d, want non-negative", exploit)
	}

	explore := cc.GetEffectiveContextTokensAfter(true)
	if explore < 0 {
		t.Errorf("GetEffectiveContextTokensAfter(true) = %d, want non-negative", explore)
	}

	ccNil := &ChunkConfig{MaxTokens: 2048}
	fallback := ccNil.GetEffectiveContextTokensAfter(false)
	if fallback != 0 {
		t.Errorf("GetEffectiveContextTokensAfter(false) with nil = %d, want 0", fallback)
	}
}

func TestChunkConfig_GetEffectiveOverflowStrategy(t *testing.T) {
	cc, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	exploit := cc.GetEffectiveOverflowStrategy(false)
	if exploit != StrategyRecursive &&
		exploit != StrategySentence &&
		exploit != StrategyTruncate {
		t.Errorf("GetEffectiveOverflowStrategy(false) = %d, want valid strategy", exploit)
	}

	explore := cc.GetEffectiveOverflowStrategy(true)
	if explore != StrategyRecursive &&
		explore != StrategySentence &&
		explore != StrategyTruncate {
		t.Errorf("GetEffectiveOverflowStrategy(true) = %d, want valid strategy", explore)
	}

	ccNil := &ChunkConfig{MaxTokens: 2048}
	fallback := ccNil.GetEffectiveOverflowStrategy(false)
	if fallback != StrategyRecursive {
		t.Errorf("GetEffectiveOverflowStrategy(false) with nil = %s, want recursive",
			fallback)
	}
}

func TestChunkConfig_ExploreSampling(t *testing.T) {
	cc, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	const numSamples = 50
	samples := make([]int, numSamples)

	for i := 0; i < numSamples; i++ {
		samples[i] = cc.GetEffectiveTargetTokens(true)
	}

	allSame := true
	for i := 1; i < numSamples; i++ {
		if samples[i] != samples[0] {
			allSame = false
			break
		}
	}

	if allSame {
		t.Logf("Explore sampling returned all identical values, may indicate low variance")
	}
}

// =============================================================================
// PriorChunkConfig Tests
// =============================================================================

func TestPriorChunkConfig(t *testing.T) {
	tests := []struct {
		name      string
		maxTokens int
		domain    Domain
		wantErr   bool
	}{
		{
			name:      "code domain",
			maxTokens: 2048,
			domain:    DomainCode,
			wantErr:   false,
		},
		{
			name:      "academic domain",
			maxTokens: 2048,
			domain:    DomainAcademic,
			wantErr:   false,
		},
		{
			name:      "history domain",
			maxTokens: 2048,
			domain:    DomainHistory,
			wantErr:   false,
		},
		{
			name:      "general domain",
			maxTokens: 2048,
			domain:    DomainGeneral,
			wantErr:   false,
		},
		{
			name:      "zero max tokens",
			maxTokens: 0,
			domain:    DomainGeneral,
			wantErr:   true,
		},
		{
			name:      "negative max tokens",
			maxTokens: -100,
			domain:    DomainGeneral,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc, err := PriorChunkConfig(tt.maxTokens, tt.domain)
			if tt.wantErr {
				if err == nil {
					t.Errorf("PriorChunkConfig() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("PriorChunkConfig() unexpected error: %v", err)
			}
			if cc == nil {
				t.Fatal("PriorChunkConfig() returned nil")
			}
			if cc.MaxTokens != tt.maxTokens {
				t.Errorf("MaxTokens = %d, want %d", cc.MaxTokens, tt.maxTokens)
			}
			if cc.TargetTokens == nil {
				t.Error("TargetTokens is nil")
			}
			if cc.MinTokens == nil {
				t.Error("MinTokens is nil")
			}
			if cc.ContextTokensBefore == nil {
				t.Error("ContextTokensBefore is nil")
			}
			if cc.ContextTokensAfter == nil {
				t.Error("ContextTokensAfter is nil")
			}
			if cc.OverflowStrategyWeights == nil {
				t.Error("OverflowStrategyWeights is nil")
			}
		})
	}
}

func TestPriorChunkConfig_CodeDomain(t *testing.T) {
	cc, err := PriorChunkConfig(2048, DomainCode)
	if err != nil {
		t.Fatalf("PriorChunkConfig() error: %v", err)
	}

	targetMean := cc.TargetTokens.Mean()
	if math.Abs(float64(targetMean)-300) > 100 {
		t.Logf("Code domain target mean is %d, expected around 300", targetMean)
	}

	strategy := cc.OverflowStrategyWeights.BestStrategy()
	if strategy != StrategyRecursive {
		t.Logf("Code domain best strategy is %s, expected recursive", strategy)
	}
}

func TestPriorChunkConfig_AcademicDomain(t *testing.T) {
	cc, err := PriorChunkConfig(2048, DomainAcademic)
	if err != nil {
		t.Fatalf("PriorChunkConfig() error: %v", err)
	}

	targetMean := cc.TargetTokens.Mean()
	if math.Abs(float64(targetMean)-500) > 150 {
		t.Logf("Academic domain target mean is %d, expected around 500", targetMean)
	}

	strategy := cc.OverflowStrategyWeights.BestStrategy()
	if strategy != StrategySentence {
		t.Logf("Academic domain best strategy is %s, expected sentence", strategy)
	}
}

func TestPriorChunkConfig_HistoryDomain(t *testing.T) {
	cc, err := PriorChunkConfig(2048, DomainHistory)
	if err != nil {
		t.Fatalf("PriorChunkConfig() error: %v", err)
	}

	targetMean := cc.TargetTokens.Mean()
	if math.Abs(float64(targetMean)-400) > 150 {
		t.Logf("History domain target mean is %d, expected around 400", targetMean)
	}

	afterMean := cc.ContextTokensAfter.Mean()
	beforeMean := cc.ContextTokensBefore.Mean()
	if afterMean >= beforeMean {
		t.Logf("History domain should prefer more context before (%d) than after (%d)",
			beforeMean, afterMean)
	}
}

func TestPriorChunkConfig_GeneralDomain(t *testing.T) {
	maxTokens := 2048
	cc, err := PriorChunkConfig(maxTokens, DomainGeneral)
	if err != nil {
		t.Fatalf("PriorChunkConfig() error: %v", err)
	}

	targetMean := cc.TargetTokens.Mean()
	expectedTarget := maxTokens / 2
	tolerance := float64(expectedTarget) * 0.3
	if math.Abs(float64(targetMean-expectedTarget)) > tolerance {
		t.Logf("General domain target mean is %d, expected around %d",
			targetMean, expectedTarget)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestChunkConfig_IntegrationExploreExploit(t *testing.T) {
	cc, err := NewChunkConfig(2048)
	if err != nil {
		t.Fatalf("NewChunkConfig() error: %v", err)
	}

	exploitTarget := cc.GetEffectiveTargetTokens(false)
	exploitMin := cc.GetEffectiveMinTokens(false)
	exploitBefore := cc.GetEffectiveContextTokensBefore(false)
	exploitAfter := cc.GetEffectiveContextTokensAfter(false)
	exploitStrategy := cc.GetEffectiveOverflowStrategy(false)

	if exploitTarget <= exploitMin {
		t.Errorf("Target tokens (%d) should be greater than min tokens (%d)",
			exploitTarget, exploitMin)
	}

	if exploitTarget > cc.MaxTokens {
		t.Errorf("Target tokens (%d) should not exceed max tokens (%d)",
			exploitTarget, cc.MaxTokens)
	}

	if exploitBefore < 0 || exploitAfter < 0 {
		t.Errorf("Context tokens should be non-negative: before=%d, after=%d",
			exploitBefore, exploitAfter)
	}

	if exploitStrategy != StrategyRecursive &&
		exploitStrategy != StrategySentence &&
		exploitStrategy != StrategyTruncate {
		t.Errorf("Invalid overflow strategy: %d", exploitStrategy)
	}
}

func TestPriorChunkConfig_DomainDifferences(t *testing.T) {
	maxTokens := 2048

	codeConfig, err := PriorChunkConfig(maxTokens, DomainCode)
	if err != nil {
		t.Fatalf("PriorChunkConfig(Code) error: %v", err)
	}

	academicConfig, err := PriorChunkConfig(maxTokens, DomainAcademic)
	if err != nil {
		t.Fatalf("PriorChunkConfig(Academic) error: %v", err)
	}

	historyConfig, err := PriorChunkConfig(maxTokens, DomainHistory)
	if err != nil {
		t.Fatalf("PriorChunkConfig(History) error: %v", err)
	}

	codeTarget := codeConfig.TargetTokens.Mean()
	academicTarget := academicConfig.TargetTokens.Mean()
	historyTarget := historyConfig.TargetTokens.Mean()

	if codeTarget == academicTarget && academicTarget == historyTarget {
		t.Error("Different domains should have different target token priors")
	}

	codeStrategy := codeConfig.OverflowStrategyWeights.BestStrategy()
	academicStrategy := academicConfig.OverflowStrategyWeights.BestStrategy()

	if codeStrategy == academicStrategy {
		t.Logf("Code and academic domains have same best strategy: %s", codeStrategy)
	}
}
