package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
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
	voyageAPIURL             = "https://api.voyageai.com/v1/embeddings"
	voyageCode3Dim           = 1024
	voyage35Dim              = 1024
	voyage35LiteDim          = 512
	voyageCode3MaxTokens     = 16384 // Voyage API docs: voyage-code-3 context window.
	voyage35MaxTokens        = 32000 // Voyage API docs: voyage-3.5 context window.
	voyage35LiteMaxTokens    = 32000 // Voyage API docs: voyage-3.5-lite context window.
	voyageDefaultRetries     = 20
	voyageRetryBackoff       = 100 * time.Millisecond
	voyageMaxBackoff         = 30 * time.Second
	voyageJitterFactor       = 0.3
	voyageDefaultTimeout     = 15 * time.Second
	voyageCharsPerToken      = 4
	voyageExpectedLatencySec = 10

	VoyageDefaultBatchSize = 128
	VoyageRequestsPerMin   = 2000
	VoyageTokensPerMin     = 8_000_000

	voyageDefaultBatchSize = VoyageDefaultBatchSize
	voyageRequestsPerMin   = VoyageRequestsPerMin
	voyageTokensPerMin     = VoyageTokensPerMin
)

type VoyageConfig struct {
	APIKey         string
	Model          VoyageModel
	InputType      VoyageInputType
	Timeout        time.Duration
	MaxRetries     int
	BatchSize      int
	Concurrency    int
	BaseURL        string
	MaxInputTokens int // If 0, derived from Model. Set explicitly for new/custom models.
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
	Data  []voyageEmbeddingData `json:"data"`
	Model string                `json:"model"`
	Usage voyageUsage           `json:"usage"`
}

type voyageEmbeddingData struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
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

type tokenBucket struct {
	tokens   chan struct{}
	stop     chan struct{}
	capacity int
}

func newTokenBucket(rate int, capacity int) *tokenBucket {
	tb := &tokenBucket{
		tokens:   make(chan struct{}, capacity),
		stop:     make(chan struct{}),
		capacity: capacity,
	}

	interval := time.Second / time.Duration(rate)
	go tb.refiller(interval)

	return tb
}

func (tb *tokenBucket) refiller(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-tb.stop:
			return
		case <-ticker.C:
			select {
			case tb.tokens <- struct{}{}:
			default:
			}
		}
	}
}

func (tb *tokenBucket) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tb.tokens:
		return nil
	}
}

func (tb *tokenBucket) Close() {
	close(tb.stop)
}

type VoyageEmbedder struct {
	config         VoyageConfig
	client         *http.Client
	dimension      int
	maxInputTokens int
	baseURL        string

	limiter *tokenBucket

	workQueue chan workItem
	workerWg  sync.WaitGroup

	mu         sync.RWMutex
	totalCalls int64
	total429s  int64
	lastError  error
}

type workItem struct {
	ctx      context.Context
	texts    []string
	startIdx int
	resultCh chan<- workResult
}

type workResult struct {
	startIdx   int
	embeddings [][]float32
	err        error
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

	if cfg.Concurrency == 0 {
		cfg.Concurrency = 1
	}

	dim := dimensionForModel(cfg.Model)
	if dim == 0 {
		return nil, fmt.Errorf("voyage: unsupported model %q", cfg.Model)
	}

	maxTokens := cfg.MaxInputTokens
	if maxTokens == 0 {
		maxTokens = maxInputTokensForModel(cfg.Model)
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = voyageAPIURL
	}

	numWorkers := voyageRequestsPerMin / 60
	connPoolSize := numWorkers * 2

	transport := &http.Transport{
		MaxIdleConns:        connPoolSize,
		MaxIdleConnsPerHost: connPoolSize,
		MaxConnsPerHost:     connPoolSize,
		IdleConnTimeout:     90 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	rate := voyageRequestsPerMin / 60
	limiter := newTokenBucket(rate, rate)

	v := &VoyageEmbedder{
		config:         cfg,
		client:         &http.Client{Timeout: cfg.Timeout, Transport: transport},
		dimension:      dim,
		maxInputTokens: maxTokens,
		baseURL:        baseURL,
		limiter:        limiter,
		workQueue:      make(chan workItem, numWorkers*2),
	}

	for range numWorkers {
		v.workerWg.Add(1)
		go v.worker()
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

	batchSize := v.config.BatchSize
	numRequests := (len(texts) + batchSize - 1) / batchSize
	results := make([][]float32, len(texts))
	resultCh := make(chan workResult, numRequests)

	for i := 0; i < len(texts); i += batchSize {
		end := min(i+batchSize, len(texts))
		v.workQueue <- workItem{
			ctx:      ctx,
			texts:    texts[i:end],
			startIdx: i,
			resultCh: resultCh,
		}
	}

	var firstErr error
	for range numRequests {
		res := <-resultCh
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
			continue
		}
		copy(results[res.startIdx:], res.embeddings)
	}

	return results, firstErr
}

func (v *VoyageEmbedder) worker() {
	defer v.workerWg.Done()
	for work := range v.workQueue {
		work.resultCh <- v.processWork(work)
	}
}

func (v *VoyageEmbedder) processWork(work workItem) workResult {
	if err := v.limiter.Wait(work.ctx); err != nil {
		return workResult{startIdx: work.startIdx, err: err}
	}

	var lastErr error
	for attempt := range v.config.MaxRetries {
		embeddings, err := v.doRequest(work.ctx, work.texts)
		if err == nil {
			return workResult{startIdx: work.startIdx, embeddings: embeddings}
		}

		lastErr = err
		if is429(err) {
			v.mu.Lock()
			v.total429s++
			v.mu.Unlock()
		}
		if !isRetryableError(err) {
			break
		}

		backoff := calculateBackoff(err, attempt)
		select {
		case <-work.ctx.Done():
			return workResult{startIdx: work.startIdx, err: work.ctx.Err()}
		case <-time.After(backoff):
		}

		if err := v.limiter.Wait(work.ctx); err != nil {
			return workResult{startIdx: work.startIdx, err: err}
		}
	}

	v.mu.Lock()
	v.lastError = lastErr
	v.mu.Unlock()

	return workResult{startIdx: work.startIdx, err: lastErr}
}

func (v *VoyageEmbedder) Dimension() int {
	return v.dimension
}

func (v *VoyageEmbedder) MaxInputBytes() int {
	return v.maxInputTokens * voyageCharsPerToken
}

func (v *VoyageEmbedder) Close() error {
	v.limiter.Close()
	close(v.workQueue)
	v.workerWg.Wait()
	return nil
}

func (v *VoyageEmbedder) Stats() (calls, rateLimits int64, lastErr error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.totalCalls, v.total429s, v.lastError
}

func is429(err error) bool {
	var voyageErr *VoyageError
	if errors.As(err, &voyageErr) {
		return voyageErr.StatusCode == http.StatusTooManyRequests
	}
	return false
}

func calculateBackoff(err error, attempt int) time.Duration {
	var voyageErr *VoyageError
	if errors.As(err, &voyageErr) && voyageErr.RetryAfter > 0 {
		return addJitter(voyageErr.RetryAfter)
	}

	backoff := voyageRetryBackoff * time.Duration(1<<attempt)
	if backoff > voyageMaxBackoff {
		backoff = voyageMaxBackoff
	}

	return addJitter(backoff)
}

func addJitter(d time.Duration) time.Duration {
	jitter := time.Duration(float64(d) * voyageJitterFactor * rand.Float64())
	return d + jitter
}

func (v *VoyageEmbedder) doRequest(ctx context.Context, texts []string) ([][]float32, error) {
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
		return nil, parseVoyageError(resp.StatusCode, respBody, resp.Header)
	}

	var voyageResp voyageResponse
	if err := json.Unmarshal(respBody, &voyageResp); err != nil {
		return nil, fmt.Errorf("voyage: unmarshal response: %w", err)
	}

	if len(voyageResp.Data) != len(texts) {
		return nil, fmt.Errorf("voyage: expected %d embeddings, got %d", len(texts), len(voyageResp.Data))
	}

	embeddings := make([][]float32, len(voyageResp.Data))
	for i, d := range voyageResp.Data {
		embeddings[i] = d.Embedding
	}

	return embeddings, nil
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

func maxInputTokensForModel(model VoyageModel) int {
	switch model {
	case VoyageCode3:
		return voyageCode3MaxTokens
	case Voyage35:
		return voyage35MaxTokens
	case Voyage35Lite:
		return voyage35LiteMaxTokens
	default:
		return 0
	}
}

func tokensPerMinForModel(model VoyageModel) int {
	switch model {
	case VoyageCode3:
		return 3_000_000
	case Voyage35:
		return 8_000_000
	case Voyage35Lite:
		return 16_000_000
	default:
		return voyageTokensPerMin
	}
}

func parseVoyageError(statusCode int, body []byte, headers http.Header) error {
	var retryAfter time.Duration
	if ra := headers.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			retryAfter = time.Duration(secs) * time.Second
		}
	}

	var errResp voyageErrorResponse
	if err := json.Unmarshal(body, &errResp); err != nil {
		return &VoyageError{
			StatusCode: statusCode,
			Message:    string(body),
			RetryAfter: retryAfter,
		}
	}

	return &VoyageError{
		StatusCode: statusCode,
		Message:    errResp.Error.Message,
		Type:       errResp.Error.Type,
		Code:       errResp.Error.Code,
		RetryAfter: retryAfter,
	}
}

func isRetryableError(err error) bool {
	var voyageErr *VoyageError
	if errors.As(err, &voyageErr) {
		return voyageErr.StatusCode == http.StatusTooManyRequests ||
			voyageErr.StatusCode >= http.StatusInternalServerError
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	return false
}

type VoyageError struct {
	StatusCode int
	Message    string
	Type       string
	Code       string
	RetryAfter time.Duration
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

func VerifyVoyageAPIKey(ctx context.Context, apiKey string) error {
	return verifyVoyageAPIKeyWithURL(ctx, apiKey, "")
}

func verifyVoyageAPIKeyWithURL(ctx context.Context, apiKey, baseURL string) error {
	if apiKey == "" {
		return errors.New("voyage: API key is empty")
	}

	cfg := VoyageConfig{
		APIKey:     apiKey,
		Model:      VoyageCode3,
		Timeout:    10 * time.Second,
		MaxRetries: 1,
		BatchSize:  1,
		BaseURL:    baseURL,
	}

	embedder, err := NewVoyageEmbedder(cfg)
	if err != nil {
		return fmt.Errorf("voyage: failed to create embedder: %w", err)
	}
	defer embedder.Close()

	_, err = embedder.Embed(ctx, "test")
	if err != nil {
		var voyageErr *VoyageError
		if errors.As(err, &voyageErr) {
			if voyageErr.StatusCode == http.StatusUnauthorized {
				return fmt.Errorf("voyage: invalid API key")
			}
		}
		return fmt.Errorf("voyage: verification failed: %w", err)
	}

	return nil
}
