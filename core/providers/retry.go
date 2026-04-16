package providers

import (
	"context"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
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

// RetryExhaustedEvent describes a provider call that failed after consuming
// its retry budget.
type RetryExhaustedEvent struct {
	RetriesUsed   int
	TotalAttempts int
	Err           error
	Transport     bool
}

// RetryObserver is called before each retry wait so callers can surface
// status to the user (e.g. "rate limited, retrying in 39s").
type RetryObserver func(RetryEvent)

// RetryExhaustedObserver is called once when a provider call still fails after
// exhausting its retries. It is intended for higher-level recovery actions
// such as agent handoff.
type RetryExhaustedObserver func(RetryExhaustedEvent)

type retryObserverKey struct{}
type retryExhaustedObserverKey struct{}

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

// WithRetryExhaustedObserver attaches a callback invoked when a provider call
// fails after consuming its retry budget.
func WithRetryExhaustedObserver(ctx context.Context, observer RetryExhaustedObserver) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, retryExhaustedObserverKey{}, observer)
}

// RetryExhaustedObserverFromContext extracts a previously attached retry
// exhausted observer from the context. Returns nil when no observer is
// present.
func RetryExhaustedObserverFromContext(ctx context.Context) RetryExhaustedObserver {
	if ctx == nil {
		return nil
	}
	obs, _ := ctx.Value(retryExhaustedObserverKey{}).(RetryExhaustedObserver)
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

func notifyRetryExhaustedObserver(ctx context.Context, event RetryExhaustedEvent) {
	if ctx == nil {
		return
	}
	obs, ok := ctx.Value(retryExhaustedObserverKey{}).(RetryExhaustedObserver)
	if !ok || obs == nil {
		return
	}
	obs(event)
}

func notifyRetryExhaustedTransport(ctx context.Context, retriesUsed, totalAttempts int, err error) {
	if retriesUsed <= 0 || totalAttempts <= 0 || !isRetryableTransportError(err) {
		return
	}
	notifyRetryExhaustedObserver(ctx, RetryExhaustedEvent{
		RetriesUsed:   retriesUsed,
		TotalAttempts: totalAttempts,
		Err:           err,
		Transport:     true,
	})
}

// retryGenerate wraps a generate function with retry logic using the provider's config.
func retryGenerate(ctx context.Context, cfg BaseConfig, fn func(context.Context) (*Response, error)) (*Response, error) {
	maxAttempts := resolveMaxRetries(cfg.MaxRetries)
	var lastErr error
	retriesUsed := 0
	totalAttempts := 0
	for attempt := range maxAttempts {
		totalAttempts = attempt + 1
		resp, err := fn(ctx)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !shouldRetryProviderCall(ctx, err, attempt, maxAttempts) {
			break
		}
		retriesUsed++
		delay := serverGuidedDelay(err, attempt, cfg.RetryBaseDelay, cfg.RetryMaxDelay)
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
	notifyRetryExhaustedTransport(ctx, retriesUsed, totalAttempts, lastErr)
	return nil, lastErr
}

// retryStream wraps a streaming function with retry logic using the provider's config.
func retryStream(ctx context.Context, cfg BaseConfig, fn func(context.Context) error) error {
	maxAttempts := resolveMaxRetries(cfg.MaxRetries)
	var lastErr error
	retriesUsed := 0
	totalAttempts := 0
	for attempt := range maxAttempts {
		totalAttempts = attempt + 1
		err := fn(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
		if !shouldRetryProviderCall(ctx, err, attempt, maxAttempts) {
			break
		}
		retriesUsed++
		delay := serverGuidedDelay(err, attempt, cfg.RetryBaseDelay, cfg.RetryMaxDelay)
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
	notifyRetryExhaustedTransport(ctx, retriesUsed, totalAttempts, lastErr)
	return lastErr
}

const anthropicTransientMaxRetries = 3

// retryAnthropicTransientGenerate retries Anthropic generate calls for
// provider-declared transient failures and transient transport failures like
// unexpected EOF. MaxRetries here is a retry budget, not a total-attempt
// budget: 3 means the initial request plus up to 3 retries.
func retryAnthropicTransientGenerate(ctx context.Context, cfg BaseConfig, fn func(context.Context) (*Response, error)) (*Response, error) {
	var lastErr error
	retriesUsed := 0
	totalAttempts := 0
	for retry := 0; ; retry++ {
		totalAttempts = retry + 1
		resp, err := fn(ctx)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !shouldRetryAnthropicTransientCall(ctx, err, retry) {
			break
		}
		retriesUsed++
		delay := serverGuidedDelay(err, retry, cfg.RetryBaseDelay, cfg.RetryMaxDelay)
		notifyRetryObserver(ctx, RetryEvent{
			Attempt:     retry + 1,
			MaxAttempts: anthropicTransientMaxRetries,
			Err:         err,
			Delay:       delay,
		})
		if err := waitRetryDelay(ctx, delay); err != nil {
			return nil, err
		}
	}
	notifyRetryExhaustedTransport(ctx, retriesUsed, totalAttempts, lastErr)
	return nil, lastErr
}

// retryAnthropicTransientStream retries Anthropic stream calls for
// provider-declared transient failures and transient transport failures like
// unexpected EOF. MaxRetries here is a retry budget, not a total-attempt
// budget: 3 means the initial request plus up to 3 retries.
func retryAnthropicTransientStream(ctx context.Context, cfg BaseConfig, fn func(context.Context) error) error {
	var lastErr error
	retriesUsed := 0
	totalAttempts := 0
	for retry := 0; ; retry++ {
		totalAttempts = retry + 1
		err := fn(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
		if !shouldRetryAnthropicTransientCall(ctx, err, retry) {
			break
		}
		retriesUsed++
		delay := serverGuidedDelay(err, retry, cfg.RetryBaseDelay, cfg.RetryMaxDelay)
		notifyRetryObserver(ctx, RetryEvent{
			Attempt:     retry + 1,
			MaxAttempts: anthropicTransientMaxRetries,
			Err:         err,
			Delay:       delay,
		})
		if err := waitRetryDelay(ctx, delay); err != nil {
			return err
		}
	}
	notifyRetryExhaustedTransport(ctx, retriesUsed, totalAttempts, lastErr)
	return lastErr
}

// serverGuidedDelay returns the retry delay for an attempt, honoring the
// server's Retry-After when available. The delay is max(exponential backoff,
// server hint), ensuring we never undercut the server's guidance. This makes
// all providers (Anthropic, Google, OpenAI) benefit from server-guided
// backoff through the generic retry path.
func serverGuidedDelay(err error, attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	computed := retryDelay(attempt, baseDelay, maxDelay)
	serverHint := GetRetryAfter(err)
	if serverHint > computed {
		return serverHint
	}
	return computed
}

// retryAwareHandler wraps a StreamHandler so that when a provider retry
// replays the stream, the replayed ChunkTypeStart chunk has RetryReset=true.
// Consumers can check this flag to discard prior partial content and prior
// terminal error state accumulated from the failed attempt.
//
// A retry is distinguished from a secondary start event (e.g. early usage)
// by whether the prior attempt emitted any content or terminal error since
// the last start. That lets callers recover cleanly even when a provider
// fails before emitting user-visible content.
func retryAwareHandler(handler StreamHandler) StreamHandler {
	var sawAttemptOutput bool
	return func(chunk *StreamChunk) error {
		switch chunk.Type {
		case ChunkTypeStart:
			if sawAttemptOutput {
				chunk.RetryReset = true
			}
			sawAttemptOutput = false
		case ChunkTypeText, ChunkTypeThought, ChunkTypeToolStart, ChunkTypeToolDelta, ChunkTypeToolEnd, ChunkTypeError:
			sawAttemptOutput = true
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

func shouldRetryAnthropicTransientCall(ctx context.Context, err error, retry int) bool {
	if retry >= anthropicTransientMaxRetries {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return isAnthropicTransientError(err) || isRetryableTransportError(err)
}

// isRetryableError returns true for transient errors using typed error
// inspection. Cascade: ProviderError (already-classified) → anthropic.Error
// (SDK typed) → timeout net.Error → transport classifier → false.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Retryable || (pe.Provider == ProviderTypeAnthropic && isAnthropicOverloadedError(pe))
	}
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		return isRetryableHTTPStatus(anthropicErr.StatusCode) || isAnthropicOverloadedError(anthropicErr)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
	}
	return isRetryableTransportError(err)
}

func isRetryableTransportError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if isRetryableNetOpError(err) {
		return true
	}
	msg := strings.ToLower(safeErrorString(err))
	switch {
	case strings.Contains(msg, "unexpected eof"):
		return true
	case strings.Contains(msg, "stream error: stream id") && strings.Contains(msg, "received from peer"):
		return true
	case strings.Contains(msg, "http2: client connection lost"):
		return true
	case strings.Contains(msg, "server sent goaway and closed the connection"):
		return true
	case strings.Contains(msg, "connection reset by peer"):
		return true
	default:
		return false
	}
}

// IsRetryableTransport reports whether err is a transient transport failure,
// such as a socket reset or unexpected EOF, that is safe to replay.
func IsRetryableTransport(err error) bool {
	return isRetryableTransportError(err)
}

// isRetryableNetOpError treats connection-level network operation failures as
// retryable. Provider calls are safe to replay on transport failure, and
// net.OpError is the stable typed wrapper Go uses for dial/read/write errors
// across SDKs, which is more reliable than enumerating message fragments.
func isRetryableNetOpError(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

func safeErrorString(err error) (msg string) {
	defer func() {
		if recover() != nil {
			msg = ""
		}
	}()
	if err == nil {
		return ""
	}
	return err.Error()
}

func isAnthropicTransientError(err error) bool {
	if err == nil {
		return false
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.StatusCode == http.StatusInternalServerError || isAnthropicOverloadedError(pe)
	}
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		return anthropicErr.StatusCode == http.StatusInternalServerError || isAnthropicOverloadedError(anthropicErr)
	}
	return isAnthropicOverloadedError(err)
}

type anthropicRawJSONCarrier interface {
	RawJSON() string
}

func isAnthropicOverloadedError(err error) bool {
	return anthropicErrorDetail(err).ErrorType == "overloaded_error"
}

func anthropicErrorDetail(err error) jsonErrorDetail {
	if err == nil {
		return jsonErrorDetail{}
	}
	var rawCarrier anthropicRawJSONCarrier
	if errors.As(err, &rawCarrier) {
		if detail := extractJSONErrorDetail(rawCarrier.RawJSON()); detail.Message != "" || detail.ErrorType != "" || detail.RequestID != "" {
			return detail
		}
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		if detail := extractJSONErrorDetail(pe.Message); detail.Message != "" || detail.ErrorType != "" || detail.RequestID != "" {
			return detail
		}
	}
	return extractJSONErrorDetail(safeErrorString(err))
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
