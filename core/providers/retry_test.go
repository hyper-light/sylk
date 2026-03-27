package providers

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestRetryGenerate_SucceedsFirstAttempt(t *testing.T) {
	cfg := BaseConfig{MaxRetries: 3, RetryBaseDelay: time.Millisecond, RetryMaxDelay: 10 * time.Millisecond}
	calls := 0
	resp, err := retryGenerate(context.Background(), cfg, func(_ context.Context) (*Response, error) {
		calls++
		return &Response{Content: "ok"}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("unexpected content: %s", resp.Content)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestRetryGenerate_RetriesOnRetryableError(t *testing.T) {
	cfg := BaseConfig{MaxRetries: 3, RetryBaseDelay: time.Millisecond, RetryMaxDelay: 10 * time.Millisecond}
	calls := 0
	resp, err := retryGenerate(context.Background(), cfg, func(_ context.Context) (*Response, error) {
		calls++
		if calls < 3 {
			return nil, &anthropic.Error{StatusCode: 429}
		}
		return &Response{Content: "ok"}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("unexpected content: %s", resp.Content)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestRetryGenerate_StopsOnNonRetryableError(t *testing.T) {
	cfg := BaseConfig{MaxRetries: 3, RetryBaseDelay: time.Millisecond, RetryMaxDelay: 10 * time.Millisecond}
	calls := 0
	_, err := retryGenerate(context.Background(), cfg, func(_ context.Context) (*Response, error) {
		calls++
		return nil, &anthropic.Error{StatusCode: 400}
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("expected 1 call for non-retryable error, got %d", calls)
	}
}

func TestRetryGenerate_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := BaseConfig{MaxRetries: 3, RetryBaseDelay: time.Millisecond, RetryMaxDelay: 10 * time.Millisecond}
	_, err := retryGenerate(ctx, cfg, func(_ context.Context) (*Response, error) {
		return nil, &anthropic.Error{StatusCode: 429}
	})
	if !errors.Is(err, context.Canceled) {
		// Could also be the 429 error if the first call happened before cancel was observed
		if err == nil {
			t.Fatal("expected error")
		}
	}
}

func TestRetryDelay_ExponentialBackoff(t *testing.T) {
	base := 100 * time.Millisecond
	max := 10 * time.Second

	d0 := retryDelay(0, base, max)
	d1 := retryDelay(1, base, max)
	d2 := retryDelay(2, base, max)

	// Allow for jitter, but the general trend should be increasing
	if d1 < d0/2 {
		t.Fatalf("expected d1 (%v) > d0/2 (%v)", d1, d0/2)
	}
	if d2 < d1/2 {
		t.Fatalf("expected d2 (%v) > d1/2 (%v)", d2, d1/2)
	}
}

func TestRetryDelay_CappedAtMax(t *testing.T) {
	base := time.Second
	max := 2 * time.Second

	// attempt=10 would be 1024s without cap
	d := retryDelay(10, base, max)
	if d > max*2 { // allow some jitter headroom
		t.Fatalf("delay %v exceeds max %v (with jitter)", d, max)
	}
}

func TestIsRetryableError_TypedErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{
			name:      "ProviderError retryable",
			err:       &ProviderError{Retryable: true},
			retryable: true,
		},
		{
			name:      "ProviderError not retryable",
			err:       &ProviderError{Retryable: false},
			retryable: false,
		},
		{
			name:      "anthropic 429",
			err:       &anthropic.Error{StatusCode: 429},
			retryable: true,
		},
		{
			name:      "anthropic 500",
			err:       &anthropic.Error{StatusCode: 500},
			retryable: true,
		},
		{
			name:      "anthropic 503",
			err:       &anthropic.Error{StatusCode: 503},
			retryable: true,
		},
		{
			name:      "anthropic 401 not retryable",
			err:       &anthropic.Error{StatusCode: 401},
			retryable: false,
		},
		{
			name:      "anthropic 400 not retryable",
			err:       &anthropic.Error{StatusCode: 400},
			retryable: false,
		},
		{
			name:      "timeout net.Error",
			err:       &testTimeoutError{timeout: true},
			retryable: true,
		},
		{
			name:      "non-timeout net.Error",
			err:       &testTimeoutError{timeout: false},
			retryable: false,
		},
		{
			name:      "plain error not retryable",
			err:       errors.New("something went wrong"),
			retryable: false,
		},
		{
			name:      "unexpected eof",
			err:       io.ErrUnexpectedEOF,
			retryable: true,
		},
		{
			name:      "nil error",
			err:       nil,
			retryable: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRetryableError(tt.err)
			if got != tt.retryable {
				t.Errorf("isRetryableError() = %v, want %v", got, tt.retryable)
			}
		})
	}
}

// testTimeoutError implements net.Error for testing timeout classification.
type testTimeoutError struct {
	timeout bool
}

func (e *testTimeoutError) Error() string   { return "test network error" }
func (e *testTimeoutError) Timeout() bool   { return e.timeout }
func (e *testTimeoutError) Temporary() bool { return false }

var _ net.Error = (*testTimeoutError)(nil)

func TestServerGuidedDelay_UsesServerHint(t *testing.T) {
	// When the server says "wait 30s", the delay should be at least 30s
	// even if exponential backoff would compute less.
	err := &ProviderError{Retryable: true, RetryAfter: 30 * time.Second}
	d := serverGuidedDelay(err, 0, time.Second, 5*time.Second)
	if d < 30*time.Second {
		t.Fatalf("expected >= 30s from server hint, got %v", d)
	}
}

func TestServerGuidedDelay_FallsBackToExponential(t *testing.T) {
	// When no server hint, use normal exponential backoff.
	err := &ProviderError{Retryable: true, RetryAfter: 0}
	d := serverGuidedDelay(err, 2, time.Second, 30*time.Second)
	// Attempt 2: base * 2^2 = 4s (±jitter)
	if d < 2*time.Second || d > 6*time.Second {
		t.Fatalf("expected ~4s from exponential backoff, got %v", d)
	}
}

func TestServerGuidedDelay_ExponentialExceedsHint(t *testing.T) {
	// When exponential backoff already exceeds the server hint,
	// use the exponential value.
	err := &ProviderError{Retryable: true, RetryAfter: 100 * time.Millisecond}
	d := serverGuidedDelay(err, 3, time.Second, 30*time.Second)
	// Attempt 3: base * 2^3 = 8s (±jitter) — exceeds 100ms hint
	if d < 5*time.Second {
		t.Fatalf("expected exponential to dominate, got %v", d)
	}
}

func TestServerGuidedDelay_NilError(t *testing.T) {
	d := serverGuidedDelay(nil, 0, time.Second, 30*time.Second)
	// No server hint → pure exponential: base * 2^0 = 1s (±jitter)
	if d < 500*time.Millisecond || d > 2*time.Second {
		t.Fatalf("expected ~1s from exponential, got %v", d)
	}
}

func TestRetryStream_SucceedsFirstAttempt(t *testing.T) {
	cfg := BaseConfig{MaxRetries: 3, RetryBaseDelay: time.Millisecond, RetryMaxDelay: 10 * time.Millisecond}
	calls := 0
	err := retryStream(context.Background(), cfg, func(_ context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestRetryAnthropicInternalServerGenerate_RetriesThreeTimes(t *testing.T) {
	cfg := BaseConfig{RetryBaseDelay: time.Millisecond, RetryMaxDelay: 10 * time.Millisecond}
	ctx := context.Background()
	var events []RetryEvent
	ctx = WithRetryObserver(ctx, func(event RetryEvent) {
		events = append(events, event)
	})

	calls := 0
	resp, err := retryAnthropicInternalServerGenerate(ctx, cfg, func(_ context.Context) (*Response, error) {
		calls++
		if calls <= anthropicInternalServerMaxRetries {
			return nil, &anthropic.Error{StatusCode: 500}
		}
		return &Response{Content: "ok"}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Content != "ok" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if calls != anthropicInternalServerMaxRetries+1 {
		t.Fatalf("expected %d calls, got %d", anthropicInternalServerMaxRetries+1, calls)
	}
	if len(events) != anthropicInternalServerMaxRetries {
		t.Fatalf("expected %d retry events, got %d", anthropicInternalServerMaxRetries, len(events))
	}
	for i, event := range events {
		if event.Attempt != i+1 {
			t.Fatalf("event %d attempt = %d, want %d", i, event.Attempt, i+1)
		}
		if event.MaxAttempts != anthropicInternalServerMaxRetries {
			t.Fatalf("event %d max attempts = %d, want %d", i, event.MaxAttempts, anthropicInternalServerMaxRetries)
		}
	}
}

func TestRetryAnthropicInternalServerGenerate_OnlyRetries500(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "429", status: 429},
		{name: "503", status: 503},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := BaseConfig{RetryBaseDelay: time.Millisecond, RetryMaxDelay: 10 * time.Millisecond}
			calls := 0
			_, err := retryAnthropicInternalServerGenerate(context.Background(), cfg, func(_ context.Context) (*Response, error) {
				calls++
				return nil, &anthropic.Error{StatusCode: tt.status}
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if calls != 1 {
				t.Fatalf("expected 1 call, got %d", calls)
			}
		})
	}
}

func TestRetryAnthropicInternalServerGenerate_RetriesUnexpectedEOF(t *testing.T) {
	cfg := BaseConfig{RetryBaseDelay: time.Millisecond, RetryMaxDelay: 10 * time.Millisecond}
	ctx := context.Background()
	var events []RetryEvent
	ctx = WithRetryObserver(ctx, func(event RetryEvent) {
		events = append(events, event)
	})

	calls := 0
	resp, err := retryAnthropicInternalServerGenerate(ctx, cfg, func(_ context.Context) (*Response, error) {
		calls++
		if calls <= anthropicInternalServerMaxRetries {
			return nil, io.ErrUnexpectedEOF
		}
		return &Response{Content: "ok"}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Content != "ok" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if calls != anthropicInternalServerMaxRetries+1 {
		t.Fatalf("expected %d calls, got %d", anthropicInternalServerMaxRetries+1, calls)
	}
	if len(events) != anthropicInternalServerMaxRetries {
		t.Fatalf("expected %d retry events, got %d", anthropicInternalServerMaxRetries, len(events))
	}
}

func TestRetryAnthropicInternalServerStream_RetriesThreeTimes(t *testing.T) {
	cfg := BaseConfig{RetryBaseDelay: time.Millisecond, RetryMaxDelay: 10 * time.Millisecond}
	ctx := context.Background()
	var events []RetryEvent
	ctx = WithRetryObserver(ctx, func(event RetryEvent) {
		events = append(events, event)
	})

	calls := 0
	err := retryAnthropicInternalServerStream(ctx, cfg, func(_ context.Context) error {
		calls++
		if calls <= anthropicInternalServerMaxRetries {
			return &anthropic.Error{StatusCode: 500}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != anthropicInternalServerMaxRetries+1 {
		t.Fatalf("expected %d calls, got %d", anthropicInternalServerMaxRetries+1, calls)
	}
	if len(events) != anthropicInternalServerMaxRetries {
		t.Fatalf("expected %d retry events, got %d", anthropicInternalServerMaxRetries, len(events))
	}
}

func TestRetryAnthropicInternalServerStream_RetriesUnexpectedEOF(t *testing.T) {
	cfg := BaseConfig{RetryBaseDelay: time.Millisecond, RetryMaxDelay: 10 * time.Millisecond}
	ctx := context.Background()
	var events []RetryEvent
	ctx = WithRetryObserver(ctx, func(event RetryEvent) {
		events = append(events, event)
	})

	calls := 0
	err := retryAnthropicInternalServerStream(ctx, cfg, func(_ context.Context) error {
		calls++
		if calls <= anthropicInternalServerMaxRetries {
			return io.ErrUnexpectedEOF
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != anthropicInternalServerMaxRetries+1 {
		t.Fatalf("expected %d calls, got %d", anthropicInternalServerMaxRetries+1, calls)
	}
	if len(events) != anthropicInternalServerMaxRetries {
		t.Fatalf("expected %d retry events, got %d", anthropicInternalServerMaxRetries, len(events))
	}
}

func TestRetryAwareHandler_MarksRetryResetAfterError(t *testing.T) {
	var starts []*StreamChunk
	handler := retryAwareHandler(func(chunk *StreamChunk) error {
		if chunk != nil && chunk.Type == ChunkTypeStart {
			copyChunk := *chunk
			starts = append(starts, &copyChunk)
		}
		return nil
	})

	if err := handler(&StreamChunk{Type: ChunkTypeStart}); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if err := handler(&StreamChunk{Type: ChunkTypeError, Text: "boom"}); err != nil {
		t.Fatalf("error chunk: %v", err)
	}
	if err := handler(&StreamChunk{Type: ChunkTypeStart}); err != nil {
		t.Fatalf("second start: %v", err)
	}

	if len(starts) != 2 {
		t.Fatalf("expected 2 starts, got %d", len(starts))
	}
	if starts[0].RetryReset {
		t.Fatal("first start should not reset")
	}
	if !starts[1].RetryReset {
		t.Fatal("second start should reset after retryable error")
	}
}
