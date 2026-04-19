// Session-scoped consultation response cache.
//
// Solves a real cost problem: when an agent (typically the architect)
// consults the same knowledge agent with the same — or near-identical —
// question across multiple turns of a session, the consulted agent
// re-does the same work (filesystem scans, git history walks, research
// queries) and returns the same answer. Wasted time, wasted tokens,
// wasted patience.
//
// The dedup decision is delegated to the existing pressure math
// (evaluateAdmission). The cache layer asks: "if these cached
// observations were the only history the throttling system saw,
// would re-asking now be admitted?" If admission says no — i.e. the
// new query is too similar to a prior cached consultation given the
// pressure-vs-gain math — AND the cached entry is still fresh AND
// covers the new query, we serve it. No new threshold; the existing
// math is the threshold.
//
// Two principled gates compose the serve decision:
//
//  1. PRESSURE — would evaluateAdmission deny re-asking against a
//     virtual ledger seeded with this cached observation? If allowed,
//     the queries are different enough that re-asking is worthwhile;
//     fall through to the live bus call. The pressure math already
//     encodes "is the cached answer relevant to the new query" via
//     similarity; no separate coverage check is needed (and a token-
//     overlap coverage gate introduces false negatives because query
//     stopwords like "does", "the", "have" rarely appear verbatim
//     in responses).
//
//  2. FRESHNESS — is age < FreshnessHorizon (agent-self-reported)?
//     A missing or zero horizon is treated as "expires immediately";
//     the agent has not committed to a validity window so cache
//     re-use is unsafe. This makes freshness an explicit contract,
//     not a guess.
//
// Memory is bounded per (session, target) by sessionCacheLRUSize and
// per-process by sessionCacheGlobalEntryCap. Eviction is oldest-first
// when either limit is exceeded; this keeps the cache from growing
// without bound across long sessions or many sessions.
package shared

import (
	"strings"
	"sync"
	"time"
)

// sessionCacheLRUSize bounds the number of cached observations per
// (session, target) pair. Sized so a session can carry several
// distinct question shapes per target without the cache crowding
// itself out — the throttling math will already have discouraged
// the LLM from asking 32 questions to a single agent.
const sessionCacheLRUSize = 32

// sessionCacheGlobalEntryCap bounds total cached observations across
// all sessions. Defensive ceiling against the long-running-process
// case where many sessions accumulate without explicit teardown;
// when exceeded, the oldest-stored entries across all sessions are
// evicted until below cap.
const sessionCacheGlobalEntryCap = 4096

// SessionConsultationCache stores past consultation observations
// scoped by SessionID, indexed by target. It is the durable side
// of the per-request throttling ledger: throttling prevents
// within-chain over-consultation; the session cache prevents
// across-turn duplication.
type SessionConsultationCache struct {
	mu      sync.RWMutex
	bySess  map[string]*sessionConsultationBucket
	totalCt int
}

type sessionConsultationBucket struct {
	mu        sync.Mutex
	perTarget map[string][]*consultationObservation
}

// NewSessionConsultationCache builds an empty cache. Process-global
// instances are allowed (DefaultSessionConsultationCache) — the
// cache is concurrency-safe.
func NewSessionConsultationCache() *SessionConsultationCache {
	return &SessionConsultationCache{
		bySess: make(map[string]*sessionConsultationBucket),
	}
}

// DefaultSessionConsultationCache is the process-global cache used
// by Store/Lookup helpers when no explicit cache is provided.
var DefaultSessionConsultationCache = NewSessionConsultationCache()

// SessionCacheLookupResult is what the cache returns to callers.
// Hit==true means a cached observation passed all three gates
// (pressure dedup, freshness, coverage); Response carries the
// stored response payload for serve-back.
type SessionCacheLookupResult struct {
	Hit              bool
	Response         any
	Target           string
	Query            string
	StoredAt         time.Time
	FreshnessHorizon time.Duration
	Reason           string // diagnostic: why a hit (or near-miss) was decided
}

// LookupSessionConsultationCache asks the cache: "is there a stored
// response that satisfies the three gates (pressure dedup, freshness,
// coverage) for this query against this target in this session?"
//
// Returns Hit=false (with a Reason describing the closest near-miss)
// when no cached observation passes all gates. Callers proceed with
// a live consultation when Hit is false.
//
// researchDepth is forwarded to evaluateAdmission so the dedup
// decision uses the same depth-credit math as the live throttling
// path. nextDepth (current consult-chain depth + 1) likewise.
func LookupSessionConsultationCache(
	cache *SessionConsultationCache,
	sessionID string,
	target string,
	query string,
	researchDepth ResearchDepth,
	nextDepth int,
) SessionCacheLookupResult {
	if cache == nil {
		return SessionCacheLookupResult{Reason: "cache disabled"}
	}
	sessionID = strings.TrimSpace(sessionID)
	target = strings.ToLower(strings.TrimSpace(target))
	query = strings.TrimSpace(query)
	if sessionID == "" || target == "" || query == "" {
		return SessionCacheLookupResult{Reason: "missing session/target/query"}
	}
	bucket := cache.bucket(sessionID, false)
	if bucket == nil {
		return SessionCacheLookupResult{Reason: "no session entries"}
	}
	bucket.mu.Lock()
	entries := append([]*consultationObservation(nil), bucket.perTarget[target]...)
	bucket.mu.Unlock()
	if len(entries) == 0 {
		return SessionCacheLookupResult{Reason: "no target entries"}
	}

	queryFingerprint, queryTokens := normalizeConsultationQuery(query)
	now := time.Now()

	// Iterate cached entries; for each, apply the two gates
	// (freshness + pressure dedup). The first entry that passes both
	// is served. Order doesn't materially affect correctness — if any
	// fresh entry is similar enough that pressure would deny
	// re-asking, that's a valid cached answer.
	var (
		bestEntry  *consultationObservation
		bestReason string
	)
	_ = queryFingerprint
	for _, entry := range entries {
		// Freshness gate: missing or zero horizon ⇒ no cache re-use
		// (agent didn't commit a validity window). Past horizon ⇒
		// stale; the world may have moved, force re-ask.
		if entry == nil || entry.FreshnessHorizon <= 0 {
			bestReason = "no freshness horizon committed"
			continue
		}
		if entry.StoredAt.IsZero() || now.Sub(entry.StoredAt) >= entry.FreshnessHorizon {
			bestReason = "horizon expired"
			continue
		}

		// Pressure gate: would evaluateAdmission DENY re-asking if the
		// only prior history were this cached observation? If so, the
		// queries are similar enough by the existing pressure math
		// that serving the cache is the correct dedup. The same
		// similarity math used to throttle re-asks here decides
		// "are these queries equivalent enough to share an answer?"
		entryTokens := normalizedConsultationTokens(entry.Fingerprint)
		similarity := consultationJaccard(queryTokens, entryTokens)
		verdict := evaluateAdmission(admissionInputs{
			Target:          target,
			Fingerprint:     entry.Fingerprint,
			Tokens:          queryTokens,
			NextDepth:       nextDepth,
			ResearchDepth:   researchDepth,
			Similarity:      similarity,
			TargetCount:     2, // this query + the cached prior
			ConsultCount:    2,
			DistinctTargets: 1,
			GlobalReward:    entry.Reward,
			TargetReward:    entry.Reward,
		})
		if verdict.Allowed {
			bestReason = "pressure math admits re-asking (queries differ)"
			continue
		}
		bestEntry = entry
		bestReason = "all gates passed"
		break
	}
	if bestEntry == nil {
		return SessionCacheLookupResult{Reason: bestReason}
	}
	return SessionCacheLookupResult{
		Hit:              true,
		Response:         bestEntry.Response,
		Target:           bestEntry.Target,
		Query:            bestEntry.Query,
		StoredAt:         bestEntry.StoredAt,
		FreshnessHorizon: bestEntry.FreshnessHorizon,
		Reason:           bestReason,
	}
}

// StoreSessionConsultationCache writes a successful consultation's
// response into the per-session cache. Callers (typically inside
// RecordConsultationOutcome) invoke this when a consultation
// returned an answer worth caching. Entries with no FreshnessHorizon
// ARE stored — telemetry/debug callers may want to see them — but
// LookupSessionConsultationCache will never return them as hits.
func StoreSessionConsultationCache(
	cache *SessionConsultationCache,
	sessionID string,
	target string,
	query string,
	response any,
	freshnessHorizon time.Duration,
	reward float64,
) {
	if cache == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	target = strings.ToLower(strings.TrimSpace(target))
	query = strings.TrimSpace(query)
	if sessionID == "" || target == "" || query == "" {
		return
	}
	fingerprint, _ := normalizeConsultationQuery(query)
	entry := &consultationObservation{
		Target:           target,
		Fingerprint:      fingerprint,
		SessionID:        sessionID,
		Query:            query,
		Response:         response,
		FreshnessHorizon: freshnessHorizon,
		StoredAt:         time.Now(),
		Reward:           reward,
		OutcomeKnown:     true,
	}
	bucket := cache.bucket(sessionID, true)
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	bucket.perTarget[target] = append(bucket.perTarget[target], entry)
	// Per-(session,target) LRU: drop oldest when over the bucket cap.
	if over := len(bucket.perTarget[target]) - sessionCacheLRUSize; over > 0 {
		bucket.perTarget[target] = bucket.perTarget[target][over:]
	}
	cache.mu.Lock()
	cache.totalCt++
	overGlobal := cache.totalCt - sessionCacheGlobalEntryCap
	cache.mu.Unlock()
	if overGlobal > 0 {
		cache.evictOldestAcrossSessions(overGlobal)
	}
}

// InvalidateSessionConsultationCacheForSession clears every cached
// observation for a given session. Called when a session ends or
// when a caller knows session-wide cached state is no longer valid.
func InvalidateSessionConsultationCacheForSession(cache *SessionConsultationCache, sessionID string) {
	if cache == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	cache.mu.Lock()
	bucket := cache.bySess[sessionID]
	if bucket != nil {
		bucket.mu.Lock()
		removed := 0
		for _, list := range bucket.perTarget {
			removed += len(list)
		}
		bucket.perTarget = make(map[string][]*consultationObservation)
		bucket.mu.Unlock()
		cache.totalCt -= removed
		if cache.totalCt < 0 {
			cache.totalCt = 0
		}
	}
	delete(cache.bySess, sessionID)
	cache.mu.Unlock()
}

func (c *SessionConsultationCache) bucket(sessionID string, create bool) *sessionConsultationBucket {
	c.mu.RLock()
	bucket := c.bySess[sessionID]
	c.mu.RUnlock()
	if bucket != nil {
		return bucket
	}
	if !create {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if bucket = c.bySess[sessionID]; bucket != nil {
		return bucket
	}
	bucket = &sessionConsultationBucket{
		perTarget: make(map[string][]*consultationObservation),
	}
	c.bySess[sessionID] = bucket
	return bucket
}

// evictOldestAcrossSessions removes the n oldest (by StoredAt)
// entries across every session bucket. Defensive eviction triggered
// when the global per-process entry cap is exceeded.
func (c *SessionConsultationCache) evictOldestAcrossSessions(n int) {
	if n <= 0 {
		return
	}
	type candidate struct {
		bucket *sessionConsultationBucket
		target string
		index  int
		stored time.Time
	}
	c.mu.RLock()
	buckets := make([]*sessionConsultationBucket, 0, len(c.bySess))
	for _, b := range c.bySess {
		buckets = append(buckets, b)
	}
	c.mu.RUnlock()

	candidates := make([]candidate, 0, n*2)
	for _, b := range buckets {
		b.mu.Lock()
		for target, list := range b.perTarget {
			for i, entry := range list {
				if entry == nil {
					continue
				}
				candidates = append(candidates, candidate{
					bucket: b,
					target: target,
					index:  i,
					stored: entry.StoredAt,
				})
			}
		}
		b.mu.Unlock()
	}
	if len(candidates) <= n {
		return
	}
	// Partial sort would suffice but n is bounded small (~ over-cap),
	// so a full sort is cheap enough and simpler.
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].stored.Before(candidates[i].stored) {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
	dropped := 0
	for _, victim := range candidates[:n] {
		victim.bucket.mu.Lock()
		list := victim.bucket.perTarget[victim.target]
		// The index may have shifted if a different victim from the
		// same target/bucket dropped earlier; locate by pointer
		// identity-equivalent (StoredAt + Query) to be safe.
		for i, entry := range list {
			if entry != nil && entry.StoredAt.Equal(victim.stored) {
				victim.bucket.perTarget[victim.target] = append(list[:i], list[i+1:]...)
				dropped++
				break
			}
		}
		victim.bucket.mu.Unlock()
	}
	c.mu.Lock()
	c.totalCt -= dropped
	if c.totalCt < 0 {
		c.totalCt = 0
	}
	c.mu.Unlock()
}

// ConsultationDepthFromMetadata extracts the consult-chain depth
// (CurrentDepth) from the deliberation snapshot embedded in
// metadata. Returns 0 when no snapshot is present, which is
// correct for "first consult in this chain". Cache lookups use
// depth+1 to model "this would be the next consult".
func ConsultationDepthFromMetadata(metadata map[string]any) int {
	return consultationSnapshotFromMetadata(metadata).CurrentDepth
}

// ExtractFreshnessHorizon reads the agent-self-reported validity
// window from a consultation response payload. Agents that want
// their answers eligible for cached re-use set FreshnessHorizon
// on the typed ConsultResponsePayload they return; legacy callers
// that still emit map[string]any can set the "freshness_horizon"
// key with a Go duration string ("15m") or numeric seconds value.
// Missing/zero/unparseable means "expires immediately" — the cache
// will refuse to serve the answer back.
//
// This is the explicit contract between agents and the cache: the
// validity window comes from the agent that produced the answer,
// not from a guess on the cache side. Agents understand their own
// volatility (librarian: workspace mtime; archivalist: commit
// cadence; academic: research half-life) far better than a generic
// cache layer could.
func ExtractFreshnessHorizon(response any) time.Duration {
	switch typed := response.(type) {
	case nil:
		return 0
	case *ConsultResponsePayload:
		if typed == nil {
			return 0
		}
		return typed.FreshnessHorizon
	case ConsultResponsePayload:
		return typed.FreshnessHorizon
	case map[string]any:
		raw, ok := typed["freshness_horizon"]
		if !ok {
			return 0
		}
		switch v := raw.(type) {
		case string:
			if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil && d > 0 {
				return d
			}
			return 0
		case float64:
			if v > 0 {
				return time.Duration(v) * time.Second
			}
		case int:
			if v > 0 {
				return time.Duration(v) * time.Second
			}
		case int64:
			if v > 0 {
				return time.Duration(v) * time.Second
			}
		}
	}
	return 0
}

