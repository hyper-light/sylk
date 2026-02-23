package container

import (
	"context"
	"log/slog"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container/network"
)

// defaultHealthSyncPeriod is derived from the default probe period (5s)
// divided by 2, so health state in the ServiceRegistry never lags more
// than one probe period behind the actual probe results.
const defaultHealthSyncPeriod = 5 * time.Second / 2

// HealthSyncer periodically reads probe results from all containers
// and updates the ServiceRegistry with current health/readiness state.
type HealthSyncer struct {
	containerReg *ContainerRegistry
	serviceReg   *network.ServiceRegistry
	scope        *concurrency.GoroutineScope
	period       time.Duration
	logger       *slog.Logger
}

// HealthSyncerConfig provides construction parameters.
type HealthSyncerConfig struct {
	ContainerRegistry *ContainerRegistry
	ServiceRegistry   *network.ServiceRegistry
	Scope             *concurrency.GoroutineScope
	Period            time.Duration // 0 = defaultHealthSyncPeriod
	Logger            *slog.Logger  // nil = slog.Default()
}

// NewHealthSyncer creates a health syncer with the given configuration.
func NewHealthSyncer(cfg HealthSyncerConfig) *HealthSyncer {
	period := cfg.Period
	if period == 0 {
		period = defaultHealthSyncPeriod
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &HealthSyncer{
		containerReg: cfg.ContainerRegistry,
		serviceReg:   cfg.ServiceRegistry,
		scope:        cfg.Scope,
		period:       period,
		logger:       logger,
	}
}

// Start launches the health sync loop as a tracked goroutine.
func (hs *HealthSyncer) Start() error {
	return hs.scope.Go("health-syncer", 0, hs.loop)
}

// loop runs the periodic sync until the context is cancelled.
func (hs *HealthSyncer) loop(ctx context.Context) error {
	ticker := time.NewTicker(hs.period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			hs.sync()
		}
	}
}

// sync reads probe state from all containers and pushes it to the
// service registry. Each container's alive/ready state flows from
// its ProbeRunner into the ServiceRegistry for health-aware routing.
func (hs *HealthSyncer) sync() {
	for _, c := range hs.containerReg.All() {
		hs.serviceReg.UpdateHealth(
			c.Agent().AgentID(),
			c.IsAlive(),
			c.IsReady(),
		)
	}
}
