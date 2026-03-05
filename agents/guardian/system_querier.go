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
	TotalFiles        int64  `json:"total_files"`
	TotalVersions     int64  `json:"total_versions"`
	TotalOperations   int64  `json:"total_operations"`
	ActivePipelines   int64  `json:"active_pipelines"`
	ActiveVariants    int64  `json:"active_variants"`
	ActiveLocks       int64  `json:"active_locks"`
	ActiveSubscribers int64  `json:"active_subscribers"`
	CurrentVersion    string `json:"current_version,omitempty"`
	WALEntries        int64  `json:"wal_entries,omitempty"`
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

// =============================================================================
// Pipeline / Orchestrator Observability
// =============================================================================

// PipelineQuerier provides read-only access to orchestrator and DAG state.
// Satisfied by a thin adapter over *orchestrator.Orchestrator + DAGBridge.
type PipelineQuerier interface {
	// Summary returns the orchestrator's workflow/task summary.
	Summary() (*PipelineSummarySnapshot, error)
	// DAGSnapshots returns up to limit DAG execution snapshots.
	DAGSnapshots(limit int) []DAGSnapshot
}

// PipelineSummarySnapshot mirrors orchestrator.OrchestratorSummary fields.
type PipelineSummarySnapshot struct {
	Overview         string         `json:"overview"`
	ActiveWorkflows  int            `json:"active_workflows"`
	TotalWorkflows   int            `json:"total_workflows"`
	ActiveTasks      int            `json:"active_tasks"`
	CompletedTasks   int            `json:"completed_tasks"`
	FailedTasks      int            `json:"failed_tasks"`
	TotalTasks       int            `json:"total_tasks"`
}

// DAGSnapshot mirrors orchestrator.dagSnap fields.
type DAGSnapshot struct {
	ID           string  `json:"id"`
	PlanID       string  `json:"plan_id"`
	State        string  `json:"state"`
	CurrentLayer int     `json:"current_layer"`
	TotalLayers  int     `json:"total_layers"`
	Progress     float64 `json:"progress"`
	NodesFailed  int     `json:"nodes_failed,omitempty"`
	Duration     string  `json:"duration,omitempty"`
}

// =============================================================================
// Gateway Observability
// =============================================================================

// GatewayQuerier provides read-only access to LLM gateway metrics.
// Satisfied by a thin adapter over []*gateway.ProviderGateway.
type GatewayQuerier interface {
	// AllMetrics returns metrics snapshots keyed by gateway name.
	AllMetrics() map[string]GatewayMetricsSnapshot
}

// GatewayMetricsSnapshot mirrors gateway.MetricsSnapshot with the gateway name.
type GatewayMetricsSnapshot struct {
	Name        string `json:"name"`
	Admitted    int64  `json:"admitted"`
	Rejected    int64  `json:"rejected"`
	Queued      int64  `json:"queued"`
	Completed   int64  `json:"completed"`
	RateLimited int64  `json:"rate_limited"`
	Errors429   int64  `json:"errors_429"`
	Inflight    int64  `json:"inflight"`
	TotalWaitMs int64  `json:"total_wait_ms"`
}

// =============================================================================
// Knowledge Observability
// =============================================================================

// KnowledgeQuerier provides read-only access to knowledge store state.
// Satisfied by a thin adapter over *knowledge.KnowledgeStore.
type KnowledgeQuerier interface {
	// Status returns readiness level and active searchers.
	Status() KnowledgeStatusSnapshot
	// QueryMetrics returns average query performance metrics.
	QueryMetrics() KnowledgeQueryMetrics
	// IngestionProgress returns background indexing progress.
	IngestionProgress() (indexed, total int64)
}

// KnowledgeStatusSnapshot captures readiness and searcher availability.
type KnowledgeStatusSnapshot struct {
	ReadinessLevel  int      `json:"readiness_level"`  // 0=none, 1=partial, 2=full
	ReadinessLabel  string   `json:"readiness_label"`
	ReadySearchers  []string `json:"ready_searchers"`
}

// KnowledgeQueryMetrics captures average query performance.
type KnowledgeQueryMetrics struct {
	TextLatencyMs     int64 `json:"text_latency_ms"`
	SemanticLatencyMs int64 `json:"semantic_latency_ms"`
	GraphLatencyMs    int64 `json:"graph_latency_ms"`
	FusionLatencyMs   int64 `json:"fusion_latency_ms"`
	TotalLatencyMs    int64 `json:"total_latency_ms"`
	TextContributed   bool  `json:"text_contributed"`
	SemanticContributed bool `json:"semantic_contributed"`
	GraphContributed  bool  `json:"graph_contributed"`
}

// =============================================================================
// Concurrency Observability
// =============================================================================

// ConcurrencyQuerier provides read-only access to goroutine budgets and LLM gate.
// Satisfied by a thin adapter over *concurrency.GoroutineBudget + *concurrency.DualQueueGate.
type ConcurrencyQuerier interface {
	// GoroutineStats returns system-wide goroutine budget status.
	GoroutineStats() GoroutineBudgetSnapshot
	// LLMGateStats returns dual-queue LLM gate status.
	LLMGateStats() LLMGateSnapshot
}

// GoroutineBudgetSnapshot captures goroutine budget state.
type GoroutineBudgetSnapshot struct {
	TotalActive int64 `json:"total_active"`
	SystemLimit int64 `json:"system_limit"`
	AgentCount  int   `json:"agent_count"`
}

// LLMGateSnapshot captures dual-queue LLM gate state.
type LLMGateSnapshot struct {
	UserQueueSize      int `json:"user_queue_size"`
	PipelineQueueSize  int `json:"pipeline_queue_size"`
	ActiveRequests     int `json:"active_requests"`
	ActiveUserRequests int `json:"active_user_requests"`
}
