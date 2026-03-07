package guardian

import (
	"sync"
	"time"
)

// TrustLevel classifies a domain's accumulated trust within a session.
type TrustLevel int

const (
	TrustNew        TrustLevel = iota // First fetch, full inspection
	TrustKnown                        // Seen before, had findings
	TrustTrusted                      // Multiple clean fetches
	TrustSuspicious                   // Had high/critical findings
)

func (t TrustLevel) String() string {
	names := [...]string{"new", "known", "trusted", "suspicious"}
	if int(t) < len(names) {
		return names[t]
	}
	return "unknown"
}

// DomainReputation tracks a domain's security history within a session.
type DomainReputation struct {
	Domain       string     `json:"domain"`
	FirstSeen    time.Time  `json:"first_seen"`
	LastSeen     time.Time  `json:"last_seen"`
	FetchCount   int        `json:"fetch_count"`
	CleanCount   int        `json:"clean_count"`
	FindingCount int        `json:"finding_count"`
	MaxSeverity  Severity   `json:"max_severity"`
	TrustLevel   TrustLevel `json:"trust_level"`
	LastCertHash string     `json:"last_cert_hash,omitempty"`
}

// DomainReputationTracker maintains per-domain trust state for a session.
// Used by the Guardian to modulate inspection depth — trusted domains get
// lighter scans on subsequent fetches, reducing latency without sacrificing
// security. All trust is session-scoped; nothing persists across sessions.
type DomainReputationTracker struct {
	mu      sync.RWMutex
	domains map[string]*DomainReputation
}

// NewDomainReputationTracker creates an empty session-scoped tracker.
func NewDomainReputationTracker() *DomainReputationTracker {
	return &DomainReputationTracker{
		domains: make(map[string]*DomainReputation),
	}
}

// RecordFetch updates the reputation for a domain after a fetch+inspection.
// findingCount is the number of security findings from the inspection.
// maxSeverity is the highest severity among those findings.
func (t *DomainReputationTracker) RecordFetch(domain string, findingCount int, maxSeverity Severity) *DomainReputation {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	rep, exists := t.domains[domain]
	if !exists {
		rep = &DomainReputation{
			Domain:    domain,
			FirstSeen: now,
		}
		t.domains[domain] = rep
	}

	rep.LastSeen = now
	rep.FetchCount++
	rep.FindingCount += findingCount

	if findingCount == 0 {
		rep.CleanCount++
	}
	if maxSeverity > rep.MaxSeverity {
		rep.MaxSeverity = maxSeverity
	}

	rep.TrustLevel = computeTrust(rep)
	return rep
}

// Get returns the current reputation for a domain, or nil if unseen.
func (t *DomainReputationTracker) Get(domain string) *DomainReputation {
	t.mu.RLock()
	defer t.mu.RUnlock()
	rep := t.domains[domain]
	return rep
}

// ShouldReduceInspection returns true if the domain has earned enough
// trust to skip redundant pattern scans. Even trusted domains still get
// hash verification and TLS checks — only the expensive content scans
// are reduced.
func (t *DomainReputationTracker) ShouldReduceInspection(domain string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	rep, exists := t.domains[domain]
	if !exists {
		return false
	}
	return rep.TrustLevel == TrustTrusted
}

// Stats returns a snapshot of all tracked domains.
func (t *DomainReputationTracker) Stats() []DomainReputation {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]DomainReputation, 0, len(t.domains))
	for _, rep := range t.domains {
		result = append(result, *rep)
	}
	return result
}

// computeTrust derives trust level from accumulated fetch history.
func computeTrust(rep *DomainReputation) TrustLevel {
	// Any high/critical finding → suspicious, regardless of history.
	if rep.MaxSeverity >= SeverityHigh {
		return TrustSuspicious
	}
	// 3+ consecutive clean fetches with no findings → trusted.
	if rep.CleanCount >= 3 && rep.FindingCount == 0 {
		return TrustTrusted
	}
	// Seen before but has some findings → known but not trusted.
	if rep.FetchCount > 1 {
		return TrustKnown
	}
	return TrustNew
}
