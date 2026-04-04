package container

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrQuotaExceeded = errors.New("resource quota exceeded")
	ErrLimitExceeded = errors.New("container limit range exceeded")
)

// ResourceQuota enforces aggregate resource limits across all containers
// in a namespace (session). Thread-safe.
type ResourceQuota struct {
	mu sync.RWMutex

	goroutineLimit           int64
	goroutineUsed            atomic.Int64
	contextWindowRequest     int64
	contextWindowLimit       int64
	contextWindowUsed        atomic.Int64
	contextSoftCapacity      atomic.Int64
	contextBurstCapacity     atomic.Int64
	contextHardCapacity      atomic.Int64
	vfsQuotaLimit            int64
	vfsQuotaUsed             atomic.Int64
	containerLimit           int64
	containerCount           atomic.Int64
	containerSoftCapacity    atomic.Int64
	containerBurstCapacity   atomic.Int64
	containerHardCapacity    atomic.Int64
	containerReleaseHalfLife time.Duration
	containerReleaseCredit   float64
	containerLastEvent       time.Time

	contextLeaseMin        time.Duration
	contextReleaseHalfLife time.Duration
	contextShortWindow     time.Duration
	contextLongWindow      time.Duration
	contextAdmissionTick   time.Duration

	contextMatcher ContextArchetypeMatcher
	contextClaims  map[string]*contextClaim
	contextAliases map[string]string
	contextWaiters map[string]time.Time
	contextNotify  chan struct{}
}

// ResourceQuotaConfig configures the quota limits.
type ResourceQuotaConfig struct {
	GoroutineLimit           int64
	ContextWindowRequest     int64
	ContextWindowLimit       int64
	VFSQuotaLimit            int64
	ContainerLimit           int64
	ContainerReleaseHalfLife time.Duration

	ContextLeaseMin        time.Duration
	ContextReleaseHalfLife time.Duration
	ContextShortWindow     time.Duration
	ContextLongWindow      time.Duration
	ContextAdmissionTick   time.Duration
}

// NewResourceQuota creates a quota with the given limits.
func NewResourceQuota(cfg ResourceQuotaConfig) *ResourceQuota {
	applyContextQuotaDefaults(&cfg)
	applyContainerQuotaDefaults(&cfg)
	q := &ResourceQuota{
		goroutineLimit:           cfg.GoroutineLimit,
		contextWindowRequest:     cfg.ContextWindowRequest,
		contextWindowLimit:       cfg.ContextWindowLimit,
		vfsQuotaLimit:            cfg.VFSQuotaLimit,
		containerLimit:           cfg.ContainerLimit,
		containerReleaseHalfLife: cfg.ContainerReleaseHalfLife,
		contextLeaseMin:          cfg.ContextLeaseMin,
		contextReleaseHalfLife:   cfg.ContextReleaseHalfLife,
		contextShortWindow:       cfg.ContextShortWindow,
		contextLongWindow:        cfg.ContextLongWindow,
		contextAdmissionTick:     cfg.ContextAdmissionTick,
		contextClaims:            make(map[string]*contextClaim),
		contextAliases:           make(map[string]string),
		contextWaiters:           make(map[string]time.Time),
		contextNotify:            make(chan struct{}),
	}
	q.contextSoftCapacity.Store(cfg.ContextWindowRequest)
	q.contextBurstCapacity.Store(cfg.ContextWindowLimit)
	q.contextHardCapacity.Store(cfg.ContextWindowLimit)
	q.containerSoftCapacity.Store(cfg.ContainerLimit)
	q.containerBurstCapacity.Store(cfg.ContainerLimit)
	q.containerHardCapacity.Store(cfg.ContainerLimit)
	return q
}

func applyContextQuotaDefaults(cfg *ResourceQuotaConfig) {
	if cfg.ContextWindowRequest <= 0 && cfg.ContextWindowLimit > 0 {
		cfg.ContextWindowRequest = cfg.ContextWindowLimit * 3 / 4
	}
	if cfg.ContextLeaseMin <= 0 {
		// Typical tool-driven turns stabilize within a few seconds. Give an
		// admitted turn breathing room for one meaningful exchange.
		cfg.ContextLeaseMin = 8 * time.Second
	}
	if cfg.ContextReleaseHalfLife <= 0 {
		cfg.ContextReleaseHalfLife = cfg.ContextLeaseMin * 3 / 2
	}
	if cfg.ContextShortWindow <= 0 {
		cfg.ContextShortWindow = cfg.ContextReleaseHalfLife * 2
	}
	if cfg.ContextLongWindow <= 0 {
		cfg.ContextLongWindow = cfg.ContextShortWindow * 4
	}
	if cfg.ContextAdmissionTick <= 0 {
		cfg.ContextAdmissionTick = cfg.ContextLeaseMin / 8
		if cfg.ContextAdmissionTick > time.Second {
			cfg.ContextAdmissionTick = time.Second
		}
		if cfg.ContextAdmissionTick <= 0 {
			cfg.ContextAdmissionTick = 250 * time.Millisecond
		}
	}
}

func applyContainerQuotaDefaults(cfg *ResourceQuotaConfig) {
	if cfg.ContainerReleaseHalfLife <= 0 {
		// Container churn is materially slower than tool-turn context churn.
		// Keep recent release credit around long enough to absorb adjacent
		// pipeline task transitions without pinning the hard count.
		cfg.ContainerReleaseHalfLife = 30 * time.Second
	}
}

// CheckContainerFits verifies that adding a container with the given spec
// would not exceed the quota. Does not consume quota — call Reserve after.
func (q *ResourceQuota) CheckContainerFits(spec *ContainerSpec) error {
	if err := q.checkGoroutines(spec.Resources.GoroutineLimit); err != nil {
		return err
	}
	if err := q.checkVFS(spec.Resources.VFSQuotaBytes); err != nil {
		return err
	}
	return q.checkContainerCount(spec)
}

func (q *ResourceQuota) checkGoroutines(request int64) error {
	if q.goroutineLimit <= 0 || request <= 0 {
		return nil
	}
	if q.goroutineUsed.Load()+request > q.goroutineLimit {
		return fmt.Errorf("%w: goroutines %d + %d > %d", ErrQuotaExceeded,
			q.goroutineUsed.Load(), request, q.goroutineLimit)
	}
	return nil
}

func (q *ResourceQuota) checkVFS(request int64) error {
	if q.vfsQuotaLimit <= 0 || request <= 0 {
		return nil
	}
	if q.vfsQuotaUsed.Load()+request > q.vfsQuotaLimit {
		return fmt.Errorf("%w: vfs %d + %d > %d", ErrQuotaExceeded,
			q.vfsQuotaUsed.Load(), request, q.vfsQuotaLimit)
	}
	return nil
}

func (q *ResourceQuota) checkContainerCount(spec *ContainerSpec) error {
	if q.containerLimit <= 0 {
		return nil
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	soft, burst, hard := q.containerCapacitiesLocked(now, spec)
	q.containerSoftCapacity.Store(soft)
	q.containerBurstCapacity.Store(burst)
	q.containerHardCapacity.Store(hard)

	current := q.containerCount.Load()
	if current+1 > hard {
		return fmt.Errorf("%w: container count %d >= %d", ErrQuotaExceeded, current, hard)
	}
	return nil
}

// Reserve consumes quota for a container's resources. Call Release when
// the container is removed.
func (q *ResourceQuota) Reserve(spec *ContainerSpec) {
	q.goroutineUsed.Add(spec.Resources.GoroutineLimit)
	q.vfsQuotaUsed.Add(spec.Resources.VFSQuotaBytes)
	q.containerCount.Add(1)
	q.mu.Lock()
	defer q.mu.Unlock()
	q.adjustContainerReleaseCreditLocked(time.Now(), -1.0)
	soft, burst, hard := q.containerCapacitiesLocked(time.Now(), spec)
	q.containerSoftCapacity.Store(soft)
	q.containerBurstCapacity.Store(burst)
	q.containerHardCapacity.Store(hard)
}

// Release returns quota consumed by a container.
func (q *ResourceQuota) Release(spec *ContainerSpec) {
	q.goroutineUsed.Add(-spec.Resources.GoroutineLimit)
	q.vfsQuotaUsed.Add(-spec.Resources.VFSQuotaBytes)
	q.containerCount.Add(-1)
	q.mu.Lock()
	defer q.mu.Unlock()
	q.adjustContainerReleaseCreditLocked(time.Now(), 1.0)
	soft, burst, hard := q.containerCapacitiesLocked(time.Now(), nil)
	q.containerSoftCapacity.Store(soft)
	q.containerBurstCapacity.Store(burst)
	q.containerHardCapacity.Store(hard)
}

// Usage returns current quota usage.
func (q *ResourceQuota) Usage() ResourceQuotaUsage {
	if q != nil {
		q.mu.Lock()
		now := time.Now()
		if q.contextWindowLimit > 0 {
			q.recomputeContextStateLocked(now)
		}
		soft, burst, hard := q.containerCapacitiesLocked(now, nil)
		q.containerSoftCapacity.Store(soft)
		q.containerBurstCapacity.Store(burst)
		q.containerHardCapacity.Store(hard)
		q.mu.Unlock()
	}
	return ResourceQuotaUsage{
		GoroutineUsed:          q.goroutineUsed.Load(),
		GoroutineLimit:         q.goroutineLimit,
		ContextWindowUsed:      q.contextWindowUsed.Load(),
		ContextWindowRequest:   q.contextWindowRequest,
		ContextWindowLimit:     q.contextWindowLimit,
		ContextSoftCapacity:    q.contextSoftCapacity.Load(),
		ContextBurstCapacity:   q.contextBurstCapacity.Load(),
		ContextHardCapacity:    q.contextHardCapacity.Load(),
		VFSUsed:                q.vfsQuotaUsed.Load(),
		VFSLimit:               q.vfsQuotaLimit,
		ContainerCount:         q.containerCount.Load(),
		ContainerLimit:         q.containerLimit,
		ContainerSoftCapacity:  q.containerSoftCapacity.Load(),
		ContainerBurstCapacity: q.containerBurstCapacity.Load(),
		ContainerHardCapacity:  q.containerHardCapacity.Load(),
		ContextWaiterCount:     q.contextWaiterCount(),
	}
}

// ResourceQuotaUsage is a snapshot of quota usage.
type ResourceQuotaUsage struct {
	GoroutineUsed          int64
	GoroutineLimit         int64
	ContextWindowUsed      int64
	ContextWindowRequest   int64
	ContextWindowLimit     int64
	ContextSoftCapacity    int64
	ContextBurstCapacity   int64
	ContextHardCapacity    int64
	VFSUsed                int64
	VFSLimit               int64
	ContainerCount         int64
	ContainerLimit         int64
	ContainerSoftCapacity  int64
	ContainerBurstCapacity int64
	ContainerHardCapacity  int64
	ContextWaiterCount     int
}

// LimitRange defines per-container resource bounds.
type LimitRange struct {
	MinGoroutines    int64
	MaxGoroutines    int64
	MinContextWindow int
	MaxContextWindow int
	MaxVFSQuota      int64
}

// CheckContainerLimits verifies a container's resources are within range.
func (lr *LimitRange) CheckContainerLimits(spec *ContainerSpec) error {
	r := &spec.Resources
	if err := lr.checkGoroutineRange(r.GoroutineLimit); err != nil {
		return err
	}
	if err := lr.checkContextRange(r.ContextWindowLimit); err != nil {
		return err
	}
	return lr.checkVFSRange(r.VFSQuotaBytes)
}

func (lr *LimitRange) checkGoroutineRange(limit int64) error {
	if lr.MinGoroutines > 0 && limit > 0 && limit < lr.MinGoroutines {
		return fmt.Errorf("%w: goroutines %d < min %d", ErrLimitExceeded, limit, lr.MinGoroutines)
	}
	if lr.MaxGoroutines > 0 && limit > lr.MaxGoroutines {
		return fmt.Errorf("%w: goroutines %d > max %d", ErrLimitExceeded, limit, lr.MaxGoroutines)
	}
	return nil
}

func (lr *LimitRange) checkContextRange(limit int) error {
	if lr.MinContextWindow > 0 && limit > 0 && limit < lr.MinContextWindow {
		return fmt.Errorf("%w: context window %d < min %d", ErrLimitExceeded, limit, lr.MinContextWindow)
	}
	if lr.MaxContextWindow > 0 && limit > lr.MaxContextWindow {
		return fmt.Errorf("%w: context window %d > max %d", ErrLimitExceeded, limit, lr.MaxContextWindow)
	}
	return nil
}

func (lr *LimitRange) checkVFSRange(limit int64) error {
	if lr.MaxVFSQuota > 0 && limit > lr.MaxVFSQuota {
		return fmt.Errorf("%w: vfs %d > max %d", ErrLimitExceeded, limit, lr.MaxVFSQuota)
	}
	return nil
}
