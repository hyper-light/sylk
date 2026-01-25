package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type VoyageModel string

const (
	VoyageCode3  VoyageModel = "voyage-code-3"
	Voyage35     VoyageModel = "voyage-3.5"
	Voyage35Lite VoyageModel = "voyage-3.5-lite"
)

type VoyageInputType string

const (
	InputTypeQuery    VoyageInputType = "query"
	InputTypeDocument VoyageInputType = "document"
)

const (
	voyageAPIURL           = "https://api.voyageai.com/v1/embeddings"
	voyageCode3Dim         = 1024
	voyage35Dim            = 1024
	voyage35LiteDim        = 512
	voyageDefaultRetries   = 3
	voyageRetryBackoff     = 500 * time.Millisecond
	voyageDefaultBatchSize = 128
	voyageDefaultTimeout   = 30 * time.Second
	voyageTokensPerMin     = 1_000_000
	voyageRequestsPerMin   = 300
	voyageCharsPerToken    = 4
)

type VoyageConfig struct {
	APIKey      string
	Model       VoyageModel
	InputType   VoyageInputType
	Timeout     time.Duration
	MaxRetries  int
	BatchSize   int
	RateLimiter *RateLimiter
	BaseURL     string
}

func DefaultVoyageConfig() VoyageConfig {
	return VoyageConfig{
		Model:      VoyageCode3,
		InputType:  InputTypeDocument,
		Timeout:    voyageDefaultTimeout,
		MaxRetries: voyageDefaultRetries,
		BatchSize:  voyageDefaultBatchSize,
	}
}

type voyageRequest struct {
	Input      []string `json:"input"`
	Model      string   `json:"model"`
	InputType  string   `json:"input_type,omitempty"`
	Truncation bool     `json:"truncation"`
}

type voyageResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Model      string      `json:"model"`
	Usage      voyageUsage `json:"usage"`
}

type voyageUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type voyageErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

type VoyageEmbedder struct {
	config     VoyageConfig
	client     *http.Client
	dimension  int
	limiter    *RateLimiter
	ownLimiter bool
	baseURL    string

	mu         sync.RWMutex
	totalCalls int64
	lastError  error
}

func NewVoyageEmbedder(cfg VoyageConfig) (*VoyageEmbedder, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("voyage: API key required")
	}

	if cfg.Model == "" {
		cfg.Model = VoyageCode3
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = voyageDefaultTimeout
	}

	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = voyageDefaultRetries
	}

	if cfg.BatchSize == 0 {
		cfg.BatchSize = voyageDefaultBatchSize
	}

	dim := dimensionForModel(cfg.Model)
	if dim == 0 {
		return nil, fmt.Errorf("voyage: unsupported model %q", cfg.Model)
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = voyageAPIURL
	}

	v := &VoyageEmbedder{
		config:    cfg,
		client:    &http.Client{Timeout: cfg.Timeout},
		dimension: dim,
		limiter:   cfg.RateLimiter,
		baseURL:   baseURL,
	}

	if v.limiter == nil {
		v.limiter = NewRateLimiter(Config{
			TokensPerMin:   voyageTokensPerMin,
			RequestsPerMin: voyageRequestsPerMin,
		})
		v.ownLimiter = true
	}

	return v, nil
}

func (v *VoyageEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := v.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, errors.New("voyage: empty response")
	}
	return embeddings[0], nil
}

func (v *VoyageEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	if len(texts) > v.config.BatchSize {
		return v.embedInBatches(ctx, texts)
	}

	return v.embedWithRetry(ctx, texts)
}

func (v *VoyageEmbedder) Dimension() int {
	return v.dimension
}

func (v *VoyageEmbedder) Close() error {
	if v.ownLimiter && v.limiter != nil {
		v.limiter.Close()
	}
	return nil
}

func (v *VoyageEmbedder) Stats() (calls int64, lastErr error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.totalCalls, v.lastError
}

func (v *VoyageEmbedder) embedInBatches(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, 0, len(texts))

	for i := 0; i < len(texts); i += v.config.BatchSize {
		end := min(i+v.config.BatchSize, len(texts))
		batch := texts[i:end]

		embeddings, err := v.embedWithRetry(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("voyage: batch %d failed: %w", i/v.config.BatchSize, err)
		}

		results = append(results, embeddings...)
	}

	return results, nil
}

func (v *VoyageEmbedder) embedWithRetry(ctx context.Context, texts []string) ([][]float32, error) {
	var lastErr error

	for attempt := range v.config.MaxRetries {
		embeddings, err := v.doEmbed(ctx, texts)
		if err == nil {
			return embeddings, nil
		}

		lastErr = err

		if !isRetryableError(err) {
			break
		}

		backoff := voyageRetryBackoff * time.Duration(1<<attempt)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}

	v.mu.Lock()
	v.lastError = lastErr
	v.mu.Unlock()

	return nil, lastErr
}

func (v *VoyageEmbedder) doEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	estimatedTokens := 0
	for _, t := range texts {
		estimatedTokens += len(t) / voyageCharsPerToken
	}

	if err := v.limiter.Acquire(ctx, estimatedTokens); err != nil {
		return nil, fmt.Errorf("voyage: rate limit: %w", err)
	}

	reqBody := voyageRequest{
		Input:      texts,
		Model:      string(v.config.Model),
		Truncation: true,
	}
	if v.config.InputType != "" {
		reqBody.InputType = string(v.config.InputType)
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("voyage: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("voyage: create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+v.config.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voyage: request failed: %w", err)
	}
	defer resp.Body.Close()

	v.mu.Lock()
	v.totalCalls++
	v.mu.Unlock()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("voyage: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, parseVoyageError(resp.StatusCode, respBody)
	}

	var voyageResp voyageResponse
	if err := json.Unmarshal(respBody, &voyageResp); err != nil {
		return nil, fmt.Errorf("voyage: unmarshal response: %w", err)
	}

	if len(voyageResp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("voyage: expected %d embeddings, got %d", len(texts), len(voyageResp.Embeddings))
	}

	return voyageResp.Embeddings, nil
}

func dimensionForModel(model VoyageModel) int {
	switch model {
	case VoyageCode3:
		return voyageCode3Dim
	case Voyage35:
		return voyage35Dim
	case Voyage35Lite:
		return voyage35LiteDim
	default:
		return 0
	}
}

func parseVoyageError(statusCode int, body []byte) error {
	var errResp voyageErrorResponse
	if err := json.Unmarshal(body, &errResp); err != nil {
		return &VoyageError{
			StatusCode: statusCode,
			Message:    string(body),
		}
	}

	return &VoyageError{
		StatusCode: statusCode,
		Message:    errResp.Error.Message,
		Type:       errResp.Error.Type,
		Code:       errResp.Error.Code,
	}
}

func isRetryableError(err error) bool {
	var voyageErr *VoyageError
	if errors.As(err, &voyageErr) {
		return voyageErr.StatusCode == http.StatusTooManyRequests ||
			voyageErr.StatusCode >= http.StatusInternalServerError
	}
	return false
}

type VoyageError struct {
	StatusCode int
	Message    string
	Type       string
	Code       string
}

func (e *VoyageError) Error() string {
	if e.Type != "" {
		return fmt.Sprintf("voyage: %s (%d): %s", e.Type, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("voyage: (%d): %s", e.StatusCode, e.Message)
}

func (e *VoyageError) Temporary() bool {
	return e.StatusCode == http.StatusTooManyRequests ||
		e.StatusCode >= http.StatusInternalServerError
}
