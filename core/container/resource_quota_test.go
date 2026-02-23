package container

import (
	"errors"
	"testing"
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
		GoroutineLimit:     100,
		ContextWindowLimit: 50000,
		VFSQuotaLimit:      1024,
		ContainerLimit:     10,
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
	if usage.ContextWindowUsed != 1000 {
		t.Fatalf("expected 1000 context window used, got %d", usage.ContextWindowUsed)
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
