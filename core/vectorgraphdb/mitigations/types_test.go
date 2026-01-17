package mitigations

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSourceTypeBaseTrust(t *testing.T) {
	tests := []struct {
		source   SourceType
		expected float64
	}{
		{SourceTypeCode, 1.0},
		{SourceTypeUser, 0.95},
		{SourceTypeAcademic, 0.85},
		{SourceTypeCrossRef, 0.75},
		{SourceTypeLLM, 0.5},
		{SourceType("unknown"), 0.1},
	}

	for _, tt := range tests {
		t.Run(string(tt.source), func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.source.BaseTrust())
		})
	}
}

func TestTrustLevelScore(t *testing.T) {
	tests := []struct {
		level    TrustLevel
		expected float64
	}{
		{TrustVerifiedCode, 1.0},
		{TrustRecentHistory, 0.9},
		{TrustOfficialDocs, 0.8},
		{TrustOldHistory, 0.7},
		{TrustExternalArticle, 0.5},
		{TrustLLMInference, 0.3},
		{TrustUnknown, 0.1},
		{TrustLevel(99), 0.1},
	}

	for _, tt := range tests {
		t.Run(tt.level.String(), func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.level.Score())
		})
	}
}

func TestTrustLevelString(t *testing.T) {
	tests := []struct {
		level    TrustLevel
		expected string
	}{
		{TrustVerifiedCode, "verified_code"},
		{TrustRecentHistory, "recent_history"},
		{TrustOfficialDocs, "official_docs"},
		{TrustOldHistory, "old_history"},
		{TrustExternalArticle, "external_article"},
		{TrustLLMInference, "llm_inference"},
		{TrustUnknown, "unknown"},
		{TrustLevel(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.level.String())
		})
	}
}

func TestDefaultQualityWeights(t *testing.T) {
	weights := DefaultQualityWeights()

	assert.Equal(t, 0.35, weights.Relevance)
	assert.Equal(t, 0.20, weights.Freshness)
	assert.Equal(t, 0.25, weights.Trust)
	assert.Equal(t, 0.15, weights.Density)
	assert.Equal(t, 0.05, weights.Redundancy)

	total := weights.Relevance + weights.Freshness + weights.Trust + weights.Density + weights.Redundancy
	assert.InDelta(t, 1.0, total, 0.001)
}

func TestDefaultDecayConfig(t *testing.T) {
	config := DefaultDecayConfig()

	assert.Equal(t, 0.01, config.CodeDecayRate)
	assert.Equal(t, 0.05, config.HistoryDecayRate)
	assert.Equal(t, 0.001, config.AcademicDecayRate)
}
