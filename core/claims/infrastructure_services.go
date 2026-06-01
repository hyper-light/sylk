package claims

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DAGProcessorToolAllocatePipeline   = "dag_allocate_pipeline"
	DAGProcessorToolDeallocatePipeline = "dag_deallocate_pipeline"
	DAGProcessorToolQueryState         = "dag_query_state"
	DAGProcessorToolRepair             = "dag_repair"
	DAGProcessorToolReportHealth       = "dag_report_health"

	VFSProvisionerToolProvision = "vfs_provision"
	VFSProvisionerToolAttach    = "vfs_attach"
	VFSProvisionerToolDetach    = "vfs_detach"
	VFSProvisionerToolSnapshot  = "vfs_snapshot"
	VFSProvisionerToolPromote   = "vfs_promote"
	VFSProvisionerToolMerge     = "vfs_merge"
	VFSProvisionerToolRollback  = "vfs_rollback"

	ToolRuntimeToolExecute            = "tool_runtime_execute"
	ToolRuntimeToolExecuteTool        = "execute_tool"
	ToolRuntimeToolValidateInvocation = "validate_invocation"
	ToolRuntimeToolQueryPolicy        = "query_policy"

	KnowledgeToolIngestNodes      = "knowledge_ingest_nodes"
	KnowledgeToolIngestEdges      = "knowledge_ingest_edges"
	KnowledgeToolUpdateEmbeddings = "knowledge_update_embeddings"
	KnowledgeToolDeleteStale      = "knowledge_delete_stale"
	KnowledgeToolQueryGraph       = "knowledge_query_graph"
	KnowledgeToolQuerySemantic    = "knowledge_query_semantic"
	KnowledgeToolQueryHybrid      = "knowledge_query_hybrid"
	KnowledgeToolReadiness        = "knowledge_readiness"

	MemoryToolCarryForwardPreview = "memory_carry_forward_preview"
	MemoryToolCarryForwardAdvance = "memory_carry_forward_advance"
	MemoryToolRepairProjection    = "memory_repair_projection"
	MemoryToolRecall              = "memory_recall"

	DocumentToolIngest        = "document_ingest"
	DocumentToolUpdate        = "document_update"
	DocumentToolDelete        = "document_delete"
	DocumentToolChunk         = "document_chunk"
	DocumentToolIndex         = "document_index"
	DocumentToolLookup        = "document_lookup"
	DocumentToolMetadataQuery = "document_metadata_query"
	DocumentToolSearchBleve   = "document_search_bleve"
	DocumentToolResultShape   = "document_result_shape"

	GuardianToolApprovalCheck         = "guardian_approval_check"
	GuardianToolPolicyCheck           = "guardian_policy_check"
	GuardianToolBranchProtection      = "guardian_branch_protection"
	GuardianToolDiffReview            = "guardian_diff_review"
	GuardianToolRollbackAuthorization = "guardian_rollback_authorization"
	GuardianToolCommandGate           = "guardian_command_gate"
	GuardianToolApproveCommand        = "approve_command"
	GuardianToolApprovePlan           = "approve_plan"
	GuardianToolScanContent           = "scan_content"
	GuardianToolContentScan           = "content_scan"
	GuardianToolGateGit               = "gate_git"
	GuardianToolFetchApproval         = "fetch_approval"
	GuardianToolRollback              = "rollback"

	ProviderGatewayToolComplete          = "provider_complete"
	ProviderGatewayToolCompleteStreaming = "provider_complete_streaming"
	ProviderGatewayToolEmbedding         = "provider_embedding"
	ProviderGatewayToolCountTokens       = "provider_count_tokens"

	ExternalAdapterToolClaim     = "external_claim"
	ExternalAdapterToolTestament = "external_testament"
	ExternalAdapterToolCIResult  = "external_ci_result"
	ExternalAdapterToolApproval  = "external_approval"

	InfrastructureStatusOK          = "ok"
	InfrastructureStatusFailed      = "failed"
	InfrastructureStatusInterrupted = "interrupted"
)

const (
	defaultInfrastructureMaxRecords       = 4096
	defaultInfrastructureOutputLimit      = 8192
	defaultInfrastructureProviderPartials = 32
	defaultExternalEventFreshnessWindow   = 24 * time.Hour
)

var ErrInfrastructureServiceInvalid = errors.New("infrastructure service invalid")

type InfrastructureServiceKind string

const (
	InfrastructureServiceKindDAG       InfrastructureServiceKind = "dag"
	InfrastructureServiceKindVFS       InfrastructureServiceKind = "vfs"
	InfrastructureServiceKindTool      InfrastructureServiceKind = "tool_runtime"
	InfrastructureServiceKindKnowledge InfrastructureServiceKind = "knowledge"
	InfrastructureServiceKindMemory    InfrastructureServiceKind = "memory"
	InfrastructureServiceKindDocument  InfrastructureServiceKind = "document"
	InfrastructureServiceKindGuardian  InfrastructureServiceKind = "guardian"
	InfrastructureServiceKindProvider  InfrastructureServiceKind = "provider"
	InfrastructureServiceKindExternal  InfrastructureServiceKind = "external"
)

type InfrastructureServiceConfig struct {
	ParticipantID        string
	Kind                 InfrastructureServiceKind
	VFSScope             string
	MaxRecords           int
	OutputSummaryLimit   int
	ProviderPartialLimit int
	Clock                ClaimsClock
	DAGBackend           DAGProcessorBackend
	VFSBackend           VFSProvisionerBackend
	ToolBackend          ToolRuntimeBackend
	KnowledgeBackend     KnowledgeBackend
	MemoryBackend        MemoryContinuityBackend
	DocumentBackend      DocumentBackend
	GuardianBackend      GuardianBackend
	ProviderBackend      ProviderGatewayBackend
	ExternalBackend      ExternalAdapterBackend
}

type InfrastructureService struct {
	mu                   sync.RWMutex
	participantID        string
	kind                 InfrastructureServiceKind
	vfsScope             string
	outputSummaryLimit   int
	providerPartialLimit int
	clock                ClaimsClock
	dagBackend           DAGProcessorBackend
	vfsBackend           VFSProvisionerBackend
	toolBackend          ToolRuntimeBackend
	knowledgeBackend     KnowledgeBackend
	memoryBackend        MemoryContinuityBackend
	documentBackend      DocumentBackend
	guardianBackend      GuardianBackend
	providerBackend      ProviderGatewayBackend
	externalBackend      ExternalAdapterBackend
	dag                  *boundedInfrastructureRecords[DAGOperationArtifactData]
	vfs                  *boundedInfrastructureRecords[VFSOperationArtifactData]
	tool                 *boundedInfrastructureRecords[ToolRuntimeExecutionArtifactData]
	knowledge            *boundedInfrastructureRecords[KnowledgeOperationArtifactData]
	memory               *boundedInfrastructureRecords[MemoryContinuityArtifactData]
	document             *boundedInfrastructureRecords[DocumentOperationArtifactData]
	guardian             *boundedInfrastructureRecords[GuardianDecisionArtifactData]
	provider             *boundedInfrastructureRecords[ProviderGatewayCallArtifactData]
	external             *boundedInfrastructureRecords[ExternalAdapterEventArtifactData]
}

type infrastructureToolHandler func(context.Context, ExpectedToolCall, ServiceClaimRequest) (ServiceClaimResult, error)

type boundedInfrastructureRecords[T any] struct {
	max   int
	byKey map[string]T
	order []string
}

func NewInfrastructureService(cfg InfrastructureServiceConfig) (*InfrastructureService, error) {
	cfg = normalizeInfrastructureServiceConfig(cfg)
	if cfg.Kind == "" {
		return nil, fmt.Errorf("%w: service kind is required", ErrInfrastructureServiceInvalid)
	}
	return &InfrastructureService{
		participantID:        cfg.ParticipantID,
		kind:                 cfg.Kind,
		vfsScope:             cfg.VFSScope,
		outputSummaryLimit:   cfg.OutputSummaryLimit,
		providerPartialLimit: cfg.ProviderPartialLimit,
		clock:                firstNonNilClock(cfg.Clock),
		dagBackend:           cfg.DAGBackend,
		vfsBackend:           cfg.VFSBackend,
		toolBackend:          cfg.ToolBackend,
		knowledgeBackend:     cfg.KnowledgeBackend,
		memoryBackend:        cfg.MemoryBackend,
		documentBackend:      cfg.DocumentBackend,
		guardianBackend:      cfg.GuardianBackend,
		providerBackend:      cfg.ProviderBackend,
		externalBackend:      cfg.ExternalBackend,
		dag:                  newBoundedInfrastructureRecords[DAGOperationArtifactData](cfg.MaxRecords),
		vfs:                  newBoundedInfrastructureRecords[VFSOperationArtifactData](cfg.MaxRecords),
		tool:                 newBoundedInfrastructureRecords[ToolRuntimeExecutionArtifactData](cfg.MaxRecords),
		knowledge:            newBoundedInfrastructureRecords[KnowledgeOperationArtifactData](cfg.MaxRecords),
		memory:               newBoundedInfrastructureRecords[MemoryContinuityArtifactData](cfg.MaxRecords),
		document:             newBoundedInfrastructureRecords[DocumentOperationArtifactData](cfg.MaxRecords),
		guardian:             newBoundedInfrastructureRecords[GuardianDecisionArtifactData](cfg.MaxRecords),
		provider:             newBoundedInfrastructureRecords[ProviderGatewayCallArtifactData](cfg.MaxRecords),
		external:             newBoundedInfrastructureRecords[ExternalAdapterEventArtifactData](cfg.MaxRecords),
	}, nil
}

func NewDAGProcessorService(cfg InfrastructureServiceConfig) *InfrastructureService {
	cfg.Kind = InfrastructureServiceKindDAG
	service, err := NewInfrastructureService(cfg)
	if err != nil {
		panic(err)
	}
	return service
}

func NewVFSProvisionerService(scope string, cfg InfrastructureServiceConfig) *InfrastructureService {
	cfg.Kind = InfrastructureServiceKindVFS
	cfg.VFSScope = firstNonEmpty(scope, cfg.VFSScope)
	service, err := NewInfrastructureService(cfg)
	if err != nil {
		panic(err)
	}
	return service
}

func NewToolRuntimeService(cfg InfrastructureServiceConfig) *InfrastructureService {
	cfg.Kind = InfrastructureServiceKindTool
	service, err := NewInfrastructureService(cfg)
	if err != nil {
		panic(err)
	}
	return service
}

func NewKnowledgeService(cfg InfrastructureServiceConfig) *InfrastructureService {
	cfg.Kind = InfrastructureServiceKindKnowledge
	service, err := NewInfrastructureService(cfg)
	if err != nil {
		panic(err)
	}
	return service
}

func NewMemoryForestService(cfg InfrastructureServiceConfig) *InfrastructureService {
	cfg.Kind = InfrastructureServiceKindMemory
	service, err := NewInfrastructureService(cfg)
	if err != nil {
		panic(err)
	}
	return service
}

func NewDocumentService(cfg InfrastructureServiceConfig) *InfrastructureService {
	cfg.Kind = InfrastructureServiceKindDocument
	service, err := NewInfrastructureService(cfg)
	if err != nil {
		panic(err)
	}
	return service
}

func NewGuardianService(cfg InfrastructureServiceConfig) *InfrastructureService {
	cfg.Kind = InfrastructureServiceKindGuardian
	service, err := NewInfrastructureService(cfg)
	if err != nil {
		panic(err)
	}
	return service
}

func NewProviderGatewayService(cfg InfrastructureServiceConfig) *InfrastructureService {
	cfg.Kind = InfrastructureServiceKindProvider
	service, err := NewInfrastructureService(cfg)
	if err != nil {
		panic(err)
	}
	return service
}

func NewExternalAdapterService(cfg InfrastructureServiceConfig) *InfrastructureService {
	cfg.Kind = InfrastructureServiceKindExternal
	service, err := NewInfrastructureService(cfg)
	if err != nil {
		panic(err)
	}
	return service
}

func NewDefaultInfrastructureServiceHandler(participantID string) ServiceHandler {
	participantID = strings.TrimSpace(participantID)
	cfg := InfrastructureServiceConfig{ParticipantID: participantID}
	switch participantID {
	case "sys:dag_processor":
		return NewDAGProcessorService(cfg)
	case "sys:vfs_pipeline_provisioner":
		return NewVFSProvisionerService("pipeline", cfg)
	case "sys:vfs_tool_provisioner":
		return NewVFSProvisionerService("tool", cfg)
	case "sys:vfs_global_provisioner":
		return NewVFSProvisionerService("global", cfg)
	case "sys:tool_runtime":
		return NewToolRuntimeService(cfg)
	case "sys:knowledge_graph_reader", "sys:knowledge_graph_writer":
		return NewKnowledgeService(cfg)
	case "sys:memory_forest":
		return NewMemoryForestService(cfg)
	case "sys:document_db_reader", "sys:document_db_writer":
		return NewDocumentService(cfg)
	case "sys:guardian_service":
		return NewGuardianService(cfg)
	case "sys:provider_gateway":
		return NewProviderGatewayService(cfg)
	case "sys:activation_controller":
		return NewActivationControllerService(ActivationControllerServiceConfig{})
	case "sys:session_manager":
		return NewSessionManagerService(SystemParticipantServiceConfig{ParticipantID: participantID})
	case "sys:fabric_subscriber":
		return NewFabricSubscriberService(SystemParticipantServiceConfig{ParticipantID: participantID})
	case "sys:bus_administrator":
		return NewBusAdministratorService(SystemParticipantServiceConfig{ParticipantID: participantID})
	default:
		return nil
	}
}

func InfrastructureSubsystemForParticipantID(participantID string) string {
	participantID = strings.TrimSpace(participantID)
	if strings.HasPrefix(participantID, "participant:service:") {
		parts := strings.Split(participantID, ":")
		if len(parts) >= 3 {
			participantID = strings.TrimSpace(parts[2])
		}
	}
	switch participantID {
	case "sys:dag_processor", "dag_processor":
		return "dag"
	case "sys:vfs_pipeline_provisioner", "sys:vfs_tool_provisioner", "sys:vfs_global_provisioner", "vfs_pipeline_provisioner", "vfs_tool_provisioner", "vfs_global_provisioner":
		return "vfs"
	case "sys:tool_runtime", "tool_runtime":
		return "tool_runtime"
	case "sys:knowledge_graph_reader", "sys:knowledge_graph_writer", "knowledge_graph_reader", "knowledge_graph_writer":
		return "knowledge"
	case "sys:memory_forest", "memory_forest":
		return "memory"
	case "sys:document_db_reader", "sys:document_db_writer", "document_db_reader", "document_db_writer":
		return "document"
	case "sys:guardian_service", "guardian_service":
		return "guardian"
	case "sys:provider_gateway", "sys_provider_gateway", "provider_gateway":
		return "provider"
	case "sys:activation_controller", "activation_controller":
		return "activation"
	case "sys:session_manager", "session_manager":
		return systemParticipantSession
	case "sys:fabric_subscriber", "fabric_subscriber":
		return systemParticipantFabric
	case "sys:bus_administrator", "bus_administrator":
		return systemParticipantBus
	case "sys:external_adapter", "external_adapter":
		return "external"
	default:
		return ""
	}
}

func (s *InfrastructureService) HandleServiceClaim(ctx context.Context, req ServiceClaimRequest) (ServiceClaimResult, error) {
	if err := validateInfrastructureService(s); err != nil {
		return infrastructureServiceFailureResult(nil, ExpectedToolCall{Tool: "infrastructure_service"}, req, err)
	}
	if req.Claim == nil {
		return s.infrastructureFailureResult(ExpectedToolCall{Tool: string(s.kind)}, req, fmt.Errorf("%w: claim is required", ErrInfrastructureServiceInvalid))
	}
	handlers := s.handlers()
	call, err := infrastructureServiceCall(req.Claim, handlers)
	if err != nil {
		return s.infrastructureFailureResult(ExpectedToolCall{Tool: string(s.kind)}, req, err)
	}
	handler, ok := handlers[strings.TrimSpace(call.Tool)]
	if !ok || handler == nil {
		return s.infrastructureFailureResult(call, req, fmt.Errorf("%w: unhandled infrastructure tool %q", ErrInfrastructureServiceInvalid, call.Tool))
	}
	return handler(ctxOrBackground(ctx), call, req)
}

func (s *InfrastructureService) ServiceTools() []string {
	if s == nil {
		return nil
	}
	handlers := s.handlers()
	tools := make([]string, 0, len(handlers))
	for tool := range handlers {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	return tools
}

func (s *InfrastructureService) infrastructureFailureResult(call ExpectedToolCall, req ServiceClaimRequest, cause error) (ServiceClaimResult, error) {
	return infrastructureServiceFailureResult(s, call, req, cause)
}

func infrastructureServiceFailureResult(s *InfrastructureService, call ExpectedToolCall, req ServiceClaimRequest, cause error) (ServiceClaimResult, error) {
	cause = infrastructureServiceCause(cause)
	if s == nil {
		return infrastructureServiceErrorResult("infrastructure service failed", cause, "infrastructure_service_failure", call.Tool)
	}
	kind := string(s.kind)
	switch s.kind {
	case InfrastructureServiceKindDAG:
		data := failDAGOperation(DAGOperationArtifactData{Operation: firstNonEmpty(call.Tool, kind), PipelineID: claimIDForInfrastructure(req.Claim)}, cause.Error())
		artifact, err := dagOperationArtifact(data)
		return serviceClaimResultWithArtifact("dag "+data.Operation+" failed", artifact, err, "dag_operation_failure_artifact_error", data.PipelineID)
	case InfrastructureServiceKindVFS:
		data := failVFSOperation(VFSOperationArtifactData{Operation: firstNonEmpty(call.Tool, kind), Scope: firstNonEmpty(s.vfsScope, "workspace")}, cause.Error())
		artifact, err := vfsOperationArtifact(data)
		return serviceClaimResultWithArtifact("vfs "+data.Operation+" failed", artifact, err, "vfs_operation_failure_artifact_error", data.MountRef)
	case InfrastructureServiceKindTool:
		data := failToolRuntimeExecution(ToolRuntimeExecutionArtifactData{ToolName: firstNonEmpty(stringArg(call.Arguments, "tool_name"), stringArg(call.Arguments, "tool"), call.Tool, kind)}, cause.Error())
		artifact, err := toolRuntimeExecutionArtifact(data)
		return serviceClaimResultWithArtifact("tool runtime "+data.ToolName+" failed", artifact, err, "tool_runtime_failure_artifact_error", data.ToolName)
	case InfrastructureServiceKindKnowledge:
		data := failKnowledgeOperation(KnowledgeOperationArtifactData{Operation: firstNonEmpty(call.Tool, kind)}, cause.Error())
		artifact, err := knowledgeOperationArtifact(data)
		return serviceClaimResultWithArtifact("knowledge "+data.Operation+" failed", artifact, err, "knowledge_operation_failure_artifact_error", data.Operation)
	case InfrastructureServiceKindMemory:
		data := failMemoryContinuity(MemoryContinuityArtifactData{Operation: firstNonEmpty(call.Tool, kind), Topic: reqSessionID(req)}, cause.Error())
		artifacts, err := memoryContinuityArtifacts(data)
		return serviceClaimResultWithArtifacts("memory "+data.Operation+" failed", artifacts, err, "memory_continuity_failure_artifact_error", data.Topic)
	case InfrastructureServiceKindDocument:
		data := failDocumentOperation(DocumentOperationArtifactData{Operation: firstNonEmpty(call.Tool, kind), DocumentID: stringArg(call.Arguments, "document_id")}, cause.Error())
		artifact, err := documentOperationArtifact(data)
		return serviceClaimResultWithArtifact("document "+data.Operation+" failed", artifact, err, "document_operation_failure_artifact_error", data.DocumentID)
	case InfrastructureServiceKindGuardian:
		data := failGuardianDecision(GuardianDecisionArtifactData{Operation: firstNonEmpty(call.Tool, kind)}, cause.Error())
		artifact, err := guardianDecisionArtifact(data)
		return serviceClaimResultWithArtifact("guardian "+data.Operation+" failed", artifact, err, "guardian_decision_failure_artifact_error", data.Operation)
	case InfrastructureServiceKindProvider:
		data := failProviderGatewayCall(ProviderGatewayCallArtifactData{Operation: firstNonEmpty(call.Tool, kind), Model: stringArg(call.Arguments, "model")}, cause.Error())
		artifacts, err := providerGatewayCallArtifacts(data)
		return serviceClaimResultWithArtifacts("provider "+data.Operation+" failed", artifacts, err, "provider_gateway_failure_artifact_error", data.ProviderRequestID)
	case InfrastructureServiceKindExternal:
		data := failExternalAdapterEvent(ExternalAdapterEventArtifactData{Operation: firstNonEmpty(call.Tool, kind), Source: normalizedExternalSource(ParticipantRef{}, call.Arguments)}, cause.Error())
		artifact, err := externalAdapterEventArtifact(data)
		return serviceClaimResultWithArtifact("external "+data.Operation+" failed", artifact, err, "external_adapter_failure_artifact_error", data.IdempotencyKey)
	default:
		return infrastructureServiceErrorResult("infrastructure service failed", cause, "infrastructure_service_failure", firstNonEmpty(kind, call.Tool, "infrastructure"))
	}
}

func infrastructureServiceErrorResult(summary string, cause error, fallbackName, reference string) (ServiceClaimResult, error) {
	return serviceClaimResultWithArtifact(summary, nil, infrastructureServiceCause(cause), fallbackName, reference)
}

func (s *InfrastructureService) ShutdownService(ctx context.Context, req ServiceShutdownRequest) (ServiceClaimResult, error) {
	if err := validateInfrastructureService(s); err != nil {
		return infrastructureServiceErrorResult("infrastructure shutdown failed", err, "infrastructure_shutdown_failure", req.SessionID)
	}
	if s.kind != InfrastructureServiceKindVFS {
		return ServiceClaimResult{}, nil
	}
	records := s.shutdownVFSRecords(ctxOrBackground(ctx), req)
	artifacts := make([]*Artifact, 0, len(records))
	for _, record := range records {
		artifact, err := vfsOperationArtifact(record)
		if err != nil {
			return infrastructureServiceErrorResult("vfs shutdown failed", err, "vfs_shutdown_artifact_error", record.MountRef)
		}
		artifacts = append(artifacts, artifact)
	}
	return ServiceClaimResult{Summary: "vfs_shutdown_detach", Artifacts: artifacts}, nil
}

func (s *InfrastructureService) shutdownVFSRecords(ctx context.Context, req ServiceShutdownRequest) []VFSOperationArtifactData {
	s.mu.Lock()
	defer s.mu.Unlock()
	records := s.vfs.Values()
	out := make([]VFSOperationArtifactData, 0, len(records))
	for _, record := range records {
		if !record.Attached || record.Shutdown {
			continue
		}
		record.Operation = VFSProvisionerToolDetach
		record.Attached = false
		record.Shutdown = true
		record.Interrupted = ctx.Err() != nil
		record.Status = InfrastructureStatusOK
		record.FailureReason = ""
		if record.Interrupted {
			record.Status = InfrastructureStatusInterrupted
			record.FailureReason = ctx.Err().Error()
		}
		record.Metadata = mergeMetadata(record.Metadata, map[string]any{"shutdown_session_id": req.SessionID})
		_ = s.vfs.Upsert(infrastructureRecordKey(record.MountRef, "shutdown"), record)
		out = append(out, record)
	}
	return out
}

func (s *InfrastructureService) handlers() map[string]infrastructureToolHandler {
	switch s.kind {
	case InfrastructureServiceKindDAG:
		return s.dagHandlers()
	case InfrastructureServiceKindVFS:
		return s.vfsHandlers()
	case InfrastructureServiceKindTool:
		return s.toolRuntimeHandlers()
	case InfrastructureServiceKindKnowledge:
		return s.knowledgeHandlers()
	case InfrastructureServiceKindMemory:
		return s.memoryHandlers()
	case InfrastructureServiceKindDocument:
		return s.documentHandlers()
	case InfrastructureServiceKindGuardian:
		return s.guardianHandlers()
	case InfrastructureServiceKindProvider:
		return s.providerHandlers()
	case InfrastructureServiceKindExternal:
		return s.externalHandlers()
	default:
		return nil
	}
}

func (s *InfrastructureService) toolRuntimeHandlers() map[string]infrastructureToolHandler {
	return map[string]infrastructureToolHandler{
		ToolRuntimeToolExecute:            s.handleToolRuntime,
		ToolRuntimeToolExecuteTool:        s.handleToolRuntime,
		ToolRuntimeToolValidateInvocation: s.handleToolRuntime,
		ToolRuntimeToolQueryPolicy:        s.handleToolRuntime,
	}
}

func (s *InfrastructureService) dagHandlers() map[string]infrastructureToolHandler {
	return map[string]infrastructureToolHandler{
		DAGProcessorToolAllocatePipeline:   s.handleDAG,
		DAGProcessorToolDeallocatePipeline: s.handleDAG,
		DAGProcessorToolQueryState:         s.handleDAG,
		DAGProcessorToolRepair:             s.handleDAG,
		DAGProcessorToolReportHealth:       s.handleDAG,
	}
}

func (s *InfrastructureService) vfsHandlers() map[string]infrastructureToolHandler {
	return map[string]infrastructureToolHandler{
		VFSProvisionerToolProvision: s.handleVFS,
		VFSProvisionerToolAttach:    s.handleVFS,
		VFSProvisionerToolDetach:    s.handleVFS,
		VFSProvisionerToolSnapshot:  s.handleVFS,
		VFSProvisionerToolPromote:   s.handleVFS,
		VFSProvisionerToolMerge:     s.handleVFS,
		VFSProvisionerToolRollback:  s.handleVFS,
	}
}

func (s *InfrastructureService) knowledgeHandlers() map[string]infrastructureToolHandler {
	return map[string]infrastructureToolHandler{
		KnowledgeToolIngestNodes:      s.handleKnowledge,
		KnowledgeToolIngestEdges:      s.handleKnowledge,
		KnowledgeToolUpdateEmbeddings: s.handleKnowledge,
		KnowledgeToolDeleteStale:      s.handleKnowledge,
		KnowledgeToolQueryGraph:       s.handleKnowledge,
		KnowledgeToolQuerySemantic:    s.handleKnowledge,
		KnowledgeToolQueryHybrid:      s.handleKnowledge,
		KnowledgeToolReadiness:        s.handleKnowledge,
	}
}

func (s *InfrastructureService) memoryHandlers() map[string]infrastructureToolHandler {
	return map[string]infrastructureToolHandler{
		MemoryToolCarryForwardPreview: s.handleMemory,
		MemoryToolCarryForwardAdvance: s.handleMemory,
		MemoryToolRepairProjection:    s.handleMemory,
		MemoryToolRecall:              s.handleMemory,
	}
}

func (s *InfrastructureService) documentHandlers() map[string]infrastructureToolHandler {
	return map[string]infrastructureToolHandler{
		DocumentToolIngest:        s.handleDocument,
		DocumentToolUpdate:        s.handleDocument,
		DocumentToolDelete:        s.handleDocument,
		DocumentToolChunk:         s.handleDocument,
		DocumentToolIndex:         s.handleDocument,
		DocumentToolLookup:        s.handleDocument,
		DocumentToolMetadataQuery: s.handleDocument,
		DocumentToolSearchBleve:   s.handleDocument,
		DocumentToolResultShape:   s.handleDocument,
	}
}

func (s *InfrastructureService) guardianHandlers() map[string]infrastructureToolHandler {
	return map[string]infrastructureToolHandler{
		GuardianToolApprovalCheck:         s.handleGuardian,
		GuardianToolPolicyCheck:           s.handleGuardian,
		GuardianToolBranchProtection:      s.handleGuardian,
		GuardianToolDiffReview:            s.handleGuardian,
		GuardianToolRollbackAuthorization: s.handleGuardian,
		GuardianToolCommandGate:           s.handleGuardian,
		GuardianToolApproveCommand:        s.handleGuardian,
		GuardianToolApprovePlan:           s.handleGuardian,
		GuardianToolScanContent:           s.handleGuardian,
		GuardianToolContentScan:           s.handleGuardian,
		GuardianToolGateGit:               s.handleGuardian,
		GuardianToolFetchApproval:         s.handleGuardian,
		GuardianToolRollback:              s.handleGuardian,
	}
}

func (s *InfrastructureService) providerHandlers() map[string]infrastructureToolHandler {
	return map[string]infrastructureToolHandler{
		ProviderGatewayToolComplete:          s.handleProvider,
		ProviderGatewayToolCompleteStreaming: s.handleProvider,
		ProviderGatewayToolEmbedding:         s.handleProvider,
		ProviderGatewayToolCountTokens:       s.handleProvider,
	}
}

func (s *InfrastructureService) externalHandlers() map[string]infrastructureToolHandler {
	return map[string]infrastructureToolHandler{
		ExternalAdapterToolClaim:     s.handleExternal,
		ExternalAdapterToolTestament: s.handleExternal,
		ExternalAdapterToolCIResult:  s.handleExternal,
		ExternalAdapterToolApproval:  s.handleExternal,
	}
}

func (s *InfrastructureService) handleDAG(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest) (ServiceClaimResult, error) {
	data, err := normalizeDAGOperation(ctx, call, req)
	if err != nil {
		data = failDAGOperation(DAGOperationArtifactData{Operation: call.Tool, PipelineID: claimIDForInfrastructure(req.Claim)}, err.Error())
	} else if s.dagBackend == nil {
		data = failDAGOperation(data, "DAG backend not configured")
	} else if backendData, backendErr := s.dagBackend.HandleDAGOperation(ctx, DAGOperationRequest{Board: req.Board, Claim: req.Claim, Delta: req.Delta, Participant: req.Participant, Call: call, Requested: data}); backendErr != nil {
		data = failDAGOperation(data, backendErr.Error())
	} else {
		data = finalizeDAGBackendData(ctx, call, req, data, backendData)
	}
	s.mu.Lock()
	if !s.dag.Upsert(infrastructureRecordKey(data.PipelineID, data.Operation, claimIDForInfrastructure(req.Claim)), data) {
		data = failDAGOperation(data, "DAG record capacity reached")
	}
	s.mu.Unlock()
	artifact, err := dagOperationArtifact(data)
	return serviceClaimResultWithArtifact("dag "+data.Operation+" "+data.Status, artifact, err, "dag_operation_artifact_error", data.PipelineID)
}

func (s *InfrastructureService) handleVFS(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest) (ServiceClaimResult, error) {
	data, err := normalizeVFSOperation(ctx, call, req, s.vfsScope)
	if err != nil {
		data = failVFSOperation(VFSOperationArtifactData{Operation: call.Tool, Scope: firstNonEmpty(s.vfsScope, "workspace")}, err.Error())
	} else if s.vfsBackend == nil {
		data = failVFSOperation(data, "VFS backend not configured")
	} else if backendData, backendErr := s.vfsBackend.HandleVFSOperation(ctx, VFSOperationRequest{Board: req.Board, Claim: req.Claim, Delta: req.Delta, Participant: req.Participant, Call: call, Requested: data}); backendErr != nil {
		data = failVFSOperation(data, backendErr.Error())
	} else {
		data = finalizeVFSBackendData(ctx, call, req, s.vfsScope, data, backendData)
	}
	s.mu.Lock()
	if !s.vfs.Upsert(infrastructureRecordKey(data.MountRef, data.Operation, claimIDForInfrastructure(req.Claim)), data) {
		data = failVFSOperation(data, "VFS record capacity reached")
	}
	s.mu.Unlock()
	artifact, err := vfsOperationArtifact(data)
	return serviceClaimResultWithArtifact("vfs "+data.Scope+" "+data.Operation+" "+data.Status, artifact, err, "vfs_operation_artifact_error", data.MountRef)
}

func (s *InfrastructureService) handleToolRuntime(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest) (ServiceClaimResult, error) {
	data, err := normalizeToolRuntimeExecution(ctx, call, req, s.clock, s.outputSummaryLimit)
	if err != nil {
		data = failToolRuntimeExecution(ToolRuntimeExecutionArtifactData{ToolName: firstNonEmpty(stringArg(call.Arguments, "tool_name"), stringArg(call.Arguments, "tool"), call.Tool)}, err.Error())
	} else if s.toolBackend == nil {
		data = failToolRuntimeExecution(data, "tool runtime backend not configured")
	} else if backendData, backendErr := s.toolBackend.HandleToolRuntimeExecution(ctx, ToolRuntimeExecutionRequest{Board: req.Board, Claim: req.Claim, Delta: req.Delta, Participant: req.Participant, Call: call, Requested: data}); backendErr != nil {
		data = failToolRuntimeExecution(data, backendErr.Error())
	} else {
		data = finalizeToolBackendData(ctx, call, s.clock, s.outputSummaryLimit, data, backendData)
	}
	s.mu.Lock()
	if !s.tool.Upsert(infrastructureRecordKey(data.ToolName, call.ID, claimIDForInfrastructure(req.Claim)), data) {
		data = failToolRuntimeExecution(data, "tool runtime record capacity reached")
	}
	s.mu.Unlock()
	artifact, err := toolRuntimeExecutionArtifact(data)
	return serviceClaimResultWithArtifact("tool runtime "+data.ToolName+" "+data.Status, artifact, err, "tool_runtime_execution_artifact_error", data.ToolName)
}

func (s *InfrastructureService) handleKnowledge(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest) (ServiceClaimResult, error) {
	data, err := normalizeKnowledgeOperation(ctx, call, req)
	if err != nil {
		data = failKnowledgeOperation(KnowledgeOperationArtifactData{Operation: call.Tool}, err.Error())
	} else if s.knowledgeBackend == nil {
		data = failKnowledgeOperation(data, "knowledge backend not configured")
	} else if backendData, backendErr := s.knowledgeBackend.HandleKnowledgeOperation(ctx, KnowledgeOperationRequest{Board: req.Board, Claim: req.Claim, Delta: req.Delta, Participant: req.Participant, Call: call, Requested: data}); backendErr != nil {
		data = failKnowledgeOperation(data, backendErr.Error())
	} else {
		data = finalizeKnowledgeBackendData(ctx, call, data, backendData)
	}
	s.mu.Lock()
	if !s.knowledge.Upsert(infrastructureRecordKey(data.Operation, claimIDForInfrastructure(req.Claim), data.QueryShape), data) {
		data = failKnowledgeOperation(data, "knowledge record capacity reached")
	}
	s.mu.Unlock()
	artifact, err := knowledgeOperationArtifact(data)
	return serviceClaimResultWithArtifact("knowledge "+data.Operation+" "+data.Status, artifact, err, "knowledge_operation_artifact_error", data.Operation)
}

func (s *InfrastructureService) handleMemory(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest) (ServiceClaimResult, error) {
	data, err := normalizeMemoryContinuity(ctx, call, req)
	if err != nil {
		data = failMemoryContinuity(MemoryContinuityArtifactData{Operation: call.Tool, Topic: reqSessionID(req)}, err.Error())
	} else if s.memoryBackend == nil {
		data = failMemoryContinuity(data, "memory continuity backend not configured")
	} else if backendData, backendErr := s.memoryBackend.HandleMemoryContinuity(ctx, MemoryContinuityRequest{Board: req.Board, Claim: req.Claim, Delta: req.Delta, Participant: req.Participant, Call: call, Requested: data}); backendErr != nil {
		data = failMemoryContinuity(data, backendErr.Error())
	} else {
		data = finalizeMemoryBackendData(ctx, call, req, data, backendData)
	}
	s.mu.Lock()
	if !s.memory.Upsert(infrastructureRecordKey(data.Topic, data.AgentID, data.Operation), data) {
		data = failMemoryContinuity(data, "memory record capacity reached")
	}
	s.mu.Unlock()
	artifacts, err := memoryContinuityArtifacts(data)
	return serviceClaimResultWithArtifacts("memory "+data.Operation+" "+data.Status, artifacts, err, "memory_continuity_artifact_error", data.Topic)
}

func (s *InfrastructureService) handleDocument(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest) (ServiceClaimResult, error) {
	data, err := normalizeDocumentOperation(ctx, call, req)
	if err != nil {
		data = failDocumentOperation(DocumentOperationArtifactData{Operation: call.Tool, DocumentID: stringArg(call.Arguments, "document_id")}, err.Error())
	} else if s.documentBackend == nil {
		data = failDocumentOperation(data, "document backend not configured")
	} else if backendData, backendErr := s.documentBackend.HandleDocumentOperation(ctx, DocumentOperationRequest{Board: req.Board, Claim: req.Claim, Delta: req.Delta, Participant: req.Participant, Call: call, Requested: data}); backendErr != nil {
		data = failDocumentOperation(data, backendErr.Error())
	} else {
		data = finalizeDocumentBackendData(ctx, call, req, data, backendData)
	}
	s.mu.Lock()
	if !s.document.Upsert(infrastructureRecordKey(data.DocumentID, data.Operation, claimIDForInfrastructure(req.Claim)), data) {
		data = failDocumentOperation(data, "document record capacity reached")
	}
	s.mu.Unlock()
	artifact, err := documentOperationArtifact(data)
	return serviceClaimResultWithArtifact("document "+data.Operation+" "+data.Status, artifact, err, "document_operation_artifact_error", data.DocumentID)
}

func (s *InfrastructureService) handleGuardian(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest) (ServiceClaimResult, error) {
	data, err := normalizeGuardianDecision(ctx, call, req)
	if err != nil {
		data = failGuardianDecision(GuardianDecisionArtifactData{Operation: call.Tool}, err.Error())
	} else if s.guardianBackend == nil {
		data = failGuardianDecision(data, "guardian backend not configured")
	} else if backendData, backendErr := s.guardianBackend.HandleGuardianDecision(ctx, GuardianDecisionRequest{Board: req.Board, Claim: req.Claim, Delta: req.Delta, Participant: req.Participant, Call: call, Requested: data}); backendErr != nil {
		data = failGuardianDecision(data, backendErr.Error())
	} else {
		data = finalizeGuardianBackendData(ctx, call, data, backendData)
	}
	s.mu.Lock()
	if !s.guardian.Upsert(infrastructureRecordKey(data.ApprovalSubject, data.Operation, data.PolicyRule), data) {
		data = failGuardianDecision(data, "guardian record capacity reached")
	}
	s.mu.Unlock()
	artifact, err := guardianDecisionArtifact(data)
	return serviceClaimResultWithArtifact("guardian "+data.Operation+" "+data.Status, artifact, err, "guardian_decision_artifact_error", data.Operation)
}

func (s *InfrastructureService) handleProvider(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest) (ServiceClaimResult, error) {
	data, err := normalizeProviderGatewayCall(ctx, call, req, s.providerPartialLimit)
	if err != nil {
		data = failProviderGatewayCall(ProviderGatewayCallArtifactData{Operation: call.Tool, Model: stringArg(call.Arguments, "model")}, err.Error())
	} else if s.providerBackend == nil {
		data = failProviderGatewayCall(data, "provider gateway backend not configured")
	} else if backendData, backendErr := s.providerBackend.HandleProviderGatewayCall(ctx, ProviderGatewayCallRequest{Board: req.Board, Claim: req.Claim, Delta: req.Delta, Participant: req.Participant, Call: call, Requested: data}); backendErr != nil {
		data = failProviderGatewayCall(data, backendErr.Error())
	} else {
		data = finalizeProviderBackendData(ctx, call, req, s.providerPartialLimit, data, backendData)
	}
	s.mu.Lock()
	if !s.provider.Upsert(infrastructureRecordKey(data.ProviderRequestID, data.PromptHash, claimIDForInfrastructure(req.Claim)), data) {
		data = failProviderGatewayCall(data, "provider gateway record capacity reached")
	}
	s.mu.Unlock()
	artifacts, err := providerGatewayCallArtifacts(data)
	return serviceClaimResultWithArtifacts("provider "+data.Operation+" "+data.Status, artifacts, err, "provider_gateway_call_artifact_error", data.ProviderRequestID)
}

func (s *InfrastructureService) handleExternal(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest) (ServiceClaimResult, error) {
	data, err := normalizeExternalAdapterEvent(ctx, call, req, s.clock)
	if err != nil {
		data = failExternalAdapterEvent(ExternalAdapterEventArtifactData{Operation: call.Tool, Source: normalizedExternalSource(ParticipantRef{}, call.Arguments)}, err.Error())
	} else if s.externalBackend == nil {
		data = failExternalAdapterEvent(data, "external adapter backend not configured")
	} else if backendData, backendErr := s.externalBackend.HandleExternalAdapterEvent(ctx, ExternalAdapterEventRequest{Board: req.Board, Claim: req.Claim, Delta: req.Delta, Participant: req.Participant, Call: call, Requested: data}); backendErr != nil {
		data = failExternalAdapterEvent(data, backendErr.Error())
	} else {
		data = finalizeExternalBackendData(ctx, call, req, s.clock, data, backendData)
	}
	s.mu.Lock()
	if !s.external.Upsert(infrastructureRecordKey(data.IdempotencyKey, data.RawEventDigest, data.Operation), data) {
		data = failExternalAdapterEvent(data, "external adapter record capacity reached")
	}
	s.mu.Unlock()
	artifact, err := externalAdapterEventArtifact(data)
	return serviceClaimResultWithArtifact("external "+data.Operation+" "+data.Status, artifact, err, "external_adapter_event_artifact_error", data.IdempotencyKey)
}

func normalizeDAGOperation(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest) (DAGOperationArtifactData, error) {
	var data DAGOperationArtifactData
	if err := decodeArgs(call.Arguments, &data); err != nil {
		return data, fmt.Errorf("%w: %v", ErrInfrastructureServiceInvalid, err)
	}
	data.Operation = firstNonEmpty(data.Operation, call.Tool)
	data.PipelineID = firstNonEmpty(data.PipelineID, claimPipelineID(req.Claim), claimIDForInfrastructure(req.Claim))
	data.Nodes = normalizeStringList(data.Nodes)
	data.Edges = normalizeDAGEdges(data.Edges)
	data.RequiredNodes = normalizeStringList(data.RequiredNodes)
	data.ScopeBoundary = cloneStringMap(data.ScopeBoundary)
	data.Metadata = cloneMetadata(data.Metadata)
	data.NodeCount = countOrDerived(data.NodeCount, len(data.Nodes))
	data.EdgeCount = countOrDerived(data.EdgeCount, len(data.Edges))
	data.MissingRequiredNodes = missingRequiredNodes(data.RequiredNodes, data.Nodes)
	data.CyclePath = dagCyclePath(data.Nodes, data.Edges)
	data.CycleFree = len(data.CyclePath) == 0
	data.PlannedPods = countOrDerived(data.PlannedPods, data.NodeCount)
	data.DAGHash = firstNonEmpty(data.DAGHash, stableInfrastructureHash(data.Nodes, data.Edges, data.ScopeBoundary))
	data.FailureReason = firstNonEmpty(data.FailureReason, dagFailureReason(data))
	data.HealthSummary = firstNonEmpty(data.HealthSummary, dagHealthSummary(data))
	data.Status = infrastructureStatus(data.FailureReason, data.CycleFree, len(data.MissingRequiredNodes) == 0)
	if err := ctx.Err(); err != nil {
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data, nil
}

func normalizeVFSOperation(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest, scope string) (VFSOperationArtifactData, error) {
	var data VFSOperationArtifactData
	if err := decodeArgs(call.Arguments, &data); err != nil {
		return data, fmt.Errorf("%w: %v", ErrInfrastructureServiceInvalid, err)
	}
	data.Operation = firstNonEmpty(data.Operation, call.Tool)
	data.Scope = firstNonEmpty(data.Scope, scope, "workspace")
	data.MountRef = firstNonEmpty(data.MountRef, data.Scope+":"+firstNonEmpty(claimPipelineID(req.Claim), claimIDForInfrastructure(req.Claim)))
	data.ScopeBoundary = cloneStringMap(data.ScopeBoundary)
	data.COWLayerChain = normalizeStringList(data.COWLayerChain)
	data.ConflictSet = normalizeStringList(data.ConflictSet)
	data.Metadata = cloneMetadata(data.Metadata)
	data.SnapshotHash = firstNonEmpty(data.SnapshotHash, hashForSnapshot(data))
	data.ResultingVersion = firstNonEmpty(data.ResultingVersion, resultingVersionForVFS(data))
	data.Attached = vfsAttachedState(data)
	data.FailureReason = firstNonEmpty(data.FailureReason, vfsFailureReason(data))
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Interrupted = true
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data, nil
}

func normalizeToolRuntimeExecution(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest, clock ClaimsClock, limit int) (ToolRuntimeExecutionArtifactData, error) {
	var data ToolRuntimeExecutionArtifactData
	if err := decodeArgs(call.Arguments, &data); err != nil {
		return data, fmt.Errorf("%w: %v", ErrInfrastructureServiceInvalid, err)
	}
	now := firstNonNilClock(clock).Now()
	data.ToolName = firstNonEmpty(data.ToolName, stringArg(call.Arguments, "tool_name"), stringArg(call.Arguments, "tool"), call.Tool)
	data.RedactedArgs = redactedToolArgs(data.RedactedArgs, call.Arguments)
	data.PolicyDecision = firstNonEmpty(data.PolicyDecision, "allowed")
	data.ExecutionMode = firstNonEmpty(data.ExecutionMode, "sandboxed")
	data.SandboxScope = cloneStringMap(data.SandboxScope)
	data.StartedAt = firstNonZeroTime(data.StartedAt, now)
	data.EndedAt = firstNonZeroTime(data.EndedAt, data.StartedAt)
	data.Duration = firstNonZeroDuration(data.Duration, data.EndedAt.Sub(data.StartedAt))
	data.OutputSummary, data.ContentTruncated = boundedOutputSummary(data.OutputSummary, limit)
	data.OutputRefs = normalizeStringList(data.OutputRefs)
	data.ProducedArtifactRefs = normalizeStringList(data.ProducedArtifactRefs)
	data.Metadata = cloneMetadata(data.Metadata)
	data.FailureReason = firstNonEmpty(data.FailureReason, toolRuntimeFailureReason(data, call))
	if data.FailureReason == "tool approval missing" {
		data.PolicyDecision = "missing_approval"
	}
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Interrupted = true
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data, nil
}

func normalizeKnowledgeOperation(ctx context.Context, call ExpectedToolCall, _ ServiceClaimRequest) (KnowledgeOperationArtifactData, error) {
	var data KnowledgeOperationArtifactData
	if err := decodeArgs(call.Arguments, &data); err != nil {
		return data, fmt.Errorf("%w: %v", ErrInfrastructureServiceInvalid, err)
	}
	data.Operation = firstNonEmpty(data.Operation, call.Tool)
	data.VectorIndexRefs = normalizeStringList(data.VectorIndexRefs)
	data.QueryShape = firstNonEmpty(data.QueryShape, defaultKnowledgeQueryShape(data.Operation))
	data.Backend = firstNonEmpty(data.Backend, "vectorgraphdb")
	data.FullTextBackend = firstNonEmpty(data.FullTextBackend, "bleve")
	data.MissingNodes = normalizeStringList(data.MissingNodes)
	data.DanglingEdges = normalizeStringList(data.DanglingEdges)
	data.Metadata = cloneMetadata(data.Metadata)
	data.Ready = boolArgDefault(call.Arguments, "ready", data.Ready || data.FailureReason == "")
	data.FailureReason = firstNonEmpty(data.FailureReason, knowledgeFailureReason(data))
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data, nil
}

func normalizeMemoryContinuity(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest) (MemoryContinuityArtifactData, error) {
	var data MemoryContinuityArtifactData
	if err := decodeArgs(call.Arguments, &data); err != nil {
		return data, fmt.Errorf("%w: %v", ErrInfrastructureServiceInvalid, err)
	}
	data.Operation = firstNonEmpty(data.Operation, call.Tool)
	data.Topic = firstNonEmpty(data.Topic, reqSessionID(req), "default")
	data.AgentID = firstNonEmpty(data.AgentID, IssuerAgentID(claimRelations(req.Claim)), claimAgentID(req.Claim))
	data.SourceIndex = normalizeStringList(data.SourceIndex)
	data.SourceAvailable = boolArgDefault(call.Arguments, "source_available", true)
	if data.LegacyNoWAL {
		data.SourceAvailable = false
	}
	data.EvidenceDigest = firstNonEmpty(data.EvidenceDigest, stableInfrastructureHash(data.WorkingContext, data.SourceIndex, data.FromSeq, data.ThroughSeq))
	data.ContinuityCursor = firstNonEmpty(data.ContinuityCursor, continuityCursor(data))
	data.SessionCursor = firstNonEmpty(data.SessionCursor, reqSessionID(req))
	data.Metadata = cloneMetadata(data.Metadata)
	data.FailureReason = firstNonEmpty(data.FailureReason, memoryFailureReason(data))
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data, nil
}

func normalizeDocumentOperation(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest) (DocumentOperationArtifactData, error) {
	var data DocumentOperationArtifactData
	if err := decodeArgs(call.Arguments, &data); err != nil {
		return data, fmt.Errorf("%w: %v", ErrInfrastructureServiceInvalid, err)
	}
	data.Operation = firstNonEmpty(data.Operation, call.Tool)
	data.DocumentID = firstNonEmpty(data.DocumentID, stringArg(call.Arguments, "path"), claimIDForInfrastructure(req.Claim))
	data.IndexPath = firstNonEmpty(data.IndexPath, "bleve:"+data.DocumentID)
	data.ContentHash = firstNonEmpty(data.ContentHash, documentContentHash(call.Arguments))
	data.FullTextBackend = firstNonEmpty(data.FullTextBackend, "bleve")
	data.ChunkCount = countOrDerived(data.ChunkCount, len(data.ChunkBoundaries))
	data.ResultShape = firstNonEmpty(data.ResultShape, defaultDocumentResultShape(data))
	data.AttachmentRefs = normalizeStringList(data.AttachmentRefs)
	data.Metadata = cloneMetadata(data.Metadata)
	data.Indexed = boolArgDefault(call.Arguments, "indexed", !data.Deleted && data.FailureReason == "")
	data.FailureReason = firstNonEmpty(data.FailureReason, documentFailureReason(data))
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data, nil
}

func normalizeGuardianDecision(ctx context.Context, call ExpectedToolCall, _ ServiceClaimRequest) (GuardianDecisionArtifactData, error) {
	var data GuardianDecisionArtifactData
	if err := decodeArgs(call.Arguments, &data); err != nil {
		return data, fmt.Errorf("%w: %v", ErrInfrastructureServiceInvalid, err)
	}
	data.Operation = firstNonEmpty(data.Operation, call.Tool)
	data.PolicyRule = firstNonEmpty(data.PolicyRule, "default")
	data.UserDecision = strings.ToLower(firstNonEmpty(data.UserDecision, "allow"))
	data.DiffFindings = normalizeStringList(data.DiffFindings)
	data.ApprovalPresent = boolArgDefault(call.Arguments, "approval_present", data.UserDecision == "allow")
	data.BranchProtected = boolArgDefault(call.Arguments, "branch_protected", data.BranchState != "unprotected")
	data.Allowed = boolArgDefault(call.Arguments, "allowed", guardianAllowed(data))
	data.Metadata = cloneMetadata(data.Metadata)
	data.FailureReason = firstNonEmpty(data.FailureReason, guardianFailureReason(data))
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data, nil
}

func normalizeProviderGatewayCall(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest, partialLimit int) (ProviderGatewayCallArtifactData, error) {
	var data ProviderGatewayCallArtifactData
	if err := decodeArgs(call.Arguments, &data); err != nil {
		return data, fmt.Errorf("%w: %v", ErrInfrastructureServiceInvalid, err)
	}
	data.Operation = firstNonEmpty(data.Operation, call.Tool)
	data.Identity = firstNonEmpty(data.Identity, claimAgentID(req.Claim), IssuerAgentID(claimRelations(req.Claim)))
	data.TaskRef = firstNonEmpty(data.TaskRef, claimTaskID(req.Claim))
	data.TotalTokens = countOrDerived(data.TotalTokens, data.InputTokens+data.OutputTokens)
	data.ProviderRequestID = firstNonEmpty(data.ProviderRequestID, stableInfrastructureHash(data.Model, data.PromptHash, data.TaskRef))
	data.PartialSummaries = boundedStringList(data.PartialSummaries, partialLimit)
	data.Metadata = cloneMetadata(data.Metadata)
	data.FailureReason = firstNonEmpty(data.FailureReason, providerFailureReason(data))
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data, nil
}

func normalizeExternalAdapterEvent(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest, clock ClaimsClock) (ExternalAdapterEventArtifactData, error) {
	var data ExternalAdapterEventArtifactData
	if err := decodeArgs(call.Arguments, &data); err != nil {
		return data, fmt.Errorf("%w: %v", ErrInfrastructureServiceInvalid, err)
	}
	now := firstNonNilClock(clock).Now()
	data.Operation = firstNonEmpty(data.Operation, call.Tool)
	data.Source = normalizedExternalSource(data.Source, call.Arguments)
	data.IdempotencyKey = firstNonEmpty(data.IdempotencyKey, call.ID, stableInfrastructureHash(data.Source.UID, data.RawEventDigest, data.Operation))
	data.RawEventDigest = firstNonEmpty(data.RawEventDigest, rawEventDigest(call.Arguments, data.ParsedPayload))
	data.ReceivedAt = firstNonZeroTime(data.ReceivedAt, now)
	data.Fresh = boolArgDefault(call.Arguments, "fresh", externalEventFresh(data.EventTimestamp, now))
	data.ReferencedClaimID = strings.TrimSpace(data.ReferencedClaimID)
	data.Valid = boolArgDefault(call.Arguments, "valid", data.FailureReason == "")
	data.Metadata = cloneMetadata(data.Metadata)
	data.FailureReason = firstNonEmpty(data.FailureReason, externalFailureReason(data, req.Board))
	data.Rejected = data.FailureReason != ""
	data.Valid = data.Valid && !data.Rejected
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
		data.Rejected = true
		data.Valid = false
	}
	return data, nil
}

func finalizeDAGBackendData(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest, requested, data DAGOperationArtifactData) DAGOperationArtifactData {
	data.Operation = firstNonEmpty(data.Operation, requested.Operation, call.Tool)
	data.PipelineID = firstNonEmpty(data.PipelineID, requested.PipelineID, claimPipelineID(req.Claim), claimIDForInfrastructure(req.Claim))
	if len(data.Nodes) == 0 {
		data.Nodes = requested.Nodes
	}
	if len(data.Edges) == 0 {
		data.Edges = requested.Edges
	}
	if len(data.RequiredNodes) == 0 {
		data.RequiredNodes = requested.RequiredNodes
	}
	data.Nodes = normalizeStringList(data.Nodes)
	data.Edges = normalizeDAGEdges(data.Edges)
	data.RequiredNodes = normalizeStringList(data.RequiredNodes)
	data.ScopeBoundary = firstNonNilStringMap(data.ScopeBoundary, requested.ScopeBoundary)
	data.Metadata = mergeMetadata(requested.Metadata, data.Metadata)
	data.NodeCount = countOrDerived(data.NodeCount, len(data.Nodes))
	data.EdgeCount = countOrDerived(data.EdgeCount, len(data.Edges))
	if len(data.MissingRequiredNodes) == 0 {
		data.MissingRequiredNodes = missingRequiredNodes(data.RequiredNodes, data.Nodes)
	}
	if len(data.CyclePath) == 0 {
		data.CyclePath = dagCyclePath(data.Nodes, data.Edges)
	}
	data.CycleFree = len(data.CyclePath) == 0
	data.PlannedPods = countOrDerived(data.PlannedPods, countOrDerived(requested.PlannedPods, data.NodeCount))
	data.DAGHash = firstNonEmpty(data.DAGHash, requested.DAGHash, stableInfrastructureHash(data.Nodes, data.Edges, data.ScopeBoundary))
	data.FailureReason = firstNonEmpty(data.FailureReason, dagFailureReason(data))
	data.HealthSummary = firstNonEmpty(data.HealthSummary, dagHealthSummary(data))
	data.Status = infrastructureStatus(data.FailureReason, data.CycleFree, len(data.MissingRequiredNodes) == 0)
	if err := ctx.Err(); err != nil {
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data
}

func finalizeVFSBackendData(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest, scope string, requested, data VFSOperationArtifactData) VFSOperationArtifactData {
	data.Operation = firstNonEmpty(data.Operation, requested.Operation, call.Tool)
	data.Scope = firstNonEmpty(data.Scope, requested.Scope, scope, "workspace")
	data.MountRef = firstNonEmpty(data.MountRef, requested.MountRef, data.Scope+":"+firstNonEmpty(claimPipelineID(req.Claim), claimIDForInfrastructure(req.Claim)))
	data.ScopeBoundary = firstNonNilStringMap(data.ScopeBoundary, requested.ScopeBoundary)
	if len(data.COWLayerChain) == 0 {
		data.COWLayerChain = requested.COWLayerChain
	}
	if len(data.ConflictSet) == 0 {
		data.ConflictSet = requested.ConflictSet
	}
	data.COWLayerChain = normalizeStringList(data.COWLayerChain)
	data.ConflictSet = normalizeStringList(data.ConflictSet)
	data.Metadata = mergeMetadata(requested.Metadata, data.Metadata)
	data.SnapshotHash = firstNonEmpty(data.SnapshotHash, requested.SnapshotHash, hashForSnapshot(data))
	data.ResultingVersion = firstNonEmpty(data.ResultingVersion, requested.ResultingVersion, resultingVersionForVFS(data))
	data.Attached = data.Attached || requested.Attached || vfsAttachedState(data)
	data.FailureReason = firstNonEmpty(data.FailureReason, vfsFailureReason(data))
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Interrupted = true
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data
}

func finalizeToolBackendData(ctx context.Context, call ExpectedToolCall, clock ClaimsClock, limit int, requested, data ToolRuntimeExecutionArtifactData) ToolRuntimeExecutionArtifactData {
	now := firstNonNilClock(clock).Now()
	data.ToolName = firstNonEmpty(data.ToolName, requested.ToolName, stringArg(call.Arguments, "tool_name"), stringArg(call.Arguments, "tool"), call.Tool)
	if data.RedactedArgs == nil {
		data.RedactedArgs = requested.RedactedArgs
	}
	data.PolicyDecision = firstNonEmpty(data.PolicyDecision, requested.PolicyDecision, "allowed")
	data.ExecutionMode = firstNonEmpty(data.ExecutionMode, requested.ExecutionMode, "sandboxed")
	data.SandboxScope = firstNonNilStringMap(data.SandboxScope, requested.SandboxScope)
	data.StartedAt = firstNonZeroTime(data.StartedAt, firstNonZeroTime(requested.StartedAt, now))
	data.EndedAt = firstNonZeroTime(data.EndedAt, firstNonZeroTime(requested.EndedAt, data.StartedAt))
	data.Duration = firstNonZeroDuration(data.Duration, firstNonZeroDuration(requested.Duration, data.EndedAt.Sub(data.StartedAt)))
	data.OutputSummary = firstNonEmpty(data.OutputSummary, requested.OutputSummary)
	data.OutputSummary, data.ContentTruncated = boundedOutputSummary(data.OutputSummary, limit)
	data.ContentTruncated = data.ContentTruncated || requested.ContentTruncated
	if len(data.OutputRefs) == 0 {
		data.OutputRefs = requested.OutputRefs
	}
	if len(data.ProducedArtifactRefs) == 0 {
		data.ProducedArtifactRefs = requested.ProducedArtifactRefs
	}
	data.OutputRefs = normalizeStringList(data.OutputRefs)
	data.ProducedArtifactRefs = normalizeStringList(data.ProducedArtifactRefs)
	data.Metadata = mergeMetadata(requested.Metadata, data.Metadata)
	data.FailureReason = firstNonEmpty(data.FailureReason, toolRuntimeFailureReason(data, call))
	if data.FailureReason == "tool approval missing" {
		data.PolicyDecision = "missing_approval"
	}
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Interrupted = true
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data
}

func finalizeKnowledgeBackendData(ctx context.Context, call ExpectedToolCall, requested, data KnowledgeOperationArtifactData) KnowledgeOperationArtifactData {
	backendReady := data.Ready
	data.Operation = firstNonEmpty(data.Operation, requested.Operation, call.Tool)
	if len(data.VectorIndexRefs) == 0 {
		data.VectorIndexRefs = requested.VectorIndexRefs
	}
	if len(data.MissingNodes) == 0 {
		data.MissingNodes = requested.MissingNodes
	}
	if len(data.DanglingEdges) == 0 {
		data.DanglingEdges = requested.DanglingEdges
	}
	data.VectorIndexRefs = normalizeStringList(data.VectorIndexRefs)
	data.MissingNodes = normalizeStringList(data.MissingNodes)
	data.DanglingEdges = normalizeStringList(data.DanglingEdges)
	data.QueryShape = firstNonEmpty(data.QueryShape, requested.QueryShape, defaultKnowledgeQueryShape(data.Operation))
	data.Backend = firstNonEmpty(data.Backend, requested.Backend, "vectorgraphdb")
	data.FullTextBackend = firstNonEmpty(data.FullTextBackend, requested.FullTextBackend, "bleve")
	data.Metadata = mergeMetadata(requested.Metadata, data.Metadata)
	data.Ready = backendReady
	data.FailureReason = firstNonEmpty(data.FailureReason, knowledgeFailureReason(data))
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data
}

func finalizeMemoryBackendData(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest, requested, data MemoryContinuityArtifactData) MemoryContinuityArtifactData {
	sourceAvailable := data.SourceAvailable
	data.Operation = firstNonEmpty(data.Operation, requested.Operation, call.Tool)
	data.Topic = firstNonEmpty(data.Topic, requested.Topic, reqSessionID(req), "default")
	data.AgentID = firstNonEmpty(data.AgentID, requested.AgentID, IssuerAgentID(claimRelations(req.Claim)), claimAgentID(req.Claim))
	data.LegacyNoWAL = data.LegacyNoWAL || requested.LegacyNoWAL
	if len(data.SourceIndex) == 0 {
		data.SourceIndex = requested.SourceIndex
	}
	data.SourceIndex = normalizeStringList(data.SourceIndex)
	data.WorkingContext = firstNonEmpty(data.WorkingContext, requested.WorkingContext)
	data.SourceAvailable = sourceAvailable
	if data.LegacyNoWAL {
		data.SourceAvailable = false
	}
	data.EvidenceDigest = firstNonEmpty(data.EvidenceDigest, requested.EvidenceDigest, stableInfrastructureHash(data.WorkingContext, data.SourceIndex, data.FromSeq, data.ThroughSeq))
	data.ContinuityCursor = firstNonEmpty(data.ContinuityCursor, requested.ContinuityCursor, continuityCursor(data))
	data.SessionCursor = firstNonEmpty(data.SessionCursor, requested.SessionCursor, reqSessionID(req))
	data.Metadata = mergeMetadata(requested.Metadata, data.Metadata)
	data.FailureReason = firstNonEmpty(data.FailureReason, memoryFailureReason(data))
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data
}

func finalizeDocumentBackendData(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest, requested, data DocumentOperationArtifactData) DocumentOperationArtifactData {
	indexed := data.Indexed
	data.Operation = firstNonEmpty(data.Operation, requested.Operation, call.Tool)
	data.DocumentID = firstNonEmpty(data.DocumentID, requested.DocumentID, stringArg(call.Arguments, "path"), claimIDForInfrastructure(req.Claim))
	data.IndexPath = firstNonEmpty(data.IndexPath, requested.IndexPath, "bleve:"+data.DocumentID)
	data.ContentHash = firstNonEmpty(data.ContentHash, requested.ContentHash, documentContentHash(call.Arguments))
	data.FullTextBackend = firstNonEmpty(data.FullTextBackend, requested.FullTextBackend, "bleve")
	if len(data.ChunkBoundaries) == 0 {
		data.ChunkBoundaries = requested.ChunkBoundaries
	}
	data.ChunkCount = countOrDerived(data.ChunkCount, countOrDerived(requested.ChunkCount, len(data.ChunkBoundaries)))
	data.ResultShape = firstNonEmpty(data.ResultShape, requested.ResultShape, defaultDocumentResultShape(data))
	if len(data.AttachmentRefs) == 0 {
		data.AttachmentRefs = requested.AttachmentRefs
	}
	data.AttachmentRefs = normalizeStringList(data.AttachmentRefs)
	data.Metadata = mergeMetadata(requested.Metadata, data.Metadata)
	data.Indexed = indexed
	data.FailureReason = firstNonEmpty(data.FailureReason, documentFailureReason(data))
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data
}

func finalizeGuardianBackendData(ctx context.Context, call ExpectedToolCall, requested, data GuardianDecisionArtifactData) GuardianDecisionArtifactData {
	approvalPresent := data.ApprovalPresent
	branchProtected := data.BranchProtected
	allowed := data.Allowed
	data.Operation = firstNonEmpty(data.Operation, requested.Operation, call.Tool)
	data.PolicyRule = firstNonEmpty(data.PolicyRule, requested.PolicyRule, "default")
	data.UserDecision = strings.ToLower(firstNonEmpty(data.UserDecision, requested.UserDecision, "allow"))
	if len(data.DiffFindings) == 0 {
		data.DiffFindings = requested.DiffFindings
	}
	data.DiffFindings = normalizeStringList(data.DiffFindings)
	data.ApprovalPresent = approvalPresent
	data.BranchProtected = branchProtected
	data.Allowed = allowed
	data.Metadata = mergeMetadata(requested.Metadata, data.Metadata)
	data.FailureReason = firstNonEmpty(data.FailureReason, guardianFailureReason(data))
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data
}

func finalizeProviderBackendData(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest, partialLimit int, requested, data ProviderGatewayCallArtifactData) ProviderGatewayCallArtifactData {
	data.Operation = firstNonEmpty(data.Operation, requested.Operation, call.Tool)
	data.Identity = firstNonEmpty(data.Identity, requested.Identity, claimAgentID(req.Claim), IssuerAgentID(claimRelations(req.Claim)))
	data.TaskRef = firstNonEmpty(data.TaskRef, requested.TaskRef, claimTaskID(req.Claim))
	data.Model = firstNonEmpty(data.Model, requested.Model)
	data.PromptHash = firstNonEmpty(data.PromptHash, requested.PromptHash)
	data.StreamingMode = firstNonEmpty(data.StreamingMode, requested.StreamingMode)
	data.TotalTokens = countOrDerived(data.TotalTokens, data.InputTokens+data.OutputTokens)
	data.ProviderRequestID = firstNonEmpty(data.ProviderRequestID, requested.ProviderRequestID, stableInfrastructureHash(data.Model, data.PromptHash, data.TaskRef))
	if len(data.PartialSummaries) == 0 {
		data.PartialSummaries = requested.PartialSummaries
	}
	data.PartialSummaries = boundedStringList(data.PartialSummaries, partialLimit)
	data.Metadata = mergeMetadata(requested.Metadata, data.Metadata)
	data.RateLimited = data.RateLimited || providerGatewayRateLimited(data.Error) || providerGatewayRateLimited(data.FailureReason)
	data.FailureReason = firstNonEmpty(data.FailureReason, providerFailureReason(data))
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
	}
	return data
}

func finalizeExternalBackendData(ctx context.Context, call ExpectedToolCall, req ServiceClaimRequest, clock ClaimsClock, requested, data ExternalAdapterEventArtifactData) ExternalAdapterEventArtifactData {
	fresh := data.Fresh
	valid := data.Valid
	now := firstNonNilClock(clock).Now()
	data.Operation = firstNonEmpty(data.Operation, requested.Operation, call.Tool)
	if data.Source.UID == "" {
		data.Source = requested.Source
	}
	data.Source = normalizedExternalSource(data.Source, call.Arguments)
	data.IdempotencyKey = firstNonEmpty(data.IdempotencyKey, requested.IdempotencyKey, call.ID, stableInfrastructureHash(data.Source.UID, data.RawEventDigest, data.Operation))
	data.RawEventDigest = firstNonEmpty(data.RawEventDigest, requested.RawEventDigest, rawEventDigest(call.Arguments, data.ParsedPayload))
	data.ReceivedAt = firstNonZeroTime(data.ReceivedAt, firstNonZeroTime(requested.ReceivedAt, now))
	data.Fresh = fresh
	data.ReferencedClaimID = firstNonEmpty(strings.TrimSpace(data.ReferencedClaimID), requested.ReferencedClaimID)
	data.Valid = valid
	data.Metadata = mergeMetadata(requested.Metadata, data.Metadata)
	data.FailureReason = firstNonEmpty(data.FailureReason, externalFailureReason(data, req.Board))
	data.Rejected = data.FailureReason != ""
	data.Valid = data.Valid && !data.Rejected
	data.Status = statusFromFailure(data.FailureReason)
	if err := ctx.Err(); err != nil {
		data.Status = InfrastructureStatusInterrupted
		data.FailureReason = err.Error()
		data.Rejected = true
		data.Valid = false
	}
	return data
}

func dagOperationArtifact(data DAGOperationArtifactData) (*Artifact, error) {
	return infrastructureArtifact("dag_operation", ArtifactKindDAGOperation, data.DAGHash, data.FailureReason, data)
}

func vfsOperationArtifact(data VFSOperationArtifactData) (*Artifact, error) {
	return infrastructureArtifact("vfs_operation", ArtifactKindVFSOperation, data.MountRef, data.FailureReason, data)
}

func NewVFSOperationArtifact(data VFSOperationArtifactData) (*Artifact, error) {
	return vfsOperationArtifact(data)
}

func NewBootPhaseArtifact(data BootPhaseArtifactData) (*Artifact, error) {
	kind := bootPhaseArtifactKind(data)
	return infrastructureArtifact("boot_phase", kind, data.Phase, data.FailureReason, data)
}

func toolRuntimeExecutionArtifact(data ToolRuntimeExecutionArtifactData) (*Artifact, error) {
	kind := ArtifactKindToolRuntimeExecution
	if data.PolicyDecision == "denied" || data.PolicyDecision == "missing_approval" {
		kind = ArtifactKindPolicyDenied
	}
	return infrastructureArtifact("tool_runtime_execution", kind, data.ToolName, data.FailureReason, data)
}

func NewToolRuntimeExecutionArtifact(data ToolRuntimeExecutionArtifactData) (*Artifact, error) {
	return toolRuntimeExecutionArtifact(data)
}

func knowledgeOperationArtifact(data KnowledgeOperationArtifactData) (*Artifact, error) {
	return infrastructureArtifact("knowledge_operation", ArtifactKindKnowledgeOperation, data.Operation, data.FailureReason, data)
}

func memoryContinuityArtifacts(data MemoryContinuityArtifactData) ([]*Artifact, error) {
	artifact, err := infrastructureArtifact("memory_continuity", ArtifactKindMemoryContinuity, data.Topic, data.FailureReason, data)
	if err != nil {
		return nil, err
	}
	return []*Artifact{artifact}, nil
}

func documentOperationArtifact(data DocumentOperationArtifactData) (*Artifact, error) {
	return infrastructureArtifact("document_operation", ArtifactKindDocumentOperation, data.DocumentID, data.FailureReason, data)
}

func guardianDecisionArtifact(data GuardianDecisionArtifactData) (*Artifact, error) {
	kind := ArtifactKindGuardianDecision
	if data.DenialReason != "" || !data.Allowed {
		kind = ArtifactKindPolicyDenied
	}
	return infrastructureArtifact("guardian_decision", kind, data.Operation, data.FailureReason, data)
}

func NewGuardianDecisionArtifact(data GuardianDecisionArtifactData) (*Artifact, error) {
	return guardianDecisionArtifact(data)
}

func providerGatewayCallArtifact(data ProviderGatewayCallArtifactData) (*Artifact, error) {
	return infrastructureArtifact("provider_gateway_call", providerGatewayPrimaryArtifactKind(data), data.ProviderRequestID, data.FailureReason, data)
}

func providerGatewayCallArtifacts(data ProviderGatewayCallArtifactData) ([]*Artifact, error) {
	primary, err := providerGatewayCallArtifact(data)
	if err != nil {
		return nil, err
	}
	artifacts := []*Artifact{primary}
	for _, secondary := range providerGatewaySecondaryArtifactSpecs(data) {
		artifact, err := infrastructureArtifact(secondary.name, secondary.kind, data.ProviderRequestID, secondary.failure, data)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

type providerGatewayArtifactSpec struct {
	name    string
	kind    string
	failure string
}

func providerGatewayPrimaryArtifactKind(data ProviderGatewayCallArtifactData) string {
	if data.RateLimited {
		return ArtifactKindRateLimitEncounter
	}
	if data.Error != "" || data.FailureReason != "" {
		return ArtifactKindProviderFailure
	}
	if data.Operation == ProviderGatewayToolCountTokens {
		return ArtifactKindUsage
	}
	return ArtifactKindLLMResponse
}

func providerGatewaySecondaryArtifactSpecs(data ProviderGatewayCallArtifactData) []providerGatewayArtifactSpec {
	specs := make([]providerGatewayArtifactSpec, 0, providerGatewaySecondaryArtifactCount(data))
	if providerGatewayHasUsage(data) && providerGatewayPrimaryArtifactKind(data) != ArtifactKindUsage {
		specs = append(specs, providerGatewayArtifactSpec{name: "usage", kind: ArtifactKindUsage})
	}
	if providerGatewayHasCacheHit(data) {
		specs = append(specs, providerGatewayArtifactSpec{name: "cache_hit", kind: ArtifactKindCacheHit})
	}
	if providerGatewayHasRetryRecord(data) {
		specs = append(specs, providerGatewayArtifactSpec{name: "retry_record", kind: ArtifactKindRetryRecord})
	}
	if data.RateLimited && providerGatewayPrimaryArtifactKind(data) != ArtifactKindRateLimitEncounter {
		specs = append(specs, providerGatewayArtifactSpec{name: "rate_limit_encounter", kind: ArtifactKindRateLimitEncounter, failure: data.FailureReason})
	}
	return specs
}

func providerGatewaySecondaryArtifactCount(data ProviderGatewayCallArtifactData) int {
	count := 0
	if providerGatewayHasUsage(data) {
		count++
	}
	if providerGatewayHasCacheHit(data) {
		count++
	}
	if providerGatewayHasRetryRecord(data) {
		count++
	}
	if data.RateLimited {
		count++
	}
	return count
}

func externalAdapterEventArtifact(data ExternalAdapterEventArtifactData) (*Artifact, error) {
	return infrastructureArtifact("external_adapter_event", ArtifactKindExternalAdapterEvent, data.IdempotencyKey, data.FailureReason, data)
}

func infrastructureArtifact[T any](name, kind, reference, failure string, data T) (*Artifact, error) {
	if failure != "" && !isErrorArtifactKind(kind) {
		kind = ArtifactKindErrorDiagnostic
	}
	artifact := &Artifact{ArtifactName: name, Kind: kind, Reference: reference}
	if err := SetArtifactData(artifact, data); err != nil {
		return nil, err
	}
	if failure != "" {
		artifact.Errors = append(artifact.Errors, &ArtifactError{Category: ArtifactErrorCategoryInternal, Description: failure, OccurredAt: time.Now().UTC()})
	}
	return artifact, nil
}

func bootPhaseArtifactKind(data BootPhaseArtifactData) string {
	if data.FailureReason != "" || data.Status == InfrastructureStatusFailed || data.Status == InfrastructureStatusInterrupted {
		return ArtifactKindBootFailure
	}
	switch strings.TrimSpace(data.Phase) {
	case "setup":
		return ArtifactKindBootSetupComplete
	case "detect":
		return ArtifactKindBootDetectResult
	case "allocate":
		return ArtifactKindBootAllocateOutcome
	case "ingest":
		return ArtifactKindBootIngestStatus
	case "commit":
		return ArtifactKindBootCommitRef
	case "finalize":
		return ArtifactKindBootFinalizeSignal
	default:
		return ArtifactKindBootPhase
	}
}

func infrastructureServiceCall(claim *Claim, handlers map[string]infrastructureToolHandler) (ExpectedToolCall, error) {
	for _, call := range claim.ExpectedToolCalls {
		if _, ok := handlers[strings.TrimSpace(call.Tool)]; ok {
			return call, nil
		}
	}
	return ExpectedToolCall{}, fmt.Errorf("%w: infrastructure expected tool call is required", ErrInfrastructureServiceInvalid)
}

func normalizeInfrastructureServiceConfig(cfg InfrastructureServiceConfig) InfrastructureServiceConfig {
	cfg.ParticipantID = strings.TrimSpace(cfg.ParticipantID)
	cfg.VFSScope = strings.TrimSpace(cfg.VFSScope)
	if cfg.MaxRecords <= 0 {
		cfg.MaxRecords = defaultInfrastructureMaxRecords
	}
	if cfg.OutputSummaryLimit <= 0 {
		cfg.OutputSummaryLimit = defaultInfrastructureOutputLimit
	}
	if cfg.ProviderPartialLimit <= 0 {
		cfg.ProviderPartialLimit = defaultInfrastructureProviderPartials
	}
	return cfg
}

func firstNonNilStringMap(primary, fallback map[string]string) map[string]string {
	if len(primary) != 0 {
		return cloneStringMap(primary)
	}
	return cloneStringMap(fallback)
}

func validateInfrastructureService(s *InfrastructureService) error {
	if s == nil || s.clock == nil || s.dag == nil || s.vfs == nil || s.tool == nil || s.provider == nil {
		return fmt.Errorf("%w: service is not initialized", ErrInfrastructureServiceInvalid)
	}
	return nil
}

func newBoundedInfrastructureRecords[T any](max int) *boundedInfrastructureRecords[T] {
	if max <= 0 {
		max = defaultInfrastructureMaxRecords
	}
	return &boundedInfrastructureRecords[T]{max: max, byKey: make(map[string]T, max)}
}

func (r *boundedInfrastructureRecords[T]) Upsert(key string, value T) bool {
	key = firstNonEmpty(strings.TrimSpace(key), stableInfrastructureHash(value))
	if _, ok := r.byKey[key]; ok {
		r.byKey[key] = value
		return true
	}
	if len(r.byKey) >= r.max {
		return false
	}
	r.byKey[key] = value
	r.order = append(r.order, key)
	return true
}

func (r *boundedInfrastructureRecords[T]) Values() []T {
	out := make([]T, 0, len(r.order))
	for _, key := range r.order {
		out = append(out, r.byKey[key])
	}
	return out
}

func stableInfrastructureHash(values ...any) string {
	data, err := json.Marshal(values)
	if err != nil {
		data = []byte(fmt.Sprint(values...))
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizeDAGEdges(edges []DAGEdgeArtifactData) []DAGEdgeArtifactData {
	if len(edges) == 0 {
		return nil
	}
	out := make([]DAGEdgeArtifactData, 0, len(edges))
	seen := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		edge.From = strings.TrimSpace(edge.From)
		edge.To = strings.TrimSpace(edge.To)
		key := edge.From + "\x00" + edge.To
		if edge.From == "" || edge.To == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, edge)
	}
	return out
}

func missingRequiredNodes(required, nodes []string) []string {
	if len(required) == 0 {
		return nil
	}
	nodeSet := stringSet(nodes)
	missing := make([]string, 0)
	for _, node := range required {
		if _, ok := nodeSet[node]; !ok {
			missing = append(missing, node)
		}
	}
	return missing
}

func dagCyclePath(nodes []string, edges []DAGEdgeArtifactData) []string {
	adjacency := dagAdjacency(nodes, edges)
	visiting := make(map[string]bool, len(adjacency))
	visited := make(map[string]bool, len(adjacency))
	for _, node := range sortedMapKeys(adjacency) {
		if path := dagCycleFrom(node, adjacency, visiting, visited, nil); len(path) != 0 {
			return path
		}
	}
	return nil
}

func dagCycleFrom(node string, adjacency map[string][]string, visiting, visited map[string]bool, stack []string) []string {
	if visiting[node] {
		return append(stack, node)
	}
	if visited[node] {
		return nil
	}
	visiting[node] = true
	stack = append(stack, node)
	for _, next := range adjacency[node] {
		if path := dagCycleFrom(next, adjacency, visiting, visited, stack); len(path) != 0 {
			return path
		}
	}
	visiting[node] = false
	visited[node] = true
	return nil
}

func dagAdjacency(nodes []string, edges []DAGEdgeArtifactData) map[string][]string {
	out := make(map[string][]string, len(nodes)+len(edges))
	for _, node := range nodes {
		out[node] = nil
	}
	for _, edge := range edges {
		out[edge.From] = append(out[edge.From], edge.To)
		if _, ok := out[edge.To]; !ok {
			out[edge.To] = nil
		}
	}
	for key := range out {
		sort.Strings(out[key])
	}
	return out
}

func dagHealthSummary(data DAGOperationArtifactData) string {
	if data.FailureReason != "" {
		return data.FailureReason
	}
	if !data.CycleFree {
		return "cycle detected"
	}
	if len(data.MissingRequiredNodes) != 0 {
		return "missing required nodes"
	}
	return "dag healthy"
}

func dagFailureReason(data DAGOperationArtifactData) string {
	if !data.CycleFree {
		return "cycle detected"
	}
	if len(data.MissingRequiredNodes) != 0 {
		return "missing required nodes"
	}
	if data.PlannedPods < 0 {
		return "planned pod count invalid"
	}
	return ""
}

func infrastructureStatus(failure string, conditions ...bool) string {
	if strings.TrimSpace(failure) != "" {
		return InfrastructureStatusFailed
	}
	for _, condition := range conditions {
		if !condition {
			return InfrastructureStatusFailed
		}
	}
	return InfrastructureStatusOK
}

func statusFromFailure(failure string) string {
	if strings.TrimSpace(failure) == "" {
		return InfrastructureStatusOK
	}
	return InfrastructureStatusFailed
}

func failDAGOperation(data DAGOperationArtifactData, reason string) DAGOperationArtifactData {
	data.FailureReason = firstNonEmpty(data.FailureReason, reason)
	data.Status = InfrastructureStatusFailed
	return data
}

func failVFSOperation(data VFSOperationArtifactData, reason string) VFSOperationArtifactData {
	data.FailureReason = firstNonEmpty(data.FailureReason, reason)
	data.Status = InfrastructureStatusFailed
	return data
}

func failToolRuntimeExecution(data ToolRuntimeExecutionArtifactData, reason string) ToolRuntimeExecutionArtifactData {
	data.FailureReason = firstNonEmpty(data.FailureReason, reason)
	data.Status = InfrastructureStatusFailed
	return data
}

func failKnowledgeOperation(data KnowledgeOperationArtifactData, reason string) KnowledgeOperationArtifactData {
	data.FailureReason = firstNonEmpty(data.FailureReason, reason)
	data.Status = InfrastructureStatusFailed
	return data
}

func failMemoryContinuity(data MemoryContinuityArtifactData, reason string) MemoryContinuityArtifactData {
	data.FailureReason = firstNonEmpty(data.FailureReason, reason)
	data.Status = InfrastructureStatusFailed
	return data
}

func failDocumentOperation(data DocumentOperationArtifactData, reason string) DocumentOperationArtifactData {
	data.FailureReason = firstNonEmpty(data.FailureReason, reason)
	data.Status = InfrastructureStatusFailed
	return data
}

func failGuardianDecision(data GuardianDecisionArtifactData, reason string) GuardianDecisionArtifactData {
	data.FailureReason = firstNonEmpty(data.FailureReason, reason)
	data.Status = InfrastructureStatusFailed
	data.Allowed = false
	return data
}

func failProviderGatewayCall(data ProviderGatewayCallArtifactData, reason string) ProviderGatewayCallArtifactData {
	data.FailureReason = firstNonEmpty(data.FailureReason, reason)
	data.Error = firstNonEmpty(data.Error, data.FailureReason)
	data.RateLimited = data.RateLimited || providerGatewayRateLimited(data.FailureReason)
	data.Status = InfrastructureStatusFailed
	return data
}

func failExternalAdapterEvent(data ExternalAdapterEventArtifactData, reason string) ExternalAdapterEventArtifactData {
	data.FailureReason = firstNonEmpty(data.FailureReason, reason)
	data.Status = InfrastructureStatusFailed
	data.Rejected = true
	data.Valid = false
	return data
}

func countOrDerived(value, derived int) int {
	if value > 0 {
		return value
	}
	return derived
}

func firstNonZeroDuration(value, fallback time.Duration) time.Duration {
	if value != 0 {
		return value
	}
	return fallback
}

func vfsAttachedState(data VFSOperationArtifactData) bool {
	if data.Operation == VFSProvisionerToolDetach || data.Operation == VFSProvisionerToolRollback {
		return false
	}
	return data.FailureReason == ""
}

func vfsFailureReason(data VFSOperationArtifactData) string {
	if data.CapacityBudgetBytes > 0 && data.CapacityUsedBytes > data.CapacityBudgetBytes {
		return "capacity budget exceeded"
	}
	if data.Operation == VFSProvisionerToolMerge && len(data.ConflictSet) != 0 {
		return "merge conflict"
	}
	return ""
}

func hashForSnapshot(data VFSOperationArtifactData) string {
	if data.Operation != VFSProvisionerToolSnapshot {
		return ""
	}
	return stableInfrastructureHash(data.MountRef, data.BaseVersion, data.COWLayerChain)
}

func resultingVersionForVFS(data VFSOperationArtifactData) string {
	switch data.Operation {
	case VFSProvisionerToolMerge, VFSProvisionerToolPromote, VFSProvisionerToolRollback:
		return stableInfrastructureHash(data.MountRef, data.BaseVersion, data.COWLayerChain, data.Operation)
	default:
		return ""
	}
}

func redactedToolArgs(existing map[string]any, args map[string]any) map[string]any {
	if len(existing) != 0 {
		return cloneAnyMap(existing)
	}
	raw, _ := args["args"].(map[string]any)
	if len(raw) == 0 {
		raw = cloneAnyMap(args)
	}
	out := make(map[string]any, len(raw))
	for key, value := range raw {
		if toolArgKeySensitive(key) {
			out[key] = "[redacted]"
			continue
		}
		out[key] = value
	}
	return out
}

func toolArgKeySensitive(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(key, "token") || strings.Contains(key, "secret") || strings.Contains(key, "password")
}

func boundedOutputSummary(summary string, limit int) (string, bool) {
	if limit <= 0 || len(summary) <= limit {
		return summary, false
	}
	return summary[:limit], true
}

func toolRuntimeFailureReason(data ToolRuntimeExecutionArtifactData, call ExpectedToolCall) string {
	if data.PolicyDecision == "denied" {
		return firstNonEmpty(data.DenialLikeReason(), "tool policy denied")
	}
	if call.Policy.RequiresUserApproval && data.GuardianClaimID == "" {
		return "tool approval missing"
	}
	if data.TimedOut {
		return "tool timed out"
	}
	if data.ExitStatus != 0 {
		return fmt.Sprintf("tool exited with status %d", data.ExitStatus)
	}
	return ""
}

func (data ToolRuntimeExecutionArtifactData) DenialLikeReason() string {
	return firstNonEmpty(data.FailureReason, data.PolicyRule)
}

func defaultKnowledgeQueryShape(operation string) string {
	switch operation {
	case KnowledgeToolQueryGraph:
		return "graph_traversal"
	case KnowledgeToolQuerySemantic:
		return "semantic_results"
	case KnowledgeToolQueryHybrid:
		return "hybrid_results"
	default:
		return "mutation_summary"
	}
}

func knowledgeFailureReason(data KnowledgeOperationArtifactData) string {
	if data.ExpectedEmbeddingDimension > 0 && data.EmbeddingDimension != data.ExpectedEmbeddingDimension {
		return "embedding dimension mismatch"
	}
	if len(data.DanglingEdges) != 0 {
		return "dangling knowledge edges"
	}
	if strings.Contains(strings.ToLower(data.FullTextBackend), "sqlite") {
		return "forbidden full-text backend"
	}
	return ""
}

func memoryFailureReason(data MemoryContinuityArtifactData) string {
	if data.LegacyNoWAL {
		return "durable carry-forward WAL missing"
	}
	if !data.SourceAvailable {
		return "carry-forward source unavailable"
	}
	if data.ThroughSeq < data.FromSeq {
		return "continuity cursor regressed"
	}
	if data.Contradicted {
		return "continuity contradicted"
	}
	return ""
}

func continuityCursor(data MemoryContinuityArtifactData) string {
	return stableInfrastructureHash(data.Topic, data.AgentID, data.FromSeq, data.ThroughSeq, data.EvidenceDigest)
}

func documentContentHash(args map[string]any) string {
	content := stringArg(args, "content")
	if content == "" {
		return ""
	}
	return stableInfrastructureHash(content)
}

func defaultDocumentResultShape(data DocumentOperationArtifactData) string {
	if data.Operation == DocumentToolLookup || data.Operation == DocumentToolSearchBleve || data.Operation == DocumentToolMetadataQuery {
		return "document_results"
	}
	return "document_mutation"
}

func documentFailureReason(data DocumentOperationArtifactData) string {
	if strings.Contains(strings.ToLower(data.FullTextBackend), "sqlite") {
		return "forbidden full-text backend"
	}
	if !validChunkBoundaries(data.ChunkBoundaries) {
		return "invalid chunk boundaries"
	}
	return ""
}

func validChunkBoundaries(boundaries []DocumentChunkBoundaryArtifactData) bool {
	priorEnd := 0
	for idx, boundary := range boundaries {
		if boundary.Index != idx || boundary.Start < priorEnd || boundary.End < boundary.Start {
			return false
		}
		priorEnd = boundary.End
	}
	return true
}

func guardianAllowed(data GuardianDecisionArtifactData) bool {
	return data.DenialReason == "" && data.UserDecision != "deny" && len(data.DiffFindings) == 0 && data.BranchProtected
}

func guardianFailureReason(data GuardianDecisionArtifactData) string {
	if data.DenialReason != "" {
		return data.DenialReason
	}
	if data.UserDecision == "deny" {
		return "user denied approval"
	}
	if !data.ApprovalPresent {
		return "approval missing"
	}
	if !data.BranchProtected {
		return "branch protection failed"
	}
	if len(data.DiffFindings) != 0 {
		return "diff findings present"
	}
	return ""
}

func providerFailureReason(data ProviderGatewayCallArtifactData) string {
	if data.Identity == "" {
		return "provider identity missing"
	}
	if data.Error != "" {
		return data.Error
	}
	if data.RateLimited {
		return "provider rate limited"
	}
	if data.BudgetTokens > 0 && data.TotalTokens > data.BudgetTokens {
		return "provider budget exceeded"
	}
	return ""
}

func providerGatewayRateLimited(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	return strings.Contains(reason, "rate limit") || strings.Contains(reason, "429")
}

func providerGatewayHasUsage(data ProviderGatewayCallArtifactData) bool {
	return data.InputTokens > 0 ||
		data.OutputTokens > 0 ||
		data.TotalTokens > 0 ||
		data.ReasoningTokens > 0 ||
		data.CacheReadTokens > 0 ||
		data.CacheWriteTokens > 0
}

func providerGatewayHasCacheHit(data ProviderGatewayCallArtifactData) bool {
	if data.CacheReadTokens > 0 {
		return true
	}
	value, _ := data.Metadata["cache_hit"].(bool)
	return value
}

func providerGatewayHasRetryRecord(data ProviderGatewayCallArtifactData) bool {
	if providerGatewayMetadataInt(data.Metadata, "retry_count") > 0 {
		return true
	}
	value, _ := data.Metadata["retry_record"].(bool)
	return value
}

func providerGatewayMetadataInt(metadata map[string]any, key string) int {
	switch value := metadata[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func boundedStringList(values []string, limit int) []string {
	values = normalizeStringList(values)
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return append([]string(nil), values[:limit]...)
}

func normalizedExternalSource(source ParticipantRef, args map[string]any) ParticipantRef {
	source = source.Normalized()
	if source.UID == "" {
		source.UID = firstNonEmpty(stringArg(args, "source_uid"), stringArg(args, "source_id"), "external")
	}
	source.Category = string(ParticipantCategoryExternal)
	if source.Type == "" {
		source.Type = firstNonEmpty(stringArg(args, "source_type"), "external")
	}
	if source.Name == "" {
		source.Name = source.Type
	}
	return source.Normalized()
}

func rawEventDigest(args map[string]any, payload map[string]any) string {
	if raw := stringArg(args, "raw_event"); raw != "" {
		return stableInfrastructureHash(raw)
	}
	return stableInfrastructureHash(payload)
}

func externalEventFresh(eventAt, now time.Time) bool {
	if eventAt.IsZero() {
		return true
	}
	age := now.Sub(eventAt)
	return age >= 0 && age <= defaultExternalEventFreshnessWindow
}

func externalFailureReason(data ExternalAdapterEventArtifactData, board *ClaimsBoard) string {
	if data.Source.Category != string(ParticipantCategoryExternal) {
		return "external source category invalid"
	}
	if data.Signature == "invalid" {
		return "external signature invalid"
	}
	if !data.Fresh {
		return "external event stale"
	}
	if data.ReferencedClaimID != "" && board != nil {
		if _, ok := board.CloneClaim(data.ReferencedClaimID); !ok {
			return "referenced claim not found"
		}
	}
	return ""
}

func boolArgDefault(args map[string]any, key string, fallback bool) bool {
	if _, ok := args[key]; !ok {
		return fallback
	}
	return boolArg(args, key)
}

func boolArg(args map[string]any, key string) bool {
	switch value := args[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func sortedMapKeys[T any](in map[string]T) []string {
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func infrastructureRecordKey(values ...string) string {
	return strings.Join(normalizeStringList(values), "|")
}

func claimPipelineID(claim *Claim) string {
	if claim == nil {
		return ""
	}
	return strings.TrimSpace(claim.PipelineID)
}

func claimIDForInfrastructure(claim *Claim) string {
	if claim == nil {
		return ""
	}
	return strings.TrimSpace(claim.ID)
}

func claimTaskID(claim *Claim) string {
	if claim == nil {
		return ""
	}
	return strings.TrimSpace(claim.TaskID)
}

func claimAgentID(claim *Claim) string {
	if claim == nil {
		return ""
	}
	return strings.TrimSpace(claim.AgentID)
}

func claimRelations(claim *Claim) []Relation {
	if claim == nil {
		return nil
	}
	return claim.Relations
}

func reqSessionID(req ServiceClaimRequest) string {
	if req.Board != nil {
		return req.Board.SessionID()
	}
	if req.Claim != nil {
		return req.Claim.SessionID
	}
	return ""
}
