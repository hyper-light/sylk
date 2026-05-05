package pod

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

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
//
// volumes and mounts are populated at construction and never mutated
// thereafter; concurrent reads of the maps are safe without a lock.
// mounted is atomic so the read paths (FileAccessFor, EnsureReady,
// InjectFileAccess, IsMounted, VolumeCount) take no locks at all.
//
// drainers is a per-volume in-flight counter + drain barrier. Callers
// that wrap their volume operations with BeginVolumeOp give UnmountAll
// the ability to wait for live writers to finish before tearing down,
// with a deadline. Callers that do NOT wrap are still safe — they just
// don't participate in the drain accounting (the counter stays at
// zero and UnmountAll proceeds immediately).
//
// transitionMu serializes MountAll and UnmountAll so Mount/Unmount on
// individual volumes don't interleave; it is never held by the read
// paths and so does not contend with hot-pod traffic.
type VolumeManager struct {
	transitionMu sync.Mutex
	volumes      map[string]ManagedVolume // volume name → volume; immutable after NewVolumeManager
	mounts       map[string]string        // agentType → volume name; immutable after NewVolumeManager
	drainers     map[string]*drainTracker // volume name → drain tracker; immutable post-NewVolumeManager
	mounted      atomic.Bool
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
		volumes:  make(map[string]ManagedVolume, len(cfg.Volumes)),
		mounts:   cfg.Mounts,
		drainers: make(map[string]*drainTracker, len(cfg.Volumes)),
	}
	if vm.mounts == nil {
		vm.mounts = make(map[string]string)
	}
	for _, v := range cfg.Volumes {
		name := v.VolumeName()
		vm.volumes[name] = v
		vm.drainers[name] = newDrainTracker()
	}
	return vm
}

// MountAll mounts all managed volumes. Called during promote to Hot.
func (vm *VolumeManager) MountAll(ctx context.Context) error {
	vm.transitionMu.Lock()
	defer vm.transitionMu.Unlock()

	if vm.mounted.Load() {
		return nil
	}

	for name, v := range vm.volumes {
		if err := v.Mount(ctx); err != nil {
			vm.unmountAllLocked(ctx)
			return fmt.Errorf("mount volume %q: %w", name, err)
		}
	}
	vm.mounted.Store(true)
	return nil
}

// EnsureReady refreshes mounted volumes that need deterministic rebinding
// across hot-pod reuse without forcing an unmount/remount cycle.
func (vm *VolumeManager) EnsureReady(ctx context.Context) error {
	if !vm.mounted.Load() {
		return nil
	}

	for _, v := range vm.volumes {
		if err := v.EnsureReady(ctx); err != nil {
			return fmt.Errorf("ensure volume %q ready: %w", v.VolumeName(), err)
		}
	}
	return nil
}

// UnmountAll unmounts all volumes after draining in-flight ops on
// each. Returns a joined error of every per-volume failure: drain
// timeout (ErrDrainTimeout) or the underlying ManagedVolume.Unmount
// error. Replaces the prior silent-swallow behavior.
//
// Drain semantics: each volume's tracker is transitioned to draining,
// then we wait for inflight to hit zero. Callers that have not opted
// into BeginVolumeOp see no inflight ops and pass instantly. Callers
// that have wrap their writes get a deterministic deadline before
// teardown.
func (vm *VolumeManager) UnmountAll(ctx context.Context) error {
	vm.transitionMu.Lock()
	defer vm.transitionMu.Unlock()

	if !vm.mounted.Load() {
		return nil
	}
	err := vm.unmountAllLocked(ctx)
	vm.mounted.Store(false)
	for _, t := range vm.drainers {
		t.resetForRemount()
	}
	return err
}

// unmountAllLocked drains then unmounts every volume. Returns a
// joined error of every per-volume failure. Must be called with
// transitionMu held.
func (vm *VolumeManager) unmountAllLocked(ctx context.Context) error {
	var errs []error
	for name, v := range vm.volumes {
		if err := vm.drainVolume(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("drain volume %q: %w", name, err))
			// Continue to unmount even if drain failed: holding
			// open mounts after a teardown decision is the worse
			// failure mode (resource leak, observable inconsistency).
		}
		if err := v.Unmount(ctx); err != nil {
			errs = append(errs, fmt.Errorf("unmount volume %q: %w", name, err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// drainVolume waits for in-flight ops on the named volume to drain
// within ctx's deadline. Returns ErrDrainTimeout (wrapping ctx.Err)
// when the deadline elapses with ops still outstanding.
func (vm *VolumeManager) drainVolume(ctx context.Context, name string) error {
	tracker, ok := vm.drainers[name]
	if !ok {
		return nil
	}
	if err := tracker.WaitDrain(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrDrainTimeout, err)
	}
	return nil
}

// BeginVolumeOp registers a new in-flight operation against the named
// volume. Returns a release function the caller must invoke when the
// op completes (typical use: `release, err := vm.BeginVolumeOp(name);
// if err != nil { ... }; defer release()`). Returns ErrVolumeDraining
// when the volume is in the drain phase of UnmountAll.
//
// Callers that opt into this API give UnmountAll the ability to
// bound teardown deadlines deterministically; callers that don't are
// still safe but can't be drained.
func (vm *VolumeManager) BeginVolumeOp(volName string) (release func(), err error) {
	tracker, ok := vm.drainers[volName]
	if !ok {
		return func() {}, nil
	}
	return tracker.BeginVolumeOp()
}

// FileAccessFor returns the FileAccess for the given agent type based
// on the configured mount mapping. Returns nil if no mapping exists
// or the volume is not mounted. Lock-free: volumes/mounts are
// immutable after construction, mounted is atomic, and the volume's
// FileAccess accessor is safe to call concurrently with Mount/Unmount.
func (vm *VolumeManager) FileAccessFor(agentType string) versioning.FileAccess {
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

// InjectFileAccess sets FileAccess on all container agents that
// implement FileAccessConsumer or ReadOnlyFileAccessConsumer. Uses
// the mount mapping to determine which volume each agent receives.
//
// Agents that implement ReadOnlyFileAccessConsumer (and do NOT also
// implement FileAccessConsumer) get a read-only handle whose method
// set does not include WriteFile / EditFile / DeleteFile / MkdirAll
// — compile-time guarantee, no runtime IsReadOnly check needed at
// the call site.
func (vm *VolumeManager) InjectFileAccess(containers map[string]*container.Container) {
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
		} else if roConsumer, ok := agent.(versioning.ReadOnlyFileAccessConsumer); ok {
			// FileAccess embeds ReadOnlyFileAccess, so fa already
			// satisfies the interface — the assignment narrows the
			// method set the consumer can reach.
			roConsumer.SetReadOnlyFileAccess(fa)
		}
		if viewsConsumer, ok := agent.(versioning.WorkspaceViewsConsumer); ok {
			viewsConsumer.SetWorkspaceViews(vol.WorkspaceViews())
		}
	}
}

// IsMounted returns whether the volumes are currently mounted.
func (vm *VolumeManager) IsMounted() bool {
	return vm.mounted.Load()
}

// VolumeCount returns the number of registered volumes.
func (vm *VolumeManager) VolumeCount() int {
	return len(vm.volumes)
}
