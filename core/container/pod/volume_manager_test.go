package pod

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/versioning"
)

// --- test helpers ---

type mockVolume struct {
	name     string
	mounted  bool
	fa       versioning.FileAccess
	mountErr error
}

func (v *mockVolume) VolumeName() string { return v.name }

func (v *mockVolume) Mount(_ context.Context) error {
	if v.mountErr != nil {
		return v.mountErr
	}
	v.mounted = true
	return nil
}

func (v *mockVolume) Unmount(_ context.Context) error {
	v.mounted = false
	return nil
}

func (v *mockVolume) FileAccess() versioning.FileAccess {
	if !v.mounted {
		return nil
	}
	return v.fa
}

// --- tests ---

func TestVolumeManager_MountUnmount(t *testing.T) {
	v1 := &mockVolume{name: "data", fa: versioning.NewDiskFileAccess(t.TempDir(), false)}
	v2 := &mockVolume{name: "cache", fa: versioning.NewDiskFileAccess(t.TempDir(), false)}

	vm := NewVolumeManager(VolumeManagerConfig{
		Volumes: []ManagedVolume{v1, v2},
		Mounts:  map[string]string{"engineer": "data", "designer": "cache"},
	})

	if vm.IsMounted() {
		t.Fatal("expected not mounted initially")
	}

	if err := vm.MountAll(context.Background()); err != nil {
		t.Fatalf("MountAll: %v", err)
	}

	if !vm.IsMounted() {
		t.Fatal("expected mounted after MountAll")
	}
	if !v1.mounted || !v2.mounted {
		t.Fatal("expected both volumes mounted")
	}

	if fa := vm.FileAccessFor("engineer"); fa == nil {
		t.Fatal("expected FileAccess for engineer")
	}
	if fa := vm.FileAccessFor("designer"); fa == nil {
		t.Fatal("expected FileAccess for designer")
	}
	if fa := vm.FileAccessFor("unknown"); fa != nil {
		t.Fatal("expected nil FileAccess for unknown agent")
	}

	if err := vm.UnmountAll(context.Background()); err != nil {
		t.Fatalf("UnmountAll: %v", err)
	}

	if vm.IsMounted() {
		t.Fatal("expected not mounted after UnmountAll")
	}
	if v1.mounted || v2.mounted {
		t.Fatal("expected both volumes unmounted")
	}
}

func TestVolumeManager_MountIdempotent(t *testing.T) {
	v := &mockVolume{name: "data", fa: versioning.NewDiskFileAccess(t.TempDir(), false)}
	vm := NewVolumeManager(VolumeManagerConfig{
		Volumes: []ManagedVolume{v},
	})

	if err := vm.MountAll(context.Background()); err != nil {
		t.Fatalf("first mount: %v", err)
	}
	if err := vm.MountAll(context.Background()); err != nil {
		t.Fatalf("second mount should be idempotent: %v", err)
	}
}

func TestVolumeManager_VolumeCount(t *testing.T) {
	v1 := &mockVolume{name: "a"}
	v2 := &mockVolume{name: "b"}
	vm := NewVolumeManager(VolumeManagerConfig{
		Volumes: []ManagedVolume{v1, v2},
	})
	if vm.VolumeCount() != 2 {
		t.Fatalf("expected 2 volumes, got %d", vm.VolumeCount())
	}
}
