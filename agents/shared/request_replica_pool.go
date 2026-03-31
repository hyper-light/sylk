package shared

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	rtmetrics "runtime/metrics"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/providers/gateway"
)

const (
	defaultReplicaControllerTick    = 1 * time.Second
	defaultReplicaTargetUtilization = 0.75
	defaultReplicaDrainWindow       = 15 * time.Second
	defaultReplicaQueueTargetAge    = 10 * time.Second
	defaultReplicaQueueScaleFactor  = 4
	defaultReplicaQueueFloor        = 8
	defaultReplicaSamples           = 64
	defaultReplicaCPUFactor         = 2
	defaultReplicaGoroutines        = 6
	defaultReplicaRetryCeiling      = 30 * time.Second
)

// RequestReplicaPoolSnapshot captures the current admission state for a
// knowledge-style agent that can serve multiple forwarded requests in parallel.
type RequestReplicaPoolSnapshot struct {
	Active              int
	Desired             int
	MaxActive           int
	Queued              int
	MaxQueued           int
	EstimatedService    time.Duration
	EstimatedRetryAfter time.Duration
	MemoryPressure      float64
	ProviderInflight    int
	ProviderMaxInflight int
}

func (s RequestReplicaPoolSnapshot) EffectiveCap() int {
	switch {
	case s.Desired > 0 && s.MaxActive > 0:
		if s.Desired < s.MaxActive {
			return s.Desired
		}
		return s.MaxActive
	case s.Desired > 0:
		return s.Desired
	default:
		return s.MaxActive
	}
}

func (s RequestReplicaPoolSnapshot) ActiveSaturated() bool {
	cap := s.EffectiveCap()
	return cap > 0 && s.Active >= cap
}

func (s RequestReplicaPoolSnapshot) QueueSaturated() bool {
	return s.MaxQueued >= 0 && s.Queued >= s.MaxQueued
}

type RequestReplicaProviderSnapshot struct {
	Name        string
	Inflight    int
	MaxInflight int
	Queued      int
	MaxQueued   int
	MaxWait     time.Duration
	RateLimited int64
	Errors429   int64
}

type RequestReplicaHostSnapshot struct {
	GOMAXPROCS     int
	NumGoroutine   int
	HeapAlloc      uint64
	HeapGoal       uint64
	MemoryPressure float64
}

type RequestReplicaPoolConfig struct {
	Name string

	// Optional hard ceilings. When zero, the pool derives scale and backlog
	// limits from live provider, host, and demand telemetry.
	HardMaxActive int
	HardMaxQueued int

	// Live infrastructure probes. Nil-safe.
	CurrentModel      func() string
	ProviderTelemetry func() RequestReplicaProviderSnapshot
	ContextQuota      *container.ResourceQuota
	HostTelemetry     func() RequestReplicaHostSnapshot

	ControllerTick    time.Duration
	TargetUtilization float64
	DrainWindow       time.Duration
	QueueTargetAge    time.Duration
	QueueScaleFactor  int
	QueueFloor        int
	Now               func() time.Time
}

type RequestReplicaAcquireOptions struct {
	OnQueued  func(RequestReplicaPoolSnapshot, int)
	OnGranted func(RequestReplicaPoolSnapshot, bool)

	SourceKey         string
	Priority          int
	PromptBytes       int
	DependencyCount   int
	CoAgentCount      int
	ContextFieldCount int
}

type RequestReplicaObservation struct {
	Duration    time.Duration
	TotalTokens int64
	Successful  bool
}

type requestReplicaWaiter struct {
	grant             chan requestReplicaGrant
	sourceKey         string
	priority          int
	workCost          float64
	queuedAt          time.Time
	promptBytes       int
	dependencyCount   int
	coAgentCount      int
	contextFieldCount int
}

type requestReplicaGrant struct {
	lease    *RequestReplicaLease
	snapshot RequestReplicaPoolSnapshot
}

// RequestReplicaPool is a bounded, autoscaling admission pool for forwarded
// knowledge-style requests. It grows desired parallelism from observed demand,
// queue age, live provider headroom, and host resource pressure instead of
// relying solely on fixed per-agent concurrency knobs.
type RequestReplicaPool struct {
	name   string
	config RequestReplicaPoolConfig

	mu sync.Mutex

	active          int
	desired         int
	hardCap         int
	maxQueued       int
	waiters         []*requestReplicaWaiter
	lastGrantedPrio int
	lastGrantedSrc  string

	lastArrival     time.Time
	arrivalRateEWMA float64
	serviceSamples  []time.Duration
	tokenSamples    []int64

	lastProvider RequestReplicaProviderSnapshot
	lastHost     RequestReplicaHostSnapshot

	estimatedService time.Duration
	retryAfter       time.Duration

	stopCh chan struct{}
	doneCh chan struct{}
}

func NewRequestReplicaPool(name string, maxActive, maxQueued int) *RequestReplicaPool {
	return NewAutoscalingRequestReplicaPool(RequestReplicaPoolConfig{
		Name:          name,
		HardMaxActive: maxActive,
		HardMaxQueued: maxQueued,
	})
}

func NewAutoscalingRequestReplicaPool(cfg RequestReplicaPoolConfig) *RequestReplicaPool {
	cfg = applyReplicaPoolDefaults(cfg)
	p := &RequestReplicaPool{
		name:           strings.TrimSpace(cfg.Name),
		config:         cfg,
		waiters:        make([]*requestReplicaWaiter, 0),
		serviceSamples: make([]time.Duration, 0, defaultReplicaSamples),
		tokenSamples:   make([]int64, 0, defaultReplicaSamples),
		stopCh:         make(chan struct{}),
		doneCh:         make(chan struct{}),
	}
	now := cfg.Now()
	p.recomputeTargetsLocked(now)
	go p.controlLoop()
	return p
}

func applyReplicaPoolDefaults(cfg RequestReplicaPoolConfig) RequestReplicaPoolConfig {
	cfg.Name = strings.TrimSpace(cfg.Name)
	if cfg.ControllerTick <= 0 {
		cfg.ControllerTick = defaultReplicaControllerTick
	}
	if cfg.TargetUtilization <= 0 || cfg.TargetUtilization >= 1 {
		cfg.TargetUtilization = defaultReplicaTargetUtilization
	}
	if cfg.DrainWindow <= 0 {
		cfg.DrainWindow = defaultReplicaDrainWindow
	}
	if cfg.QueueTargetAge <= 0 {
		cfg.QueueTargetAge = defaultReplicaQueueTargetAge
	}
	if cfg.QueueScaleFactor <= 0 {
		cfg.QueueScaleFactor = defaultReplicaQueueScaleFactor
	}
	if cfg.QueueFloor <= 0 {
		cfg.QueueFloor = defaultReplicaQueueFloor
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HostTelemetry == nil {
		cfg.HostTelemetry = sampleReplicaHost
	}
	return cfg
}

func (p *RequestReplicaPool) Close() {
	if p == nil {
		return
	}
	select {
	case <-p.stopCh:
		return
	default:
		close(p.stopCh)
		<-p.doneCh
	}
}

func (p *RequestReplicaPool) controlLoop() {
	ticker := time.NewTicker(p.config.ControllerTick)
	defer ticker.Stop()
	defer close(p.doneCh)
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.reconcile()
		}
	}
}

func (p *RequestReplicaPool) Snapshot() RequestReplicaPoolSnapshot {
	if p == nil {
		return RequestReplicaPoolSnapshot{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recomputeTargetsLocked(p.config.Now())
	return p.snapshotLocked()
}

func (p *RequestReplicaPool) Acquire(ctx context.Context, opts RequestReplicaAcquireOptions) (*RequestReplicaLease, error) {
	if p == nil {
		return nil, fmt.Errorf("request replica pool is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	now := p.config.Now()
	waiter := &requestReplicaWaiter{
		grant:             make(chan requestReplicaGrant, 1),
		sourceKey:         normalizeReplicaSourceKey(opts.SourceKey),
		priority:          opts.Priority,
		workCost:          estimateReplicaWorkCost(opts),
		queuedAt:          now,
		promptBytes:       max(opts.PromptBytes, 0),
		dependencyCount:   max(opts.DependencyCount, 0),
		coAgentCount:      max(opts.CoAgentCount, 0),
		contextFieldCount: max(opts.ContextFieldCount, 0),
	}

	p.mu.Lock()
	p.recordArrivalLocked(now)
	p.recomputeTargetsLocked(now)
	if len(p.waiters) == 0 && p.active < p.effectiveCapLocked() {
		p.active++
		p.recomputeTargetsLocked(now)
		snapshot := p.snapshotLocked()
		p.mu.Unlock()
		if opts.OnGranted != nil {
			opts.OnGranted(snapshot, false)
		}
		return &RequestReplicaLease{pool: p, admittedAt: now}, nil
	}
	if len(p.waiters) >= p.maxQueued {
		snapshot := p.snapshotLocked()
		p.mu.Unlock()
		return nil, &AgentBusyError{
			Target:     p.name,
			Snapshot:   snapshot,
			RetryAfter: snapshot.EstimatedRetryAfter,
			Message:    p.busyMessage(snapshot),
		}
	}

	p.waiters = append(p.waiters, waiter)
	p.recomputeTargetsLocked(now)
	grants := p.dispatchQueuedLocked(now)
	queuePosition, queued := p.queuePositionLocked(waiter)
	snapshot := p.snapshotLocked()
	p.mu.Unlock()

	deliverReplicaGrants(grants)
	if queued && opts.OnQueued != nil {
		opts.OnQueued(snapshot, queuePosition)
	}

	select {
	case grant := <-waiter.grant:
		if opts.OnGranted != nil {
			opts.OnGranted(grant.snapshot, true)
		}
		return grant.lease, nil
	case <-ctx.Done():
		if removed := p.removeWaiter(waiter); removed {
			return nil, ctx.Err()
		}
		select {
		case grant := <-waiter.grant:
			grant.lease.Release()
		default:
		}
		return nil, ctx.Err()
	}
}

func deliverReplicaGrants(grants []requestReplicaGrantDelivery) {
	for _, grant := range grants {
		grant.waiter.grant <- requestReplicaGrant{
			lease:    grant.lease,
			snapshot: grant.snapshot,
		}
	}
}

type requestReplicaGrantDelivery struct {
	waiter   *requestReplicaWaiter
	lease    *RequestReplicaLease
	snapshot RequestReplicaPoolSnapshot
}

func (p *RequestReplicaPool) removeWaiter(target *requestReplicaWaiter) bool {
	if p == nil || target == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for idx, waiter := range p.waiters {
		if waiter != target {
			continue
		}
		p.waiters = append(p.waiters[:idx], p.waiters[idx+1:]...)
		p.recomputeTargetsLocked(p.config.Now())
		return true
	}
	return false
}

func (p *RequestReplicaPool) reconcile() {
	if p == nil {
		return
	}
	now := p.config.Now()
	p.mu.Lock()
	p.recomputeTargetsLocked(now)
	grants := p.dispatchQueuedLocked(now)
	p.mu.Unlock()
	deliverReplicaGrants(grants)
}

func (p *RequestReplicaPool) release(obs RequestReplicaObservation, admittedAt time.Time) RequestReplicaPoolSnapshot {
	if p == nil {
		return RequestReplicaPoolSnapshot{}
	}
	now := p.config.Now()
	p.mu.Lock()
	if p.active > 0 {
		p.active--
	}
	if obs.Duration <= 0 && !admittedAt.IsZero() {
		obs.Duration = now.Sub(admittedAt)
	}
	p.observeCompletionLocked(obs)
	p.recomputeTargetsLocked(now)
	grants := p.dispatchQueuedLocked(now)
	snapshot := p.snapshotLocked()
	p.mu.Unlock()
	deliverReplicaGrants(grants)
	return snapshot
}

func (p *RequestReplicaPool) observeCompletionLocked(obs RequestReplicaObservation) {
	if obs.Duration > 0 {
		p.serviceSamples = appendSampleDuration(p.serviceSamples, obs.Duration)
	}
	if obs.TotalTokens > 0 {
		p.tokenSamples = appendSampleInt64(p.tokenSamples, obs.TotalTokens)
	}
}

func appendSampleDuration(samples []time.Duration, value time.Duration) []time.Duration {
	samples = append(samples, value)
	if len(samples) > defaultReplicaSamples {
		samples = append([]time.Duration(nil), samples[len(samples)-defaultReplicaSamples:]...)
	}
	return samples
}

func appendSampleInt64(samples []int64, value int64) []int64 {
	samples = append(samples, value)
	if len(samples) > defaultReplicaSamples {
		samples = append([]int64(nil), samples[len(samples)-defaultReplicaSamples:]...)
	}
	return samples
}

func (p *RequestReplicaPool) recordArrivalLocked(now time.Time) {
	if !p.lastArrival.IsZero() {
		delta := now.Sub(p.lastArrival)
		if delta > 0 {
			instRate := 1.0 / delta.Seconds()
			if p.arrivalRateEWMA == 0 {
				p.arrivalRateEWMA = instRate
			} else {
				p.arrivalRateEWMA = (0.25 * instRate) + (0.75 * p.arrivalRateEWMA)
			}
		}
	}
	p.lastArrival = now
}

func (p *RequestReplicaPool) decayArrivalRateLocked(now time.Time) {
	if p.lastArrival.IsZero() || p.arrivalRateEWMA == 0 {
		return
	}
	idle := now.Sub(p.lastArrival)
	if idle <= 0 {
		return
	}
	decayWindow := 30 * time.Second
	factor := math.Exp(-idle.Seconds() / decayWindow.Seconds())
	p.arrivalRateEWMA *= factor
	if p.arrivalRateEWMA < 0.01 {
		p.arrivalRateEWMA = 0
	}
}

func (p *RequestReplicaPool) recomputeTargetsLocked(now time.Time) {
	p.decayArrivalRateLocked(now)
	service := p.estimatedServiceLocked()
	host := p.sampleHostLocked()
	provider, rateLimitedDelta, errors429Delta := p.sampleProviderLocked()
	rawDesired := p.computeDesiredLocked(now, service)
	hardCap := p.computeHardCapLocked(host, provider, rateLimitedDelta, errors429Delta)
	if hardCap < p.active {
		hardCap = p.active
	}
	p.desired = rawDesired
	p.hardCap = hardCap
	p.maxQueued = p.computeQueueCapLocked(rawDesired, hardCap, provider)
	p.estimatedService = service
	p.retryAfter = p.estimateRetryAfterLocked(service)
	p.lastHost = host
}

func (p *RequestReplicaPool) sampleHostLocked() RequestReplicaHostSnapshot {
	if p.config.HostTelemetry == nil {
		return RequestReplicaHostSnapshot{}
	}
	return p.config.HostTelemetry()
}

func (p *RequestReplicaPool) sampleProviderLocked() (RequestReplicaProviderSnapshot, int64, int64) {
	if p.config.ProviderTelemetry == nil {
		return RequestReplicaProviderSnapshot{}, 0, 0
	}
	current := p.config.ProviderTelemetry()
	rateLimitedDelta := positiveDelta(current.RateLimited, p.lastProvider.RateLimited)
	errors429Delta := positiveDelta(current.Errors429, p.lastProvider.Errors429)
	p.lastProvider = current
	return current, rateLimitedDelta, errors429Delta
}

func positiveDelta(current, previous int64) int64 {
	if current <= previous {
		return 0
	}
	return current - previous
}

func (p *RequestReplicaPool) estimatedServiceLocked() time.Duration {
	if len(p.serviceSamples) == 0 {
		return 8 * time.Second
	}
	samples := append([]time.Duration(nil), p.serviceSamples...)
	sort.Slice(samples, func(i, j int) bool {
		return samples[i] < samples[j]
	})
	index := int(math.Ceil(float64(len(samples))*0.90)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(samples) {
		index = len(samples) - 1
	}
	if samples[index] <= 0 {
		return 8 * time.Second
	}
	return samples[index]
}

func (p *RequestReplicaPool) computeDesiredLocked(now time.Time, service time.Duration) int {
	serviceSeconds := service.Seconds()
	if serviceSeconds <= 0 {
		serviceSeconds = 1
	}

	desired := 0
	if p.arrivalRateEWMA > 0 {
		steady := int(math.Ceil((p.arrivalRateEWMA * serviceSeconds) / p.config.TargetUtilization))
		desired = max(desired, steady)
	}
	desired = max(desired, p.active)

	if len(p.waiters) > 0 {
		desired = max(desired, p.active+1)
		weightedQueue := p.weightedQueueLocked()
		drain := int(math.Ceil((weightedQueue * serviceSeconds) / p.config.DrainWindow.Seconds()))
		if drain < 1 {
			drain = 1
		}
		desired = max(desired, p.active+drain)

		oldest := p.oldestQueuedAgeLocked(now)
		if oldest > p.config.QueueTargetAge {
			extra := int(math.Ceil(float64(oldest) / float64(maxDuration(service, time.Second))))
			if extra < 1 {
				extra = 1
			}
			if extra > 3 {
				extra = 3
			}
			desired = max(desired, p.active+extra)
		}
	}

	if desired == 0 && (p.active > 0 || len(p.waiters) > 0) {
		return 1
	}
	if desired == 0 && !p.lastArrival.IsZero() && now.Sub(p.lastArrival) <= service {
		return 1
	}
	return desired
}

func (p *RequestReplicaPool) computeHardCapLocked(host RequestReplicaHostSnapshot, provider RequestReplicaProviderSnapshot, rateLimitedDelta, errors429Delta int64) int {
	cap := p.autoCPUCapLocked(host)

	if quota := p.config.ContextQuota; quota != nil {
		usage := quota.Usage()
		if usage.GoroutineLimit > 0 {
			remaining := usage.GoroutineLimit - usage.GoroutineUsed
			goroutineCap := p.active
			if remaining > 0 {
				goroutineCap += int(remaining / defaultReplicaGoroutines)
			}
			cap = minPositive(cap, goroutineCap)
		}
		if usage.ContextHardCapacity > 0 {
			ratio := float64(usage.ContextWindowUsed) / float64(usage.ContextHardCapacity)
			switch {
			case ratio >= 0.95:
				cap = minPositive(cap, p.active)
			case ratio >= 0.85:
				cap = minPositive(cap, max(p.active, 1))
			}
		}
	}

	switch {
	case host.MemoryPressure >= 0.92:
		cap = minPositive(cap, p.active)
	case host.MemoryPressure >= 0.85:
		cap = minPositive(cap, max(p.active, 1))
	case host.MemoryPressure >= 0.75:
		cap = minPositive(cap, max(1, cap/2))
	}

	if provider.MaxInflight > 0 {
		remaining := provider.MaxInflight - provider.Inflight
		if remaining < 0 {
			remaining = 0
		}
		providerCap := p.active + remaining
		if provider.Queued > 0 {
			providerCap = minPositive(providerCap, p.active+1)
		}
		if rateLimitedDelta > 0 || errors429Delta > 0 {
			providerCap = minPositive(providerCap, p.active)
		}
		cap = minPositive(cap, providerCap)
	}

	if p.config.HardMaxActive > 0 {
		cap = minPositive(cap, p.config.HardMaxActive)
	}
	if cap < p.active {
		return p.active
	}
	return max(cap, 0)
}

func (p *RequestReplicaPool) autoCPUCapLocked(host RequestReplicaHostSnapshot) int {
	procs := host.GOMAXPROCS
	if procs <= 0 {
		procs = runtime.GOMAXPROCS(0)
	}
	cap := procs * defaultReplicaCPUFactor
	if cap <= 0 {
		cap = 1
	}
	return cap
}

func (p *RequestReplicaPool) computeQueueCapLocked(desired, hardCap int, provider RequestReplicaProviderSnapshot) int {
	base := max(desired, hardCap)
	if base <= 0 {
		base = 1
	}
	queueCap := max(p.config.QueueFloor, base*p.config.QueueScaleFactor)
	if provider.MaxQueued > 0 {
		queueCap = minPositive(queueCap, provider.MaxQueued*2)
	}
	if p.config.HardMaxQueued > 0 {
		queueCap = minPositive(queueCap, p.config.HardMaxQueued)
	}
	return max(queueCap, 1)
}

func (p *RequestReplicaPool) estimateRetryAfterLocked(service time.Duration) time.Duration {
	if service <= 0 {
		service = p.estimatedServiceLocked()
	}
	capacity := p.effectiveCapLocked()
	if capacity <= 0 {
		capacity = 1
	}
	weightedQueue := p.weightedQueueLocked()
	wait := time.Duration(math.Ceil(weightedQueue/float64(capacity)) * float64(service))
	if wait < p.config.ControllerTick {
		wait = p.config.ControllerTick
	}
	if wait > defaultReplicaRetryCeiling {
		wait = defaultReplicaRetryCeiling
	}
	return wait
}

func (p *RequestReplicaPool) weightedQueueLocked() float64 {
	total := 0.0
	for _, waiter := range p.waiters {
		total += waiter.workCost
	}
	return total
}

func (p *RequestReplicaPool) oldestQueuedAgeLocked(now time.Time) time.Duration {
	if len(p.waiters) == 0 {
		return 0
	}
	oldest := now.Sub(p.waiters[0].queuedAt)
	for _, waiter := range p.waiters[1:] {
		age := now.Sub(waiter.queuedAt)
		if age > oldest {
			oldest = age
		}
	}
	return oldest
}

func (p *RequestReplicaPool) effectiveCapLocked() int {
	switch {
	case p.desired > 0 && p.hardCap > 0:
		if p.desired < p.hardCap {
			return p.desired
		}
		return p.hardCap
	case p.desired > 0:
		return p.desired
	default:
		return p.hardCap
	}
}

func (p *RequestReplicaPool) dispatchQueuedLocked(now time.Time) []requestReplicaGrantDelivery {
	effectiveCap := p.effectiveCapLocked()
	if effectiveCap <= p.active || len(p.waiters) == 0 {
		return nil
	}
	grants := make([]requestReplicaGrantDelivery, 0, effectiveCap-p.active)
	for p.active < effectiveCap && len(p.waiters) > 0 {
		idx := p.selectNextWaiterLocked()
		if idx < 0 || idx >= len(p.waiters) {
			break
		}
		waiter := p.waiters[idx]
		p.waiters = append(p.waiters[:idx], p.waiters[idx+1:]...)
		p.active++
		p.recomputeTargetsLocked(now)
		snapshot := p.snapshotLocked()
		grants = append(grants, requestReplicaGrantDelivery{
			waiter:   waiter,
			lease:    &RequestReplicaLease{pool: p, admittedAt: now},
			snapshot: snapshot,
		})
		p.lastGrantedSrc = waiter.sourceKey
		p.lastGrantedPrio = waiter.priority
	}
	return grants
}

func (p *RequestReplicaPool) selectNextWaiterLocked() int {
	if len(p.waiters) == 0 {
		return -1
	}
	bestPriority := p.waiters[0].priority
	for _, waiter := range p.waiters[1:] {
		if waiter.priority > bestPriority {
			bestPriority = waiter.priority
		}
	}

	sourceOrder := make([]string, 0, len(p.waiters))
	headIndex := make(map[string]int, len(p.waiters))
	for idx, waiter := range p.waiters {
		if waiter.priority != bestPriority {
			continue
		}
		if _, ok := headIndex[waiter.sourceKey]; ok {
			continue
		}
		headIndex[waiter.sourceKey] = idx
		sourceOrder = append(sourceOrder, waiter.sourceKey)
	}
	if len(sourceOrder) == 0 {
		return 0
	}
	if len(sourceOrder) == 1 {
		return headIndex[sourceOrder[0]]
	}

	start := 0
	if p.lastGrantedPrio == bestPriority && p.lastGrantedSrc != "" {
		for idx, source := range sourceOrder {
			if source == p.lastGrantedSrc {
				start = (idx + 1) % len(sourceOrder)
				break
			}
		}
	}
	return headIndex[sourceOrder[start]]
}

func (p *RequestReplicaPool) queuePositionLocked(target *requestReplicaWaiter) (int, bool) {
	if target == nil {
		return 0, false
	}
	for idx, waiter := range p.waiters {
		if waiter == target {
			return idx + 1, true
		}
	}
	return 0, false
}

func (p *RequestReplicaPool) snapshotLocked() RequestReplicaPoolSnapshot {
	return RequestReplicaPoolSnapshot{
		Active:              p.active,
		Desired:             p.desired,
		MaxActive:           p.hardCap,
		Queued:              len(p.waiters),
		MaxQueued:           p.maxQueued,
		EstimatedService:    p.estimatedService,
		EstimatedRetryAfter: p.retryAfter,
		MemoryPressure:      p.lastHost.MemoryPressure,
		ProviderInflight:    p.lastProvider.Inflight,
		ProviderMaxInflight: p.lastProvider.MaxInflight,
	}
}

func (p *RequestReplicaPool) busyMessage(snapshot RequestReplicaPoolSnapshot) string {
	target := strings.TrimSpace(p.name)
	if target == "" {
		target = "agent"
	}
	display := AgentDisplayName(strings.ToLower(target))
	return fmt.Sprintf(
		"%s is busy (%d/%d active, %d/%d queued).",
		display,
		snapshot.Active,
		max(snapshot.EffectiveCap(), snapshot.MaxActive),
		snapshot.Queued,
		snapshot.MaxQueued,
	)
}

type RequestReplicaLease struct {
	pool        *RequestReplicaPool
	admittedAt  time.Time
	releaseOnce sync.Once
}

func (l *RequestReplicaLease) Release() RequestReplicaPoolSnapshot {
	return l.ReleaseWithObservation(RequestReplicaObservation{})
}

func (l *RequestReplicaLease) ReleaseWithObservation(obs RequestReplicaObservation) RequestReplicaPoolSnapshot {
	if l == nil || l.pool == nil {
		return RequestReplicaPoolSnapshot{}
	}
	snapshot := RequestReplicaPoolSnapshot{}
	l.releaseOnce.Do(func() {
		snapshot = l.pool.release(obs, l.admittedAt)
	})
	return snapshot
}

// AgentBusyError indicates that a request could not be admitted because the
// target agent's bounded backlog is already saturated. It is retryable.
type AgentBusyError struct {
	Target     string
	Snapshot   RequestReplicaPoolSnapshot
	RetryAfter time.Duration
	Message    string
}

func (e *AgentBusyError) Error() string {
	if e == nil {
		return "agent is busy"
	}
	if msg := strings.TrimSpace(e.Message); msg != "" {
		return msg
	}
	target := strings.TrimSpace(e.Target)
	if target == "" {
		target = "agent"
	}
	return fmt.Sprintf("%s is busy", target)
}

func IsAgentBusyError(err error) bool {
	if err == nil {
		return false
	}
	var busyErr *AgentBusyError
	return errors.As(err, &busyErr)
}

func AgentBusyDisplayMessage(target string, snapshot RequestReplicaPoolSnapshot) string {
	display := AgentDisplayName(strings.ToLower(strings.TrimSpace(target)))
	if display == "" {
		display = "Agent"
	}
	return fmt.Sprintf(
		"%s is currently busy (%d/%d active, %d/%d queued).",
		display,
		snapshot.Active,
		max(snapshot.EffectiveCap(), snapshot.MaxActive),
		snapshot.Queued,
		snapshot.MaxQueued,
	)
}

func KnowledgeQueueProgressMessage(target string, snapshot RequestReplicaPoolSnapshot, queuePosition int) string {
	display := AgentDisplayName(strings.ToLower(strings.TrimSpace(target)))
	if display == "" {
		display = "Agent"
	}
	eta := ""
	if snapshot.EstimatedRetryAfter > 0 {
		eta = fmt.Sprintf(", estimated wait %s", snapshot.EstimatedRetryAfter.Round(time.Second))
	}
	if queuePosition > 0 {
		return fmt.Sprintf(
			"%s is busy. Waiting for an available replica (%d/%d active, queue position %d, %d/%d queued%s).",
			display,
			snapshot.Active,
			max(snapshot.EffectiveCap(), snapshot.MaxActive),
			queuePosition,
			snapshot.Queued,
			snapshot.MaxQueued,
			eta,
		)
	}
	return fmt.Sprintf(
		"%s is busy. Waiting for an available replica (%d/%d active, %d/%d queued%s).",
		display,
		snapshot.Active,
		max(snapshot.EffectiveCap(), snapshot.MaxActive),
		snapshot.Queued,
		snapshot.MaxQueued,
		eta,
	)
}

// BusyRetryPolicy bounds how aggressively consult/challenge routes retry after
// a saturated backlog rejects an attempt.
type BusyRetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

func DefaultBusyRetryPolicy(target string) BusyRetryPolicy {
	target = strings.ToLower(strings.TrimSpace(target))
	switch target {
	case "librarian", "academic", "archivalist":
		return BusyRetryPolicy{
			MaxAttempts: 6,
			BaseDelay:   2 * time.Second,
			MaxDelay:    20 * time.Second,
		}
	}
	return BusyRetryPolicy{
		MaxAttempts: 4,
		BaseDelay:   2 * time.Second,
		MaxDelay:    12 * time.Second,
	}
}

func (p BusyRetryPolicy) delayForAttempt(attempt int) time.Duration {
	if p.BaseDelay <= 0 {
		p.BaseDelay = 2 * time.Second
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = 12 * time.Second
	}
	if attempt <= 1 {
		return p.BaseDelay
	}
	delay := p.BaseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= p.MaxDelay {
			return p.MaxDelay
		}
	}
	return delay
}

func RetryBusyRouteRequest(
	ctx context.Context,
	target string,
	policy BusyRetryPolicy,
	attempt func(context.Context, int) (*guide.Message, error),
) (*guide.Message, error) {
	if attempt == nil {
		return nil, fmt.Errorf("route attempt is required")
	}
	if policy.MaxAttempts <= 0 {
		policy = DefaultBusyRetryPolicy(target)
	}

	var lastErr error
	for index := 1; index <= policy.MaxAttempts; index++ {
		msg, err := attempt(ctx, index)
		if err == nil {
			return msg, nil
		}
		if !IsAgentBusyError(err) {
			return nil, err
		}
		lastErr = err
		if index >= policy.MaxAttempts {
			break
		}
		delay := policy.delayForAttempt(index)
		var busyErr *AgentBusyError
		if errors.As(err, &busyErr) && busyErr.RetryAfter > delay {
			delay = busyErr.RetryAfter
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func normalizeReplicaSourceKey(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "default"
	}
	return source
}

func estimateReplicaWorkCost(opts RequestReplicaAcquireOptions) float64 {
	cost := 1.0
	if opts.PromptBytes > 0 {
		cost += math.Min(float64(opts.PromptBytes)/2048.0, 4.0)
	}
	if opts.DependencyCount > 0 {
		cost += math.Min(float64(opts.DependencyCount)*0.20, 1.0)
	}
	if opts.CoAgentCount > 0 {
		cost += math.Min(float64(opts.CoAgentCount)*0.15, 0.75)
	}
	if opts.ContextFieldCount > 0 {
		cost += math.Min(float64(opts.ContextFieldCount)*0.05, 0.5)
	}
	return cost
}

func sampleReplicaHost() RequestReplicaHostSnapshot {
	host := RequestReplicaHostSnapshot{
		GOMAXPROCS:   runtime.GOMAXPROCS(0),
		NumGoroutine: runtime.NumGoroutine(),
	}

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	host.HeapAlloc = memStats.HeapAlloc
	host.HeapGoal = memStats.NextGC

	samples := []rtmetrics.Sample{{Name: "/gc/heap/goal:bytes"}}
	rtmetrics.Read(samples)
	if samples[0].Value.Kind() == rtmetrics.KindUint64 {
		host.HeapGoal = samples[0].Value.Uint64()
	}
	if host.HeapGoal > 0 {
		host.MemoryPressure = float64(host.HeapAlloc) / float64(host.HeapGoal)
	}
	return host
}

func RequestReplicaPriorityForForwardedRequest(fwd *guide.ForwardedRequest) int {
	if fwd == nil {
		return 40
	}
	if strings.EqualFold(strings.TrimSpace(fwd.SourceAgentID), "tui") {
		return 100
	}
	if strings.TrimSpace(fwd.ParentCorrelationID) == "" {
		return 80
	}
	return 40
}

func RequestReplicaSourceKeyForForwardedRequest(fwd *guide.ForwardedRequest) string {
	if fwd == nil {
		return "default"
	}
	for _, key := range []string{"pipeline_id", "task_id"} {
		if value := metadataString(fwd.Metadata, key); value != "" {
			return value
		}
	}
	if sessionID := strings.TrimSpace(fwd.SessionID); sessionID != "" {
		return sessionID
	}
	if source := strings.TrimSpace(fwd.SourceAgentID); source != "" {
		return source
	}
	return "default"
}

func RequestReplicaPromptBytesForForwardedRequest(fwd *guide.ForwardedRequest) int {
	if fwd == nil {
		return 0
	}
	return len(strings.TrimSpace(fwd.Input))
}

func RequestReplicaContextFieldCount(fwd *guide.ForwardedRequest) int {
	if fwd == nil {
		return 0
	}
	count := len(fwd.Metadata) + len(fwd.ConversationHistory)
	if fwd.Entities != nil {
		count++
	}
	if fwd.CrossDomain != nil {
		count++
	}
	return count
}

type requestReplicaGatewayTelemetry interface {
	GatewayMetrics() gateway.MetricsSnapshot
	GatewayCapacity() gateway.CapacitySnapshot
}

func RequestReplicaProviderSnapshotFromProvider(provider any) RequestReplicaProviderSnapshot {
	if provider == nil {
		return RequestReplicaProviderSnapshot{}
	}
	telemetry, ok := provider.(requestReplicaGatewayTelemetry)
	if !ok {
		return RequestReplicaProviderSnapshot{}
	}
	metrics := telemetry.GatewayMetrics()
	capacity := telemetry.GatewayCapacity()
	return RequestReplicaProviderSnapshot{
		Name:        strings.TrimSpace(capacity.Name),
		Inflight:    int(metrics.Inflight),
		MaxInflight: max(capacity.MaxConcurrentRequests, 0),
		Queued:      int(metrics.Queued),
		MaxQueued:   max(capacity.MaxQueueSize, 0),
		MaxWait:     capacity.MaxWaitTime,
		RateLimited: metrics.RateLimited,
		Errors429:   metrics.Errors429,
	}
}

func metadataString(data map[string]any, key string) string {
	if len(data) == 0 {
		return ""
	}
	value, _ := data[key].(string)
	return strings.TrimSpace(value)
}

func minPositive(a, b int) int {
	switch {
	case a <= 0:
		return b
	case b <= 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

func maxDuration(values ...time.Duration) time.Duration {
	var best time.Duration
	for _, value := range values {
		if value > best {
			best = value
		}
	}
	return best
}
