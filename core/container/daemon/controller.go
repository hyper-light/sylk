package daemon

import (
	"context"
	"sync"

	"github.com/adalundhe/sylk/core/container"
)

// DaemonSetController ensures exactly one instance of each daemon set
// is running per session. Failed instances are restarted with backoff.
type DaemonSetController struct {
	mu        sync.RWMutex
	specs     map[string]*DaemonSetSpec       // name → spec (bounded by daemon set count)
	instances map[string]*container.Container  // name → running container
	runtime   container.ContainerRuntime
	registry  *container.ContainerRegistry
}

// NewDaemonSetController creates a daemon set controller.
func NewDaemonSetController(
	runtime container.ContainerRuntime,
	registry *container.ContainerRegistry,
) *DaemonSetController {
	return &DaemonSetController{
		specs:     make(map[string]*DaemonSetSpec),
		instances: make(map[string]*container.Container),
		runtime:   runtime,
		registry:  registry,
	}
}

// Apply adds or updates a daemon set spec. The next reconcile cycle
// will create or replace the instance.
func (dc *DaemonSetController) Apply(spec DaemonSetSpec) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.specs[spec.Name] = &spec
}

// Remove removes a daemon set spec. The next reconcile cycle will
// stop and remove the instance.
func (dc *DaemonSetController) Remove(name string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	delete(dc.specs, name)
}

// Reconcile performs a single reconciliation pass: creates missing instances,
// removes extra instances, and restarts failed ones.
func (dc *DaemonSetController) Reconcile(ctx context.Context) error {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	dc.removeStale(ctx)
	return dc.ensureDesired(ctx)
}

func (dc *DaemonSetController) removeStale(ctx context.Context) {
	for name, c := range dc.instances {
		if _, desired := dc.specs[name]; !desired {
			dc.stopAndRemove(ctx, name, c)
		}
	}
}

func (dc *DaemonSetController) ensureDesired(ctx context.Context) error {
	for name, spec := range dc.specs {
		if err := dc.ensureInstance(ctx, name, spec); err != nil {
			return err
		}
	}
	return nil
}

func (dc *DaemonSetController) ensureInstance(ctx context.Context, name string, spec *DaemonSetSpec) error {
	existing := dc.instances[name]
	if existing == nil {
		return dc.createAndStart(ctx, name, spec)
	}
	if existing.IsTerminal() {
		return dc.restartFailed(ctx, name, existing, spec)
	}
	return nil
}

func (dc *DaemonSetController) createAndStart(ctx context.Context, name string, spec *DaemonSetSpec) error {
	c, err := dc.runtime.CreateContainer(ctx, spec.ContainerSpec)
	if err != nil {
		return err
	}
	if err := dc.runtime.StartContainer(ctx, c); err != nil {
		_ = dc.runtime.RemoveContainer(ctx, c)
		return err
	}
	dc.instances[name] = c
	return nil
}

func (dc *DaemonSetController) restartFailed(ctx context.Context, name string, old *container.Container, spec *DaemonSetSpec) error {
	_ = dc.runtime.RemoveContainer(ctx, old)
	return dc.createAndStart(ctx, name, spec)
}

func (dc *DaemonSetController) stopAndRemove(ctx context.Context, name string, c *container.Container) {
	if c.IsRunning() {
		_ = dc.runtime.StopContainer(ctx, c)
	}
	_ = dc.runtime.RemoveContainer(ctx, c)
	delete(dc.instances, name)
}

// Status returns the status of all daemon sets.
func (dc *DaemonSetController) Status() []DaemonSetStatus {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	statuses := make([]DaemonSetStatus, 0, len(dc.specs))
	for name := range dc.specs {
		statuses = append(statuses, dc.instanceStatus(name))
	}
	return statuses
}

func (dc *DaemonSetController) instanceStatus(name string) DaemonSetStatus {
	c, exists := dc.instances[name]
	if !exists {
		return DaemonSetStatus{Name: name}
	}
	return DaemonSetStatus{
		Name:         name,
		Running:      c.IsRunning(),
		ContainerID:  c.ID(),
		RestartCount: c.Metrics().RestartCount.Load(),
		Healthy:      c.IsAlive(),
	}
}

// InstanceCount returns the number of running instances.
func (dc *DaemonSetController) InstanceCount() int {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return len(dc.instances)
}

// SpecCount returns the number of registered daemon set specs.
func (dc *DaemonSetController) SpecCount() int {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return len(dc.specs)
}

// InjectInstance registers a pre-created container as the running instance
// for the named daemon set. Use this when containers are created outside
// the normal Reconcile flow (e.g. parallel bootstrap) and need to be
// adopted by the controller for ongoing lifecycle management.
func (dc *DaemonSetController) InjectInstance(name string, c *container.Container) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.instances[name] = c
}
