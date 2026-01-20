package errors

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestErrorTierString(t *testing.T) {
	tests := []struct {
		tier     ErrorTier
		expected string
	}{
		{TierTransient, "transient"},
		{TierPermanent, "permanent"},
		{TierUserFixable, "user_fixable"},
		{TierExternalRateLimit, "external_rate_limit"},
		{TierExternalDegrading, "external_degrading"},
		{ErrorTier(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.tier.String(); got != tt.expected {
				t.Errorf("ErrorTier.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTieredErrorError(t *testing.T) {
	t.Run("with underlying error", func(t *testing.T) {
		underlying := errors.New("base error")
		err := NewTieredError(TierTransient, "wrapped", underlying)
		expected := "[transient] wrapped: base error"
		if err.Error() != expected {
			t.Errorf("Error() = %v, want %v", err.Error(), expected)
		}
	})

	t.Run("without underlying error", func(t *testing.T) {
		err := NewTieredError(TierPermanent, "simple error", nil)
		expected := "[permanent] simple error"
		if err.Error() != expected {
			t.Errorf("Error() = %v, want %v", err.Error(), expected)
		}
	})
}

func TestTieredErrorUnwrap(t *testing.T) {
	underlying := errors.New("base error")
	err := NewTieredError(TierTransient, "wrapped", underlying)

	if err.Unwrap() != underlying {
		t.Errorf("Unwrap() did not return underlying error")
	}
}

func TestTieredErrorIs(t *testing.T) {
	err1 := NewTieredError(TierTransient, "error 1", nil)
	err2 := NewTieredError(TierTransient, "error 2", nil)
	err3 := NewTieredError(TierPermanent, "error 3", nil)

	if !err1.Is(err2) {
		t.Error("errors with same tier should match with Is()")
	}

	if err1.Is(err3) {
		t.Error("errors with different tiers should not match with Is()")
	}

	if err1.Is(errors.New("plain error")) {
		t.Error("TieredError.Is should return false for non-TieredError")
	}
}

func TestWithStatusCode(t *testing.T) {
	err := NewTieredError(TierExternalRateLimit, "rate limited", nil)
	err = err.WithStatusCode(http.StatusTooManyRequests)

	if err.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %v, want %v", err.StatusCode, http.StatusTooManyRequests)
	}
}

func TestWithRetryAfter(t *testing.T) {
	err := NewTieredError(TierExternalRateLimit, "rate limited", nil)
	duration := 30 * time.Second
	err = err.WithRetryAfter(duration)

	if err.RetryAfter != duration {
		t.Errorf("RetryAfter = %v, want %v", err.RetryAfter, duration)
	}
}

func TestWithContext(t *testing.T) {
	err := NewTieredError(TierPermanent, "error", nil)
	err = err.WithContext("key1", "value1").WithContext("key2", "value2")

	if err.Context["key1"] != "value1" {
		t.Errorf("Context[key1] = %v, want value1", err.Context["key1"])
	}
	if err.Context["key2"] != "value2" {
		t.Errorf("Context[key2] = %v, want value2", err.Context["key2"])
	}
}

func TestGetTier(t *testing.T) {
	t.Run("tiered error returns tier", func(t *testing.T) {
		err := NewTieredError(TierUserFixable, "user error", nil)
		if GetTier(err) != TierUserFixable {
			t.Errorf("GetTier() = %v, want %v", GetTier(err), TierUserFixable)
		}
	})

	t.Run("plain error returns permanent", func(t *testing.T) {
		err := errors.New("plain error")
		if GetTier(err) != TierPermanent {
			t.Errorf("GetTier() = %v, want %v", GetTier(err), TierPermanent)
		}
	})
}

func TestGetBehavior(t *testing.T) {
	err := NewTieredError(TierTransient, "transient", nil)
	behavior := GetBehavior(err)

	if !behavior.ShouldRetry {
		t.Error("Transient errors should be retryable")
	}
	if behavior.ShouldNotify {
		t.Error("Transient errors should not notify")
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		tier     ErrorTier
		expected bool
	}{
		{TierTransient, true},
		{TierPermanent, false},
		{TierUserFixable, false},
		{TierExternalRateLimit, true},
		{TierExternalDegrading, true},
	}

	for _, tt := range tests {
		t.Run(tt.tier.String(), func(t *testing.T) {
			err := NewTieredError(tt.tier, "test", nil)
			if got := IsRetryable(err); got != tt.expected {
				t.Errorf("IsRetryable() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestWrapWithTier(t *testing.T) {
	t.Run("nil error returns nil", func(t *testing.T) {
		if WrapWithTier(TierPermanent, "msg", nil) != nil {
			t.Error("WrapWithTier(nil) should return nil")
		}
	})

	t.Run("plain error gets tier", func(t *testing.T) {
		base := errors.New("base")
		wrapped := WrapWithTier(TierTransient, "wrapped", base)
		if GetTier(wrapped) != TierTransient {
			t.Errorf("wrapped error tier = %v, want %v", GetTier(wrapped), TierTransient)
		}
	})

	t.Run("tiered error preserves original tier", func(t *testing.T) {
		original := NewTieredError(TierUserFixable, "original", nil)
		wrapped := WrapWithTier(TierPermanent, "wrapped", original)
		if GetTier(wrapped) != TierUserFixable {
			t.Errorf("wrapped tier = %v, want preserved %v", GetTier(wrapped), TierUserFixable)
		}
	})
}

func TestSentinelErrors(t *testing.T) {
	tests := []struct {
		err  *TieredError
		tier ErrorTier
	}{
		{ErrTimeout, TierTransient},
		{ErrTemporaryFailure, TierTransient},
		{ErrConnectionReset, TierTransient},
		{ErrNotFound, TierPermanent},
		{ErrInvalidInput, TierPermanent},
		{ErrUnauthorized, TierPermanent},
		{ErrForbidden, TierPermanent},
		{ErrMissingConfig, TierUserFixable},
		{ErrInvalidCredentials, TierUserFixable},
		{ErrMissingAPIKey, TierUserFixable},
		{ErrRateLimited, TierExternalRateLimit},
		{ErrQuotaExceeded, TierExternalRateLimit},
		{ErrServiceUnavailable, TierExternalDegrading},
		{ErrBadGateway, TierExternalDegrading},
		{ErrGatewayTimeout, TierExternalDegrading},
	}

	for _, tt := range tests {
		t.Run(tt.err.Message, func(t *testing.T) {
			if tt.err.Tier != tt.tier {
				t.Errorf("%s tier = %v, want %v", tt.err.Message, tt.err.Tier, tt.tier)
			}
		})
	}
}

func TestDefaultBehaviors(t *testing.T) {
	behaviors := DefaultBehaviors()

	if len(behaviors) != 5 {
		t.Errorf("DefaultBehaviors() returned %d behaviors, want 5", len(behaviors))
	}

	for tier, behavior := range behaviors {
		if tier == TierTransient && !behavior.ShouldRetry {
			t.Error("Transient tier should have ShouldRetry=true")
		}
		if tier == TierPermanent && behavior.ShouldRetry {
			t.Error("Permanent tier should have ShouldRetry=false")
		}
	}
}

func TestErrorClassifier_Classify(t *testing.T) {
	classifier := NewErrorClassifier()

	tests := []struct {
		name     string
		err      error
		expected ErrorTier
	}{
		{"nil error", nil, TierPermanent},
		{"already tiered", NewTieredError(TierUserFixable, "test", nil), TierUserFixable},
		{"rate limit text", errors.New("rate limit exceeded"), TierExternalRateLimit},
		{"too many requests", errors.New("too many requests"), TierExternalRateLimit},
		{"429 status", errors.New("HTTP 429"), TierExternalRateLimit},
		{"500 status", errors.New("HTTP 500 error"), TierExternalDegrading},
		{"502 status", errors.New("got 502 bad gateway"), TierExternalDegrading},
		{"503 status", errors.New("service unavailable 503"), TierExternalDegrading},
		{"504 status", errors.New("gateway timeout 504"), TierExternalDegrading},
		{"timeout keyword", errors.New("operation timeout"), TierTransient},
		{"temporary keyword", errors.New("temporary failure"), TierTransient},
		{"connection reset", errors.New("connection reset by peer"), TierTransient},
		{"eof keyword", errors.New("unexpected EOF"), TierTransient},
		{"unknown error", errors.New("something went wrong"), TierPermanent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifier.Classify(tt.err); got != tt.expected {
				t.Errorf("Classify() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestErrorClassifier_AddPatterns(t *testing.T) {
	classifier := NewErrorClassifier()

	t.Run("add transient pattern", func(t *testing.T) {
		err := classifier.AddTransientPattern(`(?i)custom_transient`)
		if err != nil {
			t.Fatalf("AddTransientPattern() error = %v", err)
		}

		tier := classifier.Classify(errors.New("this is a CUSTOM_TRANSIENT error"))
		if tier != TierTransient {
			t.Errorf("Classify() = %v, want %v", tier, TierTransient)
		}
	})

	t.Run("add permanent pattern", func(t *testing.T) {
		err := classifier.AddPermanentPattern(`(?i)custom_permanent`)
		if err != nil {
			t.Fatalf("AddPermanentPattern() error = %v", err)
		}

		tier := classifier.Classify(errors.New("this is a CUSTOM_PERMANENT error"))
		if tier != TierPermanent {
			t.Errorf("Classify() = %v, want %v", tier, TierPermanent)
		}
	})

	t.Run("add user-fixable pattern", func(t *testing.T) {
		err := classifier.AddUserFixablePattern(`(?i)missing_setting`)
		if err != nil {
			t.Fatalf("AddUserFixablePattern() error = %v", err)
		}

		tier := classifier.Classify(errors.New("error: MISSING_SETTING for config"))
		if tier != TierUserFixable {
			t.Errorf("Classify() = %v, want %v", tier, TierUserFixable)
		}
	})

	t.Run("invalid pattern returns error", func(t *testing.T) {
		err := classifier.AddTransientPattern(`[invalid`)
		if err == nil {
			t.Error("AddTransientPattern() should return error for invalid regex")
		}
	})
}

func TestErrorClassifierFromConfig(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := &ErrorClassifierConfig{
			TransientPatterns:   []string{`(?i)my_transient`},
			PermanentPatterns:   []string{`(?i)my_permanent`},
			UserFixablePatterns: []string{`(?i)my_user_error`},
			RateLimitStatuses:   []int{429, 420},
			DegradingStatuses:   []int{500, 503},
		}

		classifier, err := NewErrorClassifierFromConfig(cfg)
		if err != nil {
			t.Fatalf("NewErrorClassifierFromConfig() error = %v", err)
		}

		if classifier.Classify(errors.New("MY_TRANSIENT")) != TierTransient {
			t.Error("custom transient pattern not working")
		}
		if classifier.Classify(errors.New("status 420")) != TierExternalRateLimit {
			t.Error("custom rate limit status not working")
		}
	})

	t.Run("invalid transient pattern", func(t *testing.T) {
		cfg := &ErrorClassifierConfig{
			TransientPatterns: []string{`[invalid`},
		}

		_, err := NewErrorClassifierFromConfig(cfg)
		if err == nil {
			t.Error("should return error for invalid transient pattern")
		}
	})

	t.Run("invalid permanent pattern", func(t *testing.T) {
		cfg := &ErrorClassifierConfig{
			PermanentPatterns: []string{`[invalid`},
		}

		_, err := NewErrorClassifierFromConfig(cfg)
		if err == nil {
			t.Error("should return error for invalid permanent pattern")
		}
	})

	t.Run("invalid user-fixable pattern", func(t *testing.T) {
		cfg := &ErrorClassifierConfig{
			UserFixablePatterns: []string{`[invalid`},
		}

		_, err := NewErrorClassifierFromConfig(cfg)
		if err == nil {
			t.Error("should return error for invalid user-fixable pattern")
		}
	})
}

func TestDefaultErrorClassifierConfig(t *testing.T) {
	cfg := DefaultErrorClassifierConfig()

	if len(cfg.TransientPatterns) == 0 {
		t.Error("default config should have transient patterns")
	}
	if len(cfg.PermanentPatterns) == 0 {
		t.Error("default config should have permanent patterns")
	}
	if len(cfg.UserFixablePatterns) == 0 {
		t.Error("default config should have user-fixable patterns")
	}
	if len(cfg.RateLimitStatuses) == 0 {
		t.Error("default config should have rate limit statuses")
	}
	if len(cfg.DegradingStatuses) == 0 {
		t.Error("default config should have degrading statuses")
	}
}

func TestErrorClassifier_ConcurrentAccess(t *testing.T) {
	classifier := NewErrorClassifier()
	var wg sync.WaitGroup
	iterations := 100

	wg.Add(iterations * 2)

	for i := 0; i < iterations; i++ {
		go func(i int) {
			defer wg.Done()
			pattern := fmt.Sprintf(`pattern_%d`, i)
			_ = classifier.AddTransientPattern(pattern)
		}(i)

		go func() {
			defer wg.Done()
			_ = classifier.Classify(errors.New("some error"))
		}()
	}

	wg.Wait()
}

func TestErrorClassifier_RaceCondition(t *testing.T) {
	classifier := NewErrorClassifier()
	var wg sync.WaitGroup

	writers := 10
	readers := 20
	iterations := 50

	wg.Add(writers + readers)

	for i := 0; i < writers; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = classifier.AddTransientPattern(fmt.Sprintf(`writer_%d_%d`, id, j))
			}
		}(i)
	}

	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				classifier.Classify(errors.New("test error"))
			}
		}()
	}

	wg.Wait()
}

func TestErrorClassifier_EdgeCases(t *testing.T) {
	classifier := NewErrorClassifier()

	t.Run("empty error message", func(t *testing.T) {
		tier := classifier.Classify(errors.New(""))
		if tier != TierPermanent {
			t.Errorf("empty error should be Permanent, got %v", tier)
		}
	})

	t.Run("error with only whitespace", func(t *testing.T) {
		tier := classifier.Classify(errors.New("   "))
		if tier != TierPermanent {
			t.Errorf("whitespace error should be Permanent, got %v", tier)
		}
	})

	t.Run("case insensitive matching", func(t *testing.T) {
		tests := []string{"TIMEOUT", "Timeout", "TiMeOuT", "timeout"}
		for _, msg := range tests {
			tier := classifier.Classify(errors.New(msg))
			if tier != TierTransient {
				t.Errorf("%q should be Transient, got %v", msg, tier)
			}
		}
	})

	t.Run("partial match in word", func(t *testing.T) {
		tier := classifier.Classify(errors.New("this has a timeout in it"))
		if tier != TierTransient {
			t.Errorf("message containing 'timeout' should be Transient, got %v", tier)
		}
	})

	t.Run("status code in different positions", func(t *testing.T) {
		tests := []string{
			"error 429",
			"429 error",
			"got a 429 response",
		}
		for _, msg := range tests {
			tier := classifier.Classify(errors.New(msg))
			if tier != TierExternalRateLimit {
				t.Errorf("%q should be RateLimit, got %v", msg, tier)
			}
		}
	})
}

func TestTierBehavior_Values(t *testing.T) {
	behaviors := DefaultBehaviors()

	t.Run("transient behavior", func(t *testing.T) {
		b := behaviors[TierTransient]
		if b.MaxRetries < 1 {
			t.Error("transient should allow retries")
		}
		if b.BaseBackoff <= 0 {
			t.Error("transient should have positive base backoff")
		}
	})

	t.Run("rate limit behavior", func(t *testing.T) {
		b := behaviors[TierExternalRateLimit]
		if !b.ShouldRetry {
			t.Error("rate limit should be retryable")
		}
		if !b.ShouldNotify {
			t.Error("rate limit should notify")
		}
	})

	t.Run("permanent behavior", func(t *testing.T) {
		b := behaviors[TierPermanent]
		if b.ShouldRetry {
			t.Error("permanent should not be retryable")
		}
		if !b.ShouldEscalate {
			t.Error("permanent should escalate")
		}
	})
}

func TestWrappedErrorChain(t *testing.T) {
	base := errors.New("base error")
	tier1 := WrapWithTier(TierTransient, "level 1", base)
	tier2 := WrapWithTier(TierPermanent, "level 2", tier1)

	if GetTier(tier2) != TierTransient {
		t.Error("wrapped chain should preserve original tier")
	}

	if !errors.Is(tier2, base) {
		t.Error("wrapped chain should satisfy errors.Is for base")
	}
}

func TestErrorClassifier_UninitializedPatterns(t *testing.T) {
	// Create classifier with zero value (uninitialized patterns)
	c := &ErrorClassifier{}

	// loadPatterns should return emptyPatternSet, not panic
	ps := c.loadPatterns()
	if ps == nil {
		t.Fatal("loadPatterns() should not return nil")
	}

	// Classify should work without panic
	tier := c.Classify(errors.New("some error"))
	if tier != TierPermanent {
		t.Errorf("Classify() = %v, want %v for uninitialized classifier", tier, TierPermanent)
	}
}

func TestErrorClassifier_TypeSafePatternStore(t *testing.T) {
	c := NewErrorClassifier()

	// Verify initial patterns are stored correctly
	ps := c.loadPatterns()
	if ps == nil {
		t.Fatal("loadPatterns() should not return nil after initialization")
	}
	if ps.transient == nil || ps.permanent == nil || ps.userFixable == nil {
		t.Error("pattern slices should be initialized, not nil")
	}
}

func TestErrorClassifier_ConcurrentAddAndClassify(t *testing.T) {
	c := NewErrorClassifier()
	const goroutines = 10
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	// Concurrent writers for transient patterns
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = c.AddTransientPattern(fmt.Sprintf(`transient_%d_%d`, id, j))
			}
		}(i)
	}

	// Concurrent writers for permanent patterns
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = c.AddPermanentPattern(fmt.Sprintf(`permanent_%d_%d`, id, j))
			}
		}(i)
	}

	// Concurrent readers
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				c.Classify(errors.New("transient_5_50 error"))
				c.Classify(errors.New("permanent_5_50 error"))
				c.Classify(errors.New("unknown error"))
			}
		}()
	}

	wg.Wait()
}

func TestErrorClassifier_PatternSetImmutability(t *testing.T) {
	c := NewErrorClassifier()

	// Add a pattern
	_ = c.AddTransientPattern(`test_pattern`)

	// Get the pattern set
	ps1 := c.loadPatterns()
	originalLen := len(ps1.transient)

	// Add another pattern
	_ = c.AddTransientPattern(`another_pattern`)

	// Original pattern set should be unchanged (immutable)
	if len(ps1.transient) != originalLen {
		t.Error("original pattern set should not be modified after adding new pattern")
	}

	// New pattern set should have the new pattern
	ps2 := c.loadPatterns()
	if len(ps2.transient) != originalLen+1 {
		t.Errorf("new pattern set should have %d patterns, got %d", originalLen+1, len(ps2.transient))
	}
}
