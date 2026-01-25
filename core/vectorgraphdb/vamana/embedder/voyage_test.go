package embedder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewVoyageEmbedder(t *testing.T) {
	t.Run("requires API key", func(t *testing.T) {
		_, err := NewVoyageEmbedder(VoyageConfig{})
		if err == nil {
			t.Fatal("expected error for missing API key")
		}
	})

	t.Run("creates with valid config", func(t *testing.T) {
		v, err := NewVoyageEmbedder(VoyageConfig{APIKey: "test-key"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer v.Close()

		if v.Dimension() != voyageCode3Dim {
			t.Errorf("dimension = %d, want %d", v.Dimension(), voyageCode3Dim)
		}
	})

	t.Run("rejects unsupported model", func(t *testing.T) {
		_, err := NewVoyageEmbedder(VoyageConfig{
			APIKey: "test-key",
			Model:  VoyageModel("unsupported-model"),
		})
		if err == nil {
			t.Fatal("expected error for unsupported model")
		}
	})

	t.Run("uses provided rate limiter", func(t *testing.T) {
		rl := NewRateLimiter(Config{TokensPerMin: 1000, RequestsPerMin: 10})
		defer rl.Close()

		v, err := NewVoyageEmbedder(VoyageConfig{
			APIKey:      "test-key",
			RateLimiter: rl,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer v.Close()

		if v.ownLimiter {
			t.Error("should not own rate limiter when provided externally")
		}
	})

	t.Run("uses custom base URL", func(t *testing.T) {
		v, err := NewVoyageEmbedder(VoyageConfig{
			APIKey:  "test-key",
			BaseURL: "https://custom.api.com/v1/embeddings",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer v.Close()

		if v.baseURL != "https://custom.api.com/v1/embeddings" {
			t.Errorf("baseURL = %q, want custom URL", v.baseURL)
		}
	})
}

func TestVoyageEmbedder_Dimension(t *testing.T) {
	tests := []struct {
		model VoyageModel
		want  int
	}{
		{VoyageCode3, voyageCode3Dim},
		{Voyage35, voyage35Dim},
		{Voyage35Lite, voyage35LiteDim},
	}

	for _, tt := range tests {
		t.Run(string(tt.model), func(t *testing.T) {
			v, err := NewVoyageEmbedder(VoyageConfig{
				APIKey: "test-key",
				Model:  tt.model,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			defer v.Close()

			if v.Dimension() != tt.want {
				t.Errorf("Dimension() = %d, want %d", v.Dimension(), tt.want)
			}
		})
	}
}

func TestVoyageEmbedder_EmbedBatch_Empty(t *testing.T) {
	v, err := NewVoyageEmbedder(VoyageConfig{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer v.Close()

	results, err := v.EmbedBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}

	if results != nil {
		t.Errorf("expected nil results for empty input")
	}
}

func TestVoyageError(t *testing.T) {
	err := &VoyageError{
		StatusCode: 429,
		Message:    "rate limited",
		Type:       "rate_limit_error",
	}

	if !err.Temporary() {
		t.Error("rate limit error should be temporary")
	}

	expected := "voyage: rate_limit_error (429): rate limited"
	if err.Error() != expected {
		t.Errorf("error = %q, want %q", err.Error(), expected)
	}

	err2 := &VoyageError{
		StatusCode: 400,
		Message:    "bad request",
	}

	if err2.Temporary() {
		t.Error("bad request error should not be temporary")
	}
}

func TestVoyageError_WithoutType(t *testing.T) {
	err := &VoyageError{
		StatusCode: 500,
		Message:    "internal error",
	}

	expected := "voyage: (500): internal error"
	if err.Error() != expected {
		t.Errorf("error = %q, want %q", err.Error(), expected)
	}

	if !err.Temporary() {
		t.Error("5xx error should be temporary")
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"rate limit", &VoyageError{StatusCode: 429}, true},
		{"server error 500", &VoyageError{StatusCode: 500}, true},
		{"server error 503", &VoyageError{StatusCode: 503}, true},
		{"bad request", &VoyageError{StatusCode: 400}, false},
		{"unauthorized", &VoyageError{StatusCode: 401}, false},
		{"not found", &VoyageError{StatusCode: 404}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if isRetryableError(tt.err) != tt.retryable {
				t.Errorf("isRetryableError() = %v, want %v", !tt.retryable, tt.retryable)
			}
		})
	}
}

func TestDimensionForModel(t *testing.T) {
	tests := []struct {
		model VoyageModel
		want  int
	}{
		{VoyageCode3, 1024},
		{Voyage35, 1024},
		{Voyage35Lite, 512},
		{VoyageModel("unknown"), 0},
	}

	for _, tt := range tests {
		t.Run(string(tt.model), func(t *testing.T) {
			got := dimensionForModel(tt.model)
			if got != tt.want {
				t.Errorf("dimensionForModel(%q) = %d, want %d", tt.model, got, tt.want)
			}
		})
	}
}

func TestParseVoyageError(t *testing.T) {
	t.Run("valid error response", func(t *testing.T) {
		body := `{"error":{"message":"rate limited","type":"rate_limit_error","code":"rate_limit"}}`
		err := parseVoyageError(429, []byte(body))

		voyageErr, ok := err.(*VoyageError)
		if !ok {
			t.Fatalf("expected *VoyageError, got %T", err)
		}

		if voyageErr.StatusCode != 429 {
			t.Errorf("StatusCode = %d, want 429", voyageErr.StatusCode)
		}
		if voyageErr.Message != "rate limited" {
			t.Errorf("Message = %q, want %q", voyageErr.Message, "rate limited")
		}
		if voyageErr.Type != "rate_limit_error" {
			t.Errorf("Type = %q, want %q", voyageErr.Type, "rate_limit_error")
		}
	})

	t.Run("invalid JSON falls back to raw body", func(t *testing.T) {
		body := `not json`
		err := parseVoyageError(500, []byte(body))

		voyageErr, ok := err.(*VoyageError)
		if !ok {
			t.Fatalf("expected *VoyageError, got %T", err)
		}

		if voyageErr.Message != "not json" {
			t.Errorf("Message = %q, want %q", voyageErr.Message, "not json")
		}
	})
}

func TestDefaultVoyageConfig(t *testing.T) {
	cfg := DefaultVoyageConfig()

	if cfg.Model != VoyageCode3 {
		t.Errorf("Model = %q, want %q", cfg.Model, VoyageCode3)
	}
	if cfg.InputType != InputTypeDocument {
		t.Errorf("InputType = %q, want %q", cfg.InputType, InputTypeDocument)
	}
	if cfg.Timeout != voyageDefaultTimeout {
		t.Errorf("Timeout = %v, want %v", cfg.Timeout, voyageDefaultTimeout)
	}
	if cfg.MaxRetries != voyageDefaultRetries {
		t.Errorf("MaxRetries = %d, want %d", cfg.MaxRetries, voyageDefaultRetries)
	}
	if cfg.BatchSize != voyageDefaultBatchSize {
		t.Errorf("BatchSize = %d, want %d", cfg.BatchSize, voyageDefaultBatchSize)
	}
}

func TestVoyageEmbedder_Close(t *testing.T) {
	t.Run("closes owned rate limiter", func(t *testing.T) {
		v, err := NewVoyageEmbedder(VoyageConfig{APIKey: "test-key"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !v.ownLimiter {
			t.Error("should own rate limiter when not provided")
		}

		if err := v.Close(); err != nil {
			t.Errorf("Close failed: %v", err)
		}
	})

	t.Run("does not close external rate limiter", func(t *testing.T) {
		rl := NewRateLimiter(Config{TokensPerMin: 1000, RequestsPerMin: 10})

		v, err := NewVoyageEmbedder(VoyageConfig{
			APIKey:      "test-key",
			RateLimiter: rl,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if err := v.Close(); err != nil {
			t.Errorf("Close failed: %v", err)
		}

		rl.Close()
	})
}

func TestVoyageEmbedder_Integration(t *testing.T) {
	embedding := make([]float32, voyageCode3Dim)
	for i := range embedding {
		embedding[i] = float32(i) / float32(voyageCode3Dim)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}

		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing or invalid authorization header")
		}

		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %s, want application/json", r.Header.Get("Content-Type"))
		}

		var req voyageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if req.Model != string(VoyageCode3) {
			t.Errorf("model = %s, want %s", req.Model, VoyageCode3)
		}

		embeddings := make([][]float32, len(req.Input))
		for i := range embeddings {
			embeddings[i] = embedding
		}

		resp := voyageResponse{
			Embeddings: embeddings,
			Model:      req.Model,
			Usage:      voyageUsage{PromptTokens: 10, TotalTokens: 10},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	t.Run("single embed", func(t *testing.T) {
		v := createTestEmbedder(t, server.URL)
		defer v.Close()

		result, err := v.Embed(context.Background(), "test text")
		if err != nil {
			t.Fatalf("Embed failed: %v", err)
		}

		if len(result) != voyageCode3Dim {
			t.Errorf("result length = %d, want %d", len(result), voyageCode3Dim)
		}
	})

	t.Run("batch embed", func(t *testing.T) {
		v := createTestEmbedder(t, server.URL)
		defer v.Close()

		texts := []string{"text1", "text2", "text3"}
		results, err := v.EmbedBatch(context.Background(), texts)
		if err != nil {
			t.Fatalf("EmbedBatch failed: %v", err)
		}

		if len(results) != len(texts) {
			t.Errorf("results length = %d, want %d", len(results), len(texts))
		}
	})
}

func TestVoyageEmbedder_Batching(t *testing.T) {
	var requestCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)

		var req voyageRequest
		json.NewDecoder(r.Body).Decode(&req)

		embeddings := make([][]float32, len(req.Input))
		for i := range embeddings {
			embeddings[i] = make([]float32, voyageCode3Dim)
		}

		resp := voyageResponse{
			Embeddings: embeddings,
			Model:      req.Model,
		}

		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	v := createTestEmbedderWithBatchSize(t, server.URL, 5)
	defer v.Close()

	texts := make([]string, 12)
	for i := range texts {
		texts[i] = "text"
	}

	results, err := v.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}

	if len(results) != 12 {
		t.Errorf("results length = %d, want 12", len(results))
	}

	if requestCount.Load() != 3 {
		t.Errorf("request count = %d, want 3 (batches of 5, 5, 2)", requestCount.Load())
	}
}

func TestVoyageEmbedder_Retry(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(voyageErrorResponse{
				Error: struct {
					Message string `json:"message"`
					Type    string `json:"type"`
					Code    string `json:"code"`
				}{Message: "rate limited", Type: "rate_limit_error"},
			})
			return
		}

		var req voyageRequest
		json.NewDecoder(r.Body).Decode(&req)

		resp := voyageResponse{
			Embeddings: [][]float32{make([]float32, voyageCode3Dim)},
			Model:      req.Model,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	v := createTestEmbedder(t, server.URL)
	defer v.Close()

	_, err := v.Embed(context.Background(), "test")
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}

	if attempts.Load() != 2 {
		t.Errorf("attempts = %d, want 2", attempts.Load())
	}
}

func TestVoyageEmbedder_NoRetryOnAuthError(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(voyageErrorResponse{
			Error: struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			}{Message: "invalid api key", Type: "authentication_error"},
		})
	}))
	defer server.Close()

	v := createTestEmbedder(t, server.URL)
	defer v.Close()

	_, err := v.Embed(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error for auth failure")
	}

	if attempts.Load() != 1 {
		t.Errorf("attempts = %d, want 1 (should not retry auth errors)", attempts.Load())
	}
}

func TestVoyageEmbedder_Stats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := voyageResponse{
			Embeddings: [][]float32{make([]float32, voyageCode3Dim)},
			Model:      string(VoyageCode3),
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	v := createTestEmbedder(t, server.URL)
	defer v.Close()

	v.Embed(context.Background(), "test1")
	v.Embed(context.Background(), "test2")

	calls, lastErr := v.Stats()
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if lastErr != nil {
		t.Errorf("unexpected lastErr: %v", lastErr)
	}
}

func TestVoyageEmbedder_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Second)
	}))
	defer server.Close()

	v := createTestEmbedder(t, server.URL)
	defer v.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := v.Embed(ctx, "test")
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func createTestEmbedder(t *testing.T, baseURL string) *VoyageEmbedder {
	t.Helper()
	return createTestEmbedderWithBatchSize(t, baseURL, voyageDefaultBatchSize)
}

func createTestEmbedderWithBatchSize(t *testing.T, baseURL string, batchSize int) *VoyageEmbedder {
	t.Helper()

	v, err := NewVoyageEmbedder(VoyageConfig{
		APIKey:     "test-key",
		Model:      VoyageCode3,
		BatchSize:  batchSize,
		MaxRetries: 3,
		Timeout:    5 * time.Second,
		BaseURL:    baseURL,
	})
	if err != nil {
		t.Fatalf("failed to create embedder: %v", err)
	}

	return v
}
