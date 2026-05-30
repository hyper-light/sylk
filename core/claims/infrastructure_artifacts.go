package claims

import "time"

const (
	ArtifactDataTypeDAGOperation         = "sylk.infrastructure.dag_operation.v1"
	ArtifactDataTypeVFSOperation         = "sylk.infrastructure.vfs_operation.v1"
	ArtifactDataTypeBootPhase            = "sylk.infrastructure.boot_phase.v1"
	ArtifactDataTypeToolRuntimeExecution = "sylk.infrastructure.tool_runtime_execution.v1"
	ArtifactDataTypeKnowledgeOperation   = "sylk.infrastructure.knowledge_operation.v1"
	ArtifactDataTypeMemoryContinuity     = "sylk.infrastructure.memory_continuity.v1"
	ArtifactDataTypeDocumentOperation    = "sylk.infrastructure.document_operation.v1"
	ArtifactDataTypeGuardianDecision     = "sylk.infrastructure.guardian_decision.v1"
	ArtifactDataTypeProviderGatewayCall  = "sylk.infrastructure.provider_gateway_call.v1"
	ArtifactDataTypeExternalAdapterEvent = "sylk.infrastructure.external_adapter_event.v1"
	ArtifactDataTypeSessionLifecycle     = "sylk.infrastructure.session_lifecycle.v1"
	ArtifactDataTypeFabricSubscription   = "sylk.infrastructure.fabric_subscription.v1"
	ArtifactDataTypeBusTransport         = "sylk.infrastructure.bus_transport.v1"
	ArtifactDataTypeRecoveryIdempotency  = "sylk.infrastructure.recovery_idempotency.v1"
)

const (
	ArtifactKindDAGOperation         = "dag_operation"
	ArtifactKindVFSOperation         = "vfs_operation"
	ArtifactKindBootPhase            = "boot_phase"
	ArtifactKindBootSetupComplete    = "setup_complete"
	ArtifactKindBootDetectResult     = "detect_result"
	ArtifactKindBootAllocateOutcome  = "allocate_outcome"
	ArtifactKindBootIngestStatus     = "ingest_status"
	ArtifactKindBootCommitRef        = "commit_ref"
	ArtifactKindBootFinalizeSignal   = "finalize_signal"
	ArtifactKindBootFailure          = "boot_failure"
	ArtifactKindToolRuntimeExecution = "tool_runtime_execution"
	ArtifactKindKnowledgeOperation   = "knowledge_operation"
	ArtifactKindMemoryContinuity     = "memory_continuity"
	ArtifactKindDocumentOperation    = "document_operation"
	ArtifactKindGuardianDecision     = "guardian_decision"
	ArtifactKindProviderGatewayCall  = "provider_gateway_call"
	ArtifactKindExternalAdapterEvent = "external_adapter_event"
	ArtifactKindSessionHandle        = "session_handle"
	ArtifactKindSessionState         = "session_state"
	ArtifactKindPersistOutcome       = "persist_outcome"
	ArtifactKindRestoreOutcome       = "restore_outcome"
	ArtifactKindLensQueryResult      = "lens_query_result"
	ArtifactKindHarvestOutcome       = "harvest_outcome"
	ArtifactKindSubscriptionState    = "subscription_state"
	ArtifactKindTopicRegistration    = "topic_registration"
	ArtifactKindCapacityRecord       = "capacity_record"
	ArtifactKindDrainOutcome         = "drain_outcome"
	ArtifactKindRecoveryIdempotency  = "recovery_idempotency"
)

type DAGEdgeArtifactData struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type DAGOperationArtifactData struct {
	Operation            string                `json:"operation"`
	PipelineID           string                `json:"pipeline_id,omitempty"`
	DAGHash              string                `json:"dag_hash,omitempty"`
	Nodes                []string              `json:"nodes,omitempty"`
	Edges                []DAGEdgeArtifactData `json:"edges,omitempty"`
	NodeCount            int                   `json:"node_count"`
	EdgeCount            int                   `json:"edge_count"`
	CycleFree            bool                  `json:"cycle_free"`
	CyclePath            []string              `json:"cycle_path,omitempty"`
	RequiredNodes        []string              `json:"required_nodes,omitempty"`
	MissingRequiredNodes []string              `json:"missing_required_nodes,omitempty"`
	PlannedPods          int                   `json:"planned_pods"`
	HealthSummary        string                `json:"health_summary,omitempty"`
	ScopeBoundary        map[string]string     `json:"scope_boundary,omitempty"`
	Status               string                `json:"status,omitempty"`
	FailureReason        string                `json:"failure_reason,omitempty"`
	Metadata             map[string]any        `json:"metadata,omitempty"`
}

type VFSOperationArtifactData struct {
	Operation           string            `json:"operation"`
	Scope               string            `json:"scope,omitempty"`
	MountRef            string            `json:"mount_ref,omitempty"`
	ScopeBoundary       map[string]string `json:"scope_boundary,omitempty"`
	CapacityBudgetBytes int64             `json:"capacity_budget_bytes,omitempty"`
	CapacityUsedBytes   int64             `json:"capacity_used_bytes,omitempty"`
	BaseVersion         string            `json:"base_version,omitempty"`
	COWLayerChain       []string          `json:"cow_layer_chain,omitempty"`
	SnapshotHash        string            `json:"snapshot_hash,omitempty"`
	MergePreview        string            `json:"merge_preview,omitempty"`
	ConflictSet         []string          `json:"conflict_set,omitempty"`
	ResultingVersion    string            `json:"resulting_version,omitempty"`
	Attached            bool              `json:"attached"`
	ReadOnly            bool              `json:"read_only,omitempty"`
	Shutdown            bool              `json:"shutdown,omitempty"`
	Interrupted         bool              `json:"interrupted,omitempty"`
	Status              string            `json:"status,omitempty"`
	FailureReason       string            `json:"failure_reason,omitempty"`
	Metadata            map[string]any    `json:"metadata,omitempty"`
}

type BootPhaseArtifactData struct {
	Phase           string         `json:"phase"`
	PhaseOrder      int            `json:"phase_order"`
	ProcessUID      string         `json:"process_uid,omitempty"`
	StartedAt       time.Time      `json:"started_at,omitempty"`
	EndedAt         time.Time      `json:"ended_at,omitempty"`
	Duration        time.Duration  `json:"duration,omitempty"`
	Ready           bool           `json:"ready"`
	Skipped         bool           `json:"skipped,omitempty"`
	CommitRef       string         `json:"commit_ref,omitempty"`
	ArtifactRefs    []string       `json:"artifact_refs,omitempty"`
	PriorPhases     []string       `json:"prior_phases,omitempty"`
	Status          string         `json:"status,omitempty"`
	FailureReason   string         `json:"failure_reason,omitempty"`
	ValidationNotes []string       `json:"validation_notes,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type ToolRuntimeExecutionArtifactData struct {
	ToolName             string            `json:"tool_name"`
	RedactedArgs         map[string]any    `json:"redacted_args,omitempty"`
	PolicyDecision       string            `json:"policy_decision,omitempty"`
	PolicyRule           string            `json:"policy_rule,omitempty"`
	ExecutionMode        string            `json:"execution_mode,omitempty"`
	SandboxScope         map[string]string `json:"sandbox_scope,omitempty"`
	StartedAt            time.Time         `json:"started_at,omitempty"`
	EndedAt              time.Time         `json:"ended_at,omitempty"`
	Duration             time.Duration     `json:"duration,omitempty"`
	ExitStatus           int               `json:"exit_status,omitempty"`
	OutputSummary        string            `json:"output_summary,omitempty"`
	OutputRefs           []string          `json:"output_refs,omitempty"`
	ProducedArtifactRefs []string          `json:"produced_artifact_refs,omitempty"`
	GuardianClaimID      string            `json:"guardian_claim_id,omitempty"`
	Interrupted          bool              `json:"interrupted,omitempty"`
	TimedOut             bool              `json:"timed_out,omitempty"`
	ContentTruncated     bool              `json:"content_truncated,omitempty"`
	Status               string            `json:"status,omitempty"`
	FailureReason        string            `json:"failure_reason,omitempty"`
	Metadata             map[string]any    `json:"metadata,omitempty"`
}

type KnowledgeOperationArtifactData struct {
	Operation                  string         `json:"operation"`
	NodeCount                  int            `json:"node_count"`
	EdgeCount                  int            `json:"edge_count"`
	EmbeddingDimension         int            `json:"embedding_dimension,omitempty"`
	ExpectedEmbeddingDimension int            `json:"expected_embedding_dimension,omitempty"`
	VectorIndexRefs            []string       `json:"vector_index_refs,omitempty"`
	QueryShape                 string         `json:"query_shape,omitempty"`
	ResultCount                int            `json:"result_count,omitempty"`
	ScoreMin                   float64        `json:"score_min,omitempty"`
	ScoreMax                   float64        `json:"score_max,omitempty"`
	Stale                      bool           `json:"stale,omitempty"`
	Ready                      bool           `json:"ready"`
	Backend                    string         `json:"backend,omitempty"`
	FullTextBackend            string         `json:"full_text_backend,omitempty"`
	MissingNodes               []string       `json:"missing_nodes,omitempty"`
	DanglingEdges              []string       `json:"dangling_edges,omitempty"`
	Status                     string         `json:"status,omitempty"`
	FailureReason              string         `json:"failure_reason,omitempty"`
	Metadata                   map[string]any `json:"metadata,omitempty"`
}

type MemoryContinuityArtifactData struct {
	Operation        string         `json:"operation"`
	Topic            string         `json:"topic,omitempty"`
	AgentID          string         `json:"agent_id,omitempty"`
	WorkingContext   string         `json:"working_context,omitempty"`
	EvidenceDigest   string         `json:"evidence_digest,omitempty"`
	SourceIndex      []string       `json:"source_index,omitempty"`
	FromSeq          uint64         `json:"from_seq,omitempty"`
	ThroughSeq       uint64         `json:"through_seq,omitempty"`
	PreviousCursor   string         `json:"previous_cursor,omitempty"`
	ContinuityCursor string         `json:"continuity_cursor,omitempty"`
	SessionCursor    string         `json:"session_cursor,omitempty"`
	LegacyNoWAL      bool           `json:"legacy_no_wal,omitempty"`
	Contradicted     bool           `json:"contradicted,omitempty"`
	RepairReport     string         `json:"repair_report,omitempty"`
	SourceAvailable  bool           `json:"source_available"`
	Status           string         `json:"status,omitempty"`
	FailureReason    string         `json:"failure_reason,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

type DocumentChunkBoundaryArtifactData struct {
	Index int `json:"index"`
	Start int `json:"start"`
	End   int `json:"end"`
}

type DocumentOperationArtifactData struct {
	Operation       string                              `json:"operation"`
	DocumentID      string                              `json:"document_id,omitempty"`
	ChunkCount      int                                 `json:"chunk_count,omitempty"`
	IndexPath       string                              `json:"index_path,omitempty"`
	ContentHash     string                              `json:"content_hash,omitempty"`
	ResultShape     string                              `json:"result_shape,omitempty"`
	ResultCount     int                                 `json:"result_count,omitempty"`
	Stale           bool                                `json:"stale,omitempty"`
	AttachmentRefs  []string                            `json:"attachment_refs,omitempty"`
	ChunkBoundaries []DocumentChunkBoundaryArtifactData `json:"chunk_boundaries,omitempty"`
	FullTextBackend string                              `json:"full_text_backend,omitempty"`
	Indexed         bool                                `json:"indexed"`
	Deleted         bool                                `json:"deleted,omitempty"`
	Status          string                              `json:"status,omitempty"`
	FailureReason   string                              `json:"failure_reason,omitempty"`
	Metadata        map[string]any                      `json:"metadata,omitempty"`
}

type GuardianDecisionArtifactData struct {
	Operation       string         `json:"operation"`
	PolicyRule      string         `json:"policy_rule,omitempty"`
	ApprovalSubject string         `json:"approval_subject,omitempty"`
	UserDecision    string         `json:"user_decision,omitempty"`
	BranchState     string         `json:"branch_state,omitempty"`
	DiffFindings    []string       `json:"diff_findings,omitempty"`
	RollbackReceipt string         `json:"rollback_receipt,omitempty"`
	DenialReason    string         `json:"denial_reason,omitempty"`
	Allowed         bool           `json:"allowed"`
	ApprovalPresent bool           `json:"approval_present,omitempty"`
	BranchProtected bool           `json:"branch_protected,omitempty"`
	Status          string         `json:"status,omitempty"`
	FailureReason   string         `json:"failure_reason,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type ProviderGatewayCallArtifactData struct {
	Operation          string         `json:"operation"`
	Model              string         `json:"model,omitempty"`
	Identity           string         `json:"identity,omitempty"`
	TaskRef            string         `json:"task_ref,omitempty"`
	BudgetTokens       int            `json:"budget_tokens,omitempty"`
	PromptHash         string         `json:"prompt_hash,omitempty"`
	StreamingMode      string         `json:"streaming_mode,omitempty"`
	ResponseSummary    string         `json:"response_summary,omitempty"`
	FinishReason       string         `json:"finish_reason,omitempty"`
	ProviderRequestID  string         `json:"provider_request_id,omitempty"`
	InputTokens        int            `json:"input_tokens,omitempty"`
	OutputTokens       int            `json:"output_tokens,omitempty"`
	TotalTokens        int            `json:"total_tokens,omitempty"`
	Latency            time.Duration  `json:"latency,omitempty"`
	RateLimitRemaining int            `json:"rate_limit_remaining,omitempty"`
	RateLimited        bool           `json:"rate_limited,omitempty"`
	Error              string         `json:"error,omitempty"`
	PartialSummaries   []string       `json:"partial_summaries,omitempty"`
	Status             string         `json:"status,omitempty"`
	FailureReason      string         `json:"failure_reason,omitempty"`
	Metadata           map[string]any `json:"metadata,omitempty"`
}

type ExternalAdapterEventArtifactData struct {
	Operation         string         `json:"operation"`
	Source            ParticipantRef `json:"source"`
	IdempotencyKey    string         `json:"idempotency_key,omitempty"`
	RawEventDigest    string         `json:"raw_event_digest,omitempty"`
	ParsedPayload     map[string]any `json:"parsed_payload,omitempty"`
	Schema            string         `json:"schema,omitempty"`
	Signature         string         `json:"signature,omitempty"`
	ReceivedAt        time.Time      `json:"received_at,omitempty"`
	EventTimestamp    time.Time      `json:"event_timestamp,omitempty"`
	Fresh             bool           `json:"fresh"`
	ReferencedClaimID string         `json:"referenced_claim_id,omitempty"`
	Valid             bool           `json:"valid"`
	Rejected          bool           `json:"rejected,omitempty"`
	Status            string         `json:"status,omitempty"`
	FailureReason     string         `json:"failure_reason,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}

type SessionLifecycleArtifactData struct {
	Operation     string         `json:"operation"`
	SessionID     string         `json:"session_id"`
	BoardID       string         `json:"board_id,omitempty"`
	Active        bool           `json:"active"`
	Persisted     bool           `json:"persisted,omitempty"`
	Restored      bool           `json:"restored,omitempty"`
	StaleReplaced bool           `json:"stale_replaced,omitempty"`
	Status        string         `json:"status,omitempty"`
	FailureReason string         `json:"failure_reason,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type FabricSubscriptionArtifactData struct {
	Operation         string         `json:"operation"`
	SessionID         string         `json:"session_id"`
	AgentID           string         `json:"agent_id,omitempty"`
	Topic             string         `json:"topic,omitempty"`
	LensQueryShape    string         `json:"lens_query_shape,omitempty"`
	HarvestCount      int            `json:"harvest_count,omitempty"`
	DeliveredCount    int            `json:"delivered_count,omitempty"`
	DroppedCount      uint64         `json:"dropped_count,omitempty"`
	Attached          bool           `json:"attached"`
	SubscriptionState string         `json:"subscription_state,omitempty"`
	Status            string         `json:"status,omitempty"`
	FailureReason     string         `json:"failure_reason,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}

type BusTransportArtifactData struct {
	Operation       string         `json:"operation"`
	ProcessUID      string         `json:"process_uid,omitempty"`
	Topic           string         `json:"topic,omitempty"`
	Capacity        int            `json:"capacity,omitempty"`
	QueueDepth      int            `json:"queue_depth,omitempty"`
	DroppedCount    uint64         `json:"dropped_count,omitempty"`
	SubscriberError string         `json:"subscriber_error,omitempty"`
	Drained         bool           `json:"drained,omitempty"`
	Status          string         `json:"status,omitempty"`
	FailureReason   string         `json:"failure_reason,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type RecoveryIdempotencyArtifactData struct {
	Key            string    `json:"key"`
	ParticipantID  string    `json:"participant_id,omitempty"`
	Scope          string    `json:"scope,omitempty"`
	Operation      string    `json:"operation,omitempty"`
	IssuedBy       string    `json:"issued_by,omitempty"`
	IssuedAt       time.Time `json:"issued_at,omitempty"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
	SafetyEvidence string    `json:"safety_evidence,omitempty"`
}
