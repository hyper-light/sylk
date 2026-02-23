package daemon

import (
	"context"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

// reconcileInterval is the period between reconciliation passes.
const reconcileInterval = 10 * time.Second

// Reconciler drives periodic reconciliation of daemon sets via GoroutineScope.
type Reconciler struct {
	controller *DaemonSetController
	scope      *concurrency.GoroutineScope
	onError    func(error)
}

// ReconcilerConfig provides construction parameters.
type ReconcilerConfig struct {
	Controller *DaemonSetController
	Scope      *concurrency.GoroutineScope
	OnError    func(error)
}

// NewReconciler creates a reconciler backed by a GoroutineScope.
func NewReconciler(cfg ReconcilerConfig) *Reconciler {
	return &Reconciler{
		controller: cfg.Controller,
		scope:      cfg.Scope,
		onError:    cfg.OnError,
	}
}

// Start begins the reconciliation loop as a tracked goroutine via scope.Go().
func (r *Reconciler) Start() error {
	return r.scope.Go("daemon-reconciler", reconcilerLifetime, r.loop)
}

// reconcilerLifetime is long enough that the reconciler runs effectively
// forever. It exits cleanly when the scope context is cancelled.
const reconcilerLifetime = 24 * time.Hour

func (r *Reconciler) loop(ctx context.Context) error {
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.reconcileOnce(ctx)
		}
	}
}

func (r *Reconciler) reconcileOnce(ctx context.Context) {
	if err := r.controller.Reconcile(ctx); err != nil && r.onError != nil {
		r.onError(err)
	}
}
