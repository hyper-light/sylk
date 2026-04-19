package providers

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
	"google.golang.org/genai"
)

func TestFriendlyErrorMessage_Nil(t *testing.T) {
	if got := FriendlyErrorMessage(nil); got != "" {
		t.Fatalf("expected empty for nil, got %q", got)
	}
}

func TestFriendlyErrorMessage_ProviderError(t *testing.T) {
	pe := &ProviderError{
		Provider:   ProviderTypeAnthropic,
		Operation:  "stream",
		StatusCode: http.StatusTooManyRequests,
		Message:    `received error while streaming: {"error":{"message":"Rate limit reached"}}`,
	}
	got := FriendlyErrorMessage(pe)
	if got != "Rate limited — Rate limit reached" {
		t.Fatalf("expected friendly provider error, got %q", got)
	}
}

func TestFriendlyErrorMessage_WrappedProviderError(t *testing.T) {
	pe := &ProviderError{
		Provider:   ProviderTypeAnthropic,
		Operation:  "stream",
		StatusCode: http.StatusServiceUnavailable,
		Message:    `{"error":{"message":"Overloaded"}}`,
	}
	wrapped := fmt.Errorf("planning protocol: %w", pe)
	got := FriendlyErrorMessage(wrapped)
	if got != "Service unavailable — Overloaded" {
		t.Fatalf("expected friendly from wrapped ProviderError, got %q", got)
	}
}

func TestFriendlyErrorMessage_EmbeddedJSON(t *testing.T) {
	raw := `planning protocol: architect llm: anthropic stream: received error while streaming: {"type":"error","error":{"details":null,"type":"overloaded_error","message":"Overloaded"},"request_id":"req_011CYeT1GYt7Kg27RdtV9zeo"} (conversation unavailable: architect planner: anthropic stream: received error while streaming: {"type":"error","error":{"details":null,"type":"overloaded_error","message":"Overloaded"},"request_id":"req_011CYeSzHBJdCParu7tGNPhn"})`
	got := FriendlyErrorMessage(errors.New(raw))
	want := "Overloaded (overloaded_error) [req_011CYeT1GYt7Kg27RdtV9zeo]"
	if got != want {
		t.Fatalf("expected %q from embedded JSON, got %q", want, got)
	}
}

func TestFriendlyErrorMessage_PlainError(t *testing.T) {
	got := FriendlyErrorMessage(errors.New("something broke"))
	if got != "something broke" {
		t.Fatalf("expected plain fallback, got %q", got)
	}
}

func TestFriendlyErrorMessage_PrefixedPlainError(t *testing.T) {
	got := FriendlyErrorMessage(errors.New("planning protocol: architect llm: anthropic stream: connection reset"))
	if got != "connection reset" {
		t.Fatalf("expected stripped prefixes, got %q", got)
	}
}

// testAnthropicError constructs an anthropic.Error with the required Request
// and Response fields populated. The SDK's Error.Error() dereferences both.
func testAnthropicError(statusCode int, headers http.Header) *anthropic.Error {
	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", nil)
	resp := &http.Response{
		StatusCode: statusCode,
		Header:     headers,
	}
	return &anthropic.Error{
		StatusCode: statusCode,
		Request:    req,
		Response:   resp,
	}
}

func TestParseError_AnthropicRetryAfter(t *testing.T) {
	apiErr := testAnthropicError(529, http.Header{"Retry-After": []string{"30"}})
	pe := NewProviderError(ProviderTypeAnthropic, "stream", apiErr)
	if pe.StatusCode != 529 {
		t.Fatalf("expected status 529, got %d", pe.StatusCode)
	}
	if !pe.Retryable {
		t.Fatal("expected 529 to be retryable")
	}
	if pe.RetryAfter != 30*time.Second {
		t.Fatalf("expected RetryAfter=30s, got %v", pe.RetryAfter)
	}
}

func TestParseError_AnthropicRetryAfterMs(t *testing.T) {
	apiErr := testAnthropicError(429, http.Header{"Retry-After-Ms": []string{"5000"}})
	pe := NewProviderError(ProviderTypeAnthropic, "stream", apiErr)
	if pe.RetryAfter != 5*time.Second {
		t.Fatalf("expected RetryAfter=5s, got %v", pe.RetryAfter)
	}
}

func TestParseError_AnthropicNoRetryAfterHeader(t *testing.T) {
	apiErr := testAnthropicError(529, http.Header{})
	pe := NewProviderError(ProviderTypeAnthropic, "stream", apiErr)
	if pe.RetryAfter != 0 {
		t.Fatalf("expected zero RetryAfter without header, got %v", pe.RetryAfter)
	}
}

func TestParseError_AnthropicOverloaded529(t *testing.T) {
	// 529 is Anthropic's "Overloaded" status — retryable, but typically
	// without Retry-After headers (the server is too overloaded to compute one).
	apiErr := testAnthropicError(529, http.Header{})
	pe := NewProviderError(ProviderTypeAnthropic, "stream", apiErr)
	if pe.StatusCode != 529 {
		t.Fatalf("expected status 529, got %d", pe.StatusCode)
	}
	if !pe.Retryable {
		t.Fatal("expected 529 to be retryable")
	}
	if pe.RetryAfter != 0 {
		t.Fatalf("expected zero RetryAfter for bare 529, got %v", pe.RetryAfter)
	}
}

type testConnResetNetError struct{}

func (e *testConnResetNetError) Error() string {
	return "read tcp 192.168.1.32:57205->172.64.155.209:443: read: connection reset by peer"
}

func (e *testConnResetNetError) Timeout() bool   { return false }
func (e *testConnResetNetError) Temporary() bool { return false }

var _ net.Error = (*testConnResetNetError)(nil)

func TestParseError_NetConnectionResetRetryable(t *testing.T) {
	pe := NewProviderError(ProviderTypeOpenAI, "stream", &testConnResetNetError{})
	if !pe.Retryable {
		t.Fatal("expected connection reset net.Error to be retryable")
	}
}

func TestParseError_NetOpErrorRetryable(t *testing.T) {
	pe := NewProviderError(ProviderTypeOpenAI, "stream", &net.OpError{
		Op:  "read",
		Net: "tcp",
		Err: errors.New("socket failure"),
	})
	if !pe.Retryable {
		t.Fatal("expected net.OpError to be retryable")
	}
}

// ===== Google APIError path =====

func TestParseError_GoogleRetryAfterFromDetails(t *testing.T) {
	apiErr := genai.APIError{
		Code:    429,
		Status:  "RESOURCE_EXHAUSTED",
		Message: "rate limit hit",
		Details: []map[string]any{{
			"@type":      "type.googleapis.com/google.rpc.RetryInfo",
			"retryDelay": "7s",
		}},
	}
	pe := NewProviderError(ProviderTypeGoogle, "generate", apiErr)
	if pe.StatusCode != 429 {
		t.Fatalf("expected status 429, got %d", pe.StatusCode)
	}
	if !pe.Retryable {
		t.Fatal("expected 429 to be retryable")
	}
	if pe.RetryAfter != 7*time.Second {
		t.Fatalf("expected RetryAfter=7s, got %v", pe.RetryAfter)
	}
}

func TestParseError_GoogleRetryAfterFromMessage(t *testing.T) {
	apiErr := genai.APIError{
		Code:    429,
		Status:  "RESOURCE_EXHAUSTED",
		Message: "Your quota will reset after 42s.",
	}
	pe := NewProviderError(ProviderTypeGoogle, "generate", apiErr)
	if pe.RetryAfter != 42*time.Second {
		t.Fatalf("expected RetryAfter=42s from message, got %v", pe.RetryAfter)
	}
}

func TestParseError_GoogleTerminalQuotaNotRetryable(t *testing.T) {
	apiErr := genai.APIError{
		Code:    429,
		Status:  "RESOURCE_EXHAUSTED",
		Message: "daily quota exceeded",
		Details: []map[string]any{{
			"@type": "type.googleapis.com/google.rpc.QuotaFailure",
			"violations": []any{
				map[string]any{
					"quotaId":    "GenerateRequestsPerDayPerProject",
					"quotaMetric": "aiplatform.googleapis.com/generate_content_requests",
				},
			},
		}},
	}
	pe := NewProviderError(ProviderTypeGoogle, "generate", apiErr)
	if pe.Retryable {
		t.Fatal("expected terminal quota to be non-retryable")
	}
}

func TestParseError_GoogleServerError(t *testing.T) {
	apiErr := genai.APIError{Code: 503, Status: "UNAVAILABLE", Message: "service unavailable"}
	pe := NewProviderError(ProviderTypeGoogle, "generate", apiErr)
	if pe.StatusCode != 503 {
		t.Fatalf("expected status 503, got %d", pe.StatusCode)
	}
	if !pe.Retryable {
		t.Fatal("expected 503 to be retryable")
	}
	if pe.RetryAfter != 0 {
		t.Fatalf("expected zero RetryAfter without hint, got %v", pe.RetryAfter)
	}
}

func TestParseError_GoogleNonAPIErrorFallsThrough(t *testing.T) {
	pe := NewProviderError(ProviderTypeGoogle, "generate", errors.New("no api envelope"))
	if pe.StatusCode != 0 {
		t.Fatalf("expected status 0 for non-API error, got %d", pe.StatusCode)
	}
	if pe.Retryable {
		t.Fatal("expected non-retryable for opaque error")
	}
}

// ===== OpenAI Error path =====

// testOpenAIError builds an *openai.Error with Response headers populated.
func testOpenAIError(statusCode int, headers http.Header) *openai.Error {
	req, _ := http.NewRequest("POST", "https://api.openai.com/v1/responses", nil)
	resp := &http.Response{StatusCode: statusCode, Header: headers, Request: req}
	return &openai.Error{
		StatusCode: statusCode,
		Message:    "test failure",
		Request:    req,
		Response:   resp,
	}
}

func TestParseError_OpenAIRetryAfterSeconds(t *testing.T) {
	apiErr := testOpenAIError(429, http.Header{"Retry-After": []string{"12"}})
	pe := NewProviderError(ProviderTypeOpenAI, "generate", apiErr)
	if pe.StatusCode != 429 {
		t.Fatalf("expected status 429, got %d", pe.StatusCode)
	}
	if !pe.Retryable {
		t.Fatal("expected 429 to be retryable")
	}
	if pe.RetryAfter != 12*time.Second {
		t.Fatalf("expected RetryAfter=12s, got %v", pe.RetryAfter)
	}
}

func TestParseError_OpenAIRetryAfterMs(t *testing.T) {
	apiErr := testOpenAIError(429, http.Header{"Retry-After-Ms": []string{"750"}})
	pe := NewProviderError(ProviderTypeOpenAI, "generate", apiErr)
	if pe.RetryAfter != 750*time.Millisecond {
		t.Fatalf("expected RetryAfter=750ms, got %v", pe.RetryAfter)
	}
}

func TestParseError_OpenAIServerError(t *testing.T) {
	apiErr := testOpenAIError(503, http.Header{})
	pe := NewProviderError(ProviderTypeOpenAI, "generate", apiErr)
	if pe.StatusCode != 503 {
		t.Fatalf("expected status 503, got %d", pe.StatusCode)
	}
	if !pe.Retryable {
		t.Fatal("expected 503 to be retryable")
	}
	if pe.RetryAfter != 0 {
		t.Fatalf("expected zero RetryAfter for bare 503, got %v", pe.RetryAfter)
	}
}

func TestParseError_OpenAINon429NotRetryable(t *testing.T) {
	apiErr := testOpenAIError(400, http.Header{})
	pe := NewProviderError(ProviderTypeOpenAI, "generate", apiErr)
	if pe.Retryable {
		t.Fatal("expected 400 to be non-retryable")
	}
}

// ===== Provider-route independence =====

// TestParseError_ProviderTypeIsAdvisory verifies parseError classifies purely
// by the underlying error's SDK type, not the provider label. A Google APIError
// wrapped under ProviderTypeAnthropic (unrealistic but possible through the
// gateway) must still extract Retry-After via the Google path.
func TestParseError_ProviderTypeIsAdvisory(t *testing.T) {
	apiErr := genai.APIError{
		Code:    429,
		Message: "Your quota will reset after 9s.",
	}
	pe := NewProviderError(ProviderTypeAnthropic, "forwarded", apiErr)
	if pe.RetryAfter != 9*time.Second {
		t.Fatalf("expected Google path to extract RetryAfter regardless of provider label, got %v", pe.RetryAfter)
	}
}

func TestParseContextTooLarge_Google(t *testing.T) {
	pe := &ProviderError{}
	parseContextTooLarge("input token count: 150000 exceeds maximum: 100000", pe)
	if !pe.ContextTooLarge {
		t.Fatal("expected ContextTooLarge=true for Google pattern")
	}
	if pe.ContextUsedTokens != 150000 {
		t.Fatalf("expected ContextUsedTokens=150000, got %d", pe.ContextUsedTokens)
	}
	if pe.ContextMaxTokens != 100000 {
		t.Fatalf("expected ContextMaxTokens=100000, got %d", pe.ContextMaxTokens)
	}
}

func TestParseContextTooLarge_Anthropic(t *testing.T) {
	pe := &ProviderError{}
	parseContextTooLarge("prompt is too long: 250000 tokens > 200000", pe)
	if !pe.ContextTooLarge {
		t.Fatal("expected ContextTooLarge=true for Anthropic pattern")
	}
	if pe.ContextUsedTokens != 250000 {
		t.Fatalf("expected ContextUsedTokens=250000, got %d", pe.ContextUsedTokens)
	}
	if pe.ContextMaxTokens != 200000 {
		t.Fatalf("expected ContextMaxTokens=200000, got %d", pe.ContextMaxTokens)
	}
}

func TestParseContextTooLarge_NoMatch(t *testing.T) {
	pe := &ProviderError{}
	parseContextTooLarge("some other error", pe)
	if pe.ContextTooLarge {
		t.Fatal("expected ContextTooLarge=false for non-matching message")
	}
}

func TestCleanErrorPrefixes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "full chain",
			in:   "planning protocol: architect llm: anthropic stream: received error while streaming: some error",
			want: "some error",
		},
		{
			name: "with conversation unavailable suffix",
			in:   "planning protocol: architect llm: some error (conversation unavailable: more context)",
			want: "some error",
		},
		{
			name: "no prefixes",
			in:   "plain error message",
			want: "plain error message",
		},
		{
			name: "architect planner prefix",
			in:   "architect planner: anthropic stream: timeout",
			want: "timeout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanErrorPrefixes(tt.in)
			if got != tt.want {
				t.Fatalf("cleanErrorPrefixes(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
