package providers

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// RetryEvent describes a single retry attempt for observer notification.
type RetryEvent struct {
	Attempt     int
	MaxAttempts int
	Err         error
	Delay       time.Duration
}

// RetryObserver is called before each retry wait so callers can surface
// status to the user (e.g. "rate limited, retrying in 39s").
type RetryObserver func(RetryEvent)

type retryObserverKey struct{}

// WithRetryObserver attaches a retry observer to the context. The observer is
// invoked by retry loops before sleeping, giving callers visibility into
// transient failures.
func WithRetryObserver(ctx context.Context, observer RetryObserver) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, retryObserverKey{}, observer)
}

// RetryObserverFromContext extracts a previously attached RetryObserver from
// the context. Returns nil when no observer is present.
func RetryObserverFromContext(ctx context.Context) RetryObserver {
	if ctx == nil {
		return nil
	}
	obs, _ := ctx.Value(retryObserverKey{}).(RetryObserver)
	return obs
}

func notifyRetryObserver(ctx context.Context, event RetryEvent) {
	if ctx == nil {
		return
	}
	obs, ok := ctx.Value(retryObserverKey{}).(RetryObserver)
	if !ok || obs == nil {
		return
	}
	obs(event)
}

// retryGenerate wraps a generate function with retry logic using the provider's config.
func retryGenerate(ctx context.Context, cfg BaseConfig, fn func(context.Context) (*Response, error)) (*Response, error) {
	maxAttempts := resolveMaxRetries(cfg.MaxRetries)
	var lastErr error
	for attempt := range maxAttempts {
		resp, err := fn(ctx)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !shouldRetryProviderCall(ctx, err, attempt, maxAttempts) {
			break
		}
		delay := retryDelay(attempt, cfg.RetryBaseDelay, cfg.RetryMaxDelay)
		notifyRetryObserver(ctx, RetryEvent{
			Attempt:     attempt + 1,
			MaxAttempts: maxAttempts,
			Err:         err,
			Delay:       delay,
		})
		if err := waitRetryDelay(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

// retryStream wraps a streaming function with retry logic using the provider's config.
func retryStream(ctx context.Context, cfg BaseConfig, fn func(context.Context) error) error {
	maxAttempts := resolveMaxRetries(cfg.MaxRetries)
	var lastErr error
	for attempt := range maxAttempts {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
		if !shouldRetryProviderCall(ctx, err, attempt, maxAttempts) {
			break
		}
		delay := retryDelay(attempt, cfg.RetryBaseDelay, cfg.RetryMaxDelay)
		notifyRetryObserver(ctx, RetryEvent{
			Attempt:     attempt + 1,
			MaxAttempts: maxAttempts,
			Err:         err,
			Delay:       delay,
		})
		if err := waitRetryDelay(ctx, delay); err != nil {
			return err
		}
	}
	return lastErr
}

// retryAwareHandler wraps a StreamHandler so that when a provider retry
// replays the stream, the replayed ChunkTypeStart chunk has RetryReset=true.
// Consumers can check this flag to discard prior partial content accumulated
// from the failed attempt.
//
// A retry is distinguished from a secondary start event (e.g. early usage)
// by whether content chunks (text, thought, tool) were seen since the last
// start. Only a start that follows content is a retry.
func retryAwareHandler(handler StreamHandler) StreamHandler {
	var hasContent bool
	return func(chunk *StreamChunk) error {
		switch chunk.Type {
		case ChunkTypeStart:
			if hasContent {
				chunk.RetryReset = true
			}
			hasContent = false
		case ChunkTypeText, ChunkTypeThought, ChunkTypeToolStart, ChunkTypeToolDelta:
			hasContent = true
		}
		return handler(chunk)
	}
}

func resolveMaxRetries(configured int) int {
	if configured <= 0 {
		return 1
	}
	return configured
}

func shouldRetryProviderCall(ctx context.Context, err error, attempt int, maxAttempts int) bool {
	if attempt+1 >= maxAttempts {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return isRetryableError(err)
}

// isRetryableError returns true for transient errors using typed error
// inspection. Cascade: ProviderError (already-classified) → anthropic.Error
// (SDK typed) → net.Error (network transient) → false.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Retryable
	}
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		return isRetryableHTTPStatus(anthropicErr.StatusCode)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

// isRetryableHTTPStatus returns true for HTTP status codes that indicate
// transient failures: rate limits (429) and server errors (5xx).
func isRetryableHTTPStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= http.StatusInternalServerError
}

// retryDelay computes an exponential backoff delay with jitter, capped at maxDelay.
func retryDelay(attempt int, baseDelay time.Duration, maxDelay time.Duration) time.Duration {
	if baseDelay <= 0 {
		baseDelay = time.Second
	}
	if maxDelay <= 0 {
		maxDelay = 30 * time.Second
	}
	delay := float64(baseDelay) * math.Pow(2, float64(attempt))
	if delay > float64(maxDelay) {
		delay = float64(maxDelay)
	}
	jitter := delay * 0.2
	delay = delay - jitter + rand.Float64()*2*jitter
	return time.Duration(delay)
}

func waitRetryDelay(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
