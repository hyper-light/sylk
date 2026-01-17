package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrSubscriptionTrackerClosed = errors.New("subscription tracker is closed")
	ErrProviderNotConfigured     = errors.New("provider not configured")
)

const (
	WarningLevelOK       = 0
	WarningLevelWarning  = 1
	WarningLevelCritical = 2

	DefaultSubscriptionDBPath = ".sylk/shared/subscription.db"

	SignalSubscriptionWarning SignalType = "subscription_warning"
)

type SubscriptionConfig struct {
	ProviderLimits map[string]*SubscriptionLimit `yaml:"provider_limits" json:"provider_limits"`
}

type SubscriptionLimit struct {
	Period        string  `yaml:"period" json:"period"`
	MaxRequests   *int64  `yaml:"max_requests" json:"max_requests"`
	MaxTokens     *int64  `yaml:"max_tokens" json:"max_tokens"`
	WarnThreshold float64 `yaml:"warn_threshold" json:"warn_threshold"`
}

type UsageStatus struct {
	Provider             string    `json:"provider"`
	RequestsUsed         int64     `json:"requests_used"`
	TokensUsed           int64     `json:"tokens_used"`
	RequestsRemaining    *int64    `json:"requests_remaining"`
	TokensRemaining      *int64    `json:"tokens_remaining"`
	WarningLevel         int       `json:"warning_level"`
	PreviousWarningLevel int       `json:"-"`
	PeriodEndsAt         time.Time `json:"period_ends_at"`
}

type GlobalSubscriptionTrackerConfig struct {
	DBPath           string
	Config           SubscriptionConfig
	SignalDispatcher *SignalDispatcher
}

type GlobalSubscriptionTracker struct {
	db               *sql.DB
	config           SubscriptionConfig
	signalDispatcher *SignalDispatcher

	mu     sync.RWMutex
	closed bool
}

func NewGlobalSubscriptionTracker(cfg GlobalSubscriptionTrackerConfig) (*GlobalSubscriptionTracker, error) {
	dbPath := normalizeSubscriptionDBPath(cfg.DBPath)

	if err := ensureDirectory(filepath.Dir(dbPath)); err != nil {
		return nil, err
	}

	db, err := openSubscriptionDB(dbPath)
	if err != nil {
		return nil, err
	}

	if err := createSubscriptionSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &GlobalSubscriptionTracker{
		db:               db,
		config:           cfg.Config,
		signalDispatcher: cfg.SignalDispatcher,
	}, nil
}

func normalizeSubscriptionDBPath(path string) string {
	if path == "" {
		return filepath.Join(os.Getenv("HOME"), DefaultSubscriptionDBPath)
	}
	return path
}

func ensureDirectory(dir string) error {
	return os.MkdirAll(dir, 0755)
}

func openSubscriptionDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open subscription db: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable WAL: %w", err)
	}

	return db, nil
}

func createSubscriptionSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS subscription_usage (
		provider TEXT NOT NULL,
		period_start DATE NOT NULL,
		period_end DATE NOT NULL,
		requests_used INTEGER DEFAULT 0,
		tokens_used INTEGER DEFAULT 0,
		PRIMARY KEY (provider, period_start)
	);
	CREATE INDEX IF NOT EXISTS idx_usage_provider ON subscription_usage(provider);
	`

	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}
	return nil
}

func (t *GlobalSubscriptionTracker) RecordUsage(ctx context.Context, provider, sessionID string, tokens int) (*UsageStatus, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil, ErrSubscriptionTrackerClosed
	}

	periodStart, periodEnd := t.getPeriodBounds(provider)

	if err := t.upsertUsage(ctx, provider, periodStart, periodEnd, tokens); err != nil {
		return nil, err
	}

	status, err := t.checkLimits(ctx, provider, periodStart, periodEnd)
	if err != nil {
		return nil, err
	}

	t.maybeBroadcastWarning(status)
	return status, nil
}

func (t *GlobalSubscriptionTracker) getPeriodBounds(provider string) (time.Time, time.Time) {
	limit := t.getProviderLimit(provider)
	if limit == nil {
		return t.defaultPeriodBounds()
	}
	return t.calculatePeriodBounds(limit.Period)
}

func (t *GlobalSubscriptionTracker) getProviderLimit(provider string) *SubscriptionLimit {
	if t.config.ProviderLimits == nil {
		return nil
	}
	return t.config.ProviderLimits[provider]
}

func (t *GlobalSubscriptionTracker) defaultPeriodBounds() (time.Time, time.Time) {
	return t.calculatePeriodBounds("month")
}

func (t *GlobalSubscriptionTracker) calculatePeriodBounds(period string) (time.Time, time.Time) {
	now := time.Now()

	switch period {
	case "day":
		return t.dayBounds(now)
	case "week":
		return t.weekBounds(now)
	default:
		return t.monthBounds(now)
	}
}

func (t *GlobalSubscriptionTracker) dayBounds(now time.Time) (time.Time, time.Time) {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	end := start.AddDate(0, 0, 1).Add(-time.Second)
	return start, end
}

func (t *GlobalSubscriptionTracker) weekBounds(now time.Time) (time.Time, time.Time) {
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	start := time.Date(now.Year(), now.Month(), now.Day()-weekday+1, 0, 0, 0, 0, now.Location())
	end := start.AddDate(0, 0, 7).Add(-time.Second)
	return start, end
}

func (t *GlobalSubscriptionTracker) monthBounds(now time.Time) (time.Time, time.Time) {
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	end := start.AddDate(0, 1, 0).Add(-time.Second)
	return start, end
}

func (t *GlobalSubscriptionTracker) upsertUsage(ctx context.Context, provider string, periodStart, periodEnd time.Time, tokens int) error {
	_, err := t.db.ExecContext(ctx, `
		INSERT INTO subscription_usage (provider, period_start, period_end, requests_used, tokens_used)
		VALUES (?, ?, ?, 1, ?)
		ON CONFLICT (provider, period_start) DO UPDATE SET
			requests_used = requests_used + 1,
			tokens_used = tokens_used + ?
	`, provider, periodStart, periodEnd, tokens, tokens)

	if err != nil {
		return fmt.Errorf("failed to record usage: %w", err)
	}
	return nil
}

func (t *GlobalSubscriptionTracker) checkLimits(ctx context.Context, provider string, periodStart, periodEnd time.Time) (*UsageStatus, error) {
	usage, err := t.queryUsage(ctx, provider, periodStart)
	if err != nil {
		return nil, err
	}

	return t.buildUsageStatus(provider, usage, periodEnd), nil
}

func (t *GlobalSubscriptionTracker) queryUsage(ctx context.Context, provider string, periodStart time.Time) (UsageStatus, error) {
	var usage UsageStatus
	usage.Provider = provider

	row := t.db.QueryRowContext(ctx, `
		SELECT requests_used, tokens_used
		FROM subscription_usage
		WHERE provider = ? AND period_start = ?
	`, provider, periodStart)

	err := row.Scan(&usage.RequestsUsed, &usage.TokensUsed)
	if err == sql.ErrNoRows {
		return usage, nil
	}
	if err != nil {
		return usage, fmt.Errorf("failed to query usage: %w", err)
	}
	return usage, nil
}

func (t *GlobalSubscriptionTracker) buildUsageStatus(provider string, usage UsageStatus, periodEnd time.Time) *UsageStatus {
	limit := t.getProviderLimit(provider)
	usage.PeriodEndsAt = periodEnd

	if limit == nil {
		return &usage
	}

	t.calculateRemaining(&usage, limit)
	usage.WarningLevel = t.calculateWarningLevel(&usage, limit)

	return &usage
}

func (t *GlobalSubscriptionTracker) calculateRemaining(usage *UsageStatus, limit *SubscriptionLimit) {
	if limit.MaxRequests != nil {
		remaining := *limit.MaxRequests - usage.RequestsUsed
		usage.RequestsRemaining = &remaining
	}
	if limit.MaxTokens != nil {
		remaining := *limit.MaxTokens - usage.TokensUsed
		usage.TokensRemaining = &remaining
	}
}

func (t *GlobalSubscriptionTracker) calculateWarningLevel(usage *UsageStatus, limit *SubscriptionLimit) int {
	threshold := limit.WarnThreshold
	if threshold <= 0 {
		threshold = 0.8
	}

	if t.isOverLimit(usage, limit) {
		return WarningLevelCritical
	}
	if t.isOverThreshold(usage, limit, threshold) {
		return WarningLevelWarning
	}
	return WarningLevelOK
}

func (t *GlobalSubscriptionTracker) isOverLimit(usage *UsageStatus, limit *SubscriptionLimit) bool {
	return t.isRequestsOverLimit(usage, limit) || t.isTokensOverLimit(usage, limit)
}

func (t *GlobalSubscriptionTracker) isRequestsOverLimit(usage *UsageStatus, limit *SubscriptionLimit) bool {
	return limit.MaxRequests != nil && usage.RequestsUsed >= *limit.MaxRequests
}

func (t *GlobalSubscriptionTracker) isTokensOverLimit(usage *UsageStatus, limit *SubscriptionLimit) bool {
	return limit.MaxTokens != nil && usage.TokensUsed >= *limit.MaxTokens
}

func (t *GlobalSubscriptionTracker) isOverThreshold(usage *UsageStatus, limit *SubscriptionLimit, threshold float64) bool {
	return t.isRequestsOverThreshold(usage, limit, threshold) || t.isTokensOverThreshold(usage, limit, threshold)
}

func (t *GlobalSubscriptionTracker) isRequestsOverThreshold(usage *UsageStatus, limit *SubscriptionLimit, threshold float64) bool {
	if limit.MaxRequests == nil {
		return false
	}
	return float64(usage.RequestsUsed) >= float64(*limit.MaxRequests)*threshold
}

func (t *GlobalSubscriptionTracker) isTokensOverThreshold(usage *UsageStatus, limit *SubscriptionLimit, threshold float64) bool {
	if limit.MaxTokens == nil {
		return false
	}
	return float64(usage.TokensUsed) >= float64(*limit.MaxTokens)*threshold
}

func (t *GlobalSubscriptionTracker) maybeBroadcastWarning(status *UsageStatus) {
	if status.WarningLevel <= status.PreviousWarningLevel {
		return
	}
	if t.signalDispatcher == nil {
		return
	}

	payload, _ := json.Marshal(status)
	t.signalDispatcher.SendSignal(CrossSessionSignal{
		Type:      SignalSubscriptionWarning,
		Timestamp: time.Now(),
		Payload:   string(payload),
	})
}

func (t *GlobalSubscriptionTracker) GetUsage(ctx context.Context, provider string) (*UsageStatus, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.closed {
		return nil, ErrSubscriptionTrackerClosed
	}

	periodStart, periodEnd := t.getPeriodBounds(provider)
	return t.checkLimits(ctx, provider, periodStart, periodEnd)
}

func (t *GlobalSubscriptionTracker) ResetUsage(ctx context.Context, provider string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return ErrSubscriptionTrackerClosed
	}

	periodStart, _ := t.getPeriodBounds(provider)

	_, err := t.db.ExecContext(ctx, `
		DELETE FROM subscription_usage
		WHERE provider = ? AND period_start = ?
	`, provider, periodStart)

	if err != nil {
		return fmt.Errorf("failed to reset usage: %w", err)
	}
	return nil
}

func (t *GlobalSubscriptionTracker) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return ErrSubscriptionTrackerClosed
	}
	t.closed = true

	if t.db != nil {
		return t.db.Close()
	}
	return nil
}
