package fetch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ConsentDecision is the outcome of a consent check.
type ConsentDecision int

const (
	ConsentRequired  ConsentDecision = iota // Must ask the user
	ConsentGranted                          // Auto-approved by session config or prior consent
	ConsentDenied                           // Explicitly denied by user or config
	ConsentRevoked                          // Was granted, then revoked
)

// FetchProposal describes a pending fetch for user approval.
type FetchProposal struct {
	ID              string         `json:"id"`
	URL             string         `json:"url"`
	Domain          string         `json:"domain"`
	ContentType     string         `json:"content_type"`     // Predicted or detected
	EstimatedBytes  int64          `json:"estimated_bytes"`   // 0 if unknown
	SourceAgent     string         `json:"source_agent"`
	Reason          string         `json:"reason"`
	RiskAssessment  string         `json:"risk_assessment"`  // From policy evaluation
	CorrelationID   string         `json:"correlation_id"`
	Timestamp       time.Time      `json:"timestamp"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

// ConsentResult carries the user's decision.
type ConsentResult struct {
	Granted      bool   `json:"granted"`
	Reason       string `json:"reason,omitempty"`
	GrantScope   string `json:"grant_scope,omitempty"` // "once", "domain", "session"
	GrantDomain  string `json:"grant_domain,omitempty"`
}

// ConsentCallback is invoked to request user approval. Implementations
// must block until the user responds or the context is cancelled.
type ConsentCallback func(ctx context.Context, proposal *FetchProposal) (*ConsentResult, error)

// ConsentGate manages user consent for external fetches. It maintains a
// session-scoped ledger of granted and revoked consents, and supports
// auto-approval via configuration.
type ConsentGate struct {
	mu             sync.RWMutex
	autoApprove    map[string]struct{} // Domain patterns auto-approved by config
	sessionGrants  map[string]grantEntry // Domain → grant info
	callback       ConsentCallback
	consentTimeout time.Duration
}

type grantEntry struct {
	grantedAt time.Time
	reason    string
	revoked   bool
}

// ConsentGateConfig configures the consent gate.
type ConsentGateConfig struct {
	// AutoApproveDomains lists domain patterns that skip user consent.
	// Supports wildcards like "*.golang.org". Empty means all fetches
	// require consent.
	AutoApproveDomains []string

	// Callback is invoked when user consent is needed. Must not be nil
	// unless all domains are auto-approved.
	Callback ConsentCallback

	// ConsentTimeout bounds how long to wait for user response.
	// Default: 2 minutes.
	ConsentTimeout time.Duration
}

// NewConsentGate creates a consent gate from config.
func NewConsentGate(cfg ConsentGateConfig) *ConsentGate {
	timeout := cfg.ConsentTimeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}

	auto := make(map[string]struct{}, len(cfg.AutoApproveDomains))
	for _, d := range cfg.AutoApproveDomains {
		auto[strings.ToLower(d)] = struct{}{}
	}

	return &ConsentGate{
		autoApprove:    auto,
		sessionGrants:  make(map[string]grantEntry),
		callback:       cfg.Callback,
		consentTimeout: timeout,
	}
}

// Check evaluates whether a fetch to the given domain is pre-approved.
// Returns ConsentRequired if the user must be asked.
func (cg *ConsentGate) Check(domain string) ConsentDecision {
	lower := strings.ToLower(domain)

	// Auto-approve by config.
	if cg.isAutoApproved(lower) {
		return ConsentGranted
	}

	cg.mu.RLock()
	defer cg.mu.RUnlock()

	// Check session grants.
	grant, ok := cg.sessionGrants[lower]
	if !ok {
		return ConsentRequired
	}
	if grant.revoked {
		return ConsentRevoked
	}
	return ConsentGranted
}

// RequestConsent asks the user for approval and records the decision.
// Blocks until the user responds, the timeout elapses, or ctx is cancelled.
func (cg *ConsentGate) RequestConsent(ctx context.Context, proposal *FetchProposal) (*ConsentResult, error) {
	if cg.callback == nil {
		return nil, fmt.Errorf("no consent callback configured — cannot request user approval")
	}

	ctx, cancel := context.WithTimeout(ctx, cg.consentTimeout)
	defer cancel()

	result, err := cg.callback(ctx, proposal)
	if err != nil {
		return nil, fmt.Errorf("consent request failed: %w", err)
	}

	if result.Granted {
		cg.recordGrant(proposal.Domain, result)
	}

	return result, nil
}

// Revoke revokes a previously granted domain consent.
func (cg *ConsentGate) Revoke(domain string) {
	lower := strings.ToLower(domain)
	cg.mu.Lock()
	defer cg.mu.Unlock()

	if entry, ok := cg.sessionGrants[lower]; ok {
		entry.revoked = true
		cg.sessionGrants[lower] = entry
	}
}

// GrantedDomains returns all currently granted (non-revoked) domains.
func (cg *ConsentGate) GrantedDomains() []string {
	cg.mu.RLock()
	defer cg.mu.RUnlock()

	domains := make([]string, 0, len(cg.sessionGrants))
	for domain, grant := range cg.sessionGrants {
		if !grant.revoked {
			domains = append(domains, domain)
		}
	}
	return domains
}

func (cg *ConsentGate) recordGrant(domain string, result *ConsentResult) {
	lower := strings.ToLower(domain)

	cg.mu.Lock()
	defer cg.mu.Unlock()

	switch result.GrantScope {
	case "domain":
		// Grant for the entire domain.
		cg.sessionGrants[lower] = grantEntry{
			grantedAt: time.Now(),
			reason:    result.Reason,
		}
	case "session":
		// Grant for any domain (session-wide).
		cg.sessionGrants["*"] = grantEntry{
			grantedAt: time.Now(),
			reason:    result.Reason,
		}
	default:
		// "once" — no persistent grant recorded.
	}
}

func (cg *ConsentGate) isAutoApproved(domain string) bool {
	if _, ok := cg.autoApprove["*"]; ok {
		return true
	}
	if _, ok := cg.autoApprove[domain]; ok {
		return true
	}
	for pattern := range cg.autoApprove {
		if matchDomainPattern(pattern, domain) {
			return true
		}
	}

	// Check session-wide grant.
	cg.mu.RLock()
	defer cg.mu.RUnlock()
	if grant, ok := cg.sessionGrants["*"]; ok && !grant.revoked {
		return true
	}
	return false
}
