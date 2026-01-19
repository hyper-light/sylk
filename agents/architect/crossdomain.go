package architect

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/domain"
)

// DomainResult represents the result from a single domain query.
type DomainResult struct {
	Domain      domain.Domain `json:"domain"`
	Query       string        `json:"query"`
	Content     string        `json:"content"`
	Score       float64       `json:"score"`
	Source      string        `json:"source"`
	Took        time.Duration `json:"took"`
	Error       error         `json:"-"`
	ErrorMsg    string        `json:"error,omitempty"`
	RetrievedAt time.Time     `json:"retrieved_at"`
}

// CrossDomainResult represents the synthesized result from multiple domains.
type CrossDomainResult struct {
	Query           string                   `json:"query"`
	DomainResults   []DomainResult           `json:"domain_results"`
	SynthesizedText string                   `json:"synthesized_text,omitempty"`
	SourceMap       map[string]domain.Domain `json:"source_map"`
	TotalDomains    int                      `json:"total_domains"`
	SuccessDomains  int                      `json:"success_domains"`
	FailedDomains   int                      `json:"failed_domains"`
	IsCrossDomain   bool                     `json:"is_cross_domain"`
	Took            time.Duration            `json:"took"`
	CompletedAt     time.Time                `json:"completed_at"`
}

// DomainQueryHandler is the function signature for domain-specific query handlers.
type DomainQueryHandler func(ctx context.Context, d domain.Domain, query string) (*DomainResult, error)

// CrossDomainHandler coordinates multi-domain queries for the Architect agent.
type CrossDomainHandler struct {
	timeout       time.Duration
	maxConcurrent int
	logger        *slog.Logger
	queryHandler  DomainQueryHandler
}

// CrossDomainHandlerConfig configures the CrossDomainHandler.
type CrossDomainHandlerConfig struct {
	Timeout       time.Duration
	MaxConcurrent int
	Logger        *slog.Logger
	QueryHandler  DomainQueryHandler
}

// NewCrossDomainHandler creates a new cross-domain handler.
func NewCrossDomainHandler(config *CrossDomainHandlerConfig) *CrossDomainHandler {
	cfg := applyHandlerDefaults(config)
	return &CrossDomainHandler{
		timeout:       cfg.Timeout,
		maxConcurrent: cfg.MaxConcurrent,
		logger:        cfg.Logger,
		queryHandler:  cfg.QueryHandler,
	}
}

func applyHandlerDefaults(config *CrossDomainHandlerConfig) *CrossDomainHandlerConfig {
	defaults := &CrossDomainHandlerConfig{
		Timeout:       30 * time.Second,
		MaxConcurrent: 3,
		Logger:        slog.Default(),
		QueryHandler:  nil,
	}

	if config == nil {
		return defaults
	}

	if config.Timeout > 0 {
		defaults.Timeout = config.Timeout
	}
	if config.MaxConcurrent > 0 {
		defaults.MaxConcurrent = config.MaxConcurrent
	}
	if config.Logger != nil {
		defaults.Logger = config.Logger
	}
	if config.QueryHandler != nil {
		defaults.QueryHandler = config.QueryHandler
	}

	return defaults
}

// HandleCrossDomain processes a cross-domain query and returns synthesized results.
func (h *CrossDomainHandler) HandleCrossDomain(
	ctx context.Context,
	query string,
	domainCtx *domain.DomainContext,
) (*CrossDomainResult, error) {
	start := time.Now()

	if domainCtx == nil || domainCtx.IsEmpty() {
		return h.emptyResult(query, start), nil
	}

	domains := domainCtx.AllowedDomains()
	results := h.dispatchToAgents(ctx, query, domains)

	return h.buildResult(query, results, domainCtx, start), nil
}

func (h *CrossDomainHandler) emptyResult(query string, start time.Time) *CrossDomainResult {
	return &CrossDomainResult{
		Query:         query,
		DomainResults: []DomainResult{},
		SourceMap:     make(map[string]domain.Domain),
		Took:          time.Since(start),
		CompletedAt:   time.Now(),
	}
}

func (h *CrossDomainHandler) dispatchToAgents(
	ctx context.Context,
	query string,
	domains []domain.Domain,
) []DomainResult {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	resultChan := make(chan DomainResult, len(domains))
	sem := make(chan struct{}, h.maxConcurrent)

	var wg sync.WaitGroup
	for _, d := range domains {
		wg.Add(1)
		go h.queryDomain(ctx, &wg, sem, resultChan, d, query)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	return h.collectResults(resultChan)
}

func (h *CrossDomainHandler) queryDomain(
	ctx context.Context,
	wg *sync.WaitGroup,
	sem chan struct{},
	resultChan chan<- DomainResult,
	d domain.Domain,
	query string,
) {
	defer wg.Done()

	sem <- struct{}{}
	defer func() { <-sem }()

	result := h.executeDomainQuery(ctx, d, query)
	resultChan <- result
}

func (h *CrossDomainHandler) executeDomainQuery(
	ctx context.Context,
	d domain.Domain,
	query string,
) DomainResult {
	start := time.Now()

	if h.queryHandler == nil {
		return h.noHandlerResult(d, query, start)
	}

	result, err := h.queryHandler(ctx, d, query)
	if err != nil {
		return h.errorResult(d, query, err, start)
	}

	if result == nil {
		return h.emptyDomainResult(d, query, start)
	}

	result.Took = time.Since(start)
	result.RetrievedAt = time.Now()
	return *result
}

func (h *CrossDomainHandler) noHandlerResult(d domain.Domain, query string, start time.Time) DomainResult {
	return DomainResult{
		Domain:      d,
		Query:       query,
		ErrorMsg:    "no query handler configured",
		Took:        time.Since(start),
		RetrievedAt: time.Now(),
	}
}

func (h *CrossDomainHandler) errorResult(d domain.Domain, query string, err error, start time.Time) DomainResult {
	return DomainResult{
		Domain:      d,
		Query:       query,
		Error:       err,
		ErrorMsg:    err.Error(),
		Took:        time.Since(start),
		RetrievedAt: time.Now(),
	}
}

func (h *CrossDomainHandler) emptyDomainResult(d domain.Domain, query string, start time.Time) DomainResult {
	return DomainResult{
		Domain:      d,
		Query:       query,
		Took:        time.Since(start),
		RetrievedAt: time.Now(),
	}
}

func (h *CrossDomainHandler) collectResults(resultChan <-chan DomainResult) []DomainResult {
	results := make([]DomainResult, 0)
	for result := range resultChan {
		results = append(results, result)
	}
	return results
}

func (h *CrossDomainHandler) buildResult(
	query string,
	results []DomainResult,
	domainCtx *domain.DomainContext,
	start time.Time,
) *CrossDomainResult {
	successCount, failedCount := h.countResults(results)

	return &CrossDomainResult{
		Query:          query,
		DomainResults:  results,
		SourceMap:      h.buildSourceMap(results),
		TotalDomains:   len(results),
		SuccessDomains: successCount,
		FailedDomains:  failedCount,
		IsCrossDomain:  domainCtx.IsCrossDomain,
		Took:           time.Since(start),
		CompletedAt:    time.Now(),
	}
}

func (h *CrossDomainHandler) countResults(results []DomainResult) (success, failed int) {
	for _, r := range results {
		if r.Error != nil || r.ErrorMsg != "" {
			failed++
		} else {
			success++
		}
	}
	return
}

func (h *CrossDomainHandler) buildSourceMap(results []DomainResult) map[string]domain.Domain {
	sourceMap := make(map[string]domain.Domain)
	for _, r := range results {
		if r.Source != "" {
			sourceMap[r.Source] = r.Domain
		}
	}
	return sourceMap
}

// HasResults returns true if any domain returned results.
func (r *CrossDomainResult) HasResults() bool {
	return r.SuccessDomains > 0
}

// GetDomainResult returns the result for a specific domain.
func (r *CrossDomainResult) GetDomainResult(d domain.Domain) *DomainResult {
	for i := range r.DomainResults {
		if r.DomainResults[i].Domain == d {
			return &r.DomainResults[i]
		}
	}
	return nil
}

// SuccessfulResults returns only the results without errors.
func (r *CrossDomainResult) SuccessfulResults() []DomainResult {
	successful := make([]DomainResult, 0, r.SuccessDomains)
	for _, dr := range r.DomainResults {
		if dr.Error == nil && dr.ErrorMsg == "" {
			successful = append(successful, dr)
		}
	}
	return successful
}

// FailedResults returns only the results with errors.
func (r *CrossDomainResult) FailedResults() []DomainResult {
	failed := make([]DomainResult, 0, r.FailedDomains)
	for _, dr := range r.DomainResults {
		if dr.Error != nil || dr.ErrorMsg != "" {
			failed = append(failed, dr)
		}
	}
	return failed
}
