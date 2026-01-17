package errors

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mockArchivalistClient implements ArchivalistClient for testing.
type mockArchivalistClient struct {
	failures    []*AttemptSummary
	queryErr    error
	recordErr   error
	recordCalls int
}

func (m *mockArchivalistClient) QueryFailures(ctx context.Context, pipelineID string) ([]*AttemptSummary, error) {
	if m.queryErr != nil {
		return nil, m.queryErr
	}
	return m.failures, nil
}

func (m *mockArchivalistClient) RecordAttempt(ctx context.Context, pipelineID string, summary *AttemptSummary) error {
	m.recordCalls++
	return m.recordErr
}

func TestAttemptSummary_Creation(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name           string
		summary        AttemptSummary
		wantNumber     int
		wantTier       ErrorTier
		wantTokens     int
		wantApproach   string
		wantPhaseCount int
	}{
		{
			name: "basic creation",
			summary: AttemptSummary{
				AttemptNumber:  1,
				Timestamp:      now,
				Approach:       "direct API call",
				Error:          "connection timeout",
				ErrorTier:      TierTransient,
				TokensSpent:    1500,
				PhasesComplete: []string{"parse", "validate"},
				Duration:       5 * time.Second,
			},
			wantNumber:     1,
			wantTier:       TierTransient,
			wantTokens:     1500,
			wantApproach:   "direct API call",
			wantPhaseCount: 2,
		},
		{
			name: "minimal summary",
			summary: AttemptSummary{
				AttemptNumber: 1,
				Error:         "failed",
			},
			wantNumber:     1,
			wantTier:       ErrorTier(0),
			wantTokens:     0,
			wantApproach:   "",
			wantPhaseCount: 0,
		},
		{
			name: "permanent error tier",
			summary: AttemptSummary{
				AttemptNumber: 3,
				Approach:      "fallback method",
				Error:         "not found",
				ErrorTier:     TierPermanent,
			},
			wantNumber:   3,
			wantTier:     TierPermanent,
			wantApproach: "fallback method",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.summary.AttemptNumber != tt.wantNumber {
				t.Errorf("AttemptNumber = %v, want %v", tt.summary.AttemptNumber, tt.wantNumber)
			}
			if tt.summary.ErrorTier != tt.wantTier {
				t.Errorf("ErrorTier = %v, want %v", tt.summary.ErrorTier, tt.wantTier)
			}
			if tt.summary.TokensSpent != tt.wantTokens {
				t.Errorf("TokensSpent = %v, want %v", tt.summary.TokensSpent, tt.wantTokens)
			}
			if tt.summary.Approach != tt.wantApproach {
				t.Errorf("Approach = %v, want %v", tt.summary.Approach, tt.wantApproach)
			}
			if len(tt.summary.PhasesComplete) != tt.wantPhaseCount {
				t.Errorf("PhasesComplete count = %v, want %v", len(tt.summary.PhasesComplete), tt.wantPhaseCount)
			}
		})
	}
}

func TestRetryBriefing_HasPriorAttempts(t *testing.T) {
	tests := []struct {
		name     string
		briefing RetryBriefing
		want     bool
	}{
		{
			name:     "no prior attempts",
			briefing: RetryBriefing{PriorAttempts: nil},
			want:     false,
		},
		{
			name:     "empty prior attempts slice",
			briefing: RetryBriefing{PriorAttempts: []*AttemptSummary{}},
			want:     false,
		},
		{
			name: "with prior attempts",
			briefing: RetryBriefing{
				PriorAttempts: []*AttemptSummary{
					{AttemptNumber: 1, Error: "timeout"},
				},
			},
			want: true,
		},
		{
			name: "multiple prior attempts",
			briefing: RetryBriefing{
				PriorAttempts: []*AttemptSummary{
					{AttemptNumber: 1, Error: "timeout"},
					{AttemptNumber: 2, Error: "rate limited"},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.briefing.HasPriorAttempts(); got != tt.want {
				t.Errorf("HasPriorAttempts() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRetryBriefing_LastFailureReason(t *testing.T) {
	tests := []struct {
		name     string
		briefing RetryBriefing
		want     string
	}{
		{
			name:     "no prior attempts returns empty",
			briefing: RetryBriefing{PriorAttempts: nil},
			want:     "",
		},
		{
			name:     "empty slice returns empty",
			briefing: RetryBriefing{PriorAttempts: []*AttemptSummary{}},
			want:     "",
		},
		{
			name: "single attempt returns its error",
			briefing: RetryBriefing{
				PriorAttempts: []*AttemptSummary{
					{AttemptNumber: 1, Error: "connection refused"},
				},
			},
			want: "connection refused",
		},
		{
			name: "multiple attempts returns last error",
			briefing: RetryBriefing{
				PriorAttempts: []*AttemptSummary{
					{AttemptNumber: 1, Error: "timeout"},
					{AttemptNumber: 2, Error: "rate limited"},
					{AttemptNumber: 3, Error: "service unavailable"},
				},
			},
			want: "service unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.briefing.LastFailureReason(); got != tt.want {
				t.Errorf("LastFailureReason() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRetryBriefingService_PrepareRetryBriefing_WithArchivalist(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		config         RetryBriefingConfig
		failures       []*AttemptSummary
		queryErr       error
		currentError   error
		wantAttemptNum int
		wantPriorCount int
		wantAnalysis   string
		wantSuggestion string
		wantAvoidCount int
	}{
		{
			name:   "with prior attempts",
			config: DefaultRetryBriefingConfig(),
			failures: []*AttemptSummary{
				{AttemptNumber: 1, Approach: "method A", Error: "failed", ErrorTier: TierTransient},
				{AttemptNumber: 2, Approach: "method B", Error: "timeout", ErrorTier: TierTransient},
			},
			currentError:   errors.New("new error"),
			wantAttemptNum: 3,
			wantPriorCount: 2,
			wantAnalysis:   "2 prior failures, primarily transient errors",
			wantSuggestion: "previous transient error may have resolved",
			wantAvoidCount: 2,
		},
		{
			name:           "query error returns empty attempts",
			config:         DefaultRetryBriefingConfig(),
			queryErr:       errors.New("database error"),
			currentError:   NewTieredError(TierPermanent, "permanent failure", nil),
			wantAttemptNum: 1,
			wantPriorCount: 0,
			wantSuggestion: "investigate root cause before retry",
		},
		{
			name:   "limits attempts to max",
			config: RetryBriefingConfig{MaxPriorAttempts: 2, AnalysisEnabled: true},
			failures: []*AttemptSummary{
				{AttemptNumber: 1, Approach: "A", ErrorTier: TierTransient},
				{AttemptNumber: 2, Approach: "B", ErrorTier: TierTransient},
				{AttemptNumber: 3, Approach: "C", ErrorTier: TierTransient},
				{AttemptNumber: 4, Approach: "D", ErrorTier: TierTransient},
			},
			currentError:   errors.New("error"),
			wantAttemptNum: 3,
			wantPriorCount: 2,
			wantAvoidCount: 2, // only C and D (last 2)
		},
		{
			name:   "analysis disabled",
			config: RetryBriefingConfig{MaxPriorAttempts: 5, AnalysisEnabled: false},
			failures: []*AttemptSummary{
				{AttemptNumber: 1, ErrorTier: TierPermanent},
			},
			currentError:   errors.New("error"),
			wantAttemptNum: 2,
			wantPriorCount: 1,
			wantAnalysis:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockArchivalistClient{
				failures: tt.failures,
				queryErr: tt.queryErr,
			}
			svc := NewRetryBriefingService(tt.config, mock)

			briefing, err := svc.PrepareRetryBriefing(ctx, "pipeline-123", tt.currentError)
			if err != nil {
				t.Fatalf("PrepareRetryBriefing() error = %v", err)
			}

			if briefing.AttemptNumber != tt.wantAttemptNum {
				t.Errorf("AttemptNumber = %v, want %v", briefing.AttemptNumber, tt.wantAttemptNum)
			}
			if len(briefing.PriorAttempts) != tt.wantPriorCount {
				t.Errorf("PriorAttempts count = %v, want %v", len(briefing.PriorAttempts), tt.wantPriorCount)
			}
			if tt.wantAnalysis != "" && briefing.FailureAnalysis != tt.wantAnalysis {
				t.Errorf("FailureAnalysis = %v, want %v", briefing.FailureAnalysis, tt.wantAnalysis)
			}
			if tt.wantSuggestion != "" && briefing.SuggestedApproach != tt.wantSuggestion {
				t.Errorf("SuggestedApproach = %v, want %v", briefing.SuggestedApproach, tt.wantSuggestion)
			}
			if tt.wantAvoidCount > 0 && len(briefing.AvoidPatterns) != tt.wantAvoidCount {
				t.Errorf("AvoidPatterns count = %v, want %v", len(briefing.AvoidPatterns), tt.wantAvoidCount)
			}
		})
	}
}

func TestRetryBriefingService_PrepareRetryBriefing_NilArchivalist(t *testing.T) {
	ctx := context.Background()
	config := DefaultRetryBriefingConfig()
	svc := NewRetryBriefingService(config, nil)

	testErr := NewTieredError(TierTransient, "timeout", nil)
	briefing, err := svc.PrepareRetryBriefing(ctx, "pipeline-456", testErr)

	if err != nil {
		t.Fatalf("PrepareRetryBriefing() error = %v", err)
	}

	if briefing.AttemptNumber != 1 {
		t.Errorf("AttemptNumber = %v, want 1 (no prior attempts)", briefing.AttemptNumber)
	}
	if briefing.HasPriorAttempts() {
		t.Error("HasPriorAttempts() = true, want false for nil archivalist")
	}
	if briefing.SuggestedApproach != "retry with exponential backoff" {
		t.Errorf("SuggestedApproach = %v, want 'retry with exponential backoff'", briefing.SuggestedApproach)
	}
}

func TestRetryBriefingService_FormatForAgent(t *testing.T) {
	tests := []struct {
		name     string
		briefing *RetryBriefing
		want     string
		contains []string
	}{
		{
			name:     "nil briefing returns empty",
			briefing: nil,
			want:     "",
		},
		{
			name: "first attempt with no prior failures",
			briefing: &RetryBriefing{
				AttemptNumber: 1,
				PriorAttempts: nil,
			},
			contains: []string{"Attempt 1."},
		},
		{
			name: "retry with prior failure",
			briefing: &RetryBriefing{
				AttemptNumber: 2,
				PriorAttempts: []*AttemptSummary{
					{AttemptNumber: 1, Error: "connection timeout"},
				},
				AvoidPatterns:     []string{"direct connection"},
				SuggestedApproach: "use proxy",
			},
			contains: []string{
				"Attempt 2.",
				"Prior attempt failed due to connection timeout.",
				"Avoid direct connection.",
				"Try use proxy.",
			},
		},
		{
			name: "multiple avoid patterns",
			briefing: &RetryBriefing{
				AttemptNumber: 3,
				PriorAttempts: []*AttemptSummary{
					{AttemptNumber: 1, Error: "first"},
					{AttemptNumber: 2, Error: "second error"},
				},
				AvoidPatterns: []string{"method A", "method B"},
			},
			contains: []string{
				"Attempt 3.",
				"Prior attempt failed due to second error.",
				"Avoid method A, method B.",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatForAgent(tt.briefing)

			if tt.want != "" && got != tt.want {
				t.Errorf("FormatForAgent() = %v, want %v", got, tt.want)
			}

			for _, substr := range tt.contains {
				if got != "" && !containsString(got, substr) {
					t.Errorf("FormatForAgent() = %v, want to contain %q", got, substr)
				}
			}
		})
	}
}

func TestRetryBriefingService_AnalyzeFailurePatterns(t *testing.T) {
	tests := []struct {
		name     string
		config   RetryBriefingConfig
		attempts []*AttemptSummary
		want     string
	}{
		{
			name:     "empty attempts returns empty",
			config:   DefaultRetryBriefingConfig(),
			attempts: nil,
			want:     "",
		},
		{
			name:     "analysis disabled returns empty",
			config:   RetryBriefingConfig{AnalysisEnabled: false},
			attempts: []*AttemptSummary{{ErrorTier: TierTransient}},
			want:     "",
		},
		{
			name:   "single transient failure",
			config: DefaultRetryBriefingConfig(),
			attempts: []*AttemptSummary{
				{ErrorTier: TierTransient},
			},
			want: "1 prior failures, primarily transient errors",
		},
		{
			name:   "mixed failures shows dominant",
			config: DefaultRetryBriefingConfig(),
			attempts: []*AttemptSummary{
				{ErrorTier: TierTransient},
				{ErrorTier: TierTransient},
				{ErrorTier: TierExternalRateLimit},
			},
			want: "3 prior failures, primarily transient errors",
		},
		{
			name:   "rate limit dominant",
			config: DefaultRetryBriefingConfig(),
			attempts: []*AttemptSummary{
				{ErrorTier: TierExternalRateLimit},
				{ErrorTier: TierExternalRateLimit},
				{ErrorTier: TierTransient},
			},
			want: "3 prior failures, primarily external_rate_limit errors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewRetryBriefingService(tt.config, nil)
			got := svc.analyzeFailurePatterns(tt.attempts)
			if got != tt.want {
				t.Errorf("analyzeFailurePatterns() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRetryBriefingService_ExtractAvoidPatterns(t *testing.T) {
	tests := []struct {
		name     string
		attempts []*AttemptSummary
		want     []string
	}{
		{
			name:     "nil attempts returns nil",
			attempts: nil,
			want:     nil,
		},
		{
			name:     "empty attempts returns nil",
			attempts: []*AttemptSummary{},
			want:     nil,
		},
		{
			name: "extracts unique approaches",
			attempts: []*AttemptSummary{
				{Approach: "direct call"},
				{Approach: "batch request"},
				{Approach: "direct call"}, // duplicate
			},
			want: []string{"direct call", "batch request"},
		},
		{
			name: "skips empty approaches",
			attempts: []*AttemptSummary{
				{Approach: "method A"},
				{Approach: ""},
				{Approach: "method B"},
			},
			want: []string{"method A", "method B"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewRetryBriefingService(DefaultRetryBriefingConfig(), nil)
			got := svc.extractAvoidPatterns(tt.attempts)

			if tt.want == nil {
				if got != nil {
					t.Errorf("extractAvoidPatterns() = %v, want nil", got)
				}
				return
			}

			if len(got) != len(tt.want) {
				t.Errorf("extractAvoidPatterns() length = %v, want %v", len(got), len(tt.want))
				return
			}

			for i, v := range tt.want {
				if got[i] != v {
					t.Errorf("extractAvoidPatterns()[%d] = %v, want %v", i, got[i], v)
				}
			}
		})
	}
}

func TestRetryBriefingConfig_Defaults(t *testing.T) {
	config := DefaultRetryBriefingConfig()

	if config.MaxPriorAttempts != 5 {
		t.Errorf("MaxPriorAttempts = %v, want 5", config.MaxPriorAttempts)
	}
	if !config.AnalysisEnabled {
		t.Error("AnalysisEnabled = false, want true")
	}
}

func TestRetryBriefingService_RecordAttempt(t *testing.T) {
	ctx := context.Background()

	t.Run("nil archivalist returns nil", func(t *testing.T) {
		svc := NewRetryBriefingService(DefaultRetryBriefingConfig(), nil)
		err := svc.RecordAttempt(ctx, "pipeline-1", &AttemptSummary{})
		if err != nil {
			t.Errorf("RecordAttempt() error = %v, want nil", err)
		}
	})

	t.Run("calls archivalist RecordAttempt", func(t *testing.T) {
		mock := &mockArchivalistClient{}
		svc := NewRetryBriefingService(DefaultRetryBriefingConfig(), mock)

		summary := &AttemptSummary{AttemptNumber: 1, Error: "test"}
		err := svc.RecordAttempt(ctx, "pipeline-2", summary)

		if err != nil {
			t.Errorf("RecordAttempt() error = %v", err)
		}
		if mock.recordCalls != 1 {
			t.Errorf("RecordAttempt() calls = %v, want 1", mock.recordCalls)
		}
	})

	t.Run("returns archivalist error", func(t *testing.T) {
		wantErr := errors.New("record failed")
		mock := &mockArchivalistClient{recordErr: wantErr}
		svc := NewRetryBriefingService(DefaultRetryBriefingConfig(), mock)

		err := svc.RecordAttempt(ctx, "pipeline-3", &AttemptSummary{})
		if err != wantErr {
			t.Errorf("RecordAttempt() error = %v, want %v", err, wantErr)
		}
	})
}

func TestRetryBriefingService_SuggestApproach_AllTiers(t *testing.T) {
	svc := NewRetryBriefingService(DefaultRetryBriefingConfig(), nil)

	tests := []struct {
		tier ErrorTier
		want string
	}{
		{TierTransient, "retry with exponential backoff"},
		{TierPermanent, "investigate root cause before retry"},
		{TierUserFixable, "verify configuration and credentials"},
		{TierExternalRateLimit, "wait for rate limit reset"},
		{TierExternalDegrading, "try alternative approach or service"},
		{ErrorTier(999), "proceed with caution"},
	}

	for _, tt := range tests {
		t.Run(tt.tier.String(), func(t *testing.T) {
			err := NewTieredError(tt.tier, "test", nil)
			got := svc.suggestForNewAttempt(err)
			if got != tt.want {
				t.Errorf("suggestForNewAttempt(%v) = %v, want %v", tt.tier, got, tt.want)
			}
		})
	}
}

func TestRetryBriefingService_SuggestBasedOnHistory(t *testing.T) {
	svc := NewRetryBriefingService(DefaultRetryBriefingConfig(), nil)

	t.Run("transient last error", func(t *testing.T) {
		attempts := []*AttemptSummary{
			{ErrorTier: TierTransient, Approach: "method A"},
		}
		got := svc.suggestBasedOnHistory(attempts)
		if got != "previous transient error may have resolved" {
			t.Errorf("got %v", got)
		}
	})

	t.Run("non-transient last error", func(t *testing.T) {
		attempts := []*AttemptSummary{
			{ErrorTier: TierPermanent, Approach: "bad method"},
		}
		got := svc.suggestBasedOnHistory(attempts)
		if got != "avoid approach: bad method" {
			t.Errorf("got %v, want 'avoid approach: bad method'", got)
		}
	})
}

// containsString checks if s contains substr.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
