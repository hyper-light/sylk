package providers

import (
	"errors"
	"net/http"
	"strings"
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
	retryable := &ProviderError{Provider: ProviderTypeGoogle, StatusCode: 429, Retryable: true}
	if !isGoogleRetryable(retryable) {
		t.Fatal("expected 429 retryable provider error to be retryable")
	}

	nonRetryable := &ProviderError{Provider: ProviderTypeGoogle, StatusCode: 403, Retryable: false}
	if isGoogleRetryable(nonRetryable) {
		t.Fatal("expected 403 provider error to be non-retryable")
	}

	// Terminal quota: 429 but Retryable=false (daily limit).
	terminal := &ProviderError{Provider: ProviderTypeGoogle, StatusCode: 429, Retryable: false}
	if isGoogleRetryable(terminal) {
		t.Fatal("expected terminal 429 provider error to be non-retryable")
	}
}

// ---------------------------------------------------------------------------
// classifyGoogleQuota
// ---------------------------------------------------------------------------

func TestClassifyGoogleQuota_RateLimitExceeded(t *testing.T) {
	apiErr := genai.APIError{
		Code:   429,
		Status: "RESOURCE_EXHAUSTED",
		Details: []map[string]any{
			{
				"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
				"reason": "RATE_LIMIT_EXCEEDED",
				"domain": "googleapis.com",
			},
		},
	}

	info := classifyGoogleQuota(apiErr)
	if info == nil {
		t.Fatal("expected non-nil quota info")
	}
	if info.Class != GoogleQuotaRetryable {
		t.Fatalf("expected retryable, got %d", info.Class)
	}
	// ErrorInfo without RetryInfo gives 10s default.
	if info.RetryDelay != 10*time.Second {
		t.Fatalf("expected 10s default delay, got %v", info.RetryDelay)
	}
}

func TestClassifyGoogleQuota_QuotaExhausted(t *testing.T) {
	apiErr := genai.APIError{
		Code:   429,
		Status: "RESOURCE_EXHAUSTED",
		Details: []map[string]any{
			{
				"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
				"reason": "QUOTA_EXHAUSTED",
				"domain": "cloudcode.googleapis.com",
			},
		},
	}

	info := classifyGoogleQuota(apiErr)
	if info == nil {
		t.Fatal("expected non-nil quota info")
	}
	if info.Class != GoogleQuotaTerminal {
		t.Fatalf("expected terminal, got %d", info.Class)
	}
}

func TestClassifyGoogleQuota_DailyQuotaFailure(t *testing.T) {
	apiErr := genai.APIError{
		Code:   429,
		Status: "RESOURCE_EXHAUSTED",
		Details: []map[string]any{
			{
				"@type": "type.googleapis.com/google.rpc.QuotaFailure",
				"violations": []any{
					map[string]any{
						"quotaId": "GenerateContentRequestsPerDayPerProjectPerModel",
					},
				},
			},
		},
	}

	info := classifyGoogleQuota(apiErr)
	if info == nil {
		t.Fatal("expected non-nil quota info")
	}
	if info.Class != GoogleQuotaTerminal {
		t.Fatalf("expected terminal for daily quota, got %d", info.Class)
	}
}

func TestClassifyGoogleQuota_PerMinuteQuotaFailure(t *testing.T) {
	apiErr := genai.APIError{
		Code:   429,
		Status: "RESOURCE_EXHAUSTED",
		Details: []map[string]any{
			{
				"@type": "type.googleapis.com/google.rpc.QuotaFailure",
				"violations": []any{
					map[string]any{
						"quotaId": "GenerateContentRequestsPerMinutePerProjectPerModel",
					},
				},
			},
		},
	}

	info := classifyGoogleQuota(apiErr)
	if info == nil {
		t.Fatal("expected non-nil quota info")
	}
	if info.Class != GoogleQuotaRetryable {
		t.Fatalf("expected retryable for per-minute quota, got %d", info.Class)
	}
	if info.RetryDelay != 60*time.Second {
		t.Fatalf("expected 60s default for per-minute quota, got %v", info.RetryDelay)
	}
}

func TestClassifyGoogleQuota_RetryInfoDelay(t *testing.T) {
	apiErr := genai.APIError{
		Code:   429,
		Status: "RESOURCE_EXHAUSTED",
		Details: []map[string]any{
			{
				"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
				"reason": "RATE_LIMIT_EXCEEDED",
			},
			{
				"@type":      "type.googleapis.com/google.rpc.RetryInfo",
				"retryDelay": "25s",
			},
		},
	}

	info := classifyGoogleQuota(apiErr)
	if info == nil {
		t.Fatal("expected non-nil quota info")
	}
	if info.Class != GoogleQuotaRetryable {
		t.Fatalf("expected retryable, got %d", info.Class)
	}
	if info.RetryDelay != 25*time.Second {
		t.Fatalf("expected 25s from RetryInfo, got %v", info.RetryDelay)
	}
}

func TestClassifyGoogleQuota_Non429ReturnsNil(t *testing.T) {
	apiErr := genai.APIError{Code: 500, Status: "INTERNAL"}
	if info := classifyGoogleQuota(apiErr); info != nil {
		t.Fatalf("expected nil for non-429, got %+v", info)
	}
}

func TestClassifyGoogleQuota_Bare429FallbackToMessage(t *testing.T) {
	apiErr := genai.APIError{
		Code:    429,
		Status:  "RESOURCE_EXHAUSTED",
		Message: "Your quota will reset after 45s.",
	}

	info := classifyGoogleQuota(apiErr)
	if info == nil {
		t.Fatal("expected non-nil quota info")
	}
	if info.Class != GoogleQuotaRetryable {
		t.Fatalf("expected retryable, got %d", info.Class)
	}
	if info.RetryDelay != 45*time.Second {
		t.Fatalf("expected 45s from message, got %v", info.RetryDelay)
	}
}

// ---------------------------------------------------------------------------
// Terminal quota skips retry
// ---------------------------------------------------------------------------

func TestTerminalQuotaSkipsRetry(t *testing.T) {
	apiErr := genai.APIError{
		Code:   429,
		Status: "RESOURCE_EXHAUSTED",
		Details: []map[string]any{
			{
				"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
				"reason": "QUOTA_EXHAUSTED",
			},
		},
	}
	wrapped := formatGoogleError("generate", apiErr)

	var pe *ProviderError
	if !errors.As(wrapped, &pe) {
		t.Fatalf("expected ProviderError, got %T", wrapped)
	}
	if pe.Retryable {
		t.Fatal("expected terminal quota to be non-retryable")
	}
	if isGoogleRetryable(wrapped) {
		t.Fatal("expected isGoogleRetryable to return false for terminal quota")
	}
}

// ---------------------------------------------------------------------------
// Rate limit delay uses 5s base
// ---------------------------------------------------------------------------

func TestGoogleRetryDelay_RateLimitUses5sBase(t *testing.T) {
	cfg := BaseConfig{
		RetryBaseDelay: time.Second,
		RetryMaxDelay:  30 * time.Second,
	}
	err := &ProviderError{
		Provider:   ProviderTypeGoogle,
		StatusCode: 429,
		Retryable:  true,
	}

	got := googleRetryDelay(err, 0, cfg)
	// With 5s base and 20% jitter, range is [4s, 6s].
	if got < 4*time.Second || got > 6*time.Second {
		t.Fatalf("expected delay ~5s (4-6s range), got %v", got)
	}

	// Second attempt: 10s base with jitter [8s, 12s].
	got = googleRetryDelay(err, 1, cfg)
	if got < 8*time.Second || got > 12*time.Second {
		t.Fatalf("expected delay ~10s (8-12s range), got %v", got)
	}
}

func TestGoogleRetryDelay_NonRateLimitUsesConfig(t *testing.T) {
	cfg := BaseConfig{
		RetryBaseDelay: time.Second,
		RetryMaxDelay:  30 * time.Second,
	}
	err := &ProviderError{
		Provider:   ProviderTypeGoogle,
		StatusCode: 500,
		Retryable:  true,
	}

	got := googleRetryDelay(err, 0, cfg)
	// With 1s base and 20% jitter, range is [0.8s, 1.2s].
	if got < 800*time.Millisecond || got > 1200*time.Millisecond {
		t.Fatalf("expected delay ~1s for 500 error, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// resolveGoogleMaxRetries
// ---------------------------------------------------------------------------

func TestResolveGoogleMaxRetries_Floor(t *testing.T) {
	if got := resolveGoogleMaxRetries(3); got != googleMinRetries {
		t.Fatalf("expected floor %d, got %d", googleMinRetries, got)
	}
	if got := resolveGoogleMaxRetries(12); got != 12 {
		t.Fatalf("expected 12, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// Code Assist quota classification
// ---------------------------------------------------------------------------

func TestCodeAssistHTTPError_TerminalQuota(t *testing.T) {
	body := []byte(`{"error":{"code":429,"message":"daily limit","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"QUOTA_EXHAUSTED","domain":"cloudcode.googleapis.com"}]}}`)

	pe := codeAssistHTTPError("stream", 429, body)
	if pe.Retryable {
		t.Fatal("expected terminal Code Assist quota to be non-retryable")
	}
}

func TestCodeAssistHTTPError_RetryableRateLimit(t *testing.T) {
	body := []byte(`{"error":{"code":429,"message":"rate limit","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RATE_LIMIT_EXCEEDED","domain":"googleapis.com"},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"15s"}]}}`)

	pe := codeAssistHTTPError("generate", 429, body)
	if !pe.Retryable {
		t.Fatal("expected retryable Code Assist rate limit")
	}
	if pe.RetryAfter != 15*time.Second {
		t.Fatalf("expected 15s retry delay, got %v", pe.RetryAfter)
	}
}

func TestCodeAssistHTTPError_PerMinuteQuota(t *testing.T) {
	body := []byte(`{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateContentRequestsPerMinutePerProjectPerModel"}]}]}}`)

	pe := codeAssistHTTPError("stream", 429, body)
	if !pe.Retryable {
		t.Fatal("expected retryable per-minute quota")
	}
	if pe.RetryAfter != 60*time.Second {
		t.Fatalf("expected 60s default for per-minute, got %v", pe.RetryAfter)
	}
}

func TestCodeAssistHTTPError_DailyQuota(t *testing.T) {
	body := []byte(`{"error":{"code":429,"message":"daily limit","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"TokensPerDayPerProjectPerModel"}]}]}}`)

	pe := codeAssistHTTPError("generate", 429, body)
	if pe.Retryable {
		t.Fatal("expected daily Code Assist quota to be non-retryable")
	}
}

func TestCodeAssistHTTPError_MessageFallback(t *testing.T) {
	body := []byte(`{"error":{"code":429,"message":"Please retry after 30s.","status":"RESOURCE_EXHAUSTED"}}`)

	pe := codeAssistHTTPError("stream", 429, body)
	if !pe.Retryable {
		t.Fatal("expected retryable for bare 429 with message")
	}
	if pe.RetryAfter != 30*time.Second {
		t.Fatalf("expected 30s from message fallback, got %v", pe.RetryAfter)
	}
}

func TestCodeAssistHTTPError_MalformedBody(t *testing.T) {
	body := []byte(`not json at all`)

	pe := codeAssistHTTPError("stream", 429, body)
	// Should still be retryable (basic 429 status) even without parsed details.
	if !pe.Retryable {
		t.Fatal("expected retryable for 429 with malformed body")
	}
}

func TestCodeAssistHTTPError_ServerError(t *testing.T) {
	body := []byte(`{"error":{"code":500,"message":"internal"}}`)

	pe := codeAssistHTTPError("generate", 500, body)
	if !pe.Retryable {
		t.Fatal("expected 500 to be retryable")
	}
	// classifyCodeAssistQuota returns nil for non-429, so no RetryAfter.
	if pe.RetryAfter != 0 {
		t.Fatalf("expected no retry-after for 500, got %v", pe.RetryAfter)
	}
}

func TestClassifyCodeAssistQuota_NilForNon429(t *testing.T) {
	body := []byte(`{"error":{"code":500,"message":"internal"}}`)
	if info := classifyCodeAssistQuota(500, body); info != nil {
		t.Fatalf("expected nil for non-429, got %+v", info)
	}
}

func TestClassifyCodeAssistQuota_NilForEmptyBody(t *testing.T) {
	if info := classifyCodeAssistQuota(429, nil); info != nil {
		t.Fatalf("expected nil for empty body, got %+v", info)
	}
	if info := classifyCodeAssistQuota(429, []byte("")); info != nil {
		t.Fatalf("expected nil for empty string body, got %+v", info)
	}
}

// ---------------------------------------------------------------------------
// HydratedGoogleAuth / NewGoogleProviderFromHydrated
// ---------------------------------------------------------------------------

func TestHydrateGoogleAuth_ReturnsFieldsFromConfig(t *testing.T) {
	// HydrateGoogleAuth with API key mode (no OAuth) populates the auth
	// service and preserves config defaults. This validates the public API
	// contract without requiring live OAuth credentials.
	cfg := GoogleConfig{
		BaseConfig: BaseConfig{
			APIKey:    "test-key",
			Model:     "gemini-3-flash-preview",
			MaxTokens: 4096,
		},
		AuthMode: GoogleAuthModeAPIKey,
	}

	hydrated, err := HydrateGoogleAuth(t.Context(), cfg)
	if err != nil {
		t.Fatalf("HydrateGoogleAuth: %v", err)
	}
	if hydrated == nil {
		t.Fatal("expected non-nil hydrated auth")
	}
	if hydrated.AuthService == nil {
		t.Fatal("expected non-nil auth service")
	}
	// API key mode doesn't enable Code Assist or Vertex.
	if hydrated.UseCodeAssist {
		t.Fatal("expected UseCodeAssist=false for API key mode")
	}
	if hydrated.UseVertexAI {
		t.Fatal("expected UseVertexAI=false for API key mode")
	}
}

func TestNewGoogleProviderFromHydrated_NilHydratedReturnsError(t *testing.T) {
	cfg := DefaultGoogleConfig()
	cfg.APIKey = "test-key"
	cfg.AuthMode = GoogleAuthModeAPIKey

	_, err := NewGoogleProviderFromHydrated(t.Context(), cfg, nil)
	if err == nil {
		t.Fatal("expected error for nil hydrated auth")
	}
}

func TestNewGoogleProviderFromHydrated_InheritsHydratedFields(t *testing.T) {
	cfg := GoogleConfig{
		BaseConfig: BaseConfig{
			APIKey:    "test-key",
			Model:     "gemini-3-flash-preview",
			MaxTokens: 4096,
		},
		AuthMode: GoogleAuthModeAPIKey,
	}

	hydrated, err := HydrateGoogleAuth(t.Context(), cfg)
	if err != nil {
		t.Fatalf("HydrateGoogleAuth: %v", err)
	}

	// Create provider with a different model but same hydrated auth.
	providerCfg := cfg
	providerCfg.Model = "gemini-3.1-pro-preview"

	provider, err := NewGoogleProviderFromHydrated(t.Context(), providerCfg, hydrated)
	if err != nil {
		t.Fatalf("NewGoogleProviderFromHydrated: %v", err)
	}
	if provider == nil {
		t.Fatal("expected non-nil provider")
	}
	if provider.DefaultModel() != "gemini-3.1-pro-preview" {
		t.Fatalf("expected model gemini-3.1-pro-preview, got %q", provider.DefaultModel())
	}
}

// ---------------------------------------------------------------------------
// Code Assist headers
// ---------------------------------------------------------------------------

func TestSetCodeAssistHeaders_SetsUserAgentAndClient(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://example.com", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	setCodeAssistHeaders(req)

	ua := req.Header.Get("User-Agent")
	if ua != codeAssistUserAgent {
		t.Fatalf("expected User-Agent %q, got %q", codeAssistUserAgent, ua)
	}

	client := req.Header.Get("X-Goog-Api-Client")
	if client == "" {
		t.Fatal("expected non-empty X-Goog-Api-Client header")
	}
	if !strings.HasPrefix(client, "google-genai-sdk/") {
		t.Fatalf("expected X-Goog-Api-Client to start with 'google-genai-sdk/', got %q", client)
	}
	if !strings.Contains(client, "gl-go/go") {
		t.Fatalf("expected X-Goog-Api-Client to contain 'gl-go/go', got %q", client)
	}
}

func TestCodeAssistClientHeader_MatchesExpectedFormat(t *testing.T) {
	// Validate the init-computed header matches "google-genai-sdk/1.47.0 gl-go/goX.Y.Z".
	if !strings.HasPrefix(codeAssistClientHeader, "google-genai-sdk/1.47.0 gl-go/go") {
		t.Fatalf("unexpected codeAssistClientHeader format: %q", codeAssistClientHeader)
	}
}
