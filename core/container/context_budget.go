package container

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"time"
)

var ErrContextBudgetDeferred = errors.New("context budget deferred")

// ContextArchetypeMatch describes how closely a live usage vector matches
// a known workload shape and the operating envelope that shape implies.
type ContextArchetypeMatch struct {
	Name                  string
	Score                 float64
	HeadroomMultiplier    float64
	BurstRatio            float64
	ReleaseHalfLife       time.Duration
	LeaseDuration         time.Duration
	UncertaintyMultiplier float64
}

// ContextArchetypeMatcher scores a normalized usage vector against workload
// archetypes. The implementation is injected so the quota can stay inside the
// existing container control path while the activation package owns the SIMD
// vector matching.
type ContextArchetypeMatcher interface {
	MatchContextArchetypes(features []float32, agentType string, limit int) []ContextArchetypeMatch
}

// ContextLeaseRequest describes a pending unit of work that wants context
// budget. ClaimID should be stable for the work item even before the runtime
// worker ACK arrives.
type ContextLeaseRequest struct {
	ClaimID           string
	AgentType         string
	RequestTokens     int64
	HardLimitTokens   int64
	PromptBytes       int
	DependencyCount   int
	CoAgentCount      int
	ContextFieldCount int
}

// ContextUsageObservation records authoritative live token usage from provider
// telemetry or another anchored source.
type ContextUsageObservation struct {
	AgentID     string
	AgentType   string
	InputTokens int64
	ObservedAt  time.Time
}

// ContextLease represents an admitted claim on the live session token budget.
type ContextLease struct {
	quota     *ResourceQuota
	claimID   string
	releaseMu sync.Once
}

// ContextBudgetDeferredError indicates the live budget could not admit the
// claim yet but expects the request to become schedulable after RetryAfter.
type ContextBudgetDeferredError struct {
	ClaimID    string
	RetryAfter time.Duration
	Reason     string
}

func (e *ContextBudgetDeferredError) Error() string {
	if e == nil {
		return ErrContextBudgetDeferred.Error()
	}
	if strings.TrimSpace(e.Reason) != "" {
		return e.Reason
	}
	return ErrContextBudgetDeferred.Error()
}

func (e *ContextBudgetDeferredError) Unwrap() error {
	return ErrContextBudgetDeferred
}

// BindAlias associates a runtime agent ID with this claim so later telemetry
// can update the same ledger entry.
func (l *ContextLease) BindAlias(alias string) {
	if l == nil || l.quota == nil {
		return
	}
	l.quota.BindContextAlias(l.claimID, alias)
}

// Release relinquishes the claim and starts the decay tail.
func (l *ContextLease) Release() {
	if l == nil || l.quota == nil {
		return
	}
	l.releaseMu.Do(func() {
		l.quota.ReleaseContextLease(l.claimID)
	})
}

type contextClaim struct {
	id              string
	agentType       string
	requestTokens   int64
	hardLimitTokens int64

	active            bool
	currentTokens     int64
	provisionalTokens int64
	releaseBaseTokens int64

	shortEMA    float64
	longEMA     float64
	varianceEMA float64
	recentPeak  int64
	lastDelta   int64

	complexityWeight      float64
	headroomMultiplier    float64
	burstRatio            float64
	uncertaintyMultiplier float64
	releaseHalfLife       time.Duration
	leaseDuration         time.Duration
	phase                 string

	lastObservedTokens int64
	lastObservedAt     time.Time
	lastUpdated        time.Time
	lastCompactedAt    time.Time
	leaseUntil         time.Time
	releasedAt         time.Time

	aliases map[string]struct{}
}

type contextClaimForecast struct {
	claim                 int64
	complexityWeight      float64
	headroomMultiplier    float64
	burstRatio            float64
	uncertaintyMultiplier float64
	releaseHalfLife       time.Duration
	leaseDuration         time.Duration
	phase                 string
}

func (q *ResourceQuota) SetContextArchetypeMatcher(matcher ContextArchetypeMatcher) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.contextMatcher = matcher
}

func (q *ResourceQuota) AcquireContextLease(ctx context.Context, req ContextLeaseRequest) (*ContextLease, error) {
	if q == nil {
		return &ContextLease{}, nil
	}
	req = normalizeContextLeaseRequest(req)
	if q.contextWindowLimit <= 0 || req.ClaimID == "" {
		return &ContextLease{quota: q, claimID: req.ClaimID}, nil
	}

	for {
		lease, err := q.TryAcquireContextLease(req)
		if err == nil {
			return lease, nil
		}
		var deferred *ContextBudgetDeferredError
		if !errors.As(err, &deferred) {
			return nil, err
		}

		waitFor := q.contextAdmissionTick
		if deferred.RetryAfter > 0 {
			waitFor = deferred.RetryAfter
		}

		q.mu.Lock()
		notifyCh := q.contextWaitChannelLocked()
		q.mu.Unlock()

		timer := time.NewTimer(waitFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			q.mu.Lock()
			delete(q.contextWaiters, req.ClaimID)
			q.recomputeContextStateLocked(time.Now())
			q.signalContextWaitersLocked()
			q.mu.Unlock()
			return nil, ctx.Err()
		case <-notifyCh:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (q *ResourceQuota) TryAcquireContextLease(req ContextLeaseRequest) (*ContextLease, error) {
	if q == nil {
		return &ContextLease{}, nil
	}
	req = normalizeContextLeaseRequest(req)
	if q.contextWindowLimit <= 0 || req.ClaimID == "" {
		return &ContextLease{quota: q, claimID: req.ClaimID}, nil
	}

	now := time.Now()
	q.mu.Lock()
	defer q.mu.Unlock()

	claim := q.ensureContextClaimLocked(req.ClaimID, req.AgentType, req.RequestTokens, req.HardLimitTokens)
	forecast := q.evaluateClaimForecastLocked(now, claim, req, max(claim.currentTokens, claim.provisionalTokens))
	admitted, retryAfter := q.tryAdmitContextLeaseLocked(now, claim, req, forecast)
	if admitted {
		delete(q.contextWaiters, claim.id)
		q.signalContextWaitersLocked()
		return &ContextLease{quota: q, claimID: claim.id}, nil
	}

	q.contextWaiters[claim.id] = now
	q.recomputeContextStateLocked(now)
	return nil, &ContextBudgetDeferredError{
		ClaimID:    claim.id,
		RetryAfter: retryAfter,
		Reason:     "context budget deferred",
	}
}

// WaitForContextBudget blocks until either the live-budget state changes,
// maxWait elapses, or the context is cancelled.
func (q *ResourceQuota) WaitForContextBudget(ctx context.Context, maxWait time.Duration) error {
	if q == nil || q.contextWindowLimit <= 0 {
		return nil
	}
	if maxWait <= 0 {
		return nil
	}

	q.mu.Lock()
	notifyCh := q.contextWaitChannelLocked()
	q.mu.Unlock()

	timer := time.NewTimer(maxWait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-notifyCh:
		return nil
	case <-timer.C:
		return nil
	}
}

func (q *ResourceQuota) BindContextAlias(claimID, alias string) {
	if q == nil {
		return
	}
	claimID = strings.TrimSpace(claimID)
	alias = strings.TrimSpace(alias)
	if claimID == "" || alias == "" {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	claim := q.lookupContextClaimLocked(claimID)
	if claim == nil {
		return
	}

	if prevID, ok := q.contextAliases[alias]; ok && prevID != claim.id {
		if prev := q.contextClaims[prevID]; prev != nil && prev.aliases != nil {
			delete(prev.aliases, alias)
		}
	}
	if claim.aliases == nil {
		claim.aliases = make(map[string]struct{})
	}
	claim.aliases[alias] = struct{}{}
	q.contextAliases[alias] = claim.id
}

func (q *ResourceQuota) ObserveContextUsage(obs ContextUsageObservation) {
	if q == nil || q.contextWindowLimit <= 0 {
		return
	}
	obs.AgentID = strings.TrimSpace(obs.AgentID)
	if obs.AgentID == "" || obs.InputTokens <= 0 {
		return
	}
	if obs.ObservedAt.IsZero() {
		obs.ObservedAt = time.Now()
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	claim := q.lookupContextClaimLocked(obs.AgentID)
	if claim == nil {
		claim = q.ensureContextClaimLocked(obs.AgentID, obs.AgentType, obs.InputTokens, obs.InputTokens)
	}
	if strings.TrimSpace(obs.AgentType) != "" {
		claim.agentType = strings.TrimSpace(obs.AgentType)
	}
	if claim.requestTokens <= 0 {
		claim.requestTokens = max(obs.InputTokens, q.contextWindowRequest/16)
	}
	if claim.hardLimitTokens < obs.InputTokens {
		claim.hardLimitTokens = obs.InputTokens
	}

	q.updateClaimUsageLocked(claim, obs.ObservedAt, obs.InputTokens)
	forecast := q.evaluateClaimForecastLocked(obs.ObservedAt, claim, ContextLeaseRequest{
		ClaimID:         claim.id,
		AgentType:       claim.agentType,
		RequestTokens:   claim.requestTokens,
		HardLimitTokens: claim.hardLimitTokens,
	}, obs.InputTokens)
	q.applyForecastLocked(claim, obs.ObservedAt, forecast, true)
	q.recomputeContextStateLocked(obs.ObservedAt)
	q.signalContextWaitersLocked()
}

func (q *ResourceQuota) TouchContextClaim(agentID, agentType string) {
	if q == nil || q.contextWindowLimit <= 0 {
		return
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	claim := q.lookupContextClaimLocked(agentID)
	if claim == nil {
		claim = q.ensureContextClaimLocked(agentID, agentType, 0, 0)
	}
	if strings.TrimSpace(agentType) != "" {
		claim.agentType = strings.TrimSpace(agentType)
	}
	claim.lastUpdated = time.Now()
	q.recomputeContextStateLocked(claim.lastUpdated)
}

func (q *ResourceQuota) ReleaseContextLease(claimID string) {
	if q == nil || q.contextWindowLimit <= 0 {
		return
	}
	claimID = strings.TrimSpace(claimID)
	if claimID == "" {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	claim := q.lookupContextClaimLocked(claimID)
	if claim == nil {
		delete(q.contextWaiters, claimID)
		q.recomputeContextStateLocked(now)
		q.signalContextWaitersLocked()
		return
	}

	effective := q.claimEffectiveTokensLocked(now, claim)
	claim.active = false
	claim.currentTokens = 0
	claim.provisionalTokens = 0
	claim.releaseBaseTokens = max(effective, claim.releaseBaseTokens)
	claim.releasedAt = now
	claim.leaseUntil = now
	claim.lastUpdated = now
	delete(q.contextWaiters, claim.id)

	q.recomputeContextStateLocked(now)
	q.signalContextWaitersLocked()
}

func (q *ResourceQuota) contextWaiterCount() int {
	if q == nil {
		return 0
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.contextWaiters)
}

func normalizeContextLeaseRequest(req ContextLeaseRequest) ContextLeaseRequest {
	req.ClaimID = strings.TrimSpace(req.ClaimID)
	req.AgentType = strings.TrimSpace(req.AgentType)
	if req.RequestTokens < 0 {
		req.RequestTokens = 0
	}
	if req.HardLimitTokens < req.RequestTokens {
		req.HardLimitTokens = req.RequestTokens
	}
	if req.DependencyCount < 0 {
		req.DependencyCount = 0
	}
	if req.CoAgentCount < 0 {
		req.CoAgentCount = 0
	}
	if req.ContextFieldCount < 0 {
		req.ContextFieldCount = 0
	}
	return req
}

func (q *ResourceQuota) ensureContextClaimLocked(key, agentType string, requestTokens, hardLimitTokens int64) *contextClaim {
	if claim := q.lookupContextClaimLocked(key); claim != nil {
		if requestTokens > 0 {
			claim.requestTokens = max(claim.requestTokens, requestTokens)
		}
		if hardLimitTokens > 0 {
			claim.hardLimitTokens = max(claim.hardLimitTokens, hardLimitTokens)
		}
		if strings.TrimSpace(agentType) != "" {
			claim.agentType = strings.TrimSpace(agentType)
		}
		return claim
	}

	claim := &contextClaim{
		id:                    key,
		agentType:             strings.TrimSpace(agentType),
		requestTokens:         requestTokens,
		hardLimitTokens:       hardLimitTokens,
		complexityWeight:      1.0,
		headroomMultiplier:    1.15,
		burstRatio:            1.20,
		uncertaintyMultiplier: 1.10,
		releaseHalfLife:       q.contextReleaseHalfLife,
		leaseDuration:         q.contextLeaseMin,
		phase:                 "idle",
		aliases:               map[string]struct{}{key: {}},
	}
	q.contextClaims[key] = claim
	q.contextAliases[key] = key
	return claim
}

func (q *ResourceQuota) lookupContextClaimLocked(key string) *contextClaim {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	if primary, ok := q.contextAliases[key]; ok {
		key = primary
	}
	return q.contextClaims[key]
}

func (q *ResourceQuota) updateClaimUsageLocked(claim *contextClaim, now time.Time, tokens int64) {
	previous := claim.lastObservedTokens
	if !claim.lastObservedAt.IsZero() {
		q.updateClaimEWMAsLocked(claim, now, tokens)
	} else {
		claim.shortEMA = float64(tokens)
		claim.longEMA = float64(tokens)
	}
	if previous > 0 && tokens < previous*7/10 {
		claim.lastCompactedAt = now
	}
	claim.lastDelta = tokens - previous
	claim.lastObservedTokens = tokens
	claim.lastObservedAt = now
	claim.lastUpdated = now
	claim.currentTokens = tokens
	claim.active = true
	if tokens > claim.recentPeak {
		claim.recentPeak = tokens
	}
}

func (q *ResourceQuota) updateClaimEWMAsLocked(claim *contextClaim, now time.Time, tokens int64) {
	dt := now.Sub(claim.lastObservedAt)
	if dt <= 0 {
		dt = q.contextAdmissionTick
	}
	shortAlpha := ewmaAlpha(dt, q.contextShortWindow)
	longAlpha := ewmaAlpha(dt, q.contextLongWindow)

	value := float64(tokens)
	claim.shortEMA += shortAlpha * (value - claim.shortEMA)
	claim.longEMA += longAlpha * (value - claim.longEMA)

	diff := value - claim.longEMA
	claim.varianceEMA += longAlpha * ((diff * diff) - claim.varianceEMA)
}

func ewmaAlpha(delta, window time.Duration) float64 {
	if window <= 0 {
		return 1
	}
	ratio := float64(delta) / float64(window)
	if ratio <= 0 {
		return 0.25
	}
	alpha := 1 - math.Exp(-ratio)
	if alpha < 0.05 {
		return 0.05
	}
	if alpha > 1 {
		return 1
	}
	return alpha
}

func (q *ResourceQuota) evaluateClaimForecastLocked(now time.Time, claim *contextClaim, req ContextLeaseRequest, observedTokens int64) contextClaimForecast {
	requestTokens := max(claim.requestTokens, req.RequestTokens)
	if requestTokens <= 0 {
		requestTokens = max(observedTokens, q.contextWindowRequest/16)
	}
	complexity := q.claimComplexityLocked(req, requestTokens)
	phase := determineClaimPhase(now, claim, requestTokens, observedTokens)

	features := q.contextFeatureVectorLocked(claim, requestTokens, observedTokens, complexity)
	matches := q.matchContextArchetypesLocked(features, firstNonEmpty(claim.agentType, req.AgentType))
	headroom, burstRatio, uncertainty, releaseHalfLife, leaseDuration := blendContextArchetypes(matches, q.contextReleaseHalfLife, q.contextLeaseMin)

	variance := math.Sqrt(max(claim.varianceEMA, 0))
	riskBase := max(observedTokens, claim.provisionalTokens, int64(claim.shortEMA), int64(claim.longEMA))
	if claim.lastDelta > 0 {
		riskBase += claim.lastDelta / 2
	}
	riskBase += int64(variance)
	if phase == "burst" {
		riskBase = max(riskBase, int64(float64(max(observedTokens, claim.recentPeak))*burstRatio))
	}

	projected := int64(float64(max(riskBase, requestTokens)) * headroom * complexity)
	hardLimit := max(claim.hardLimitTokens, req.HardLimitTokens)
	if hardLimit > 0 && projected > hardLimit {
		projected = hardLimit
	}
	projected = max(projected, observedTokens)

	return contextClaimForecast{
		claim:                 projected,
		complexityWeight:      complexity,
		headroomMultiplier:    headroom,
		burstRatio:            burstRatio,
		uncertaintyMultiplier: uncertainty,
		releaseHalfLife:       releaseHalfLife,
		leaseDuration:         leaseDuration,
		phase:                 phase,
	}
}

func (q *ResourceQuota) applyForecastLocked(claim *contextClaim, now time.Time, forecast contextClaimForecast, admitted bool) {
	claim.complexityWeight = forecast.complexityWeight
	claim.headroomMultiplier = forecast.headroomMultiplier
	claim.burstRatio = forecast.burstRatio
	claim.uncertaintyMultiplier = forecast.uncertaintyMultiplier
	claim.releaseHalfLife = forecast.releaseHalfLife
	claim.leaseDuration = forecast.leaseDuration
	claim.phase = forecast.phase
	if admitted {
		claim.active = true
		claim.provisionalTokens = forecast.claim
		claim.releaseBaseTokens = forecast.claim
		claim.releasedAt = time.Time{}
		claim.leaseUntil = now.Add(forecast.leaseDuration)
		claim.lastUpdated = now
	}
}

func (q *ResourceQuota) tryAdmitContextLeaseLocked(now time.Time, claim *contextClaim, req ContextLeaseRequest, forecast contextClaimForecast) (bool, time.Duration) {
	q.applyForecastLocked(claim, now, forecast, false)
	q.recomputeContextStateLocked(now)

	current := q.claimEffectiveTokensLocked(now, claim)
	used := q.contextWindowUsed.Load()
	projected := used - current + forecast.claim

	soft := q.contextSoftCapacity.Load()
	burst := q.contextBurstCapacity.Load()
	hard := q.contextHardCapacity.Load()

	if hard <= 0 {
		claim.provisionalTokens = forecast.claim
		claim.active = true
		return true, 0
	}
	if projected <= soft {
		q.applyForecastLocked(claim, now, forecast, true)
		q.recomputeContextStateLocked(now)
		return true, 0
	}
	if projected <= burst && !q.shouldDeferForFairnessLocked(now, claim, projected) {
		q.applyForecastLocked(claim, now, forecast, true)
		q.recomputeContextStateLocked(now)
		return true, 0
	}
	if projected <= hard && q.waiterAgeLocked(now, claim.id) >= q.contextLeaseMin {
		q.applyForecastLocked(claim, now, forecast, true)
		q.recomputeContextStateLocked(now)
		return true, 0
	}

	return false, q.nextContextRetryLocked(now, req.ClaimID)
}

func (q *ResourceQuota) shouldDeferForFairnessLocked(now time.Time, claim *contextClaim, projected int64) bool {
	activeCount := 0
	totalLong := 0.0
	for _, entry := range q.contextClaims {
		if q.claimEffectiveTokensLocked(now, entry) <= 0 {
			continue
		}
		activeCount++
		totalLong += max(entry.longEMA, 1)
	}
	if activeCount <= 1 || totalLong <= 0 {
		return false
	}

	candidateShare := max(claim.longEMA, float64(max(projected, claim.requestTokens))) / totalLong
	averageShare := 1.0 / float64(activeCount)
	if candidateShare <= averageShare*1.35 {
		return false
	}
	return q.waiterAgeLocked(now, claim.id) < q.contextLeaseMin
}

func (q *ResourceQuota) waiterAgeLocked(now time.Time, claimID string) time.Duration {
	firstWait, ok := q.contextWaiters[claimID]
	if !ok {
		return 0
	}
	return now.Sub(firstWait)
}

func (q *ResourceQuota) nextContextRetryLocked(now time.Time, claimID string) time.Duration {
	wait := q.contextAdmissionTick
	if wait <= 0 {
		wait = 250 * time.Millisecond
	}
	if age := q.waiterAgeLocked(now, claimID); age > 0 {
		backoffSteps := int(age / q.contextLeaseMin)
		if backoffSteps > 3 {
			backoffSteps = 3
		}
		wait += time.Duration(backoffSteps) * (q.contextAdmissionTick / 2)
	}
	wait += deterministicLeaseJitter(claimID, q.contextAdmissionTick/3)
	if wait <= 0 {
		return q.contextAdmissionTick
	}
	return wait
}

func deterministicLeaseJitter(key string, maxJitter time.Duration) time.Duration {
	if maxJitter <= 0 || key == "" {
		return 0
	}
	var sum uint32
	for i := 0; i < len(key); i++ {
		sum += uint32(key[i])
	}
	return time.Duration(sum % uint32(maxJitter))
}

func (q *ResourceQuota) contextFeatureVectorLocked(claim *contextClaim, requestTokens, observedTokens int64, complexity float64) []float32 {
	req := float64(max(requestTokens, 1))
	peers := float64(max(len(q.contextClaims)-1, 0))
	return []float32{
		float32(float64(observedTokens) / req),
		float32(claim.shortEMA / req),
		float32(claim.longEMA / req),
		float32(float64(claim.lastDelta) / req),
		float32(math.Sqrt(max(claim.varianceEMA, 0)) / req),
		float32(float64(claim.recentPeak) / req),
		float32(peers / (peers + 2)),
		float32(complexity),
		float32(float64(max(claim.provisionalTokens, observedTokens)) / req),
	}
}

func (q *ResourceQuota) claimComplexityLocked(req ContextLeaseRequest, requestTokens int64) float64 {
	if requestTokens <= 0 {
		requestTokens = 1
	}
	promptTokens := float64(req.PromptBytes) / 4
	complexity := 1.0
	complexity += min(promptTokens/float64(requestTokens), 1.0) * 0.45
	complexity += min(float64(req.DependencyCount)/6.0, 1.0) * 0.30
	complexity += min(float64(req.CoAgentCount)/4.0, 1.0) * 0.20
	complexity += min(float64(req.ContextFieldCount)/12.0, 1.0) * 0.15
	if complexity < 1.0 {
		return 1.0
	}
	if complexity > 2.1 {
		return 2.1
	}
	return complexity
}

func determineClaimPhase(now time.Time, claim *contextClaim, requestTokens, observedTokens int64) string {
	req := max(requestTokens, 1)
	if !claim.lastCompactedAt.IsZero() && now.Sub(claim.lastCompactedAt) <= max(claim.releaseHalfLife, 5*time.Second) {
		return "compacted"
	}
	if observedTokens == 0 && claim.shortEMA <= float64(req)/10 {
		return "idle"
	}
	if claim.lastDelta > req/6 {
		if math.Sqrt(max(claim.varianceEMA, 0)) > float64(req)/5 {
			return "burst"
		}
		return "ramp"
	}
	if claim.lastDelta < -req/8 {
		return "decay"
	}
	if math.Abs(float64(claim.lastDelta)) <= float64(req)/12 && claim.longEMA >= float64(req)/2 {
		return "plateau"
	}
	return "burst"
}

func (q *ResourceQuota) matchContextArchetypesLocked(features []float32, agentType string) []ContextArchetypeMatch {
	if q.contextMatcher == nil {
		return nil
	}
	return q.contextMatcher.MatchContextArchetypes(features, agentType, 3)
}

func blendContextArchetypes(matches []ContextArchetypeMatch, defaultHalfLife, defaultLease time.Duration) (headroom, burstRatio, uncertainty float64, releaseHalfLife, leaseDuration time.Duration) {
	if len(matches) == 0 {
		return 1.15, 1.20, 1.10, defaultHalfLife, defaultLease
	}

	var totalWeight float64
	for _, match := range matches {
		if match.Score > 0 {
			totalWeight += match.Score
		}
	}
	if totalWeight == 0 {
		return 1.15, 1.20, 1.10, defaultHalfLife, defaultLease
	}

	var releaseWeight float64
	var leaseWeight float64
	for _, match := range matches {
		weight := match.Score / totalWeight
		headroom += match.HeadroomMultiplier * weight
		burstRatio += match.BurstRatio * weight
		uncertainty += match.UncertaintyMultiplier * weight
		releaseWeight += float64(match.ReleaseHalfLife) * weight
		leaseWeight += float64(match.LeaseDuration) * weight
	}
	if releaseWeight > 0 {
		releaseHalfLife = time.Duration(releaseWeight)
	}
	if releaseHalfLife <= 0 {
		releaseHalfLife = defaultHalfLife
	}
	if leaseWeight > 0 {
		leaseDuration = time.Duration(leaseWeight)
	}
	if leaseDuration <= 0 {
		leaseDuration = defaultLease
	}
	return headroom, burstRatio, uncertainty, releaseHalfLife, leaseDuration
}

func (q *ResourceQuota) recomputeContextStateLocked(now time.Time) {
	var (
		totalClaim       int64
		totalStable      int64
		totalUncertainty int64
		activeCount      int64
		averageClaim     int64
		waiterCount      int64 = int64(len(q.contextWaiters))
		burstSpan              = max(q.contextWindowLimit-q.contextWindowRequest, int64(0))
	)

	for key, claim := range q.contextClaims {
		effective := q.claimEffectiveTokensLocked(now, claim)
		if effective <= 0 && !claim.active && !claim.releasedAt.IsZero() && now.Sub(claim.releasedAt) > claim.releaseHalfLife*3 {
			q.deleteContextClaimLocked(key, claim)
			continue
		}
		if effective <= 0 {
			continue
		}
		activeCount++
		totalClaim += effective
		totalStable += int64(max(claim.longEMA, 0))
		totalUncertainty += max(effective-max(int64(claim.longEMA), claim.currentTokens), 0)
	}
	if activeCount > 0 {
		averageClaim = totalClaim / activeCount
	}

	stabilityCredit := min(int64(max(activeCount-1, 0))*averageClaim/2, burstSpan)
	steadyFloor := max(q.contextWindowRequest, totalStable)
	adaptiveBase := min(q.contextWindowLimit, steadyFloor+stabilityCredit)

	fairnessHeadroom := min(max(averageClaim, q.contextWindowRequest/8), burstSpan/2)
	churnBuffer := min(max(waiterCount*max(averageClaim/2, q.contextWindowRequest/16), int64(0)), burstSpan)
	if activeCount == 0 && waiterCount == 0 {
		churnBuffer = 0
	}
	uncertaintyReserve := min(totalUncertainty+churnBuffer/2, burstSpan)
	hostReserve := uncertaintyReserve / 4

	hard := q.contextWindowLimit - hostReserve
	if hard < q.contextWindowRequest {
		hard = q.contextWindowRequest
	}
	burst := hard - uncertaintyReserve/2
	if burst < adaptiveBase {
		burst = adaptiveBase
	}
	soft := burst - fairnessHeadroom - churnBuffer/2
	if soft < adaptiveBase {
		soft = adaptiveBase
	}
	if soft > burst {
		soft = burst
	}
	if burst > hard {
		burst = hard
	}

	q.contextWindowUsed.Store(totalClaim)
	q.contextSoftCapacity.Store(max(soft, 0))
	q.contextBurstCapacity.Store(max(burst, 0))
	q.contextHardCapacity.Store(max(hard, 0))
}

func (q *ResourceQuota) deleteContextClaimLocked(key string, claim *contextClaim) {
	delete(q.contextClaims, key)
	delete(q.contextWaiters, key)
	for alias := range claim.aliases {
		if primary, ok := q.contextAliases[alias]; ok && primary == key {
			delete(q.contextAliases, alias)
		}
	}
}

func (q *ResourceQuota) claimEffectiveTokensLocked(now time.Time, claim *contextClaim) int64 {
	if claim == nil {
		return 0
	}
	if claim.active {
		if !claim.leaseUntil.IsZero() && now.After(claim.leaseUntil) && now.Sub(claim.lastUpdated) > claim.leaseDuration {
			claim.active = false
			claim.releaseBaseTokens = max(claim.provisionalTokens, claim.currentTokens, int64(claim.shortEMA))
			claim.releasedAt = now
			claim.currentTokens = 0
			claim.provisionalTokens = 0
		} else {
			return max(claim.provisionalTokens, claim.currentTokens)
		}
	}
	if claim.releaseBaseTokens <= 0 || claim.releasedAt.IsZero() {
		return 0
	}
	halfLife := max(claim.releaseHalfLife, q.contextAdmissionTick)
	elapsed := now.Sub(claim.releasedAt)
	if elapsed <= 0 {
		return claim.releaseBaseTokens
	}
	tail := float64(claim.releaseBaseTokens) * math.Exp(-0.693*elapsed.Seconds()/halfLife.Seconds())
	minFloor := max(claim.requestTokens/32, int64(64))
	if int64(tail) < minFloor {
		return 0
	}
	return int64(tail)
}

func (q *ResourceQuota) contextWaitChannelLocked() <-chan struct{} {
	if q.contextNotify == nil {
		q.contextNotify = make(chan struct{})
	}
	return q.contextNotify
}

func (q *ResourceQuota) signalContextWaitersLocked() {
	if q.contextNotify == nil {
		q.contextNotify = make(chan struct{})
		return
	}
	close(q.contextNotify)
	q.contextNotify = make(chan struct{})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
