package fetcher

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/substrate/recipe"
)

// HTTPSFetcher fetches bytes over HTTPS.
//
// HTTP without TLS is deliberately not supported. Supply-chain
// integrity demands in-transit confidentiality and integrity for any
// substrate-bound content; allowing http:// would let a network
// attacker silently substitute different bytes that the recipe's hash
// pin would then certify as canonical.
type HTTPSFetcher struct {
	client *http.Client
}

// HTTPSConfig configures the HTTPS fetcher's transport.
type HTTPSConfig struct {
	// RequestTimeout is the per-request wall-clock cap. Zero defaults
	// to defaultHTTPSTimeout.
	RequestTimeout time.Duration

	// MinTLSVersion is the lowest TLS version accepted. Default
	// tls.VersionTLS12. The substrate refuses to negotiate weaker.
	MinTLSVersion uint16

	// InsecureSkipVerify disables certificate verification. Intended
	// only for tests against local fixtures (e.g. httptest.Server with
	// self-signed certs). Production substrate code must never set
	// this true.
	InsecureSkipVerify bool

	// UserAgent overrides the User-Agent header. Empty means use the
	// substrate's default identifier.
	UserAgent string
}

// DefaultHTTPSConfig returns sensible defaults for production use.
func DefaultHTTPSConfig() HTTPSConfig {
	return HTTPSConfig{
		RequestTimeout: defaultHTTPSTimeout,
		MinTLSVersion:  tls.VersionTLS12,
		UserAgent:      defaultUserAgent,
	}
}

// NewHTTPSFetcher returns an HTTPSFetcher with the given configuration.
func NewHTTPSFetcher(cfg HTTPSConfig) *HTTPSFetcher {
	transport := buildTransport(cfg)
	client := &http.Client{
		Transport: transport,
		Timeout:   resolveTimeout(cfg.RequestTimeout),
	}
	return &HTTPSFetcher{client: client}
}

func resolveTimeout(t time.Duration) time.Duration {
	if t == 0 {
		return defaultHTTPSTimeout
	}
	return t
}

func buildTransport(cfg HTTPSConfig) *http.Transport {
	tlsCfg := &tls.Config{
		MinVersion:         resolveMinTLS(cfg.MinTLSVersion),
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	}
	return &http.Transport{
		TLSClientConfig:       tlsCfg,
		MaxIdleConns:          defaultMaxIdleConns,
		IdleConnTimeout:       defaultIdleConnTimeout,
		TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
		ResponseHeaderTimeout: defaultResponseHeaderTimeout,
		ExpectContinueTimeout: defaultExpectContinueTimeout,
		ForceAttemptHTTP2:     true,
	}
}

func resolveMinTLS(v uint16) uint16 {
	if v == 0 {
		return tls.VersionTLS12
	}
	return v
}

// Scheme returns "https".
func (HTTPSFetcher) Scheme() string { return "https" }

// Fetch executes a GET against spec.Source and returns the body bytes.
func (h *HTTPSFetcher) Fetch(ctx context.Context, spec recipe.FetchSpec, opts FetchOptions) ([]byte, error) {
	req, err := h.buildRequest(ctx, spec)
	if err != nil {
		return nil, errFetch("https", spec.Source, err)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, errFetch("https", spec.Source, err)
	}
	defer resp.Body.Close()
	if err := requireOK(resp); err != nil {
		return nil, errFetch("https", spec.Source, err)
	}
	return readAllBounded(ctx, resp.Body, opts, "https", spec.Source)
}

func (h *HTTPSFetcher) buildRequest(ctx context.Context, spec recipe.FetchSpec) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.Source, nil)
	if err != nil {
		return nil, err
	}
	applyHeaders(req, spec.Headers)
	applyDefaultHeaders(req)
	return req, nil
}

func applyHeaders(req *http.Request, headers map[string]string) {
	for name, valueTemplate := range headers {
		req.Header.Set(name, expandEnvRefs(valueTemplate))
	}
}

func applyDefaultHeaders(req *http.Request) {
	if req.Header.Get(headerUserAgent) == "" {
		req.Header.Set(headerUserAgent, defaultUserAgent)
	}
	if req.Header.Get(headerAccept) == "" {
		req.Header.Set(headerAccept, "*/*")
	}
}

// expandEnvRefs expands ${VAR} references in a header value. Unresolved
// refs are left literal so the recipe author can spot the typo.
func expandEnvRefs(s string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	return os.Expand(s, lookupEnvOrLiteral)
}

func lookupEnvOrLiteral(name string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return "${" + name + "}"
}

func requireOK(resp *http.Response) error {
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("HTTP %d %s", resp.StatusCode, resp.Status)
}

const (
	defaultHTTPSTimeout          = 5 * time.Minute
	defaultMaxIdleConns          = 16
	defaultIdleConnTimeout       = 90 * time.Second
	defaultTLSHandshakeTimeout   = 10 * time.Second
	defaultResponseHeaderTimeout = 30 * time.Second
	defaultExpectContinueTimeout = 1 * time.Second

	headerUserAgent = "User-Agent"
	headerAccept    = "Accept"

	defaultUserAgent = "sylk-substrate/0.1"
)
