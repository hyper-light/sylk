package session

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNewGlobalSubscriptionTracker(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_subscription.db")

	tracker, err := NewGlobalSubscriptionTracker(GlobalSubscriptionTrackerConfig{
		DBPath: dbPath,
	})
	if err != nil {
		t.Fatalf("failed to create tracker: %v", err)
	}
	defer tracker.Close()

	if tracker.db == nil {
		t.Error("expected non-nil db")
	}
}

func TestNewGlobalSubscriptionTracker_DefaultPath(t *testing.T) {
	home := os.Getenv("HOME")
	expectedDir := filepath.Join(home, ".sylk", "shared")

	os.RemoveAll(expectedDir)
	defer os.RemoveAll(expectedDir)

	tracker, err := NewGlobalSubscriptionTracker(GlobalSubscriptionTrackerConfig{})
	if err != nil {
		t.Fatalf("failed to create tracker: %v", err)
	}
	defer tracker.Close()

	if _, err := os.Stat(expectedDir); os.IsNotExist(err) {
		t.Error("expected default directory to be created")
	}
}

func TestGlobalSubscriptionTracker_RecordUsage(t *testing.T) {
	tracker := newTestTracker(t)
	defer tracker.Close()

	ctx := context.Background()

	status, err := tracker.RecordUsage(ctx, "anthropic", "session-1", 100)
	if err != nil {
		t.Fatalf("RecordUsage failed: %v", err)
	}

	if status.Provider != "anthropic" {
		t.Errorf("expected provider anthropic, got %s", status.Provider)
	}
	if status.RequestsUsed != 1 {
		t.Errorf("expected 1 request, got %d", status.RequestsUsed)
	}
	if status.TokensUsed != 100 {
		t.Errorf("expected 100 tokens, got %d", status.TokensUsed)
	}
}

func TestGlobalSubscriptionTracker_RecordUsageAccumulates(t *testing.T) {
	tracker := newTestTracker(t)
	defer tracker.Close()

	ctx := context.Background()

	tracker.RecordUsage(ctx, "anthropic", "session-1", 100)
	tracker.RecordUsage(ctx, "anthropic", "session-2", 200)
	status, _ := tracker.RecordUsage(ctx, "anthropic", "session-1", 150)

	if status.RequestsUsed != 3 {
		t.Errorf("expected 3 requests, got %d", status.RequestsUsed)
	}
	if status.TokensUsed != 450 {
		t.Errorf("expected 450 tokens, got %d", status.TokensUsed)
	}
}

func TestGlobalSubscriptionTracker_WarningLevels(t *testing.T) {
	maxReqs := int64(10)
	maxTokens := int64(1000)

	tracker := newTestTrackerWithConfig(t, SubscriptionConfig{
		ProviderLimits: map[string]*SubscriptionLimit{
			"anthropic": {
				Period:        "day",
				MaxRequests:   &maxReqs,
				MaxTokens:     &maxTokens,
				WarnThreshold: 0.8,
			},
		},
	})
	defer tracker.Close()

	ctx := context.Background()

	status, _ := tracker.RecordUsage(ctx, "anthropic", "s1", 100)
	if status.WarningLevel != WarningLevelOK {
		t.Errorf("expected OK level, got %d", status.WarningLevel)
	}

	for i := 0; i < 7; i++ {
		status, _ = tracker.RecordUsage(ctx, "anthropic", "s1", 100)
	}
	if status.WarningLevel != WarningLevelWarning {
		t.Errorf("expected WARNING level at 80%%, got %d", status.WarningLevel)
	}

	for i := 0; i < 2; i++ {
		status, _ = tracker.RecordUsage(ctx, "anthropic", "s1", 100)
	}
	if status.WarningLevel != WarningLevelCritical {
		t.Errorf("expected CRITICAL level at limit, got %d", status.WarningLevel)
	}
}

func TestGlobalSubscriptionTracker_RemainingCalculation(t *testing.T) {
	maxReqs := int64(100)
	maxTokens := int64(10000)

	tracker := newTestTrackerWithConfig(t, SubscriptionConfig{
		ProviderLimits: map[string]*SubscriptionLimit{
			"openai": {
				Period:        "month",
				MaxRequests:   &maxReqs,
				MaxTokens:     &maxTokens,
				WarnThreshold: 0.8,
			},
		},
	})
	defer tracker.Close()

	ctx := context.Background()

	status, _ := tracker.RecordUsage(ctx, "openai", "s1", 500)

	if status.RequestsRemaining == nil {
		t.Fatal("expected non-nil RequestsRemaining")
	}
	if *status.RequestsRemaining != 99 {
		t.Errorf("expected 99 remaining requests, got %d", *status.RequestsRemaining)
	}

	if status.TokensRemaining == nil {
		t.Fatal("expected non-nil TokensRemaining")
	}
	if *status.TokensRemaining != 9500 {
		t.Errorf("expected 9500 remaining tokens, got %d", *status.TokensRemaining)
	}
}

func TestGlobalSubscriptionTracker_UnlimitedProvider(t *testing.T) {
	tracker := newTestTrackerWithConfig(t, SubscriptionConfig{
		ProviderLimits: map[string]*SubscriptionLimit{
			"anthropic": {
				Period: "month",
			},
		},
	})
	defer tracker.Close()

	ctx := context.Background()

	for i := 0; i < 100; i++ {
		status, _ := tracker.RecordUsage(ctx, "anthropic", "s1", 1000)
		if status.WarningLevel != WarningLevelOK {
			t.Errorf("expected OK level for unlimited, got %d", status.WarningLevel)
		}
		if status.RequestsRemaining != nil {
			t.Error("expected nil RequestsRemaining for unlimited")
		}
	}
}

func TestGlobalSubscriptionTracker_GetUsage(t *testing.T) {
	tracker := newTestTracker(t)
	defer tracker.Close()

	ctx := context.Background()

	tracker.RecordUsage(ctx, "anthropic", "s1", 100)
	tracker.RecordUsage(ctx, "anthropic", "s2", 200)

	status, err := tracker.GetUsage(ctx, "anthropic")
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}

	if status.RequestsUsed != 2 {
		t.Errorf("expected 2 requests, got %d", status.RequestsUsed)
	}
	if status.TokensUsed != 300 {
		t.Errorf("expected 300 tokens, got %d", status.TokensUsed)
	}
}

func TestGlobalSubscriptionTracker_ResetUsage(t *testing.T) {
	tracker := newTestTracker(t)
	defer tracker.Close()

	ctx := context.Background()

	tracker.RecordUsage(ctx, "anthropic", "s1", 100)
	tracker.RecordUsage(ctx, "anthropic", "s2", 200)

	err := tracker.ResetUsage(ctx, "anthropic")
	if err != nil {
		t.Fatalf("ResetUsage failed: %v", err)
	}

	status, _ := tracker.GetUsage(ctx, "anthropic")
	if status.RequestsUsed != 0 {
		t.Errorf("expected 0 requests after reset, got %d", status.RequestsUsed)
	}
}

func TestGlobalSubscriptionTracker_MultipleProviders(t *testing.T) {
	tracker := newTestTracker(t)
	defer tracker.Close()

	ctx := context.Background()

	tracker.RecordUsage(ctx, "anthropic", "s1", 100)
	tracker.RecordUsage(ctx, "openai", "s1", 200)
	tracker.RecordUsage(ctx, "anthropic", "s2", 150)

	anthropicStatus, _ := tracker.GetUsage(ctx, "anthropic")
	if anthropicStatus.TokensUsed != 250 {
		t.Errorf("expected 250 tokens for anthropic, got %d", anthropicStatus.TokensUsed)
	}

	openaiStatus, _ := tracker.GetUsage(ctx, "openai")
	if openaiStatus.TokensUsed != 200 {
		t.Errorf("expected 200 tokens for openai, got %d", openaiStatus.TokensUsed)
	}
}

func TestGlobalSubscriptionTracker_PeriodBoundsDay(t *testing.T) {
	tracker := newTestTrackerWithConfig(t, SubscriptionConfig{
		ProviderLimits: map[string]*SubscriptionLimit{
			"test": {Period: "day"},
		},
	})
	defer tracker.Close()

	start, end := tracker.getPeriodBounds("test")
	now := time.Now()

	expectedStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if !start.Equal(expectedStart) {
		t.Errorf("expected start %v, got %v", expectedStart, start)
	}

	expectedEnd := expectedStart.AddDate(0, 0, 1).Add(-time.Second)
	if !end.Equal(expectedEnd) {
		t.Errorf("expected end %v, got %v", expectedEnd, end)
	}
}

func TestGlobalSubscriptionTracker_PeriodBoundsWeek(t *testing.T) {
	tracker := newTestTrackerWithConfig(t, SubscriptionConfig{
		ProviderLimits: map[string]*SubscriptionLimit{
			"test": {Period: "week"},
		},
	})
	defer tracker.Close()

	start, end := tracker.getPeriodBounds("test")

	if start.Weekday() != time.Monday {
		t.Errorf("expected start on Monday, got %v", start.Weekday())
	}

	weekDuration := end.Sub(start)
	if weekDuration < 6*24*time.Hour || weekDuration > 7*24*time.Hour {
		t.Errorf("expected ~7 day duration, got %v", weekDuration)
	}
}

func TestGlobalSubscriptionTracker_PeriodBoundsMonth(t *testing.T) {
	tracker := newTestTrackerWithConfig(t, SubscriptionConfig{
		ProviderLimits: map[string]*SubscriptionLimit{
			"test": {Period: "month"},
		},
	})
	defer tracker.Close()

	start, end := tracker.getPeriodBounds("test")
	now := time.Now()

	if start.Day() != 1 {
		t.Errorf("expected start on 1st, got %d", start.Day())
	}

	if end.Month() != now.Month() {
		t.Errorf("expected end in same month, got %v", end.Month())
	}
}

func TestGlobalSubscriptionTracker_ClosedTracker(t *testing.T) {
	tracker := newTestTracker(t)
	tracker.Close()

	ctx := context.Background()

	_, err := tracker.RecordUsage(ctx, "anthropic", "s1", 100)
	if err != ErrSubscriptionTrackerClosed {
		t.Errorf("expected ErrSubscriptionTrackerClosed, got %v", err)
	}

	_, err = tracker.GetUsage(ctx, "anthropic")
	if err != ErrSubscriptionTrackerClosed {
		t.Errorf("expected ErrSubscriptionTrackerClosed, got %v", err)
	}

	err = tracker.ResetUsage(ctx, "anthropic")
	if err != ErrSubscriptionTrackerClosed {
		t.Errorf("expected ErrSubscriptionTrackerClosed, got %v", err)
	}

	err = tracker.Close()
	if err != ErrSubscriptionTrackerClosed {
		t.Errorf("expected ErrSubscriptionTrackerClosed on double close, got %v", err)
	}
}

func TestGlobalSubscriptionTracker_ConcurrentUsage(t *testing.T) {
	tracker := newTestTracker(t)
	defer tracker.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			tracker.RecordUsage(ctx, "anthropic", "session", 10)
		}(i)
	}

	wg.Wait()

	status, err := tracker.GetUsage(ctx, "anthropic")
	if err != nil {
		t.Fatalf("GetUsage failed: %v", err)
	}

	if status.RequestsUsed != 100 {
		t.Errorf("expected 100 requests, got %d", status.RequestsUsed)
	}
	if status.TokensUsed != 1000 {
		t.Errorf("expected 1000 tokens, got %d", status.TokensUsed)
	}
}

func TestGlobalSubscriptionTracker_ConcurrentMultipleProviders(t *testing.T) {
	tracker := newTestTracker(t)
	defer tracker.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	providers := []string{"anthropic", "openai", "google"}

	for _, provider := range providers {
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(p string) {
				defer wg.Done()
				tracker.RecordUsage(ctx, p, "session", 10)
			}(provider)
		}
	}

	wg.Wait()

	for _, provider := range providers {
		status, err := tracker.GetUsage(ctx, provider)
		if err != nil {
			t.Fatalf("GetUsage for %s failed: %v", provider, err)
		}
		if status.RequestsUsed != 50 {
			t.Errorf("expected 50 requests for %s, got %d", provider, status.RequestsUsed)
		}
	}
}

func TestGlobalSubscriptionTracker_WarningBroadcast(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subscription.db")

	dispatcher, err := NewSignalDispatcher(SignalDispatcherConfig{
		BaseDir:   tmpDir,
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("failed to create dispatcher: %v", err)
	}
	defer dispatcher.Close()

	maxReqs := int64(10)
	tracker, err := NewGlobalSubscriptionTracker(GlobalSubscriptionTrackerConfig{
		DBPath: dbPath,
		Config: SubscriptionConfig{
			ProviderLimits: map[string]*SubscriptionLimit{
				"anthropic": {
					Period:        "day",
					MaxRequests:   &maxReqs,
					WarnThreshold: 0.5,
				},
			},
		},
		SignalDispatcher: dispatcher,
	})
	if err != nil {
		t.Fatalf("failed to create tracker: %v", err)
	}
	defer tracker.Close()

	ctx := context.Background()

	for i := 0; i < 6; i++ {
		tracker.RecordUsage(ctx, "anthropic", "s1", 10)
	}
}

func TestGlobalSubscriptionTracker_ZeroThreshold(t *testing.T) {
	maxReqs := int64(10)

	tracker := newTestTrackerWithConfig(t, SubscriptionConfig{
		ProviderLimits: map[string]*SubscriptionLimit{
			"anthropic": {
				Period:        "day",
				MaxRequests:   &maxReqs,
				WarnThreshold: 0,
			},
		},
	})
	defer tracker.Close()

	ctx := context.Background()

	for i := 0; i < 8; i++ {
		status, _ := tracker.RecordUsage(ctx, "anthropic", "s1", 10)
		if i < 7 && status.WarningLevel != WarningLevelOK {
			t.Errorf("expected OK at %d requests with default threshold", i+1)
		}
	}
}

func TestGlobalSubscriptionTracker_TokensOnlyLimit(t *testing.T) {
	maxTokens := int64(1000)

	tracker := newTestTrackerWithConfig(t, SubscriptionConfig{
		ProviderLimits: map[string]*SubscriptionLimit{
			"anthropic": {
				Period:        "day",
				MaxTokens:     &maxTokens,
				WarnThreshold: 0.8,
			},
		},
	})
	defer tracker.Close()

	ctx := context.Background()

	status, _ := tracker.RecordUsage(ctx, "anthropic", "s1", 900)

	if status.WarningLevel != WarningLevelWarning {
		t.Errorf("expected WARNING at 90%% tokens, got %d", status.WarningLevel)
	}

	if status.RequestsRemaining != nil {
		t.Error("expected nil RequestsRemaining when only tokens limited")
	}
}

func newTestTracker(t *testing.T) *GlobalSubscriptionTracker {
	return newTestTrackerWithConfig(t, SubscriptionConfig{})
}

func newTestTrackerWithConfig(t *testing.T, config SubscriptionConfig) *GlobalSubscriptionTracker {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_subscription.db")

	tracker, err := NewGlobalSubscriptionTracker(GlobalSubscriptionTrackerConfig{
		DBPath: dbPath,
		Config: config,
	})
	if err != nil {
		t.Fatalf("failed to create tracker: %v", err)
	}

	return tracker
}
