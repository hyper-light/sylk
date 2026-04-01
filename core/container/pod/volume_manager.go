package pod

import (
	"context"
	"fmt"
	"sync"

	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/versioning"
)

// ManagedVolume is a volume whose lifecycle is tied to pod tier transitions.
type ManagedVolume interface {
	// Mount prepares the volume for use (called on promote to Hot).
	Mount(ctx context.Context) error

	// EnsureReady validates any hot-pod bindings that must remain live across
	// retries while the volume stays mounted.
	EnsureReady(ctx context.Context) error

	// Unmount tears down the volume (called on demote from Hot).
	Unmount(ctx context.Context) error

	// FileAccess returns the FileAccess interface for this volume.
	// Returns nil if the volume is not mounted.
	FileAccess() versioning.FileAccess

	// WorkspaceViews returns explicit disk/global/pipeline view access for this
	// volume when available. Returns nil when the volume does not expose layered
	// workspace state.
	WorkspaceViews() versioning.WorkspaceViewAccess

	// VolumeName returns the volume's name (matches VolumeSpec.Name).
	VolumeName() string
}

// VolumeManager coordinates volume lifecycle for a pod's tier transitions.
// Each volume is keyed by its VolumeSpec.Name.
type VolumeManager struct {
	mu      sync.RWMutex
	volumes map[string]ManagedVolume // volume name → volume
	mounts  map[string]string        // agentType → volume name
	mounted bool
}

// VolumeManagerConfig provides construction parameters.
type VolumeManagerConfig struct {
	Volumes []ManagedVolume
	// Mounts maps agent types to the volume name they should use.
	Mounts map[string]string
}

// NewVolumeManager creates a VolumeManager with the given volumes and mount mappings.
func NewVolumeManager(cfg VolumeManagerConfig) *VolumeManager {
	vm := &VolumeManager{
		volumes: make(map[string]ManagedVolume, len(cfg.Volumes)),
		mounts:  cfg.Mounts,
	}
	if vm.mounts == nil {
		vm.mounts = make(map[string]string)
	}
	for _, v := range cfg.Volumes {
		vm.volumes[v.VolumeName()] = v
	}
	return vm
}

// MountAll mounts all managed volumes. Called during promote to Hot.
func (vm *VolumeManager) MountAll(ctx context.Context) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if vm.mounted {
		return nil
	}

	for name, v := range vm.volumes {
		if err := v.Mount(ctx); err != nil {
			vm.unmountAllLocked(ctx)
			return fmt.Errorf("mount volume %q: %w", name, err)
		}
	}
	vm.mounted = true
	return nil
}

// EnsureReady refreshes mounted volumes that need deterministic rebinding
// across hot-pod reuse without forcing an unmount/remount cycle.
func (vm *VolumeManager) EnsureReady(ctx context.Context) error {
	vm.mu.RLock()
	if !vm.mounted {
		vm.mu.RUnlock()
		return nil
	}
	volumes := make([]ManagedVolume, 0, len(vm.volumes))
	for _, v := range vm.volumes {
		volumes = append(volumes, v)
	}
	vm.mu.RUnlock()

	for _, v := range volumes {
		if err := v.EnsureReady(ctx); err != nil {
			return fmt.Errorf("ensure volume %q ready: %w", v.VolumeName(), err)
		}
	}
	return nil
}

// UnmountAll unmounts all volumes. Called during demote from Hot.
func (vm *VolumeManager) UnmountAll(ctx context.Context) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if !vm.mounted {
		return nil
	}
	vm.unmountAllLocked(ctx)
	vm.mounted = false
	return nil
}

// unmountAllLocked unmounts all volumes. Must be called with mu held.
func (vm *VolumeManager) unmountAllLocked(ctx context.Context) {
	for _, v := range vm.volumes {
		_ = v.Unmount(ctx)
	}
}

// FileAccessFor returns the FileAccess for the given agent type based
// on the configured mount mapping. Returns nil if no mapping exists
// or the volume is not mounted.
func (vm *VolumeManager) FileAccessFor(agentType string) versioning.FileAccess {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	volName, ok := vm.mounts[agentType]
	if !ok {
		return nil
	}
	vol, ok := vm.volumes[volName]
	if !ok {
		return nil
	}
	return vol.FileAccess()
}

// WorkspaceViewsFor returns the layered workspace view access for the given
// agent type based on the configured mount mapping. Returns nil if no mapping
// exists or the volume is not mounted.
func (vm *VolumeManager) WorkspaceViewsFor(agentType string) versioning.WorkspaceViewAccess {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	volName, ok := vm.mounts[agentType]
	if !ok {
		return nil
	}
	vol, ok := vm.volumes[volName]
	if !ok {
		return nil
	}
	return vol.WorkspaceViews()
}

// InjectFileAccess sets FileAccess on all container agents that implement
// FileAccessConsumer. Uses the mount mapping to determine which volume
// each agent receives.
func (vm *VolumeManager) InjectFileAccess(containers map[string]*container.Container) {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	for agentType, c := range containers {
		volName, ok := vm.mounts[agentType]
		if !ok {
			continue
		}
		vol, ok := vm.volumes[volName]
		if !ok {
			continue
		}
		fa := vol.FileAccess()
		if fa == nil {
			continue
		}
		agent := c.Agent()
		if consumer, ok := agent.(versioning.FileAccessConsumer); ok {
			consumer.SetFileAccess(fa)
		}
		if viewsConsumer, ok := agent.(versioning.WorkspaceViewsConsumer); ok {
			viewsConsumer.SetWorkspaceViews(vol.WorkspaceViews())
		}
	}
}

// IsMounted returns whether the volumes are currently mounted.
func (vm *VolumeManager) IsMounted() bool {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return vm.mounted
}

// VolumeCount returns the number of registered volumes.
func (vm *VolumeManager) VolumeCount() int {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return len(vm.volumes)
}
