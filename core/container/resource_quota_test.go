package container

import (
	"errors"
	"testing"
	"time"
)

func TestResourceQuota_FitsWhenUnderLimit(t *testing.T) {
	q := NewResourceQuota(ResourceQuotaConfig{
		GoroutineLimit: 100,
		ContainerLimit: 10,
	})

	spec := &ContainerSpec{
		Resources: ResourceSpec{GoroutineLimit: 10},
	}
	if err := q.CheckContainerFits(spec); err != nil {
		t.Fatalf("should fit: %v", err)
	}
}

func TestResourceQuota_ExceedsGoroutineLimit(t *testing.T) {
	q := NewResourceQuota(ResourceQuotaConfig{
		GoroutineLimit: 20,
	})

	spec := &ContainerSpec{
		Resources: ResourceSpec{GoroutineLimit: 10},
	}
	q.Reserve(spec)

	overSpec := &ContainerSpec{
		Resources: ResourceSpec{GoroutineLimit: 15},
	}
	err := q.CheckContainerFits(overSpec)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
}

func TestResourceQuota_ExceedsContainerLimit(t *testing.T) {
	q := NewResourceQuota(ResourceQuotaConfig{
		ContainerLimit: 2,
	})

	spec := &ContainerSpec{}
	q.Reserve(spec)
	q.Reserve(spec)

	err := q.CheckContainerFits(spec)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
}

func TestResourceQuota_ReleaseFreesSpace(t *testing.T) {
	q := NewResourceQuota(ResourceQuotaConfig{
		ContainerLimit: 1,
	})

	spec := &ContainerSpec{}
	q.Reserve(spec)

	err := q.CheckContainerFits(spec)
	if err == nil {
		t.Fatal("should be at limit")
	}

	q.Release(spec)

	if err := q.CheckContainerFits(spec); err != nil {
		t.Fatalf("should fit after release: %v", err)
	}
}

func TestResourceQuota_Usage(t *testing.T) {
	q := NewResourceQuota(ResourceQuotaConfig{
		GoroutineLimit:       100,
		ContextWindowRequest: 40000,
		ContextWindowLimit:   50000,
		VFSQuotaLimit:        1024,
		ContainerLimit:       10,
	})

	spec := &ContainerSpec{
		Resources: ResourceSpec{
			GoroutineLimit:     5,
			ContextWindowLimit: 1000,
			VFSQuotaBytes:      256,
		},
	}
	q.Reserve(spec)

	usage := q.Usage()
	if usage.GoroutineUsed != 5 {
		t.Fatalf("expected 5 goroutines used, got %d", usage.GoroutineUsed)
	}
	if usage.ContextWindowUsed != 0 {
		t.Fatalf("expected container reserve to not charge context window, got %d", usage.ContextWindowUsed)
	}
	if usage.ContextWindowRequest != 40000 {
		t.Fatalf("expected context request 40000, got %d", usage.ContextWindowRequest)
	}
	if usage.ContextHardCapacity != 50000 {
		t.Fatalf("expected hard capacity 50000, got %d", usage.ContextHardCapacity)
	}
	if usage.VFSUsed != 256 {
		t.Fatalf("expected 256 vfs used, got %d", usage.VFSUsed)
	}
	if usage.ContainerCount != 1 {
		t.Fatalf("expected 1 container, got %d", usage.ContainerCount)
	}
}

func TestResourceQuota_ZeroLimitNoEnforcement(t *testing.T) {
	q := NewResourceQuota(ResourceQuotaConfig{})

	spec := &ContainerSpec{
		Resources: ResourceSpec{GoroutineLimit: 9999},
	}
	if err := q.CheckContainerFits(spec); err != nil {
		t.Fatalf("zero limits should not enforce: %v", err)
	}
}

func TestResourceQuota_VFSExceedsLimit(t *testing.T) {
	q := NewResourceQuota(ResourceQuotaConfig{
		VFSQuotaLimit: 100,
	})

	spec := &ContainerSpec{
		Resources: ResourceSpec{VFSQuotaBytes: 60},
	}
	q.Reserve(spec)

	overSpec := &ContainerSpec{
		Resources: ResourceSpec{VFSQuotaBytes: 50},
	}
	err := q.CheckContainerFits(overSpec)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded for VFS, got %v", err)
	}
}

func TestLimitRange_WithinBounds(t *testing.T) {
	lr := &LimitRange{
		MinGoroutines:    2,
		MaxGoroutines:    100,
		MinContextWindow: 1000,
		MaxContextWindow: 50000,
		MaxVFSQuota:      1024,
	}

	spec := &ContainerSpec{
		Resources: ResourceSpec{
			GoroutineLimit:     10,
			ContextWindowLimit: 5000,
			VFSQuotaBytes:      512,
		},
	}
	if err := lr.CheckContainerLimits(spec); err != nil {
		t.Fatalf("should be within bounds: %v", err)
	}
}

func TestResourceQuota_ContextLeaseTracksLiveUsage(t *testing.T) {
	q := NewResourceQuota(ResourceQuotaConfig{
		ContextWindowRequest:   1000,
		ContextWindowLimit:     4000,
		ContextLeaseMin:        30 * time.Millisecond,
		ContextReleaseHalfLife: 20 * time.Millisecond,
		ContextShortWindow:     20 * time.Millisecond,
		ContextLongWindow:      80 * time.Millisecond,
		ContextAdmissionTick:   5 * time.Millisecond,
	})

	lease, err := q.AcquireContextLease(t.Context(), ContextLeaseRequest{
		ClaimID:         "agent-1",
		AgentType:       "engineer",
		RequestTokens:   1000,
		HardLimitTokens: 2000,
		PromptBytes:     200,
	})
	if err != nil {
		t.Fatalf("AcquireContextLease: %v", err)
	}
	defer lease.Release()

	if got := q.Usage().ContextWindowUsed; got <= 0 {
		t.Fatalf("expected provisional context usage after admission, got %d", got)
	}

	q.ObserveContextUsage(ContextUsageObservation{
		AgentID:     "agent-1",
		AgentType:   "engineer",
		InputTokens: 1200,
		ObservedAt:  time.Now(),
	})
	if got := q.Usage().ContextWindowUsed; got < 1200 {
		t.Fatalf("expected live usage to reflect observed tokens, got %d", got)
	}

	lease.Release()
	if got := q.Usage().ContextWindowUsed; got <= 0 {
		t.Fatalf("expected release tail to preserve short-lived claim, got %d", got)
	}

	time.Sleep(140 * time.Millisecond)
	if got := q.Usage().ContextWindowUsed; got != 0 {
		t.Fatalf("expected released claim to decay to zero, got %d", got)
	}
}

func TestResourceQuota_ContextAliasBindingTransfersUsage(t *testing.T) {
	q := NewResourceQuota(ResourceQuotaConfig{
		ContextWindowRequest:   800,
		ContextWindowLimit:     4000,
		ContextLeaseMin:        30 * time.Millisecond,
		ContextReleaseHalfLife: 20 * time.Millisecond,
		ContextShortWindow:     20 * time.Millisecond,
		ContextLongWindow:      80 * time.Millisecond,
		ContextAdmissionTick:   5 * time.Millisecond,
	})

	lease, err := q.AcquireContextLease(t.Context(), ContextLeaseRequest{
		ClaimID:         "node-1",
		AgentType:       "tester-pipeline",
		RequestTokens:   800,
		HardLimitTokens: 1600,
	})
	if err != nil {
		t.Fatalf("AcquireContextLease(node-1): %v", err)
	}
	lease.BindAlias("worker-1")

	q.ObserveContextUsage(ContextUsageObservation{
		AgentID:     "worker-1",
		AgentType:   "tester-pipeline",
		InputTokens: 900,
		ObservedAt:  time.Now(),
	})
	if got := q.Usage().ContextWindowUsed; got < 900 {
		t.Fatalf("expected alias-bound usage to update live claim, got %d", got)
	}

	lease.Release()

	nextLease, err := q.AcquireContextLease(t.Context(), ContextLeaseRequest{
		ClaimID:         "node-2",
		AgentType:       "tester-pipeline",
		RequestTokens:   800,
		HardLimitTokens: 1600,
	})
	if err != nil {
		t.Fatalf("AcquireContextLease(node-2): %v", err)
	}
	defer nextLease.Release()
	nextLease.BindAlias("worker-1")

	q.ObserveContextUsage(ContextUsageObservation{
		AgentID:     "worker-1",
		AgentType:   "tester-pipeline",
		InputTokens: 950,
		ObservedAt:  time.Now(),
	})
	if got := q.Usage().ContextWindowUsed; got < 950 {
		t.Fatalf("expected rebound usage on re-bound alias, got %d", got)
	}
}

func TestResourceQuota_ContextAdmissionWaitsForRelease(t *testing.T) {
	q := NewResourceQuota(ResourceQuotaConfig{
		ContextWindowRequest:   1000,
		ContextWindowLimit:     1700,
		ContextLeaseMin:        20 * time.Millisecond,
		ContextReleaseHalfLife: 8 * time.Millisecond,
		ContextShortWindow:     20 * time.Millisecond,
		ContextLongWindow:      80 * time.Millisecond,
		ContextAdmissionTick:   5 * time.Millisecond,
	})

	first, err := q.AcquireContextLease(t.Context(), ContextLeaseRequest{
		ClaimID:         "node-a",
		AgentType:       "engineer",
		RequestTokens:   1000,
		HardLimitTokens: 1700,
	})
	if err != nil {
		t.Fatalf("AcquireContextLease(node-a): %v", err)
	}
	defer first.Release()

	acquired := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		lease, err := q.AcquireContextLease(t.Context(), ContextLeaseRequest{
			ClaimID:         "node-b",
			AgentType:       "engineer",
			RequestTokens:   1000,
			HardLimitTokens: 1700,
		})
		if err != nil {
			errCh <- err
			return
		}
		lease.Release()
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("second lease should have waited under pressure")
	case <-time.After(25 * time.Millisecond):
	}

	first.Release()

	select {
	case <-acquired:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("second lease did not acquire after release")
	}

	select {
	case err := <-errCh:
		t.Fatalf("AcquireContextLease(node-b): %v", err)
	default:
	}
}

func TestLimitRange_BelowMinGoroutines(t *testing.T) {
	lr := &LimitRange{MinGoroutines: 5}

	spec := &ContainerSpec{
		Resources: ResourceSpec{GoroutineLimit: 2},
	}
	err := lr.CheckContainerLimits(spec)
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expected ErrLimitExceeded, got %v", err)
	}
}

func TestLimitRange_AboveMaxGoroutines(t *testing.T) {
	lr := &LimitRange{MaxGoroutines: 50}

	spec := &ContainerSpec{
		Resources: ResourceSpec{GoroutineLimit: 100},
	}
	err := lr.CheckContainerLimits(spec)
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expected ErrLimitExceeded, got %v", err)
	}
}

func TestLimitRange_AboveMaxVFS(t *testing.T) {
	lr := &LimitRange{MaxVFSQuota: 500}

	spec := &ContainerSpec{
		Resources: ResourceSpec{VFSQuotaBytes: 1000},
	}
	err := lr.CheckContainerLimits(spec)
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expected ErrLimitExceeded, got %v", err)
	}
}
