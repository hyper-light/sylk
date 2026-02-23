package container

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

var (
	ErrQuotaExceeded = errors.New("resource quota exceeded")
	ErrLimitExceeded = errors.New("container limit range exceeded")
)

// ResourceQuota enforces aggregate resource limits across all containers
// in a namespace (session). Thread-safe.
type ResourceQuota struct {
	mu sync.RWMutex

	goroutineLimit     int64
	goroutineUsed      atomic.Int64
	contextWindowLimit int64
	contextWindowUsed  atomic.Int64
	vfsQuotaLimit      int64
	vfsQuotaUsed       atomic.Int64
	containerLimit     int64
	containerCount     atomic.Int64
}

// ResourceQuotaConfig configures the quota limits.
type ResourceQuotaConfig struct {
	GoroutineLimit     int64
	ContextWindowLimit int64
	VFSQuotaLimit      int64
	ContainerLimit     int64
}

// NewResourceQuota creates a quota with the given limits.
func NewResourceQuota(cfg ResourceQuotaConfig) *ResourceQuota {
	return &ResourceQuota{
		goroutineLimit:     cfg.GoroutineLimit,
		contextWindowLimit: cfg.ContextWindowLimit,
		vfsQuotaLimit:      cfg.VFSQuotaLimit,
		containerLimit:     cfg.ContainerLimit,
	}
}

// CheckContainerFits verifies that adding a container with the given spec
// would not exceed the quota. Does not consume quota — call Reserve after.
func (q *ResourceQuota) CheckContainerFits(spec *ContainerSpec) error {
	if err := q.checkGoroutines(spec.Resources.GoroutineLimit); err != nil {
		return err
	}
	if err := q.checkContextWindow(spec.Resources.ContextWindowLimit); err != nil {
		return err
	}
	if err := q.checkVFS(spec.Resources.VFSQuotaBytes); err != nil {
		return err
	}
	return q.checkContainerCount()
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

func (q *ResourceQuota) checkContextWindow(request int) error {
	if q.contextWindowLimit <= 0 || request <= 0 {
		return nil
	}
	if q.contextWindowUsed.Load()+int64(request) > q.contextWindowLimit {
		return fmt.Errorf("%w: context window %d + %d > %d", ErrQuotaExceeded,
			q.contextWindowUsed.Load(), request, q.contextWindowLimit)
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

func (q *ResourceQuota) checkContainerCount() error {
	if q.containerLimit <= 0 {
		return nil
	}
	if q.containerCount.Load()+1 > q.containerLimit {
		return fmt.Errorf("%w: container count %d >= %d", ErrQuotaExceeded,
			q.containerCount.Load(), q.containerLimit)
	}
	return nil
}

// Reserve consumes quota for a container's resources. Call Release when
// the container is removed.
func (q *ResourceQuota) Reserve(spec *ContainerSpec) {
	q.goroutineUsed.Add(spec.Resources.GoroutineLimit)
	q.contextWindowUsed.Add(int64(spec.Resources.ContextWindowLimit))
	q.vfsQuotaUsed.Add(spec.Resources.VFSQuotaBytes)
	q.containerCount.Add(1)
}

// Release returns quota consumed by a container.
func (q *ResourceQuota) Release(spec *ContainerSpec) {
	q.goroutineUsed.Add(-spec.Resources.GoroutineLimit)
	q.contextWindowUsed.Add(-int64(spec.Resources.ContextWindowLimit))
	q.vfsQuotaUsed.Add(-spec.Resources.VFSQuotaBytes)
	q.containerCount.Add(-1)
}

// Usage returns current quota usage.
func (q *ResourceQuota) Usage() ResourceQuotaUsage {
	return ResourceQuotaUsage{
		GoroutineUsed:     q.goroutineUsed.Load(),
		GoroutineLimit:    q.goroutineLimit,
		ContextWindowUsed: q.contextWindowUsed.Load(),
		ContextWindowLimit: q.contextWindowLimit,
		VFSUsed:           q.vfsQuotaUsed.Load(),
		VFSLimit:          q.vfsQuotaLimit,
		ContainerCount:    q.containerCount.Load(),
		ContainerLimit:    q.containerLimit,
	}
}

// ResourceQuotaUsage is a snapshot of quota usage.
type ResourceQuotaUsage struct {
	GoroutineUsed      int64
	GoroutineLimit     int64
	ContextWindowUsed  int64
	ContextWindowLimit int64
	VFSUsed            int64
	VFSLimit           int64
	ContainerCount     int64
	ContainerLimit     int64
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
