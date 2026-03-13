package deadlinelease

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DefaultDeadlineGuard is the minimum remaining parent budget needed to
// justify one more renewed child attempt.
const DefaultDeadlineGuard = 10 * time.Second

// Config controls how a bounded child context is refreshed while a longer-
// lived parent operation remains active.
type Config struct {
	AttemptTimeout     time.Duration
	MaxRefreshes       int
	DeadlineGuard      time.Duration
	AttemptContext     func(context.Context, time.Duration) (context.Context, context.CancelFunc)
	IsRefreshableError func(error, error) bool
	OnRefresh          func(Refresh)
}

// Refresh records a successful deadline renewal.
type Refresh struct {
	RefreshCount   int
	AttemptTimeout time.Duration
	Error          error
}

// TimeoutError reports that a renewable child deadline expired until the
// refresh budget was exhausted.
type TimeoutError struct {
	AttemptTimeout time.Duration
	Refreshes      int
	Elapsed        time.Duration
	Err            error
}

func (e *TimeoutError) Error() string {
	if e == nil {
		return "context lease exhausted"
	}
	elapsed := e.Elapsed
	if elapsed <= 0 {
		elapsed = e.AttemptTimeout
	}
	return fmt.Sprintf(
		"context lease exhausted after %d attempt(s) over %s: %v",
		e.Refreshes+1,
		elapsed.Truncate(time.Millisecond),
		e.Err,
	)
}

func (e *TimeoutError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Run reruns a child-scoped operation when only the child deadline expires
// and the parent context still has enough remaining budget.
func Run(parent context.Context, cfg Config, run func(context.Context) error) error {
	if run == nil {
		return nil
	}
	parent = nonNilContext(parent)
	cfg = normalizeConfig(cfg)
	refreshes := 0
	start := time.Now()

	for {
		attemptCtx, cancel := cfg.AttemptContext(parent, cfg.AttemptTimeout)
		err := run(attemptCtx)
		attemptErr := attemptCtx.Err()
		cancel()

		if err == nil {
			return nil
		}
		if !shouldRefresh(parent, cfg, refreshes, attemptErr, err) {
			if isTimeoutExhausted(parent, cfg, attemptErr, err) {
				return &TimeoutError{
					AttemptTimeout: cfg.AttemptTimeout,
					Refreshes:      refreshes,
					Elapsed:        time.Since(start),
					Err:            err,
				}
			}
			return err
		}

		refreshes++
		if cfg.OnRefresh != nil {
			cfg.OnRefresh(Refresh{
				RefreshCount:   refreshes,
				AttemptTimeout: cfg.AttemptTimeout,
				Error:          err,
			})
		}
	}
}

// WrapTimeoutError formats a user-facing timeout message while preserving the
// underlying context or lease error for callers/tests.
func WrapTimeoutError(subject string, fallback time.Duration, err error) error {
	if err == nil {
		return nil
	}
	var leaseErr *TimeoutError
	if errors.As(err, &leaseErr) {
		return fmt.Errorf("%s timed out after %s: %w", subject, leaseErr.Elapsed.Truncate(time.Millisecond), err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s timed out after %s: %w", subject, fallback, err)
	}
	return err
}

// IsRefreshableDeadline reports whether the child attempt ended because its
// context deadline expired.
func IsRefreshableDeadline(attemptErr error, err error) bool {
	if errors.Is(attemptErr, context.DeadlineExceeded) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// HasBudget reports whether the parent context still has enough remaining
// budget to justify another renewed child attempt.
func HasBudget(ctx context.Context, guard time.Duration) bool {
	if ctx == nil {
		return false
	}
	if guard <= 0 {
		guard = DefaultDeadlineGuard
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return true
	}
	return time.Until(deadline) >= guard
}

func normalizeConfig(cfg Config) Config {
	if cfg.MaxRefreshes < 0 {
		cfg.MaxRefreshes = 0
	}
	if cfg.DeadlineGuard <= 0 {
		cfg.DeadlineGuard = DefaultDeadlineGuard
	}
	if cfg.AttemptContext == nil {
		cfg.AttemptContext = func(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
			if timeout > 0 {
				return context.WithTimeout(parent, timeout)
			}
			return context.WithCancel(parent)
		}
	}
	if cfg.IsRefreshableError == nil {
		cfg.IsRefreshableError = IsRefreshableDeadline
	}
	return cfg
}

func shouldRefresh(
	parent context.Context,
	cfg Config,
	refreshes int,
	attemptErr error,
	err error,
) bool {
	if err == nil || parent.Err() != nil || refreshes >= cfg.MaxRefreshes {
		return false
	}
	if !cfg.IsRefreshableError(attemptErr, err) {
		return false
	}
	return HasBudget(parent, cfg.DeadlineGuard)
}

func isTimeoutExhausted(
	parent context.Context,
	cfg Config,
	attemptErr error,
	err error,
) bool {
	if parent.Err() != nil {
		return false
	}
	if !cfg.IsRefreshableError(attemptErr, err) {
		return false
	}
	return true
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}
