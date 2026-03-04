package guardian

// ActivationQuerier provides read-only access to agent activation state.
// Satisfied by *activation.ActivationController.
type ActivationQuerier interface {
	TierOf(agentType string) (int32, error)
	EntryCount() int
}

// ActivationMetricsQuerier provides a snapshot of activation counters.
// Satisfied by *activation.ActivationMetrics via a thin adapter.
type ActivationMetricsQuerier interface {
	Snapshot() map[string]int64
}

// DaemonQuerier provides read-only access to daemon set status.
// Satisfied by *daemon.DaemonSetController.
type DaemonQuerier interface {
	Status() []DaemonStatusSnapshot
}

// DaemonStatusSnapshot mirrors daemon.DaemonSetStatus without importing
// the daemon package.
type DaemonStatusSnapshot struct {
	Name         string `json:"name"`
	Running      bool   `json:"running"`
	ContainerID  string `json:"container_id"`
	RestartCount int64  `json:"restart_count"`
	Healthy      bool   `json:"healthy"`
}

// CVSQuerier provides read-only access to content version store metrics.
// Satisfied by versioning.CVS (the Stats() method).
type CVSQuerier interface {
	Stats() CVSStatsSnapshot
}

// CVSStatsSnapshot mirrors versioning.CVSStats without importing the
// versioning package.
type CVSStatsSnapshot struct {
	TotalFiles        int64 `json:"total_files"`
	TotalVersions     int64 `json:"total_versions"`
	TotalOperations   int64 `json:"total_operations"`
	ActivePipelines   int64 `json:"active_pipelines"`
	ActiveVariants    int64 `json:"active_variants"`
	ActiveLocks       int64 `json:"active_locks"`
	ActiveSubscribers int64 `json:"active_subscribers"`
}

// VFSManagerQuerier provides read-only access to VFS manager metrics.
// Satisfied by *versioning.MemoryVFSManager via a thin adapter.
type VFSManagerQuerier interface {
	Stats() VFSManagerSnapshot
}

// VFSManagerSnapshot mirrors versioning.VFSManagerStats without importing
// the versioning package.
type VFSManagerSnapshot struct {
	ActiveVFSes    int `json:"active_vfses"`
	VariantGroups  int `json:"variant_groups"`
	ActiveSessions int `json:"active_sessions"`
	TotalPipelines int `json:"total_pipelines"`
}
