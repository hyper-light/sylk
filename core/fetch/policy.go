// Package fetch provides a security-gated HTTP fetch pipeline for external
// resource retrieval. Every fetch request passes through:
//
//  1. SecurityContext capability check (ActionNetworkEgress)
//  2. FetchPolicy evaluation (SSRF, domain, size, rate, TLS)
//  3. User consent gate (interactive approval or config override)
//  4. Quarantine + Guardian inspection before ingestion
package fetch

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

// PolicyVerdict is the outcome of a policy evaluation.
type PolicyVerdict int

const (
	VerdictAllow PolicyVerdict = iota
	VerdictDeny
)

// PolicyResult carries the verdict and the reason for denial.
type PolicyResult struct {
	Verdict PolicyVerdict
	Reason  string
	Domain  string
}

// FetchPolicy evaluates URL requests against security rules before any
// network I/O occurs. All checks are deterministic and non-LLM.
type FetchPolicy struct {
	mu             sync.RWMutex
	domainDenylist map[string]struct{}
	domainAllow    map[string]struct{} // If non-empty, only these domains are allowed
	maxBytes       int64               // Max download size in bytes
	requireTLS     bool                // Reject non-HTTPS URLs
	rateLimiter    *domainRateLimiter
}

// PolicyConfig configures the fetch policy.
type PolicyConfig struct {
	DomainDenylist []string // Domains to block (exact or wildcard "*.evil.com")
	DomainAllow    []string // If set, only these domains are allowed
	MaxBytes       int64    // Max download size (default: 10MB)
	RequireTLS     bool     // Require HTTPS (default: true)
	RatePerDomain  int     // Max requests per domain per minute (default: 30)
	RateGlobal     int     // Max total requests per minute (default: 120)
}

// DefaultPolicyConfig returns safe defaults.
func DefaultPolicyConfig() PolicyConfig {
	return PolicyConfig{
		MaxBytes:      10 * 1024 * 1024, // 10MB
		RequireTLS:    true,
		RatePerDomain: 30,
		RateGlobal:    120,
	}
}

// NewFetchPolicy creates a policy from config.
func NewFetchPolicy(cfg PolicyConfig) *FetchPolicy {
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 10 * 1024 * 1024
	}
	if cfg.RatePerDomain == 0 {
		cfg.RatePerDomain = 30
	}
	if cfg.RateGlobal == 0 {
		cfg.RateGlobal = 120
	}

	return &FetchPolicy{
		domainDenylist: toSet(cfg.DomainDenylist),
		domainAllow:    toSet(cfg.DomainAllow),
		maxBytes:       cfg.MaxBytes,
		requireTLS:     cfg.RequireTLS,
		rateLimiter:    newDomainRateLimiter(cfg.RatePerDomain, cfg.RateGlobal),
	}
}

// Evaluate checks a URL against all policy rules. Returns VerdictDeny with
// a reason on the first failing check.
func (p *FetchPolicy) Evaluate(rawURL string) PolicyResult {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return PolicyResult{Verdict: VerdictDeny, Reason: fmt.Sprintf("invalid URL: %v", err)}
	}

	domain := strings.ToLower(parsed.Hostname())
	if domain == "" {
		return PolicyResult{Verdict: VerdictDeny, Reason: "empty hostname"}
	}

	// Scheme check.
	if p.requireTLS && parsed.Scheme != "https" {
		return PolicyResult{Verdict: VerdictDeny, Reason: "TLS required: scheme is " + parsed.Scheme, Domain: domain}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return PolicyResult{Verdict: VerdictDeny, Reason: "unsupported scheme: " + parsed.Scheme, Domain: domain}
	}

	// SSRF check.
	if reason, blocked := checkSSRF(domain); blocked {
		return PolicyResult{Verdict: VerdictDeny, Reason: reason, Domain: domain}
	}

	// Domain denylist.
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.isDenied(domain) {
		return PolicyResult{Verdict: VerdictDeny, Reason: "domain denied by policy: " + domain, Domain: domain}
	}

	// Domain allowlist (if configured).
	if len(p.domainAllow) > 0 && !p.isAllowed(domain) {
		return PolicyResult{Verdict: VerdictDeny, Reason: "domain not in allowlist: " + domain, Domain: domain}
	}

	// Rate limit.
	if !p.rateLimiter.allow(domain) {
		return PolicyResult{Verdict: VerdictDeny, Reason: "rate limit exceeded for domain: " + domain, Domain: domain}
	}

	return PolicyResult{Verdict: VerdictAllow, Domain: domain}
}

// MaxBytes returns the configured max download size.
func (p *FetchPolicy) MaxBytes() int64 {
	return p.maxBytes
}

// AddDenyDomain adds a domain to the denylist at runtime.
func (p *FetchPolicy) AddDenyDomain(domain string) {
	p.mu.Lock()
	p.domainDenylist[strings.ToLower(domain)] = struct{}{}
	p.mu.Unlock()
}

func (p *FetchPolicy) isDenied(domain string) bool {
	if _, ok := p.domainDenylist[domain]; ok {
		return true
	}
	// Check wildcard patterns.
	for pattern := range p.domainDenylist {
		if matchDomainPattern(pattern, domain) {
			return true
		}
	}
	return false
}

func (p *FetchPolicy) isAllowed(domain string) bool {
	if _, ok := p.domainAllow[domain]; ok {
		return true
	}
	for pattern := range p.domainAllow {
		if matchDomainPattern(pattern, domain) {
			return true
		}
	}
	return false
}

// matchDomainPattern checks if domain matches a "*.suffix" pattern.
func matchDomainPattern(pattern, domain string) bool {
	if len(pattern) < 3 || pattern[0] != '*' || pattern[1] != '.' {
		return false
	}
	suffix := pattern[1:] // ".example.com"
	return len(domain) > len(suffix) && domain[len(domain)-len(suffix):] == suffix
}

// =============================================================================
// SSRF Protection
// =============================================================================

// checkSSRF blocks requests to internal/reserved IP ranges and cloud metadata
// endpoints. Resolves the hostname to check the actual IP addresses.
func checkSSRF(host string) (string, bool) {
	// Block known metadata endpoints by hostname.
	if isMetadataHost(host) {
		return "SSRF: cloud metadata endpoint blocked", true
	}

	// Check if the host is an IP literal.
	if ip := net.ParseIP(host); ip != nil {
		if reason, blocked := checkIPReserved(ip); blocked {
			return reason, true
		}
		return "", false
	}

	// Resolve hostname and check all addresses.
	addrs, err := net.LookupHost(host)
	if err != nil {
		// DNS resolution failure — allow the fetch to proceed and fail
		// at the HTTP level with a proper error message.
		return "", false
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if reason, blocked := checkIPReserved(ip); blocked {
			return fmt.Sprintf("SSRF: %s resolves to reserved IP %s — %s", host, addr, reason), true
		}
	}
	return "", false
}

// checkIPReserved returns true for any IP in a reserved/private/loopback range.
func checkIPReserved(ip net.IP) (string, bool) {
	for _, entry := range reservedRanges {
		if entry.network.Contains(ip) {
			return "reserved range: " + entry.name, true
		}
	}
	return "", false
}

type reservedEntry struct {
	network *net.IPNet
	name    string
}

// reservedRanges covers all IANA-reserved and link-local ranges.
var reservedRanges = func() []reservedEntry {
	cidrs := []struct {
		cidr string
		name string
	}{
		{"127.0.0.0/8", "IPv4 loopback"},
		{"10.0.0.0/8", "RFC 1918 private"},
		{"172.16.0.0/12", "RFC 1918 private"},
		{"192.168.0.0/16", "RFC 1918 private"},
		{"169.254.0.0/16", "IPv4 link-local"},
		{"100.64.0.0/10", "carrier-grade NAT"},
		{"0.0.0.0/8", "this network"},
		{"192.0.0.0/24", "IETF protocol assignments"},
		{"192.0.2.0/24", "documentation (TEST-NET-1)"},
		{"198.51.100.0/24", "documentation (TEST-NET-2)"},
		{"203.0.113.0/24", "documentation (TEST-NET-3)"},
		{"224.0.0.0/4", "multicast"},
		{"240.0.0.0/4", "reserved"},
		{"::1/128", "IPv6 loopback"},
		{"fc00::/7", "IPv6 unique local"},
		{"fe80::/10", "IPv6 link-local"},
		{"ff00::/8", "IPv6 multicast"},
		{"::ffff:127.0.0.0/104", "IPv4-mapped loopback"},
		{"::ffff:10.0.0.0/104", "IPv4-mapped private"},
		{"::ffff:172.16.0.0/108", "IPv4-mapped private"},
		{"::ffff:192.168.0.0/112", "IPv4-mapped private"},
		{"::ffff:169.254.0.0/112", "IPv4-mapped link-local"},
	}

	entries := make([]reservedEntry, 0, len(cidrs))
	for _, c := range cidrs {
		_, network, err := net.ParseCIDR(c.cidr)
		if err != nil {
			continue
		}
		entries = append(entries, reservedEntry{network: network, name: c.name})
	}
	return entries
}()

// isMetadataHost checks for known cloud provider metadata hostnames/IPs.
func isMetadataHost(host string) bool {
	lower := strings.ToLower(host)
	for _, m := range metadataHosts {
		if lower == m {
			return true
		}
	}
	return false
}

var metadataHosts = []string{
	"169.254.169.254",         // AWS, GCP, Azure
	"metadata.google.internal", // GCP
	"metadata.internal",       // GCP alt
	"100.100.100.200",         // Alibaba Cloud
	"169.254.170.2",           // AWS ECS task metadata
}

// =============================================================================
// Rate Limiter
// =============================================================================

type domainRateLimiter struct {
	mu            sync.Mutex
	perDomainMax  int
	globalMax     int
	domainWindows map[string]*slidingWindow
	globalWindow  *slidingWindow
}

type slidingWindow struct {
	timestamps []time.Time
	maxPerMin  int
}

func newSlidingWindow(maxPerMin int) *slidingWindow {
	return &slidingWindow{
		timestamps: make([]time.Time, 0, maxPerMin),
		maxPerMin:  maxPerMin,
	}
}

func (w *slidingWindow) allow(now time.Time) bool {
	cutoff := now.Add(-time.Minute)
	// Compact expired entries.
	valid := 0
	for _, t := range w.timestamps {
		if t.After(cutoff) {
			w.timestamps[valid] = t
			valid++
		}
	}
	w.timestamps = w.timestamps[:valid]

	if len(w.timestamps) >= w.maxPerMin {
		return false
	}
	w.timestamps = append(w.timestamps, now)
	return true
}

func newDomainRateLimiter(perDomain, global int) *domainRateLimiter {
	return &domainRateLimiter{
		perDomainMax:  perDomain,
		globalMax:     global,
		domainWindows: make(map[string]*slidingWindow),
		globalWindow:  newSlidingWindow(global),
	}
}

func (r *domainRateLimiter) allow(domain string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()

	// Global limit.
	if !r.globalWindow.allow(now) {
		return false
	}

	// Per-domain limit.
	w, ok := r.domainWindows[domain]
	if !ok {
		w = newSlidingWindow(r.perDomainMax)
		r.domainWindows[domain] = w
	}
	return w.allow(now)
}

// =============================================================================
// Helpers
// =============================================================================

func toSet(items []string) map[string]struct{} {
	s := make(map[string]struct{}, len(items))
	for _, item := range items {
		s[strings.ToLower(item)] = struct{}{}
	}
	return s
}
