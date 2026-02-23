package providers

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/genai"
)

func TestFormatGoogleError_WrapsAPIErrorAndExtractsRetryAfter(t *testing.T) {
	original := genai.APIError{
		Code:    429,
		Status:  "RESOURCE_EXHAUSTED",
		Message: "rate limit hit",
		Details: []map[string]any{
			{
				"@type":      "type.googleapis.com/google.rpc.RetryInfo",
				"retryDelay": "3.5s",
			},
		},
	}

	wrapped := formatGoogleError("stream", original)
	if wrapped == nil {
		t.Fatal("expected wrapped error")
	}

	var gotAPIError genai.APIError
	if !errors.As(wrapped, &gotAPIError) {
		t.Fatalf("expected wrapped error to expose genai.APIError, got %T", wrapped)
	}
	if gotAPIError.Code != original.Code {
		t.Fatalf("expected API error code %d, got %d", original.Code, gotAPIError.Code)
	}

	var providerErr *ProviderError
	if !errors.As(wrapped, &providerErr) {
		t.Fatalf("expected wrapped error to expose ProviderError, got %T", wrapped)
	}
	if providerErr.Operation != "stream" {
		t.Fatalf("expected operation stream, got %q", providerErr.Operation)
	}
	if providerErr.RetryAfter != 3500*time.Millisecond {
		t.Fatalf("expected retry-after 3.5s, got %v", providerErr.RetryAfter)
	}
}

func TestGoogleRetryAfterFromDetails_NestedValue(t *testing.T) {
	details := []map[string]any{
		{
			"errorInfo": map[string]any{
				"retryDelay": "2",
			},
		},
	}

	d, ok := googleRetryAfterFromDetails(details)
	if !ok {
		t.Fatal("expected retry-after to be parsed")
	}
	if d != 2*time.Second {
		t.Fatalf("expected retry-after 2s, got %v", d)
	}
}

func TestGoogleRetryDelay_UsesProviderRetryAfter(t *testing.T) {
	cfg := BaseConfig{
		RetryBaseDelay: time.Second,
		RetryMaxDelay:  3 * time.Second,
	}
	err := &ProviderError{
		Provider:   ProviderTypeGoogle,
		RetryAfter: 5 * time.Second,
	}

	// Server-specified retry delays are authoritative and not capped by RetryMaxDelay.
	got := googleRetryDelay(err, 0, cfg)
	if got != 5*time.Second {
		t.Fatalf("expected server-specified retry delay 5s, got %v", got)
	}
}

func TestGoogleRetryAfterFromMessage(t *testing.T) {
	d, ok := googleRetryAfterFromMessage("Your quota will reset after 39s.")
	if !ok {
		t.Fatal("expected to parse retry-after from message")
	}
	if d != 39*time.Second {
		t.Fatalf("expected 39s, got %v", d)
	}

	d, ok = googleRetryAfterFromMessage("You have exhausted your capacity. Your quota will reset after 2m30s.")
	if !ok {
		t.Fatal("expected to parse retry-after from message")
	}
	if d != 2*time.Minute+30*time.Second {
		t.Fatalf("expected 2m30s, got %v", d)
	}

	_, ok = googleRetryAfterFromMessage("Something completely unrelated.")
	if ok {
		t.Fatal("expected no retry-after from unrelated message")
	}
}

func TestIsGoogleRetryable_FromProviderError(t *testing.T) {
	retryable := &ProviderError{Provider: ProviderTypeGoogle, StatusCode: 429}
	if !isGoogleRetryable(retryable) {
		t.Fatal("expected 429 provider error to be retryable")
	}

	nonRetryable := &ProviderError{Provider: ProviderTypeGoogle, StatusCode: 403}
	if isGoogleRetryable(nonRetryable) {
		t.Fatal("expected 403 provider error to be non-retryable")
	}
}
