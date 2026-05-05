package pod

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/versioning"
)

// fakeManagedVolume is a minimal ManagedVolume for testing the drain
// barrier without a real PureVFS mount.
type fakeManagedVolume struct {
	name        string
	mountErr    error
	unmountErr  error
	mountCount  int
	unmountedAt time.Time
}

func (f *fakeManagedVolume) Mount(_ context.Context) error   { f.mountCount++; return f.mountErr }
func (f *fakeManagedVolume) EnsureReady(_ context.Context) error { return nil }
func (f *fakeManagedVolume) Unmount(_ context.Context) error {
	f.unmountedAt = time.Now()
	return f.unmountErr
}
func (f *fakeManagedVolume) FileAccess() versioning.FileAccess         { return nil }
func (f *fakeManagedVolume) WorkspaceViews() versioning.WorkspaceViewAccess { return nil }
func (f *fakeManagedVolume) VolumeName() string                        { return f.name }

// TestVolumeManager_DrainTimeoutWhenOpsOutstanding pins the new
// behavior: when an op is in flight and the unmount deadline elapses,
// UnmountAll returns ErrDrainTimeout instead of silently swallowing.
func TestVolumeManager_DrainTimeoutWhenOpsOutstanding(t *testing.T) {
	vol := &fakeManagedVolume{name: "test"}
	vm := NewVolumeManager(VolumeManagerConfig{Volumes: []ManagedVolume{vol}})

	if err := vm.MountAll(context.Background()); err != nil {
		t.Fatalf("mount: %v", err)
	}

	release, err := vm.BeginVolumeOp("test")
	if err != nil {
		t.Fatalf("BeginVolumeOp: %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = vm.UnmountAll(ctx)
	if !errors.Is(err, ErrDrainTimeout) {
		t.Fatalf("UnmountAll err = %v, want ErrDrainTimeout", err)
	}
}

// TestVolumeManager_DrainCompletesWhenOpReleases verifies the happy
// path: an in-flight op releases before the deadline; UnmountAll
// returns nil and the volume is unmounted.
func TestVolumeManager_DrainCompletesWhenOpReleases(t *testing.T) {
	vol := &fakeManagedVolume{name: "test"}
	vm := NewVolumeManager(VolumeManagerConfig{Volumes: []ManagedVolume{vol}})

	if err := vm.MountAll(context.Background()); err != nil {
		t.Fatalf("mount: %v", err)
	}

	release, err := vm.BeginVolumeOp("test")
	if err != nil {
		t.Fatalf("BeginVolumeOp: %v", err)
	}

	// Schedule the release after a short delay; UnmountAll must wait
	// until then and then succeed.
	go func() {
		time.Sleep(20 * time.Millisecond)
		release()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := vm.UnmountAll(ctx); err != nil {
		t.Fatalf("UnmountAll: %v", err)
	}
	if vol.unmountedAt.IsZero() {
		t.Fatal("expected Unmount to have been called")
	}
}

// TestVolumeManager_BeginVolumeOpRejectedDuringDrain verifies that new
// ops cannot start once the drain has been triggered — protects
// against a writer that ignores ctx and would otherwise extend the
// drain indefinitely.
func TestVolumeManager_BeginVolumeOpRejectedDuringDrain(t *testing.T) {
	vol := &fakeManagedVolume{name: "test"}
	vm := NewVolumeManager(VolumeManagerConfig{Volumes: []ManagedVolume{vol}})
	if err := vm.MountAll(context.Background()); err != nil {
		t.Fatalf("mount: %v", err)
	}

	// Hold one op so UnmountAll blocks on drain; another goroutine
	// triggers the unmount.
	hold, err := vm.BeginVolumeOp("test")
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	defer hold()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = vm.UnmountAll(ctx)
	}()

	// Wait briefly so UnmountAll has set draining=true.
	time.Sleep(20 * time.Millisecond)

	_, err = vm.BeginVolumeOp("test")
	if !errors.Is(err, ErrVolumeDraining) {
		t.Fatalf("BeginVolumeOp during drain err = %v, want ErrVolumeDraining", err)
	}
}
