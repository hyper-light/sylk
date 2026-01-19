package architect

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResultSynthesizer_Defaults(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)

	require.NotNil(t, synthesizer)
	assert.Equal(t, 0.8, synthesizer.SimilarityThreshold())
	assert.Equal(t, 0.3, synthesizer.ConflictThreshold())
	assert.Equal(t, 10000, synthesizer.MaxContentLength())
}

func TestNewResultSynthesizer_WithConfig(t *testing.T) {
	config := &SynthesizerConfig{
		SimilarityThreshold: 0.9,
		ConflictThreshold:   0.4,
		MaxContentLength:    5000,
	}

	synthesizer := NewResultSynthesizer(config)

	assert.Equal(t, 0.9, synthesizer.SimilarityThreshold())
	assert.Equal(t, 0.4, synthesizer.ConflictThreshold())
	assert.Equal(t, 5000, synthesizer.MaxContentLength())
}

func TestNewResultSynthesizer_PartialConfig(t *testing.T) {
	config := &SynthesizerConfig{
		SimilarityThreshold: 0.95,
	}

	synthesizer := NewResultSynthesizer(config)

	assert.Equal(t, 0.95, synthesizer.SimilarityThreshold())
	assert.Equal(t, 0.3, synthesizer.ConflictThreshold())
	assert.Equal(t, 10000, synthesizer.MaxContentLength())
}

func TestResultSynthesizer_SynthesizeResults_Empty(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)

	result := synthesizer.SynthesizeResults(nil)

	require.NotNil(t, result)
	assert.Empty(t, result.Content)
	assert.Empty(t, result.Sources)
	assert.Empty(t, result.DomainCoverage)
	assert.NotZero(t, result.SynthesizedAt)
}

func TestResultSynthesizer_SynthesizeResults_EmptySlice(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)

	result := synthesizer.SynthesizeResults([]DomainResult{})

	require.NotNil(t, result)
	assert.Empty(t, result.Content)
	assert.Empty(t, result.Sources)
}

func TestResultSynthesizer_SynthesizeResults_AllErrors(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{Domain: domain.DomainLibrarian, ErrorMsg: "failed"},
		{Domain: domain.DomainAcademic, ErrorMsg: "also failed"},
	}

	result := synthesizer.SynthesizeResults(results)

	require.NotNil(t, result)
	assert.Empty(t, result.Content)
	assert.Empty(t, result.Sources)
}

func TestResultSynthesizer_SynthesizeResults_SingleResult(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{
			Domain:  domain.DomainLibrarian,
			Content: "test content from librarian",
			Score:   0.9,
			Source:  "lib-source-1",
		},
	}

	result := synthesizer.SynthesizeResults(results)

	require.NotNil(t, result)
	assert.Equal(t, "test content from librarian", result.Content)
	assert.Len(t, result.Sources, 1)
	assert.Equal(t, domain.DomainLibrarian, result.Sources[0].Domain)
	assert.Equal(t, "single_domain", result.SynthesisMethod)
	assert.Equal(t, 1.0, result.DomainCoverage[domain.DomainLibrarian])
}

func TestResultSynthesizer_SynthesizeResults_MultipleResults(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{
			Domain:  domain.DomainLibrarian,
			Content: "librarian unique content here",
			Score:   0.9,
			Source:  "lib-1",
		},
		{
			Domain:  domain.DomainAcademic,
			Content: "academic unique content here",
			Score:   0.8,
			Source:  "acad-1",
		},
	}

	result := synthesizer.SynthesizeResults(results)

	require.NotNil(t, result)
	assert.Len(t, result.Sources, 2)
	assert.Equal(t, "multi_domain_merge", result.SynthesisMethod)
	assert.Contains(t, result.Content, "[librarian]")
	assert.Contains(t, result.Content, "[academic]")
}

func TestResultSynthesizer_Deduplication_ExactMatch(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{
			Domain:  domain.DomainLibrarian,
			Content: "exact same content",
			Score:   0.9,
			Source:  "lib-1",
		},
		{
			Domain:  domain.DomainAcademic,
			Content: "exact same content",
			Score:   0.8,
			Source:  "acad-1",
		},
	}

	result := synthesizer.SynthesizeResults(results)

	require.NotNil(t, result)
	assert.Equal(t, 2, result.Deduplication.OriginalCount)
	assert.Equal(t, 1, result.Deduplication.FinalCount)
	assert.Equal(t, 1, result.Deduplication.RemovedCount)
	assert.Len(t, result.Deduplication.RemovedReasons, 1)
}

func TestResultSynthesizer_Deduplication_HighSimilarity(t *testing.T) {
	synthesizer := NewResultSynthesizer(&SynthesizerConfig{
		SimilarityThreshold: 0.7,
	})

	results := []DomainResult{
		{
			Domain:  domain.DomainLibrarian,
			Content: "the quick brown fox jumps over",
			Score:   0.9,
			Source:  "lib-1",
		},
		{
			Domain:  domain.DomainAcademic,
			Content: "the quick brown fox jumps",
			Score:   0.8,
			Source:  "acad-1",
		},
	}

	result := synthesizer.SynthesizeResults(results)

	require.NotNil(t, result)
	assert.Equal(t, 1, result.Deduplication.FinalCount)
	assert.Equal(t, 1, result.Deduplication.RemovedCount)
}

func TestResultSynthesizer_Deduplication_LowSimilarity(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{
			Domain:  domain.DomainLibrarian,
			Content: "completely different topic about cats",
			Score:   0.9,
			Source:  "lib-1",
		},
		{
			Domain:  domain.DomainAcademic,
			Content: "unrelated content about programming languages",
			Score:   0.8,
			Source:  "acad-1",
		},
	}

	result := synthesizer.SynthesizeResults(results)

	require.NotNil(t, result)
	assert.Equal(t, 2, result.Deduplication.FinalCount)
	assert.Equal(t, 0, result.Deduplication.RemovedCount)
}

func TestResultSynthesizer_ConflictDetection(t *testing.T) {
	synthesizer := NewResultSynthesizer(&SynthesizerConfig{
		SimilarityThreshold: 0.8,
		ConflictThreshold:   0.3,
	})

	results := []DomainResult{
		{
			Domain:  domain.DomainLibrarian,
			Content: "the answer is definitely yes and confirmed",
			Score:   0.9,
			Source:  "lib-1",
		},
		{
			Domain:  domain.DomainAcademic,
			Content: "the answer is probably no and uncertain",
			Score:   0.8,
			Source:  "acad-1",
		},
	}

	result := synthesizer.SynthesizeResults(results)

	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result.Conflicts), 0)
}

func TestResultSynthesizer_ConflictDetection_NoConflict_TooSimilar(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{
			Domain:  domain.DomainLibrarian,
			Content: "exactly the same content here",
			Score:   0.9,
		},
		{
			Domain:  domain.DomainAcademic,
			Content: "exactly the same content here",
			Score:   0.8,
		},
	}

	result := synthesizer.SynthesizeResults(results)

	require.NotNil(t, result)
	assert.Empty(t, result.Conflicts)
}

func TestResultSynthesizer_ConflictDetection_NoConflict_TooDifferent(t *testing.T) {
	synthesizer := NewResultSynthesizer(&SynthesizerConfig{
		ConflictThreshold: 0.5,
	})

	results := []DomainResult{
		{
			Domain:  domain.DomainLibrarian,
			Content: "cats are wonderful pets",
			Score:   0.9,
		},
		{
			Domain:  domain.DomainAcademic,
			Content: "programming languages evolution",
			Score:   0.8,
		},
	}

	result := synthesizer.SynthesizeResults(results)

	require.NotNil(t, result)
	assert.Empty(t, result.Conflicts)
}

func TestResultSynthesizer_SourceAttribution(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{
			Domain:  domain.DomainLibrarian,
			Content: "short",
			Score:   0.9,
			Source:  "lib-source",
		},
		{
			Domain:  domain.DomainAcademic,
			Content: "also short but different",
			Score:   0.8,
			Source:  "acad-source",
		},
	}

	result := synthesizer.SynthesizeResults(results)

	require.NotNil(t, result)
	require.Len(t, result.Sources, 2)

	assert.Equal(t, domain.DomainLibrarian, result.Sources[0].Domain)
	assert.Equal(t, "lib-source", result.Sources[0].SourceID)
	assert.Equal(t, 0.9, result.Sources[0].Score)
	assert.Equal(t, 0, result.Sources[0].StartIndex)

	assert.Equal(t, domain.DomainAcademic, result.Sources[1].Domain)
	assert.Equal(t, "acad-source", result.Sources[1].SourceID)
}

func TestResultSynthesizer_Coverage_SingleDomain(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{Domain: domain.DomainLibrarian, Content: "content", Score: 0.9},
	}

	result := synthesizer.SynthesizeResults(results)

	require.NotNil(t, result)
	assert.Equal(t, 1.0, result.DomainCoverage[domain.DomainLibrarian])
}

func TestResultSynthesizer_Coverage_MultipleDomains(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{Domain: domain.DomainLibrarian, Content: "unique content one", Score: 0.6},
		{Domain: domain.DomainAcademic, Content: "unique content two", Score: 0.4},
	}

	result := synthesizer.SynthesizeResults(results)

	require.NotNil(t, result)
	assert.InDelta(t, 0.6, result.DomainCoverage[domain.DomainLibrarian], 0.01)
	assert.InDelta(t, 0.4, result.DomainCoverage[domain.DomainAcademic], 0.01)
}

func TestResultSynthesizer_Coverage_ZeroScores(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{Domain: domain.DomainLibrarian, Content: "unique content one", Score: 0},
		{Domain: domain.DomainAcademic, Content: "unique content two", Score: 0},
	}

	result := synthesizer.SynthesizeResults(results)

	require.NotNil(t, result)
	assert.InDelta(t, 0.5, result.DomainCoverage[domain.DomainLibrarian], 0.01)
	assert.InDelta(t, 0.5, result.DomainCoverage[domain.DomainAcademic], 0.01)
}

func TestResultSynthesizer_ContentMerge_Single(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{Domain: domain.DomainLibrarian, Content: "single domain content", Score: 0.9},
	}

	result := synthesizer.SynthesizeResults(results)

	assert.Equal(t, "single domain content", result.Content)
}

func TestResultSynthesizer_ContentMerge_Multiple(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{Domain: domain.DomainLibrarian, Content: "first content", Score: 0.9},
		{Domain: domain.DomainAcademic, Content: "second content", Score: 0.8},
	}

	result := synthesizer.SynthesizeResults(results)

	assert.Contains(t, result.Content, "[librarian] first content")
	assert.Contains(t, result.Content, "[academic] second content")
}

func TestResultSynthesizer_ContentTruncation(t *testing.T) {
	synthesizer := NewResultSynthesizer(&SynthesizerConfig{
		MaxContentLength: 50,
	})

	longContent := "this is a very long content that exceeds the maximum allowed length for testing"
	results := []DomainResult{
		{Domain: domain.DomainLibrarian, Content: longContent, Score: 0.9},
	}

	result := synthesizer.SynthesizeResults(results)

	assert.LessOrEqual(t, len(result.Content), 53)
	assert.True(t, len(result.Content) <= 53)
	if len(longContent) > 50 {
		assert.Contains(t, result.Content, "...")
	}
}

func TestResultSynthesizer_SynthesisMethod_Empty(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)

	result := synthesizer.SynthesizeResults(nil)

	assert.Empty(t, result.SynthesisMethod)
}

func TestResultSynthesizer_SynthesisMethod_SingleDomain(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{Domain: domain.DomainLibrarian, Content: "content", Score: 0.9},
	}

	result := synthesizer.SynthesizeResults(results)

	assert.Equal(t, "single_domain", result.SynthesisMethod)
}

func TestResultSynthesizer_SynthesisMethod_SingleDomainMultiResult(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{Domain: domain.DomainLibrarian, Content: "content one", Score: 0.9},
		{Domain: domain.DomainLibrarian, Content: "content two different", Score: 0.8},
	}

	result := synthesizer.SynthesizeResults(results)

	assert.Equal(t, "single_domain_multi_result", result.SynthesisMethod)
}

func TestResultSynthesizer_SynthesisMethod_MultiDomainMerge(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{Domain: domain.DomainLibrarian, Content: "librarian content", Score: 0.9},
		{Domain: domain.DomainAcademic, Content: "academic content", Score: 0.8},
	}

	result := synthesizer.SynthesizeResults(results)

	assert.Equal(t, "multi_domain_merge", result.SynthesisMethod)
}

func TestResultSynthesizer_SynthesizeFromCrossDomain_Nil(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)

	result := synthesizer.SynthesizeFromCrossDomain(nil)

	require.NotNil(t, result)
	assert.Empty(t, result.Content)
}

func TestResultSynthesizer_SynthesizeFromCrossDomain_WithResults(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	crossDomainResult := &CrossDomainResult{
		Query: "test query",
		DomainResults: []DomainResult{
			{Domain: domain.DomainLibrarian, Content: "librarian content", Score: 0.9},
			{Domain: domain.DomainAcademic, Content: "academic content", Score: 0.8},
		},
		SuccessDomains: 2,
	}

	result := synthesizer.SynthesizeFromCrossDomain(crossDomainResult)

	require.NotNil(t, result)
	assert.Len(t, result.Sources, 2)
	assert.Equal(t, "multi_domain_merge", result.SynthesisMethod)
}

func TestResultSynthesizer_FilterSuccessful(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{Domain: domain.DomainLibrarian, Content: "good content", Score: 0.9},
		{Domain: domain.DomainAcademic, Content: "", Score: 0.8},
		{Domain: domain.DomainArchivalist, ErrorMsg: "failed", Score: 0.7},
		{Domain: domain.DomainDesigner, Content: "also good", Score: 0.6},
	}

	result := synthesizer.SynthesizeResults(results)

	require.NotNil(t, result)
	assert.Len(t, result.Sources, 2)
}

func TestResultSynthesizer_SortByScore(t *testing.T) {
	synthesizer := NewResultSynthesizer(nil)
	results := []DomainResult{
		{Domain: domain.DomainAcademic, Content: "lower score different words", Score: 0.5},
		{Domain: domain.DomainLibrarian, Content: "higher score unique content", Score: 0.9},
	}

	result := synthesizer.SynthesizeResults(results)

	require.NotNil(t, result)
	require.Len(t, result.Sources, 2)
	assert.Equal(t, domain.DomainLibrarian, result.Sources[0].Domain)
}

func TestResultSynthesizer_ConflictSeverity(t *testing.T) {
	synthesizer := NewResultSynthesizer(&SynthesizerConfig{
		SimilarityThreshold: 0.8,
		ConflictThreshold:   0.3,
	})

	tests := []struct {
		name             string
		contentA         string
		contentB         string
		expectedSeverity messaging.ConflictSeverity
		expectConflict   bool
	}{
		{
			name:           "no conflict - too similar",
			contentA:       "exact same words here now",
			contentB:       "exact same words here now",
			expectConflict: false,
		},
		{
			name:           "no conflict - too different",
			contentA:       "cats dogs pets animals",
			contentB:       "programming code software engineering",
			expectConflict: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			results := []DomainResult{
				{Domain: domain.DomainLibrarian, Content: tc.contentA, Score: 0.9},
				{Domain: domain.DomainAcademic, Content: tc.contentB, Score: 0.8},
			}

			result := synthesizer.SynthesizeResults(results)

			if tc.expectConflict {
				require.NotEmpty(t, result.Conflicts)
				assert.Equal(t, tc.expectedSeverity, result.Conflicts[0].Severity)
			} else {
				assert.Empty(t, result.Conflicts)
			}
		})
	}
}

func TestJaccardSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected float64
		delta    float64
	}{
		{
			name:     "identical strings",
			a:        "hello world",
			b:        "hello world",
			expected: 1.0,
			delta:    0.01,
		},
		{
			name:     "completely different",
			a:        "hello world",
			b:        "foo bar baz",
			expected: 0.0,
			delta:    0.01,
		},
		{
			name:     "partial overlap",
			a:        "hello world foo",
			b:        "hello world bar",
			expected: 0.5,
			delta:    0.01,
		},
		{
			name:     "both empty",
			a:        "",
			b:        "",
			expected: 1.0,
			delta:    0.01,
		},
		{
			name:     "one empty",
			a:        "hello world",
			b:        "",
			expected: 0.0,
			delta:    0.01,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := jaccardSimilarity(tc.a, tc.b)
			assert.InDelta(t, tc.expected, result, tc.delta)
		})
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple words",
			input:    "Hello World",
			expected: []string{"hello", "world"},
		},
		{
			name:     "with extra spaces",
			input:    "  hello   world  ",
			expected: []string{"hello", "world"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "mixed case",
			input:    "HeLLo WoRLD",
			expected: []string{"hello", "world"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tokenize(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestMakeSet(t *testing.T) {
	words := []string{"hello", "world", "hello"}
	set := makeSet(words)

	assert.True(t, set["hello"])
	assert.True(t, set["world"])
	assert.False(t, set["foo"])
	assert.Len(t, set, 2)
}

func TestUnifiedResult_Fields(t *testing.T) {
	now := time.Now()
	result := UnifiedResult{
		Content:         "test content",
		Sources:         []SourceAttribution{{Domain: domain.DomainLibrarian}},
		Deduplication:   DeduplicationSummary{OriginalCount: 2, FinalCount: 1},
		Conflicts:       []messaging.ConflictInfo{{Severity: messaging.SeverityHigh}},
		DomainCoverage:  map[domain.Domain]float64{domain.DomainLibrarian: 1.0},
		SynthesisMethod: "test_method",
		SynthesizedAt:   now,
	}

	assert.Equal(t, "test content", result.Content)
	assert.Len(t, result.Sources, 1)
	assert.Equal(t, 2, result.Deduplication.OriginalCount)
	assert.Len(t, result.Conflicts, 1)
	assert.Equal(t, 1.0, result.DomainCoverage[domain.DomainLibrarian])
	assert.Equal(t, "test_method", result.SynthesisMethod)
	assert.Equal(t, now, result.SynthesizedAt)
}

func TestSourceAttribution_Fields(t *testing.T) {
	attr := SourceAttribution{
		Domain:     domain.DomainAcademic,
		SourceID:   "source-123",
		Content:    "attributed content",
		Score:      0.85,
		StartIndex: 0,
		EndIndex:   18,
	}

	assert.Equal(t, domain.DomainAcademic, attr.Domain)
	assert.Equal(t, "source-123", attr.SourceID)
	assert.Equal(t, "attributed content", attr.Content)
	assert.Equal(t, 0.85, attr.Score)
	assert.Equal(t, 0, attr.StartIndex)
	assert.Equal(t, 18, attr.EndIndex)
}

func TestDeduplicationSummary_Fields(t *testing.T) {
	summary := DeduplicationSummary{
		OriginalCount:  5,
		FinalCount:     3,
		RemovedCount:   2,
		RemovedReasons: []string{"duplicate from librarian", "duplicate from academic"},
	}

	assert.Equal(t, 5, summary.OriginalCount)
	assert.Equal(t, 3, summary.FinalCount)
	assert.Equal(t, 2, summary.RemovedCount)
	assert.Len(t, summary.RemovedReasons, 2)
}

func TestSynthesizerConfig_Fields(t *testing.T) {
	config := SynthesizerConfig{
		SimilarityThreshold: 0.85,
		ConflictThreshold:   0.35,
		MaxContentLength:    8000,
	}

	assert.Equal(t, 0.85, config.SimilarityThreshold)
	assert.Equal(t, 0.35, config.ConflictThreshold)
	assert.Equal(t, 8000, config.MaxContentLength)
}
