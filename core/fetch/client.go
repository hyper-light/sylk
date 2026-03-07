package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// FetchResult is the raw result of an HTTP fetch before quarantine.
type FetchResult struct {
	URL         string            `json:"url"`
	Domain      string            `json:"domain"`
	StatusCode  int               `json:"status_code"`
	ContentType string            `json:"content_type"`
	Content     []byte            `json:"-"`
	ContentHash string            `json:"content_hash"`
	Headers     map[string]string `json:"headers,omitempty"`
	FetchedAt   time.Time         `json:"fetched_at"`
	Duration    time.Duration     `json:"duration"`
	BytesRead   int64             `json:"bytes_read"`
	TLSFindings []TLSFinding      `json:"tls_findings,omitempty"`
}

// Client performs HTTP fetches bounded by the policy's size limits and
// timeouts. It does NOT evaluate policy or consent — that is the caller's
// responsibility (enforced by the Pipeline).
type Client struct {
	httpClient   *http.Client
	maxBytes     int64
	userAgent    string
	tlsValidator *TLSValidator
}

// ClientConfig configures the fetch client.
type ClientConfig struct {
	MaxBytes       int64         // Max response body size (from policy)
	RequestTimeout time.Duration // Per-request timeout (default: 30s)
	UserAgent      string        // User-Agent header (default: "Sylk-Academic/1.0")
}

// NewClient creates a fetch client.
func NewClient(cfg ClientConfig) *Client {
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 10 * 1024 * 1024
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "Sylk-Academic/1.0"
	}

	return &Client{
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects (max 5)")
				}
				return nil
			},
		},
		maxBytes:     cfg.MaxBytes,
		userAgent:    cfg.UserAgent,
		tlsValidator: NewTLSValidator(),
	}
}

// Fetch downloads content from the given URL. The response body is bounded
// by maxBytes; reads beyond the limit return an error. The caller must have
// already evaluated policy and consent.
func (c *Client) Fetch(ctx context.Context, rawURL string) (*FetchResult, error) {
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "text/html, application/pdf, text/plain, application/json, text/markdown, */*;q=0.1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	// TLS certificate validation — runs before reading body.
	// Critical findings (hostname mismatch) abort the fetch immediately.
	var tlsFindings []TLSFinding
	if resp.TLS != nil && c.tlsValidator != nil {
		hostname := req.URL.Hostname()
		tlsFindings = c.tlsValidator.Validate(resp.TLS, hostname)
		if MaxSeverity(tlsFindings) >= TLSSeverityCritical {
			return nil, fmt.Errorf("TLS validation failed for %s: %s", hostname, tlsFindings[0].Issue)
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("fetch %s: HTTP %d %s", rawURL, resp.StatusCode, resp.Status)
	}

	// Size-bounded read.
	limitedReader := io.LimitReader(resp.Body, c.maxBytes+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > c.maxBytes {
		return nil, fmt.Errorf("response exceeds max size (%d bytes)", c.maxBytes)
	}

	hash := sha256.Sum256(body)
	contentType := resp.Header.Get("Content-Type")
	// Normalize content type (strip parameters like charset).
	if idx := strings.Index(contentType, ";"); idx >= 0 {
		contentType = strings.TrimSpace(contentType[:idx])
	}

	domain := req.URL.Hostname()

	headers := make(map[string]string, 4)
	for _, key := range []string{"Content-Type", "Last-Modified", "ETag", "X-Robots-Tag"} {
		if v := resp.Header.Get(key); v != "" {
			headers[key] = v
		}
	}

	return &FetchResult{
		URL:         rawURL,
		Domain:      domain,
		StatusCode:  resp.StatusCode,
		ContentType: contentType,
		Content:     body,
		ContentHash: hex.EncodeToString(hash[:]),
		Headers:     headers,
		FetchedAt:   start,
		Duration:    time.Since(start),
		BytesRead:   int64(len(body)),
		TLSFindings: tlsFindings,
	}, nil
}

// ToQuarantineEntry converts a FetchResult into a QuarantineEntry for
// Guardian inspection.
func (r *FetchResult) ToQuarantineEntry(sourceAgent, consentID string) *QuarantineEntry {
	return &QuarantineEntry{
		ID:            "q_" + uuid.NewString()[:8],
		URL:           r.URL,
		Domain:        r.Domain,
		ContentType:   r.ContentType,
		Content:       r.Content,
		ContentHash:   r.ContentHash,
		ContentLength: len(r.Content),
		FetchedAt:     r.FetchedAt,
		SourceAgent:   sourceAgent,
		ConsentID:     consentID,
		Metadata:      r.Headers,
	}
}
